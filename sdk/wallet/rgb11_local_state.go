package wallet

import (
	"bytes"
	"context"
	"fmt"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// commitRGB11Local publishes a locally constructed operation. Its proof data
// was verified at import/creation; saving a journal does not revalidate history.
func (p *rgb11Manager) commitRGB11Local(ctx context.Context, current, next *RGB11WalletSnapshot) error {
	if current.WalletID != next.WalletID || current.AccountIndex != next.AccountIndex ||
		current.EngineBuildID != next.EngineBuildID {
		return ErrRGB11Inconsistent
	}
	view, err := rgb11wallet.SplitLocalProjection(next.ProjectionRecords, p.rgb11ScopeState().ProofFaults...)
	if err != nil {
		return err
	}
	proofs, err := rgb11wallet.ProofsFromProjectionSnapshot(view.Records)
	if err != nil {
		return err
	}
	transfers, err := rgb11wallet.TransfersFromProjectionSnapshot(view.Records)
	if err != nil {
		return err
	}
	requirements := rgb11ProofConfirmationRequirements(transfers)
	active := rgb11ReservedInputOutpoints(transfers)
	reasons := make(map[string]string, len(proofs))
	for _, proof := range proofs {
		reason := sdkcommon.UtxoLockRGB
		if active[proof.OutPoint] || !rgb11ProofIsAvailable(proof, requirements) {
			reason = sdkcommon.UtxoLockRGBPending
		}
		if reasons[proof.OutPoint] != sdkcommon.UtxoLockRGBPending {
			reasons[proof.OutPoint] = reason
		}
	}
	for point := range view.Points {
		reasons[point] = sdkcommon.UtxoLockRGBPending
	}
	infos, err := p.tickerInfosFromRGB11Snapshot(next)
	if err != nil {
		return err
	}
	_, err = p.publishRGB11CandidateSnapshot(ctx, current, &rgb11ResolvedRecoveryProjection{
		snapshot: next, lockReasons: reasons, tickerInfos: infos, faults: view.Faults, checkEvidence: ctx.Err,
	})
	return err
}

// Check only locally owned carriers. A missing UTXO means spent; there is no
// need to discover its spender or re-fetch the histories of other outputs.
func (p *rgb11Manager) refreshRGB11UTXOs(ctx context.Context) error {
	id, err := p.RGB11WalletID()
	if err != nil {
		return err
	}
	current, err := p.readRGB11Local(id)
	if err != nil {
		return err
	}
	view, err := rgb11wallet.SplitLocalProjection(current.ProjectionRecords)
	if err != nil {
		return err
	}
	proofs, err := rgb11wallet.ProofsFromProjectionSnapshot(view.Records)
	if err != nil {
		return err
	}
	transfers, err := rgb11wallet.TransfersFromProjectionSnapshot(view.Records)
	if err != nil {
		return err
	}
	requirements := rgb11ProofConfirmationRequirements(transfers)
	settledInputs := make(map[string]bool)
	for _, transfer := range transfers {
		if transfer.Direction == "send" && transfer.Status == "settled" {
			for _, point := range transfer.InputOutPoints {
				settledInputs[point] = true
			}
		}
	}
	outputs, err := rgb11wallet.OutputsFromProjectionSnapshot(view.Records)
	if err != nil {
		return err
	}
	observed := make(map[string]*rgb11wallet.BitcoinUTXO)
	var updates []rgb11wallet.SnapshotProofUpdate
	for _, proof := range proofs {
		if proof == nil || proof.Status == "inconsistent" || proof.Status == "orphaned" || settledInputs[proof.OutPoint] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		utxo, found := observed[proof.OutPoint]
		if !found {
			utxo, err = p.evidence.GetUTXO(proof.OutPoint)
			if err != nil {
				return err
			}
			observed[proof.OutPoint] = utxo
		}
		required := max(int64(1), requirements[proof.OutPoint])
		status, confirmations := proof.Status, proof.Confirmations
		if utxo == nil {
			status, confirmations = "spending", 0
		} else {
			output := outputs[proof.OutPoint]
			if output == nil || utxo.OutPoint != proof.OutPoint || utxo.Value != output.OutValue.Value ||
				!bytes.Equal(utxo.PkScript, output.OutValue.PkScript) {
				return fmt.Errorf("%w: RGB carrier does not match its saved output", ErrRGB11Inconsistent)
			}
			// A carrier can predate a blind assignment. Only full resolution may
			// raise its confirmation depth; this check can only invalidate it.
			if status == "spending" || utxo.Confirmations < required {
				status, confirmations = "valid", 0
			} else if status == "settled" && confirmations < required {
				status = "valid"
			}
		}
		// Normal block growth preserves the saved assignment depth and creates
		// no additional snapshot or backup.
		if status != proof.Status || confirmations != proof.Confirmations {
			updates = append(updates, rgb11wallet.SnapshotProofUpdate{OutPoint: proof.OutPoint,
				AssetName: proof.AssetName, Status: status, Confirmations: confirmations})
		}
	}
	if len(updates) == 0 {
		return nil
	}
	records, err := rgb11wallet.UpdateProjectionSnapshotProofs(view.Records, updates)
	if err != nil {
		return err
	}
	next := *current
	next.ProjectionRecords, err = view.Merge(records)
	if err != nil {
		return err
	}
	return p.commitRGB11Local(ctx, current, &next)
}
