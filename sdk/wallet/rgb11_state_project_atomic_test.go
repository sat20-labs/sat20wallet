package wallet

import (
	"errors"
	"reflect"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func TestRGB11ProjectAllocationRejectsMismatchAndPublishesAtomically(t *testing.T) {
	sender, _, imported, _, _ := newRGB11ValidContractSpendFixture(t)
	sender.rgbManager.scopeStates.stopReconciliations()
	proofs, err := sender.rgbManager.projectionStore.ListProofs()
	if err != nil || len(proofs) != 1 {
		t.Fatalf("load issuer proof: count=%d err=%v", len(proofs), err)
	}
	proof := proofs[0]
	output, err := sender.rgbManager.projectionStore.LoadOutput(proof.OutPoint)
	if err != nil {
		t.Fatal(err)
	}
	amount := output.GetAsset(&imported.AssetName)
	if amount == nil {
		t.Fatal("issuer output lacks imported asset")
	}
	projectedAsset := &indexer.AssetInfo{Name: imported.AssetName, Amount: *amount.Clone()}
	if err := sender.rgbManager.projectionStore.DeleteProjections([]string{proof.OutPoint}); err != nil {
		t.Fatal(err)
	}
	if err := sender.utxoLockerL1.UnlockUtxo(proof.OutPoint); err != nil {
		t.Fatal(err)
	}

	baselineDB := snapshotRGB11ProjectionDB(t, sender.db)
	baselineLocks := sender.utxoLockerL1.GetLockedUtxoList()
	wrongAsset := projectedAsset.Clone()
	wrongAsset.Amount.Value.SetInt64(1)
	if err := sender.rgbManager.ProjectRGB11Allocation(proof.OutPoint, wrongAsset, proof); !errors.Is(err, rgb11wallet.ErrInvalidProof) {
		t.Fatalf("altered allocation amount error=%v, want ErrInvalidProof", err)
	}
	if !reflect.DeepEqual(baselineDB, snapshotRGB11ProjectionDB(t, sender.db)) ||
		!reflect.DeepEqual(baselineLocks, sender.utxoLockerL1.GetLockedUtxoList()) {
		t.Fatal("rejected proof mismatch changed projection records or carrier locks")
	}

	faultDB := &rgb11ProjectionFailOnceDB{KVDB: sender.db}
	sender.db = faultDB
	scope := rgb11StorageScope(sender.status.CurrentWallet, sender.status.CurrentAccount)
	projectionStore := rgb11wallet.NewProjectionStore(faultDB, sender.utxoLockerL1)
	if err := projectionStore.SetScope(scope); err != nil {
		t.Fatal(err)
	}
	projectionStore.SetReservationPersistence(sender.rgbManager)
	engineStore := rgb11wallet.NewEngineStore(faultDB)
	if err := engineStore.SetScope(scope); err != nil {
		t.Fatal(err)
	}
	engineStore.SetReservationPersistence(sender.rgbManager)
	sender.rgbManager.projectionStore = projectionStore
	sender.rgbManager.engineStore = engineStore

	beforeFailedCommit := snapshotRGB11ProjectionDB(t, sender.db)
	beforeFailedLocks := sender.utxoLockerL1.GetLockedUtxoList()
	err = sender.rgbManager.ProjectRGB11Allocation(proof.OutPoint, projectedAsset, proof)
	if !errors.Is(err, errRGB11ProjectionInjectedFlushFailure) {
		t.Fatalf("ProjectRGB11Allocation error=%v, want injected atomic flush failure", err)
	}
	if count := faultDB.flushCount(); count != 1 {
		t.Fatalf("ProjectRGB11Allocation flush count=%d, want 1", count)
	}
	touches := faultDB.failedBatchTouches()
	if touches.projection == 0 || touches.carrierLock == 0 {
		t.Fatalf("failed transaction did not stage proof and lock together: projection=%d locks=%d",
			touches.projection, touches.carrierLock)
	}
	if !reflect.DeepEqual(beforeFailedCommit, snapshotRGB11ProjectionDB(t, sender.db)) {
		t.Fatal("failed ProjectRGB11Allocation flush partially changed durable projection records")
	}
	if !reflect.DeepEqual(beforeFailedLocks, sender.utxoLockerL1.GetLockedUtxoList()) {
		t.Fatal("failed ProjectRGB11Allocation flush partially changed carrier locks")
	}
	if proofs, err := sender.rgbManager.projectionStore.ListProofs(); err != nil || len(proofs) != 0 {
		t.Fatalf("failed ProjectRGB11Allocation published proof rows: count=%d err=%v", len(proofs), err)
	}

	if err := sender.rgbManager.ProjectRGB11Allocation(proof.OutPoint, projectedAsset, proof); err != nil {
		t.Fatalf("retry ProjectRGB11Allocation after transient flush failure: %v", err)
	}
	if count := faultDB.flushCount(); count != 2 {
		t.Fatalf("ProjectRGB11Allocation flush count after retry=%d, want one failed and one successful batch", count)
	}
	storedProof, err := sender.rgbManager.projectionStore.LoadProof(proof.OutPoint, proof.AssetName)
	if err != nil || storedProof == nil || storedProof.OperationID != proof.OperationID ||
		storedProof.AssignmentType != proof.AssignmentType || storedProof.AssignmentIndex != proof.AssignmentIndex {
		t.Fatalf("successful ProjectRGB11Allocation proof=%+v err=%v", storedProof, err)
	}
	storedOutput, err := sender.rgbManager.projectionStore.LoadOutput(proof.OutPoint)
	if err != nil || storedOutput == nil {
		t.Fatalf("successful ProjectRGB11Allocation output=%v err=%v", storedOutput, err)
	}
	storedAsset := storedOutput.GetAsset(&proof.AssetName)
	if storedAsset == nil || storedAsset.Cmp(&projectedAsset.Amount) != 0 {
		t.Fatalf("successful ProjectRGB11Allocation asset=%v, want %s", storedAsset, projectedAsset.Amount.String())
	}
	lock := sender.utxoLockerL1.GetLockedUtxoList()[proof.OutPoint]
	if lock == nil || lock.Reason != rgb11wallet.LockReasonRGB || lock.ReservationID != "" {
		t.Fatalf("successful ProjectRGB11Allocation carrier lock=%+v", lock)
	}
}
