package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	"github.com/sat20-labs/rgb11/invoicing"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
)

const NativeContractDepositAction = "native-contract-deposit"
const NativeContractDepositPeerAction = "native-contract-deposit-peer"
const NativeContractDepositVersion = 1

var ErrNativeContractInputNotFound = errors.New("native contract input not found")

type NativeContractDepositIntent struct {
	Version     uint32 `json:"version"`
	ContractURL string `json:"contract_url"`
	Recipient   string `json:"recipient"`
	AssetName   string `json:"asset_name"`
	Amount      string `json:"amount"` // exact atomic quantity
}

// NativeContractDeposit is safe to send to both pool owners. It contains no
// sender change secrets or private channel package. Sender recovery remains in
// the existing RGB pending-transfer store and its protected input reservation.
type NativeContractDeposit struct {
	Version    uint32                      `json:"version"`
	TransferID string                      `json:"transfer_id"`
	Intent     NativeContractDepositIntent `json:"intent"`
	// Like PendingTransfer.SignedTx at prepare time, this is the fixed unsigned
	// transaction. Final signatures belong to the existing RGB broadcast record.
	SignedTx      []byte               `json:"signed_tx"`
	WitnessScript []byte               `json:"witness_script"`
	Source        *NativeChannelSource `json:"source"`
}

type nativeContractDepositJournal struct {
	Deposit          NativeContractDeposit `json:"deposit"`
	PeerAcknowledged bool                  `json:"peer_acknowledged"`
	Broadcast        bool                  `json:"broadcast"`
	DirectPeer       bool                  `json:"direct_peer,omitempty"`
	OwnerPublicKey   []byte                `json:"owner_public_key"`
	PeerPublicKey    []byte                `json:"peer_public_key"`
}

type NativeContractInput struct {
	Output      *TxOutput
	Recipient   string
	ProofDigest []byte
	NativeProof []byte
}

func nativeContractIntentScript(intent NativeContractDepositIntent) ([]byte, error) {
	channel, asset, _, err := ParseContractURL(intent.ContractURL)
	if err != nil || channel == "" || asset != intent.AssetName || intent.Version != NativeContractDepositVersion {
		return nil, fmt.Errorf("invalid native deposit contract identity")
	}
	amount, err := strconv.ParseUint(intent.Amount, 10, 64)
	if err != nil || amount == 0 || strconv.FormatUint(amount, 10) != intent.Amount || !IsBtcAddress(intent.Recipient) {
		return nil, fmt.Errorf("invalid native deposit beneficiary or amount")
	}
	raw, err := json.Marshal(intent)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(append([]byte("sat20/native-contract-deposit/intent/v1\x00"), raw...))
	return txscript.NewScriptBuilder().AddOp(txscript.OP_RETURN).AddData([]byte("STP/RGB/DEP/1")).AddData(h[:]).Script()
}

func nativeDepositKey(direction, id string) []byte {
	h := sha256.Sum256([]byte(id))
	return []byte(GetDBKeyPrefix() + "native-contract-deposit/" + _chain + "/" + direction + "/" + hex.EncodeToString(h[:]))
}

func nativeContractWitness(contract ContractRuntime) ([]byte, error) {
	base := contract.GetRuntimeBase()
	witness, err := utils.GenMultiSigScript(base.LocalPubKey, base.RemotePubKey)
	if err != nil {
		return nil, err
	}
	script, err := utils.WitnessScriptHash(witness)
	if err != nil {
		return nil, err
	}
	expected, err := GetPkScriptFromAddress(contract.Address())
	if err != nil || !bytes.Equal(script, expected) {
		return nil, fmt.Errorf("contract owners do not derive the public channel")
	}
	return witness, nil
}

