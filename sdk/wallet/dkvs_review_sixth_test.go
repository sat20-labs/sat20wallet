package wallet

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
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
	assert.Equal(t, DKVSSubscriptionSyncing, after.Status, "the selected source has no session write qualification")
	after.Status = before.Status
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
			assert.Equal(t, scenario == "known-height-wins", store.IsReady(key), "a new runtime or selected URL must first establish its session")
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
			if scenario == "known-height-wins" {
				assert.Zero(t, requests, "the continuing session reads its confirmed local replica")
			} else {
				assert.Equal(t, 1, requests, "startup must attempt synchronization before falling back to confirmed cache")
				cached, cacheErr := client.GetRecord(key)
				assert.NoError(t, cacheErr)
				assert.Equal(t, dkvs.RecordHash(record), dkvs.RecordHash(cached))
				assert.Equal(t, 1, requests, "subsequent offline cache reads must not repeatedly require transport")
				assert.Error(t, manager.dkvs.requireOnlinePaths(client, []string{key}), "offline cached reads must not grant writes")
			}
			after, err := manager.GetDKVSSubscriptionStatus()
			require.NoError(t, err)
			if scenario != "known-height-wins" {
				assert.Equal(t, DKVSSubscriptionOfflineReady, after.Status)
				after.Status = before.Status
			}
			assert.Equal(t, before, after, "offline fallback preserves the confirmed source, generations, and verification height")
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

