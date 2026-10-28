package wallet

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	btcblockchain "github.com/btcsuite/btcd/blockchain"
	btcwire "github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/share/btclucky"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const (
	BTCLuckyAscendReason = "btc-lucky-ascend"
	BTCLuckyClaimReason  = "btc-lucky-claim"
)

var errNotBTCLuckyReward = errors.New("outpoint is not a BTC lucky coinbase reward")

type BTCLuckyReward struct {
	FundingUtxo   string `json:"fundingUtxo"`
	Amount        int64  `json:"amount"`
	Confirmations uint64 `json:"confirmations"`
	Maturity      uint64 `json:"maturity"`
	AnchorTxID    string `json:"anchorTxId,omitempty"`
	Status        string `json:"status"`
	BTCHeight     uint32 `json:"btcHeight"`
	BTCBlockHash  string `json:"btcBlockHash"`
	Error         string `json:"error,omitempty"`
}

func CheckBTCLuckyMaturity(confirmations, maturity uint64) error {
	if maturity == 0 || confirmations < maturity {
		return fmt.Errorf("BTC lucky reward is immature: %d/%d confirmations", confirmations, maturity)
	}
	return nil
}

// Fees are paid before the 85/10/5 split. The winner receives integer rounding.
func BTCLuckyRewardOutputs(value, fee int64, user, service, bootstrap []byte) ([]*swire.TxOut, error) {
	if fee < 0 || value <= fee || len(user) == 0 || len(service) == 0 || len(bootstrap) == 0 {
		return nil, fmt.Errorf("invalid BTC lucky reward amount or recipients")
	}
	value -= fee
	serviceValue, bootstrapValue := value/100*10+(value%100)*10/100, value/100*5+(value%100)*5/100
	amounts := []int64{value - serviceValue - bootstrapValue, serviceValue, bootstrapValue}
	var outputs []*swire.TxOut
	for i, script := range [][]byte{user, service, bootstrap} {
		if amounts[i] == 0 {
			continue
		}
		merged := false
		for _, output := range outputs {
			if bytes.Equal(output.PkScript, script) {
				output.Value += amounts[i]
				merged = true
				break
			}
		}
		if !merged {
			outputs = append(outputs, swire.NewTxOut(amounts[i], nil, script))
		}
	}
	return outputs, nil
}

func (p *Manager) BTCLuckyInfo() (*btclucky.InfoResponse, error) {
	return btclucky.NewHTTPTemplateBackend(p.l1IndexerBaseURL(), 10*time.Second).Info()
}

func (p *Manager) checkBTCLuckyRewardMaturity(reward *BTCLuckyReward) error {
	info, err := p.BTCLuckyInfo()
	if err != nil {
		return err
	}
	reward.Maturity = info.Service.CoinbaseMaturity
	return CheckBTCLuckyMaturity(reward.Confirmations, reward.Maturity)
}

func (p *Manager) btcLuckyAscendData(utxo string) (*sindexer.AscendData, error) {
	data, err := p.GetIndexerClient_SatsNet().GetAscendData(utxo)
	// The existing endpoint distinguishes absence in its explicit error message.
	// Transport errors must not be interpreted as permission to create a new anchor.
	if err != nil && err.Error() == "GetAscendData "+utxo+" not found" {
		return nil, nil
	}
	return data, err
}

func (p *Manager) checkBTCLuckyUnmanaged(utxo string, witness []byte, l2 bool) error {
	script, err := utils.WitnessScriptHash(witness)
	if err != nil {
		return err
	}
	address, err := GetP2WSHaddressFromScript(script)
	if err != nil {
		return err
	}
	if channel := p.GetChannel(address); channel != nil {
		channel.Mutex.RLock()
		controlled := len(channel.InControl([]string{utxo})) != 0
		if l2 {
			controlled = channel.UtxosInControl_SatsNet()[utxo]
		}
		channel.Mutex.RUnlock()
		if controlled {
			return fmt.Errorf("BTC lucky reward is managed by an existing channel")
		}
	}
	locker := p.utxoLockerL1
	if l2 {
		locker = p.utxoLockerL2
	}
	if locker != nil && locker.IsLocked(utxo) {
		return fmt.Errorf("BTC lucky reward UTXO is reserved")
	}
	return nil
}