// PrepareNativeContractDeposit fixes the unsigned Bitcoin transaction before
// delivery. Retrying uses ResumeNativeContractDeposit with this TransferID;
// it never reselects inputs or creates another native transition.
func (p *Manager) PrepareNativeContractDeposit(ctx context.Context, contractURL, recipient,
	assetName, amount string, feeRate int64) (*NativeContractDeposit, error) {
	contract := p.getRemoteDeployedContract(contractURL)
	if contract == nil || !contract.GetRuntimeBase().IsActive() {
		return nil, fmt.Errorf("native deposit contract is not active")
	}
	return p.prepareNativeContractDeposit(ctx, contract, recipient, assetName, amount, feeRate, nil, false)
}

func (p *Manager) prepareNativeContractDeposit(ctx context.Context, contract ContractRuntime, recipient,
	assetName, amount string, feeRate int64, owner common.Wallet, directPeer bool) (*NativeContractDeposit, error) {
	if p.l2IndexerClient == nil || !GetChainParam_SatsNet().NativeAssetsActive(int32(p.l2IndexerClient.GetBestHeight()+1)) {
		return nil, fmt.Errorf("native asset mappings are not active")
	}
	batch, err := NewAtomicWriteBatch(p.GetDB())
	if err != nil {
		return nil, err
	}
	batch.Close()
	if contract == nil || contract.GetAssetName().String() != assetName {
		return nil, fmt.Errorf("native deposit contract does not match the requested asset")
	}
	contractURL := contract.URL()
	witness, err := nativeContractWitness(contract)
	if err != nil {
		return nil, err
	}
	name := ParseAssetString(assetName)
	if name == nil || name.Protocol != rgb11wallet.Protocol {
		return nil, rgb11wallet.ErrInvalidRGB11Asset
	}
	info := p.GetTickerInfo(name)
	if info == nil {
		return nil, fmt.Errorf("native deposit requires validated genesis metadata")
	}
	value, err := indexer.NewDecimalFromString(amount, info.Divisibility)
	if err != nil || value.Sign() <= 0 || !value.Value.IsUint64() {
		return nil, fmt.Errorf("invalid native deposit quantity")
	}
	if recipient == "" {
		recipient = p.GetWallet().GetAddress()
	}
	intent := NativeContractDepositIntent{Version: 1, ContractURL: contractURL, Recipient: recipient,
		AssetName: assetName, Amount: value.Value.String()}
	memo, err := nativeContractIntentScript(intent)
	if err != nil {
		return nil, err
	}
	return runRGB11ManagedOperationWithManager(p, ctx, rgb11ManagedOperationNew,
		func() (*rgb11Manager, error) { return p.stpRGB11Scope(owner) }, func(rgb *rgb11Manager) (*NativeContractDeposit, error) {
			id, err := rgb.rgb11ContractIDForAssetName(*name)
			if err != nil {
				return nil, err
			}
			contractID, err := consensus.ParseContractID(id)
			if err != nil {
				return nil, err
			}
			script, err := utils.WitnessScriptHash(witness)
			if err != nil {
				return nil, err
			}
			beneficiary, err := invoicing.NewWitnessBeneficiary(rgb11InvoiceNetwork(GetChainParam()), script, nil)
			if err != nil {
				return nil, err
			}
			expiry := time.Now().Add(24 * time.Hour).Unix()
			invoice := invoicing.Invoice{Contract: &contractID, Beneficiary: beneficiary, Expiry: &expiry,
				Assignment: &invoicing.InvoiceState{Kind: invoicing.StateAmount, Amount: invoicing.Amount(value.Value.Uint64())}}
			rgb.nativeDepositMemo = memo
			defer func() { rgb.nativeDepositMemo = nil }()
			prepared, err := rgb.prepareRGB11TransferForEndpoints(ctx, RGB11SendRequest{Invoice: invoice.String(), FeeRate: feeRate, MinConfirmations: 1}, false, nil)
			if err != nil {
				return nil, err
			}
			pending, err := rgb.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
			if err != nil {
				return nil, err
			}
			proof, err := nativeproof.BuildRGB11(pending.RecipientConsignment, nil)
			if err != nil {
				return nil, err
			}
			tx := new(wire.MsgTx)
			if err := tx.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
				return nil, err
			}
			vout := pending.State.RecipientVout
			if int(vout) >= len(tx.TxOut) {
				return nil, ErrRGB11Inconsistent
			}
			output := indexer.NewTxOutput(tx.TxOut[vout].Value)
			output.OutPointStr = fmt.Sprintf("%s:%d", tx.TxID(), vout)
			output.OutValue.PkScript = append([]byte(nil), tx.TxOut[vout].PkScript...)
			output.Assets = indexer.TxAssets{pending.State.Asset}
			deposit := &NativeContractDeposit{Version: 1, TransferID: pending.State.TransferID, Intent: intent,
				SignedTx: append([]byte(nil), pending.SignedTx...), WitnessScript: witness,
				Source: &NativeChannelSource{Version: 1, ChannelID: contract.Address(), Output: output, PublicProof: proof}}
			var peerKey []byte
			if directPeer {
				peerKey = contract.GetRuntimeBase().RemotePubKey
			} else if node := p.GetServerNode(); node != nil && node.Pubkey != nil {
				peerKey = node.Pubkey.SerializeCompressed()
			}
			if len(peerKey) == 0 {
				return nil, fmt.Errorf("native deposit peer unavailable")
			}
			raw, err := json.Marshal(nativeContractDepositJournal{Deposit: *deposit, DirectPeer: directPeer,
				OwnerPublicKey: rgb.wallet.GetPaymentPubKey().SerializeCompressed(), PeerPublicKey: append([]byte(nil), peerKey...)})
			if err != nil {
				return nil, err
			}
			if err := p.GetDB().Write(nativeDepositKey("send", deposit.TransferID), raw); err != nil {
				return deposit, fmt.Errorf("persist native deposit %s: %w", deposit.TransferID, err)
			}
			return deposit, nil
		})
}

