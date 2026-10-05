package wallet

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDKVSReviewKnownEndpointSwitchCannotReadPreviousReplica(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	client, err := manager.GetDKVSClient()
	require.NoError(t, err)
	key := accountTestKey(t, manager, "sixth-source/value")
	prefix, err := dkvs.CollectionPathForKey(key)
	require.NoError(t, err)
	_, err = finalDKVSSeedClient(manager, remote).PutRecord(freeLocalRecord(t, manager, key, 1, "endpoint-a-only"))
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
	before, err := manager.GetDKVSSubscriptionStatus()
	require.NoError(t, err)

	// Use the public configured-client entry after selecting another URL.
	// Its observed identity also differs; no source migration was approved.
	manager.cfg.IndexerL2.Host = "replacement.dkvs.test"
	remote.mu.Lock()
	remote.endpointID = "replacement-endpoint"
	remote.mu.Unlock()
	replacement, err := manager.GetDKVSClient()
	require.NoError(t, err)
	require.NotSame(t, client, replacement)
	config, err := replacement.GetDKVSClientConfig()
	require.NoError(t, err)
	require.NotEqual(t, before.EndpointID, config.EndpointID)
	_, _, syncErr := manager.syncDKVSOnceResult()
	require.ErrorIs(t, syncErr, dkvs.ErrLocalOnlyEndpointMismatch)

	read, readErr := replacement.GetRecord(key)
	t.Logf("old_endpoint=%s selected_endpoint=%s read=%v error=%v", before.EndpointID, config.EndpointID, read, readErr)
	assert.Error(t, readErr, "a client with a known different identity cannot serve the old endpoint's FREE_LOCAL data as its confirmed state")
	assert.Nil(t, read)
	listed, total, listErr := replacement.ListRecords(prefix, 0, 0)
	assert.Error(t, listErr, "directory reads need the same source boundary as synchronization")
	assert.Empty(t, listed)
	assert.Zero(t, total)
	store := &dkvsStore{manager: manager.dkvs, client: replacement}
	assert.False(t, store.IsReady(key))
	assert.ErrorIs(t, store.WaitReady(key), dkvs.ErrEndpointMismatch)
	builderCalled := false
	_, err = store.Update([]string{key}, func(map[string]*dkvsValue, map[string]uint64) ([]dkvsValueMutation, error) {
		builderCalled = true
		return nil, nil
	})
	assert.ErrorIs(t, err, dkvs.ErrEndpointMismatch)
	assert.False(t, builderCalled, "a write builder must not receive another source's local state")
	after, err := manager.GetDKVSSubscriptionStatus()
	require.NoError(t, err)
	assert.Equal(t, before, after, "rejecting a different source must preserve the original replica")
	releaseDKVSManagerRuntime(manager.dkvs)
	manager.dkvs = nil
	replacement, err = manager.GetDKVSClient()
	require.NoError(t, err)
	_, err = replacement.GetDKVSClientConfig()
	require.NoError(t, err)
	assert.False(t, (&dkvsStore{manager: manager.dkvs, client: replacement}).IsReady(key))
	_, heightKnown := manager.dkvs.verificationHeight()
	assert.False(t, heightKnown, "a different source cannot restore the old replica's verification authority")
}

