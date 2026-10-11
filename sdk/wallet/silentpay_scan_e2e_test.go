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
	mgr, wallet := spE2EAccountManager(t)
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

func TestSilentPayManagedDataRestoresMonotonicAccountFlag(t *testing.T) {
	originalChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = originalChain })
	mgr, w := spE2EAccountManager(t)
	if _, err := mgr.EnableSilentPay(w.GetId(), 1); err != nil {
		t.Fatal(err)
	}
	provider := &silentPayAccountManagedDataProvider{owner: mgr}
	catalog := AccountManagedDataCatalog{Network: "testnet", Scopes: []AccountManagedDataScope{
		{WalletID: w.GetId(), WalletFingerprint: walletFingerprint(w), AccountIndex: 0, Network: "testnet"},
		{WalletID: w.GetId(), WalletFingerprint: walletFingerprint(w), AccountIndex: 1, Network: "testnet"},
	}}
	payload, err := provider.Export(catalog)
	if err != nil || len(payload) != 1 || payload[0].Scope != catalog.Scopes[1].ID() {
		t.Fatal("SP opt-in not exported", err)
	}
	other, otherWallet := spE2EAccountManager(t)
	otherProvider := &silentPayAccountManagedDataProvider{owner: other}
	restoredCatalog := AccountManagedDataCatalog{Network: "testnet", Scopes: []AccountManagedDataScope{
		{WalletFingerprint: walletFingerprint(otherWallet), AccountIndex: 0, Network: "testnet"},
		{WalletFingerprint: walletFingerprint(otherWallet), AccountIndex: 1, Network: "testnet"},
	}}
	if err := otherProvider.Import(restoredCatalog, payload); err != nil {
		t.Fatal(err)
	}
	if other.GetWalletCatalog()[0].Accounts[0].SPEnabled ||
		!other.GetWalletCatalog()[0].Accounts[1].SPEnabled {
		t.Fatal("SP recovered in the wrong subaccount")
	}
	// Older/stale managed-data snapshots never silently disable an address
	// that may have been published already.
	if err := otherProvider.Import(restoredCatalog, nil); err != nil {
		t.Fatal(err)
	}
	if !other.GetWalletCatalog()[0].Accounts[1].SPEnabled {
		t.Fatal("remote account metadata erased an existing SP address")
	}
	if _, _, err := other.SilentPayAccount(otherWallet.GetId(), 1); errors.Is(err, ErrSilentPayUnsupported) {
		t.Fatal("restored account unexpectedly locked")
	}
}

type spFailCommitDB struct{ db.KVDB }

func (d *spFailCommitDB) NewWriteBatch() db.WriteBatch {
	return &spFailCommitBatch{WriteBatch: d.KVDB.NewWriteBatch()}
}

type spFailCommitBatch struct{ db.WriteBatch }

func (b *spFailCommitBatch) Flush() error {
	return errors.New("silentpay: injected wallet persistence failure")
}

func TestSilentPayActivationFailsAtomicallyOnStorageError(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager, w := spE2EAccountManager(t)
	original := manager.db
	manager.db = &spFailCommitDB{KVDB: original}
	if address, err := manager.EnableSilentPay(w.GetId(), 1); err == nil || address != "" {
		t.Fatalf("failed transaction published SP address: %s %v", address, err)
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

func TestSilentPayEnabledWalletCannotBeDeleted(t *testing.T) {
	oldChain:=_chain
	_chain="testnet"
	defer func(){_chain=oldChain}()
	mgr,first:=spE2EAccountManager(t)
	second:=NewInternalWalletWithMnemonic(silentPayOtherMnemonic,"",&chaincfg.TestNet4Params)
	if second==nil{t.Fatal("second mnemonic unavailable")}
	mgr.walletInfoMap[second.GetId()]=&WalletInfo{WalletInDB:WalletInDB{
		Id:second.GetId(),Accounts:1,Type:WALLET_TYPE_MNEMONIC,Name:"Secondary",
		AccountNames:map[uint32]string{0:"Income"},AccountDIDs:map[uint32]string{},
	},Wallet:second}
	mgr.status.CurrentWallet=first.GetId()
	mgr.status.TotalWallet=2
	if _,err:=mgr.EnableSilentPay(second.GetId(),0);err!=nil{t.Fatal(err)}
	if err:=mgr.DeleteWallet(second.GetId());err==nil {
		t.Fatal("published SP address may be forgotten by deleting its mnemonic wallet")
	}
	if _,ok:=mgr.walletInfoMap[second.GetId()];!ok {
		t.Fatal("rejected deletion changed the wallet catalog")
	}
}
