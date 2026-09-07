package wallet

import (
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
)

func mustLoadAllResv(t *testing.T, database indexer.KVDB) map[int64]Reservation {
	t.Helper()
	loaded, err := LoadAllResvFromDB(database, nil)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func minimalPersistedCommitmentTx(tag byte) *wire.MsgTx {
	var previousHash chainhash.Hash
	previousHash[0] = tag
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(wire.NewOutPoint(&previousHash, 0), nil, nil))
	tx.AddTxOut(wire.NewTxOut(1_000, []byte{0x51})) // OP_TRUE test output.
	return tx
}

func savePendingFundingFixture(t *testing.T, database *memoryKVDB, walletValue *InternalWallet,
	channelID string, reservationID int64) *ChannelInDB {
	t.Helper()
	stored := NewChannelInDB()
	stored.ChannelId = channelID
	stored.Address = channelID
	stored.Status = CS_FUNDING_BROADCASTED
	stored.FundingTime = reservationID
	stored.LocalWalletId = walletValue.GetId()
	stored.LocalChanCfg.PaymentKey = walletValue.GetPaymentPubKey()
	stored.LocalChanCfg.RevocationBasePoint = walletValue.GetRevocationBaseKey()
	stored.RemoteChanCfg.PaymentKey = walletValue.GetPaymentPubKey()
	stored.RemoteChanCfg.RevocationBasePoint = walletValue.GetRevocationBaseKey()
	stored.LocalCommitment = NewChannelCommitment()
	stored.RemoteCommitment = NewChannelCommitment()
	stored.LocalCommitment.CommitTx = minimalPersistedCommitmentTx(1)
	stored.RemoteCommitment.CommitTx = minimalPersistedCommitmentTx(2)
	stored.ChanPoint = &TxOutput{
		OutPointStr: "b241000000000000000000000000000000000000000000000000000000000000:0",
		OutValue:    wire.TxOut{Value: 40_000},
	}
	stored.StaticMerkleRoot = stored.CalcStaticMerkleRoot()
	stored.LocalAssetsMerkleRoot = stored.CalcLocalAssetsMerkleRoot()
	stored.RemoteAssetsMerkleRoot = stored.CalcRemoteAssetsMerkleRoot()
	stored.ChannelHash = stored.CalcHash()
	if err := SaveChannelInDB(database, stored); err != nil {
		t.Fatalf("save channel: %v", err)
	}

	resv := &FundingReservation{FundingDataInDB: FundingDataInDB{
		ReservationBase: NewReservationBase(reservationID, true, ResvStatus(RS_FUNDING_BROADCASTED), walletValue),
		ChannelId:       channelID,
	}}
	if err := SaveReservation(database, resv); err != nil {
		t.Fatalf("save reservation: %v", err)
	}
	return stored
}

func savePendingLockWithExpandFixture(t *testing.T, database *memoryKVDB, walletValue *InternalWallet,
	channelID string, reservationID int64) *LocalActionPerformData {
	t.Helper()
	stored := savePendingFundingFixture(t, database, walletValue, channelID, reservationID)
	if err := DeleteReservation(database, RESV_TYPE_OPEN, reservationID); err != nil {
		t.Fatal(err)
	}
	stored.Status = CS_READY
	stored.FundingTime = 0
	stored.StaticMerkleRoot = stored.CalcStaticMerkleRoot()
	if err := SaveChannelInDB(database, stored); err != nil {
		t.Fatal(err)
	}

	resv := &LocalActionPerformData{
		ReservationBase: NewReservationBase(reservationID, true, RS_PERFORM_ACTION_TX_BROADCASTED, walletValue),
		Action:          LOCAL_ACTION_LOCK_WITH_EXPAND,
		ActionParam:     &LocalActionParam_Expand{ChannelId: channelID},
		ReqPubKey:       walletValue.GetPaymentPubKey().SerializeCompressed(),
		TxId:            "53af8eaf00000000000000000000000000000000000000000000000000003d70",
		IsL1Tx:          true,
	}
	if err := SaveReservation(database, resv); err != nil {
		t.Fatal(err)
	}
	return resv
}

