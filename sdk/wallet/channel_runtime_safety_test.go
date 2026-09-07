package wallet

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/common"
)

func TestLocalActionConfirmedCallbackIgnoresStaleChainTick(t *testing.T) {
	manager := &Manager{localActionPerformMap: make(map[int64]Reservation)}
	resv := &LocalActionPerformData{
		ReservationBase: newReservationBase(5, RS_PERFORM_ACTION_TX_BROADCASTED, nil),
		Action:          LOCAL_ACTION_LOCK_WITH_EXPAND,
		TxId:            "53af8eaf-new-l1-carrier-3d70",
		IsL1Tx:          true,
	}
	manager.localActionPerformMap[resv.Id] = resv

	applied, err := manager.handleLocalActionTxConfirmed(resv.Id,
		RS_PERFORM_ACTION_TX_BROADCASTED, "old-l2-withdraw", false)
	if err != nil {
		t.Fatalf("stale confirmation callback: %v", err)
	}
	if applied {
		t.Fatal("stale L2 confirmation callback was applied")
	}
	if resv.Status != RS_PERFORM_ACTION_TX_BROADCASTED || !resv.IsL1Tx ||
		resv.TxId != "53af8eaf-new-l1-carrier-3d70" {
		t.Fatalf("stale callback changed replacement transaction: status=%x l1=%v tx=%s",
			resv.Status, resv.IsL1Tx, resv.TxId)
	}
}

func TestLocalActionConfirmedCallbackAppliesOverlappingTickOnce(t *testing.T) {
	database := newMemoryKVDB()
	manager := &Manager{db: database, localActionPerformMap: make(map[int64]Reservation)}
	resv := &LocalActionPerformData{
		ReservationBase: newReservationBase(6, RS_PERFORM_ACTION_TX_BROADCASTED, nil),
		Action:          LOCAL_ACTION_CONFIRM_TX_L2,
		TxId:            "overlapping-l2-tx",
		IsL1Tx:          false,
	}
	manager.localActionPerformMap[resv.Id] = resv

	const ticks = 16
	var applied atomic.Int32
	errorsByTick := make(chan error, ticks)
	var workers sync.WaitGroup
	for index := 0; index < ticks; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			changed, err := manager.handleLocalActionTxConfirmed(resv.Id,
				RS_PERFORM_ACTION_TX_BROADCASTED, "overlapping-l2-tx", false)
			if changed {
				applied.Add(1)
			}
			errorsByTick <- err
		}()
	}
	workers.Wait()
	close(errorsByTick)
	for err := range errorsByTick {
		if err != nil {
			t.Fatalf("overlapping confirmation callback: %v", err)
		}
	}
	if applied.Load() != 1 {
		t.Fatalf("confirmation applied %d times, want 1", applied.Load())
	}
	if resv.Status != RS_PERFORM_ACTION_COMPLETED {
		t.Fatalf("unexpected final status %x", resv.Status)
	}
}

func TestLocalActionConfirmedWaitTickDoesNotRepeatConfirmationLog(t *testing.T) {
	database := newMemoryKVDB()
	manager := &Manager{db: database, localActionPerformMap: make(map[int64]Reservation)}
	resv := &LocalActionPerformData{
		ReservationBase: newReservationBase(7, RS_PERFORM_ACTION_TX_CONFIRMED, nil),
		Action:          LOCAL_ACTION_LOCK_WITH_EXPAND,
		TxId:            "confirmed-l2-withdraw",
		IsL1Tx:          false,
	}
	manager.localActionPerformMap[resv.Id] = resv
	record, err := manager.BeginOperationLog(OperationLogCreate{
		Category: "channel", Action: LOCAL_ACTION_LOCK_WITH_EXPAND,
		Title: "Lock with expand", Summary: "Waiting for carrier",
	})
	if err != nil {
		t.Fatalf("begin operation log: %v", err)
	}
	if err := manager.BindOperationLogReservation(record.ID, RESV_TYPE_LOCALACTION, resv.Id); err != nil {
		t.Fatalf("bind operation log: %v", err)
	}

	before, err := manager.GetOperationLog(record.ID)
	if err != nil {
		t.Fatalf("read operation log before wait tick: %v", err)
	}
	applied, callbackErr := manager.handleLocalActionTxConfirmed(resv.Id,
		RS_PERFORM_ACTION_TX_CONFIRMED, "confirmed-l2-withdraw", false)
	if !applied || callbackErr == nil {
		t.Fatalf("wait tick result: applied=%v err=%v", applied, callbackErr)
	}
	after, err := manager.GetOperationLog(record.ID)
	if err != nil {
		t.Fatalf("read operation log after wait tick: %v", err)
	}
	if len(after.History) != len(before.History) {
		t.Fatalf("confirmed wait tick appended operation log history: before=%d after=%d",
			len(before.History), len(after.History))
	}
}

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

	loaded, ok := mustLoadAllResv(t, database)[resv.Id].(*LocalActionPerformData)
	if !ok || loaded.TxId != carrierTxID || !loaded.IsL1Tx || loaded.Status != RS_PERFORM_ACTION_TX_BROADCASTED {
		t.Fatalf("resumed carrier was not persisted: %+v", loaded)
	}
}
