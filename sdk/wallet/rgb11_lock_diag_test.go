//go:build rgb11discard

package wallet

import (
	"testing"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func TestRGB11LockDiagCache(t *testing.T) {
	oldMode, oldEnv, oldChain := _mode, _env, _chain
	_mode, _env, _chain = LIGHT_NODE, "prd", "testnet"
	t.Cleanup(func() { _mode, _env, _chain = oldMode, oldEnv, oldChain })

	database := newMemoryKVDB()
	protected := "c57:1"
	target := rgb11StaleLockOnlyTarget.OutPoint
	ordinary := NewUtxoLocker(database, nil, L1_NETWORK_BITCOIN)
	ordinary.Init()
	if err := ordinary.LockUtxo(protected, rgb11wallet.LockReasonRGB); err != nil {
		t.Fatal(err)
	}
	ordinary.mutex.Lock()
	ordinary.lockmap[target] = &LockedUtxo{LockedTime: 123, Reason: rgb11wallet.LockReasonRGB}
	ordinary.mutex.Unlock()
	if ordinary.GetLockedUtxoList()[target] == nil {
		t.Fatal("ordinary manager lost its cache-only lock")
	}

	fresh := NewUtxoLocker(database, nil, L1_NETWORK_BITCOIN)
	fresh.Init()
	if fresh.GetLockedUtxoList()[target] != nil {
		t.Fatal("fresh maintenance manager unexpectedly loaded cache-only lock")
	}
	ordinaryDiag, err := (&Manager{db: database, utxoLockerL1: ordinary}).DiagnoseRGB11Lock(target)
	if err != nil {
		t.Fatal(err)
	}
	freshDiag, err := (&Manager{db: database, utxoLockerL1: fresh}).DiagnoseRGB11Lock(target)
	if err != nil {
		t.Fatal(err)
	}
	if ordinaryDiag.RawState != "absent" || ordinaryDiag.LockerLock == nil {
		t.Fatalf("ordinary diagnostic=%+v", ordinaryDiag)
	}
	if freshDiag.RawState != "absent" || freshDiag.LockerLock != nil {
		t.Fatalf("fresh diagnostic=%+v", freshDiag)
	}
	if ordinaryDiag.MarkerValue != freshDiag.MarkerValue || ordinaryDiag.MarkerValue == 0 {
		t.Fatalf("marker ordinary=%d fresh=%d", ordinaryDiag.MarkerValue, freshDiag.MarkerValue)
	}
}
