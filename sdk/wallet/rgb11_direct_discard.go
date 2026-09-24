//go:build rgb11discard

package wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/strict_types"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

var errRGB11DiscardGone = errors.New("target RGB11 mailbox message is absent")

type RGB11DiscardTarget struct {
	ReceiverAccountID string `json:"receiver_account_id"`
	SenderAccountID   string `json:"sender_account_id"`
	ApplicationID     string `json:"application_id"`
	DirectMessageID   string `json:"direct_message_id"`
	TransferID        string `json:"transfer_id"`
	WitnessTxID       string `json:"witness_txid"`
	OutPoint          string `json:"outpoint"`
	SpendingTxID      string `json:"spending_txid"`
}

type RGB11DiscardPlan struct {
	Target             RGB11DiscardTarget `json:"target"`
	WalletID           int64              `json:"wallet_id"`
	SubAccount         uint32             `json:"sub_account"`
	ReceiverAddress    string             `json:"receiver_address"`
	RecordKey          string             `json:"record_key"`
	RecordHash         string             `json:"record_hash"`
	ConsignmentHash    string             `json:"consignment_hash"`
	SpendEvidence      string             `json:"spend_evidence"`
	SpendingVin        uint32             `json:"spending_vin"`
	SpendBlockHeight   int64              `json:"spend_block_height,omitempty"`
	SpendBlockHash     string             `json:"spend_block_hash,omitempty"`
	SpendConfirmations int64              `json:"spend_confirmations,omitempty"`
	Fingerprint        string             `json:"fingerprint"`
}

var rgb11DiscardOnlyTarget = RGB11DiscardTarget{
	ReceiverAccountID: "91cc8c5390f67915dc16bfd3e3ec0cb492e9736252081f6c047bf64b9fb0cf6f",
	SenderAccountID:   "148cbe135aea8ee9b72f18ca6ddf0efc052e54b6d723cc473a0cc6011766d776",
	ApplicationID:     "172d6960e7b91018952f590dfa83cfed62d6f6494ac7d640e7474843e6184e3f",
	DirectMessageID:   "00065c102d719ec8c329515259e04904",
	TransferID:        "rgb:csg:Fy1pYOe5-EBiVL1k-N_oPP7W-LW9klKx-9ZA50dI-Q_YYTj8#magnum-race-lake",
	WitnessTxID:       "0bbc6051be126edb43d04bd390967c19a9f95b4aa8fb67edf12bca1f64d3dfe6",
	OutPoint:          "0bbc6051be126edb43d04bd390967c19a9f95b4aa8fb67edf12bca1f64d3dfe6:1",
	SpendingTxID:      "976bb0c68ccb546fc06125e61a5979314393fd79231c70156462ebb9202578d1",
}

type rgb11DiscardSource struct {
	accountID   string
	wallet      common.Wallet
	walletID    int64
	subAccount  uint32
	messages    []*AccountDirectMessage
	hasTransfer func(string) (bool, error)
	hasProof    func(string) (bool, error)
	getOutspend func(string) (*rgb11wallet.BitcoinOutspend, error)
	getRawTx    func(string) ([]byte, error)
	getTxStatus func(string) (*rgb11wallet.BitcoinTxStatus, error)
}

type rgb11DiscardApplyOps struct {
	plan    func() (*RGB11DiscardPlan, error)
	delete  func(string) error
	confirm func(string) error
	cleanup func() error
	mark    func() error
}

func (p *Manager) UnlockRGB11Discard(password string) (int64, error) {
	p.mutex.RLock()
	alreadyUnlocked := p.wallet != nil
	p.mutex.RUnlock()
	if alreadyUnlocked {
		return p.UnlockWallet(password)
	}
	p.channelIdentityMu.Lock()
	defer p.channelIdentityMu.Unlock()
	p.mutex.Lock()
	id, err := p.unlockWallet(password)
	p.mutex.Unlock()
	return id, err
}

