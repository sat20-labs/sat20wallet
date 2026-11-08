package wallet

import (
	"reflect"
	"testing"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// Recovery here means fresh local DB/store handles. It neither restores an
// account backup nor constructs a Manager from another Manager's cached fields.
func TestRGBCancelLocalReopen(t *testing.T) {
	fixture := newExpiredCancelGroup(t, 2)
	// All pending seed and account discovery completed before this confirmed
	// cancellation. No preparation writes are part of a confirmation preview.
	if err := fixture.manager.CancelExpiredRGB11Transfer(fixture.transferIDs[0]); err != nil {
		t.Fatal(err)
	}
	assertExpiredTombstone(t, fixture)
	persisted := make([]*rgb11wallet.PendingTransfer, len(fixture.transferIDs))
	for i, id := range fixture.transferIDs {
		value, err := fixture.manager.rgbManager.projectionStore.LoadPendingTransfer(id)
		if err != nil {
			t.Fatal(err)
		}
		persisted[i] = value
	}

	rawBefore, err := fixture.manager.rgbManager.projectionStore.ReadLocalRecords()
	if err != nil {
		t.Fatal(err)
	}
	fixture.manager.Close()
	if err := fixture.manager.db.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.manager.db = nil // Retire the closed handle from its original cleanup.
	reopened := indexerdb.NewKVDB(fixture.dbPath)
	if reopened == nil {
		t.Fatal("reopen local cancellation database")
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	locker := NewUtxoLocker(reopened, nil, L1_NETWORK_BITCOIN)
	locker.Init()
	store := rgb11wallet.NewProjectionStore(reopened, locker)
	if err := store.SetScope(rgb11StorageScope(201, 0)); err != nil {
		t.Fatal(err)
	}
	rawAfter, err := store.ReadLocalRecords()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rawBefore, rawAfter) {
		t.Fatal("reopening changed the exact durable projection record bytes")
	}
	for i, id := range fixture.transferIDs {
		actual, err := store.LoadPendingTransfer(id)
		if err != nil {
			t.Fatal(err)
		}
		if persisted[i] == nil || actual == nil {
			t.Fatal("cancellation tombstone missing before or after reopen")
		}
		expected, restored := *persisted[i], *actual
		// Reservation state and the strict projection codec represent only
		// an empty rejection list differently. Normalize that field in
		// comparison copies; nonempty lists and all other fields stay exact.
		if len(expected.State.RejectedOpouts) == 0 && len(restored.State.RejectedOpouts) == 0 {
			expected.State.RejectedOpouts = nil
			restored.State.RejectedOpouts = nil
		}
		if !reflect.DeepEqual(expected, restored) {
			t.Fatal("reopened cancellation tombstone differs from its durable record")
		}
	}
	if locker.GetLockedUtxoList()[fixture.input] != nil {
		t.Fatal("reopened database revived a released cancellation reservation")
	}
}