func accountDKVSReviewKeys(t *testing.T, manager *Manager) ([]string, []string) {
	t.Helper()
	localKeys := accountLocalRecordKeys(t, manager)
	require.Contains(t, localKeys, accountWalletModuleKey(t, manager))
	indexKey, err := account.ManagedIndexKey(manager.accountProfile.AccountID)
	require.NoError(t, err)
	require.Contains(t, localKeys, indexKey)
	manager.mutex.RLock()
	controls, err := manager.accountControlKeysLocked()
	manager.mutex.RUnlock()
	require.NoError(t, err)
	all := make(map[string]bool, len(localKeys)+len(controls))
	for _, key := range localKeys {
		all[key] = true
	}
	for key := range controls {
		all[key] = true
	}
	keys := make([]string, 0, len(all))
	for key := range all {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return localKeys, keys
}

func TestDKVSReviewAccountReadinessCheckUsesOnlyConfirmedReplica(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	manager, _, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	remote := newRGB11MemoryDKVSHTTP()
	http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	configureRGB11DKVSTestManager(manager, http)
	_, keys := accountDKVSReviewKeys(t, manager)
	for _, key := range keys {
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
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	remote := newRGB11MemoryDKVSHTTP()
	http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	service := newRGB11MessageNodeClient(remote)
	manager := newAccountManagementAutoTestManager(t)
	manager.serverNode = NewNode(service, "message.test", SERVER_NODE,
		service.CoreNodePubKey(), service.CoreNodePubKey())
	configureRGB11DKVSTestManager(manager, http)
	_, err := manager.ImportWallet(accountRootWrapperTestMnemonic, "password")
	require.NoError(t, err)
	require.NoError(t, manager.InitializeAccountManagement("password"))
	require.NoError(t, manager.refreshDKVSRegistrations())
	_, err = manager.syncDKVSOnce()
	require.NoError(t, err)
	pkg, auth, secret := rereviewRecoveryPackage(t, manager)
	defer zeroBytes(secret)
	require.NoError(t, manager.ActivateAccountManagement(secret, "password", *auth,
		pkg.Envelope.Locator, ""))
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	require.True(t, manager.GetAccountManagementStatus().DKVSWriteAllowed,
		"the original endpoint must establish a verified account baseline before worker restart")
	endpointBefore, err := manager.GetDKVSSubscriptionStatus()
	require.NoError(t, err)
	require.NotEmpty(t, endpointBefore.EndpointID)
	started, release := make(chan struct{}), make(chan struct{})
	var blockOnce, releaseOnce sync.Once
	var hold atomic.Bool
	var requestCanceled atomic.Bool
	releaseTransport := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseTransport()
	http.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if path == "/v3/dkvs/active/sync" && hold.Load() {
			blockOnce.Do(func() {
				close(started)
				select {
				case <-release:
				case <-ctx.Done():
					requestCanceled.Store(true)
				}
			})
		}
		return remote.SendDKVSPostContext(ctx, path, body)
	}
	// Keep the same endpoint from initial discovery through the receiving
	// worker. Requalification after Start must come from that real worker.
	t.Cleanup(manager.StopDKVSSync)
	require.NoError(t, manager.StartDKVSSync())
	require.NoError(t, manager.RetryDKVSSync())
	require.Eventually(t, func() bool {
		return manager.GetAccountManagementStatus().DKVSWriteAllowed
	}, 5*time.Second, 10*time.Millisecond, "the original endpoint must restore real write qualification")
	qualifiedEndpoint, err := manager.GetDKVSSubscriptionStatus()
	require.NoError(t, err)
	require.Equal(t, endpointBefore.EndpointID, qualifiedEndpoint.EndpointID,
		"worker requalification must use the same endpoint that established the original baseline")
	hold.Store(true)
	require.NoError(t, manager.RetryDKVSSync())
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the existing receiving worker did not start")
	}
	require.NoError(t, manager.UpdateWalletName(manager.GetCurrentWalletId(), "pending backup"),
		"a verified endpoint can accept a local edit while its shared receive worker is in flight")
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
	assert.False(t, requestCanceled.Load(), "canceling the readiness waiter must not cancel the shared request")
	releaseTransport()
	ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, manager.WaitAccountManagedDataReady(ctx), "the original worker must complete reception after the cancelled waiter leaves")
	status := manager.GetAccountManagementStatus()
	assert.True(t, status.DKVSWriteAllowed)
	assert.False(t, status.ManagedDataDirty)
	assert.Zero(t, status.PendingChanges)
	store, err := manager.accountDKVSStore()
	require.NoError(t, err)
	module, err := store.GetAuthoritative(accountWalletModuleKey(t, manager))
	require.NoError(t, err)
	require.NotNil(t, module)
	manager.mutex.RLock()
	secretCopy := append([]byte(nil), manager.accountSecret...)
	manager.mutex.RUnlock()
	defer zeroBytes(secretCopy)
	state, err := account.OpenManagedState(secretCopy, status.AccountID, module.Value)
	require.NoError(t, err)
	require.Equal(t, "pending backup", state.Wallets[0].Name,
		"the original receiving worker must publish the pending local edit after the waiter cancels")
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
	wrapperKey, err := accountRootWrapperKey(sourceRoot)
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
	sourceStatus := source.GetAccountManagementStatus()
	packageKey, err := dkvs.AccountPersonalKey(sourceStatus.AccountID, accountPackagePath(sourceStatus.PackageID))
	require.NoError(t, err)
	accountPrefixes := make([]string, 0, 2)
	for _, key := range []string{wrapperKey, packageKey} {
		prefix, prefixErr := dkvs.CollectionPathForKey(key)
		require.NoError(t, prefixErr)
		alreadyAdded := false
		for _, current := range accountPrefixes {
			if current == prefix {
				alreadyAdded = true
				break
			}
		}
		if !alreadyAdded {
			accountPrefixes = append(accountPrefixes, prefix)
			require.NoError(t, target.SubscribeDKVSPrefix(prefix))
		}
	}
	require.NoError(t, target.dkvs.forceCurrentPrefixes(client, accountPrefixes))
	remoteStore, err := target.accountDKVSStore()
	require.NoError(t, err)
	observedWrapper, err := remoteStore.Get(wrapperKey)
	require.NoError(t, err)
	require.NotNil(t, observedWrapper, "the foreign root wrapper must be present in this synchronized non-empty DB")
	observedPackage, err := remoteStore.Get(packageKey)
	require.NoError(t, err)
	require.NotNil(t, observedPackage, "the recovery package must be readable before testing the root-identity guard")
	before := target.GetWalletCatalog()
	require.NotEmpty(t, before, "the target database must contain its own wallet before root recovery")
	identity := target.GetAccountManagementStatus().AccountID
	require.NotEqual(t, identity, sourceStatus.AccountID)
	profileBefore, err := target.db.Read(accountManagementProfileKey())
	require.NoError(t, err)
	_, err = NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote).GetRecordDirect(wrapperKey)
	require.NoError(t, err, "control: the other root exists at the same source")
	_, err = target.RecoverAccountManagementFromRootMnemonic(context.Background(), accountRootWrapperTestMnemonic, "password")
	require.ErrorContains(t, err, "account restore requires an empty wallet database",
		"the request must reach the non-empty-database guard for the different root")
	assert.NotErrorIs(t, err, ErrRootAccountNotFound)
	assert.Equal(t, identity, target.GetAccountManagementStatus().AccountID)
	assert.Equal(t, before, target.GetWalletCatalog())
	profileAfter, readErr := target.db.Read(accountManagementProfileKey())
	assert.NoError(t, readErr)
	assert.Equal(t, profileBefore, profileAfter, "rejected root selection must not change the persisted active account")
}

func TestDKVSReviewAccountReadyWaitCancelsDuringInitialSync(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	manager, _, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	require.False(t, manager.accountProfile.ManagedDataDirty)
	localKeys, _ := accountDKVSReviewKeys(t, manager)
	for _, key := range localKeys {
		value, keyErr := manager.accountLocalForKey(manager.accountProfile.AccountID, key)
		require.NoError(t, keyErr)
		require.NotNil(t, value)
		require.NotNil(t, value.Baseline, "the cancellation fixture starts from a confirmed per-key record")
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

func TestDKVSReadyNeedsWallet(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
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
	started := time.Now()
	err := manager.WaitAccountManagedDataReady(context.Background())
	assert.ErrorIs(t, err, ErrAccountManagementWalletUnavailable,
		"a locked wallet is a fail-fast preparation condition, not a waitable network state")
	assert.Less(t, time.Since(started), 500*time.Millisecond,
		"wallet unavailability should be reported without consuming the default network wait deadline")
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
