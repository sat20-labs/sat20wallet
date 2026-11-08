package wallet

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type accountACKModuleFact struct {
	Provider, Scope, Key, Codec     string
	Generation, Captured, Confirmed uint64
	ValueHash, BaselineHash         string
	BaselineSeq                     uint64
	Deleted, Restoring, Conflict    bool
	LastError                       string
}

func accountACKModuleFacts(t *testing.T, manager *Manager) []accountACKModuleFact {
	t.Helper()
	accountID := manager.GetAccountManagementStatus().AccountID
	modules, err := manager.listAccountLocal(accountID)
	require.NoError(t, err)
	result := make([]accountACKModuleFact, 0, len(modules))
	for _, module := range modules {
		fact := accountACKModuleFact{Provider: module.Provider, Scope: module.Scope, Key: module.Key,
			Codec: module.Codec, Generation: module.Generation, Captured: module.Captured,
			Confirmed: module.Confirmed, ValueHash: accountStateDigest(module.Value),
			Deleted: module.Deleted, Restoring: module.Restoring, Conflict: module.Conflict,
			LastError: module.LastError}
		if module.Baseline != nil {
			fact.BaselineHash = accountStateDigest(module.Baseline.Value)
			fact.BaselineSeq = module.Baseline.Seq
		}
		result = append(result, fact)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Provider != result[j].Provider {
			return result[i].Provider < result[j].Provider
		}
		return result[i].Scope < result[j].Scope
	})
	return result
}

func requireAccountACKModulesClean(t *testing.T, manager *Manager) {
	t.Helper()
	facts := accountACKModuleFacts(t, manager)
	require.NotEmpty(t, facts)
	for _, fact := range facts {
		require.NotEmpty(t, fact.Key, "confirmed module must retain its canonical DKVS key")
		require.Equal(t, fact.Generation, fact.Confirmed,
			"per-module generation and confirmed watermark must agree after initial backup")
		require.NotZero(t, fact.BaselineSeq)
		require.NotEmpty(t, fact.BaselineHash)
		require.False(t, fact.Restoring || fact.Conflict)
		require.Empty(t, fact.LastError)
	}
}

// Manager construction, import, initialization, activation and synchronization
// use the existing production lifecycle fixture. No profile/cache/ready setup.
func newAccountACKGuard(t *testing.T, remote *rgb11MemoryDKVSHTTP, transport *reviewAcceptanceHTTP) *Manager {
	t.Helper()
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	manager := newRGB11AccountScopeRecoveryManager(t, remote, newCoreE2EChain())
	configureRGB11DKVSTestManager(manager, transport)
	activateRGB11AccountScopeRecovery(t, manager, coreE2ESenderMnemonic)
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, manager.WaitAccountManagedDataReady(ctx))
	manager.mutex.RLock()
	scope := manager.accountWriteScopeLocked()
	manager.mutex.RUnlock()
	requireAccountACKModulesClean(t, manager)
	require.NotEmpty(t, scope)
	require.True(t, manager.dkvs.scopeReady(scope))
	return manager
}

func TestAccountNoACKFailureBlocks(t *testing.T) {
	remote := newRGB11MemoryDKVSHTTP()
	transport := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	var fail atomic.Bool
	fault := errors.New("injected no-op account synchronization failure")
	transport.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if path == "/v3/dkvs/active/sync" && fail.Load() {
			return nil, fault
		}
		return remote.SendDKVSPostContext(ctx, path, body)
	}
	manager := newAccountACKGuard(t, remote, transport)
	before := manager.GetWalletCatalog()
	require.NotEmpty(t, before)
	profile, err := manager.db.Read(accountManagementProfileKey())
	require.NoError(t, err)
	beforeModules := accountACKModuleFacts(t, manager)
	client, err := manager.dkvs.primaryClient()
	require.NoError(t, err)
	entries, err := newDKVSReplicaStore(manager.db).LoadOutbox(client.replicaNamespace)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		entries, err = newDKVSReplicaStore(manager.db).LoadOutbox(client.replicaNamespace)
		return err == nil && len(entries) == 0
	}, 5*time.Second, 10*time.Millisecond, "settle actual first-backup cleanup; no ACK may repair this failure")
	fail.Store(true)
	defer fail.Store(false)
	require.ErrorIs(t, manager.SyncAccountManagementState(context.Background()), fault)
	scope, onlineErr := func() (string, error) {
		manager.mutex.RLock()
		defer manager.mutex.RUnlock()
		scope := manager.accountWriteScopeLocked()
		controls, keyErr := manager.accountControlKeysLocked()
		require.NoError(t, keyErr)
		keys := make([]string, 0, len(controls))
		for key := range controls {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		// Actual failed receiving may revoke the online group as well. Observe
		// it without manufacturing an old-cache qualification that PWA cannot keep.
		onlineErr := manager.dkvs.requireOnlineGroup(client, keys)
		return scope, onlineErr
	}()
	t.Logf("after failed no-op sync: online_group_ready=%t account_ready=%t", onlineErr == nil, manager.dkvs.scopeReady(scope))
	require.False(t, manager.dkvs.scopeReady(scope))
	require.ErrorIs(t, manager.UpdateWalletName(before[0].ID, "must remain rejected"), ErrDKVSPathNotSynced)
	require.False(t, manager.dkvs.scopeReady(scope), "empty ACK preparation restored failed qualification")
	after := manager.GetWalletCatalog()
	require.True(t, len(before) == len(after) && before[0].Name == after[0].Name)
	actual, err := manager.db.Read(accountManagementProfileKey())
	require.NoError(t, err)
	require.True(t, bytes.Equal(profile, actual), "rejected preparation changed persistent profile")
	require.True(t, reflect.DeepEqual(beforeModules, accountACKModuleFacts(t, manager)),
		"rejected mutation changed the actual per-module generation, baseline, or value")
	entries, err = newDKVSReplicaStore(manager.db).LoadOutbox(client.replicaNamespace)
	require.NoError(t, err)
	require.Empty(t, entries)
	fail.Store(false)
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	require.NoError(t, manager.UpdateWalletName(before[0].ID, "after real recovery"))
	// A normal queued backup does not revoke the successful qualification.
	require.NoError(t, manager.UpdateWalletName(before[0].ID, "normal pending update"))
}

