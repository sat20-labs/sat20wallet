package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type RGB11RefreshResult struct {
	Settled      int      `json:"settled"`
	Pending      int      `json:"pending"`
	Reorged      int      `json:"reorged"`
	Conflicted   int      `json:"conflicted"`
	Inconsistent []string `json:"inconsistent,omitempty"`
}

// RefreshRGB11State derives lifecycle changes only from Bitcoin facts and the
// wallet's validated local history. Unknown spends are fail-closed and remain
// locked; reorgs roll settled proofs back to valid without deleting history.
func (p *Manager) RefreshRGB11State(ctx context.Context) (*RGB11RefreshResult, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || p.rgbManager.evidence == nil {
		return nil, ErrRGB11Inconsistent
	}
	result := &RGB11RefreshResult{}
	transfers, err := p.rgbManager.projectionStore.ListTransfers()
	if err != nil {
		return nil, err
	}
	expectedSpends := make(map[string]string)
	for _, state := range transfers {
		if state.Direction == "receive" {
			status, err := p.rgbManager.evidence.GetTxStatus(state.WitnessTxID)
			if err != nil {
				return nil, err
			}
			settled := status != nil && status.Confirmed && status.Confirmations >= int64(state.MinConfirmations)
			if settled {
				if state.Status != "settled" {
					result.Settled++
				}
				state.Status = "settled"
			} else {
				if state.Status == "settled" {
					result.Reorged++
				}
				state.Status = "pending"
				result.Pending++
			}
			if err := p.rgbManager.projectionStore.SaveTransferState(state); err != nil {
				return nil, err
			}
			continue
		}
		if state.Status == "broadcast" || state.Status == "pending" || state.Status == "settled" {
			for _, outpoint := range state.InputOutPoints {
				expectedSpends[outpoint] = state.WitnessTxID
			}
		}
		if state.Status != "broadcast" && state.Status != "pending" && state.Status != "settled" {
			continue
		}
		pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(state.TransferID)
		if err != nil {
			return nil, err
		}
		status, err := p.rgbManager.evidence.GetTxStatus(state.WitnessTxID)
		if err != nil {
			return nil, err
		}
		if status != nil && (status.InMempool || status.Confirmed) {
			if err := p.applyRGB11LocalChange(ctx, pending, status); err != nil {
				// Indexers may observe the witness before outspends/UTXOs are
				// queryable. Keep the transfer pending and retry next refresh.
				pending.State.Status = "pending"
				_ = p.rgbManager.projectionStore.SavePendingTransferState(pending)
				result.Pending++
				continue
			}
			if status.Confirmed && status.Confirmations >= int64(state.MinConfirmations) {
				if pending.State.Status != "settled" {
					result.Settled++
				}
				pending.State.Status = "settled"
			} else {
				if pending.State.Status == "settled" {
					result.Reorged++
				}
				pending.State.Status = "pending"
				result.Pending++
			}
			if err := p.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
				return nil, err
			}
			if pending.State.Status == "settled" {
				transferIDs := pending.State.BatchTransferIDs
				if len(transferIDs) == 0 {
					transferIDs = []string{pending.State.TransferID}
				}
				if err := p.rgbManager.projectionStore.CompactSettledRecipientConsignments(transferIDs); err != nil {
					return nil, err
				}
			}
			continue
		}
		conflicted := false
		for _, outpoint := range state.InputOutPoints {
			outspend, err := p.rgbManager.evidence.GetOutspend(outpoint)
			if err != nil {
				return nil, err
			}
			if outspend != nil && outspend.Spent && outspend.SpendingTx != state.WitnessTxID {
				conflicted = true
				result.Inconsistent = append(result.Inconsistent, outpoint)
			}
		}
		if conflicted {
			pending.State.Status = "conflicted"
			pending.State.AckStatus = "invalidated"
			result.Conflicted++
		} else if pending.State.Status == "settled" {
			pending.State.Status = "broadcast"
			result.Reorged++
		}
		if err := p.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
			return nil, err
		}
	}

	proofs, err := p.rgbManager.projectionStore.ListProofs()
	if err != nil {
		return nil, err
	}
	for _, proof := range proofs {
		outspend, err := p.rgbManager.evidence.GetOutspend(proof.OutPoint)
		if err != nil {
			return nil, err
		}
		if outspend != nil && outspend.Spent {
			if expectedSpends[proof.OutPoint] == outspend.SpendingTx {
				proof.Status = "pending"
			} else {
				proof.Status = "inconsistent"
				result.Inconsistent = append(result.Inconsistent, proof.OutPoint)
				_ = p.utxoLockerL1.LockUtxo(proof.OutPoint, rgb11wallet.LockReasonRGB)
			}
			if err := p.rgbManager.projectionStore.SaveProofState(proof); err != nil {
				return nil, err
			}
			continue
		}
		status, err := p.rgbManager.evidence.GetTxStatus(proof.WitnessTxID)
		if err != nil {
			return nil, err
		}
		wasSettled := proof.Status == "settled"
		if status != nil && status.Confirmed {
			proof.Status = "settled"
			proof.Confirmations = status.Confirmations
		} else {
			proof.Status = "valid"
			proof.Confirmations = 0
			if wasSettled {
				result.Reorged++
			}
		}
		if err := p.rgbManager.projectionStore.SaveProofState(proof); err != nil {
			return nil, err
		}
	}
	if len(result.Inconsistent) > 0 {
		p.rgbManager.consistencyStatus = "broken"
		return result, fmt.Errorf("%w: unknown or conflicting RGB11 spend", ErrRGB11Inconsistent)
	}
	p.rgbManager.consistencyStatus = "ok"
	p.autoBackupRGB11AfterMutation()
	return result, nil
}