// This proves the L1 source and current chain membership, without a maturity wait.
func (p *Manager) InspectBTCLuckyReward(utxo string, witness []byte) (*BTCLuckyReward, error) {
	return p.inspectBTCLuckyReward(utxo, witness, true)
}

// Resolve actual parent transactions for discovery, collateral and co-signing.
// Request-local parents are authenticated by their txid, never by declared amounts.
func (p *Manager) btcLuckySourceTransaction(utxo string, parents map[string]*btcwire.MsgTx) (*btcwire.MsgTx, uint32, error) {
	parts := strings.Split(utxo, ":")
	if len(parts) != 2 {
		return nil, 0, fmt.Errorf("invalid reward outpoint")
	}
	vout, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return nil, 0, err
	}
	if parts[0]+":"+strconv.FormatUint(vout, 10) != utxo {
		return nil, 0, fmt.Errorf("reward outpoint is not canonical")
	}
	tx := parents[parts[0]]
	if tx == nil {
		raw, err := p.GetIndexerClient().GetRawTx(parts[0])
		if err != nil {
			return nil, 0, err
		}
		tx, err = DecodeMsgTx(raw)
		if err != nil {
			return nil, 0, err
		}
	}
	if tx.TxID() != parts[0] || vout >= uint64(len(tx.TxOut)) {
		return nil, 0, fmt.Errorf("BTC lucky reward outpoint mismatch")
	}
	return tx, uint32(vout), nil
}

// Validate the original coinbase and its current block membership. Lucky
// backing stays at this outpoint, including after ascend and claim.
func (p *Manager) inspectBTCLuckyReward(utxo string, witness []byte, requireUnspent bool) (*BTCLuckyReward, error) {
	tx, vout, err := p.btcLuckySourceTransaction(utxo, nil)
	if err != nil {
		return nil, err
	}
	if !btcblockchain.IsCoinBaseTx(tx) || !bytes.Contains(tx.TxIn[0].SignatureScript, []byte("satoshinet")) {
		return nil, errNotBTCLuckyReward
	}
	if requireUnspent {
		if err := p.checkBTCLuckyUnmanaged(utxo, witness, false); err != nil {
			return nil, err
		}
	}
	script, err := utils.WitnessScriptHash(witness)
	if err != nil {
		return nil, err
	}
	output := tx.TxOut[vout]
	if output.Value <= 0 || !bytes.Equal(output.PkScript, script) {
		return nil, fmt.Errorf("BTC lucky reward script mismatch")
	}
	if requireUnspent {
		current, err := p.GetIndexerClient().GetTxOutput(utxo)
		if err != nil {
			return nil, err
		}
		if current == nil || current.Value() != output.Value || !bytes.Equal(current.OutValue.PkScript, script) {
			return nil, fmt.Errorf("BTC lucky reward UTXO mismatch")
		}
	}

	info, err := p.GetIndexerClient().GetTxInfo(tx.TxID())
	if err != nil {
		return nil, err
	}
	if info == nil || info.Confirmations == 0 || info.BlockHeight < 1 {
		return nil, fmt.Errorf("BTC lucky reward is not confirmed on L1")
	}
	if uint64(info.BlockHeight) > math.MaxUint32 {
		return nil, fmt.Errorf("BTC lucky source height is out of range")
	}
	blockHash, err := p.GetIndexerClient().GetBlockHash(int(info.BlockHeight))
	if err != nil {
		return nil, err
	}
	hash, err := chainhash.NewHashFromStr(blockHash)
	if err != nil || *hash == (chainhash.Hash{}) {
		return nil, fmt.Errorf("invalid BTC lucky source block hash")
	}
	return &BTCLuckyReward{FundingUtxo: utxo, Amount: output.Value, Confirmations: info.Confirmations, Status: "unascended", BTCHeight: uint32(info.BlockHeight), BTCBlockHash: hash.String()}, nil
}

// ValidateBTCLuckyAnchor is shared by Lucky and ordinary signing entry points.
// A marker is a signed assertion about verified L1 facts, never a substitute
// for source validation. Both ascend and claim require a mature L1 coinbase.
func (p *Manager) ValidateBTCLuckyAnchor(tx *swire.MsgTx, witness []byte) (*BTCLuckyReward, error) {
	return p.validateBTCLuckyAnchor(tx, witness, true)
}

