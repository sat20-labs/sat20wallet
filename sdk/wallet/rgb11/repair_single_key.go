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
	corewallet "github.com/sat20-labs/rgb11/wallet"
)

// RepairExport contains only the selected RGB projection namespace, never
// wallet secrets. Identity is compared to a separately supplied trusted target.
type RepairExport struct {
	Identity string           `json:"identity"`
	Prefix   string           `json:"prefix"`
	Records  []SnapshotRecord `json:"records"`
}

// OrphanReceiveRepairTarget binds an offline repair to one exact damaged
// self-receive. The separately supplied verifier must prove the witness is
// absent from both the configured chain and mempool.
type OrphanReceiveRepairTarget struct {
	WalletID        string `json:"wallet_id"`
	AccountIndex    uint32 `json:"account_index"`
	SnapshotHash    string `json:"snapshot_hash"`
	RequestID       string `json:"request_id"`
	TransferID      string `json:"transfer_id"`
	WitnessTxID     string `json:"witness_txid"`
	ConsignmentHash string `json:"consignment_hash"`
}

type OrphanReceiveRepairPlan struct {
	Target      OrphanReceiveRepairTarget   `json:"target"`
	BeforeHash  string                      `json:"before_hash"`
	AfterHash   string                      `json:"after_hash"`
	ChangedKeys []string                    `json:"changed_keys"`
	Changes     []OrphanReceiveRepairChange `json:"changes"`
}

type OrphanReceiveRepairChange struct {
	Store        string `json:"store"`
	Key          string `json:"key"`
	BeforeSHA256 string `json:"before_sha256"`
	AfterSHA256  string `json:"after_sha256,omitempty"`
	Delete       bool   `json:"delete,omitempty"`
}