func TestFundingReservationRehydratesChannelAfterRestart(t *testing.T) {
	database := newMemoryKVDB()
	walletValue := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire",
		"", GetChainParam(),
	)
	if walletValue == nil {
		t.Fatal("create wallet")
	}

	channelID := "pendingchannelrehydration"
	stored := savePendingFundingFixture(t, database, walletValue, channelID, 101)

	restarted := &Manager{
		db:                database,
		wallet:            walletValue,
		walletInfoMap:     map[int64]*WalletInfo{walletValue.GetId(): {WalletInDB: WalletInDB{Id: walletValue.GetId()}, Wallet: walletValue}},
		channelMap:        make(map[string]*Channel),
		fundingChannelMap: make(map[int64]*FundingReservation),
	}
	restarted.resetResvMapsLocked()
	loaded := mustLoadAllResv(t, database)
	loadedFunding, ok := loaded[101].(*FundingReservation)
	if !ok {
		t.Fatalf("loaded reservation type = %T", loaded[101])
	}
	if loadedFunding.Channel != nil {
		t.Fatal("locked DB load must not construct a runtime channel")
	}
	restarted.addResv(loadedFunding)
	restarted.rehydratePendingFundingRuntime()
	loadedFunding = restarted.GetFundingReservations()[101]
	if loadedFunding == nil || loadedFunding.Channel == nil {
		t.Fatal("funding reservation channel was not rehydrated after unlock")
	}
	if loadedFunding.Channel.ChannelId != channelID || loadedFunding.Channel.Status != CS_FUNDING_BROADCASTED {
		t.Fatalf("rehydrated channel = %s/%d", loadedFunding.Channel.ChannelId, loadedFunding.Channel.Status)
	}
	if loadedFunding.Channel.ChanPoint == nil || loadedFunding.Channel.ChanPoint.OutPointStr != stored.ChanPoint.OutPointStr {
		t.Fatalf("monitor channel point not restored: %+v", loadedFunding.Channel.ChanPoint)
	}

	if got := restarted.FindChannel(channelID); got != loadedFunding.Channel {
		t.Fatalf("FindChannel returned %p, want rehydrated %p", got, loadedFunding.Channel)
	}
	if got := restarted.GetAllChannels()[channelID]; got != loadedFunding.Channel {
		t.Fatalf("GetAllChannels returned %p, want rehydrated %p", got, loadedFunding.Channel)
	}
}