func (p *Manager) validateBTCLuckyAnchor(tx *swire.MsgTx, witness []byte, requireUnspent bool) (*BTCLuckyReward, error) {
	if tx == nil || len(tx.TxIn) != 1 || tx.TxIn[0] == nil || tx.TxIn[0].PreviousOutPoint != (swire.OutPoint{Index: swire.AnchorTxOutIndex}) {
		return nil, fmt.Errorf("BTC lucky metadata requires an anchor input")
	}
	metadata, err := sindexer.BTCLuckyMetadataFromOutputs(tx.TxOut)
	if err != nil {
		return nil, err
	}
	if metadata == nil {
		return nil, fmt.Errorf("BTC lucky anchor is missing its marker")
	}
	data, err := ParseStandardAnchorScript(tx.TxIn[0].SignatureScript)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(data.WitnessScript, witness) || len(data.Assets) != 0 {
		return nil, fmt.Errorf("BTC lucky anchor witness or assets mismatch")
	}
	reward, err := p.inspectBTCLuckyReward(data.Utxo, witness, requireUnspent)
	if err != nil {
		return nil, err
	}
	if metadata.BlockHeight != reward.BTCHeight || metadata.BlockHash.String() != reward.BTCBlockHash {
		return nil, fmt.Errorf("BTC lucky marker does not match the current L1 block")
	}
	if err := p.checkBTCLuckyRewardMaturity(reward); err != nil {
		return nil, err
	}
	script, err := utils.WitnessScriptHash(witness)
	if err != nil {
		return nil, err
	}
	if data.Value != reward.Amount || len(tx.TxOut) == 0 || tx.TxOut[0] == nil || tx.TxOut[0].Value != reward.Amount ||
		len(tx.TxOut[0].Assets) != 0 || !bytes.Equal(tx.TxOut[0].PkScript, script) {
		return nil, fmt.Errorf("BTC lucky anchor must map the entire reward to output zero")
	}
	for _, output := range tx.TxOut[1:] {
		if output.Value != 0 || len(output.Assets) != 0 || !sindexer.IsOpReturn(output.PkScript) {
			return nil, fmt.Errorf("invalid BTC lucky anchor output")
		}
	}
	return reward, nil
}

func (p *Manager) btcLuckyWitness() ([]byte, []byte, string, error) {
	address, err := p.btcLuckyRewardAddress(nil)
	if err != nil {
		return nil, nil, "", err
	}
	witness, peer, err := p.channelWitness(p.wallet, address)
	return witness, peer, address, err
}

func (p *Manager) GetBTCLuckyRewards() ([]*BTCLuckyReward, error) {
	if p == nil || p.wallet == nil {
		return nil, fmt.Errorf("wallet is not initialized")
	}
	witness, _, address, err := p.btcLuckyWitness()
	if err != nil {
		return nil, err
	}
	info, err := p.BTCLuckyInfo()
	if err != nil {
		return nil, err
	}
	return p.getBTCLuckyRewards(witness, address, info.Service.CoinbaseMaturity)
}

func (p *Manager) getBTCLuckyRewards(witness []byte, address string, maturity uint64) ([]*BTCLuckyReward, error) {
	rewards := make([]*BTCLuckyReward, 0)
	outputs, err := p.GetIndexerClient().GetUtxosWithAddress(address)
	if err != nil {
		return nil, err
	}
	ledger, err := p.GetIndexerClient_SatsNet().GetChannelLedger(address)
	if err != nil {
		return nil, err
	}
	sources := make(map[string]bool)
	sourceErrors := make(map[string]error)
	for point := range outputs {
		sources[point] = true
	}
	// Reuse the chain ledger to report mapped rewards even when their original
	// backing disappeared. Such a reward must report an error, never claimable.
	for _, entry := range ledger {
		if entry == nil || entry.ChannelId != address || entry.Direction != "ascending" {
			continue
		}
		for _, source := range entry.L1Outpoints {
			anchor, err := p.btcLuckyAscendData(source)
			if err != nil {
				sources[source] = true
				sourceErrors[source] = err
				continue
			}
			if anchor != nil && anchor.BTCLucky != nil {
				sources[source] = true
			}
		}
	}
	for utxo, requireUnspent := range sources {
		var reward *BTCLuckyReward
		err := sourceErrors[utxo]
		if err == nil {
			reward, err = p.inspectBTCLuckyReward(utxo, witness, requireUnspent)
		}
		if errors.Is(err, errNotBTCLuckyReward) {
			continue
		}
		if reward == nil {
			reward = &BTCLuckyReward{FundingUtxo: utxo, Maturity: maturity}
		}
		if err == nil {
			err = p.setBTCLuckyRewardStatus(reward, maturity)
		}
		if err != nil {
			reward.Status, reward.Error = "error", err.Error()
		}
		rewards = append(rewards, reward)
	}
	return rewards, nil
}

