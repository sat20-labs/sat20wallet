package wallet

import "testing"

func TestReconcileReadyOpenChannelOperationLog(t *testing.T) {
	database := newMemoryKVDB()
	walletValue := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire",
		"", GetChainParam(),
	)
	if walletValue == nil {
		t.Fatal("create wallet")
	}

	const (
		channelID     = "ready-channel-operation-log"
		reservationID = int64(701)
	)
	stored := savePendingFundingFixture(t, database, walletValue, channelID, reservationID)
	stored.Status = CS_READY
	if err := SaveChannelInDB(database, stored); err != nil {
		t.Fatal(err)
	}

	manager := &Manager{db: database, wallet: walletValue}
	record, err := manager.BeginOperationLog(OperationLogCreate{
		Category: "channel",
		Action:   "open_channel",
		Title:    "Open channel",
		Summary:  "Funding transaction broadcast; waiting for confirmation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.BindOperationLogReservation(record.ID, RESV_TYPE_OPEN, reservationID); err != nil {
		t.Fatal(err)
	}

	manager.reconcileReadyOpenChannelOperationLogs()
	updated, err := manager.GetOperationLog(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != OperationLogSucceeded || updated.Result["channel_id"] != channelID {
		t.Fatalf("unexpected reconciled log: %+v", updated)
	}

	manager.reconcileReadyOpenChannelOperationLogs()
	idempotent, err := manager.GetOperationLog(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(idempotent.History) != len(updated.History) {
		t.Fatalf("reconciliation was not idempotent: history %d -> %d", len(updated.History), len(idempotent.History))
	}
}

func TestReconcileReadyOpenChannelOperationLogRejectsWrongWallet(t *testing.T) {
	database := newMemoryKVDB()
	channelWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire",
		"", GetChainParam(),
	)
	otherWallet := NewInternalWalletWithMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"", GetChainParam(),
	)
	if channelWallet == nil || otherWallet == nil {
		t.Fatal("create wallets")
	}

	const reservationID = int64(702)
	stored := savePendingFundingFixture(t, database, channelWallet, "other-wallet-channel", reservationID)
	stored.Status = CS_READY
	if err := SaveChannelInDB(database, stored); err != nil {
		t.Fatal(err)
	}

	manager := &Manager{db: database, wallet: otherWallet}
	record, err := manager.BeginOperationLog(OperationLogCreate{
		Category: "channel", Action: "open_channel", Title: "Open channel",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.BindOperationLogReservation(record.ID, RESV_TYPE_OPEN, reservationID); err != nil {
		t.Fatal(err)
	}

	manager.reconcileReadyOpenChannelOperationLogs()
	unchanged, err := manager.GetOperationLog(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != OperationLogRunning {
		t.Fatalf("wrong-wallet log was reconciled: %+v", unchanged)
	}
}
