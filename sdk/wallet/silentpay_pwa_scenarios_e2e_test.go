package wallet

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
)

// PWA acceptance cases exercise the same Manager APIs exported by WASM.
// The existing HTTP E2E and genuine Bitcoin Core regtest test cover the
// transport and consensus boundary; here the fake returns only public L1 data.
type spPWATestPayment struct {
	tx *wire.MsgTx
	index uint32
	block SPIndexerBlock
	item SPIndexerTransaction
}

func spPWAMakePayment(t *testing.T, receiver *InternalWallet, account uint32,
	payerID byte, index uint32, height int, amounts ...int64) spPWATestPayment {
	t.Helper()
	address, err := receiver.GetSilentPayAddress(account)
	if err != nil {
		t.Fatal(err)
	}
	destination, err := DecodeSilentPayAddress(address, &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	var secret [32]byte
	secret[31] = payerID
	payer, _ := btcec.PrivKeyFromBytes(secret[:])
	previous := wire.OutPoint{Hash: chainhash.Hash{payerID, 0x34}, Index: uint32(payerID)}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&previous, nil, nil))
	item := SPIndexerTransaction{TxIndex: index, UnspentBitmap: []byte{byte((1 << uint(len(amounts))) - 1)}}
	for k, amount := range amounts {
		script, err := SilentPayDestinationScript(destination, []SilentPaySenderInput{
			{Outpoint: previous, Key: payer},
		}, uint32(k))
		if err != nil {
			t.Fatal(err)
		}
		tx.AddTxOut(wire.NewTxOut(amount, script))
		item.P2TROutputs = append(item.P2TROutputs, SPIndexerOutput{
			Vout: uint32(k), OutputKey: append([]byte(nil), script[2:]...),
		})
	}
	var serialized [36]byte
	copy(serialized[:32], previous.Hash[:])
	binary.LittleEndian.PutUint32(serialized[32:], previous.Index)
	normalized := payer.Key
	if payer.PubKey().SerializeCompressed()[0] == 3 {
		normalized.Negate()
	}
	aggregate := btcec.PrivKeyFromScalar(&normalized).PubKey()
	hash, err := silentPayCheckedScalar(silentPayTaggedHash(
		"BIP0352/Inputs", serialized[:], aggregate.SerializeCompressed(),
	))
	if err != nil {
		t.Fatal(err)
	}
	T, err := silentPaySharedSecretPoint(&hash, aggregate)
	if err != nil {
		t.Fatal(err)
	}
	item.Tweak = T.SerializeCompressed()
	blockHash := (chainhash.Hash{byte(height)}).String()
	return spPWATestPayment{
		tx: tx, index: index, item: item,
		block: SPIndexerBlock{Height: height, BlockHash: blockHash},
	}
}

type spPWAScenarioIndexer struct {
	IndexerRPCClient
	status SPIndexerStatus
	snapshot SPIndexerSnapshot
	pages []SPIndexerPage
	txByIndex map[uint32]*wire.MsgTx
	txByID map[string]*wire.MsgTx
	failCursor string
	badRawTx bool
	badLookup bool
	scans int
	lookups int
}

func spPWANewIndexer(payments ...spPWATestPayment) *spPWAScenarioIndexer {
	anchor := SPBaseAnchor{Height: 15, BlockHash: (chainhash.Hash{15}).String()}
	transactions := make([]SPIndexerTransaction, 0, len(payments))
	byIndex := make(map[uint32]*wire.MsgTx)
	byID := make(map[string]*wire.MsgTx)
	for _, payment := range payments {
		transactions = append(transactions, payment.item)
		byIndex[payment.index] = payment.tx
		byID[payment.tx.TxHash().String()] = payment.tx
	}
	sort.Slice(transactions, func(i, j int) bool {
		return transactions[i].TxIndex < transactions[j].TxIndex
	})
	page := SPIndexerPage{
		SnapshotID: "pwa-view", Anchor: anchor, Done: true,
		Blocks: []SPIndexerBlock{{Height: 15, BlockHash: anchor.BlockHash, Transactions: transactions}},
	}
	return &spPWAScenarioIndexer{
		status: SPIndexerStatus{Network: "testnet4", SchemaVersion: 1,
			CoverageStartHeight: 0, BaseAnchor: anchor, DataReady: true},
		snapshot: SPIndexerSnapshot{SnapshotID: "pwa-view", Anchor: anchor,
			CoverageStartHeight: 0, SchemaVersion: 1},
		pages: []SPIndexerPage{page},
		txByIndex: byIndex, txByID: byID,
	}
}