func TestDKVSReviewConfirmedReplicaReadBoundaries(t *testing.T) {
	for _, scenario := range []string{"same-source-url-alias", "unknown-source-offline", "confirmed-zero-height", "known-height-wins"} {
		t.Run(scenario, func(t *testing.T) {
			manager, remote := finalDKVSTestManager(t)
			http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
			configureRGB11DKVSTestManager(manager, http)
			client, err := manager.GetDKVSClient()
			require.NoError(t, err)
			key := accountTestKey(t, manager, "sixth-boundaries/value")
			prefix, err := dkvs.CollectionPathForKey(key)
			require.NoError(t, err)
			record := freeLocalRecord(t, manager, key, 1, "confirmed")
			if scenario == "confirmed-zero-height" {
				remote.mu.Lock()
				remote.bestHeight = 0
				remote.mu.Unlock()
				record, err = newDKVSAccountSignedRecordWithFreeLocal(manager.wallet, key, record.Value,
					dkvs.RecordOptions{Seq: 1, TTL: testRGB11FreeLocalTTL})
				require.NoError(t, err)
			}
			_, err = finalDKVSSeedClient(manager, remote).PutRecord(record)
			require.NoError(t, err)
			require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
			if scenario == "confirmed-zero-height" {
				// The general fixture starts views at height one. Supply an
				// actual height-zero page through its existing transport hook.
				http.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
					if path != "/v3/dkvs/active/sync" {
						return remote.SendDKVSPostContext(ctx, path, body)
					}
					var request dkvs.ActiveSyncRequest
					if err := json.Unmarshal(body, &request); err != nil {
						return nil, err
					}
					page, err := remote.activePage(request)
					if err != nil {
						return nil, err
					}
					page.Meta.ViewHeight = 0
					return testActiveResponse(page, nil)
				}
			}
			require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
			before, err := manager.GetDKVSSubscriptionStatus()
			require.NoError(t, err)
			if scenario == "confirmed-zero-height" {
				require.Zero(t, before.ViewHeight)
			}
			switch scenario {
			case "same-source-url-alias":
				manager.cfg.IndexerL2.Host = "same-source-alias.dkvs.test"
				alias, err := manager.GetDKVSClient()
				require.NoError(t, err)
				require.NotSame(t, client, alias)
				client = alias
				config, err := client.GetDKVSClientConfig()
				require.NoError(t, err)
				require.Equal(t, before.EndpointID, config.EndpointID)
			case "unknown-source-offline", "confirmed-zero-height":
				releaseDKVSManagerRuntime(manager.dkvs)
				manager.dkvs = nil
				client, err = manager.GetDKVSClient()
				require.NoError(t, err)
				require.Empty(t, client.endpointID)
			case "known-height-wins":
				manager.dkvs.setEndpointVerificationHeight(dkvs.RecordExpiryHeight(record), true)
			}
			requests := 0
			http.get = func(context.Context, string, map[string]string) ([]byte, error) {
				requests++
				return nil, &net.DNSError{Err: "offline", IsTimeout: true}
			}
			http.post = func(context.Context, string, []byte) ([]byte, error) {
				requests++
				return nil, &net.DNSError{Err: "offline", IsTimeout: true}
			}
			store := &dkvsStore{manager: manager.dkvs, client: client}
			assert.True(t, store.IsReady(key))
			read, readErr := client.GetRecord(key)
			values, verifiedErr := store.List(prefix)
			listed, total, listErr := client.ListRecords(prefix, 0, 0)
			if scenario == "known-height-wins" {
				assert.ErrorIs(t, readErr, dkvs.ErrExpiredRecord)
				assert.Nil(t, read)
				assert.Empty(t, values)
				assert.Empty(t, listed)
				assert.Zero(t, total)
			} else {
				assert.NoError(t, readErr)
				assert.NotNil(t, read)
				assert.Len(t, values, 1)
				assert.Len(t, listed, 1)
				assert.Equal(t, 1, total)
			}
			assert.NoError(t, verifiedErr)
			assert.NoError(t, listErr)
			height, known := manager.dkvs.verificationHeight()
			assert.True(t, known, "a confirmed zero height is known, too")
			if scenario == "known-height-wins" {
				assert.Equal(t, dkvs.RecordExpiryHeight(record), height)
			} else {
				assert.Equal(t, before.ViewHeight, height)
			}
			assert.Zero(t, requests, "confirmed local reads must not require transport")
			after, err := manager.GetDKVSSubscriptionStatus()
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}

func TestDKVSReviewRememberedPrefixCannotUsePreviousReadinessOrHeight(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	client, err := manager.GetDKVSClient()
	require.NoError(t, err)
	key := accountTestKey(t, manager, "sixth-ready/value")
	prefix, err := dkvs.CollectionPathForKey(key)
	require.NoError(t, err)
	_, err = finalDKVSSeedClient(manager, remote).PutRecord(freeLocalRecord(t, manager, key, 1, "confirmed"))
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
	releaseDKVSManagerRuntime(manager.dkvs)
	manager.dkvs = nil
	client, err = manager.GetDKVSClient()
	require.NoError(t, err)
	newKey := accountTestKey(t, manager, "sixth-new-prefix/value")
	manager.dkvs.rememberPaths([]string{newKey})
	store := &dkvsStore{manager: manager.dkvs, client: client}
	assert.False(t, store.IsReady(key), "all desired prefixes must complete before confirmed reads resume")
	assert.False(t, store.IsReady(newKey))
	_, known := manager.dkvs.verificationHeight()
	assert.False(t, known, "an incomplete replica cannot restore verification authority")
}

func TestDKVSReviewAccountReadinessCheckUsesOnlyConfirmedReplica(t *testing.T) {
	manager, _, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	remote := newRGB11MemoryDKVSHTTP()
	http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	configureRGB11DKVSTestManager(manager, http)
	root, err := manager.accountManagementRootWallet()
	require.NoError(t, err)
	stateKey, err := manager.accountManagedStateKey(root)
	require.NoError(t, err)
	dataKey, err := manager.accountManagedDataBlobKey(root)
	require.NoError(t, err)
	for _, key := range []string{stateKey, dataKey} {
		prefix, err := dkvs.CollectionPathForKey(key)
		require.NoError(t, err)
		require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	}
	requests := 0
	http.post = func(context.Context, string, []byte) ([]byte, error) {
		requests++
		return nil, &net.DNSError{Err: "offline", IsTimeout: true}
	}
	assert.ErrorIs(t, manager.requireCurrentAccountManagedData(), ErrDKVSPathNotSynced)
	assert.Zero(t, requests, "readiness observes the local replica; the existing worker owns synchronization")
}

func TestDKVSReviewAccountReadyCancellationPreservesReceivingWorker(t *testing.T) {
	manager, source, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	remote := newRGB11MemoryDKVSHTTP()
	http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	configureRGB11DKVSTestManager(manager, http)
	root, err := manager.accountManagementRootWallet()
	require.NoError(t, err)
	for key, value := range source.records {
		record, err := newDKVSAccountSignedRecordWithFreeLocal(root, key, value.Value,
			dkvs.RecordOptions{Seq: value.Seq, IssueHeight: 1, TTL: 144})
		require.NoError(t, err)
		_, err = finalDKVSSeedClient(manager, remote).PutRecord(record)
		require.NoError(t, err)
		prefix, err := dkvs.CollectionPathForKey(key)
		require.NoError(t, err)
		require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	}
	started, release := make(chan struct{}), make(chan struct{})
	var blockOnce, releaseOnce sync.Once
	releaseTransport := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseTransport()
	http.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if path == "/v3/dkvs/active/sync" {
			blockOnce.Do(func() {
				close(started)
				select {
				case <-release:
				case <-ctx.Done():
				}
			})
		}
		return remote.SendDKVSPostContext(ctx, path, body)
	}
	require.NoError(t, manager.StartDKVSSync())
	defer manager.StopDKVSSync()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the existing receiving worker did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	assert.ErrorIs(t, manager.WaitAccountManagedDataReady(ctx), context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 500*time.Millisecond)
	assert.NoError(t, manager.dkvs.requestContext().Err(), "the caller cannot cancel the shared receiving worker")
	manager.dkvs.runMu.Lock()
	busy := manager.dkvs.runActive
	manager.dkvs.runMu.Unlock()
	assert.True(t, busy, "the worker's blocked transport remains owned by that worker")
	releaseTransport()
	ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, manager.WaitAccountManagedDataReady(ctx), "the original worker must complete reception after the cancelled waiter leaves")
}

