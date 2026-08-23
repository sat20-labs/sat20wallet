package wallet

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type dkvsReplicaIntegrityFixture struct {
	owner  *Manager
	remote *rgb11MemoryDKVSHTTP
	client *SatsNetDKVSClient
	store  *dkvsReplicaStore
	path   string
	scope  string
	key    string
	record *swire.DKVSRecord
}

func newDKVSReplicaIntegrityFixture(t *testing.T, namespace, suffix string) *dkvsReplicaIntegrityFixture {
	t.Helper()
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	owner := newRGB11MultiDeviceManager(t, priv, 997)
	remote := newRGB11MemoryDKVSHTTP()
	configureRGB11DKVSTestManager(owner, remote)
	accountID := dkvsindexer.AccountID(owner.wallet.GetPubKey().SerializeCompressed())
	var key string
	switch namespace {
	case "personal":
		key = "/personal/" + accountID + "/account/" + suffix
	case "blob":
		key = "/blob/" + accountID + "/" + suffix
	default:
		t.Fatalf("unsupported fixture namespace %q", namespace)
	}
	record, err := NewDKVSSignedRecord(owner.wallet, key, []byte("canonical"),
		dkvsindexer.RecordOptions{Seq: 1, IssueHeight: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := attachDKVSAutopayFeeProof(owner.wallet, record, DKVSAutopayOptions{
		AddressParams: GetChainParam_SatsNet(), PoolContract: dkvsindexer.NetworkDefaultsForParams(
			GetChainParam_SatsNet()).AutopayContract,
	}); err != nil {
		t.Fatal(err)
	}
	path, err := dkvsindexer.CollectionPathForKey(key)
	if err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	remote.records[key] = cloneRGB11DKVSRecord(record)
	remote.generations[path] = 1
	remote.mu.Unlock()
	manager := owner.ensureDKVSManager()
	client, err := manager.primaryClient()
	if err != nil {
		t.Fatal(err)
	}
	store := newDKVSReplicaStore(owner.db)
	state, err := owner.syncDKVSDirectory(client, store, path)
	if err != nil {
		t.Fatal(err)
	}
	return &dkvsReplicaIntegrityFixture{owner: owner, remote: remote, client: client,
		store: store, path: path, scope: state.Scope, key: key, record: record}
}

func (f *dkvsReplicaIntegrityFixture) pathSyncCount() int {
	f.remote.mu.Lock()
	defer f.remote.mu.Unlock()
	return f.remote.pathSyncs[f.path]
}

func (f *dkvsReplicaIntegrityFixture) removeConfirmed(t *testing.T, key string) {
	t.Helper()
	if err := f.owner.db.Delete(dkvsReplicaRecordKey(dkvsReplicaConfirmedPrefix,
		f.scope, key)); err != nil {
		t.Fatal(err)
	}
}

func (f *dkvsReplicaIntegrityFixture) resync(t *testing.T) {
	t.Helper()
	if _, err := f.owner.syncDKVSDirectory(f.client, f.store, f.path); err != nil {
		t.Fatal(err)
	}
}

func TestDKVSDirectoryFastPathValidatesMaterializedReplica(t *testing.T) {
	t.Run("empty confirmed", func(t *testing.T) {
		fixture := newDKVSReplicaIntegrityFixture(t, "personal", "state")
		if fixture.pathSyncCount() != 1 {
			t.Fatalf("initial path syncs=%d", fixture.pathSyncCount())
		}
		fixture.removeConfirmed(t, fixture.key)
		fixture.resync(t)
		if fixture.pathSyncCount() != 2 {
			t.Fatalf("missing confirmed record used fast path: syncs=%d", fixture.pathSyncCount())
		}
		confirmed, err := fixture.store.loadConfirmed(fixture.scope)
		if err != nil || len(confirmed) != 1 || confirmed[0].Key != fixture.key {
			t.Fatalf("confirmed replica was not repaired: records=%v err=%v", confirmed, err)
		}
	})

	t.Run("same count different content", func(t *testing.T) {
		fixture := newDKVSReplicaIntegrityFixture(t, "personal", "state")
		fixture.removeConfirmed(t, fixture.key)
		otherKey := strings.TrimSuffix(fixture.key, "/state") + "/head"
		other, err := NewDKVSSignedRecord(fixture.owner.wallet, otherKey, []byte("different"),
			dkvsindexer.RecordOptions{Seq: 1, IssueHeight: 1})
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := dkvsindexer.MarshalRecord(other)
		if err != nil {
			t.Fatal(err)
		}
		if err := fixture.owner.db.Write(dkvsReplicaRecordKey(dkvsReplicaConfirmedPrefix,
			fixture.scope, otherKey), encoded); err != nil {
			t.Fatal(err)
		}
		fixture.resync(t)
		if fixture.pathSyncCount() != 2 {
			t.Fatalf("same-count divergent replica used fast path: syncs=%d", fixture.pathSyncCount())
		}
	})

	t.Run("blob missing", func(t *testing.T) {
		fixture := newDKVSReplicaIntegrityFixture(t, "blob", "account-managed-data")
		fixture.removeConfirmed(t, fixture.key)
		fixture.resync(t)
		if fixture.pathSyncCount() != 2 {
			t.Fatalf("missing blob used fast path: syncs=%d", fixture.pathSyncCount())
		}
	})

	t.Run("complete replica keeps fast path", func(t *testing.T) {
		fixture := newDKVSReplicaIntegrityFixture(t, "personal", "state")
		fixture.resync(t)
		if fixture.pathSyncCount() != 1 {
			t.Fatalf("healthy replica fetched another snapshot: syncs=%d", fixture.pathSyncCount())
		}
	})

	t.Run("free local overlay excluded from network root", func(t *testing.T) {
		fixture := newDKVSReplicaIntegrityFixture(t, "personal", "state")
		localKey := strings.TrimSuffix(fixture.key, "/state") + "/local"
		local, err := NewDKVSSignedRecord(fixture.owner.wallet, localKey, []byte("local"),
			dkvsindexer.RecordOptions{Seq: 1, IssueHeight: 1, TTL: 144})
		if err != nil {
			t.Fatal(err)
		}
		proof, err := dkvsindexer.NewFreeLocalFeeProof(local.Key, "personal",
			uint32(dkvsindexer.RecordSize(local)), dkvsindexer.RecordExpiryHeight(local))
		if err != nil {
			t.Fatal(err)
		}
		if err := AttachDKVSFeeProof(local, proof); err != nil {
			t.Fatal(err)
		}
		if err := SignDKVSRecord(fixture.owner.wallet, local); err != nil {
			t.Fatal(err)
		}
		fixture.remote.mu.Lock()
		fixture.remote.records[localKey] = cloneRGB11DKVSRecord(local)
		fixture.remote.mu.Unlock()
		fixture.resync(t)
		fixture.resync(t)
		if fixture.pathSyncCount() != 1 {
			t.Fatalf("FREE_LOCAL overlay changed network integrity: syncs=%d", fixture.pathSyncCount())
		}
		confirmed, err := fixture.store.loadConfirmed(fixture.scope)
		if err != nil || len(confirmed) != 2 {
			t.Fatalf("local overlay was not retained: records=%d err=%v", len(confirmed), err)
		}
	})
}

func TestWaitAccountManagedDataReadyRepairsReplicaAfterManagerRebuild(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	owner, _, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	remote := newRGB11MemoryDKVSHTTP()
	configureRGB11DKVSTestManager(owner, remote)
	root, err := owner.accountManagementRootWallet()
	if err != nil {
		t.Fatal(err)
	}
	stateKey, err := owner.accountManagedStateKey(root)
	if err != nil {
		t.Fatal(err)
	}
	dataKey, err := owner.accountManagedDataBlobKey(root)
	if err != nil {
		t.Fatal(err)
	}
	owner.mutex.Lock()
	owner.accountProfile.StorageMode = AccountStoragePaid
	owner.accountProfile.RecordTTL = 0
	owner.accountProfile.AutopayContract = dkvsindexer.NetworkDefaultsForParams(
		GetChainParam_SatsNet()).AutopayContract
	profile := *owner.accountProfile
	owner.mutex.Unlock()
	dataValue, err := EncodeDKVSBlobValue(profile.ManagedDataEnvelope, nil)
	if err != nil {
		t.Fatal(err)
	}
	makeRecord := func(key string, value []byte) *swire.DKVSRecord {
		record, recordErr := NewDKVSSignedRecord(root, key, value,
			dkvsindexer.RecordOptions{Seq: profile.StateSeq, IssueHeight: 1})
		if recordErr != nil {
			t.Fatal(recordErr)
		}
		if recordErr = attachDKVSAutopayFeeProof(root, record, DKVSAutopayOptions{
			AddressParams: GetChainParam_SatsNet(), PoolContract: profile.AutopayContract,
		}); recordErr != nil {
			t.Fatal(recordErr)
		}
		return record
	}
	stateRecord := makeRecord(stateKey, profile.StateEnvelope)
	dataRecord := makeRecord(dataKey, dataValue)
	statePath, _ := dkvsindexer.CollectionPathForKey(stateKey)
	dataPath, _ := dkvsindexer.CollectionPathForKey(dataKey)
	remote.mu.Lock()
	remote.records[stateKey] = cloneRGB11DKVSRecord(stateRecord)
	remote.records[dataKey] = cloneRGB11DKVSRecord(dataRecord)
	remote.generations[statePath] = 1
	remote.generations[dataPath] = 1
	remote.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	if err := owner.WaitAccountManagedDataReady(ctx); err != nil {
		cancel()
		t.Fatalf("initial account-managed sync: %v", err)
	}
	cancel()
	namespace := owner.dkvsReplicaNamespace()
	for _, item := range []struct{ key, path string }{{stateKey, statePath}, {dataKey, dataPath}} {
		scope := dkvsReplicaScope(namespace, []dkvsindexer.Subscription{{
			Type: dkvsindexer.SubscriptionPrefix, Target: item.path,
		}})
		if err := owner.db.Delete(dkvsReplicaRecordKey(dkvsReplicaConfirmedPrefix,
			scope, item.key)); err != nil {
			t.Fatal(err)
		}
	}

	// Recreate the manager as an HMR/full-page restart would: readiness is empty,
	// while the persisted baselines and incomplete confirmed replica remain.
	owner.dkvs = newDKVSManager(owner)
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := owner.WaitAccountManagedDataReady(ctx); err != nil {
		t.Fatalf("account-managed wait did not self-heal the replica: %v", err)
	}
	remote.mu.Lock()
	stateSyncs, dataSyncs := remote.pathSyncs[statePath], remote.pathSyncs[dataPath]
	remote.mu.Unlock()
	if stateSyncs != 2 || dataSyncs != 2 {
		t.Fatalf("manager rebuild path syncs state=%d data=%d, want 2/2", stateSyncs, dataSyncs)
	}
}

func TestDKVSManagerStopWaitsForWorker(t *testing.T) {
	manager := &Manager{}
	dkvs := newDKVSManager(manager)
	stop := make(chan struct{})
	done := make(chan struct{})
	dkvs.stop = stop
	dkvs.done = done

	stopped := make(chan struct{})
	go func() {
		dkvs.stopAndWait()
		close(stopped)
	}()

	select {
	case <-stop:
	case <-time.After(time.Second):
		t.Fatal("background sync worker was not stopped")
	}
	select {
	case <-stopped:
		t.Fatal("stop returned before the worker exited")
	case <-time.After(20 * time.Millisecond):
	}

	close(done)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop did not return after the worker exited")
	}
	if dkvs.stop != nil || dkvs.done != nil {
		t.Fatal("background sync state was not cleared")
	}
}

func TestDKVSManagerWakeKeepsWorkerState(t *testing.T) {
	manager := newDKVSManager(&Manager{})
	stop := make(chan struct{})
	done := make(chan struct{})
	wake := make(chan struct{}, 1)
	manager.stop = stop
	manager.done = done
	manager.wake = wake

	manager.wakeSync()

	if manager.stop != stop || manager.done != done || manager.wake != wake {
		t.Fatal("waking DKVS synchronization replaced the active worker")
	}
	select {
	case <-wake:
	default:
		t.Fatal("waking DKVS synchronization did not notify the active worker")
	}
}

func TestDKVSManagerWakeIgnoredWhileStopping(t *testing.T) {
	manager := newDKVSManager(&Manager{})
	manager.wake = make(chan struct{}, 1)
	manager.stopping = true

	manager.wakeSync()

	select {
	case <-manager.wake:
		t.Fatal("stopping DKVS manager must not accept new synchronization work")
	default:
	}
}

func TestDKVSManagerStopCancelsInFlightHTTP(t *testing.T) {
	requestStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		<-r.Context().Done()
	}))
	defer server.Close()

	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	manager := newDKVSManager(&Manager{})
	requestCtx, cancelRequests := context.WithCancel(context.Background())
	manager.stop = make(chan struct{})
	manager.done = make(chan struct{})
	manager.requestCtx = requestCtx
	manager.cancelRequests = cancelRequests

	client := NewSatsNetDKVSClient(endpoint.Scheme, endpoint.Host, "testnet",
		&NetClient{Client: server.Client()})
	client.manager = manager
	requestDone := make(chan error, 1)
	go func() {
		_, _, requestErr := client.ListRecords("/tmp", 0, 1)
		requestDone <- requestErr
		close(manager.done)
	}()

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("DKVS request did not start")
	}

	startedAt := time.Now()
	manager.stopAndWait()
	if elapsed := time.Since(startedAt); elapsed > 500*time.Millisecond {
		t.Fatalf("stopping DKVS manager took %s; in-flight HTTP was not cancelled", elapsed)
	}
	requestErr := <-requestDone
	if !errors.Is(requestErr, context.Canceled) {
		t.Fatalf("request error = %v, want context cancellation", requestErr)
	}
}