// PlanOrphanReceiveRepair is a pure offline transform. It makes an acknowledged
// request reusable only when its object is absent, the paired sender was
// explicitly rejected, and independent chain evidence proves no witness exists.
func PlanOrphanReceiveRepair(snapshot *RGB11WalletSnapshot, target OrphanReceiveRepairTarget,
	verifyEvidence func(*PendingTransfer) error) (*OrphanReceiveRepairPlan, *RGB11WalletSnapshot, error) {
	fail := func() (*OrphanReceiveRepairPlan, *RGB11WalletSnapshot, error) {
		return nil, nil, fmt.Errorf("RGB11 orphan receive repair binding rejected")
	}
	if snapshot == nil || verifyEvidence == nil || target.WalletID == "" ||
		target.RequestID == "" || target.TransferID == "" || target.WitnessTxID == "" ||
		target.ConsignmentHash == "" || snapshot.WalletID != target.WalletID ||
		snapshot.AccountIndex != target.AccountIndex {
		return fail()
	}
	before, err := HistoricalRepairSnapshotHash(snapshot)
	if err != nil || before != target.SnapshotHash {
		return fail()
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, nil, err
	}
	var candidate RGB11WalletSnapshot
	if err := json.Unmarshal(raw, &candidate); err != nil {
		return nil, nil, err
	}
	projection := make(map[string]int, len(candidate.ProjectionRecords))
	for index, record := range candidate.ProjectionRecords {
		if _, exists := projection[record.Key]; record.Key == "" || len(record.Value) == 0 || exists {
			return fail()
		}
		projection[record.Key] = index
	}
	engine := make(map[string]int, len(candidate.EngineRecords))
	for index, record := range candidate.EngineRecords {
		if _, exists := engine[record.Key]; record.Key == "" || len(record.Value) == 0 || exists {
			return fail()
		}
		engine[record.Key] = index
	}
	requestKey := "wallet/receive/" + target.RequestID
	requestIndex, ok := engine[requestKey]
	if !ok {
		return fail()
	}
	request, err := corewallet.DecodeReceiveRequest(candidate.EngineRecords[requestIndex].Value)
	if err != nil || request.Status != corewallet.ReceiveAcknowledged || request.RequestID != target.RequestID ||
		request.TransferID != target.TransferID || request.WitnessTxID != target.WitnessTxID ||
		request.ObjectHash != target.ConsignmentHash {
		return fail()
	}
	requestBefore := append([]byte(nil), candidate.EngineRecords[requestIndex].Value...)
	transferKey := "transfer-" + target.TransferID
	transferIndex, ok := projection[transferKey]
	if !ok {
		return fail()
	}
	var receive TransferState
	if err := decode(candidate.ProjectionRecords[transferIndex].Value, &receive); err != nil ||
		receive.Direction != "receive" || receive.Status != "awaiting_broadcast" ||
		receive.TransferID != target.TransferID || receive.WitnessTxID != target.WitnessTxID ||
		receive.ConsignmentHash != target.ConsignmentHash {
		return fail()
	}
	preparedKey := "prepared-receive-" + target.TransferID
	preparedIndex, ok := projection[preparedKey]
	if !ok || string(candidate.ProjectionRecords[preparedIndex].Value) != target.RequestID {
		return fail()
	}
	if _, exists := projection["object-"+target.ConsignmentHash]; exists {
		return fail()
	}
	validationKey := "validation-" + target.ConsignmentHash
	validationIndex, hasValidation := projection[validationKey]
	if hasValidation {
		var receipt ValidationReceipt
		if err := decode(candidate.ProjectionRecords[validationIndex].Value, &receipt); err != nil ||
			receipt.validate(target.ConsignmentHash) != nil ||
			receipt.TransferID != target.TransferID {
			return fail()
		}
	}
	if senderIndex, ok := projection["pending-"+target.TransferID]; !ok {
		return fail()
	} else {
		var sender PendingTransfer
		if err := decode(candidate.ProjectionRecords[senderIndex].Value, &sender); err != nil ||
			sender.State.Direction != "send" || sender.State.Status != "rejected" ||
			sender.State.RejectReason != "user-rejected" || sender.State.WitnessTxID != target.WitnessTxID ||
			len(sender.SignedTx) != 0 || len(sender.SignedPSBT) != 0 ||
			len(sender.RecipientConsignment) != 0 || len(sender.LocalConsignment) != 0 ||
			sender.RecipientObjectHash != "" || sender.LocalObjectHash != "" {
			return fail()
		}
		if err := verifyEvidence(&sender); err != nil {
			return nil, nil, err
		}
	}
	request.Status = corewallet.ReceivePrepared
	request.TransferID = ""
	request.ObjectHash = ""
	request.WitnessTxID = ""
	request.FailureCode = ""
	encoded, err := corewallet.EncodeReceiveRequest(request)
	if err != nil {
		return nil, nil, err
	}
	candidate.EngineRecords[requestIndex].Value = encoded
	remove := map[int]bool{transferIndex: true, preparedIndex: true}
	changed := []string{"engine/" + requestKey, "projection/" + transferKey, "projection/" + preparedKey}
	changes := []OrphanReceiveRepairChange{
		orphanRepairChange("engine", requestKey, requestBefore, encoded, false),
		orphanRepairChange("projection", transferKey, snapshot.ProjectionRecords[transferIndex].Value, nil, true),
		orphanRepairChange("projection", preparedKey, snapshot.ProjectionRecords[preparedIndex].Value, nil, true),
	}
	if hasValidation {
		remove[validationIndex] = true
		changed = append(changed, "projection/"+validationKey)
		changes = append(changes, orphanRepairChange("projection", validationKey, snapshot.ProjectionRecords[validationIndex].Value, nil, true))
	}
	filtered := make([]SnapshotRecord, 0, len(candidate.ProjectionRecords)-len(remove))
	for index, record := range candidate.ProjectionRecords {
		if !remove[index] {
			filtered = append(filtered, record)
		}
	}
	candidate.ProjectionRecords = filtered
	if err := ValidateWalletSnapshot(&candidate); err != nil {
		return nil, nil, err
	}
	after, err := HistoricalRepairSnapshotHash(&candidate)
	if err != nil || after == before {
		return fail()
	}
	plan := &OrphanReceiveRepairPlan{Target: target, BeforeHash: before, AfterHash: after, ChangedKeys: changed, Changes: changes}
	return plan, &candidate, nil
}

// ApplyOrphanReceiveRepair revalidates every safety condition and only returns
// the candidate matching the explicitly approved plan. It has no DB handle.
func ApplyOrphanReceiveRepair(snapshot *RGB11WalletSnapshot, approved *OrphanReceiveRepairPlan,
	verifyEvidence func(*PendingTransfer) error) (*RGB11WalletSnapshot, error) {
	if approved == nil {
		return nil, fmt.Errorf("RGB11 orphan receive repair approval missing")
	}
	plan, candidate, err := PlanOrphanReceiveRepair(snapshot, approved.Target, verifyEvidence)
	if err != nil {
		return nil, err
	}
	if plan.BeforeHash != approved.BeforeHash || plan.AfterHash != approved.AfterHash ||
		!bytes.Equal(mustJSON(plan.ChangedKeys), mustJSON(approved.ChangedKeys)) ||
		!bytes.Equal(mustJSON(plan.Changes), mustJSON(approved.Changes)) {
		return nil, fmt.Errorf("RGB11 orphan receive repair approved plan changed")
	}
	return candidate, nil
}

func orphanRepairChange(store, key string, before, after []byte, deleted bool) OrphanReceiveRepairChange {
	beforeHash := sha256.Sum256(before)
	change := OrphanReceiveRepairChange{
		Store: store, Key: key, BeforeSHA256: hex.EncodeToString(beforeHash[:]), Delete: deleted,
	}
	if !deleted {
		afterHash := sha256.Sum256(after)
		change.AfterSHA256 = hex.EncodeToString(afterHash[:])
	}
	return change
}

func mustJSON(value any) []byte {
	raw, _ := json.Marshal(value)
	return raw
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
