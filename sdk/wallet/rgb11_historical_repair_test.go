package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// Test-only offline adapter: the manager is a separately loaded isolated copy,
// never the running wallet. No snapshot import or live DB write is performed.
func planRGB11HistoricalRepairCopy(ctx context.Context, manager *Manager,
	snapshot *rgb11wallet.RGB11WalletSnapshot, target rgb11wallet.HistoricalRepairTarget,
	expectedAddress, expectedEnv, expectedNetwork, change, spender string,
) (*rgb11wallet.HistoricalRepairPlan, *rgb11wallet.RGB11WalletSnapshot, error) {
	if manager.cfg == nil || manager.cfg.Env != expectedEnv || manager.cfg.Chain != expectedNetwork ||
		expectedAddress == "" || manager.wallet.GetAddress() != expectedAddress ||
		change == "" || spender == "" {
		return nil, nil, fmt.Errorf("offline repair identity mismatch")
	}
	walletID, err := manager.RGB11WalletID()
	if err != nil || walletID != target.WalletID || uint32(manager.status.CurrentAccount) != target.AccountIndex {
		return nil, nil, fmt.Errorf("offline repair wallet mismatch")
	}
	loaded, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		return nil, nil, err
	}
	loadedHash, err := rgb11wallet.HistoricalRepairSnapshotHash(loaded)
	if err != nil || loadedHash != target.SnapshotHash {
		return nil, nil, fmt.Errorf("offline verifier copy fingerprint mismatch")
	}
	return rgb11wallet.PlanHistoricalRepair(snapshot, target, func(pending *rgb11wallet.PendingTransfer) error {
		if err := validateRGB11PendingTransaction(pending); err != nil {
			return err
		}
		status, err := manager.rgbManager.evidence.GetTxStatus(pending.State.WitnessTxID)
		if err != nil {
			return err
		}
		if status == nil || !status.Confirmed || status.Confirmations < max(int64(pending.State.MinConfirmations), 1) {
			return fmt.Errorf("original witness not confirmed")
		}
		spent, err := manager.rgbManager.evidence.GetOutspend(change)
		if err != nil {
			return err
		}
		if spent == nil || !spent.Spent || spent.SpendingTx != spender {
			return fmt.Errorf("offline repair spender mismatch")
		}
		receipt, err := manager.rgbManager.loadRGB11HistoricalReceipt(pending)
		if err != nil {
			return err
		}
		for _, allocation := range receipt.Allocations {
			if allocation.OutPoint != change || !allocation.WitnessTxPtr {
				continue
			}
			matched := false
			for _, seal := range pending.ChangeSeals {
				if seal.Vout == outpointVoutMust(change) && seal.Blinding == allocation.SealBlinding {
					strict, strictErr := seal.StrictBytes()
					matched = strictErr == nil && bytes.Equal(strict, allocation.SealDisclosure)
				}
			}
			if !matched {
				return fmt.Errorf("offline repair change seal mismatch")
			}
			return manager.rgbManager.validateRGB11SpentChangeHistory(ctx, pending, receipt, allocation, spender)
		}
		return fmt.Errorf("offline repair allocation missing")
	})
}

// Apply publishes one complete, verified candidate to a NEW offline artifact.
// Hard-link publication is atomic and refuses an existing destination; no
// profile DB import is available through this adapter.
func applyRGB11HistoricalRepairCopy(output string, approved *rgb11wallet.HistoricalRepairPlan,
	revalidate func() (*rgb11wallet.HistoricalRepairPlan, *rgb11wallet.RGB11WalletSnapshot, error)) error {
	plan, candidate, err := revalidate()
	if err != nil {
		return err
	}
	if approved == nil || plan.BeforeHash != approved.BeforeHash || plan.AfterHash != approved.AfterHash || plan.TransferID != approved.TransferID {
		return fmt.Errorf("offline repair approved plan changed")
	}
	raw, err := json.Marshal(candidate)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(output), ".rgb11-repair-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Link(file.Name(), output)
}

