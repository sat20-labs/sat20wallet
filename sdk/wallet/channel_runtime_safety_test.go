package wallet

import (
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/common"
)

func TestFindWalletByPubKeySkipsLockedWalletEntries(t *testing.T) {
	w := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire",
		"", GetChainParam(),
	)
	if w == nil {
		t.Fatal("create wallet")
	}

	manager := &Manager{
		walletInfoMap: map[int64]*WalletInfo{
			1: nil,
			2: {WalletInDB: WalletInDB{Id: w.GetId()}}, // persisted but still locked
			3: {WalletInDB: WalletInDB{Id: w.GetId()}, Wallet: w},
		},
	}

	rootKey := w.GetNodePubKey()
	if rootKey == nil {
		t.Fatal("root wallet key is nil")
	}
	if got := manager.FindWalletByPubKey(rootKey.SerializeCompressed()); got != w {
		t.Fatalf("FindWalletByPubKey returned %p, want %p", got, w)
	}

	derived := w.Clone()
	derived.SetSubAccount(3)
	derivedKey := derived.GetPaymentPubKey()
	if derivedKey == nil {
		t.Fatal("derived wallet key is nil")
	}
	got := manager.FindWalletByPubKeyWithDepth(derivedKey.SerializeCompressed(), 3)
	if got == nil || got.GetSubAccount() != 3 {
		t.Fatalf("FindWalletByPubKeyWithDepth returned %v, want subaccount 3", got)
	}
}

func TestLocalActionMonitorSkipsLockedWallet(t *testing.T) {
	manager := &Manager{
		localActionPerformMap: make(map[int64]Reservation),
	}
	completed, failed := manager.HandleLocalActionStatus(false)
	if len(completed) != 0 || len(failed) != 0 {
		t.Fatalf("locked manager advanced local actions: completed=%d failed=%d", len(completed), len(failed))
	}
}

func TestLocalActionOwnershipUsesStablePaymentKey(t *testing.T) {
	w := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire",
		"", GetChainParam(),
	)
	if w == nil {
		t.Fatal("create wallet")
	}
	w.SetSubAccount(2)

	manager := &Manager{wallet: w}
	resv := &LocalActionPerformData{
		ReservationBase: ReservationBase{
			WalletId: common.WalletId{Id: w.GetId() - 1, SubAccountId: 2},
		},
		ReqPubKey: w.GetPaymentPubKey().SerializeCompressed(),
	}
	if !manager.localActionBelongsToCurrentWallet(resv) {
		t.Fatal("same payment key was rejected after local wallet id changed")
	}

	w.SetSubAccount(3)
	if manager.localActionBelongsToCurrentWallet(resv) {
		t.Fatal("reservation was accepted for a different subaccount payment key")
	}
}

func TestResumeLockWithExpandFromL1TxRejectsInvalidStage(t *testing.T) {
	w := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire",
		"", GetChainParam(),
	)
	if w == nil {
		t.Fatal("create wallet")
	}

	manager := &Manager{
		wallet:                w,
		localActionPerformMap: make(map[int64]Reservation),
	}
	resv := &LocalActionPerformData{
		ReservationBase: NewReservationBase(7, true, RS_PERFORM_ACTION_TX_CONFIRMED, w),
		Action:          LOCAL_ACTION_LOCK_WITH_EXPAND,
		ReqPubKey:       w.GetPaymentPubKey().SerializeCompressed(),
		ActionResvs:     []*SubActionInfo{{ActionType: "lock"}},
	}
	manager.localActionPerformMap[resv.Id] = resv

	err := manager.ResumeLockWithExpandFromL1Tx(resv.Id, "7c8f6f6eb423967566fd1f1a0519e88de9cece84797aa3b408b78204174025c3")
	if err == nil {
		t.Fatal("non-withdraw action stage was accepted")
	}
}

func TestResumeLockWithExpandFromL1TxPersistsExistingCarrier(t *testing.T) {
	w := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire",
		"", GetChainParam(),
	)
	if w == nil {
		t.Fatal("create wallet")
	}

	database := newMemoryKVDB()
	manager := &Manager{
		db:                    database,
		wallet:                w,
		localActionPerformMap: make(map[int64]Reservation),
	}
	resv := &LocalActionPerformData{
		ReservationBase: NewReservationBase(8, true, RS_PERFORM_ACTION_TX_CONFIRMED, w),
		Action:          LOCAL_ACTION_LOCK_WITH_EXPAND,
		ReqPubKey:       w.GetPaymentPubKey().SerializeCompressed(),
		TxId:            "689eca1db9ceca3a202bf15af4688ae61962bd529626ec792351d8c67b7d5316",
		ActionResvs:     []*SubActionInfo{{ActionType: "withdraw"}},
	}
	manager.localActionPerformMap[resv.Id] = resv

	carrierTxID := "7c8f6f6eb423967566fd1f1a0519e88de9cece84797aa3b408b78204174025c3"
	if err := manager.ResumeLockWithExpandFromL1Tx(resv.Id, carrierTxID); err != nil {
		t.Fatalf("resume lock-with-expand: %v", err)
	}
	if resv.TxId != carrierTxID || !resv.IsL1Tx || resv.Status != RS_PERFORM_ACTION_TX_BROADCASTED {
		t.Fatalf("unexpected resumed state: tx=%s l1=%v status=%x", resv.TxId, resv.IsL1Tx, resv.Status)
	}

	loaded, ok := LoadAllResvFromDB(database, nil)[resv.Id].(*LocalActionPerformData)
	if !ok || loaded.TxId != carrierTxID || !loaded.IsL1Tx || loaded.Status != RS_PERFORM_ACTION_TX_BROADCASTED {
		t.Fatalf("resumed carrier was not persisted: %+v", loaded)
	}
}