func TestDKVSReviewAnotherExistingRootCannotBeReportedAsAbsent(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	source, sourceValues, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	require.NoError(t, source.syncAccountRootWrapper(sourceValues))
	sourceRoot, err := source.accountManagementRootWallet()
	require.NoError(t, err)
	remote := newRGB11MemoryDKVSHTTP()
	for key, value := range sourceValues.records {
		record, err := newDKVSAccountSignedRecordWithFreeLocal(sourceRoot, key, value.Value,
			dkvs.RecordOptions{Seq: value.Seq, IssueHeight: 1, TTL: 144})
		require.NoError(t, err)
		_, err = finalDKVSSeedClient(source, remote).PutRecord(record)
		require.NoError(t, err)
	}
	target := newAccountManagementAutoTestManager(t)
	configureRGB11DKVSTestManager(target, remote)
	_, err = target.ImportWallet("legal winner thank year wave sausage worth useful legal winner thank yellow", "password")
	require.NoError(t, err)
	require.NoError(t, target.InitializeAccountManagement("password"))
	client, err := target.GetDKVSClient()
	require.NoError(t, err)
	key := accountTestKey(t, target, "sixth-discovery/current")
	_, err = finalDKVSSeedClient(target, remote).PutRecord(freeLocalRecord(t, target, key, 1, "confirmed"))
	require.NoError(t, err)
	prefix, err := dkvs.CollectionPathForKey(key)
	require.NoError(t, err)
	require.NoError(t, target.SubscribeDKVSPrefix(prefix))
	require.NoError(t, target.dkvs.forceCurrentPrefixes(client, []string{prefix}))
	before := target.GetWalletCatalog()
	identity := target.GetAccountManagementStatus().AccountID
	require.NotEqual(t, identity, source.GetAccountManagementStatus().AccountID)
	wrapperKey, err := accountRootWrapperKey(sourceRoot)
	require.NoError(t, err)
	_, err = NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote).GetRecordDirect(wrapperKey)
	require.NoError(t, err, "control: the other root exists at the same source")
	_, err = target.RecoverAccountManagementFromRootMnemonic(context.Background(), accountRootWrapperTestMnemonic, "password")
	assert.ErrorIs(t, err, ErrRootAccountWrapperInvalid, "reject replacing the active account after confirming the other root, rather than claiming it is absent")
	assert.NotErrorIs(t, err, ErrRootAccountNotFound)
	assert.Equal(t, identity, target.GetAccountManagementStatus().AccountID)
	assert.Equal(t, before, target.GetWalletCatalog())
}

