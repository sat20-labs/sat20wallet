//go:build rgb11discard

package wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

var errRGB11OwnerCleared = errors.New("target RGB11 reservation owner is already cleared")

const (
	rgb11OwnerFeeInput  = "0bbc6051be126edb43d04bd390967c19a9f95b4aa8fb67edf12bca1f64d3dfe6:1"
	rgb11OwnerKeepPoint = "c57fb2b8b73f2ee5febb0a593eb681c90eb813c6f7d3500f04d46b711a28589d:1"
	rgb11OwnerKeepAsset = "rgb11:f:r2paid@tkszuzr7"
)

type RGB11StaleOwnerPlan struct {
	Target             RGB11StaleLockTarget `json:"target"`
	WalletID           int64                `json:"wallet_id"`
	SubAccount         uint32               `json:"sub_account"`
	ReceiverAddress    string               `json:"receiver_address"`
	PendingStatus      string               `json:"pending_status"`
	PendingAck         string               `json:"pending_ack"`
	ReservationIDHash  string               `json:"reservation_id_hash"`
	TargetProofStatus  string               `json:"target_proof_status"`
	TargetLockState    string               `json:"target_lock_state"`
	ProtectedOutPoint  string               `json:"protected_outpoint"`
	ProtectedAsset     string               `json:"protected_asset"`
	ProtectedAmountRaw string               `json:"protected_amount_raw"`
	ProtectedProof     string               `json:"protected_proof_status"`
	ProtectedLockTime  int64                `json:"protected_lock_time"`
	SpendEvidence      string               `json:"spend_evidence"`
	SpendingVin        uint32               `json:"spending_vin"`
	SpendBlockHeight   int64                `json:"spend_block_height"`
	SpendBlockHash     string               `json:"spend_block_hash"`
	SpendConfirmations int64                `json:"spend_confirmations"`
	Fingerprint        string               `json:"fingerprint"`
}

type rgb11OwnerSource struct {
	accountID       string
	wallet          common.Wallet
	walletID        int64
	subAccount      uint32
	pending         *rgb11wallet.PendingTransfer
	targetProof     *rgb11wallet.AllocationProof
	targetLock      *LockedUtxo
	protectedProof  *rgb11wallet.AllocationProof
	protectedOutput *indexer.TxOutput
	protectedLock   *LockedUtxo
	protectedAmount *indexer.Decimal
	transfers       []*rgb11wallet.TransferState
	receiveResv     []*rgb11wallet.ReceiveReservation
	reservationJSON []string
	getOutspend     func(string) (*rgb11wallet.BitcoinOutspend, error)
	getRawTx        func(string) ([]byte, error)
	getTxStatus     func(string) (*rgb11wallet.BitcoinTxStatus, error)
}

func (p *Manager) PlanRGB11StaleOwner(target RGB11StaleLockTarget) (*RGB11StaleOwnerPlan, error) {
	source, err := p.rgb11OwnerSource(target)
	if err != nil {
		return nil, err
	}
	return buildRGB11OwnerPlan(target, source, "")
}

func (p *Manager) ApplyRGB11StaleOwner(approved RGB11StaleOwnerPlan) error {
	if approved.Target != rgb11StaleLockOnlyTarget {
		return errors.New("maintenance plan is not for the approved C/4Dmrd owner")
	}
	want, err := rgb11OwnerFingerprint(approved)
	if err != nil || approved.Fingerprint == "" || want != approved.Fingerprint {
		return errors.New("approved stale owner plan fingerprint mismatch")
	}
	source, err := p.rgb11OwnerSource(approved.Target)
	if err != nil {
		return err
	}
	current, planErr := buildRGB11OwnerPlan(approved.Target, source, approved.ReservationIDHash)
	if planErr != nil {
		return planErr
	}
	if current == nil || *current != approved {
		return errors.New("approved stale owner plan no longer matches live evidence")
	}
	if source.pending.ReservationID != "" {
		if err := p.clearRGB11StaleOwner(approved.Target.TransferID, approved.ReservationIDHash); err != nil {
			return err
		}
	}
	readback, err := p.rgbManager.projectionStore.LoadPendingTransfer(approved.Target.TransferID)
	if err != nil || readback.ReservationID != "" {
		if err != nil {
			return err
		}
		return errors.New("target reservation owner clearing was not persisted")
	}
	verified, err := p.rgb11OwnerSource(approved.Target)
	if err != nil {
		return err
	}
	after, err := buildRGB11OwnerPlan(approved.Target, verified, approved.ReservationIDHash)
	if err != nil || after == nil || *after != approved {
		if err != nil {
			return err
		}
		return errors.New("protected RGB11 state changed while clearing owner")
	}
	return nil
}