func TestUnlockWalletRehydratesPendingFundingWithoutManagerLockDeadlock(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	manager := newAccountManagementAutoTestManager(t)
	manager.resetResvMapsLocked()
	manager.channelMap = make(map[string]*Channel)
	const mnemonic = "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire"
	walletID, err := manager.ImportWallet(mnemonic, "password")
	if err != nil {
		t.Fatal(err)
	}
	walletValue := manager.wallet
	channelID := "unlocklifecyclerehydration"
	savePendingFundingFixture(t, manager.db.(*memoryKVDB), walletValue.(*InternalWallet), channelID, 202)

	manager.mutex.Lock()
	manager.wallet = nil
	for _, info := range manager.walletInfoMap {
		info.Wallet = nil
	}
	manager.resetResvMapsLocked()
	for _, resv := range mustLoadAllResv(t, manager.db) {
		manager.addResvLocked(resv)
	}
	manager.mutex.Unlock()
	if manager.GetFundingReservations()[202].Channel != nil {
		t.Fatal("runtime channel restored before unlock")
	}

	done := make(chan error, 1)
	go func() {
		_, unlockErr := manager.UnlockWallet("password")
		done <- unlockErr
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("UnlockWallet failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("UnlockWallet deadlocked while restoring reservation channels")
	}
	loaded := manager.GetFundingReservations()[202]
	if loaded == nil || loaded.Channel == nil || loaded.Channel.ChannelId != channelID {
		t.Fatalf("pending channel not restored after unlock: %+v", loaded)
	}
	if walletID != loaded.Channel.LocalWalletId {
		t.Fatalf("restored channel wallet=%d, want %d", loaded.Channel.LocalWalletId, walletID)
	}
}

func TestUnlockWalletRehydratesPendingLockWithExpandChannel(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	manager := newAccountManagementAutoTestManager(t)
	manager.resetResvMapsLocked()
	manager.channelMap = make(map[string]*Channel)
	manager.nodeMap = make(map[string]string)
	const mnemonic = "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire"
	walletID, err := manager.ImportWallet(mnemonic, "password")
	if err != nil {
		t.Fatal(err)
	}
	walletValue := manager.wallet.(*InternalWallet)
	const channelID = "unlocklockwithexpandrehydration"
	const reservationID int64 = 203
	savePendingLockWithExpandFixture(t, manager.db.(*memoryKVDB), walletValue, channelID, reservationID)

	manager.mutex.Lock()
	manager.wallet = nil
	for _, info := range manager.walletInfoMap {
		info.Wallet = nil
	}
	manager.resetResvMapsLocked()
	for _, resv := range mustLoadAllResv(t, manager.db) {
		manager.addResvLocked(resv)
	}
	manager.mutex.Unlock()
	if got := manager.GetChannel(channelID); got != nil {
		t.Fatalf("channel restored before unlock: %p", got)
	}

	done := make(chan error, 1)
	go func() {
		_, unlockErr := manager.UnlockWallet("password")
		done <- unlockErr
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("UnlockWallet failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("UnlockWallet deadlocked while restoring lock-with-expand channel")
	}

	restored := manager.GetChannel(channelID)
	if restored == nil || restored.LocalWallet() == nil || restored.LocalWallet().GetId() != walletID {
		t.Fatalf("pending lock-with-expand channel not restored for wallet %d: %+v", walletID, restored)
	}
	if got := manager.FindChannel(channelID); got != restored {
		t.Fatalf("FindChannel returned %p, want runtime channel %p", got, restored)
	}
	if err := manager.rehydratePendingLocalActionRuntime(); err != nil {
		t.Fatal(err)
	}
	if got := manager.GetChannel(channelID); got != restored {
		t.Fatalf("second recovery replaced runtime channel %p with %p", restored, got)
	}
}

func TestPendingLockWithExpandRecoverySkipsOtherWallet(t *testing.T) {
	database := newMemoryKVDB()
	currentWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", GetChainParam())
	otherWallet := NewInternalWalletWithMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", "", GetChainParam())
	if currentWallet == nil || otherWallet == nil {
		t.Fatal("create wallets")
	}
	const reservationID int64 = 204
	const channelID = "otherwalletlockwithexpand"
	savePendingLockWithExpandFixture(t, database, otherWallet, channelID, reservationID)
	if err := DeleteReservation(database, RESV_TYPE_LOCALACTION, reservationID); err != nil {
		t.Fatal(err)
	}
	resv := &LocalActionPerformData{
		ReservationBase: NewReservationBase(reservationID, true, RS_PERFORM_ACTION_TX_BROADCASTED, currentWallet),
		Action:          LOCAL_ACTION_LOCK_WITH_EXPAND,
		ActionParam:     &LocalActionParam_Expand{ChannelId: channelID},
		ReqPubKey:       currentWallet.GetPaymentPubKey().SerializeCompressed(),
	}
	if err := SaveReservation(database, resv); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		db:         database,
		wallet:     currentWallet,
		channelMap: make(map[string]*Channel),
		nodeMap:    make(map[string]string),
		walletInfoMap: map[int64]*WalletInfo{
			currentWallet.GetId(): {WalletInDB: WalletInDB{Id: currentWallet.GetId()}, Wallet: currentWallet},
			otherWallet.GetId():   {WalletInDB: WalletInDB{Id: otherWallet.GetId()}, Wallet: otherWallet},
		},
	}
	manager.resetResvMapsLocked()
	for _, resv := range mustLoadAllResv(t, database) {
		manager.addResv(resv)
	}
	if err := manager.rehydratePendingLocalActionRuntime(); err == nil ||
		!strings.Contains(err.Error(), "persisted channel belongs to another wallet") {
		t.Fatalf("ownership recovery error = %v", err)
	}
	if got := manager.GetChannel(channelID); got != nil {
		t.Fatalf("other wallet channel was restored: %p", got)
	}
	if _, ok := manager.GetLocalActionReservations()[reservationID]; !ok {
		t.Fatal("other wallet reservation was removed")
	}
}

func TestPendingLockWithExpandRecoveryKeepsReservationOnChannelFailure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		corrupt bool
	}{
		{name: "missing"},
		{name: "corrupt", corrupt: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database := newMemoryKVDB()
			walletValue := NewInternalWalletWithMnemonic(
				"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", GetChainParam())
			if walletValue == nil {
				t.Fatal("create wallet")
			}
			const reservationID int64 = 205
			channelID := "failedlockwithexpand" + tc.name
			resv := &LocalActionPerformData{
				ReservationBase: NewReservationBase(reservationID, true, RS_PERFORM_ACTION_TX_BROADCASTED, walletValue),
				Action:          LOCAL_ACTION_LOCK_WITH_EXPAND,
				ActionParam:     &LocalActionParam_Expand{ChannelId: channelID},
				ReqPubKey:       walletValue.GetPaymentPubKey().SerializeCompressed(),
			}
			if err := SaveReservation(database, resv); err != nil {
				t.Fatal(err)
			}
			if tc.corrupt {
				if err := database.Write([]byte(GetChannelKey(channelID)), []byte("not-a-channel")); err != nil {
					t.Fatal(err)
				}
			}
			manager := &Manager{
				db:            database,
				wallet:        walletValue,
				walletInfoMap: map[int64]*WalletInfo{walletValue.GetId(): {WalletInDB: WalletInDB{Id: walletValue.GetId()}, Wallet: walletValue}},
				channelMap:    make(map[string]*Channel),
				nodeMap:       make(map[string]string),
			}
			manager.resetResvMapsLocked()
			manager.addResv(resv)
			err := manager.rehydratePendingLocalActionRuntime()
			if err == nil || !strings.Contains(err.Error(), "local action 205 channel "+channelID) {
				t.Fatalf("recovery error = %v", err)
			}
			if _, ok := manager.GetLocalActionReservations()[reservationID]; !ok {
				t.Fatal("failed recovery removed active reservation")
			}
			if _, err := LoadReservation(database, manager, RESV_TYPE_LOCALACTION, reservationID); err != nil {
				t.Fatalf("failed recovery deleted persisted reservation: %v", err)
			}
			if got := manager.GetChannel(channelID); got != nil {
				t.Fatalf("failed recovery installed channel: %p", got)
			}
		})
	}
}