func (p *Manager) setBTCLuckyRewardStatus(reward *BTCLuckyReward, maturity uint64) error {
	reward.Maturity = maturity
	reward.Status = "immature"
	if CheckBTCLuckyMaturity(reward.Confirmations, maturity) == nil {
		reward.Status = "unascended"
	}
	anchor, err := p.btcLuckyAscendData(reward.FundingUtxo)
	if err != nil || anchor == nil {
		return err
	}
	reward.AnchorTxID = anchor.AnchorTxId
	if anchor.BTCLucky == nil || anchor.BTCLucky.BlockHeight != reward.BTCHeight || anchor.BTCLucky.BlockHash.String() != reward.BTCBlockHash {
		return fmt.Errorf("BTC lucky anchor does not match the current L1 source")
	}
	unspent, err := p.GetIndexerClient_SatsNet().GetExistingUtxos([]string{anchor.AnchorTxId + ":0"})
	if err != nil {
		return err
	}
	if len(unspent) == 0 {
		reward.Status = "spent"
	} else if reward.Status == "unascended" {
		reward.Status = "claimable"
	}
	return nil
}

func (p *Manager) AscendBTCLuckyReward(utxo string) (string, error) {
	witness, peerKey, address, err := p.btcLuckyWitness()
	if err != nil {
		return "", err
	}
	reward, err := p.InspectBTCLuckyReward(utxo, witness)
	if err != nil {
		return "", err
	}
	if err := p.checkBTCLuckyRewardMaturity(reward); err != nil {
		return "", err
	}
	anchor, err := p.btcLuckyAscendData(utxo)
	if err != nil {
		return "", err
	}
	if anchor != nil {
		return anchor.AnchorTxId, nil
	}
	output := indexer.NewTxOutput(reward.Amount)
	output.OutPointStr = utxo
	ticker := p.getTickerInfo(&indexer.ASSET_PLAIN_SAT)
	if ticker == nil {
		return "", fmt.Errorf("plain BTC ticker is unavailable")
	}
	blockHash, err := chainhash.NewHashFromStr(reward.BTCBlockHash)
	if err != nil {
		return "", err
	}
	marker, err := (sindexer.BTCLuckyMetadata{Version: sindexer.BTCLuckyVersion, BlockHeight: reward.BTCHeight, BlockHash: *blockHash}).Script()
	if err != nil {
		return "", err
	}
	tx, err := CreateAnchorTx(output, GetAssetName(ticker), ticker, witness, nil, marker, false)
	if err != nil {
		return "", err
	}
	if _, err := p.ValidateBTCLuckyAnchor(tx, witness); err != nil {
		return "", err
	}
	if _, err := p.SignAnchorTx(tx, p.wallet.SignMessage); err != nil {
		return "", err
	}
	sigs, err := p.requestBTCLuckySignature(BTCLuckyAscendReason, address, peerKey, witness, tx, nil)
	if err != nil {
		return "", err
	}
	if len(sigs) != 1 || len(sigs[0]) != 1 {
		return "", fmt.Errorf("invalid BTC lucky anchor signature")
	}
	data, err := ParseStandardAnchorScript(tx.TxIn[0].SignatureScript)
	if err != nil {
		return "", err
	}
	tx.TxIn[0].SignatureScript, err = sindexer.StandardAnchorScriptWithSig(utxo, witness, data.Value, data.Assets, sigs[0][0])
	if err != nil {
		return "", err
	}
	// A reorg or confirmation rollback while waiting for the peer invalidates
	// this attempt even if the earlier signature was accepted.
	if _, err := p.ValidateBTCLuckyAnchor(tx, witness); err != nil {
		return "", err
	}
	return p.BroadcastTx_SatsNet(tx)
}