func (p *Manager) clearRGB11StaleOwner(transferID, ownerHash string) error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil ||
		strings.TrimSpace(transferID) == "" || !validDiscardHex(ownerHash, 32) {
		return ErrRGB11Inconsistent
	}
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return err
	}
	if pending.ReservationID == "" {
		return nil
	}
	digest := sha256.Sum256([]byte(pending.ReservationID))
	if hex.EncodeToString(digest[:]) != ownerHash {
		return errors.New("target reservation owner changed after approval")
	}
	pending.ReservationID = ""
	if err := p.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
		return err
	}
	readback, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return err
	}
	if readback.ReservationID != "" {
		return errors.New("target reservation owner clearing was not persisted")
	}
	return nil
}

func (p *Manager) rgb11OwnerSource(target RGB11StaleLockTarget) (rgb11OwnerSource, error) {
	var source rgb11OwnerSource
	if target != rgb11StaleLockOnlyTarget {
		return source, errors.New("maintenance target is not the approved C/4Dmrd owner")
	}
	if p == nil || p.status == nil || p.wallet == nil || p.rgbManager == nil ||
		p.rgbManager.evidence == nil || p.rgbManager.projectionStore == nil || p.db == nil {
		return source, ErrRGB11Inconsistent
	}
	accountID, err := dkvsAccountID(p.wallet)
	if err != nil || accountID != target.ReceiverAccountID {
		return source, errors.New("active wallet is not the target receiver scope")
	}
	store := p.rgbManager.projectionStore
	pending, err := store.LoadPendingTransfer(target.TransferID)
	if err != nil {
		return source, err
	}
	asset := indexer.NewAssetNameFromString(target.AssetName)
	keepAsset := indexer.NewAssetNameFromString(rgb11OwnerKeepAsset)
	if asset == nil || keepAsset == nil {
		return source, errors.New("fixed RGB11 owner asset is invalid")
	}
	targetProof, err := store.LoadProof(target.OutPoint, *asset)
	if err != nil {
		return source, err
	}
	protectedProof, err := store.LoadProof(rgb11OwnerKeepPoint, *keepAsset)
	if err != nil {
		return source, err
	}
	protectedOutput, err := store.LoadOutput(rgb11OwnerKeepPoint)
	if err != nil {
		return source, err
	}
	protectedAmount, err := store.Balance(*keepAsset)
	if err != nil {
		return source, err
	}
	protectedLock, err := loadRGB11RawLock(p.db, rgb11OwnerKeepPoint)
	if err != nil {
		return source, err
	}
	var targetLock *LockedUtxo
	targetLock, err = loadRGB11RawLock(p.db, target.OutPoint)
	if err != nil && !errors.Is(err, errRGB11LockGone) {
		return source, err
	}
	transfers, err := store.ListTransfers()
	if err != nil {
		return source, err
	}
	receiveResv, err := store.ListReceiveReservations()
	if err != nil {
		return source, err
	}
	allReservations := p.GetAllResv()
	reservationJSON := make([]string, 0, len(allReservations))
	for _, reservation := range allReservations {
		if reservation == nil {
			continue
		}
		raw, marshalErr := json.Marshal(reservation.GetStructInDB())
		if marshalErr != nil {
			return source, marshalErr
		}
		reservationJSON = append(reservationJSON, string(raw))
	}
	return rgb11OwnerSource{
		accountID: accountID, wallet: p.wallet, walletID: p.status.CurrentWallet,
		subAccount: p.wallet.GetSubAccount(), pending: pending, targetProof: targetProof,
		targetLock: targetLock, protectedProof: protectedProof, protectedOutput: protectedOutput,
		protectedLock: protectedLock, protectedAmount: protectedAmount, transfers: transfers,
		receiveResv: receiveResv, reservationJSON: reservationJSON,
		getOutspend: p.rgbManager.evidence.GetOutspend,
		getRawTx:    p.rgbManager.evidence.GetRawTx, getTxStatus: p.rgbManager.evidence.GetTxStatus,
	}, nil
}

