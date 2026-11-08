package wallet

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func TestRGB11ConflictCandidateOlderBundleReevaluation(t *testing.T) {
	var cold *Manager
	f := newRGB11ConflictFixture(t, false, func(source *Manager, remote *rgb11MemoryDKVSHTTP,
		evidence *rgb11AddressEvidence, rpc *rgb11FlowIndexer) {
		cold = newRGBColdDevice(t, source, remote, evidence, rpc, coreE2ESenderMnemonic, "password")
	})
	old, err := rgb11wallet.RecoveryPackageFromSnapshot(f.before, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.publish(t, true)
	// Crash boundary: only the pre-conflict complete account bundle survived.
	// No proof state or new journal marker may be borrowed from live RAM.
	snap, err := old.WalletSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	assertRGBColdEmpty(t, cold)
	cold.rgbManager.scopeStates.stopReconciliations()
	if err = cold.rgbManager.importSelectedRGB11AccountRecoverySnapshot(snap); err != nil {
		t.Fatalf("old complete bundle safe import: %v", err)
	}
	p, err := cold.rgbManager.projectionStore.LoadPendingTransfer(f.pending.State.TransferID)
	if err != nil || p.State.Status != "conflicted" || p.State.AckStatus != "invalidated" {
		t.Fatal("cold import did not derive journal conflict from old bundle")
	}
	proof, err := cold.rgbManager.projectionStore.LoadProof(f.carrier, f.asset)
	if err != nil || proof.Status != "inconsistent" || cold.GetRGB11ConsistencyStatus() != "broken" {
		t.Fatal("cold import did not derive quarantine")
	}
}
func TestRGB11ConflictCandidateRGBMempool(t *testing.T) {
	f := newRGB11ConflictFixture(t, false)
	f.publish(t, false)
	_, _ = f.sender.RefreshRGB11State(context.Background())
	if f.journal(t).State.Status == "conflicted" {
		t.Fatal("unconfirmed RGB spender retired journal")
	}
	f.assertProtected(t)
	f.restoreInputs(t)
	if _, err := f.sender.RefreshRGB11State(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.assertProtected(t)
}
func TestRGB11ConflictCandidateWrongRawInput(t *testing.T) {
	f := newRGB11ConflictFixture(t, true)
	f.publish(t, true)
	// Rival is a real signed fee-only transaction, but indexer falsely claims
	// that it also consumed the RGB carrier. Exact raw/input binding must fail.
	f.evidence.mu.Lock()
	f.evidence.spendingTx[f.carrier] = f.rival.TxHash().String()
	delete(f.evidence.utxos, f.carrier)
	delete(f.rpc.outputs, f.carrier)
	f.evidence.mu.Unlock()
	if _, err := f.sender.RefreshRGB11State(context.Background()); err == nil {
		t.Fatal("raw that omits actual carrier was accepted")
	}
	if f.journal(t).State.Status != "broadcast" {
		t.Fatal("bad outspend committed conflict")
	}
	f.assertProtected(t)
	f.assertImmutable(t)
}

var errRGB11ConflictMarkerFailure = errors.New("injected account marker write failure")

type rgb11ConflictMarkerFailureDB struct {
	indexer.KVDB
	fixture   *rgb11ConflictFixture
	failed    bool
	moduleKey []byte
	rgbPrefix []byte
}

type rgb11ConflictMarkerFailureBatch struct {
	indexer.WriteBatch
	owner *rgb11ConflictMarkerFailureDB
	rgb   bool
}

func (db *rgb11ConflictMarkerFailureDB) NewAtomicWriteBatch() indexer.WriteBatch {
	return &rgb11ConflictMarkerFailureBatch{WriteBatch: db.KVDB.(indexer.AtomicWriteBatchDB).NewAtomicWriteBatch(), owner: db}
}
func (batch *rgb11ConflictMarkerFailureBatch) Put(key, value []byte) error {
	batch.rgb = batch.rgb || bytes.HasPrefix(key, batch.owner.rgbPrefix)
	if !batch.owner.failed && batch.rgb && bytes.Equal(key, batch.owner.moduleKey) {
		batch.owner.failed = true
		return errRGB11ConflictMarkerFailure
	}
	return batch.WriteBatch.Put(key, value)
}
func TestRGB11ConflictCandidateMarkerFailureIsAtomic(t *testing.T) {
	f := newRGB11ConflictFixture(t, false)
	f.publish(t, true)
	before := f.snapshot(t)
	beforeProfile, err := f.sender.db.Read(accountManagementProfileKey())
	if err != nil {
		t.Fatal(err)
	}
	scope, err := f.sender.rgbManager.rgb11AccountManagedScopeID()
	if err != nil {
		t.Fatal(err)
	}
	moduleKey := accountLocalKey(f.sender.accountProfile.AccountID, rgb11AccountManagedProviderID, scope)
	beforeModule, err := f.sender.db.Read(moduleKey)
	if err != nil {
		t.Fatal(err)
	}
	db := &rgb11ConflictMarkerFailureDB{KVDB: f.sender.db, fixture: f, moduleKey: moduleKey,
		rgbPrefix: []byte("rgb11-" + f.sender.rgbManager.rgb11ScopeKey() + "-")}
	f.sender.db = db
	defer func() { f.sender.db = db.KVDB }()
	_, err = f.sender.RefreshRGB11State(context.Background())
	if !db.failed || !errors.Is(err, errRGB11ConflictMarkerFailure) {
		t.Fatalf("atomic marker failure was not reached: %v", err)
	}
	if !reflect.DeepEqual(before, f.snapshot(t)) {
		t.Fatal("failed marker partially committed the RGB projection")
	}
	afterProfile, err := f.sender.db.Read(accountManagementProfileKey())
	if err != nil || !bytes.Equal(beforeProfile, afterProfile) {
		t.Fatal("failed marker partially changed the account profile")
	}
	afterModule, err := f.sender.db.Read(moduleKey)
	if err != nil || !bytes.Equal(beforeModule, afterModule) {
		t.Fatal("failed marker partially changed the durable RGB module")
	}
	f.assertProtected(t)
	result, err := f.sender.RefreshRGB11State(context.Background())
	if !errors.Is(err, ErrRGB11Inconsistent) || result == nil || len(result.Inconsistent) != 1 {
		t.Fatalf("retry did not commit the safe conflict state: result=%+v err=%v", result, err)
	}
	if f.journal(t).State.Status != "conflicted" {
		t.Fatal("retry lost the committed quarantine")
	}
	if err := f.sender.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := f.sender.verifyCurrentRGB11RecoveryClosure(); err != nil {
		t.Fatal(err)
	}
}