func btcLuckyRecipientScripts(user, service []byte) ([][]byte, error) {
	bootstrap, err := hex.DecodeString(indexer.GetBootstrapPubKey())
	if err != nil {
		return nil, err
	}
	// The bootstrap co-signs core rewards. A core using its own indexer is
	// both the 85% miner and the 10% data provider; bootstrap receives only 5%.
	if bytes.Equal(service, bootstrap) {
		service = user
	}
	var scripts [][]byte
	for _, raw := range [][]byte{user, service, bootstrap} {
		key, err := utils.BytesToPublicKey(raw)
		if err != nil {
			return nil, err
		}
		script, err := GetP2TRpkScript(key)
		if err != nil {
			return nil, err
		}
		scripts = append(scripts, script)
	}
	return scripts, nil
}

func (p *Manager) ValidateBTCLuckyClaim(tx *swire.MsgTx, witness, user, service []byte) (stxscript.PrevOutputFetcher, error) {
	if tx == nil || len(tx.TxIn) != 1 {
		return nil, fmt.Errorf("BTC lucky claim requires exactly one reward input")
	}
	input := tx.TxIn[0].PreviousOutPoint
	if err := p.checkBTCLuckyUnmanaged(input.String(), witness, true); err != nil {
		return nil, err
	}
	if input.Index != 0 {
		return nil, fmt.Errorf("BTC lucky claim requires the anchor reward output")
	}
	raw, err := p.GetIndexerClient_SatsNet().GetRawTx(input.Hash.String())
	if err != nil {
		return nil, err
	}
	anchor, err := DecodeMsgTx_SatsNet(raw)
	if err != nil {
		return nil, err
	}
	if anchor.TxID() != input.Hash.String() || len(anchor.TxIn) != 1 || len(anchor.TxOut) == 0 ||
		anchor.TxIn[0].PreviousOutPoint != (swire.OutPoint{Index: swire.AnchorTxOutIndex}) {
		return nil, fmt.Errorf("BTC lucky claim input is not an anchor")
	}
	data, err := ParseStandardAnchorScript(anchor.TxIn[0].SignatureScript)
	if err != nil {
		return nil, err
	}
	reward, err := p.validateBTCLuckyAnchor(anchor, witness, true)
	if err != nil {
		return nil, err
	}
	ascend, err := p.GetIndexerClient_SatsNet().GetAscendData(data.Utxo)
	if err != nil {
		return nil, err
	}
	if ascend == nil || ascend.AnchorTxId != anchor.TxID() || data.Value != reward.Amount {
		return nil, fmt.Errorf("BTC lucky anchor provenance mismatch")
	}
	previous, err := p.GetIndexerClient_SatsNet().GetTxOutput(input.String())
	if err != nil {
		return nil, err
	}
	script, err := utils.WitnessScriptHash(witness)
	if err != nil {
		return nil, err
	}
	if previous == nil || previous.Value() != reward.Amount || !bytes.Equal(previous.OutValue.PkScript, script) || len(previous.Assets) != 0 {
		return nil, fmt.Errorf("BTC lucky anchor output mismatch")
	}
	if hex.EncodeToString(service) == indexer.GetBootstrapPubKey() {
		core, err := p.GetIndexerClient_SatsNet().IsCoreNode(user)
		if err != nil {
			return nil, err
		}
		if !core {
			return nil, fmt.Errorf("bootstrap may co-sign only core BTC lucky rewards")
		}
	}
	scripts, err := btcLuckyRecipientScripts(user, service)
	if err != nil {
		return nil, err
	}
	expected, err := BTCLuckyRewardOutputs(reward.Amount, DEFAULT_FEE_SATSNET, scripts[0], scripts[1], scripts[2])
	if err != nil {
		return nil, err
	}
	if len(tx.TxOut) != len(expected) {
		return nil, fmt.Errorf("invalid BTC lucky claim output count")
	}
	for i, output := range expected {
		if tx.TxOut[i].Value != output.Value || !bytes.Equal(tx.TxOut[i].PkScript, output.PkScript) || len(tx.TxOut[i].Assets) != 0 {
			return nil, fmt.Errorf("invalid BTC lucky claim distribution")
		}
	}
	fetcher := stxscript.NewMultiPrevOutFetcher(nil)
	converted := OutputToSatsNet(previous)
	fetcher.AddPrevOut(input, &converted.OutValue)
	return fetcher, nil
}

