package wallet

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
)

type blockingContextHTTP struct {
	started chan struct{}
	once    sync.Once
}

func (h *blockingContextHTTP) SendGetRequest(*URL) ([]byte, error) {
	return nil, errors.New("unexpected non-context GET")
}

func (h *blockingContextHTTP) SendPostRequest(*URL, []byte) ([]byte, error) {
	return nil, errors.New("unexpected non-context POST")
}

func (h *blockingContextHTTP) SendGetRequestContext(ctx context.Context, _ *URL) ([]byte, error) {
	h.once.Do(func() { close(h.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func (h *blockingContextHTTP) SendPostRequestContext(ctx context.Context, _ *URL, _ []byte) ([]byte, error) {
	h.once.Do(func() { close(h.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestChannelFundingIndexerLagClassification(t *testing.T) {
	if !isChannelFundingTxNotIndexedYetError(errors.New("transaction may not be indexed yet")) {
		t.Fatal("expected temporary not-indexed error to be classified as deferrable")
	}
	if !isChannelFundingTxNotIndexedYetError(errors.New("transaction has not be indexed yet")) {
		t.Fatal("expected legacy not-indexed error to be classified as deferrable")
	}
	if isChannelFundingTxNotIndexedYetError(errors.New("transaction not found")) {
		t.Fatal("permanent not-found error must not be classified as indexer lag")
	}
}

func safetyTestManager(t testing.TB, channel *Channel) *Manager {
	t.Helper()
	wallet := NewInternalWalletWithMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"", &chaincfg.TestNet4Params,
	)
	if wallet == nil {
		t.Fatal("create safety test wallet")
	}
	return &Manager{
		wallet:     wallet,
		channelMap: map[string]*Channel{channel.ChannelId: channel},
		nodeMap:    make(map[string]string),
	}
}

func safetyTestCommitment() *ChannelCommitment {
	commitment := NewChannelCommitment()
	commitment.CommitTx = wire.NewMsgTx(2)
	return commitment
}

func safetyTestTx(seed byte) *wire.MsgTx {
	tx := wire.NewMsgTx(2)
	var hash chainhash.Hash
	hash[0] = seed
	tx.AddTxIn(wire.NewTxIn(wire.NewOutPoint(&hash, uint32(seed)), nil, nil))
	tx.AddTxOut(wire.NewTxOut(1, []byte{0x51}))
	return tx
}

func TestCommitmentExportReportsMissingRemoteCommitment(t *testing.T) {
	channel := &Channel{ChannelInDB: *NewChannelInDB()}
	channel.ChannelId = "channel-export"
	channel.Address = "channel-address"
	channel.Status = CS_READY
	channel.LocalCommitment = safetyTestCommitment()
	mgr := safetyTestManager(t, channel)

	exported, err := mgr.CommitmentExport(channel.ChannelId)
	if err != nil {
		t.Fatalf("CommitmentExport failed: %v", err)
	}
	if !exported.LocalCommitmentPresent || exported.RemoteCommitmentPresent {
		t.Fatalf("unexpected commitment presence: local=%v remote=%v",
			exported.LocalCommitmentPresent, exported.RemoteCommitmentPresent)
	}
	if !containsSafetyEvidence(exported.MissingEvidence, "MISSING_REMOTE_COMMITMENT") ||
		!containsSafetyEvidence(exported.MissingEvidence, "MISSING_CHANNEL_POINT") {
		t.Fatalf("unexpected missing evidence: %v", exported.MissingEvidence)
	}
}

func TestSafetySnapshotFailsClosedWithoutWatchtower(t *testing.T) {
	channel := &Channel{ChannelInDB: *NewChannelInDB()}
	channel.ChannelId = "channel-safety"
	channel.Status = CS_READY
	channel.LocalCommitment = safetyTestCommitment()
	channel.RemoteCommitment = safetyTestCommitment()
	mgr := safetyTestManager(t, channel)

	snapshot, err := mgr.SafetySnapshot(channel.ChannelId)
	if err != nil {
		t.Fatalf("SafetySnapshot failed: %v", err)
	}
	if snapshot.Status != "READY_DEGRADED" {
		t.Fatalf("status=%s, want READY_DEGRADED", snapshot.Status)
	}
	if snapshot.PunishCoverage == nil || snapshot.PunishCoverage.Status != "PUNISH_COVERAGE_UNKNOWN" {
		t.Fatalf("unexpected punish coverage: %+v", snapshot.PunishCoverage)
	}
	if !containsSafetyEvidence(snapshot.MissingEvidence, "MISSING_PUNISH_COVERAGE") ||
		!containsSafetyEvidence(snapshot.MissingEvidence, "MISSING_CHANNEL_POINT") {
		t.Fatalf("unexpected missing evidence: %v", snapshot.MissingEvidence)
	}
}

func TestBuildSweepRejectsNonInitiatorWallet(t *testing.T) {
	channel := &Channel{ChannelInDB: *NewChannelInDB()}
	channel.ChannelId = "channel-server"
	channel.LocalCommitment = safetyTestCommitment()
	mgr := safetyTestManager(t, channel)

	_, err := mgr.BuildSweepTx(channel.ChannelId, 100)
	if err == nil || !strings.Contains(err.Error(), "only supported for the channel initiator") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCommitmentExportRequiresChannelPoint(t *testing.T) {
	channel := &Channel{ChannelInDB: *NewChannelInDB()}
	channel.ChannelId = "channel-client"
	channel.IsInitiator = true
	channel.LocalCommitment = safetyTestCommitment()
	channel.RemoteCommitment = safetyTestCommitment()
	mgr := safetyTestManager(t, channel)

	exported, err := mgr.CommitmentExport(channel.ChannelId)
	if err != nil {
		t.Fatalf("CommitmentExport failed: %v", err)
	}
	if !containsSafetyEvidence(exported.MissingEvidence, "MISSING_CHANNEL_POINT") {
		t.Fatalf("missing channel point was not reported: %v", exported.MissingEvidence)
	}
}

func TestGetCommitTxAssetInfoIncludesOutputIdentityAndScript(t *testing.T) {
	channel := &Channel{ChannelInDB: *NewChannelInDB()}
	channel.ChannelId = "channel-commit-output"
	fundingTx := safetyTestTx(21)
	fundingHash := fundingTx.TxHash()
	fundingOutPoint := wire.NewOutPoint(&fundingHash, 0)
	channel.ChanPoint = indexer.NewTxOutput(1_000)
	channel.ChanPoint.OutPointStr = fundingOutPoint.String()
	channel.ChanPoint.OutValue.PkScript = []byte{0x51}

	commitTx := wire.NewMsgTx(2)
	commitTx.AddTxIn(wire.NewTxIn(fundingOutPoint, nil, nil))
	commitTx.AddTxOut(wire.NewTxOut(400, []byte{0x51, 0x01}))
	commitTx.AddTxOut(wire.NewTxOut(600, []byte{0x51, 0x02}))
	channel.LocalCommitment = NewChannelCommitment()
	channel.LocalCommitment.CommitTx = commitTx
	mgr := safetyTestManager(t, channel)

	info, err := mgr.GetCommitTxAssetInfo(channel.ChannelId)
	if err != nil {
		t.Fatalf("GetCommitTxAssetInfo failed: %v", err)
	}
	if len(info.OutputAssets) != len(commitTx.TxOut) {
		t.Fatalf("output count=%d, want %d", len(info.OutputAssets), len(commitTx.TxOut))
	}
	for i, output := range info.OutputAssets {
		wantOutpoint := commitTx.TxID() + ":" + strconv.Itoa(i)
		if output.OutPoint != wantOutpoint {
			t.Fatalf("output %d outpoint=%q, want %q", i, output.OutPoint, wantOutpoint)
		}
		if !bytes.Equal(output.PkScript, commitTx.TxOut[i].PkScript) {
			t.Fatalf("output %d script=%x, want %x", i, output.PkScript, commitTx.TxOut[i].PkScript)
		}
	}
}

func TestPersistTerminalTransitionRejectsStatusGenerationChange(t *testing.T) {
	database := NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB returned nil")
	}
	defer database.Close()

	channel := &Channel{ChannelInDB: *NewChannelInDB()}
	channel.ChannelId = "channel-status-generation"
	channel.Status = CS_READY
	channel.LocalCommitment = safetyTestCommitment()
	channel.RemoteCommitment = safetyTestCommitment()
	generation, err := captureChannelSafetyGeneration(channel)
	if err != nil {
		t.Fatalf("captureChannelSafetyGeneration failed: %v", err)
	}
	channel.Status = CS_CLOSING_STARTED
	mgr := safetyTestManager(t, channel)
	mgr.db = database

	if err := mgr.persistTerminalTransition(channel, generation, CS_CLOSED_UNEXPECTED); err == nil {
		t.Fatal("terminal transition accepted a changed channel status")
	}
	if channel.Status != CS_CLOSING_STARTED {
		t.Fatalf("channel status=%d, want %d", channel.Status, CS_CLOSING_STARTED)
	}
}

func TestWatchTowerStopCancelsInFlightRetryAndCanRestart(t *testing.T) {
	database := NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB returned nil")
	}
	defer database.Close()

	commitTx := safetyTestTx(31)
	channel := &Channel{ChannelInDB: *NewChannelInDB()}
	channel.ChannelId = "channel-retry-stop"
	channel.Status = CS_CLOSED_UNEXPECTED
	channel.LocalCommitment = safetyTestCommitment()
	channel.RemoteCommitment = safetyTestCommitment()
	http := &blockingContextHTTP{started: make(chan struct{})}
	client := NewIndexerClient("http", "watchtower.test", "", http)
	rpc := NewIndexerRPCClientMgr()
	rpc.SetMaster(client)
	mgr := safetyTestManager(t, channel)
	mgr.db = database
	mgr.l1IndexerClient = rpc
	mgr.utxoLockerL1 = NewUtxoLocker(database, client, L1_NETWORK_BITCOIN)
	tower := newWatchTower(mgr, false)
	mgr.watchTower = tower
	if err := tower.AddCommitTx(channel, commitTx, []*wire.MsgTx{safetyTestTx(32)}); err != nil {
		t.Fatalf("AddCommitTx failed: %v", err)
	}
	if err := tower.markPunishPending(commitTx.TxID()); err != nil {
		t.Fatalf("markPunishPending failed: %v", err)
	}
	tower.retryInterval = time.Millisecond
	tower.Start()
	select {
	case <-http.started:
	case <-time.After(time.Second):
		t.Fatal("retry did not enter the context-aware request")
	}
	stopped := make(chan struct{})
	go func() {
		tower.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel the in-flight retry")
	}

	// A fresh lifecycle must schedule the still-pending record again.
	http.started = make(chan struct{})
	http.once = sync.Once{}
	tower.Start()
	select {
	case <-http.started:
	case <-time.After(time.Second):
		t.Fatal("Start after Stop did not restore the pending retry worker")
	}
	tower.Stop()
}

func TestPunishRetryCallbackCanStopWatchTower(t *testing.T) {
	mgr := &Manager{}
	ctx, cancel := context.WithCancel(context.Background())
	tower := &WatchTower{
		manager:         mgr,
		retryContext:    ctx,
		retryCancel:     cancel,
		retryRunning:    true,
		retryGeneration: 1,
		retryWorkers:    map[string]uint64{"commit": 1},
	}
	mgr.watchTower = tower
	tower.retryWG.Add(1)

	callbackDone := make(chan struct{})
	callbackCount := 0
	mgr.RegisterCallback(func(event string, _ interface{}) {
		if event != MSG_CHANNEL_PUNISHED {
			return
		}
		callbackCount++
		tower.Stop()
		close(callbackDone)
	})
	go tower.notifyPunishRetryCompleted("commit", 1, "punish")

	select {
	case <-callbackDone:
	case <-time.After(time.Second):
		t.Fatal("punish callback deadlocked while stopping watchtower")
	}
	if callbackCount != 1 {
		t.Fatalf("callback count=%d, want 1", callbackCount)
	}
}

func TestWatchTowerCleanupValidatesLegacyKeysBeforeDeleting(t *testing.T) {
	database := NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB returned nil")
	}
	defer database.Close()

	channelID := "cleanupchannel"
	commitTxID := safetyTestTx(41).TxID()
	badKey := []byte(GetDBKeyPrefix() + DB_KEY_WT_PUNISHTX + channelID + "-bad-extra")
	if err := database.Write(badKey, []byte("bad")); err != nil {
		t.Fatalf("write malformed legacy key failed: %v", err)
	}
	if err := saveBroadcastedCommitTx(database, commitTxID); err != nil {
		t.Fatalf("save pending failed: %v", err)
	}
	if _, _, err := deleteAllWatchtowerDataWithPending(database, channelID, commitTxID); err == nil {
		t.Fatal("cleanup accepted a malformed legacy key")
	}
	if _, err := database.Read(badKey); err != nil {
		t.Fatalf("malformed evidence was deleted before validation: %v", err)
	}
	if _, err := database.Read([]byte(GetBroadcastedCommitKey(commitTxID))); err != nil {
		t.Fatalf("pending flag was deleted before validation: %v", err)
	}
}

func TestPunishStatusFiltersCurrentRemoteWithoutDeletingEvidence(t *testing.T) {
	database := NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB returned nil")
	}
	defer database.Close()

	currentRemote := safetyTestTx(1)
	oldRemote := safetyTestTx(2)
	channel := &Channel{ChannelInDB: *NewChannelInDB()}
	channel.ChannelId = "channel-punish-status"
	channel.RemoteCommitment = NewChannelCommitment()
	channel.RemoteCommitment.CommitTx = currentRemote
	mgr := safetyTestManager(t, channel)
	mgr.db = database
	mgr.watchTower = newWatchTower(mgr, false)
	if err := mgr.watchTower.AddCommitTx(channel, currentRemote, []*wire.MsgTx{safetyTestTx(3)}); err != nil {
		t.Fatalf("AddCommitTx current failed: %v", err)
	}
	if err := mgr.watchTower.AddCommitTx(channel, oldRemote, []*wire.MsgTx{safetyTestTx(4)}); err != nil {
		t.Fatalf("AddCommitTx old failed: %v", err)
	}

	items, err := mgr.PunishStatus(channel.ChannelId)
	if err != nil {
		t.Fatalf("PunishStatus failed: %v", err)
	}
	if len(items) != 1 || items[0].CommitTxId != oldRemote.TxID() {
		t.Fatalf("unexpected punish status: %+v", items)
	}
	if !mgr.watchTower.HasCommitTx(currentRemote.TxID()) {
		t.Fatal("read-only PunishStatus deleted current remote evidence")
	}
}

func TestWatchTowerLoadsLegacyDatabaseRecordsAfterRestart(t *testing.T) {
	dbPath := t.TempDir()
	database := NewKVDB(dbPath)
	if database == nil {
		t.Fatal("NewKVDB returned nil")
	}

	channelID := "legacychannel"
	commitTx := safetyTestTx(11)
	punishTx := safetyTestTx(12)
	punishHex, err := EncodeMsgTx(punishTx)
	if err != nil {
		t.Fatalf("EncodeMsgTx failed: %v", err)
	}
	// These are the exact key/value formats written by the online watchtower:
	// plain transaction hex, gob-encoded map, and gob-encoded integer flag.
	if err := database.Write([]byte(GetPunishTxKey(channelID, commitTx.TxID())), []byte(punishHex)); err != nil {
		t.Fatalf("write legacy punish record failed: %v", err)
	}
	utxo := commitTx.TxIn[0].PreviousOutPoint.String()
	utxoValue, err := EncodeToBytes(map[string]bool{commitTx.TxID(): true})
	if err != nil {
		t.Fatalf("encode legacy utxo record failed: %v", err)
	}
	if err := database.Write([]byte(GetUtxoToCommitTxIdMapKey(channelID, utxo)), utxoValue); err != nil {
		t.Fatalf("write legacy utxo record failed: %v", err)
	}
	pendingValue, err := EncodeToBytes(1)
	if err != nil {
		t.Fatalf("encode legacy pending record failed: %v", err)
	}
	if err := database.Write([]byte(GetBroadcastedCommitKey(commitTx.TxID())), pendingValue); err != nil {
		t.Fatalf("write legacy pending record failed: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close legacy database failed: %v", err)
	}

	database = NewKVDB(dbPath)
	if database == nil {
		t.Fatal("reopen NewKVDB returned nil")
	}
	defer database.Close()
	tower := newWatchTower(&Manager{db: database}, false)
	if !tower.HasCommitTx(commitTx.TxID()) || !tower.HasBroadcastedCommitTx(commitTx.TxID()) {
		t.Fatalf("legacy records were not restored: commit=%v pending=%v",
			tower.HasCommitTx(commitTx.TxID()), tower.HasBroadcastedCommitTx(commitTx.TxID()))
	}
	loadedChannelID, loadedPunishTxs, err := tower.GetPunishTx(commitTx.TxID())
	if err != nil {
		t.Fatalf("GetPunishTx legacy record failed: %v", err)
	}
	if loadedChannelID != channelID || len(loadedPunishTxs) != 1 || loadedPunishTxs[0].TxID() != punishTx.TxID() {
		t.Fatalf("unexpected legacy punish record: channel=%s txs=%d", loadedChannelID, len(loadedPunishTxs))
	}
	if commits := tower.utxoMap[utxo]; !commits[commitTx.TxID()] {
		t.Fatalf("legacy utxo mapping was not restored: %+v", commits)
	}
}

func containsSafetyEvidence(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
