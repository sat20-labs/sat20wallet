package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// Load only a receipt previously validated for these exact local bytes.
func (p *rgb11Manager) loadRGB11HistoricalReceipt(pending *rgb11wallet.PendingTransfer) (*rgb11wallet.ValidationReceipt, error) {
	if err := validateRGB11PendingTransaction(pending); err != nil {
		return nil, err
	}
	hash := sha256.Sum256(pending.LocalConsignment)
	receipt, err := p.rgbManager.projectionStore.LoadValidationReceipt(hex.EncodeToString(hash[:]))
	if err != nil {
		return nil, err
	}
	if receipt.Status != "valid" {
		return nil, fmt.Errorf("%w: RGB11 historical receipt is not valid", ErrRGB11Inconsistent)
	}
	return receipt, nil
}

// This validates history only; it never restores a spent projection or changes
// transfer state. The caller must separately enforce its lifecycle boundary.
func (p *rgb11Manager) validateRGB11SpentChangeHistory(ctx context.Context,
	original *rgb11wallet.PendingTransfer, receipt *rgb11wallet.ValidationReceipt,
	allocation rgb11wallet.ValidatedAllocation, spender string) error {
	if spender == original.State.WitnessTxID {
		return fmt.Errorf("%w: RGB11 spent change names its own transaction as successor", ErrRGB11Inconsistent)
	}
	if spender == "" || spender == "unknown" {
		return fmt.Errorf("RGB11 spent change has no distinct known successor")
	}
	receiptHash, err := receipt.Hash()
	if err != nil {
		return err
	}
	proofs, err := p.rgbManager.projectionStore.ListProofs()
	if err != nil {
		return err
	}
	bound := false
	for _, proof := range proofs {
		if proof.OutPoint == allocation.OutPoint && proof.WitnessTxID == original.State.WitnessTxID &&
			proof.Status == "spending" && proof.ConsignmentHash == receipt.ConsignmentHash && proof.ValidationHash == receiptHash &&
			proof.AssetName == allocation.AssetName && proof.OperationID == allocation.OperationID &&
			proof.AssignmentType == allocation.AssignmentType && proof.AssignmentIndex == allocation.AssignmentIndex &&
			proof.StateClass == allocation.StateClass && bytes.Equal(proof.StateData, allocation.StateData) &&
			bytes.Equal(proof.SealDisclosure, allocation.SealDisclosure) {
			bound = true
		}
	}
	if !bound {
		return fmt.Errorf("%w: RGB11 spent change lacks matching validated history", ErrRGB11Inconsistent)
	}
	transfers, err := p.rgbManager.projectionStore.ListTransfers()
	if err != nil {
		return err
	}
	for _, state := range transfers {
		if state.Direction != "send" || state.Status != "settled" || state.WitnessTxID != spender {
			continue
		}
		consumes := false
		for _, input := range state.InputOutPoints {
			if input == allocation.OutPoint {
				consumes = true
			}
		}
		if !consumes {
			continue
		}
		successor, err := p.rgbManager.projectionStore.LoadPendingTransfer(state.TransferID)
		if err != nil {
			return err
		}
		if err := validateRGB11PendingTransaction(successor); err != nil {
			return err
		}
		status, err := p.rgbManager.evidence.GetTxStatus(spender)
		if err != nil {
			return err
		}
		if status == nil || !status.Confirmed || status.Confirmations < max(int64(state.MinConfirmations), 1) {
			return fmt.Errorf("RGB11 spent change successor is not confirmed")
		}
		validator := rgb11wallet.NewNativeConsensusValidatorWithReveals(successor.ChangeSeals...)
		if _, err := rgb11wallet.ValidateWith(ctx, validator, successor.LocalConsignment, p.rgbManager.evidence); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("RGB11 spent change has no matching settled successor")
}
