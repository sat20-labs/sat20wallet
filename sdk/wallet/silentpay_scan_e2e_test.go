package wallet

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	db "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/stretchr/testify/require"
)

func spE2EAccountManager(t *testing.T) (*Manager, *InternalWallet) {
	t.Helper()
	w := NewInternalWalletWithMnemonic(silentPayE2EMnemonic, "", &chaincfg.TestNet4Params)
	if w == nil {
		t.Fatal("fixture mnemonic unavailable")
	}
	info := &WalletInfo{WalletInDB: WalletInDB{
		Id: w.GetId(), Accounts: 2, Type: WALLET_TYPE_MNEMONIC,
		Name: "Personal", AccountNames: map[uint32]string{0: "Main", 1: "Savings"},
		AccountDIDs: map[uint32]string{},
	}, Wallet: w}
	mgr := &Manager{
		db: newMemoryKVDB(), status: newDefaultStatus(),
		walletInfoMap: map[int64]*WalletInfo{w.GetId(): info},
		wallet:        w, l1IndexerClient: NewIndexerRPCClientMgr(),
	}
	t.Cleanup(func() { mgr.l1IndexerClient.Stop() })
	return mgr, w
}

// Use real HTTP, the production RESTClient, signed Bitcoin transaction bytes
// and the SDK scanner. Only the L1 Indexer public records are substituted by
// a deterministic mock; no test build tags, no private keys on the server.
func TestSilentPaySDKHTTPScanEnablesAndRestoresAccountBalancesE2E(t *testing.T) {
	originalChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = originalChain })
	for _, state := range []accountGroupState{accountGroupInactive, accountGroupReady} {
		t.Run(string(state), func(t *testing.T) {
			mgr, wallet := spAccountGroup(t, state)
			runSPAccountScan(t, mgr, wallet)
		})
	}
}

func spAccountGroup(t *testing.T, state accountGroupState) (*Manager, *InternalWallet) {
	t.Helper()
	manager, wallet := spE2EAccountManager(t)
	manager.status.CurrentWallet = wallet.GetId()
	// Account activation imports the wallet catalog and rebinds its RGB scope,
	// even when the account has no RGB assets.
	rgb, err := newRGB11Manager(manager, manager.db, nil, nil)
	require.NoError(t, err)
	manager.rgbManager = rgb
	require.NoError(t, rgb.selectRGB11Scope())
	prepareAccountGroup(t, manager, state, silentPayE2EMnemonic, "password")
	return manager, wallet
}