func TestDKVSManagerReusesEndpointSession(t *testing.T) {
	manager := newDKVSManager(&Manager{})
	first, err := manager.clientFor("http", "dkvs.test", "testnet", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.clientFor("http", "dkvs.test", "testnet", nil)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("DKVS endpoint session was not reused")
	}
}

func TestDKVSManagerManagesCollectionAndExactKeys(t *testing.T) {
	accountID := strings.Repeat("0", 64)
	manager := newDKVSManager(&Manager{})
	managed := []string{
		"/personal/" + accountID + "/rgb/head",
		"/svc/autopay/config",
		"/mail/" + accountID + "/msg/" + accountID + "/message",
		"/blob/" + accountID + "/snapshot",
	}
	for _, key := range managed {
		if !manager.managesKey(key) {
			t.Fatalf("expected collection key to be managed: %s", key)
		}
	}
	exact := []string{
		"/account/testnet/tb1qexample",
		"/name/example",
		"/tmp/example",
		"/sys/params",
	}
	for _, key := range exact {
		if !manager.managesKey(key) {
			t.Fatalf("expected exact key to be managed: %s", key)
		}
	}
	if manager.managesKey("/invalid/key") {
		t.Fatal("invalid key must not be treated as a managed collection key")
	}
}

func TestDKVSPendingJobRefreshRunsOutsideLifecycleLock(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	owner := newRGB11MultiDeviceManager(t, priv, 994)
	remote := newRGB11MemoryDKVSHTTP()
	configureRGB11DKVSTestManager(owner, remote)
	manager := owner.ensureDKVSManager()
	const key = "/tmp/pending-job-refresh"
	record, err := NewDKVSSignedRecord(owner.wallet, key, []byte("value"),
		dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000})
	if err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	remote.records[key] = cloneRGB11DKVSRecord(record)
	remote.mu.Unlock()
	manager.rememberPaths([]string{key})

	jobDone := make(chan error, 1)
	manager.schedule("refresh", func(store *dkvsStore) error {
		err := store.Refresh(key)
		jobDone <- err
		return err
	})
	syncDone := make(chan error, 1)
	go func() {
		_, syncErr := owner.syncDKVSOnce()
		syncDone <- syncErr
	}()
	select {
	case err := <-syncDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("pending DKVS job deadlocked while refreshing")
	}
	select {
	case err := <-jobDone:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("pending refresh job did not execute")
	}
}