func TestDKVSReviewAccountReadyWaitCancelsDuringInitialSync(t *testing.T) {
	manager, _, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	require.False(t, manager.accountProfile.ManagedDataDirty)
	require.NotZero(t, manager.accountProfile.StateSeq)
	require.NotZero(t, manager.accountProfile.ManagedDataRevision)
	root, err := manager.accountManagementRootWallet()
	require.NoError(t, err)
	stateKey, err := manager.accountManagedStateKey(root)
	require.NoError(t, err)
	dataKey, err := manager.accountManagedDataBlobKey(root)
	require.NoError(t, err)
	for _, key := range []string{stateKey, dataKey} {
		prefix, err := dkvs.CollectionPathForKey(key)
		require.NoError(t, err)
		require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	}

	started, release := make(chan struct{}, 1), make(chan struct{})
	releaseTransport := func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/v3/dkvs/config") {
			select {
			case started <- struct{}{}:
			default:
			}
			select {
			case <-r.Context().Done():
				return
			case <-release:
				http.Error(w, "offline", http.StatusServiceUnavailable)
				return
			}
		}
		http.Error(w, "unexpected request", http.StatusBadRequest)
	}))
	defer server.Close()
	defer releaseTransport()
	manager.cfg.IndexerL2.Scheme = "http"
	manager.cfg.IndexerL2.Host = strings.TrimPrefix(server.URL, "http://")
	manager.cfg.IndexerL2.Proxy = ""
	manager.http = &NetClient{Client: server.Client()}
	require.NoError(t, manager.StartDKVSSync())
	defer manager.StopDKVSSync()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		releaseTransport()
		t.Fatal("the background initial sync did not reach real HTTP")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- manager.WaitAccountManagedDataReady(ctx) }()
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(250 * time.Millisecond):
		t.Error("cancelling the caller did not stop the readiness wait while its DKVS request remained blocked")
		releaseTransport()
		select {
		case err := <-done:
			t.Logf("wait returned only after unrelated transport completion: %v", err)
		case <-time.After(2 * time.Second):
			t.Fatal("readiness wait did not finish after the test released HTTP")
		}
	}
}

