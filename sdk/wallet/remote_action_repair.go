//go:build remoteactionrepair

package wallet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/sat20-labs/sat20wallet/sdk/common"
)

// UnlockWalletForRemoteActionRepair installs wallet keys without starting the
// ordinary post-unlock DKVS/RGB/channel work. The maintenance command must be
// the only process using the profile.
func (p *Manager) UnlockWalletForRemoteActionRepair(password string) (int64, error) {
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
	if err != nil {
		return id, err
	}
	if err := p.rehydratePendingRemoteActionRuntime(); err != nil {
		return id, err
	}
	return id, nil
}

// RemoteActionRepairTarget binds maintenance to one historical action and all
// original transaction identities. It deliberately contains no mutable status.
type RemoteActionRepairTarget struct {
	ReservationID int64  `json:"reservation_id"`
	Action        string `json:"action"`
	SignerPubKey  string `json:"signer_pubkey"`
	FeeTxID       string `json:"fee_txid"`
	CommitTxID    string `json:"commit_txid"`
	RevealTxID    string `json:"reveal_txid"`
}

type RemoteActionRepairPlan struct {
	Target      RemoteActionRepairTarget `json:"target"`
	WalletID    int64                    `json:"wallet_id"`
	SubAccount  uint32                   `json:"sub_account"`
	Status      ResvStatus               `json:"status"`
	Fingerprint string                   `json:"fingerprint"`
}

// InspectRemoteActionRepairTarget reads one persisted reservation and builds
// the exact target template needed by the guarded plan/apply flow. It does not
// unlock a wallet, install runtime signers, change state or contact a node.
func (p *Manager) InspectRemoteActionRepairTarget(reservationID int64) (*RemoteActionRepairTarget, error) {
	if p == nil || p.db == nil || reservationID <= 0 {
		return nil, errors.New("a positive persisted reservation id is required")
	}
	stored, err := LoadReservation(p.db, nil, RESV_TYPE_REMOTEACTION, reservationID)
	if err != nil {
		return nil, fmt.Errorf("load persisted remote action %d: %w", reservationID, err)
	}
	resv, ok := stored.(*RemoteActionPerformReservation)
	if !ok || resv.Id != reservationID || !resv.IsInitiator || resv.Status <= RS_CLOSED {
		return nil, errors.New("target is not a persisted active initiator remote action")
	}
	if resv.Action != REMOTE_ACTION_DEPLOY_RUNES {
		return nil, fmt.Errorf("repair action %s is not supported", resv.Action)
	}
	if _, err := btcec.ParsePubKey(resv.ReqPubKey); err != nil {
		return nil, fmt.Errorf("persisted request pubkey is invalid: %w", err)
	}
	if err := validateRemoteActionRepairTxID("fee", resv.FeeTxId); err != nil {
		return nil, err
	}
	var result RemoteDeployRunesResult
	if err := json.Unmarshal(resv.ActionResult, &result); err != nil {
		return nil, fmt.Errorf("decode original runes result: %w", err)
	}
	if err := validateRemoteActionRepairTxID("commit", result.CommitTxId); err != nil {
		return nil, err
	}
	if err := validateRemoteActionRepairTxID("reveal", result.RevealTxId); err != nil {
		return nil, err
	}
	return &RemoteActionRepairTarget{
		ReservationID: resv.Id,
		Action:        resv.Action,
		SignerPubKey:  hex.EncodeToString(resv.ReqPubKey),
		FeeTxID:       resv.FeeTxId,
		CommitTxID:    result.CommitTxId,
		RevealTxID:    result.RevealTxId,
	}, nil
}

func validateRemoteActionRepairTxID(name, txID string) error {
	trimmed := strings.TrimSpace(txID)
	decoded, err := hex.DecodeString(trimmed)
	if err != nil || len(decoded) != 32 || trimmed != txID {
		return fmt.Errorf("persisted %s transaction id is invalid", name)
	}
	return nil
}