func TestDKVSPendingJobsRunWithoutManagedStates(t *testing.T) {
	owner := &Manager{db: newMemoryKVDB()}
	configureRGB11DKVSTestManager(owner, newRGB11MemoryDKVSHTTP())
	manager := owner.ensureDKVSManager()
	ran := false
	manager.schedule("empty", func(*dkvsStore) error {
		ran = true
		return nil
	})
	states, err := owner.syncDKVSOnce()
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 0 || !ran {
		t.Fatalf("empty-state pending job: states=%d ran=%v", len(states), ran)
	}
}

func TestDKVSPendingJobTransientFailureIsRetried(t *testing.T) {
	owner := &Manager{db: newMemoryKVDB()}
	configureRGB11DKVSTestManager(owner, newRGB11MemoryDKVSHTTP())
	manager := owner.ensureDKVSManager()
	attempts := 0
	manager.schedule("retry", func(*dkvsStore) error {
		attempts++
		if attempts == 1 {
			return context.DeadlineExceeded
		}
		return nil
	})
	if _, err := owner.syncDKVSOnce(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first pending job error=%v", err)
	}
	if _, err := owner.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("pending job attempts=%d, want 2", attempts)
	}
}

func TestDKVSPendingJobFailureDoesNotStarveObservers(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	owner := newRGB11MultiDeviceManager(t, priv, 993)
	remote := newRGB11MemoryDKVSHTTP()
	configureRGB11DKVSTestManager(owner, remote)
	manager := owner.ensureDKVSManager()
	const key = "/tmp/pending-job-observer"
	record, err := NewDKVSSignedRecord(owner.wallet, key, []byte("value"),
		dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000})
	if err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	remote.records[key] = cloneRGB11DKVSRecord(record)
	remote.mu.Unlock()
	manager.rememberPaths([]string{key})

	observed := false
	manager.addObserver(func(paths []string) {
		for _, path := range paths {
			if path == key {
				observed = true
			}
		}
	})
	manager.schedule("failure", func(*dkvsStore) error {
		return context.DeadlineExceeded
	})
	if _, err := owner.syncDKVSOnce(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pending job error=%v", err)
	}
	if !observed {
		t.Fatal("pending job failure starved DKVS observers")
	}
}