// These are callback-boundary unit regressions using a real initialized Manager.
// The old-source success is real. Calling its captured completion after the new
// source fails tests precisely the stale publication guard, not a fake Manager.
func TestAccountSyncScopePinned(t *testing.T) {
	for _, mode := range []string{"endpoint-url", "transport-generation", "worker-generation"} {
		t.Run(mode, func(t *testing.T) {
			remote := newRGB11MemoryDKVSHTTP()
			transport := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
			var fail atomic.Bool
			fault := errors.New("replacement source has not synchronized")
			transport.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
				if path == "/v3/dkvs/active/sync" && fail.Load() {
					return nil, fault
				}
				return remote.SendDKVSPostContext(ctx, path, body)
			}
			manager := newAccountACKGuard(t, remote, transport)
			late := manager.accountSyncResult()
			require.NoError(t, manager.SyncAccountManagementState(context.Background()))
			oldClient, err := manager.GetDKVSClient()
			require.NoError(t, err)
			manager.mutex.RLock()
			oldScope := manager.accountWriteScopeLocked()
			manager.mutex.RUnlock()
			fail.Store(true)
			defer fail.Store(false)
			switch mode {
			case "endpoint-url":
				// Same public source-selection/client path used by the existing DKVS
				// source-boundary tests; never install a profile or a replica by hand.
				manager.mutex.Lock()
				manager.cfg.IndexerL2.Host = "replacement.dkvs.test"
				manager.mutex.Unlock()
				manager.SetDKVSHttpClient(transport)
			case "transport-generation":
				replacement := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote, post: transport.post}
				manager.SetDKVSHttpClient(replacement)
			case "worker-generation":
				// This explicit suspend/resume is the lifecycle under test, not setup
				// suppression: the new worker runs and observes the real source failure.
				manager.StopDKVSSync()
				require.NoError(t, manager.StartDKVSSync())
			}
			current, err := manager.GetDKVSClient()
			require.NoError(t, err)
			_, err = current.GetDKVSClientConfig()
			require.NoError(t, err)
			if mode == "endpoint-url" {
				_, _, err = manager.syncDKVSOnceResult()
				require.Error(t, err)
			} else {
				require.Error(t, manager.SyncAccountManagementState(context.Background()))
			}
			manager.mutex.RLock()
			currentScope := manager.accountWriteScopeLocked()
			manager.mutex.RUnlock()
			if mode == "endpoint-url" {
				require.True(t, oldScope != currentScope)
			}
			if mode == "transport-generation" {
				require.True(t, oldClient != current)
			}
			require.False(t, manager.dkvs.scopeReady(currentScope))
			late(nil)
			require.False(t, manager.dkvs.scopeReady(currentScope), "old completion authorized the replacement scope/lifecycle")
			before := manager.GetWalletCatalog()
			require.NotEmpty(t, before)
			require.Error(t, manager.UpdateWalletName(before[0].ID, "stale completion must not authorize"))
			after := manager.GetWalletCatalog()
			require.True(t, len(before) == len(after) && before[0].Name == after[0].Name)
		})
	}
}