func (p *Manager) PlanRemoteActionRepair(target RemoteActionRepairTarget) (*RemoteActionRepairPlan, error) {
	if target.ReservationID == 0 || target.Action == "" || target.SignerPubKey == "" ||
		target.FeeTxID == "" || target.CommitTxID == "" || target.RevealTxID == "" {
		return nil, errors.New("repair target must include reservation, action, signer, fee, commit and reveal ids")
	}
	if target.Action != REMOTE_ACTION_DEPLOY_RUNES {
		return nil, fmt.Errorf("repair action %s is not supported", target.Action)
	}
	resv := p.GetRemoteAction(target.ReservationID)
	if resv == nil || !resv.IsInitiator || resv.Status <= RS_CLOSED {
		return nil, errors.New("target is not an active initiator remote action")
	}
	if resv.Action != target.Action || resv.FeeTxId != target.FeeTxID {
		return nil, errors.New("target does not match the persisted action or fee transaction")
	}
	signerBytes, err := hex.DecodeString(target.SignerPubKey)
	if err != nil || len(signerBytes) == 0 || !bytesEqual(signerBytes, resv.ReqPubKey) {
		return nil, errors.New("target signer does not match the persisted request pubkey")
	}
	if err := validateRemoteActionRuntimeSigner(resv); err != nil {
		return nil, err
	}
	var result RemoteDeployRunesResult
	if err := json.Unmarshal(resv.ActionResult, &result); err != nil {
		return nil, fmt.Errorf("decode original runes result: %w", err)
	}
	if result.CommitTxId != target.CommitTxID || result.RevealTxId != target.RevealTxID {
		return nil, errors.New("target does not match the original commit or reveal transaction")
	}

	fingerprint, err := remoteActionRepairFingerprint(target, resv)
	if err != nil {
		return nil, err
	}
	return &RemoteActionRepairPlan{
		Target: target, WalletID: resv.WalletId.Id, SubAccount: resv.WalletId.SubAccountId,
		Status: resv.Status, Fingerprint: fingerprint,
	}, nil
}

// ApplyRemoteActionRepair invokes the existing state machine once. It neither
// prepares a request nor constructs or broadcasts a transaction.
func (p *Manager) ApplyRemoteActionRepair(approved RemoteActionRepairPlan) error {
	current, err := p.PlanRemoteActionRepair(approved.Target)
	if err != nil {
		return err
	}
	if current.Fingerprint != approved.Fingerprint || current.WalletID != approved.WalletID ||
		current.SubAccount != approved.SubAccount || current.Status != approved.Status {
		return errors.New("approved repair plan no longer matches the active reservation")
	}
	resv := p.GetRemoteAction(approved.Target.ReservationID)
	if resv == nil {
		return errors.New("target reservation is no longer active")
	}
	if err := p.handleRemoteActionStatus(resv); err != nil {
		return err
	}
	if resv.Status != RS_CLOSED {
		return fmt.Errorf("target reservation %d did not reach the closed state", resv.Id)
	}
	p.notifyActionStatus(&ActionStatusEvent{
		Event:      ACTION_STATUS_EVENT_COMPLETED,
		Resv:       resv,
		ResvType:   RESV_TYPE_REMOTEACTION,
		Action:     resv.Action,
		Status:     RS_PERFORM_ACTION_COMPLETED,
		SendTxInL1: resv.SendTxInL1,
	})
	return nil
}

func remoteActionRepairFingerprint(target RemoteActionRepairTarget, resv *RemoteActionPerformReservation) (string, error) {
	value := struct {
		Target      RemoteActionRepairTarget `json:"target"`
		WalletID    common.WalletId          `json:"wallet_id"`
		Status      ResvStatus               `json:"status"`
		ActionParam []byte                   `json:"action_param"`
		MoreData    []byte                   `json:"more_data"`
		Invoice     []byte                   `json:"invoice"`
		InvoiceSig  []byte                   `json:"invoice_sig"`
		FeeTx       string                   `json:"fee_tx"`
		ActionData  []byte                   `json:"action_data"`
		SendTxInL1  bool                     `json:"send_tx_in_l1"`
		ToBootstrap bool                     `json:"to_bootstrap"`
	}{
		Target: target, WalletID: resv.WalletId, Status: resv.Status,
		ActionParam: resv.ActionParam, MoreData: resv.MoreData,
		Invoice: resv.Invoice, InvoiceSig: resv.InvoiceSig, FeeTx: resv.FeeTx,
		ActionData: resv.ActionResult, SendTxInL1: resv.SendTxInL1,
		ToBootstrap: resv.SendToBootstrapNode,
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

func bytesEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var diff byte
	for i := range left {
		diff |= left[i] ^ right[i]
	}
	return diff == 0
}