func buildRGB11OwnerPlan(target RGB11StaleLockTarget, source rgb11OwnerSource,
	clearedHash string) (*RGB11StaleOwnerPlan, error) {
	if source.wallet == nil || source.accountID != target.ReceiverAccountID || source.pending == nil ||
		source.getOutspend == nil || source.getRawTx == nil || source.getTxStatus == nil {
		return nil, errors.New("stale owner source does not match receiver scope")
	}
	if source.targetLock != nil {
		return nil, errors.New("target RGB11 input lock is not absent")
	}
	pending := source.pending
	state := &pending.State
	asset := indexer.NewAssetNameFromString(target.AssetName)
	if asset == nil || state.TransferID != target.TransferID || state.Direction != "send" ||
		state.Asset.Name != *asset || state.Asset.Amount.Value == nil || state.Asset.Amount.String() != "1" ||
		state.Status != "settled" || state.AckStatus != "accepted" ||
		state.RelayDurability != "STANDARD_PROXY" || state.WitnessTxID != target.SpendingTxID ||
		len(state.InputOutPoints) != 2 || len(state.OutputOutPoints) != 1 ||
		state.OutputOutPoints[0] != target.SpendingTxID+":1" {
		return nil, errors.New("target settled send journal evidence mismatch")
	}
	if err := validateRGB11PendingTransaction(pending); err != nil {
		return nil, err
	}
	targetInputs, feeInputs := 0, 0
	for _, input := range state.InputOutPoints {
		targetInputs += boolInt(input == target.OutPoint)
		feeInputs += boolInt(input == rgb11OwnerFeeInput)
	}
	if targetInputs != 1 || feeInputs != 1 {
		return nil, errors.New("target send journal inputs mismatch")
	}
	reservationHash := clearedHash
	if pending.ReservationID != "" {
		digest := sha256.Sum256([]byte(pending.ReservationID))
		reservationHash = hex.EncodeToString(digest[:])
	} else if reservationHash == "" {
		return nil, errRGB11OwnerCleared
	}
	if !validDiscardHex(reservationHash, 32) {
		return nil, errors.New("target reservation owner hash is invalid")
	}
	if source.targetProof == nil || source.targetProof.OutPoint != target.OutPoint ||
		source.targetProof.AssetName != *asset || source.targetProof.Status != "spending" {
		return nil, errors.New("target historical proof is not exact spending state")
	}
	matchingTransfers := 0
	for _, transfer := range source.transfers {
		if transfer != nil && transfer.TransferID == target.TransferID && transfer.Direction == "send" &&
			(transfer.WitnessTxID != target.SpendingTxID || transfer.Status != "settled") {
			return nil, errors.New("target send journal has contradictory lifecycle state")
		}
		if transfer != nil && transfer.TransferID == target.TransferID && transfer.Direction == "send" {
			matchingTransfers++
		}
	}
	if matchingTransfers != 1 {
		return nil, errors.New("target settled send journal is not unique")
	}
	for _, reservation := range source.receiveResv {
		if reservation != nil && (reservation.OutPoint == target.OutPoint ||
			reservation.ReservationID == pending.ReservationID) {
			return nil, errors.New("target owner still has an RGB receive reservation")
		}
	}
	for _, encoded := range source.reservationJSON {
		if strings.Contains(encoded, target.OutPoint) || strings.Contains(encoded, target.TransferID) ||
			(pending.ReservationID != "" && strings.Contains(encoded, pending.ReservationID)) {
			return nil, errors.New("target owner still has a wallet reservation")
		}
	}
	if err := validateRGB11Protected(source); err != nil {
		return nil, err
	}
	spend, err := verifyRGB11DiscardSpend(RGB11DiscardTarget{
		OutPoint: target.OutPoint, SpendingTxID: target.SpendingTxID,
	}, rgb11DiscardSource{
		getOutspend: source.getOutspend, getRawTx: source.getRawTx, getTxStatus: source.getTxStatus,
	})
	if err != nil {
		return nil, err
	}
	if spend.mode != "confirmed-raw-tx" || spend.vin != 0 {
		return nil, errors.New("target owner lacks confirmed exact spender vin 0")
	}
	chainRaw, err := source.getRawTx(target.SpendingTxID)
	if err != nil || !bytes.Equal(chainRaw, pending.SignedTx) {
		return nil, errors.New("settled send journal raw transaction differs from chain evidence")
	}
	plan := &RGB11StaleOwnerPlan{
		Target: target, WalletID: source.walletID, SubAccount: source.subAccount,
		ReceiverAddress: source.wallet.GetAddress(), PendingStatus: state.Status,
		PendingAck: state.AckStatus, ReservationIDHash: reservationHash,
		TargetProofStatus: source.targetProof.Status, TargetLockState: "absent",
		ProtectedOutPoint: rgb11OwnerKeepPoint, ProtectedAsset: rgb11OwnerKeepAsset,
		ProtectedAmountRaw: source.protectedAmount.String(), ProtectedProof: source.protectedProof.Status,
		ProtectedLockTime: source.protectedLock.LockedTime, SpendEvidence: spend.mode,
		SpendingVin: spend.vin, SpendBlockHeight: spend.blockHeight,
		SpendBlockHash: spend.blockHash, SpendConfirmations: spend.confirmations,
	}
	plan.Fingerprint, err = rgb11OwnerFingerprint(*plan)
	return plan, err
}

