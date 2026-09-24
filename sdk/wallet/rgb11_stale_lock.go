//go:build rgb11discard

package wallet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

var errRGB11LockGone = errors.New("target RGB11 lock is absent")

type RGB11StaleLockTarget struct {
	ReceiverAccountID string `json:"receiver_account_id"`
	TransferID        string `json:"transfer_id"`
	AssetName         string `json:"asset_name"`
	OutPoint          string `json:"outpoint"`
	SpendingTxID      string `json:"spending_txid"`
}

type RGB11StaleLockPlan struct {
	Target             RGB11StaleLockTarget `json:"target"`
	WalletID           int64                `json:"wallet_id"`
	SubAccount         uint32               `json:"sub_account"`
	ReceiverAddress    string               `json:"receiver_address"`
	LockTime           int64                `json:"lock_time"`
	LockReason         string               `json:"lock_reason"`
	LockValue          int64                `json:"lock_value"`
	TransferStatus     string               `json:"transfer_status"`
	TransferAck        string               `json:"transfer_ack"`
	TransferAmountRaw  string               `json:"transfer_amount_raw"`
	TransferOutput     string               `json:"transfer_output"`
	SpendEvidence      string               `json:"spend_evidence"`
	SpendingVin        uint32               `json:"spending_vin"`
	SpendBlockHeight   int64                `json:"spend_block_height"`
	SpendBlockHash     string               `json:"spend_block_hash"`
	SpendConfirmations int64                `json:"spend_confirmations"`
	Fingerprint        string               `json:"fingerprint"`
}

var rgb11StaleLockOnlyTarget = RGB11StaleLockTarget{
	ReceiverAccountID: "91cc8c5390f67915dc16bfd3e3ec0cb492e9736252081f6c047bf64b9fb0cf6f",
	TransferID:        "rgb:csg:4DmrdT09-dI626Lj-htOf~RT-v63o0ln-Dw8ggV2-4CRnBgo#egypt-rapid-garage",
	AssetName:         "rgb11:f:r2direct@gvml24je",
	OutPoint:          "69fa4dfed54e66d1d1bdf5b67dacd038a4fe27cc097daabdc9439c4aa508ea1fb8:1",
	SpendingTxID:      "976bb0c68ccb546fc06125e61a5979314393fd79231c70156462ebb9202578d1",
}

type rgb11LockDiscardSource struct {
	accountID           string
	wallet              common.Wallet
	walletID            int64
	subAccount          uint32
	lock                *LockedUtxo
	transfer            *rgb11wallet.TransferState
	transfers           []*rgb11wallet.TransferState
	receiveReservations []*rgb11wallet.ReceiveReservation
	reservationJSON     []string
	balance             *indexer.Decimal
	getOutspend         func(string) (*rgb11wallet.BitcoinOutspend, error)
	getRawTx            func(string) ([]byte, error)
	getTxStatus         func(string) (*rgb11wallet.BitcoinTxStatus, error)
}

type rgb11LockApplyOps struct {
	plan   func() (*RGB11StaleLockPlan, error)
	delete func() error
	absent func() bool
}

