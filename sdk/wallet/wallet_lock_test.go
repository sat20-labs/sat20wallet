package wallet

import (
	"bytes"
	"testing"
)

func TestUnlockWalletIsAtomicOnPasswordFailure(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	manager := newAccountManagementAutoTestManager(t)
	if _, _, err := manager.CreateWallet("correct-password"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.CreateWallet("correct-password"); err != nil {
		t.Fatal(err)
	}
	// Simulate a fresh process with encrypted metadata only, not a UI lock.
	manager.wallet = nil
	manager.clearAccountManagementSession()
	var err error
	manager.walletInfoMap, err = loadAllWalletFromDB(manager.db)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := manager.UnlockWallet("wrong-password"); err == nil {
		t.Fatal("UnlockWallet accepted an incorrect password")
	}
	if manager.GetWallet() != nil {
		t.Fatal("failed unlock installed a current runtime wallet")
	}
	manager.mutex.RLock()
	defer manager.mutex.RUnlock()
	for id, info := range manager.walletInfoMap {
		if info.Wallet != nil {
			t.Fatalf("failed unlock installed runtime wallet %d", id)
		}
	}
	if len(manager.accountSecret) != 0 || manager.accountPassword != "" {
		t.Fatal("failed unlock retained account-management credentials")
	}
}

// UI lock lives in the PWA; the SDK must keep its wallet and workers intact.
func TestUnlockRunningWalletOnlyVerifiesPassword(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	manager := newAccountManagementAutoTestManager(t)
	walletID, _, err := manager.CreateWallet("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	manager.initResvMap()
	manager.l1IndexerClient = NewIndexerRPCClientMgr()
	manager.l2IndexerClient = NewIndexerRPCClientMgr()
	manager.watchTower = NewWatchTower(manager)
	// This fixture has no indexer endpoints. Keep monitor threads alive without
	// running network ticks; protocol progression has its own monitor tests.
	manager.actionMonitorL1Lock.Lock()
	manager.actionMonitorL2Lock.Lock()
	t.Cleanup(manager.actionMonitorL1Lock.Unlock)
	t.Cleanup(manager.actionMonitorL2Lock.Unlock)
	manager.startActionMonitor()
	manager.startChannelHeartbeat()
	manager.watchTower.Start()
	manager.dkvs.start()
	t.Cleanup(manager.Stop)

	walletBefore := manager.wallet
	secretBefore := append([]byte(nil), manager.accountSecret...)
	actionStop, heartbeatStop := manager.actionMonitorStop, manager.channelHeartbeatStop
	towerGeneration := manager.watchTower.retryGeneration
	dkvsStop := manager.dkvs.stop
	resv := &FundingReservation{FundingDataInDB: FundingDataInDB{
		ReservationBase: NewReservationBase(901, true, ResvStatus(RS_FUNDING_BROADCASTED), manager.wallet),
	}, Channel: &Channel{ResvId: 901, localWallet: manager.wallet}}
	manager.addResv(resv)

	for i := 0; i < 100; i++ {
		password := "correct-password"
		if i%2 == 0 {
			password = "wrong-password"
		}
		id, err := manager.UnlockWallet(password)
		if password == "wrong-password" {
			if err == nil {
				t.Fatal("running wallet accepted an incorrect password")
			}
		} else if err != nil || id != walletID {
			t.Fatalf("running wallet verification: id=%d err=%v", id, err)
		}
		if manager.wallet != walletBefore || manager.walletInfoMap[walletID].Wallet != walletBefore ||
			!bytes.Equal(manager.accountSecret, secretBefore) || manager.accountPassword != "correct-password" {
			t.Fatal("password verification changed runtime identity or credentials")
		}
		if manager.GetFundingReservations()[901] != resv || resv.Channel.ResvId != 901 {
			t.Fatal("password verification rebuilt a pending reservation")
		}
		if manager.actionMonitorStop != actionStop || manager.channelHeartbeatStop != heartbeatStop ||
			manager.watchTower.retryGeneration != towerGeneration || manager.dkvs.stop != dkvsStop {
			t.Fatal("password verification restarted background workers")
		}
	}
	manager.Stop()
	select {
	case <-actionStop:
	default:
		t.Fatal("explicit Stop did not stop the action monitor")
	}
	select {
	case <-heartbeatStop:
	default:
		t.Fatal("explicit Stop did not stop the heartbeat")
	}
	if manager.watchTower.retryRunning || manager.dkvs.stop != nil || !manager.rgbManager.scopeStates.stopping {
		t.Fatal("explicit Stop did not stop watchtower/DKVS/RGB")
	}
}
