package wallet

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	btcwire "github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/indexer/share/btclucky"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	sch "github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

type luckyRewardRPC struct {
	IndexerRPCClient
	raw           string
	output        *TxOutput
	confirmations uint64
	ascend        *sindexer.AscendData
	spent         bool
	backing       *TxOutput
	blockHash     string
	rawByID       map[string]string
	rawErr        error
	outputs       map[string]*TxOutput
	ascends       map[string]*sindexer.AscendData
	ascendErrs    map[string]error
	ledger        []*sindexer.ChannelLedgerEntry
	ledgerErr     error
}

func (r *luckyRewardRPC) GetBlockHash(int) (string, error) {
	if r.blockHash != "" {
		return r.blockHash, nil
	}
	return fmt.Sprintf("%064x", 1), nil
}

func (r *luckyRewardRPC) GetRawTx(id string) (string, error) {
	if r.rawErr != nil {
		return "", r.rawErr
	}
	if r.rawByID != nil {
		if raw, ok := r.rawByID[id]; ok {
			return raw, nil
		}
		return "", fmt.Errorf("unknown transaction")
	}
	return r.raw, nil
}
func (r *luckyRewardRPC) GetTxOutput(point string) (*TxOutput, error) {
	if r.outputs != nil {
		if output := r.outputs[point]; output != nil {
			return output, nil
		}
		return nil, fmt.Errorf("unknown output %s", point)
	}
	if r.backing != nil && point == r.backing.OutPointStr {
		return r.backing, nil
	}
	if r.spent {
		return nil, fmt.Errorf("output already spent")
	}
	return r.output, nil
}
func (r *luckyRewardRPC) GetTxInfo(string) (*indexerwire.TxSimpleInfo, error) {
	return &indexerwire.TxSimpleInfo{Confirmations: r.confirmations, BlockHeight: 1}, nil
}
func (r *luckyRewardRPC) GetAscendData(point string) (*sindexer.AscendData, error) {
	if err := r.ascendErrs[point]; err != nil {
		return nil, err
	}
	if r.ascends != nil {
		return r.ascends[point], nil
	}
	return r.ascend, nil
}
func (r *luckyRewardRPC) GetExistingUtxos(points []string) ([]string, error) {
	if r.spent {
		return nil, nil
	}
	return points, nil
}
func (r *luckyRewardRPC) GetSyncHeight() int   { return 10 }
func (r *luckyRewardRPC) GetBestHeight() int64 { return 10 }
func (r *luckyRewardRPC) GetUtxosWithAddress(string) (map[string]*btcwire.TxOut, error) {
	if r.outputs != nil {
		result := make(map[string]*btcwire.TxOut)
		for point, out := range r.outputs {
			result[point] = &out.OutValue
		}
		return result, nil
	}
	if r.backing != nil {
		return map[string]*btcwire.TxOut{r.backing.OutPointStr: &r.backing.OutValue}, nil
	}
	if r.spent {
		return map[string]*btcwire.TxOut{}, nil
	}
	return map[string]*btcwire.TxOut{r.output.OutPointStr: &r.output.OutValue}, nil
}
func (r *luckyRewardRPC) GetChannelLedger(channel string) ([]*sindexer.ChannelLedgerEntry, error) {
	if r.ledgerErr != nil || r.ledger != nil {
		return r.ledger, r.ledgerErr
	}
	if r.ascend == nil {
		return nil, nil
	}
	return []*sindexer.ChannelLedgerEntry{{ChannelId: channel, Direction: "ascending", L2TxId: r.ascend.AnchorTxId, L1Outpoints: []string{r.ascend.FundingUtxo}, L2Height: 1, Value: r.ascend.Value, Assets: r.ascend.Assets}}, nil
}

func (r *luckyRewardRPC) GetChannelStateEvents(string) ([]*sindexer.ChannelStateEvent, error) {
	return nil, nil
}