func runSPAccountScan(t *testing.T, mgr *Manager, wallet *InternalWallet) {
	t.Helper()
	if _, err := mgr.LastSyncedSilentPay(wallet.GetId(), 1); err == nil {
		t.Fatal("unknown balance was incorrectly displayed as zero")
	}
	addr0, err := mgr.EnableSilentPay(wallet.GetId(), 0)
	if err != nil {
		t.Fatal(err)
	}
	addr1, err := mgr.EnableSilentPay(wallet.GetId(), 1)
	if err != nil || addr0 == addr1 {
		t.Fatal("SP account activation failed", err)
	}
	state := mgr.GetWalletCatalog()
	if len(state) != 1 || len(state[0].Accounts) != 2 ||
		!state[0].Accounts[0].SPEnabled || !state[0].Accounts[1].SPEnabled {
		t.Fatal("account catalog forgot SP opt-ins")
	}

	// A different account's Spend/Scan Key must not match the payment.
	recipient, err := DecodeSilentPayAddress(addr1, &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	payerBytes := make([]byte, 32)
	payerBytes[31] = 19
	payer, _ := btcec.PrivKeyFromBytes(payerBytes)
	op := wire.OutPoint{Hash: chainhash.Hash{11, 22, 33}, Index: 2}
	spScript, err := SilentPayDestinationScript(recipient, []SilentPaySenderInput{{Outpoint: op, Key: payer}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var outpointBytes [36]byte
	copy(outpointBytes[:32], op.Hash[:])
	binary.LittleEndian.PutUint32(outpointBytes[32:], op.Index)
	normalized := payer.Key
	if payer.PubKey().SerializeCompressed()[0] == 3 {
		normalized.Negate()
	}
	normalizedPub := btcec.PrivKeyFromScalar(&normalized).PubKey()
	ih, err := silentPayCheckedScalar(silentPayTaggedHash("BIP0352/Inputs", outpointBytes[:], normalizedPub.SerializeCompressed()))
	if err != nil {
		t.Fatal(err)
	}
	T, err := silentPaySharedSecretPoint(&ih, normalizedPub)
	if err != nil {
		t.Fatal(err)
	}

	payment := wire.NewMsgTx(2)
	payment.AddTxIn(wire.NewTxIn(&op, nil, nil))
	payment.AddTxOut(wire.NewTxOut(105000, spScript))
	var serialized bytes.Buffer
	if err := payment.Serialize(&serialized); err != nil {
		t.Fatal(err)
	}
	txid := payment.TxHash().String()
	blockHash := (chainhash.Hash{102}).String()
	anchor := SPBaseAnchor{Height: 102, BlockHash: blockHash}
	pages := []SPIndexerPage{
		{SnapshotID: "s1", Anchor: anchor, Done: false, NextCursor: "next",
			Blocks: []SPIndexerBlock{{Height: 102, BlockHash: blockHash,
				Transactions: []SPIndexerTransaction{{TxIndex: 1, Tweak: T.SerializeCompressed(),
					P2TROutputs:   []SPIndexerOutput{{Vout: 0, OutputKey: spScript[2:]}},
					UnspentBitmap: []byte{1}}}}}},
		{SnapshotID: "s1", Anchor: anchor, Done: true},
	}
	var requests atomic.Int32
	var failNext atomic.Bool
	failNext.Store(true)
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, data any) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(data); err != nil {
			t.Error(err)
		}
	}
	mux.HandleFunc("/test/v1/sp/status", func(w http.ResponseWriter, r *http.Request) {
		write(w, SPIndexerStatus{Network: "testnet4", SchemaVersion: 1,
			CoverageStartHeight: 0, BaseAnchor: anchor, DataReady: true})
	})
	mux.HandleFunc("/test/v1/sp/live-snapshot", func(w http.ResponseWriter, r *http.Request) {
		write(w, SPIndexerSnapshot{SnapshotID: "s1", Anchor: anchor, SchemaVersion: 1, CoverageStartHeight: 0})
	})
	mux.HandleFunc("/test/v1/sp/scan-data", func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Query().Get("mode") != "live" || r.URL.Query().Get("format") != "json" ||
			r.URL.Query().Get("snapshot_id") != "s1" {
			t.Error("SDK sent an invalid SP scan request")
		}
		if r.URL.Query().Get("cursor") == "next" {
			if failNext.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				write(w, map[string]any{"code": -1, "msg": "temporary scan failure"})
				return
			}
			write(w, pages[1])
		} else {
			write(w, pages[0])
		}
	})
	mux.HandleFunc("/test/v1/sp/txids-by-position", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"code": 0, "msg": "ok", "data": []SPIndexerPositionResult{{
			SPIndexerPosition: SPIndexerPosition{Height: 102, BlockHash: blockHash, TxIndex: 1},
			TxID:              txid,
		}}})
	})
	mux.HandleFunc("/test/v3/bitcoin/rawtx/batch", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"code": 0, "msg": "ok", "data": []map[string]any{{
			"txid": txid, "raw_tx": hex.EncodeToString(serialized.Bytes()),
		}}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := NewIndexerClient("http", strings.TrimPrefix(server.URL, "http://"), "/test",
		&NetClient{Client: &http.Client{Timeout: 5 * time.Second}})
	mgr.l1IndexerClient.SetMaster(client)

	_, err = mgr.SyncSilentPayLive(context.Background())
	if err == nil {
		t.Fatal("partial scan incorrectly succeeded")
	}
	if _, err = mgr.LastSyncedSilentPay(wallet.GetId(), 1); err == nil {
		t.Fatal("partial HTTP page committed a misleading account balance")
	}
	failNext.Store(false)
	results, err := mgr.SyncSilentPayLive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || !results[0].Complete || !results[1].Complete ||
		results[0].Balance != 0 || results[1].Balance != 105000 ||
		len(results[1].Outputs) != 1 || results[1].Outputs[0].TxID != txid {
		t.Fatalf("account isolation or recovered balance mismatch: %+v", results)
	}
	if requests.Load() < 4 {
		t.Fatalf("expected page retry, got %d requests", requests.Load())
	}
	last, err := mgr.LastSyncedSilentPay(wallet.GetId(), 1)
	if err != nil || last.Balance != 105000 || len(last.Outputs) != 1 {
		t.Fatalf("persisted SP funds missing: %+v %v", last, err)
	}
	spendKeys, err := wallet.DeriveSilentPayKeys(1)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := SilentPayOutputSpendKey(spendKeys, SilentPayMatch{
		Vout: last.Outputs[0].Vout, K: last.Outputs[0].K,
		OutputKey: spScript[2:], Tweak: last.Outputs[0].Tweak,
	})
	if err != nil || priv == nil {
		t.Fatalf("restored UTXO no longer spendable: %v", err)
	}
}