func (p *Manager) PlanRGB11Discard(target RGB11DiscardTarget) (*RGB11DiscardPlan, error) {
	if target != rgb11DiscardOnlyTarget {
		return nil, errors.New("maintenance target is not the approved C/172d Direct message")
	}
	if p == nil || p.wallet == nil || p.rgbManager == nil || p.rgbManager.evidence == nil {
		return nil, ErrRGB11Inconsistent
	}
	accountID, err := dkvsAccountID(p.wallet)
	if err != nil || accountID != target.ReceiverAccountID {
		return nil, errors.New("active wallet is not the target receiver scope")
	}
	messages, err := p.readWalletDirectMessages(p.wallet)
	if err != nil {
		return nil, err
	}
	source := rgb11DiscardSource{
		accountID: accountID, wallet: p.wallet, walletID: p.status.CurrentWallet,
		subAccount: p.wallet.GetSubAccount(), messages: messages,
		hasTransfer: func(id string) (bool, error) {
			_, loadErr := p.rgbManager.projectionStore.LoadTransferState(id)
			if loadErr == nil {
				return true, nil
			}
			if errors.Is(loadErr, indexer.ErrKeyNotFound) {
				return false, nil
			}
			return false, loadErr
		},
		hasProof: func(outpoint string) (bool, error) {
			proofs, listErr := p.rgbManager.projectionStore.ListProofs()
			if listErr != nil {
				return false, listErr
			}
			for _, proof := range proofs {
				if proof != nil && proof.OutPoint == outpoint {
					return true, nil
				}
			}
			return false, nil
		},
		getOutspend: p.rgbManager.evidence.GetOutspend,
		getRawTx:    p.rgbManager.evidence.GetRawTx,
		getTxStatus: p.rgbManager.evidence.GetTxStatus,
	}
	return buildRGB11DiscardPlan(target, source)
}

func (p *Manager) ApplyRGB11Discard(approved RGB11DiscardPlan) error {
	if approved.Target != rgb11DiscardOnlyTarget {
		return errors.New("maintenance plan is not for the approved C/172d Direct message")
	}
	if p == nil || p.wallet == nil || p.rgbManager == nil {
		return ErrRGB11Inconsistent
	}
	accountID, err := dkvsAccountID(p.wallet)
	if err != nil || accountID != approved.Target.ReceiverAccountID {
		return errors.New("active wallet is not the target receiver scope")
	}
	store, err := p.accountDKVSStore()
	if err != nil {
		return err
	}
	ops := rgb11DiscardApplyOps{
		plan: func() (*RGB11DiscardPlan, error) {
			return p.PlanRGB11Discard(approved.Target)
		},
		delete: func(key string) error { return p.DeleteMailboxMessage(p.wallet, key) },
		confirm: func(key string) error {
			return confirmRGB11DiscardGone(store.client, key)
		},
		cleanup: func() error {
			return newDKVSReplicaStore(p.db).DiscardExactReplica(
				store.client.replicaNamespace, approved.RecordKey, approved.RecordHash)
		},
		mark: func() error {
			return p.rgbManager.markRGB11AddressMessageProcessed("consignment", approved.Target.ApplicationID)
		},
	}
	return applyRGB11Discard(approved, ops)
}