func TestDKVSReviewAccountReadyDefaultDeadlineSurvivesWalletUnavailable(t *testing.T) {
	manager, _, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	// Startup may have the durable account profile before its root wallet is
	// unlocked. This is the public wait's existing retryable wallet condition.
	manager.mutex.Lock()
	for _, info := range manager.walletInfoMap {
		info.Wallet = nil
	}
	manager.wallet = nil
	manager.mutex.Unlock()
	require.ErrorIs(t, manager.requireCurrentAccountManagedData(), ErrAccountManagementWalletUnavailable)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- manager.WaitAccountManagedDataReady(ctx) }()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.DeadlineExceeded, "the existing default timeout must bound the whole wait")
	case <-time.After(accountManagedDataReadyTimeout + time.Second):
		cancel()
		select {
		case err := <-done:
			t.Errorf("the wait outlived its %s default deadline and only caller cancellation released it: %v", accountManagedDataReadyTimeout, err)
		case <-time.After(2 * time.Second):
			t.Fatal("the default-deadline wait did not respond to cleanup cancellation")
		}
	}
}

func TestDKVSReviewConfirmedOfflineReplicaReadableAfterManagerRestart(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	configureRGB11DKVSTestManager(manager, http)
	client, err := manager.GetDKVSClient()
	require.NoError(t, err)
	key := accountTestKey(t, manager, "sixth-restart/value")
	prefix, err := dkvs.CollectionPathForKey(key)
	require.NoError(t, err)
	_, err = finalDKVSSeedClient(manager, remote).PutRecord(freeLocalRecord(t, manager, key, 1, "confirmed"))
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
	http.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if path == "/v3/dkvs/active/sync" {
			return nil, &net.DNSError{Err: "offline", IsTimeout: true}
		}
		return remote.SendDKVSPostContext(ctx, path, body)
	}
	_, _, err = manager.syncDKVSOnceResult()
	require.Error(t, err)
	before, err := manager.GetDKVSSubscriptionStatus()
	require.NoError(t, err)
	require.Equal(t, DKVSSubscriptionOfflineReady, before.Status)
	require.NotZero(t, before.ViewHeight, "the confirmed verification boundary is already persisted")
	_, err = client.GetRecord(key)
	require.NoError(t, err, "control: offline read works before the in-memory manager is replaced")
	http.get = func(context.Context, string, map[string]string) ([]byte, error) {
		return nil, &net.DNSError{Err: "offline", IsTimeout: true}
	}
	http.post = func(context.Context, string, []byte) ([]byte, error) {
		return nil, &net.DNSError{Err: "offline", IsTimeout: true}
	}

	// Recreate the production coordinator over the same durable replica. No
	// height observations, clients, or transient readiness survive this restart.
	releaseDKVSManagerRuntime(manager.dkvs)
	manager.dkvs = nil
	restarted, err := manager.GetDKVSClient()
	require.NoError(t, err)
	_, _, syncErr := manager.syncDKVSOnceResult()
	require.Error(t, syncErr, "startup reconciliation encounters the offline transport")
	after, err := manager.GetDKVSSubscriptionStatus()
	require.NoError(t, err)
	require.Equal(t, DKVSSubscriptionOfflineReady, after.Status)
	require.Equal(t, before.ViewHeight, after.ViewHeight)
	read, readErr := restarted.GetRecord(key)
	t.Logf("persisted_status=%s persisted_height=%d read_error=%v", after.Status, after.ViewHeight, readErr)
	if assert.NoError(t, readErr, "a restart must retain the same confirmed offline read boundary") {
		require.NotNil(t, read)
		assert.Equal(t, "confirmed", string(read.Value))
	}
	store := &dkvsStore{manager: manager.dkvs, client: restarted}
	values, verifiedListErr := store.List(prefix)
	if assert.NoError(t, verifiedListErr, "the verified directory entry needs the same restored height as Get") {
		assert.Len(t, values, 1)
	}
	listed, total, listErr := restarted.ListRecords(prefix, 0, 0)
	if assert.NoError(t, listErr) {
		assert.Len(t, listed, 1)
		assert.Equal(t, 1, total)
	}
}