func TestSilentPayStateMultiDeviceE2E(t *testing.T) {
	first, second, remote := reviewAccountDevices(t)
	firstID := first.GetAccountManagementStatus().RootWalletID
	secondID := second.GetAccountManagementStatus().RootWalletID
	require.NoError(t, first.EnsureAccount(firstID, 1, "Savings", ""))
	syncImportGroup(t, first, nil)
	syncImportGroup(t, second, nil)

	store, err := first.accountDKVSStore()
	require.NoError(t, err)
	status := first.GetAccountManagementStatus()
	indexKey, err := account.ManagedIndexKey(status.AccountID)
	require.NoError(t, err)
	indexBefore, err := store.GetAuthoritative(indexKey)
	require.NoError(t, err)

	main, err := first.EnableSilentPay(firstID, 0)
	require.NoError(t, err)
	savings, err := second.EnableSilentPay(secondID, 1)
	require.NoError(t, err)
	require.NotEmpty(t, main)
	require.NotEmpty(t, savings)
	require.NotEqual(t, main, savings)
	syncImportGroup(t, first, nil)
	syncImportGroup(t, second, nil)
	syncImportGroup(t, first, nil)

	indexAfter, err := store.GetAuthoritative(indexKey)
	require.NoError(t, err)
	require.Equal(t, indexBefore.Hash, indexAfter.Hash)
	for _, index := range []uint32{0, 1} {
		scope := AccountManagedDataScope{
			Network: first.GetChain(), WalletFingerprint: status.RootFingerprint, AccountIndex: index,
		}
		key, err := account.ManagedModuleKey(first.accountSecret, status.AccountID, "silentpay", scope.ID())
		require.NoError(t, err)
		_, err = store.GetAuthoritative(key)
		require.ErrorIs(t, err, ErrDKVSRecordNotFound)
	}

	restored := reviewColdCopy(t, first, remote)
	for _, manager := range []*Manager{first, second, restored} {
		id := manager.GetAccountManagementStatus().RootWalletID
		for index, want := range []string{main, savings} {
			enabled, address, err := manager.SilentPayAccount(id, uint32(index))
			require.NoError(t, err)
			require.True(t, enabled)
			require.Equal(t, want, address)
		}
	}
}

var errSPCommitInjected = errors.New("silentpay: injected wallet persistence failure")

type spFailCommitDB struct {
	db.KVDB
	hits atomic.Uint32
}

func (d *spFailCommitDB) NewWriteBatch() db.WriteBatch {
	return &spFailCommitBatch{WriteBatch: d.KVDB.NewWriteBatch(), owner: d}
}