func TestBTCLuckyClaimChecksSourceMaturityAndDistribution(t *testing.T) {
	userKey, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{1}, 32))
	serviceKey, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{2}, 32))
	user, service := userKey.PubKey().SerializeCompressed(), serviceKey.PubKey().SerializeCompressed()
	witness, err := utils.GenMultiSigScript(user, service)
	require.NoError(t, err)
	script, err := utils.WitnessScriptHash(witness)
	require.NoError(t, err)
	coinbase := btcwire.NewMsgTx(2)
	coinbase.AddTxIn(btcwire.NewTxIn(&btcwire.OutPoint{Index: 0xffffffff}, []byte("height1-satoshinet"), nil))
	coinbase.AddTxOut(btcwire.NewTxOut(5_000_000_000, script))
	var encoded bytes.Buffer
	require.NoError(t, coinbase.Serialize(&encoded))
	utxo := coinbase.TxID() + ":0"
	source := &luckyRewardRPC{raw: hex.EncodeToString(encoded.Bytes()), confirmations: 1, output: indexer.NewTxOutput(5_000_000_000)}
	source.output.OutValue.PkScript = script
	source.output.OutPointStr = utxo
	anchor := swire.NewMsgTx(2)
	invoice, err := sindexer.StandardAnchorScriptWithSig(utxo, witness, 5_000_000_000, nil, nil)
	require.NoError(t, err)
	anchor.AddTxIn(&swire.TxIn{PreviousOutPoint: swire.OutPoint{Index: swire.AnchorTxOutIndex}, SignatureScript: invoice})
	anchor.AddTxOut(swire.NewTxOut(5_000_000_000, nil, script))
	blockHash, err := sch.NewHashFromStr(fmt.Sprintf("%064x", 1))
	require.NoError(t, err)
	metadata := sindexer.BTCLuckyMetadata{Version: sindexer.BTCLuckyVersion, BlockHeight: 1, BlockHash: *blockHash}
	marker, err := metadata.Script()
	require.NoError(t, err)
	anchor.AddTxOut(swire.NewTxOut(0, nil, marker))
	anchorRaw, err := EncodeMsgTx_SatsNet(anchor)
	require.NoError(t, err)
	l2 := &luckyRewardRPC{raw: anchorRaw, output: source.output, ascend: &sindexer.AscendData{FundingUtxo: utxo, AnchorTxId: anchor.TxID(), Value: 5_000_000_000}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(btclucky.APIResponse[*btclucky.InfoResponse]{Data: &btclucky.InfoResponse{Service: btclucky.TemplateServiceStatus{CoinbaseMaturity: 10}}})
	}))
	defer server.Close()
	manager := &Manager{cfg: &common.Config{IndexerL1: &common.Indexer{Scheme: "http", Host: server.Listener.Addr().String()}}, l1IndexerClient: &IndexerRPCClientMgr{active: source}, l2IndexerClient: &IndexerRPCClientMgr{active: l2}}
	// Discovery can show an immature reward, but neither ascend entry may sign it.
	reward, err := manager.InspectBTCLuckyReward(utxo, witness)
	require.NoError(t, err)
	require.EqualValues(t, 1, reward.Confirmations)
	_, err = manager.ValidateBTCLuckyAnchor(anchor, witness)
	require.ErrorContains(t, err, "immature")
	_, err = manager.ValidateBTCLuckyAnchorSource(anchor, witness)
	require.ErrorContains(t, err, "immature")
	source.confirmations = 10
	_, err = manager.ValidateBTCLuckyAnchor(anchor, witness)
	require.NoError(t, err)
	// A confirmation rollback must be checked afresh rather than cached.
	source.confirmations = 9
	_, err = manager.ValidateBTCLuckyAnchorSource(anchor, witness)
	require.ErrorContains(t, err, "immature")
	source.confirmations = 10
	source.blockHash = fmt.Sprintf("%064x", 2)
	_, err = manager.ValidateBTCLuckyAnchor(anchor, witness)
	require.ErrorContains(t, err, "current L1 block")
	source.blockHash = ""
	anchor.TxOut = anchor.TxOut[:1]
	_, err = manager.ValidateBTCLuckyAnchorSource(anchor, witness)
	require.ErrorContains(t, err, "missing its marker")
	_, err = manager.ValidateBTCLuckyAnchor(anchor, witness)
	require.ErrorContains(t, err, "missing its marker")
	anchor.AddTxOut(swire.NewTxOut(0, nil, marker))
	anchor.TxOut[0].Value--
	_, err = manager.ValidateBTCLuckyAnchor(anchor, witness)
	require.ErrorContains(t, err, "entire reward")
	anchor.TxOut[0].Value++
	hash, err := sch.NewHashFromStr(anchor.TxID())
	require.NoError(t, err)
	claim := swire.NewMsgTx(2)
	claim.AddTxIn(swire.NewTxIn(swire.NewOutPoint(hash, 0), nil, nil))
	scripts, err := btcLuckyRecipientScripts(user, service)
	require.NoError(t, err)
	claim.TxOut, err = BTCLuckyRewardOutputs(reward.Amount, DEFAULT_FEE_SATSNET, scripts[0], scripts[1], scripts[2])
	require.NoError(t, err)
	for _, confirmations := range []uint64{1, 9} {
		source.confirmations = confirmations
		_, err = manager.ValidateBTCLuckyClaim(claim, witness, user, service)
		require.ErrorContains(t, err, "immature")
	}
	source.confirmations = 10
	_, err = manager.ValidateBTCLuckyClaim(claim, witness, user, service)
	require.NoError(t, err)
	claim.TxOut[0].Value++
	claim.TxOut[1].Value--
	_, err = manager.ValidateBTCLuckyClaim(claim, witness, user, service)
	require.ErrorContains(t, err, "distribution")
	claim.TxOut[0].Value--
	claim.TxOut[1].Value++
	l2.spent = true
	_, err = manager.ValidateBTCLuckyClaim(claim, witness, user, service)
	require.ErrorContains(t, err, "spent")
	l2.spent = false
	source.confirmations = 0
	_, err = manager.ValidateBTCLuckyClaim(claim, witness, user, service)
	require.ErrorContains(t, err, "not confirmed")
	source.confirmations = 10
	l2.ascend.AnchorTxId = "another-anchor"
	_, err = manager.ValidateBTCLuckyClaim(claim, witness, user, service)
	require.ErrorContains(t, err, "provenance")
	l2.ascend.AnchorTxId = anchor.TxID()
	// A replacement backing output cannot make a spent Lucky source claimable.
	source.spent = true
	_, err = manager.ValidateBTCLuckyClaim(claim, witness, user, service)
	require.ErrorContains(t, err, "spent")
	backing := btcwire.NewMsgTx(2)
	backing.AddTxIn(btcwire.NewTxIn(source.output.OutPoint(), nil, nil))
	backing.AddTxOut(btcwire.NewTxOut(source.output.Value(), script))
	backingRaw, err := EncodeMsgTx(backing)
	require.NoError(t, err)
	source.backing = indexer.GenerateTxOutput(backing, 0)
	source.rawByID = map[string]string{coinbase.TxID(): source.raw, backing.TxID(): backingRaw}
	_, err = manager.ValidateBTCLuckyClaim(claim, witness, user, service)
	require.ErrorContains(t, err, "spent")
	// Restore the original source output before checking maturity independently.
	source.spent = false
	source.backing = nil
	source.rawByID = nil
	source.confirmations = 9
	_, err = manager.ValidateBTCLuckyClaim(claim, witness, user, service)
	require.ErrorContains(t, err, "immature")
	source.confirmations = 10
	source.spent = false
	source.backing = nil
	address, err := GetP2WSHaddressFromScript(script)
	require.NoError(t, err)

	t.Run("missing coinbase remains visible but is not claimable", func(t *testing.T) {
		source.spent = true
		defer func() { source.spent = false }()
		l2.ascend.BTCLucky = &metadata
		rewards, err := manager.getBTCLuckyRewards(witness, address, 10)
		require.NoError(t, err)
		require.Len(t, rewards, 1)
		require.Equal(t, utxo, rewards[0].FundingUtxo)
		require.Equal(t, "error", rewards[0].Status)
		require.NotEmpty(t, rewards[0].Error)
		source.spent = false
		l2.spent = true
		rewards, err = manager.getBTCLuckyRewards(witness, address, 10)
		require.NoError(t, err)
		require.Equal(t, "spent", rewards[0].Status)
		l2.spent = false
	})
	controlledOutput := OutputToSatsNet(l2.output)
	controlledOutput.OutPointStr = anchor.TxID() + ":0"
	controlled := &Channel{ChannelInDB: ChannelInDB{UtxosL2: map[string]*TxOutput_SatsNet{controlledOutput.OutPointStr: controlledOutput}}}
	manager.channelMap = map[string]*Channel{address: controlled}
	_, err = manager.ValidateBTCLuckyClaim(claim, witness, user, service)
	require.ErrorContains(t, err, "managed by an existing channel")
}