func (p *Manager) ClaimBTCLuckyReward(utxo string) (string, error) {
	witness, peerKey, address, err := p.btcLuckyWitness()
	if err != nil {
		return "", err
	}
	anchor, err := p.GetIndexerClient_SatsNet().GetAscendData(utxo)
	if err != nil {
		return "", err
	}
	if anchor == nil {
		return "", fmt.Errorf("BTC lucky reward has not ascended")
	}
	hash, err := swire.NewOutPointFromString(anchor.AnchorTxId + ":0")
	if err != nil {
		return "", err
	}
	tx := swire.NewMsgTx(swire.TxVersion)
	tx.AddTxIn(swire.NewTxIn(hash, nil, nil))
	scripts, err := btcLuckyRecipientScripts(p.wallet.GetPubKey().SerializeCompressed(), peerKey)
	if err != nil {
		return "", err
	}
	tx.TxOut, err = BTCLuckyRewardOutputs(anchor.Value, DEFAULT_FEE_SATSNET, scripts[0], scripts[1], scripts[2])
	if err != nil {
		return "", err
	}
	prev, err := p.ValidateBTCLuckyClaim(tx, witness, p.wallet.GetPubKey().SerializeCompressed(), peerKey)
	if err != nil {
		return "", err
	}
	sigs, err := PartialSignTxWithWallet_SatsNet(p.wallet, tx, prev, witness, peerKey)
	if err != nil {
		return "", err
	}
	remote, err := p.requestBTCLuckySignature(BTCLuckyClaimReason, address, peerKey, witness, tx, sigs)
	if err != nil {
		return "", err
	}
	if len(remote) != 1 {
		return "", fmt.Errorf("invalid BTC lucky claim signature count")
	}
	// The peer round trip can span a reorg or a change of channel backing.
	// Refresh both policy checks and prevouts before completing and broadcasting.
	prev, err = p.ValidateBTCLuckyClaim(tx, witness, p.wallet.GetPubKey().SerializeCompressed(), peerKey)
	if err != nil {
		return "", err
	}
	if _, err = FinalSignTxWithWallet_SatsNet(p.wallet, tx, prev, witness, peerKey, remote[0]); err != nil {
		return "", err
	}
	if err = VerifySignedTx_SatsNet(tx, prev); err != nil {
		return "", err
	}
	return p.BroadcastTx_SatsNet(tx)
}

func (p *Manager) requestBTCLuckySignature(reason, address string, peerKey, witness []byte, tx *swire.MsgTx, localSigs [][]byte) ([][][]byte, error) {
	peer, err := p.configuredSigningPeer(peerKey)
	if err != nil {
		return nil, err
	}
	raw, err := EncodeMsgTx_SatsNet(tx)
	if err != nil {
		return nil, err
	}
	more, err := json.Marshal(wwire.RemoteSignMoreData{Action: reason, Witness: witness, Tx: []*wwire.TxSignInfo{{Tx: raw, LocalSigs: localSigs}}})
	if err != nil {
		return nil, err
	}
	req := wwire.SignRequest{MsgHeader: wwire.NewMsgHeader(), ChannelId: address, CommitHeight: -1, Reason: reason, MoreData: more, PubKey: p.wallet.GetPubKey().SerializeCompressed(), NodeId: p.wallet.GetNodePubKey().SerializeCompressed()}
	encoded, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	sig, err := p.wallet.SignMessageWithIndex(encoded, 0)
	if err != nil {
		return nil, err
	}
	return peer.client.SendSigReq(&req, sig)
}

// ValidateBTCLuckyAnchorSource prevents a real Lucky coinbase from being
// mapped through an ordinary anchor after its required marker is omitted.
// An ordinary Bitcoin transaction returns nil, nil; lookup failures reject.
func (p *Manager) ValidateBTCLuckyAnchorSource(tx *swire.MsgTx, witness []byte) (*BTCLuckyReward, error) {
	if tx == nil || len(tx.TxIn) != 1 {
		return nil, fmt.Errorf("invalid anchor source")
	}
	marker, err := sindexer.BTCLuckyMetadataFromOutputs(tx.TxOut)
	if err != nil {
		return nil, err
	}
	if marker != nil {
		return p.ValidateBTCLuckyAnchor(tx, witness)
	}
	data, err := ParseStandardAnchorScript(tx.TxIn[0].SignatureScript)
	if err != nil {
		return nil, err
	}
	// This branch only classifies the source before rejecting a missing marker.
	// Managed expansion callers may already hold the channel's write lock.
	_, err = p.inspectBTCLuckyReward(data.Utxo, witness, false)
	if errors.Is(err, errNotBTCLuckyReward) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("BTC lucky anchor is missing its marker")
}

