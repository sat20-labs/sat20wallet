package wallet

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"sync"
	"testing"
	"time"

	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRootOfflineAbsentIsPending(t *testing.T) {
	old := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = old })
	ctx := context.Background()
	source, remote, pkg, auth, secret := newLocalActivationFixture(t)
	defer zeroBytes(secret)
	require.Equal(t, AccountStorageTemporary, auth.Mode)
	repo, err := source.NewAccountRepositoryForStorage(*auth)
	require.NoError(t, err)
	require.NoError(t, repo.SaveRecoveryPackage(ctx, *pkg))
	locator := pkg.Envelope.Locator
	rootKey, err := dkvs.AccountPersonalKey(locator.AccountID, accountRootWrapperPath)
	require.NoError(t, err)
	pkgKey, err := dkvs.AccountPersonalKey(locator.AccountID, accountPackagePath(locator.PackageID))
	require.NoError(t, err)
	initial := snapshotRGB11MemoryRecords(remote)
	require.Nil(t, initial[rootKey])
	require.NotNil(t, initial[pkgKey])
	require.NoError(t, dkvs.VerifySignature(initial[pkgKey]))
	rootPrefix, err := dkvs.CollectionPathForKey(rootKey)
	require.NoError(t, err)
	rootPages := func() int {
		remote.mu.Lock()
		defer remote.mu.Unlock()
		return remote.snapshotsByPrefix[rootPrefix] + remote.deltasByPrefix[rootPrefix]
	}

	device := func(t *testing.T, link HttpClient) *Manager {
		t.Helper()
		m := newAccountManagementAutoTestManager(t)
		configureRGB11DKVSTestManager(m, link)
		return m
	}
	warmPreview := func(t *testing.T, m *Manager) {
		t.Helper()
		loaded, err := m.LoadAccountRecoveryPackage(auth.Location, locator)
		require.NoError(t, err)
		require.Equal(t, locator, loaded.Envelope.Locator)
		preview, err := m.LoadAccountManagementStateForRecovery(auth.Location, locator, secret, accountRootWrapperTestMnemonic)
		require.ErrorIs(t, err, ErrRootAccountDiscoveryPending)
		require.Nil(t, preview)
		status, err := m.GetDKVSSubscriptionStatus()
		require.NoError(t, err)
		require.Equal(t, DKVSSubscriptionReady, status.Status)
	}
	firstDiscovery := func(t *testing.T, m *Manager, want, reject error) {
		t.Helper()
		before, err := m.GetWalletCatalogSnapshot()
		require.NoError(t, err)
		require.Empty(t, before.Wallets)
		require.Empty(t, before.RootAccountID)
		records := snapshotRGB11MemoryRecords(remote)
		result, err := m.RecoverAccountManagementFromRootMnemonic(ctx, accountRootWrapperTestMnemonic, "password")
		assert.ErrorIs(t, err, want)
		assert.NotErrorIs(t, err, reject)
		if want != ErrRootAccountDiscoveryPending {
			assert.NotErrorIs(t, err, ErrRootAccountDiscoveryPending)
		}
		assert.Empty(t, result)
		after, err := m.GetWalletCatalogSnapshot()
		require.NoError(t, err)
		assert.Equal(t, before, after)
		assert.False(t, m.GetAccountManagementStatus().Active)
		assert.True(t, reflect.DeepEqual(records, snapshotRGB11MemoryRecords(remote)), "discovery modified the endpoint")
	}
	t.Run("online_absent", func(t *testing.T) {
		m := device(t, remote)
		before := rootPages()
		firstDiscovery(t, m, ErrRootAccountNotFound, ErrRootAccountDiscoveryPending)
		require.Equal(t, before+1, rootPages())
		id, err := m.ImportWallet(accountRootWrapperTestMnemonic, "password")
		require.NoError(t, err)
		before = rootPages()
		require.True(t, m.GetAccountManagementStatus().Active)
		require.NoError(t, m.EnsureAccount(id, 1, "Second", ""))
		require.NoError(t, m.SwitchAccount(1))
		_, err = m.RecoverAccountManagementFromCurrentWallet(ctx, "password")
		require.ErrorIs(t, err, ErrRootAccountNotFound)
		_, err = m.RecoverAccountManagementFromRootMnemonic(ctx, accountRootWrapperTestMnemonic, "password")
		require.ErrorIs(t, err, ErrRootAccountNotFound)
		require.Equal(t, before, rootPages(), "first-import freshness must not affect an existing catalog")
	})
	t.Run("warm_absent_with_monitor", func(t *testing.T) {
		m := device(t, remote)
		warmPreview(t, m)
		monitor, err := m.CreateMonitorWallet(source.wallet.GetAddress())
		require.NoError(t, err)
		require.Empty(t, m.GetWalletCatalog())
		before := rootPages()
		values, err := m.RecoverAccountManagementFromRootMnemonic(ctx, accountRootWrapperTestMnemonic, "password")
		require.ErrorIs(t, err, ErrRootAccountNotFound)
		require.Empty(t, values)
		require.Equal(t, before+1, rootPages(), "unsaved monitor is not a durable first wallet")
		require.Equal(t, monitor, m.GetCurrentWalletId())
		require.Empty(t, m.GetWalletCatalog())
	})
	offline := &net.DNSError{Err: "test DKVS endpoint offline", IsTimeout: true}
	forbidden := &HTTPResponseError{StatusCode: http.StatusForbidden, Body: []byte(`{"error_code":"DKVS_RECORD_NOT_FOUND"}`)}
	unauthorized := &HTTPResponseError{StatusCode: http.StatusUnauthorized, Body: []byte(`{"error_code":"DKVS_STORAGE_UNAVAILABLE"}`)}
	bodyRead := func(err error) error { return &url.Error{Op: "read", URL: "https://dkvs.test", Err: err} }
	failedReceive := func(t *testing.T, fault, want error, warm bool) {
		t.Helper()
		transport := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
		m := device(t, transport)
		if warm {
			warmPreview(t, m)
		}
		attempts := 0
		transport.post = func(context.Context, string, []byte) ([]byte, error) {
			attempts++
			return nil, fault
		}
		firstDiscovery(t, m, want, ErrRootAccountNotFound)
		require.Equal(t, 1, attempts)
	}
	for _, tc := range []struct {
		name  string
		fault error
		want  error
	}{
		{"receive_record_not_found", dkvs.ErrRecordNotFound, ErrRootAccountDiscoveryPending},
		{"receive_route_404", &HTTPResponseError{StatusCode: http.StatusNotFound}, ErrRootAccountDiscoveryPending},
		{"receive_5xx", &HTTPResponseError{StatusCode: http.StatusBadGateway}, ErrRootAccountDiscoveryPending},
		{"receive_permission", dkvs.ErrPermissionDenied, dkvs.ErrPermissionDenied},
		{"403_missing_body", forbidden, forbidden},
		{"401_unavailable_body", unauthorized, unauthorized},
		{"url_truncated_response", bodyRead(io.ErrUnexpectedEOF), ErrRootAccountDiscoveryPending},
		{"url_fetch_rejected", &url.Error{Op: "Post", Err: errors.New("net/http: fetch() failed: TypeError: Failed to fetch")}, ErrRootAccountDiscoveryPending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failedReceive(t, tc.fault, tc.want, false)
		})
	}
	t.Run("warm_fetch_body_interrupted", func(t *testing.T) {
		failedReceive(t, bodyRead(errors.New("読み込みが中断されました")), ErrRootAccountDiscoveryPending, true)
	})

	for _, change := range []string{"catalog_changed", "same_url_new_client", "monitor_changed"} {
		t.Run(change, func(t *testing.T) {
			gate := &blockedAccountConfigHTTP{rgb11MemoryDKVSHTTP: remote,
				started: make(chan struct{}), release: make(chan struct{}), continueAfterRelease: true}
			m := device(t, gate)
			if change == "monitor_changed" {
				_, err := m.CreateMonitorWallet(source.wallet.GetAddress())
				require.NoError(t, err)
			}
			done := make(chan error, 1)
			var once sync.Once
			release := func() { once.Do(func() { close(gate.release) }) }
			defer release()
			before := rootPages()
			go func() {
				_, err := m.RecoverAccountManagementFromRootMnemonic(ctx, accountRootWrapperTestMnemonic, "password")
				done <- err
			}()
			select {
			case <-gate.started:
			case <-time.After(5 * time.Second):
				t.Fatal("discovery did not reach the source")
			}
			require.NoError(t, accountOperationWithin(t, "local discovery scope change", func() error {
				if len(m.GetWalletCatalog()) != 0 {
					return errors.New("unexpected durable wallet during discovery")
				}
				switch change {
				case "catalog_changed":
					_, err := m.ImportWallet(accountRootWrapperTestMnemonic, "password")
					return err
				case "monitor_changed":
					_, err := m.CreateMonitorWallet(source.wallet.GetAddress())
					return err
				}
				m.SetDKVSHttpClient(remote)
				return nil
			}))
			release()
			select {
			case err := <-done:
				require.ErrorIs(t, err, ErrRootAccountDiscoveryPending)
				require.NotErrorIs(t, err, ErrRootAccountNotFound)
			case <-time.After(5 * time.Second):
				t.Fatal("stale discovery did not finish")
			}
			require.Equal(t, before+1, rootPages(), "do not retry a stale request")
		})
	}

	// A locator preview, not a previous mnemonic discovery, warms this empty
	// device's account directory while only the genuine recovery package exists.
	link := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	target := device(t, link)
	warmPreview(t, target)
	store, err := target.accountDKVSStore()
	require.NoError(t, err)
	baseline, err := newDKVSReplicaStore(target.db).ReadSnapshot(store.client.replicaNamespace, []string{rootKey, pkgKey})
	require.NoError(t, err)
	require.Nil(t, baseline[rootKey])
	require.NotNil(t, baseline[pkgKey])
	require.Equal(t, dkvs.RecordHash(initial[pkgKey]), dkvs.RecordHash(baseline[pkgKey]))
	require.True(t, reflect.DeepEqual(initial, snapshotRGB11MemoryRecords(remote)), "preview modified the endpoint")

	// Only the target loses DKVS access. Let the real receive path decide
	// offline readiness; never install a baseline or grant in the test.
	link.post = func(context.Context, string, []byte) ([]byte, error) { return nil, offline }
	link.get = func(context.Context, string, map[string]string) ([]byte, error) { return nil, offline }
	t.Run("disconnected_while_ready", func(t *testing.T) {
		firstDiscovery(t, target, ErrRootAccountDiscoveryPending, ErrRootAccountNotFound)
	})
	_, _, receiveErr := target.syncDKVSOnceResult()
	require.ErrorIs(t, receiveErr, offline)
	status, err := target.GetDKVSSubscriptionStatus()
	require.NoError(t, err)
	require.Equal(t, DKVSSubscriptionOfflineReady, status.Status)
	require.True(t, store.IsReady(rootKey))
	require.ErrorIs(t, store.manager.requireOnlinePaths(store.client, []string{rootKey}), ErrDKVSPathNotSynced)
	cached, err := store.client.GetRecord(pkgKey)
	require.NoError(t, err)
	require.Equal(t, dkvs.RecordHash(initial[pkgKey]), dkvs.RecordHash(cached))

	require.NoError(t, source.ActivateAccountManagement(secret, "password", *auth, locator, ""))
	require.NoError(t, source.SyncAccountManagementState(ctx))
	source.StopDKVSSync()
	root := snapshotRGB11MemoryRecords(remote)[rootKey]
	require.NotNil(t, root)
	require.NoError(t, dkvs.VerifySignature(root))
	_, err = store.client.GetRecordDirect(rootKey)
	require.ErrorIs(t, err, offline, "the target cannot consult the now-active source")
	t.Run("fresh_offline", func(t *testing.T) {
		fresh := device(t, link)
		empty, err := fresh.GetDKVSSubscriptionStatus()
		require.NoError(t, err)
		require.Equal(t, DKVSSubscriptionSyncing, empty.Status)
		require.Empty(t, empty.Generations)
		firstDiscovery(t, fresh, ErrRootAccountDiscoveryPending, ErrRootAccountNotFound)
	})
	t.Run("cached_offline", func(t *testing.T) {
		firstDiscovery(t, target, ErrRootAccountDiscoveryPending, ErrRootAccountNotFound)
	})
	t.Run("reconnected", func(t *testing.T) {
		link.post, link.get = nil, nil
		target.serverNode = source.serverNode
		t.Cleanup(target.StopDKVSSync)
		results, err := target.RecoverAccountManagementFromRootMnemonic(ctx, accountRootWrapperTestMnemonic, "password")
		require.NoError(t, err)
		require.NotEmpty(t, results)
		status := target.GetAccountManagementStatus()
		require.True(t, status.RecoveryConfigured)
		require.Equal(t, locator.AccountID, status.AccountID)
	})
	t.Run("cached_signed_root", func(t *testing.T) {
		// Isolate the root read: full recovery deliberately also refreshes its
		// linked package, which shares the very same prefix.
		peer := device(t, remote)
		s, err := peer.accountDKVSStore()
		require.NoError(t, err)
		s.manager.rememberPaths([]string{rootKey})
		require.NoError(t, s.manager.ensureCurrentSubscription(s.client))
		before := rootPages()
		value, _, err := readFirstRoot(ctx, s, rootKey, "")
		require.NoError(t, err)
		require.NotNil(t, value)
		require.Equal(t, dkvs.RecordHash(root), dkvs.RecordHash(value.record))
		require.Equal(t, before, rootPages())
	})
}
