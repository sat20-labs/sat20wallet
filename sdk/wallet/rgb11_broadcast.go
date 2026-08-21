package wallet

import (
	"bytes"
	"errors"
	"fmt"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

const rgb11StatusBroadcastAttempted = "broadcast-attempted"

var ErrRGB11BroadcastResultUnknown = errors.New("RGB11 transaction broadcast result is unknown")

// RGB11BroadcastResultUnknownError means the signed transaction may already
// have reached the Bitcoin network, but the backend response did not establish
// a reliable result. Callers must retain TxID and must not treat this error as
// proof that rebroadcast or cancellation is safe.
type RGB11BroadcastResultUnknownError struct {
	TxID string
	Err  error
}

func (e *RGB11BroadcastResultUnknownError) Error() string {
	if e == nil {
		return ErrRGB11BroadcastResultUnknown.Error()
	}
	return fmt.Sprintf("%s: txid=%s: %v", ErrRGB11BroadcastResultUnknown, e.TxID, e.Err)
}

func (e *RGB11BroadcastResultUnknownError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *RGB11BroadcastResultUnknownError) Is(target error) bool {
	return target == ErrRGB11BroadcastResultUnknown
}

func rgb11BroadcastCompleteStatus(status string) bool {
	switch status {
	case "broadcast", "pending", "settled":
		return true
	default:
		return false
	}
}

// broadcastRGB11PendingBatch persists an irreversible broadcast intent before
// calling the backend. All outcomes are keyed to the locally computed witness
// txid; a backend error or mismatched returned txid can never roll the state
// back to a safely cancellable pre-broadcast status.
func (p *rgb11Manager) broadcastRGB11PendingBatch(
	pendingList []*rgb11wallet.PendingTransfer,
	onBroadcast func(*rgb11wallet.PendingTransfer),
) (string, error) {
	if p == nil || p.projectionStore == nil || p.evidence == nil || len(pendingList) == 0 {
		return "", ErrRGB11Inconsistent
	}
	first := pendingList[0]
	if first == nil || first.State.WitnessTxID == "" || len(first.SignedTx) == 0 {
		return "", ErrRGB11Inconsistent
	}
	expectedTxID := first.State.WitnessTxID
	allComplete := true
	intentChanged := false
	for _, pending := range pendingList {
		if pending == nil || pending.State.TransferID == "" ||
			pending.State.WitnessTxID != expectedTxID ||
			!bytes.Equal(pending.SignedTx, first.SignedTx) {
			return "", ErrRGB11Inconsistent
		}
		if err := validateRGB11PendingTransaction(pending); err != nil {
			return "", err
		}
		if rgb11BroadcastCompleteStatus(pending.State.Status) {
			continue
		}
		allComplete = false
		if pending.State.Status != rgb11StatusBroadcastAttempted {
			pending.State.Status = rgb11StatusBroadcastAttempted
			intentChanged = true
		}
	}
	if allComplete {
		return expectedTxID, nil
	}
	if intentChanged {
		if err := p.projectionStore.SavePendingTransferStates(pendingList); err != nil {
			// No backend call has occurred, so a normal persistence error is
			// sufficient and the caller may safely retry after repairing storage.
			return "", err
		}
		p.autoBackupRGB11AfterMutation()
	}

	// A retry after an ambiguous response first resolves the locally computed
	// witness txid. This avoids treating an already accepted transaction as a
	// fresh operation merely because the previous response was lost.
	_, visible := p.expectedRGB11TransactionStatus(first)
	var backendTxID string
	var broadcastErr error
	if !visible {
		backendTxID, broadcastErr = p.evidence.Broadcast(first.SignedTx)
		if broadcastErr == nil && backendTxID != "" && backendTxID != expectedTxID {
			broadcastErr = fmt.Errorf("RGB11 backend returned witness txid %s, expected %s",
				backendTxID, expectedTxID)
		}
	}
	if broadcastErr != nil {
		// The response may have been lost after the backend accepted the raw
		// transaction. Resolve immediately when evidence already sees it;
		// otherwise retain the durable ambiguous state for reconciliation.
		if _, visible = p.expectedRGB11TransactionStatus(first); !visible {
			p.autoBackupRGB11AfterMutation()
			p.scheduleRGB11ChainReconciliation()
			return expectedTxID, &RGB11BroadcastResultUnknownError{
				TxID: expectedTxID, Err: broadcastErr,
			}
		}
	}

	for _, pending := range pendingList {
		if !rgb11BroadcastCompleteStatus(pending.State.Status) {
			pending.State.Status = "broadcast"
			if onBroadcast != nil {
				onBroadcast(pending)
			}
		}
	}
	if err := p.projectionStore.SavePendingTransferStates(pendingList); err != nil {
		p.autoBackupRGB11AfterMutation()
		p.scheduleRGB11ChainReconciliation()
		return expectedTxID, &RGB11BroadcastPersistenceError{TxID: expectedTxID, Err: err}
	}
	p.autoBackupRGB11AfterMutation()
	p.scheduleRGB11ChainReconciliation()
	return expectedTxID, nil
}