var errBTCLuckyChannelInput = errors.New("BTC lucky reward cannot enter channel before btc-lucky-claim; original L1 backing remains isolated")

// Channel admission may hold the channel lock. Only inspect the source here;
// the dedicated ascend/claim paths retain their unmanaged and unspent checks.
func (p *Manager) rejectBTCLuckyChannelFunding(utxo string, witness []byte) error {
	_, err := p.inspectBTCLuckyReward(utxo, witness, false)
	if errors.Is(err, errNotBTCLuckyReward) {
		return nil
	}
	if err != nil {
		return err
	}
	return errBTCLuckyChannelInput
}

// Lucky L1 backing remains outside ordinary channel operations after ascend
// and claim. Its provenance is immutable; an indexed anchor never releases it.
func (p *Manager) isBTCLuckyL1Output(point string, parents map[string]*btcwire.MsgTx) (bool, error) {
	parent, _, err := p.btcLuckySourceTransaction(point, parents)
	if err != nil {
		return false, err
	}
	return btcblockchain.IsCoinBaseTx(parent) && bytes.Contains(parent.TxIn[0].SignatureScript, []byte("satoshinet")), nil
}

// Exclude original backing before ordinary L1 coin selection. Reuse the
// caller's transient exclusion map; no lock or balance record is persisted.
func (p *Manager) excludeBTCLuckyL1Outputs(address string, excluded map[string]bool) error {
	outputs, err := p.GetIndexerRPCClient().GetUtxosWithAddress(address)
	if err != nil {
		return err
	}
	for point := range outputs {
		if excluded[point] {
			continue
		}
		lucky, err := p.isBTCLuckyL1Output(point, nil)
		if err != nil {
			return err
		}
		if lucky {
			excluded[point] = true
		}
	}
	return nil
}

// Exclude unclaimed Lucky anchors from ordinary L2 spending, including fees.
// Claim outputs have no Lucky marker and remain ordinary spendable assets.
func (p *Manager) excludeBTCLuckyL2Outputs(address string, excluded map[string]bool) error {
	outputs, err := p.GetIndexerRPCClient_SatsNet().GetUtxosWithAddress(address)
	if err != nil {
		return err
	}
	for point := range outputs {
		if excluded[point] {
			continue
		}
		err := p.rejectBTCLuckyOutput(point, nil)
		if errors.Is(err, errBTCLuckyChannelInput) {
			excluded[point] = true
		} else if err != nil {
			return err
		}
	}
	return nil
}

func (p *Manager) RejectBTCLuckyL1Inputs(tx *btcwire.MsgTx, parents map[string]*btcwire.MsgTx) error {
	if tx == nil {
		return fmt.Errorf("missing L1 signing transaction")
	}
	for _, input := range tx.TxIn {
		if input == nil {
			return fmt.Errorf("nil L1 signing input")
		}
		lucky, err := p.isBTCLuckyL1Output(input.PreviousOutPoint.String(), parents)
		if err != nil {
			return fmt.Errorf("inspect L1 signing source: %w", err)
		}
		if lucky {
			return fmt.Errorf("BTC lucky L1 backing cannot enter ordinary channel operations")
		}
	}
	return nil
}

// RejectBTCLuckyInputs protects generic L2 co-signing policies, including old
// unstake/profit and mixed inputs. Lucky rewards must use the dedicated claim
// policy, which checks Bitcoin maturity and the 85/10/5 recipients.
// Request-local parents may be unconfirmed; their txids still bind the facts.
func (p *Manager) RejectBTCLuckyInputs(tx *swire.MsgTx, parents map[string]*swire.MsgTx) error {
	if tx == nil {
		return fmt.Errorf("missing L2 signing transaction")
	}
	for _, input := range tx.TxIn {
		if input == nil {
			return fmt.Errorf("nil L2 signing input")
		}
		if err := p.rejectBTCLuckyOutput(input.PreviousOutPoint.String(), parents); err != nil {
			return err
		}
	}
	return nil
}