func (s *spPWAScenarioIndexer) SPStatus(context.Context) (SPIndexerStatus, error) {
	return s.status, nil
}

func (s *spPWAScenarioIndexer) SPSnapshot(context.Context) (SPIndexerSnapshot, error) {
	return s.snapshot, nil
}

func (s *spPWAScenarioIndexer) SPScan(_ context.Context, req SPScanRequest) (SPIndexerPage, error) {
	s.scans++
	if req.Mode != "live" || req.SnapshotID != s.snapshot.SnapshotID ||
		req.ToHeight != s.snapshot.Anchor.Height {
		return SPIndexerPage{}, fmt.Errorf("unexpected PWA Live request: %+v", req)
	}
	if req.Cursor == s.failCursor && s.failCursor != "" {
		return SPIndexerPage{}, fmt.Errorf("injected connection loss during SP pagination")
	}
	if req.Cursor == "" {
		return s.pages[0], nil
	}
	if req.Cursor == "second" && len(s.pages) > 1 {
		return s.pages[1], nil
	}
	return SPIndexerPage{}, fmt.Errorf("unexpected cursor %q", req.Cursor)
}

func (s *spPWAScenarioIndexer) SPLookup(_ context.Context,
	positions []SPIndexerPosition) ([]SPIndexerPositionResult, error) {
	s.lookups++
	results := make([]SPIndexerPositionResult, 0, len(positions))
	for _, position := range positions {
		tx := s.txByIndex[position.TxIndex]
		if tx == nil {
			return nil, fmt.Errorf("invalid candidate position")
		}
		txid := tx.TxHash().String()
		if s.badLookup {
			txid = (chainhash.Hash{0xaa}).String()
		}
		results = append(results, SPIndexerPositionResult{
			SPIndexerPosition: position, TxID: txid,
		})
	}
	return results, nil
}

func (s *spPWAScenarioIndexer) SPOutpoints(context.Context, string,
	[]SPIndexerPosition) (SPBaseAnchor, []SPIndexerPositionResult, error) {
	return SPBaseAnchor{}, nil, errors.New("not used in Live recovery cases")
}

func (s *spPWAScenarioIndexer) GetBitcoinRawTx(txid string) (*indexerwire.BitcoinRawTx, error) {
	tx := s.txByID[txid]
	if tx == nil {
		return nil, fmt.Errorf("unknown real Bitcoin txid %s", txid)
	}
	var raw bytes.Buffer
	if err := tx.Serialize(&raw); err != nil {
		return nil, err
	}
	if s.badRawTx {
		return &indexerwire.BitcoinRawTx{TxID: txid, RawTx: "deadbeef"}, nil
	}
	return &indexerwire.BitcoinRawTx{TxID: txid, RawTx: hex.EncodeToString(raw.Bytes())}, nil
}

func spPWAChain(t *testing.T) {
	t.Helper()
	old := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = old })
}

func spPWAFindBalance(t *testing.T, values []SPAccountBalance,
	walletID int64, index uint32) SPAccountBalance {
	t.Helper()
	for _, value := range values {
		if value.WalletID == walletID && value.AccountIndex == index {
			return value
		}
	}
	t.Fatalf("missing SP account %d/%d in %+v", walletID, index, values)
	return SPAccountBalance{}
}

