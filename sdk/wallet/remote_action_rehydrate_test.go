package wallet

import (
	"bytes"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/common"
)

func remoteActionRestartConfig() *common.Config {
	return &common.Config{
		Env: "test", Chain: "testnet", Mode: CLIENT_NODE,
		IndexerL1: &common.Indexer{Scheme: "http", Host: "127.0.0.1:1"},
		IndexerL2: &common.Indexer{Scheme: "http", Host: "127.0.0.1:1"},
	}
}

func TestUnlockWalletRehydratesInitiatorRemoteActionSigningWallet(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })

	databasePath := t.TempDir()
	database := NewKVDB(databasePath)
	if database == nil {
		t.Fatal("open source Pebble database")
	}
	config := remoteActionRestartConfig()
	source := NewManager(config, database)
	if source == nil {
		t.Fatal("create source manager")
	}
	walletID, _, err := source.CreateWallet("password")
	if err != nil {
		t.Fatal(err)
	}
	source.SwitchAccount(2)
	signer := source.GetWallet().Clone()
	if signer == nil || signer.GetPaymentPubKey() == nil {
		t.Fatal("derive source signer")
	}
	const reservationID = int64(9101)
	resv := &RemoteActionPerformReservation{
		ReservationBase: NewReservationBase(reservationID, true, RS_PERFORM_ACTION_RUN_STARTED, signer),
		Action:          REMOTE_ACTION_DEPLOY_RUNES,
		ReqPubKey:       signer.GetPaymentPubKey().SerializeCompressed(),
		FeeTxId:         "commit-tx",
		ActionResult:    []byte("reveal-tx"),
		SendTxInL1:      true,
	}
	if err := source.SaveWalletReservation(resv); err != nil {
		t.Fatal(err)
	}
	// The selected wallet intentionally differs from the persisted signer.
	source.SwitchAccount(0)
	source.Close()
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	restartedDatabase := NewKVDB(databasePath)
	if restartedDatabase == nil {
		t.Fatal("reopen Pebble database")
	}
	restarted := NewManager(config, restartedDatabase)
	if restarted == nil {
		t.Fatal("create restarted manager")
	}
	t.Cleanup(func() {
		restarted.Close()
		_ = restartedDatabase.Close()
	})
	loaded, ok := restarted.GetRemoteActionReservation(reservationID)
	if !ok {
		t.Fatal("active reservation was not loaded")
	}
	if loaded.GetBase().LocalWallet() != nil {
		t.Fatal("locked restart restored signing material before unlock")
	}
	if _, err := restarted.UnlockWallet("password"); err != nil {
		t.Fatal(err)
	}

	localWallet := loaded.GetBase().LocalWallet()
	if localWallet == nil || localWallet.GetPaymentPubKey() == nil {
		t.Fatal("active initiator remote action signer was not rehydrated after unlock")
	}
	if got := localWallet.GetWalletId(); got.Id != walletID || got.SubAccountId != 2 {
		t.Fatalf("rehydrated wallet identity = %+v, want wallet=%d account=2", got, walletID)
	}
	if !bytes.Equal(localWallet.GetPaymentPubKey().SerializeCompressed(), resv.ReqPubKey) {
		t.Fatal("rehydrated signer does not match persisted request pubkey")
	}
}

func TestUnlockWalletRemoteActionSignerMismatchFailsClosed(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })

	database := newMemoryKVDB()
	config := remoteActionRestartConfig()
	source := NewManager(config, database)
	if source == nil {
		t.Fatal("create source manager")
	}
	if _, _, err := source.CreateWallet("password"); err != nil {
		t.Fatal(err)
	}
	rootSigner := source.GetWallet().Clone()
	source.SwitchAccount(2)
	accountSigner := source.GetWallet().Clone()
	const reservationID = int64(9201)
	resv := &RemoteActionPerformReservation{
		ReservationBase: NewReservationBase(reservationID, true, RS_PERFORM_ACTION_RUN_STARTED, accountSigner),
		Action:          REMOTE_ACTION_DEPLOY_RUNES,
		// Deliberately bind account 2 metadata to account 0's signer.
		ReqPubKey:    rootSigner.GetPaymentPubKey().SerializeCompressed(),
		FeeTxId:      "original-fee-tx",
		ActionResult: []byte("original-result"),
		SendTxInL1:   true,
	}
	if err := source.SaveWalletReservation(resv); err != nil {
		t.Fatal(err)
	}
	source.Close()

	restarted := NewManager(config, database)
	if restarted == nil {
		t.Fatal("create restarted manager")
	}
	t.Cleanup(restarted.Close)
	if _, err := restarted.UnlockWallet("password"); err != nil {
		t.Fatal(err)
	}
	loaded := restarted.GetRemoteAction(reservationID)
	if loaded == nil {
		t.Fatal("mismatched active reservation was silently discarded")
	}
	if loaded.LocalWallet() != nil {
		t.Fatal("mismatched signer was installed")
	}
	if loaded.Status != RS_PERFORM_ACTION_RUN_STARTED || loaded.FeeTxId != "original-fee-tx" ||
		string(loaded.ActionResult) != "original-result" || loaded.Invoice != nil {
		t.Fatalf("mismatched reservation was rewritten: %+v", loaded)
	}
	if err := restarted.handleRemoteActionStatus(loaded); err == nil {
		t.Fatal("monitor accepted a remote action without its exact signer")
	}
	if len(restarted.GetAllResv()) != 1 {
		t.Fatalf("rehydration created or removed reservations: %d", len(restarted.GetAllResv()))
	}
}

func TestRestartDoesNotActivateClosedRemoteAction(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })

	database := newMemoryKVDB()
	config := remoteActionRestartConfig()
	source := NewManager(config, database)
	if source == nil {
		t.Fatal("create source manager")
	}
	if _, _, err := source.CreateWallet("password"); err != nil {
		t.Fatal(err)
	}
	signer := source.GetWallet().Clone()
	const reservationID = int64(9301)
	resv := &RemoteActionPerformReservation{
		ReservationBase: NewReservationBase(reservationID, true, RS_CLOSED, signer),
		Action:          REMOTE_ACTION_DEPLOY_RUNES,
		ReqPubKey:       signer.GetPaymentPubKey().SerializeCompressed(),
	}
	if err := source.SaveWalletReservation(resv); err != nil {
		t.Fatal(err)
	}
	source.Close()

	restarted := NewManager(config, database)
	if restarted == nil {
		t.Fatal("create restarted manager")
	}
	t.Cleanup(restarted.Close)
	if _, err := restarted.UnlockWallet("password"); err != nil {
		t.Fatal(err)
	}
	if _, ok := restarted.GetRemoteActionReservation(reservationID); ok {
		t.Fatal("closed remote action was activated after restart")
	}
}