func (d *spFailCommitDB) NewAtomicWriteBatch() db.WriteBatch {
	atomicDB, ok := d.KVDB.(db.AtomicWriteBatchDB)
	if !ok {
		return nil
	}
	batch := atomicDB.NewAtomicWriteBatch()
	if batch == nil {
		return nil
	}
	return &spFailCommitBatch{WriteBatch: batch, owner: d}
}

type spFailCommitBatch struct {
	db.WriteBatch
	owner *spFailCommitDB
}

func (b *spFailCommitBatch) Flush() error {
	b.owner.hits.Add(1)
	return errSPCommitInjected
}

func TestSilentPayActivationFailsAtomicallyOnStorageError(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager, w := spAccountGroup(t, accountGroupInactive)
	original := manager.db
	faultDB := &spFailCommitDB{KVDB: original}
	manager.db = faultDB
	if address, err := manager.EnableSilentPay(w.GetId(), 1); !errors.Is(err, errSPCommitInjected) || address != "" {
		t.Fatalf("failed transaction did not preserve the injected SP error: address_empty=%t injected_match=%t", address == "", errors.Is(err, errSPCommitInjected))
	}
	if faultDB.hits.Load() != 1 {
		t.Fatalf("SP atomic commit fault hits=%d, want 1", faultDB.hits.Load())
	}
	if manager.GetWalletCatalog()[0].Accounts[1].SPEnabled {
		t.Fatal("failed SP activation changed the live account catalog")
	}
	raw, err := original.Read([]byte(getWalletDBKey(w.GetId())))
	if err == nil {
		var persisted WalletInDB
		if err := DecodeFromBytes(raw, &persisted); err != nil {
			t.Fatal(err)
		}
		if persisted.SPEnabled[1] {
			t.Fatal("failed SP activation wrote the account flag")
		}
	}
	manager.db = original
	if _, err := manager.EnableSilentPay(w.GetId(), 1); err != nil {
		t.Fatal(err)
	}
	raw, err = original.Read([]byte(getWalletDBKey(w.GetId())))
	if err != nil {
		t.Fatal(err)
	}
	var persisted WalletInDB
	if err := DecodeFromBytes(raw, &persisted); err != nil || !persisted.SPEnabled[1] {
		t.Fatalf("successful SP activation is not durable: %v", err)
	}
}