func TestSilentPayPWAMultiWalletIsolationRepeatRefreshAndLateActivationE2E(t *testing.T) {
	spPWAChain(t)
	manager, first := spE2EAccountManager(t)
	second := NewInternalWalletWithMnemonic(silentPayOtherMnemonic, "", &chaincfg.TestNet4Params)
	if second == nil {
		t.Fatal("second mnemonic wallet unavailable")
	}
	second.id = first.GetId() + 100
	manager.walletInfoMap[second.GetId()] = &WalletInfo{
		WalletInDB: WalletInDB{
			Id: second.GetId(), Accounts: 2, Type: WALLET_TYPE_MNEMONIC,
			Name: "Secondary", AccountNames: map[uint32]string{0: "Payments", 1: "Unpublished"},
			AccountDIDs: map[uint32]string{},
		},
		Wallet: second,
	}
	for _, item := range []struct {
		wallet *InternalWallet
		index uint32
	}{
		{first, 0}, {first, 1}, {second, 0},
	} {
		if _, err := manager.EnableSilentPay(item.wallet.GetId(), item.index); err != nil {
			t.Fatal(err)
		}
	}
	if enabled, address, err := manager.SilentPayAccount(second.GetId(), 1);
		err != nil || enabled || address != "" {
		t.Fatalf("unpublished account was enabled: %t %q %v", enabled, address, err)
	}
	// No account borrows another account's checkpoint, even in the same Root.
	if _, err := manager.LastSyncedSilentPay(second.GetId(), 1); err == nil {
		t.Fatal("unpublished account unexpectedly had a balance checkpoint")
	}
	one := spPWAMakePayment(t, first, 0, 31, 1, 15, 60000)
	two := spPWAMakePayment(t, first, 1, 32, 2, 15, 75000)
	three := spPWAMakePayment(t, second, 0, 33, 3, 15, 90000)
	reader := spPWANewIndexer(one, two, three)
	manager.l1IndexerClient.SetMaster(reader)

	for run := 0; run < 2; run++ {
		// Equivalent to a PWA account switch; the scanner is deliberately
		// independent of the current UI account selection.
		first.SetSubAccount(uint32(run))
		result, err := manager.SyncSilentPayLive(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(result) != 3 ||
			spPWAFindBalance(t, result, first.GetId(), 0).Balance != 60000 ||
			spPWAFindBalance(t, result, first.GetId(), 1).Balance != 75000 ||
			spPWAFindBalance(t, result, second.GetId(), 0).Balance != 90000 {
			t.Fatalf("PWA wallet or account scope leaked on refresh %d: %+v", run, result)
		}
		for _, item := range result {
			if !item.Complete || len(item.Outputs) != 1 {
				t.Fatalf("refreshed balance was duplicated: %+v", item)
			}
		}
	}
	if reader.scans != 2 {
		t.Fatalf("three accounts should share two Live scans, got %d", reader.scans)
	}
	// Enabling a fourth account after an existing scan must not invent a
	// previous scan checkpoint or inherit somebody else's BTC.
	if _, err := manager.EnableSilentPay(second.GetId(), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.LastSyncedSilentPay(second.GetId(), 1); err == nil {
		t.Fatal("late-enabled account inherited existing snapshot")
	}
	result, err := manager.SyncSilentPayLive(context.Background())
	if err != nil || len(result) != 4 {
		t.Fatalf("late account scan failed: %+v %v", result, err)
	}
	late := spPWAFindBalance(t, result, second.GetId(), 1)
	if !late.Complete || late.Balance != 0 || len(late.Outputs) != 0 {
		t.Fatalf("late account should have a verified zero snapshot: %+v", late)
	}
}

func TestSilentPayPWACloseReopenRestoresScanAndEnablementE2E(t *testing.T) {
	spPWAChain(t)
	manager, wallet := spE2EAccountManager(t)
	address, err := manager.EnableSilentPay(wallet.GetId(), 1)
	if err != nil {
		t.Fatal(err)
	}
	payment := spPWAMakePayment(t, wallet, 1, 43, 1, 15, 123456)
	manager.l1IndexerClient.SetMaster(spPWANewIndexer(payment))
	if _, err := manager.SyncSilentPayLive(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Reconstruct a different SDK Manager from persisted wallet metadata and
	// the same wallet storage. Do not reuse unlocked secret objects in memory.
	walletRow, err := manager.db.Read([]byte(getWalletDBKey(wallet.GetId())))
	if err != nil {
		t.Fatal(err)
	}
	var stored WalletInDB
	if err := DecodeFromBytes(walletRow, &stored); err != nil {
		t.Fatal(err)
	}
	recoveredWallet := NewInternalWalletWithMnemonic(silentPayE2EMnemonic, "", &chaincfg.TestNet4Params)
	if recoveredWallet == nil {
		t.Fatal("restored mnemonic unavailable")
	}
	recoveredWallet.id = wallet.GetId()
	restored := &Manager{
		db: manager.db, status: newDefaultStatus(),
		walletInfoMap: map[int64]*WalletInfo{
			recoveredWallet.GetId(): {WalletInDB: stored, Wallet: recoveredWallet},
		},
	}
	if enabled, regenerated, err := restored.SilentPayAccount(wallet.GetId(), 1);
		err != nil || !enabled || regenerated != address {
		t.Fatalf("PWA reopen lost SP address: %q %q %v", address, regenerated, err)
	}
	balance, err := restored.LastSyncedSilentPay(wallet.GetId(), 1)
	if err != nil || !balance.Complete || balance.Balance != 123456 ||
		len(balance.Outputs) != 1 {
		t.Fatalf("PWA reopen lost verified SP outputs: %+v %v", balance, err)
	}
	if _, err := restored.LastSyncedSilentPay(wallet.GetId(), 0); err == nil {
		t.Fatal("disabled subaccount acquired an old balance")
	}
	// The same persisted DB cannot be misread under another Bitcoin network.
	_chain = "mainnet"
	if _, err := restored.LastSyncedSilentPay(wallet.GetId(), 1); err == nil {
		t.Fatal("testnet SP balance was visible as mainnet funds")
	}
	_chain = "testnet"
}

func TestSilentPayPWAInterruptedLiveRefreshDoesNotReplaceCommittedBalanceE2E(t *testing.T) {
	spPWAChain(t)
	manager, wallet := spE2EAccountManager(t)
	if _, err := manager.EnableSilentPay(wallet.GetId(), 0); err != nil {
		t.Fatal(err)
	}
	payment := spPWAMakePayment(t, wallet, 0, 53, 1, 15, 65432)
	reader := spPWANewIndexer(payment)
	manager.l1IndexerClient.SetMaster(reader)
	original, err := manager.SyncSilentPayLive(context.Background())
	if err != nil || original[0].Balance != 65432 {
		t.Fatalf("initial confirmed snapshot failed: %+v %v", original, err)
	}
	reader.pages = []SPIndexerPage{
		{SnapshotID: reader.snapshot.SnapshotID, Anchor: reader.snapshot.Anchor,
			Blocks: reader.pages[0].Blocks, Done: false, NextCursor: "second"},
		{SnapshotID: reader.snapshot.SnapshotID, Anchor: reader.snapshot.Anchor, Done: true},
	}
	reader.failCursor = "second"
	if _, err := manager.SyncSilentPayLive(context.Background()); err == nil {
		t.Fatal("PWA accepted a partial Live pagination as complete")
	}
	cached, err := manager.LastSyncedSilentPay(wallet.GetId(), 0)
	if err != nil || !cached.Complete || cached.Balance != 65432 ||
		cached.Anchor != original[0].Anchor {
		t.Fatalf("failed refresh replaced reliable balance: %+v %v", cached, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.SyncSilentPayLive(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled PWA scan did not stop: %v", err)
	}
	// A new confirmed snapshot with an empty Live directory means the UTXO
	// was spent; successful refresh must now publish a genuine zero balance.
	newAnchor := SPBaseAnchor{Height: 16, BlockHash: (chainhash.Hash{16}).String()}
	reader.status.BaseAnchor = newAnchor
	reader.snapshot.Anchor = newAnchor
	reader.pages = []SPIndexerPage{{
		SnapshotID: reader.snapshot.SnapshotID, Anchor: newAnchor, Done: true,
	}}
	reader.failCursor = ""
	refreshed, err := manager.SyncSilentPayLive(context.Background())
	if err != nil || len(refreshed) != 1 || !refreshed[0].Complete ||
		refreshed[0].Balance != 0 || len(refreshed[0].Outputs) != 0 {
		t.Fatalf("confirmed spend was not reflected after PWA resumed: %+v %v", refreshed, err)
	}
	zero, err := manager.LastSyncedSilentPay(wallet.GetId(), 0)
	if err != nil || zero.Anchor != newAnchor || zero.Balance != 0 {
		t.Fatalf("confirmed zero was not committed: %+v %v", zero, err)
	}
}

func TestSilentPayPWAUntrustedIndexerDoesNotEraseBalanceE2E(t *testing.T) {
	spPWAChain(t)
	manager, wallet := spE2EAccountManager(t)
	if _, err := manager.EnableSilentPay(wallet.GetId(), 0); err != nil {
		t.Fatal(err)
	}
	payment := spPWAMakePayment(t, wallet, 0, 63, 1, 15, 77777)
	reader := spPWANewIndexer(payment)
	manager.l1IndexerClient.SetMaster(reader)
	if _, err := manager.SyncSilentPayLive(context.Background()); err != nil {
		t.Fatal(err)
	}
	baselineStatus, baselineSnapshot := reader.status, reader.snapshot
	baselinePages := reader.pages
	cases := []struct {
		name string
		mutate func()
	}{
		{"wrong_network", func() { reader.status.Network = "mainnet" }},
		{"unsupported_schema", func() { reader.status.SchemaVersion = 2 }},
		{"not_ready", func() { reader.status.DataReady = false }},
		{"future_snapshot", func() { reader.snapshot.Anchor.Height++ }},
		{"snapshot_anchor_drift", func() { reader.pages[0].Anchor.BlockHash = (chainhash.Hash{71}).String() }},
		{"invalid_live_bitmap", func() {
			reader.pages[0].Blocks[0].Transactions[0].UnspentBitmap = nil
		}},
		{"wrong_transaction_identity", func() { reader.badLookup = true }},
		{"corrupted_raw_transaction", func() { reader.badRawTx = true }},
		{"repeated_cursor", func() {
			reader.pages = []SPIndexerPage{
				{SnapshotID: reader.snapshot.SnapshotID, Anchor: reader.snapshot.Anchor,
					Blocks: baselinePages[0].Blocks, Done: false, NextCursor: "second"},
				{SnapshotID: reader.snapshot.SnapshotID, Anchor: reader.snapshot.Anchor,
					Done: false, NextCursor: "second"},
			}
		}},
		{"request_aborted_mid_pagination", func() {
			reader.pages = []SPIndexerPage{
				{SnapshotID: reader.snapshot.SnapshotID, Anchor: reader.snapshot.Anchor,
					Blocks: baselinePages[0].Blocks, Done: false, NextCursor: "second"},
			}
			reader.failCursor = "second"
		}},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			reader.status = baselineStatus
			reader.snapshot = baselineSnapshot
			// Each case owns a fresh nested response so mutation cannot
			// pollute the next scenario.
			p := spPWANewIndexer(payment)
			reader.pages = p.pages
			reader.failCursor = ""
			reader.badRawTx = false
			reader.badLookup = false
			scenario.mutate()
			if _, err := manager.SyncSilentPayLive(context.Background()); err == nil {
				t.Fatal("invalid or incomplete public L1 data was accepted")
			}
			last, err := manager.LastSyncedSilentPay(wallet.GetId(), 0)
			if err != nil || !last.Complete || last.Balance != 77777 ||
				last.Anchor != baselineSnapshot.Anchor || len(last.Outputs) != 1 {
				t.Fatalf("rejected response destroyed last good SP checkpoint: %+v %v", last, err)
			}
		})
	}
	// Even a fully verified future result cannot be made visible if local
	// storage fails at the atomic batch commit.
	reader.status = baselineStatus
	reader.snapshot = baselineSnapshot
	reader.pages = []SPIndexerPage{{
		SnapshotID: reader.snapshot.SnapshotID, Anchor: reader.snapshot.Anchor, Done: true,
	}}
	reader.badRawTx = false
	reader.badLookup = false
	reader.failCursor = ""
	originalDB := manager.db
	manager.db = &spFailCommitDB{KVDB: originalDB}
	if _, err := manager.SyncSilentPayLive(context.Background()); err == nil {
		t.Fatal("failed local write was reported as a completed PWA recovery")
	}
	manager.db = originalDB
	last, err := manager.LastSyncedSilentPay(wallet.GetId(), 0)
	if err != nil || last.Balance != 77777 || len(last.Outputs) != 1 {
		t.Fatalf("local storage failure replaced last valid balance: %+v %v", last, err)
	}
}

func TestSilentPayPWAArchiveForkRecoveryDoesNotDuplicateReceiptsE2E(t *testing.T) {
	spPWAChain(t)
	manager, wallet := spE2EAccountManager(t)
	if _, err := manager.EnableSilentPay(wallet.GetId(), 0); err != nil {
		t.Fatal(err)
	}
	payment := spPWAMakePayment(t, wallet, 0, 73, 1, 10, 22000, 33000)
	first := SPBaseAnchor{Height: 11, BlockHash: (chainhash.Hash{11}).String()}
	reader := &spArchiveFakeIndexer{
		tx: payment.tx, anchor: first, blockHash: (chainhash.Hash{10}).String(),
		T: payment.item.Tweak,
	}
	manager.l1IndexerClient.SetMaster(reader)
	result, err := manager.SyncSilentPayArchive(context.Background())
	if err != nil || len(result) != 1 || len(result[0].Payments) != 2 ||
		!result[0].Complete {
		t.Fatalf("initial Archive failed: %+v %v", result, err)
	}
	// Simulate replacement of the last confirmed block. The old account
	// checkpoint must not be trusted on the new fork or appended twice.
	second := SPBaseAnchor{Height: 11, BlockHash: (chainhash.Hash{91}).String()}
	reader.anchor = second
	reader.failNext = true
	if _, err := manager.SyncSilentPayArchive(context.Background()); err == nil {
		t.Fatal("interrupted reorg replay committed partial SP history")
	}
	cached, err := manager.LastSyncedSilentPayHistory(wallet.GetId(), 0)
	if err != nil || cached.Anchor != first || len(cached.Payments) != 2 {
		t.Fatalf("interrupted reorg replay changed persisted records: %+v %v", cached, err)
	}
	reader.failNext = false
	recovered, err := manager.SyncSilentPayArchive(context.Background())
	if err != nil || len(recovered) != 1 || !recovered[0].Complete ||
		recovered[0].Anchor != second || len(recovered[0].Payments) != 2 {
		t.Fatalf("Archive reorg replay duplicated or missed receipts: %+v %v", recovered, err)
	}
	for i, payment := range recovered[0].Payments {
		if payment.K != uint32(i) || payment.Vout != uint32(i) {
			t.Fatalf("Archive changed payment order after reorg: %+v", recovered[0].Payments)
		}
	}
}
