package wallet

import (
	"errors"
	"testing"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
)

func TestUtxoReservationIsAtomicAndOwnerBound(t *testing.T) {
	db := indexerdb.NewKVDB(t.TempDir())
	defer db.Close()
	locker := NewUtxoLocker(db, nil, L1_NETWORK_BITCOIN)
	locker.Init()
	if err := locker.SetLockReason("rgb:0", "rgb"); err != nil {
		t.Fatal(err)
	}
	if err := locker.TryReserve([]string{"rgb:0", "fee:1"}, "pending-rgb", "op-a", "rgb"); err != nil {
		t.Fatal(err)
	}
	locks := locker.GetLockedUtxoList()
	if locks["rgb:0"].ReservationID != "op-a" || locks["fee:1"].ReservationID != "op-a" {
		t.Fatalf("reservation owner was not persisted: %+v", locks)
	}
	if err := locker.TryReserve([]string{"fee:1", "fee:2"}, "pending-rgb", "op-b", "rgb"); !errors.Is(err, ErrUtxoReserved) {
		t.Fatalf("competing reservation should fail: %v", err)
	}
	if locker.IsLocked("fee:2") {
		t.Fatal("failed all-or-nothing reservation left a partial lock")
	}
	if err := locker.ReleaseReservation([]string{"rgb:0", "fee:1"}, "op-b"); !errors.Is(err, ErrUtxoReservationOwner) {
		t.Fatalf("wrong owner released reservation: %v", err)
	}
	if err := locker.ReleaseReservation([]string{"rgb:0", "fee:1"}, "op-a"); err != nil {
		t.Fatal(err)
	}
	locks = locker.GetLockedUtxoList()
	if locks["rgb:0"] == nil || locks["rgb:0"].Reason != "rgb" || locks["rgb:0"].ReservationID != "" {
		t.Fatalf("claimed RGB lock was not restored: %+v", locks["rgb:0"])
	}
	if locks["fee:1"] != nil {
		t.Fatal("new fee lock was not removed on rollback")
	}

	if err := locker.TryReserve([]string{"rgb:0", "change:0"}, "pending-rgb", "op-c", "rgb"); err != nil {
		t.Fatal(err)
	}
	if err := locker.SetLockReason("change:0", "rgb"); err != nil {
		t.Fatal(err)
	}
	locks = locker.GetLockedUtxoList()
	if locks["change:0"].ReservationID != "op-c" {
		t.Fatal("ordinary reason transition stripped the reservation owner")
	}
	if err := locker.FinalizeReservation([]string{"change:0"}, "op-wrong", "rgb"); !errors.Is(err, ErrUtxoReservationOwner) {
		t.Fatalf("wrong owner finalized reservation: %v", err)
	}
	if err := locker.FinalizeReservation([]string{"change:0"}, "op-c", "rgb"); err != nil {
		t.Fatal(err)
	}
	locks = locker.GetLockedUtxoList()
	if locks["change:0"].Reason != "rgb" || locks["change:0"].ReservationID != "" {
		t.Fatalf("change output was not finalized: %+v", locks["change:0"])
	}
	if err := locker.TryReserve([]string{"change:0"}, "pending-rgb", "op-next", "rgb"); err != nil {
		t.Fatalf("finalized RGB change cannot be reused: %v", err)
	}
}
