package wallet

import (
	"errors"
	"testing"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func TestDappUtxoLockOwnerIsPersistentAndExact(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	defer database.Close()
	ownerA := UtxoLockOwner{Origin: "https://a.example", Network: "testnet", WalletFingerprint: "wallet-a", AccountIndex: 1}
	ownerB := UtxoLockOwner{Origin: "https://b.example", Network: "testnet", WalletFingerprint: "wallet-a", AccountIndex: 1}
	locker := NewUtxoLocker(database, nil, L1_NETWORK_BITCOIN)
	locker.Init()

	if err := locker.LockUtxoForOwner("tx:0", "dapp", ownerA); err != nil {
		t.Fatal(err)
	}
	reloaded := NewUtxoLocker(database, nil, L1_NETWORK_BITCOIN)
	reloaded.Init()
	if lock := reloaded.GetLockedUtxoList()["tx:0"]; lock == nil || !sameUtxoLockOwner(lock.Owner, &ownerA) {
		t.Fatalf("owned lock was not persisted: %+v", lock)
	}
	if err := reloaded.UnlockUtxoForOwner("tx:0", ownerB); !errors.Is(err, ErrUtxoLockOwner) {
		t.Fatalf("different DApp unlocked owned UTXO: %v", err)
	}
	if !reloaded.IsLocked("tx:0") {
		t.Fatal("owner mismatch removed the lock")
	}
	if err := reloaded.UnlockUtxoForOwner("tx:0", ownerA); err != nil {
		t.Fatal(err)
	}
	if reloaded.IsLocked("tx:0") {
		t.Fatal("matching owner did not remove the lock")
	}
}

func TestLegacyOwnerlessUtxoLockLoadsButDappCannotUnlock(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	defer database.Close()
	type legacyLockedUtxo struct {
		LockedTime                int64
		Reason                    string
		ReservationID             string
		ReservationPreviousReason string
		Value                     int64
		Assets                    swire.TxAssets
	}
	encoded, err := EncodeToBytes(&legacyLockedUtxo{LockedTime: 1, Reason: "wallet"})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Write([]byte(GetLockedUtxoKey(L1_NETWORK_BITCOIN, "legacy:0")), encoded); err != nil {
		t.Fatal(err)
	}
	locker := NewUtxoLocker(database, nil, L1_NETWORK_BITCOIN)
	locker.Init()
	owner := UtxoLockOwner{Origin: "https://a.example", Network: "testnet", WalletFingerprint: "wallet-a"}
	if err := locker.UnlockUtxoForOwner("legacy:0", owner); !errors.Is(err, ErrUtxoLockOwner) {
		t.Fatalf("DApp should not unlock an ownerless legacy lock: %v", err)
	}
	if err := locker.UnlockUtxo("legacy:0"); err != nil {
		t.Fatalf("local wallet management should unlock legacy lock: %v", err)
	}
}