func buildRGB11DiscardPlan(target RGB11DiscardTarget,
	source rgb11DiscardSource) (*RGB11DiscardPlan, error) {
	if source.wallet == nil || source.accountID == "" || source.accountID != target.ReceiverAccountID ||
		source.getOutspend == nil {
		return nil, errors.New("discard source does not match receiver scope")
	}
	if !validDiscardHex(target.ApplicationID, 32) || !validDiscardHex(target.DirectMessageID, 16) ||
		!validDiscardHex(target.ReceiverAccountID, 32) || !validDiscardHex(target.SenderAccountID, 32) ||
		!validDiscardHex(target.WitnessTxID, 32) || !validDiscardHex(target.SpendingTxID, 32) {
		return nil, errors.New("discard target contains an invalid identifier")
	}
	if source.hasTransfer != nil {
		hasTransfer, err := source.hasTransfer(target.TransferID)
		if err != nil {
			return nil, fmt.Errorf("read local transfer state: %w", err)
		}
		if hasTransfer {
			return nil, errors.New("target transfer still has local wallet state")
		}
	}
	if source.hasProof != nil {
		hasProof, err := source.hasProof(target.OutPoint)
		if err != nil {
			return nil, fmt.Errorf("read local RGB proofs: %w", err)
		}
		if hasProof {
			return nil, errors.New("target outpoint still has a local RGB proof")
		}
	}
	var matched *AccountDirectMessage
	for _, item := range source.messages {
		if item == nil || item.Payload == nil || item.Payload.ApplicationID != target.ApplicationID {
			continue
		}
		if matched != nil {
			return nil, errors.New("target application has multiple mailbox records")
		}
		matched = item
	}
	if matched == nil {
		return nil, errRGB11DiscardGone
	}
	if matched.Direct == nil || matched.Record == nil ||
		matched.Payload.Kind != AccountMessageKindRGB11Consignment ||
		matched.Direct.SenderAccount != target.SenderAccountID ||
		matched.Direct.RecipientAccount != target.ReceiverAccountID ||
		matched.Direct.MessageID != target.DirectMessageID {
		return nil, errors.New("target mailbox envelope mismatch")
	}
	expectedKey, err := dkvsindexer.MailMsgKey(
		target.ReceiverAccountID, target.SenderAccountID, target.DirectMessageID)
	if err != nil || matched.Record.Key != expectedKey {
		return nil, errors.New("target mailbox key mismatch")
	}
	container, tx, err := rgb11DiscardWitness(matched.Payload.Body)
	if err != nil || container.Armor == nil || container.Armor.ID != target.TransferID ||
		tx.TxHash().String() != target.WitnessTxID {
		return nil, errors.New("target consignment or witness mismatch")
	}
	txID, vout, err := splitDiscardOutpoint(target.OutPoint)
	if err != nil || txID != target.WitnessTxID || vout >= uint32(len(tx.TxOut)) {
		return nil, errors.New("target recipient outpoint mismatch")
	}
	walletScript, err := AddrToPkScript(source.wallet.GetAddress(), GetChainParam())
	if err != nil || !bytes.Equal(tx.TxOut[vout].PkScript, walletScript) {
		return nil, errors.New("target outpoint is not controlled by receiver wallet")
	}
	spend, err := verifyRGB11DiscardSpend(target, source)
	if err != nil {
		return nil, err
	}
	recordHash := dkvsindexer.RecordHash(matched.Record)
	consignmentHash := sha256.Sum256(matched.Payload.Body)
	plan := &RGB11DiscardPlan{
		Target: target, WalletID: source.walletID, SubAccount: source.subAccount,
		ReceiverAddress: source.wallet.GetAddress(), RecordKey: matched.Record.Key,
		RecordHash:      hex.EncodeToString(recordHash[:]),
		ConsignmentHash: hex.EncodeToString(consignmentHash[:]),
		SpendEvidence:   spend.mode, SpendingVin: spend.vin,
		SpendBlockHeight: spend.blockHeight, SpendBlockHash: spend.blockHash,
		SpendConfirmations: spend.confirmations,
	}
	plan.Fingerprint, err = rgb11DiscardFingerprint(*plan)
	return plan, err
}

type rgb11DiscardSpend struct {
	mode          string
	vin           uint32
	blockHeight   int64
	blockHash     string
	confirmations int64
}

