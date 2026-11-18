package wallet

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// Observe the real memory backend's staged rows, before its existing Flush hook.
type rgb11GateProbeDB struct {
	*memoryKVDB
	staged *memoryWriteBatch
}

func (d *rgb11GateProbeDB) NewAtomicWriteBatch() indexer.WriteBatch {
	d.staged = d.memoryKVDB.NewAtomicWriteBatch().(*memoryWriteBatch)
	return d.staged
}

func TestRGB11SelectedOwnerGate(t *testing.T) {
	sender, owner, _, evidence, rpc := newRGB11GenericSendFixture(t)
	sender.rgbManager.scopeStates.stopReconciliations()
	owner.rgbManager.scopeStates.stopReconciliations()
	script, err := AddrToPkScript(owner.wallet.GetAddress(), GetChainParam())
	if err != nil {
		t.Fatal(err)
	}
	point := seedRGB11FlowEvidenceUTXO(t, evidence.rgb11FlowEvidence, script, 100000, 97)
	rebuildRGB11ProjectionTestIndexerUTXOs(evidence.rgb11FlowEvidence, rpc)
	req, err := owner.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "blind", TransportMode: "out-of-band",
		AmountRaw: "2", Expiry: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.EnsureAccount(owner.GetAccountManagementStatus().RootWalletID, 1, "rollback scope", ""); err != nil {
		t.Fatal(err)
	}
	if err := owner.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	owner.stopAccountBackgroundTasks()
	catalog, err := owner.accountManagedDataCatalog()
	if err != nil || len(catalog.Scopes) != 2 || catalog.Scopes[0].AccountIndex != 0 || catalog.Scopes[1].AccountIndex != 1 {
		t.Fatalf("missing ordered provider scopes: %v", err)
	}
	payloads, err := accountTestPayloads(owner, rgb11AccountManagedProviderID)
	if err != nil || len(payloads) != 2 {
		t.Fatalf("missing authenticated scopes: count=%d err=%v", len(payloads), err)
	}
	first, second := payloads[catalog.Scopes[0].ID()], payloads[catalog.Scopes[1].ID()]
	pkg, err := rgb11wallet.DecodeRecoveryPackage(first.Payload)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := rgb11wallet.ReceiveReservationsFromProjectionSnapshot(pkg.ProjectionRecords)
	if err != nil || len(rows) != 1 || rows[0].OutPoint != point || rows[0].RequestID != req.RequestID || rows[0].ReservationID == "" {
		t.Fatalf("selected body lacks the actual blind receive reservation: %v", err)
	}
	locker := owner.utxoLockerL1
	before := locker.GetLockedUtxoList()[point]
	persisted, err := locker.readRGB11LockRow(point)
	if err != nil || before == nil || before.Reason != sdkcommon.UtxoLockRGBPending || before.ReservationID != rows[0].ReservationID || !reflect.DeepEqual(before, persisted) {
		t.Fatalf("the selected reservation does not already have its exact DB and RAM owner: %v", err)
	}

	base := owner.db.(*memoryKVDB)
	observed := &rgb11GateProbeDB{memoryKVDB: base}
	blocked := &closedFundingReturnDB{KVDB: observed}
	flush := 0
	blocked.beforeFlush = func() {
		flush++
		blocked.failBatch.Store(flush == 2) // Later scope fails; provider rolls back the first.
		if flush != 1 && flush != 3 {
			return
		}
		batch := observed.staged
		lockPrefix := GetDBKeyPrefix() + DB_KEY_LOCKEDUTXO
		isLockKey := func(key string) bool {
			return strings.HasPrefix(key, lockPrefix) || key == GeLastLockTimeKey(locker.network)
		}
		for key := range batch.puts {
			if isLockKey(key) {
				t.Errorf("flush %d: expected no physical lock put", flush)
			}
		}
		for _, key := range batch.deletes {
			if isLockKey(key) {
				t.Errorf("flush %d: expected no physical lock delete", flush)
			}
		}
		// Flush cannot proceed until the independent local writer-gate probe
		// returns. It never mutates the lock row or makes a network call.
		checked := make(chan bool)
		go func() {
			open := locker.mutex.TryLock()
			if open {
				locker.mutex.Unlock()
			}
			checked <- open
		}()
		if <-checked {
			t.Errorf("flush %d: selected reservation owner gate opened before local commit", flush)
		}
	}
	owner.db = blocked
	defer func() { owner.db = base }()
	provider := &rgb11AccountManagedDataProvider{owner: owner}
	if err := provider.Import(catalog, []AccountManagedDataPayload{first, second}); err != errClosedFundingReturnWrite || flush != 3 {
		t.Fatalf("expected first apply, second flush failure, then successful undo: flushes=%d err=%v", flush, err)
	}
	if !locker.mutex.TryLock() {
		t.Fatal("provider left owner gate closed after rollback")
	}
	cached := cloneLockedUtxo(locker.lockmap[point])
	locker.mutex.Unlock()
	persisted, err = locker.readRGB11LockRow(point)
	if err != nil || !reflect.DeepEqual(persisted, before) || !reflect.DeepEqual(cached, before) {
		t.Fatalf("rollback did not retain the exact original reservation owner: %v", err)
	}
}
