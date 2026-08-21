package wallet

import (
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
)

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
	loaded := LoadAllResvFromDB(database, restarted)
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
	for _, resv := range LoadAllResvFromDB(manager.db, nil) {
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
	for _, resv := range LoadAllResvFromDB(database, nil) {
		manager.addResv(resv)
	}
	manager.rehydratePendingFundingRuntime()
	if got := manager.GetFundingReservations()[303]; got == nil || got.Channel != nil {
		t.Fatalf("missing-wallet funding reservation changed unexpectedly: %+v", got)
	}
}