func TestBTCLuckyRewardMaturity(t *testing.T) {
	for _, tc := range []struct {
		confirmations, maturity uint64
		mature                  bool
	}{
		{1, 10, false}, {9, 10, false}, {10, 10, true}, {11, 10, true},
		{99, 100, false}, {100, 100, true}, {10, 0, false},
	} {
		err := CheckBTCLuckyMaturity(tc.confirmations, tc.maturity)
		require.Equal(t, tc.mature, err == nil, "%+v", tc)
	}
}

func TestBTCLuckyRewardDistribution(t *testing.T) {
	user, service, bootstrap := []byte{1}, []byte{2}, []byte{3}
	outputs, err := BTCLuckyRewardOutputs(100003, 1000, user, service, bootstrap)
	require.NoError(t, err)
	require.Equal(t, []*swire.TxOut{
		{Value: 84153, PkScript: user}, {Value: 9900, PkScript: service}, {Value: 4950, PkScript: bootstrap},
	}, outputs)
	merged, err := BTCLuckyRewardOutputs(100003, 1000, user, service, service)
	require.NoError(t, err)
	require.Len(t, merged, 2)
	require.EqualValues(t, 14850, merged[1].Value)
	_, err = BTCLuckyRewardOutputs(1000, 1000, user, service, bootstrap)
	require.Error(t, err)
}

