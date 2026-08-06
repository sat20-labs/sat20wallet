package wallet

import (
	"errors"
	"strings"
	"testing"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func TestRGB11OwnerlessReceiveReservationFailsClosed(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	defer database.Close()
	locker := NewUtxoLocker(database, nil, L1_NETWORK_BITCOIN)
	outpoint := strings.Repeat("ab", 32) + ":0"
	if err := locker.TryReserve([]string{outpoint}, rgb11wallet.LockReasonPending, "active-owner"); err != nil {
		t.Fatal(err)
	}
	deleted := false
	err := releaseRGB11ReceiveReservationValue(locker, &rgb11wallet.ReceiveReservation{
		Version: 1, RequestID: "ownerless", OutPoint: outpoint,
	}, func() error {
		deleted = true
		return nil
	})
	if !errors.Is(err, ErrRGB11Inconsistent) {
		t.Fatalf("ownerless release err=%v", err)
	}
	if deleted {
		t.Fatal("ownerless reservation deleted its projection record")
	}
	lock := locker.GetLockedUtxoList()[outpoint]
	if lock == nil || lock.ReservationID != "active-owner" ||
		lock.Reason != rgb11wallet.LockReasonPending {
		t.Fatalf("ownerless reservation changed UTXO lock: %+v", lock)
	}
}

func TestRGB11OwnedReceiveReservationReleasesBeforeDelete(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	defer database.Close()
	locker := NewUtxoLocker(database, nil, L1_NETWORK_BITCOIN)
	outpoint := strings.Repeat("cd", 32) + ":1"
	const owner = "receive-owner"
	if err := locker.TryReserve([]string{outpoint}, rgb11wallet.LockReasonPending, owner); err != nil {
		t.Fatal(err)
	}
	deleted := false
	if err := releaseRGB11ReceiveReservationValue(locker, &rgb11wallet.ReceiveReservation{
		Version: 1, RequestID: "owned", OutPoint: outpoint, ReservationID: owner,
	}, func() error {
		if locker.GetLockedUtxoList()[outpoint] != nil {
			t.Fatal("projection record deleted before UTXO reservation was released")
		}
		deleted = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("owned reservation did not delete projection record")
	}
}