func (p *Manager) ResumeNativeContractDeposit(ctx context.Context, transferID string) (string, error) {
	raw, err := p.GetDB().Read(nativeDepositKey("send", transferID))
	if err != nil {
		return "", err
	}
	var journal nativeContractDepositJournal
	if json.Unmarshal(raw, &journal) != nil || journal.Deposit.TransferID != transferID || journal.Deposit.Source == nil {
		return "", fmt.Errorf("invalid native deposit journal")
	}
	point, err := wire.NewOutPointFromString(journal.Deposit.Source.Output.OutPointStr)
	if err != nil {
		return "", err
	}
	if journal.Broadcast {
		return point.Hash.String(), nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var owner common.Wallet
	for _, account := range p.localRGB11Accounts() {
		if bytes.Equal(account.Wallet.GetPaymentPubKey().SerializeCompressed(), journal.OwnerPublicKey) {
			owner = account.Wallet
			break
		}
	}
	if owner == nil {
		return "", fmt.Errorf("native deposit's fixed wallet is unavailable")
	}
	if !journal.PeerAcknowledged {
		raw, err := json.Marshal(journal.Deposit)
		if err != nil {
			return "", err
		}
		peer, err := p.configuredSigningPeer(journal.PeerPublicKey)
		if err != nil {
			return "", err
		}
		action := NativeContractDepositAction
		if journal.DirectPeer {
			action = NativeContractDepositPeerAction
		}
		if err := peer.client.SendActionResultNfty(owner, 0, action, 0, string(raw)); err != nil {
			return "", err
		}
		journal.PeerAcknowledged = true
		raw, err = json.Marshal(journal)
		if err != nil {
			return "", err
		}
		if err := p.GetDB().Write(nativeDepositKey("send", transferID), raw); err != nil {
			return "", err
		}
	}
	// Only the existing RGB broadcast boundary may sign. It persists the ACK
	// and broadcast intent before signing, verifies the signed spend, and lets
	// the Bitcoin backend apply mempool policy to those final bytes.
	txid, err := runRGB11ManagedOperationWithManager(p, ctx, rgb11ManagedOperationContinue,
		func() (*rgb11Manager, error) { return p.stpRGB11Scope(owner) },
		func(rgb *rgb11Manager) (string, error) {
			pending, err := rgb.projectionStore.LoadPendingTransfer(transferID)
			if err != nil {
				return "", err
			}
			fixed, err := decodeRGB11BroadcastTx(journal.Deposit.SignedTx)
			if err != nil {
				return "", err
			}
			current, err := decodeRGB11BroadcastTx(pending.SignedTx)
			if err != nil || !sameRGB11UnsignedTx(fixed, current) {
				return "", fmt.Errorf("native deposit pending transaction changed")
			}
			if status, statusErr := rgb.evidence.GetTxStatus(point.Hash.String()); statusErr == nil && status != nil && (status.Confirmed || status.InMempool) {
				if err := validateObservedNativeContractDeposit(rgb.evidence, &journal.Deposit, false); err != nil {
					return "", err
				}
			}
			return rgb.BroadcastRGB11OutOfBand([]string{transferID})
		})
	if err != nil {
		return txid, err
	}
	if txid != point.Hash.String() {
		return "", fmt.Errorf("native deposit broadcaster changed the fixed transaction")
	}
	journal.Broadcast = true
	raw, err = json.Marshal(journal)
	if err != nil {
		return txid, err
	}
	if err := p.GetDB().Write(nativeDepositKey("send", transferID), raw); err != nil {
		return txid, err
	}
	return txid, nil
}

// ReceiveNativeContractDeposit acknowledges the exact unsigned transition and
// proof, not Bitcoin spend authorization. Materialize independently requires
// the matching confirmed transaction and validates its complete signatures.
func (p *Manager) ReceiveNativeContractDeposit(ctx context.Context, contract ContractRuntime, deposit *NativeContractDeposit) error {
	if deposit == nil || deposit.Version != 1 || deposit.Source == nil || deposit.Source.Output == nil ||
		deposit.Intent.ContractURL != contract.URL() || deposit.Intent.AssetName != contract.GetAssetName().String() {
		return fmt.Errorf("invalid native deposit envelope")
	}
	if err := p.registerNativePublicContractBinding(contract); err != nil {
		return err
	}
	witness, err := nativeContractWitness(contract)
	if err != nil || !bytes.Equal(witness, deposit.WitnessScript) {
		return fmt.Errorf("native deposit witness mismatch")
	}
	memo, err := nativeContractIntentScript(deposit.Intent)
	if err != nil {
		return err
	}
	tx := new(wire.MsgTx)
	reader := bytes.NewReader(deposit.SignedTx)
	if err := tx.Deserialize(reader); err != nil || reader.Len() != 0 {
		return fmt.Errorf("invalid native deposit Bitcoin transaction")
	}
	if tx.HasWitness() {
		return fmt.Errorf("native deposit preparation must not contain signatures")
	}
	for _, input := range tx.TxIn {
		if len(input.SignatureScript) != 0 {
			return fmt.Errorf("native deposit preparation must not contain signatures")
		}
	}
	point, err := wire.NewOutPointFromString(deposit.Source.Output.OutPointStr)
	if err != nil || point.Hash.String() != tx.TxID() || int(point.Index) >= len(tx.TxOut) {
		return fmt.Errorf("native deposit carrier mismatch")
	}
	out := tx.TxOut[point.Index]
	if out.Value != deposit.Source.Output.Value() || !bytes.Equal(out.PkScript, deposit.Source.Output.OutValue.PkScript) {
		return fmt.Errorf("native deposit carrier facts differ from signed transaction")
	}
	count := 0
	for _, output := range tx.TxOut {
		if bytes.Equal(output.PkScript, memo) {
			if output.Value != 0 {
				return fmt.Errorf("valued native intent")
			}
			count++
		}
	}
	if count != 1 {
		return fmt.Errorf("native deposit requires one signed intent commitment")
	}
	// A durable, previously verified envelope is an immutable ACK. Replaying
	// it after its inputs have been spent must not require those inputs to be
	// unspent again; new or changed envelopes still undergo every check below.
	raw, err := json.Marshal(deposit)
	if err != nil {
		return err
	}
	key := nativeDepositKey("receive", deposit.Source.Output.OutPointStr)
	old, err := p.GetDB().Read(key)
	if err == nil {
		if !bytes.Equal(old, raw) {
			return fmt.Errorf("native deposit is immutable")
		}
		return nil
	}
	if !errors.Is(err, indexer.ErrKeyNotFound) {
		return err
	}
	if pool, ok := contract.(*LaunchPoolContractRunTime); ok && !pool.IsActive() {
		if err := pool.validateNativeLaunchDeposit(ctx, deposit); err != nil {
			return err
		}
	}
	validated, err := p.validateNativeChannelSource(ctx, contract.GetRuntimeBase().GetLocalWallet(), contract.Address(), deposit.Source, nativeproof.Prepared)
	if err != nil {
		return err
	}
	if validated.Asset.Amount.Value.String() != deposit.Intent.Amount {
		return fmt.Errorf("native deposit amount differs from signed intent")
	}
	// Preparation checks every input independently of the RGB assignment proof.
	// Spend authorization is checked against the observed final witness before
	// any contract input or anchor can be authorized.
	rgb, err := p.captureRGB11NetworkScope(true)
	if err != nil {
		return err
	}
	for _, input := range tx.TxIn {
		prev, err := rgb.evidence.GetUTXO(input.PreviousOutPoint.String())
		if err != nil || prev == nil {
			return fmt.Errorf("native deposit input evidence unavailable: %w", err)
		}
		if !txscript.IsPayToTaproot(prev.PkScript) {
			return fmt.Errorf("native deposit input must use taproot")
		}
	}
	nativeChannelInventoryMu.Lock()
	defer nativeChannelInventoryMu.Unlock()
	old, err = p.GetDB().Read(key)
	if err == nil && !bytes.Equal(old, raw) {
		return fmt.Errorf("native deposit is immutable")
	}
	if err != nil && !errors.Is(err, indexer.ErrKeyNotFound) {
		return err
	}
	return p.GetDB().Write(key, raw)
}

// The prepared receipt never authorizes minting. Resolve the final Bitcoin
// witness and historical prevouts independently, including after those inputs
// have been spent, before using the receipt as a contract input or anchor.
func validateObservedNativeContractDeposit(evidence rgb11wallet.BitcoinEvidenceProvider,
	deposit *NativeContractDeposit, requireConfirmed bool) error {
	if evidence == nil || deposit == nil {
		return fmt.Errorf("native deposit evidence unavailable")
	}
	fixed, err := decodeRGB11BroadcastTx(deposit.SignedTx)
	if err != nil {
		return err
	}
	txid := fixed.TxID()
	status, err := evidence.GetTxStatus(txid)
	if err != nil {
		return err
	}
	if status == nil || status.TxID != txid || (!status.Confirmed && (requireConfirmed || !status.InMempool)) {
		return fmt.Errorf("native deposit Bitcoin transaction is not observed at the required confirmation state")
	}
	raw, err := evidence.GetRawTx(txid)
	if err != nil {
		return err
	}
	tx, err := decodeRGB11BroadcastTx(raw)
	if err != nil || !sameRGB11UnsignedTx(fixed, tx) {
		return fmt.Errorf("observed native deposit differs from the acknowledged transaction")
	}
	var canonical bytes.Buffer
	if err := tx.Serialize(&canonical); err != nil || !bytes.Equal(canonical.Bytes(), raw) {
		return fmt.Errorf("invalid observed native deposit transaction encoding")
	}
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	for _, input := range tx.TxIn {
		parentRaw, err := evidence.GetRawTx(input.PreviousOutPoint.Hash.String())
		if err != nil {
			return err
		}
		parent, err := decodeRGB11BroadcastTx(parentRaw)
		if err != nil || parent.TxHash() != input.PreviousOutPoint.Hash || int(input.PreviousOutPoint.Index) >= len(parent.TxOut) {
			return fmt.Errorf("native deposit input transaction evidence mismatch")
		}
		prev := parent.TxOut[input.PreviousOutPoint.Index]
		if !txscript.IsPayToTaproot(prev.PkScript) || len(input.SignatureScript) != 0 || len(input.Witness) != 1 ||
			(len(input.Witness[0]) != 64 && !(len(input.Witness[0]) == 65 && input.Witness[0][64] == byte(txscript.SigHashAll))) {
			return fmt.Errorf("native deposit input does not commit all outputs")
		}
		fetcher.AddPrevOut(input.PreviousOutPoint, prev)
	}
	hashes := txscript.NewTxSigHashes(tx, fetcher)
	for i, input := range tx.TxIn {
		prev := fetcher.FetchPrevOutput(input.PreviousOutPoint)
		engine, err := txscript.NewEngine(prev.PkScript, tx, i, txscript.StandardVerifyFlags, nil, hashes, prev.Value, fetcher)
		if err != nil {
			return err
		}
		if err := engine.Execute(); err != nil {
			return err
		}
	}
	return nil
}

func (p *Manager) MaterializeNativeContractInput(ctx context.Context, contract ContractRuntime, outpoint string) (*NativeContractInput, error) {
	raw, err := p.GetDB().Read(nativeDepositKey("receive", outpoint))
	if errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, ErrNativeContractInputNotFound
	}
	if err != nil {
		return nil, err
	}
	var deposit NativeContractDeposit
	if json.Unmarshal(raw, &deposit) != nil || deposit.Source == nil || deposit.Source.Output == nil {
		return nil, fmt.Errorf("invalid native deposit record")
	}
	if deposit.Intent.ContractURL != contract.URL() {
		return nil, ErrNativeContractInputNotFound
	}
	scope, err := p.stpRGB11Scope(contract.GetRuntimeBase().GetLocalWallet())
	if err != nil {
		return nil, err
	}
	if err := validateObservedNativeContractDeposit(scope.evidence, &deposit, true); err != nil {
		return nil, err
	}
	spent, err := scope.evidence.GetOutspend(outpoint)
	if err != nil || spent == nil {
		return nil, fmt.Errorf("native deposit current carrier status unavailable: %w", err)
	}
	if spent.Spent {
		if !rgb11KnownObservedChannelSpend(scope.evidence, outpoint, spent.SpendingTx) {
			return nil, fmt.Errorf("native deposit has no exact observed spender")
		}
		if err := p.validateHistoricalNativeContractDeposit(ctx, contract, deposit.Source); err != nil {
			return nil, err
		}
	} else if err := p.RegisterNativeChannelSettlementSource(ctx, contract.GetRuntimeBase().GetLocalWallet(), contract.Address(), deposit.Source); err != nil {
		return nil, err
	}
	if err := contract.GetRuntimeBase().initializeNativePublicMetadata(deposit.Source.Output); err != nil {
		return nil, err
	}
	h := sha256.Sum256(deposit.Source.PublicProof)
	return &NativeContractInput{Output: deposit.Source.Output.Clone(), Recipient: deposit.Intent.Recipient,
		ProofDigest: h[:], NativeProof: append([]byte(nil), deposit.Source.PublicProof...)}, nil
}

// NativeContractInputProtected guards generic Bitcoin fee selection even
// before a derived RGB wallet projection or transient UTXO lock is rebuilt.
// A storage error must be treated as protected by the caller.
func (p *Manager) NativeContractInputProtected(outpoint string) (bool, error) {
	_, err := p.GetDB().Read(nativeDepositKey("receive", outpoint))
	if errors.Is(err, indexer.ErrKeyNotFound) {
		_, err = p.GetDB().Read(nativeLaunchPoolKey("source", outpoint))
		if errors.Is(err, indexer.ErrKeyNotFound) {
			return false, nil
		}
	}
	if err != nil {
		return true, err
	}
	return true, nil
}