func TestBTCLuckyCoreSelfIndexerReceivesBothShares(t *testing.T) {
	winner, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{27}, 32))
	bootstrap, err := hex.DecodeString(indexer.GetBootstrapPubKey())
	require.NoError(t, err)
	scripts, err := btcLuckyRecipientScripts(winner.PubKey().SerializeCompressed(), bootstrap)
	require.NoError(t, err)
	outputs, err := BTCLuckyRewardOutputs(100010, 10, scripts[0], scripts[1], scripts[2])
	require.NoError(t, err)
	var winnerValue, bootstrapValue int64
	for _, output := range outputs {
		if bytes.Equal(output.PkScript, scripts[0]) {
			winnerValue += output.Value
		}
		if bytes.Equal(output.PkScript, scripts[2]) {
			bootstrapValue += output.Value
		}
	}
	require.EqualValues(t, 95000, winnerValue)
	require.EqualValues(t, 5000, bootstrapValue)
}

func TestBTCLuckyGenericSigningRejectsActualRewardAndMixedInputs(t *testing.T) {
	a, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{41}, 32))
	b, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{42}, 32))
	witness, script, err := GetP2WSHscript(a.PubKey().SerializeCompressed(), b.PubKey().SerializeCompressed())
	require.NoError(t, err)
	coinbase := btcwire.NewMsgTx(2)
	coinbase.AddTxIn(btcwire.NewTxIn(&btcwire.OutPoint{Index: 0xffffffff}, []byte("height1-satoshinet"), nil))
	coinbase.AddTxOut(btcwire.NewTxOut(5000, script))
	sourceRaw, err := EncodeMsgTx(coinbase)
	require.NoError(t, err)
	invoice, err := sindexer.StandardAnchorScriptWithSig(coinbase.TxID()+":0", witness, 5000, nil, nil)
	require.NoError(t, err)
	anchor := swire.NewMsgTx(2)
	anchor.AddTxIn(&swire.TxIn{PreviousOutPoint: swire.OutPoint{Index: swire.AnchorTxOutIndex}, SignatureScript: invoice})
	anchor.AddTxOut(swire.NewTxOut(5000, nil, script))
	marker, err := (sindexer.BTCLuckyMetadata{Version: 1, BlockHeight: 1, BlockHash: sch.Hash{1}}).Script()
	require.NoError(t, err)
	anchor.AddTxOut(swire.NewTxOut(0, nil, marker))
	ordinary := swire.NewMsgTx(2)
	ordinary.AddTxIn(swire.NewTxIn(&swire.OutPoint{Hash: sch.Hash{7}}, nil, nil))
	ordinary.AddTxOut(swire.NewTxOut(5000, nil, script))
	l1 := &luckyRewardRPC{raw: sourceRaw, confirmations: 1}
	l2 := &luckyRewardRPC{rawByID: make(map[string]string)}
	p := &Manager{l1IndexerClient: &IndexerRPCClientMgr{active: l1}, l2IndexerClient: &IndexerRPCClientMgr{active: l2}}
	spend := func(parents ...*swire.MsgTx) *swire.MsgTx {
		tx := swire.NewMsgTx(2)
		for _, parent := range parents {
			raw, err := EncodeMsgTx_SatsNet(parent)
			require.NoError(t, err)
			l2.rawByID[parent.TxID()] = raw
			outpoint, err := swire.NewOutPointFromString(parent.TxID() + ":0")
			require.NoError(t, err)
			tx.AddTxIn(swire.NewTxIn(outpoint, nil, nil))
		}
		tx.AddTxOut(swire.NewTxOut(1000, nil, []byte{0x51}))
		return tx
	}
	require.NoError(t, p.RejectBTCLuckyInputs(spend(ordinary), nil))
	require.ErrorContains(t, p.RejectBTCLuckyInputs(spend(anchor), nil), "btc-lucky-claim")
	require.ErrorContains(t, p.RejectBTCLuckyInputs(spend(ordinary, anchor), nil), "btc-lucky-claim")
	// Omitting the marker cannot bypass classification of the real coinbase.
	anchor.TxOut = anchor.TxOut[:1]
	require.ErrorContains(t, p.RejectBTCLuckyInputs(spend(anchor), nil), "btc-lucky-claim")
	// Request-local parents use their actual txid, including unconfirmed chains.
	require.ErrorContains(t, p.RejectBTCLuckyInputs(spend(anchor), map[string]*swire.MsgTx{anchor.TxID(): anchor}), "btc-lucky-claim")
	l2.rawErr = fmt.Errorf("source lookup unavailable")
	require.ErrorContains(t, p.RejectBTCLuckyInputs(spend(ordinary), nil), "lookup unavailable")
}