func validateRGB11Protected(source rgb11OwnerSource) error {
	asset := indexer.NewAssetNameFromString(rgb11OwnerKeepAsset)
	if asset == nil || source.protectedProof == nil || source.protectedOutput == nil ||
		source.protectedLock == nil || source.protectedAmount == nil || source.protectedAmount.Value == nil {
		return errors.New("protected c57 RGB11 state is incomplete")
	}
	if source.protectedProof.OutPoint != rgb11OwnerKeepPoint || source.protectedProof.AssetName != *asset ||
		source.protectedProof.Status != "settled" || source.protectedAmount.String() != "1" ||
		source.protectedLock.Reason != rgb11wallet.LockReasonRGB || source.protectedLock.ReservationID != "" ||
		source.protectedLock.ReservationPreviousReason != "" || source.protectedLock.Value != 0 ||
		len(source.protectedLock.Assets) != 0 || source.protectedLock.Owner != nil {
		return errors.New("protected c57 RGB11 state mismatch")
	}
	outspend, err := source.getOutspend(rgb11OwnerKeepPoint)
	if err != nil || (outspend != nil && outspend.Spent) {
		return errors.New("protected c57 RGB11 outpoint is not unspent")
	}
	matches := 0
	for _, item := range source.protectedOutput.Assets {
		if item.Name == *asset && item.Amount.Value != nil && item.Amount.String() == "1" {
			matches++
		}
	}
	if matches != 1 {
		return errors.New("protected c57 output allocation mismatch")
	}
	return nil
}

func rgb11OwnerFingerprint(plan RGB11StaleOwnerPlan) (string, error) {
	plan.Fingerprint = ""
	raw, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
