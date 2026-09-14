//go:build rgb11repair

package rgb11wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/btcsuite/btcd/wire"
)

// RepairExport contains only the selected RGB projection namespace, never
// wallet secrets. Identity is compared to a separately supplied trusted target.
type RepairExport struct {
	Identity string           `json:"identity"`
	Prefix   string           `json:"prefix"`
	Records  []SnapshotRecord `json:"records"`
}
type SingleKeyRepairTarget struct {
	Identity    string `json:"identity"`
	Prefix      string `json:"prefix"`
	Fingerprint string `json:"fingerprint"`
	TransferID  string `json:"transfer_id"`
	Witness     string `json:"witness"`
	Change      string `json:"change"`
	Successor   string `json:"successor"`
}
type SingleKeyRepairPatch struct {
	Target SingleKeyRepairTarget `json:"target"`
	Key    string                `json:"key"`
	Before []byte                `json:"before"`
	After  []byte                `json:"after"`
}

func RepairExportFingerprint(value RepairExport) string {
	raw, _ := json.Marshal(value)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

// PrepareSingleKeyRepair verifies a closed-profile export and emits one
// status-only patch. No DB writes, snapshot import, or broadcast is possible.
func PrepareSingleKeyRepair(ctx context.Context, value RepairExport, target SingleKeyRepairTarget, evidence BitcoinEvidenceProvider) (*SingleKeyRepairPatch, error) {
	fail := func() (*SingleKeyRepairPatch, error) { return nil, fmt.Errorf("RGB11 repair binding rejected") }
	if evidence == nil || target.Identity == "" || value.Identity != target.Identity || value.Prefix != target.Prefix ||
		target.Prefix == "" || target.TransferID == "" || target.Witness == "" || target.Successor == "" ||
		target.Witness == target.Successor || target.Fingerprint != RepairExportFingerprint(value) {
		return fail()
	}
	records := make(map[string][]byte)
	for _, record := range value.Records {
		if _, exists := records[record.Key]; exists {
			return fail()
		}
		if !strings.HasPrefix(record.Key, "pending-") && !strings.HasPrefix(record.Key, "transfer-") && !strings.HasPrefix(record.Key, "proof-") && !strings.HasPrefix(record.Key, "validation-") && !strings.HasPrefix(record.Key, "object-") {
			return fail()
		}
		records[record.Key] = record.Value
	}
	key := "pending-" + target.TransferID
	var original PendingTransfer
	if err := decode(records[key], &original); err != nil {
		return nil, err
	}
	if original.State.Status != "pending" || original.State.Direction != "send" || original.State.TransferID != target.TransferID || original.State.WitnessTxID != target.Witness {
		return fail()
	}
	if paired := records["transfer-"+target.TransferID]; paired != nil {
		var state TransferState
		if err := decode(paired, &state); err != nil {
			return nil, err
		}
		// Two dirty lifecycle keys are intentionally unsupported.
		if state.Status != "settled" || state.TransferID != target.TransferID || state.WitnessTxID != target.Witness || state.Direction != "send" {
			return fail()
		}
	}
	hydrate := func(p *PendingTransfer) {
		if len(p.LocalConsignment) == 0 {
			p.LocalConsignment = records["object-"+p.LocalObjectHash]
		}
	}
	verifiedTx := func(p *PendingTransfer) error {
		tx := wire.NewMsgTx(wire.TxVersion)
		if err := tx.Deserialize(bytes.NewReader(p.SignedTx)); err != nil {
			return err
		}
		if tx.TxHash().String() != p.State.WitnessTxID {
			return fmt.Errorf("witness mismatch")
		}
		for _, wanted := range p.State.InputOutPoints {
			found := false
			for _, in := range tx.TxIn {
				if in.PreviousOutPoint.String() == wanted {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("input mismatch")
			}
		}
		status, err := evidence.GetTxStatus(p.State.WitnessTxID)
		if err != nil {
			return err
		}
		if status == nil || !status.Confirmed || status.Confirmations < max(int64(p.State.MinConfirmations), 1) {
			return fmt.Errorf("witness not confirmed")
		}
		return nil
	}
	if err := verifiedTx(&original); err != nil {
		return nil, err
	}
	originalBytes := append([]byte(nil), records[key]...)
	hydrated := original
	hydrate(&hydrated)
	hash := sha256.Sum256(hydrated.LocalConsignment)
	var receipt ValidationReceipt
	if err := decode(records["validation-"+hex.EncodeToString(hash[:])], &receipt); err != nil {
		return nil, err
	}
	if err := receipt.validate(hex.EncodeToString(hash[:])); err != nil {
		return nil, err
	}
	receiptHash, err := receipt.Hash()
	if err != nil {
		return nil, err
	}
	var allocation *ValidatedAllocation
	for i := range receipt.Allocations {
		a := &receipt.Allocations[i]
		if a.OutPoint == target.Change && a.WitnessTxPtr && strings.HasPrefix(a.OutPoint, target.Witness+":") {
			allocation = a
		}
	}
	if allocation == nil {
		return fail()
	}
	sealBound := false
	for _, seal := range original.ChangeSeals {
		strict, err := seal.StrictBytes()
		if err == nil && bytes.Equal(strict, allocation.SealDisclosure) && seal.Blinding == allocation.SealBlinding && target.Change == fmt.Sprintf("%s:%d", target.Witness, seal.Vout) {
			sealBound = true
		}
	}
	if !sealBound {
		return fail()
	}
	proofBound := false
	for k, v := range records {
		if !strings.HasPrefix(k, "proof-") {
			continue
		}
		var proof AllocationProof
		if err := decode(v, &proof); err != nil {
			return nil, err
		}
		if proof.OutPoint == target.Change && proof.Status == "spending" && proof.WitnessTxID == target.Witness && proof.ConsignmentHash == receipt.ConsignmentHash && proof.ValidationHash == receiptHash && proof.AssetName == allocation.AssetName && proof.OperationID == allocation.OperationID && proof.AssignmentType == allocation.AssignmentType && proof.AssignmentIndex == allocation.AssignmentIndex && proof.StateClass == allocation.StateClass && bytes.Equal(proof.StateData, allocation.StateData) && bytes.Equal(proof.SealDisclosure, allocation.SealDisclosure) {
			proofBound = true
		}
	}
	if !proofBound {
		return fail()
	}
	spent, err := evidence.GetOutspend(target.Change)
	if err != nil {
		return nil, err
	}
	if spent == nil || !spent.Spent || spent.SpendingTx != target.Successor {
		return fail()
	}
	successorBound := false
	for k, v := range records {
		if !strings.HasPrefix(k, "pending-") {
			continue
		}
		var next PendingTransfer
		if err := decode(v, &next); err != nil {
			return nil, err
		}
		if next.State.Status != "settled" || next.State.Direction != "send" || next.State.WitnessTxID != target.Successor {
			continue
		}
		consumes := false
		for _, input := range next.State.InputOutPoints {
			if input == target.Change {
				consumes = true
			}
		}
		if !consumes {
			continue
		}
		if err := verifiedTx(&next); err != nil {
			return nil, err
		}
		hydrate(&next)
		validator := NewNativeConsensusValidatorWithReveals(next.ChangeSeals...)
		if _, err := ValidateWith(ctx, validator, next.LocalConsignment, evidence); err != nil {
			return nil, err
		}
		successorBound = true
	}
	if !successorBound {
		return fail()
	}
	original.State.Status = "settled"
	after, err := encode(&original)
	if err != nil {
		return nil, err
	}
	return &SingleKeyRepairPatch{Target: target, Key: target.Prefix + key, Before: originalBytes, After: after}, nil
}