func TestBTCLuckyOriginalL1CoinbaseStaysExcludedAfterAscend(t *testing.T) {
	for _, ascended := range []bool{false, true} {
		t.Run(fmt.Sprintf("ascended=%t", ascended), func(t *testing.T) {
			p, channel, l1, l2, _ := luckyChannelFixture(t, true, true)
			if !ascended {
				l2.ascend = nil
			}

			spend := btcwire.NewMsgTx(2)
			spend.AddTxIn(l1.output.TxIn())
			spend.AddTxOut(btcwire.NewTxOut(49_000, l1.output.OutValue.PkScript))
			require.ErrorContains(t, p.RejectBTCLuckyL1Inputs(spend, nil), "cannot enter ordinary channel operations")

			// A managed channel anchor built from a funding transaction that
			// spends the original reward is also rejected before signing.
			funding := btcwire.NewMsgTx(2)
			funding.AddTxIn(l1.output.TxIn())
			funding.AddTxOut(btcwire.NewTxOut(49_000, channel.GetChannelPkScript()))
			invoice, err := sindexer.StandardAnchorScriptWithSig(funding.TxID()+":0", channel.RedeemScript, 49_000, nil, nil)
			require.NoError(t, err)
			anchor := swire.NewMsgTx(2)
			anchor.AddTxIn(&swire.TxIn{PreviousOutPoint: swire.OutPoint{Index: swire.AnchorTxOutIndex}, SignatureScript: invoice})
			anchor.AddTxOut(swire.NewTxOut(49_000, nil, channel.GetChannelPkScript()))
			signed := false
			_, err = p.SignChannelAnchorTxWithFunding(anchor, funding, nil, func([]byte) ([]byte, error) {
				signed = true
				return nil, nil
			})
			require.ErrorContains(t, err, "cannot enter ordinary channel operations")
			require.False(t, signed)

			ordinaryManager, _, ordinaryL1, _, _ := luckyChannelFixture(t, false, false)
			ordinarySpend := btcwire.NewMsgTx(2)
			ordinarySpend.AddTxIn(ordinaryL1.output.TxIn())
			ordinarySpend.AddTxOut(btcwire.NewTxOut(49_000, ordinaryL1.output.OutValue.PkScript))
			require.NoError(t, ordinaryManager.RejectBTCLuckyL1Inputs(ordinarySpend, nil))
		})
	}
}

func TestBTCLuckyClaimOutputsAreOrdinaryL2Inputs(t *testing.T) {
	a, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{41}, 32))
	b, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{42}, 32))
	p, _, _, l2, anchor := luckyChannelFixture(t, true, true)
	scripts, err := btcLuckyRecipientScripts(a.PubKey().SerializeCompressed(), b.PubKey().SerializeCompressed())
	require.NoError(t, err)
	claim := swire.NewMsgTx(2)
	outpoint, err := swire.NewOutPointFromString(anchor.TxID() + ":0")
	require.NoError(t, err)
	claim.AddTxIn(swire.NewTxIn(outpoint, nil, nil))
	claim.TxOut, err = BTCLuckyRewardOutputs(50_000, 10, scripts[0], scripts[1], scripts[2])
	require.NoError(t, err)
	claimRaw, err := EncodeMsgTx_SatsNet(claim)
	require.NoError(t, err)
	l2.rawByID[claim.TxID()] = claimRaw

	for outputIndex := range claim.TxOut {
		spend := swire.NewMsgTx(2)
		claimOutput, err := swire.NewOutPointFromString(fmt.Sprintf("%s:%d", claim.TxID(), outputIndex))
		require.NoError(t, err)
		spend.AddTxIn(swire.NewTxIn(claimOutput, nil, nil))
		spend.AddTxOut(swire.NewTxOut(1_000, nil, []byte{0x51}))
		require.NoError(t, p.RejectBTCLuckyInputs(spend, nil))
	}
}