func TestPendingLockWithExpandRecoveryDoesNotReplaceCurrentChannel(t *testing.T) {
	database := newMemoryKVDB()
	walletValue := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", GetChainParam())
	if walletValue == nil {
		t.Fatal("create wallet")
	}
	const reservationID int64 = 206
	const channelID = "currentchannellockwithexpand"
	resv := savePendingLockWithExpandFixture(t, database, walletValue, channelID, reservationID)
	manager := &Manager{
		db:            database,
		wallet:        walletValue,
		walletInfoMap: map[int64]*WalletInfo{walletValue.GetId(): {WalletInDB: WalletInDB{Id: walletValue.GetId()}, Wallet: walletValue}},
		channelMap:    make(map[string]*Channel),
		nodeMap:       make(map[string]string),
	}
	manager.resetResvMapsLocked()
	manager.addResv(resv)
	stale, err := manager.LoadChannel(channelID)
	if err != nil {
		t.Fatal(err)
	}
	current := stale.Clone()
	if err := manager.EnableChannel(current); err != nil {
		t.Fatal(err)
	}

	if err := manager.rehydratePendingLocalActionRuntime(); err != nil {
		t.Fatal(err)
	}
	if got := manager.GetChannel(channelID); got != current {
		t.Fatalf("recovery replaced current channel %p with %p", current, got)
	}
}