func TestDeleteSilentPayWallet(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	mgr, first, second := newSPWalletPairGroup(t, LIGHT_NODE, []string{"Income"})
	_, err := mgr.EnableSilentPay(second.GetId(), 0)
	require.NoError(t, err)
	child, root := walletFingerprint(second), walletFingerprint(first)
	keys := [][]byte{spAccountDataKey("testnet4", child, 0), spHistoryKey("testnet4", child, 0),
		spAccountDataKey("mainnet", child, 0), spHistoryKey("mainnet", child, 0)}
	rootHistory := spHistoryKey("testnet4", root, 0)
	for _, key := range append(keys, rootHistory) {
		require.NoError(t, mgr.db.Write(key, []byte("saved")))
	}
	// An RGB reservation identifies this carrier even if a channel has since locked it.
	carrier := strings.Repeat("a", 64) + ":0"
	reservation := RGB11TransferReservation{LockOutpoint: carrier}
	reservation.Id = 41
	reservation.WalletId.Id = second.GetId()
	reservation.Scope = rgb11StorageScope(second.GetId(), 0)
	raw, err := EncodeToBytes(&reservation)
	require.NoError(t, err)
	resvKey := []byte(GetResvKey(RESV_TYPE_RGB11, reservation.Id))
	require.NoError(t, mgr.db.Write(resvKey, raw))
	require.NoError(t, mgr.utxoLockerL1.LockUtxo(carrier, "channel"))
	dapp := strings.Repeat("b", 64) + ":1"
	other := strings.Repeat("c", 64) + ":2"
	for _, locker := range []*UtxoLocker{mgr.utxoLockerL1, mgr.utxoLockerL2} {
		owner := UtxoLockOwner{Origin: "https://app.test", Network: locker.network, WalletFingerprint: child}
		require.NoError(t, locker.LockUtxoForOwner(dapp, "app", owner))
		owner.WalletFingerprint = root
		require.NoError(t, locker.LockUtxoForOwner(other, "app", owner))
	}
	manual := strings.Repeat("d", 64) + ":0"
	require.NoError(t, mgr.SwitchWallet(second.GetId(), "password"))
	require.NoError(t, mgr.LockUtxo(second.GetAddress(), manual, "manual"))
	require.NoError(t, mgr.LockUtxo_SatsNet(second.GetAddress(), manual, "manual"))
	require.NoError(t, mgr.SwitchWallet(first.GetId(), "password"))
	mainKey := func(key []byte) []byte {
		prefix := GetDBKeyPrefix()
		require.Equal(t, "test-testnet-", prefix)
		require.True(t, bytes.HasPrefix(key, []byte(prefix)))
		return []byte("test-mainnet-" + string(key[len(prefix):]))
	}
	// A browser uses one physical DB. Seed its other network with the same
	// production-encoded rows and retain an independently owned control lock.
	copyToMain := func(key []byte) []byte {
		row, err := mgr.db.Read(key)
		require.NoError(t, err)
		otherKey := mainKey(key)
		require.NoError(t, mgr.db.Write(otherKey, row))
		return otherKey
	}
	keys = append(keys, copyToMain(resvKey), copyToMain([]byte(GetLockedUtxoKey(L1_NETWORK_BITCOIN, carrier))))
	var keep [][]byte
	for _, locker := range []*UtxoLocker{mgr.utxoLockerL1, mgr.utxoLockerL2} {
		for _, point := range []string{manual, dapp} {
			key := []byte(GetLockedUtxoKey(locker.network, point))
			keys = append(keys, key, copyToMain(key))
		}
		keep = append(keep, copyToMain([]byte(GetLockedUtxoKey(locker.network, other))))
	}
	logKey := mainKey([]byte(operationLogRecordKey("same-id")))
	linkKey := mainKey([]byte(operationLogRelationKey(RESV_TYPE_RGB11, 41)))
	logRow, err := EncodeToBytes(OperationLogRecord{ID: "same-id", WalletID: second.GetId()})
	require.NoError(t, err)
	require.NoError(t, mgr.db.Write(logKey, logRow))
	require.NoError(t, mgr.db.Write(linkKey, []byte("same-id")))
	keys = append(keys, logKey, linkKey)
	original := mgr.db
	mgr.db = &spFailCommitDB{KVDB: original}
	require.ErrorIs(t, mgr.DeleteWallet(second.GetId()), errSPCommitInjected)
	mgr.db = original
	require.Contains(t, mgr.walletInfoMap, second.GetId())
	require.True(t, mgr.utxoLockerL1.IsLocked(carrier))
	for _, key := range keys {
		_, err := mgr.db.Read(key)
		require.NoError(t, err)
	}
	require.NoError(t, mgr.DeleteWallet(second.GetId()))
	require.NotContains(t, mgr.walletInfoMap, second.GetId())
	for _, key := range append(keys, resvKey) {
		_, err := mgr.db.Read(key)
		require.ErrorIs(t, err, db.ErrKeyNotFound)
	}
	for _, key := range keep {
		_, err := mgr.db.Read(key)
		require.NoError(t, err)
	}
	got, err := mgr.db.Read(rootHistory)
	require.NoError(t, err)
	require.Equal(t, []byte("saved"), got)
	require.False(t, mgr.utxoLockerL1.IsLocked(carrier))
	for _, locker := range []*UtxoLocker{mgr.utxoLockerL1, mgr.utxoLockerL2} {
		require.False(t, locker.IsLocked(manual))
		require.False(t, locker.IsLocked(dapp))
		require.True(t, locker.IsLocked(other))
		_, err := mgr.db.Read([]byte(GetLockedUtxoKey(locker.network, dapp)))
		require.ErrorIs(t, err, db.ErrKeyNotFound)
	}
}