func assertRGB11HistoricalRepairDryRun(t *testing.T, manager *Manager, first, second *rgb11wallet.PendingTransfer, change string) {
	t.Helper()
	walletID, err := manager.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := rgb11wallet.HistoricalRepairSnapshotHash(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	target := rgb11wallet.HistoricalRepairTarget{WalletID: walletID, AccountIndex: uint32(manager.status.CurrentAccount), SnapshotHash: fingerprint, TransferID: first.State.TransferID, WitnessTxID: first.State.WitnessTxID}
	plan := func(s *rgb11wallet.RGB11WalletSnapshot, target rgb11wallet.HistoricalRepairTarget, spender string) (*rgb11wallet.HistoricalRepairPlan, *rgb11wallet.RGB11WalletSnapshot, error) {
		return planRGB11HistoricalRepairCopy(context.Background(), manager, s, target, manager.wallet.GetAddress(), manager.cfg.Env, manager.cfg.Chain, change, spender)
	}
	proposal, candidate, err := plan(snapshot, target, second.State.WitnessTxID)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.BeforeHash != fingerprint || proposal.AfterHash == fingerprint {
		t.Fatal("invalid repair fingerprint plan")
	}
	unchanged, _ := rgb11wallet.HistoricalRepairSnapshotHash(snapshot)
	if unchanged != fingerprint {
		t.Fatal("dry run modified original snapshot")
	}
	old, err := manager.rgbManager.projectionStore.LoadPendingTransfer(first.State.TransferID)
	if err != nil || old.State.Status != "pending" {
		t.Fatal("dry run wrote manager state")
	}
	allowed := make(map[string]bool)
	for _, key := range proposal.ChangedKeys {
		allowed[key] = true
	}
	if len(allowed) < 1 || len(allowed) > 2 {
		t.Fatal("unexpected repair scope")
	}
	for i, record := range snapshot.ProjectionRecords {
		other := candidate.ProjectionRecords[i]
		if record.Key != other.Key {
			t.Fatal("repair changed key set")
		}
		if string(record.Value) != string(other.Value) && !allowed[record.Key] {
			t.Fatalf("repair changed unrelated key %s", record.Key)
		}
	}
	output := filepath.Join(t.TempDir(), "repaired-copy.json")
	revalidate := func() (*rgb11wallet.HistoricalRepairPlan, *rgb11wallet.RGB11WalletSnapshot, error) {
		return plan(snapshot, target, second.State.WitnessTxID)
	}
	if err := applyRGB11HistoricalRepairCopy(output, proposal, revalidate); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var published rgb11wallet.RGB11WalletSnapshot
	if err := json.Unmarshal(written, &published); err != nil {
		t.Fatal(err)
	}
	publishedHash, err := rgb11wallet.HistoricalRepairSnapshotHash(&published)
	if err != nil || publishedHash != proposal.AfterHash {
		t.Fatal("atomic artifact differs from approved candidate")
	}
	if err := applyRGB11HistoricalRepairCopy(output, proposal, revalidate); err == nil {
		t.Error("overwrote existing repair artifact")
	}
	wrong := target
	wrong.SnapshotHash = "wrong"
	if _, _, err := plan(snapshot, wrong, second.State.WitnessTxID); err == nil {
		t.Error("accepted changed fingerprint")
	}
	wrong = target
	wrong.TransferID = "wrong"
	if _, _, err := plan(snapshot, wrong, second.State.WitnessTxID); err == nil {
		t.Error("accepted wrong target")
	}
	wrong = target
	wrong.AccountIndex++
	if _, _, err := plan(snapshot, wrong, second.State.WitnessTxID); err == nil {
		t.Error("accepted wrong scope")
	}
	if _, _, err := plan(snapshot, target, first.State.WitnessTxID); err == nil {
		t.Error("accepted wrong successor")
	}
	if _, _, err := plan(candidate, target, second.State.WitnessTxID); err == nil {
		t.Error("accepted stale apply plan")
	}
	target.SnapshotHash = proposal.AfterHash
	if _, _, err := plan(candidate, target, second.State.WitnessTxID); err == nil {
		t.Error("accepted already repaired target")
	}
}