func TestNilFundingReservationDoesNotMaskPersistedChannel(t *testing.T) {
	database := newMemoryKVDB()
	manager := &Manager{
		db:                database,
		channelMap:        make(map[string]*Channel),
		fundingChannelMap: make(map[int64]*FundingReservation),
	}
	manager.fundingChannelMap[1] = &FundingReservation{FundingDataInDB: FundingDataInDB{ChannelId: "missing"}}
	missing := &FundingReservation{FundingDataInDB: FundingDataInDB{
		ReservationBase: NewReservationBase(2, true, RS_INIT, nil),
		ChannelId:       "missing",
	}}
	if err := SaveReservation(database, missing); err != nil {
		t.Fatalf("save reservation: %v", err)
	}
	loaded, err := LoadReservation(database, manager, RESV_TYPE_OPEN, 2)
	if err != nil {
		t.Fatalf("missing channel must not make reservation unreadable: %v", err)
	}
	if loaded.(*FundingReservation).Channel != nil {
		t.Fatal("missing channel was fabricated during single reservation load")
	}
	if got := manager.GetAllChannels(); len(got) != 0 {
		t.Fatalf("nil funding channel leaked into result: %+v", got)
	}
	if got := manager.FindChannel("missing"); got != nil {
		t.Fatalf("FindChannel returned nil reservation channel: %+v", got)
	}
}

func TestFundingReservationRecoverySkipsUnavailableWalletWithoutPanic(t *testing.T) {
	database := newMemoryKVDB()
	walletValue := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire",
		"", GetChainParam(),
	)
	if walletValue == nil {
		t.Fatal("create wallet")
	}
	savePendingFundingFixture(t, database, walletValue, "missingwalletchannel", 303)

	manager := &Manager{
		db:                database,
		walletInfoMap:     make(map[int64]*WalletInfo),
		channelMap:        make(map[string]*Channel),
		fundingChannelMap: make(map[int64]*FundingReservation),
	}
	manager.resetResvMapsLocked()
	for _, resv := range mustLoadAllResv(t, database) {
		manager.addResv(resv)
	}
	manager.rehydratePendingFundingRuntime()
	if got := manager.GetFundingReservations()[303]; got == nil || got.Channel != nil {
		t.Fatalf("missing-wallet funding reservation changed unexpectedly: %+v", got)
	}
}

func TestFundingReservationRecoveryRejectsMismatchedGeneration(t *testing.T) {
	database := newMemoryKVDB()
	walletValue := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire",
		"", GetChainParam(),
	)
	if walletValue == nil {
		t.Fatal("create wallet")
	}
	stored := savePendingFundingFixture(t, database, walletValue, "generationmismatch", 404)
	stored.FundingTime = 403
	stored.StaticMerkleRoot = stored.CalcStaticMerkleRoot()
	if err := SaveChannelInDB(database, stored); err != nil {
		t.Fatal(err)
	}

	manager := &Manager{
		db:                database,
		wallet:            walletValue,
		walletInfoMap:     map[int64]*WalletInfo{walletValue.GetId(): {WalletInDB: WalletInDB{Id: walletValue.GetId()}, Wallet: walletValue}},
		channelMap:        make(map[string]*Channel),
		fundingChannelMap: make(map[int64]*FundingReservation),
	}
	manager.resetResvMapsLocked()
	for _, resv := range mustLoadAllResv(t, database) {
		manager.addResv(resv)
	}
	manager.rehydratePendingFundingRuntime()
	if got := manager.GetFundingReservations()[404]; got == nil || got.Channel != nil {
		t.Fatalf("mismatched generation was rehydrated: %+v", got)
	}
	if _, err := LoadReservation(database, manager, RESV_TYPE_OPEN, 404); err != nil {
		t.Fatalf("pending reservation was deleted after rejected recovery: %v", err)
	}
}