func (p *Manager) applyRGB11LocalChange(ctx context.Context, pending *rgb11wallet.PendingTransfer,
	status *rgb11wallet.BitcoinTxStatus) error {
	validator := rgb11wallet.NewNativeConsensusValidatorWithReveals(pending.ChangeSeals...)
	receipt, err := p.rgbManager.projectionStore.ValidateAndStoreConsignment(ctx, validator, p.rgbManager.evidence, pending.LocalConsignment)
	if err != nil {
		return err
	}
	receiptHash, err := receipt.Hash()
	if err != nil {
		return err
	}
	replacements := make([]rgb11wallet.ProjectionReplacement, 0)
	wantPrefix := pending.State.WitnessTxID + ":"
	for _, allocation := range receipt.Allocations {
		if !strings.HasPrefix(allocation.OutPoint, wantPrefix) || !allocation.WitnessTxPtr {
			continue
		}
		matched := false
		for _, changeSeal := range pending.ChangeSeals {
			if changeSeal.Vout == outpointVoutMust(allocation.OutPoint) && changeSeal.Blinding == allocation.SealBlinding {
				strict, strictErr := changeSeal.StrictBytes()
				matched = strictErr == nil && bytes.Equal(strict, allocation.SealDisclosure)
				if matched {
					break
				}
			}
		}
		if !matched {
			continue
		}
		utxo, err := p.rgbManager.evidence.GetUTXO(allocation.OutPoint)
		if err != nil || utxo == nil {
			return fmt.Errorf("resolve RGB11 change %s: %w", allocation.OutPoint, err)
		}
		output := indexer.NewTxOutput(utxo.Value)
		output.OutPointStr = allocation.OutPoint
		output.OutValue.PkScript = append([]byte(nil), utxo.PkScript...)
		binding, err := p.rgb11CarrierBinding(allocation, utxo)
		if err != nil {
			return err
		}
		asset := &indexer.AssetInfo{Name: allocation.AssetName, Amount: *allocation.Amount.Clone(), BindingSat: 0}
		commitment := consensus.TaggedHash(consensus.SecretSealCommitmentTag, allocation.SealDisclosure)
		proofStatus := "valid"
		confirmations := int64(0)
		if status != nil && status.Confirmed {
			proofStatus, confirmations = "settled", status.Confirmations
		}
		proof := &rgb11wallet.AllocationProof{
			OutPoint: allocation.OutPoint, AssetName: allocation.AssetName,
			OperationID: allocation.OperationID, AssignmentType: allocation.AssignmentType,
			AssignmentIndex: allocation.AssignmentIndex, StateClass: allocation.StateClass,
			StateData:       append([]byte(nil), allocation.StateData...),
			SealCommitment:  hex.EncodeToString(commitment[:]),
			SealDisclosure:  append([]byte(nil), allocation.SealDisclosure...),
			ConsignmentHash: receipt.ConsignmentHash, ValidationHash: receiptHash,
			WitnessTxID: pending.State.WitnessTxID, Status: proofStatus, Confirmations: confirmations,
			CarrierBinding: binding,
		}
		replacements = append(replacements, rgb11wallet.ProjectionReplacement{Output: output, Asset: asset, Proof: proof})
	}
	return p.rgbManager.projectionStore.ReplaceProjections(pending.State.InputOutPoints, replacements)
}

func outpointVoutMust(outpoint string) uint32 {
	vout, _ := outpointVout(outpoint)
	return vout
}