func (p *Manager) PlanRGB11StaleLock(target RGB11StaleLockTarget) (*RGB11StaleLockPlan, error) {
	if target != rgb11StaleLockOnlyTarget {
		return nil, errors.New("maintenance target is not the approved C/69fa lock")
	}
	if p == nil || p.status == nil || p.wallet == nil || p.rgbManager == nil || p.rgbManager.evidence == nil ||
		p.rgbManager.projectionStore == nil || p.utxoLockerL1 == nil {
		return nil, ErrRGB11Inconsistent
	}
	accountID, err := dkvsAccountID(p.wallet)
	if err != nil || accountID != target.ReceiverAccountID {
		return nil, errors.New("active wallet is not the target receiver scope")
	}
	asset := indexer.NewAssetNameFromString(target.AssetName)
	if asset == nil || asset.String() != target.AssetName {
		return nil, errors.New("target asset name is invalid")
	}
	var transfer *rgb11wallet.TransferState
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(target.TransferID)
	if err == nil {
		if pending == nil {
			return nil, errors.New("target pending transfer is empty")
		}
		state := pending.State
		transfer = &state
	} else if errors.Is(err, indexer.ErrKeyNotFound) {
		transfer, err = p.rgbManager.projectionStore.LoadTransferState(target.TransferID)
		if err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	transfers, err := p.rgbManager.projectionStore.ListTransfers()
	if err != nil {
		return nil, err
	}
	reservations, err := p.rgbManager.projectionStore.ListReceiveReservations()
	if err != nil {
		return nil, err
	}
	balance, err := p.rgbManager.projectionStore.Balance(*asset)
	if err != nil {
		return nil, err
	}
	allReservations := p.GetAllResv()
	reservationJSON := make([]string, 0, len(allReservations))
	for _, reservation := range allReservations {
		if reservation == nil {
			continue
		}
		raw, marshalErr := json.Marshal(reservation.GetStructInDB())
		if marshalErr != nil {
			return nil, marshalErr
		}
		reservationJSON = append(reservationJSON, string(raw))
	}
	lock, err := loadRGB11RawLock(p.db, target.OutPoint)
	if err != nil {
		return nil, err
	}
	source := rgb11LockDiscardSource{
		accountID: accountID, wallet: p.wallet, walletID: p.status.CurrentWallet,
		subAccount: p.wallet.GetSubAccount(), lock: lock, transfer: transfer,
		transfers: transfers, receiveReservations: reservations,
		reservationJSON: reservationJSON, balance: balance,
		getOutspend: p.rgbManager.evidence.GetOutspend,
		getRawTx:    p.rgbManager.evidence.GetRawTx, getTxStatus: p.rgbManager.evidence.GetTxStatus,
	}
	return buildRGB11LockPlan(target, source)
}

func (p *Manager) ApplyRGB11StaleLock(approved RGB11StaleLockPlan) error {
	if approved.Target != rgb11StaleLockOnlyTarget {
		return errors.New("maintenance plan is not for the approved C/69fa lock")
	}
	if p == nil || p.wallet == nil || p.utxoLockerL1 == nil {
		return ErrRGB11Inconsistent
	}
	accountID, err := dkvsAccountID(p.wallet)
	if err != nil || accountID != approved.Target.ReceiverAccountID {
		return errors.New("active wallet is not the target receiver scope")
	}
	ops := rgb11LockApplyOps{
		plan: func() (*RGB11StaleLockPlan, error) {
			return p.PlanRGB11StaleLock(approved.Target)
		},
		delete: func() error {
			return p.discardRGB11RawLock(approved)
		},
		absent: func() bool {
			_, err := loadRGB11RawLock(p.db, approved.Target.OutPoint)
			return errors.Is(err, errRGB11LockGone)
		},
	}
	return applyRGB11LockDiscard(approved, ops)
}

func buildRGB11LockPlan(target RGB11StaleLockTarget,
	source rgb11LockDiscardSource) (*RGB11StaleLockPlan, error) {
	if source.wallet == nil || source.accountID == "" || source.accountID != target.ReceiverAccountID ||
		source.getOutspend == nil {
		return nil, errors.New("stale lock source does not match receiver scope")
	}
	if !validDiscardHex(target.ReceiverAccountID, 32) || !validDiscardHex(target.SpendingTxID, 32) {
		return nil, errors.New("stale lock target contains an invalid identifier")
	}
	asset := indexer.NewAssetNameFromString(target.AssetName)
	if asset == nil || asset.Protocol != rgb11wallet.Protocol || asset.String() != target.AssetName {
		return nil, errors.New("stale lock target asset is invalid")
	}
	lock := source.lock
	if lock == nil {
		return nil, errRGB11LockGone
	}
	if lock.Reason != rgb11wallet.LockReasonRGB || lock.Value != 0 || len(lock.Assets) != 0 ||
		lock.ReservationID != "" || lock.ReservationPreviousReason != "" || lock.Owner != nil {
		return nil, errors.New("target lock is not the exact ownerless empty RGB lock")
	}
	transfer := source.transfer
	if transfer == nil || transfer.TransferID != target.TransferID || transfer.Direction != "send" ||
		transfer.Asset.Name != *asset || transfer.Asset.Amount.Value == nil ||
		transfer.Asset.Amount.String() != "1" || transfer.Status != "settled" ||
		transfer.AckStatus != "accepted" || transfer.RelayDurability != "STANDARD_PROXY" ||
		transfer.WitnessTxID != target.SpendingTxID || len(transfer.OutputOutPoints) != 1 ||
		transfer.OutputOutPoints[0] != target.SpendingTxID+":1" {
		return nil, errors.New("target settled transfer evidence mismatch")
	}
	inputMatches := 0
	for _, input := range transfer.InputOutPoints {
		if input == target.OutPoint {
			inputMatches++
		}
	}
	if inputMatches != 1 {
		return nil, errors.New("target transfer does not uniquely reference stale lock")
	}
	if source.balance != nil && (source.balance.Value == nil || source.balance.Value.Sign() != 0) {
		return nil, errors.New("target asset balance is not exact zero")
	}
	for _, item := range source.transfers {
		if item != nil && item.Asset.Name == *asset && item.Status != "settled" {
			return nil, errors.New("target asset still has a pending transfer")
		}
	}
	for _, reservation := range source.receiveReservations {
		if reservation != nil && reservation.OutPoint == target.OutPoint {
			return nil, errors.New("target lock still has an RGB receive reservation")
		}
	}
	for _, encoded := range source.reservationJSON {
		if strings.Contains(encoded, target.OutPoint) || strings.Contains(encoded, target.TransferID) {
			return nil, errors.New("target lock still has a wallet reservation")
		}
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
		return nil, errors.New("target stale lock lacks confirmed raw spender vin 0 evidence")
	}
	plan := &RGB11StaleLockPlan{
		Target: target, WalletID: source.walletID, SubAccount: source.subAccount,
		ReceiverAddress: source.wallet.GetAddress(), LockTime: lock.LockedTime,
		LockReason: lock.Reason, LockValue: lock.Value, TransferStatus: transfer.Status,
		TransferAck: transfer.AckStatus, TransferAmountRaw: transfer.Asset.Amount.String(),
		TransferOutput: transfer.OutputOutPoints[0], SpendEvidence: spend.mode,
		SpendingVin: spend.vin, SpendBlockHeight: spend.blockHeight,
		SpendBlockHash: spend.blockHash, SpendConfirmations: spend.confirmations,
	}
	plan.Fingerprint, err = rgb11LockFingerprint(*plan)
	return plan, err
}

func applyRGB11LockDiscard(approved RGB11StaleLockPlan, ops rgb11LockApplyOps) error {
	want, err := rgb11LockFingerprint(approved)
	if err != nil || approved.Fingerprint == "" || want != approved.Fingerprint {
		return errors.New("approved stale lock plan fingerprint mismatch")
	}
	current, planErr := ops.plan()
	if planErr == nil {
		if current == nil || *current != approved {
			return errors.New("approved stale lock plan no longer matches live evidence")
		}
		if err := ops.delete(); err != nil {
			return err
		}
	} else if !errors.Is(planErr, errRGB11LockGone) {
		return planErr
	}
	if ops.absent == nil || !ops.absent() {
		return errors.New("target stale lock deletion was not persisted")
	}
	return nil
}

func rgb11LockFingerprint(plan RGB11StaleLockPlan) (string, error) {
	plan.Fingerprint = ""
	raw, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func loadRGB11RawLock(database indexer.KVDB, outpoint string) (*LockedUtxo, error) {
	if database == nil || strings.TrimSpace(outpoint) == "" {
		return nil, ErrRGB11Inconsistent
	}
	raw, err := database.Read([]byte(GetLockedUtxoKey(L1_NETWORK_BITCOIN, outpoint)))
	if errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, errRGB11LockGone
	}
	if err != nil {
		return nil, err
	}
	var lock LockedUtxo
	if err := DecodeFromBytes(raw, &lock); err != nil {
		return nil, fmt.Errorf("decode target RGB11 lock: %w", err)
	}
	return &lock, nil
}

func (p *Manager) discardRGB11RawLock(plan RGB11StaleLockPlan) error {
	if p == nil || p.db == nil {
		return ErrRGB11Inconsistent
	}
	lock, err := loadRGB11RawLock(p.db, plan.Target.OutPoint)
	if errors.Is(err, errRGB11LockGone) {
		return nil
	}
	if err != nil {
		return err
	}
	if lock.LockedTime != plan.LockTime || lock.Reason != plan.LockReason ||
		lock.Value != plan.LockValue || len(lock.Assets) != 0 || lock.ReservationID != "" ||
		lock.ReservationPreviousReason != "" || lock.Owner != nil {
		return fmt.Errorf("target stale lock changed after approval")
	}
	batch := p.db.NewWriteBatch()
	if batch == nil {
		return errors.New("create stale RGB11 lock deletion batch")
	}
	defer batch.Close()
	if err := batch.Delete([]byte(GetLockedUtxoKey(L1_NETWORK_BITCOIN, plan.Target.OutPoint))); err != nil {
		return err
	}
	marker := time.Now().UnixMilli()
	if previous, loadErr := loadLastLockTime(p.db, L1_NETWORK_BITCOIN); loadErr == nil && marker <= previous {
		marker = previous + 1
	}
	encoded, err := EncodeToBytes(marker)
	if err != nil {
		return err
	}
	if err := batch.Put([]byte(GeLastLockTimeKey(L1_NETWORK_BITCOIN)), encoded); err != nil {
		return err
	}
	if err := batch.Flush(); err != nil {
		return err
	}
	if p.utxoLockerL1 != nil {
		p.utxoLockerL1.Reload("")
	}
	if _, err := loadRGB11RawLock(p.db, plan.Target.OutPoint); !errors.Is(err, errRGB11LockGone) {
		if err == nil {
			return errors.New("target stale lock deletion was not persisted")
		}
		return err
	}
	return nil
}