func verifyRGB11DiscardSpend(target RGB11DiscardTarget,
	source rgb11DiscardSource) (*rgb11DiscardSpend, error) {
	spent, err := source.getOutspend(target.OutPoint)
	if err != nil {
		return nil, fmt.Errorf("query target outpoint spender: %w", err)
	}
	if spent == nil || !spent.Spent {
		return nil, errors.New("target outpoint is not spent")
	}
	if spent.SpendingTx == target.SpendingTxID {
		return &rgb11DiscardSpend{mode: "outspend", vin: spent.Vin}, nil
	}
	if spent.SpendingTx != "" && spent.SpendingTx != "unknown" {
		return nil, errors.New("target outpoint spender mismatch")
	}
	if source.getRawTx == nil || source.getTxStatus == nil {
		return nil, errors.New("target spender identity is unavailable")
	}
	status, err := source.getTxStatus(target.SpendingTxID)
	if err != nil {
		return nil, fmt.Errorf("query target spender status: %w", err)
	}
	if status == nil || status.TxID != target.SpendingTxID || !status.Confirmed ||
		status.Confirmations < 1 || status.BlockHeight <= 0 || !validDiscardHex(status.BlockHash, 32) {
		return nil, errors.New("target spender is not confirmed with exact block evidence")
	}
	raw, err := source.getRawTx(target.SpendingTxID)
	if err != nil {
		return nil, fmt.Errorf("read target spender transaction: %w", err)
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	reader := bytes.NewReader(raw)
	if err := tx.Deserialize(reader); err != nil || reader.Len() != 0 ||
		tx.TxHash().String() != target.SpendingTxID {
		return nil, errors.New("target spender raw transaction mismatch")
	}
	matched := -1
	for index, input := range tx.TxIn {
		if input.PreviousOutPoint.String() != target.OutPoint {
			continue
		}
		if matched >= 0 {
			return nil, errors.New("target outpoint appears multiple times in spender")
		}
		matched = index
	}
	if matched < 0 {
		return nil, errors.New("target spender does not consume target outpoint")
	}
	return &rgb11DiscardSpend{
		mode: "confirmed-raw-tx", vin: uint32(matched), blockHeight: status.BlockHeight,
		blockHash: status.BlockHash, confirmations: status.Confirmations,
	}, nil
}

func applyRGB11Discard(approved RGB11DiscardPlan, ops rgb11DiscardApplyOps) error {
	want, err := rgb11DiscardFingerprint(approved)
	if err != nil || approved.Fingerprint == "" || want != approved.Fingerprint {
		return errors.New("approved discard plan fingerprint mismatch")
	}
	current, planErr := ops.plan()
	if planErr == nil {
		if current == nil || *current != approved {
			return errors.New("approved discard plan no longer matches live evidence")
		}
		if err := ops.delete(approved.RecordKey); err != nil && !errors.Is(err, ErrDKVSRecordNotFound) {
			return err
		}
	} else if errors.Is(planErr, errRGB11DiscardGone) {
	} else {
		return planErr
	}
	if ops.confirm == nil {
		return errors.New("remote mailbox deletion confirmation is required")
	}
	if err := ops.confirm(approved.RecordKey); err != nil {
		return err
	}
	if ops.cleanup != nil {
		if err := ops.cleanup(); err != nil {
			return err
		}
	}
	return ops.mark()
}

func confirmRGB11DiscardGone(client *SatsNetDKVSClient, key string) error {
	if client == nil {
		return errors.New("remote mailbox client is unavailable")
	}
	var last error
	for attempt := 0; attempt < 5; attempt++ {
		state, err := client.GetKeyState(key)
		if err == nil && state != nil && state.Key == key &&
			(state.Status == dkvsindexer.KeyStateNeverSeen || state.Status == dkvsindexer.KeyStateDeleted) {
			return nil
		}
		if err != nil {
			last = err
		} else {
			last = fmt.Errorf("remote mailbox state is %v", state)
		}
		if attempt < 4 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	return fmt.Errorf("remote mailbox deletion is unconfirmed: %w", last)
}

func rgb11DiscardFingerprint(plan RGB11DiscardPlan) (string, error) {
	plan.Fingerprint = ""
	raw, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func rgb11DiscardWitness(raw []byte) (*coreconsignment.Container, *wire.MsgTx, error) {
	container, err := coreconsignment.Decode(raw)
	if err != nil {
		return nil, nil, err
	}
	bundles, ok := container.Value.Field("bundles")
	bundles = bundles.Unwrap()
	if !ok || len(bundles.Items) == 0 {
		return nil, nil, errors.New("consignment has no witness bundle")
	}
	witness, ok := bundles.Items[len(bundles.Items)-1].Unwrap().Field("pubWitness")
	witness = witness.Unwrap()
	if !ok || witness.Kind != strict_types.ValueUnion || witness.Name != "tx" || witness.Inner == nil {
		return nil, nil, errors.New("latest witness is not embedded")
	}
	tx, err := rgb11DiscardStrictTx(witness.Inner.Unwrap())
	return container, tx, err
}

func rgb11DiscardStrictTx(value strict_types.Value) (*wire.MsgTx, error) {
	versionValue, versionOK := value.Field("version")
	inputsValue, inputsOK := value.Field("inputs")
	outputsValue, outputsOK := value.Field("outputs")
	lockTimeValue, lockTimeOK := value.Field("lockTime")
	version, versionNumberOK := discardSigned(versionValue)
	lockTime, lockTimeNumberOK := discardUnsigned(lockTimeValue)
	inputs, outputs := inputsValue.Unwrap(), outputsValue.Unwrap()
	if !versionOK || !inputsOK || !outputsOK || !lockTimeOK || !versionNumberOK || !lockTimeNumberOK ||
		version < math.MinInt32 || version > math.MaxInt32 || lockTime > math.MaxUint32 ||
		inputs.Kind != strict_types.ValueList || outputs.Kind != strict_types.ValueList {
		return nil, errors.New("invalid embedded witness transaction")
	}
	tx := wire.NewMsgTx(int32(version))
	tx.LockTime = uint32(lockTime)
	for _, item := range inputs.Items {
		item = item.Unwrap()
		previous, previousOK := item.Field("prevOutput")
		scriptValue, scriptOK := item.Field("sigScript")
		sequenceValue, sequenceOK := item.Field("sequence")
		txidValue, txidOK := previous.Unwrap().Field("txid")
		voutValue, voutOK := previous.Unwrap().Field("vout")
		txid, bytesOK := txidValue.Bytes()
		vout, voutNumberOK := discardUnsigned(voutValue)
		sequence, sequenceNumberOK := discardUnsigned(sequenceValue)
		script, scriptBytesOK := scriptValue.Bytes()
		if !previousOK || !scriptOK || !sequenceOK || !txidOK || !voutOK || !bytesOK || len(txid) != 32 ||
			!voutNumberOK || vout > math.MaxUint32 || !sequenceNumberOK || sequence > math.MaxUint32 || !scriptBytesOK {
			return nil, errors.New("invalid embedded witness input")
		}
		var hash chainhash.Hash
		copy(hash[:], txid)
		input := wire.NewTxIn(&wire.OutPoint{Hash: hash, Index: uint32(vout)}, script, nil)
		input.Sequence = uint32(sequence)
		tx.AddTxIn(input)
	}
	for _, item := range outputs.Items {
		item = item.Unwrap()
		amountValue, amountOK := item.Field("value")
		scriptValue, scriptOK := item.Field("scriptPubkey")
		amount, amountNumberOK := discardUnsigned(amountValue)
		script, scriptBytesOK := scriptValue.Bytes()
		if !amountOK || !scriptOK || !amountNumberOK || amount > math.MaxInt64 || !scriptBytesOK {
			return nil, errors.New("invalid embedded witness output")
		}
		tx.AddTxOut(wire.NewTxOut(int64(amount), script))
	}
	return tx, nil
}

func discardUnsigned(value strict_types.Value) (uint64, bool) {
	value = value.Unwrap()
	if value.Kind == strict_types.ValueUnion && value.Inner != nil {
		return discardUnsigned(*value.Inner)
	}
	return value.Uint64()
}

func discardSigned(value strict_types.Value) (int64, bool) {
	value = value.Unwrap()
	if value.Signed != nil {
		return *value.Signed, true
	}
	if value.Unsigned != nil && *value.Unsigned <= math.MaxInt64 {
		return int64(*value.Unsigned), true
	}
	return 0, false
}

func splitDiscardOutpoint(outpoint string) (string, uint32, error) {
	parts := strings.Split(outpoint, ":")
	if len(parts) != 2 || !validDiscardHex(parts[0], 32) {
		return "", 0, errors.New("invalid outpoint")
	}
	var vout uint64
	if _, err := fmt.Sscanf(parts[1], "%d", &vout); err != nil || vout > math.MaxUint32 || fmt.Sprint(vout) != parts[1] {
		return "", 0, errors.New("invalid outpoint")
	}
	return parts[0], uint32(vout), nil
}

func validDiscardHex(value string, size int) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == size && value == strings.ToLower(value)
}