func TestDKVSPendingJobFailurePreservesUnexecutedJobs(t *testing.T) {
	owner := &Manager{db: newMemoryKVDB()}
	configureRGB11DKVSTestManager(owner, newRGB11MemoryDKVSHTTP())
	manager := owner.ensureDKVSManager()
	failures := 0
	nextRuns := 0
	manager.schedule("a-failure", func(*dkvsStore) error {
		failures++
		if failures == 1 {
			return context.DeadlineExceeded
		}
		return nil
	})
	manager.schedule("b-next", func(*dkvsStore) error {
		nextRuns++
		return nil
	})
	if _, err := owner.syncDKVSOnce(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first pending job error=%v", err)
	}
	if nextRuns != 0 {
		t.Fatalf("later pending job ran before retry: %d", nextRuns)
	}
	if _, err := owner.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
	if failures != 2 || nextRuns != 1 {
		t.Fatalf("pending jobs after retry: failures=%d next=%d", failures, nextRuns)
	}
}

func TestDKVSPendingJobSameIDRunsLatestTask(t *testing.T) {
	owner := &Manager{db: newMemoryKVDB()}
	configureRGB11DKVSTestManager(owner, newRGB11MemoryDKVSHTTP())
	manager := owner.ensureDKVSManager()
	result := ""
	manager.schedule("same", func(*dkvsStore) error {
		result = "old"
		return nil
	})
	manager.schedule("same", func(*dkvsStore) error {
		result = "latest"
		return nil
	})
	if _, err := owner.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
	if result != "latest" {
		t.Fatalf("same-ID pending job result=%q", result)
	}
}