func TestClosingReservationRehydratesAndPersistsTerminalState(t *testing.T) {
	database := newMemoryKVDB()
	walletValue := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire",
		"", GetChainParam(),
	)
	if walletValue == nil {
		t.Fatal("create wallet")
	}
	const reservationID int64 = 505
	const channelID = "pendingclosingrehydration"
	stored := savePendingFundingFixture(t, database, walletValue, channelID, reservationID)
	if err := DeleteReservation(database, RESV_TYPE_OPEN, reservationID); err != nil {
		t.Fatal(err)
	}
	stored.Status = CS_CLOSING_DEANCHOR_BROADCASTED
	stored.UpdateTime = reservationID
	stored.StaticMerkleRoot = stored.CalcStaticMerkleRoot()
	if err := SaveChannelInDB(database, stored); err != nil {
		t.Fatal(err)
	}
	closing := &ClosingReservation{ClosingDataInDB: ClosingDataInDB{
		ReservationBase: NewReservationBase(reservationID, true, ResvStatus(CS_CLOSING_DEANCHOR_BROADCASTED), walletValue),
		ChannelId:       channelID,
	}}
	if err := SaveReservation(database, closing); err != nil {
		t.Fatal(err)
	}

	manager := &Manager{
		db:            database,
		wallet:        walletValue,
		walletInfoMap: map[int64]*WalletInfo{walletValue.GetId(): {WalletInDB: WalletInDB{Id: walletValue.GetId()}, Wallet: walletValue}},
		channelMap:    make(map[string]*Channel),
	}
	manager.resetResvMapsLocked()
	for _, resv := range mustLoadAllResv(t, database) {
		manager.addResv(resv)
	}
	original := manager.GetClosingReservations()[reservationID]
	if original == nil {
		t.Fatal("closing reservation was not loaded")
	}
	originalMutex := original.Mutex()
	manager.rehydratePendingClosingRuntime()
	restored := manager.GetClosingReservations()[reservationID]
	if restored == nil || restored.Channel == nil || restored.Channel.ChannelId != channelID {
		t.Fatalf("pending close not restored: %+v", restored)
	}
	if restored == original || restored.Mutex() == originalMutex {
		t.Fatal("closing runtime recovery reused the persisted runtime object or mutex")
	}

	if err := manager.HandleChannelClosed(restored); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.GetClosingReservations()[reservationID]; ok {
		t.Fatal("terminal closing reservation remained active")
	}
	persisted, err := LoadReservation(database, manager, RESV_TYPE_CLOSE, reservationID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.GetStatus() != RS_CLOSED {
		t.Fatalf("persisted closing status=%d, want closed", persisted.GetStatus())
	}
	closedChannel, err := manager.LoadChannelInDB(channelID)
	if err != nil {
		t.Fatal(err)
	}
	if closedChannel.Status != CS_CLOSED {
		t.Fatalf("persisted channel status=%d, want closed", closedChannel.Status)
	}
}

func TestClosingReservationRehydratesAnchorConfirmedState(t *testing.T) {
	database := newMemoryKVDB()
	walletValue := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire",
		"", GetChainParam(),
	)
	if walletValue == nil {
		t.Fatal("create wallet")
	}
	const reservationID int64 = 506
	const channelID = "anchorconfirmedclosingrehydration"
	stored := savePendingFundingFixture(t, database, walletValue, channelID, reservationID)
	stored.Status = CS_ANCHOR_CONFIRMED
	stored.UpdateTime = reservationID
	stored.StaticMerkleRoot = stored.CalcStaticMerkleRoot()
	if err := SaveChannelInDB(database, stored); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		db:            database,
		walletInfoMap: map[int64]*WalletInfo{walletValue.GetId(): {WalletInDB: WalletInDB{Id: walletValue.GetId()}, Wallet: walletValue}},
	}
	restored, terminal, err := manager.loadPendingClosingChannel(channelID, reservationID, walletValue.GetWalletId())
	if err != nil {
		t.Fatal(err)
	}
	if terminal || restored == nil || restored.Status != CS_ANCHOR_CONFIRMED {
		t.Fatalf("anchor-confirmed close restored=%+v terminal=%t", restored, terminal)
	}
}