// Reuse the same parent/source classification for co-signing and for outputs
// installed directly in a channel without passing through the generic signer.
func (p *Manager) rejectBTCLuckyOutput(utxo string, parents map[string]*swire.MsgTx) error {
	point, err := swire.NewOutPointFromString(utxo)
	if err != nil {
		return err
	}
	if *point == (swire.OutPoint{Index: swire.AnchorTxOutIndex}) {
		return nil
	}
	id := point.Hash.String()
	parent := parents[id]
	if parent == nil {
		raw, err := p.GetIndexerClient_SatsNet().GetRawTx(id)
		if err != nil {
			return fmt.Errorf("inspect L2 signing source: %w", err)
		}
		parent, err = DecodeMsgTx_SatsNet(raw)
		if err != nil {
			return err
		}
	}
	if parent.TxID() != id || uint64(point.Index) >= uint64(len(parent.TxOut)) {
		return fmt.Errorf("L2 signing source mismatch")
	}
	marker, err := sindexer.BTCLuckyMetadataFromOutputs(parent.TxOut)
	if err != nil {
		return err
	}
	if marker != nil {
		return errBTCLuckyChannelInput
	}
	if len(parent.TxIn) != 1 || parent.TxIn[0].PreviousOutPoint != (swire.OutPoint{Index: swire.AnchorTxOutIndex}) {
		return nil
	}
	data, err := ParseStandardAnchorScript(parent.TxIn[0].SignatureScript)
	if err != nil {
		return err
	}
	// A missing marker cannot turn the original Lucky coinbase into ordinary funding.
	return p.rejectBTCLuckyChannelFunding(data.Utxo, data.WitnessScript)
}

// Keep the indexer's account-based balances and remove only the Lucky UTXOs
// excluded by ordinary coin selection. This is a request-local view, not a ledger.
func (p *Manager) channelAssetSummaryWithoutLucky(address string, l2 bool) ([]*indexer.AssetInfo, error) {
	rpc := p.GetIndexerRPCClient()
	excluded := make(map[string]bool)
	var err error
	if l2 {
		rpc = p.GetIndexerRPCClient_SatsNet()
		err = p.excludeBTCLuckyL2Outputs(address, excluded)
	} else {
		err = p.excludeBTCLuckyL1Outputs(address, excluded)
	}
	if err != nil {
		return nil, err
	}
	summary := rpc.GetAssetSummaryWithAddress(address)
	if summary == nil {
		return nil, fmt.Errorf("can't get channel asset summary %s", address)
	}
	assets := make([]*indexer.AssetInfo, 0, len(summary.Data))
	for _, asset := range summary.Data {
		if asset == nil {
			continue
		}
		assets = append(assets, asset.Clone())
	}
	for point := range excluded {
		output, err := rpc.GetTxOutput(point)
		if err != nil {
			return nil, err
		}
		if output == nil {
			return nil, fmt.Errorf("missing Lucky output %s", point)
		}
		for _, asset := range assets {
			amount := output.GetAsset(&asset.Name)
			if asset.Name == indexer.ASSET_ALL_SAT {
				amount = indexer.NewDefaultDecimal(output.Value())
			} else if !l2 && asset.Name == indexer.ASSET_PLAIN_SAT {
				// L1 address summaries count only entirely plain UTXOs.
				amount = nil
				if !output.HasAsset() {
					amount = indexer.NewDefaultDecimal(output.Value())
				}
			}
			remaining := asset.Amount.SubAlignPrecision(amount)
			if remaining.Sign() < 0 {
				return nil, fmt.Errorf("Lucky output exceeds channel summary for %s", asset.Name.String())
			}
			asset.Amount = *remaining
		}
	}
	return assets, nil
}

func (p *Manager) channelAssetBalanceWithoutLucky(address string, name *indexer.AssetName, l2 bool) (*Decimal, error) {
	assets, err := p.channelAssetSummaryWithoutLucky(address, l2)
	if err != nil {
		return nil, err
	}
	for _, asset := range assets {
		if asset.Name == *name {
			return asset.Amount.Clone(), nil
		}
	}
	return indexer.NewDefaultDecimal(0), nil
}
