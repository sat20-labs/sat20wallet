package e2e

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Import registers the service identity. A controlled 503 leaves the root
// local, and backup activation must wait for the service binding to recover.
func TestSDKAccountActivationRetry(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	runSDKAccountActivationRetry(t, network, accountSyncMnemonic(t, 249))
}

func runSDKAccountActivationRetry(t *testing.T, network *realSatoshiNet, mnemonic string) {
	t.Helper()
	config, _ := accountReviewConfig(t, network)
	target, err := url.Parse("http://" + config.IndexerL2.Host)
	require.NoError(t, err)
	proxy := httputil.NewSingleHostReverseProxy(target)
	audit := newBootstrapCASAudit()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if audit.trackReceive.Load() && r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/active/sync") {
			body, readErr := io.ReadAll(r.Body)
			_ = r.Body.Close()
			if readErr != nil {
				http.Error(w, "read receive request", http.StatusBadRequest)
				return
			}
			var request dkvs.ActiveSyncRequest
			if json.Unmarshal(body, &request) != nil {
				http.Error(w, "decode receive request", http.StatusBadRequest)
				return
			}
			audit.recordReceive(request)
			if audit.blockReceive.Load() {
				audit.blockedReceives.Add(1)
				http.Error(w, "controlled transient reconnect rejection", http.StatusServiceUnavailable)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			proxy.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/records/batch-cas") {
			proxy.ServeHTTP(w, r)
			return
		}
		body, readErr := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if readErr != nil {
			http.Error(w, "read CAS request", http.StatusBadRequest)
			return
		}
		var batch dkvscore.BatchCASRequest
		if json.Unmarshal(body, &batch) != nil {
			http.Error(w, "decode CAS request", http.StatusBadRequest)
			return
		}
		rows, bindingKey, valid := summarizeBootstrapCAS(batch)
		if !valid {
			http.Error(w, "classify CAS request", http.StatusBadRequest)
			return
		}
		if bindingKey != "" {
			audit.setBindingKey(bindingKey)
		}
		if bindingKey != "" && audit.blockBinding.Load() {
			audit.append(rows, "rejected_before_backend_http_503")
			http.Error(w, "controlled transient bootstrap CAS rejection", http.StatusServiceUnavailable)
			audit.signalFirstBlockedBinding()
			return
		}
		if bindingKey != "" {
			// Only measure the reconnect receive before this binding submission,
			// not later recovery-package or ordinary backup synchronization.
			audit.trackReceive.Store(false)
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		response := &bootstrapCASResponseRecorder{ResponseWriter: w}
		proxy.ServeHTTP(response, r)
		status := response.status
		if status == 0 {
			status = http.StatusOK
		}
		audit.append(rows, "forwarded_http_"+strconv.Itoa(status))
	}))
	t.Cleanup(server.Close)
	config.IndexerL2.Host = strings.TrimPrefix(server.URL, "http://")
	db := indexerdb.NewKVDB(t.TempDir())
	require.NotNil(t, db)
	manager := wallet.NewManager(config, db)
	manager.Start()
	t.Cleanup(func() {
		audit.blockBinding.Store(false)
		audit.blockReceive.Store(false)
		audit.trackReceive.Store(false)
		manager.StopDKVSSync()
		manager.Close()
		db.Close()
	})
	_, err = manager.RecoverAccountManagementFromRootMnemonic(context.Background(), mnemonic, accountReviewPassword)
	require.ErrorIs(t, err, wallet.ErrRootAccountNotFound,
		"an empty temporary node must authorize only this mnemonic's first-wallet import")

	walletID, err := manager.ImportWallet(mnemonic, accountReviewPassword)
	require.NoError(t, err)
	require.NoError(t, manager.InitializeAccountManagement(accountReviewPassword))
	// The successful pre-import query is the one root-existence confirmation.
	// Observe its local result without discovering the same account again.
	identity := manager.GetAccountManagementStatus()
	require.NotEmpty(t, identity.AccountID)
	require.Equal(t, walletID, identity.RootWalletID)
	require.Equal(t, "inactive", identity.DiscoveryStatus)
	require.True(t, identity.LocalOnly)
	require.False(t, identity.RecoveryConfigured)
	require.False(t, identity.ActivationPending)
	blocked, forwarded, unexpected, _ := audit.safeCounts()
	require.Equal(t, 1, blocked, "only the root import registers the service")
	require.Zero(t, forwarded)
	require.Zero(t, unexpected, "inactive discovery must not publish")
	grant, err := manager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
	require.NoError(t, err)
	require.NotNil(t, grant)
	backup, err := manager.ExportAccountBackupForPWA(accountReviewPassword, nil)
	require.NoError(t, err)
	pkg, err := manager.CreateAccountRecoveryPackage(account.CreateOptions{AccountID: manager.GetAccountManagementStatus().AccountID, Backup: backup, RecoveryMode: account.RecoveryMode2Of2, Questions: e2eKnowledgeQuestions()})
	require.NoError(t, err)
	share, err := account.RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle, e2eKnowledgeAnswers())
	require.NoError(t, err)
	_, secret, err := account.RecoverAccount(pkg.Envelope, pkg.UserShare, share)
	require.NoError(t, err)
	defer clearBytes(secret)
	activate := func() error {
		stage := "storage_authorization"
		err := manager.UseAccountStorageAuthorization(wallet.AccountStoragePurposeRecovery, func(auth *wallet.AccountStorageAuthorization) error {
			stage = "binding"
			repo, err := manager.NewAccountRepositoryForStorage(*auth)
			if err != nil {
				return fmt.Errorf("bind repository: %w", err)
			}
			stage = "recovery_package_publish"
			if err = account.NewManager(repo).Publish(context.Background(), *pkg); err != nil {
				return fmt.Errorf("publish recovery package: %w", err)
			}
			stage = "service_activation"
			if err = manager.ActivateAccountManagement(secret, accountReviewPassword, *auth, pkg.Envelope.Locator, "account://"+pkg.Envelope.Locator.PackageID); err != nil {
				return fmt.Errorf("activate account service: %w", err)
			}
			return nil
		})
		if err != nil {
			status := manager.GetAccountManagementStatus()
			t.Logf("activation failure stage=%s discovery=%s dkvs_status=%s activation_pending=%t recovery_configured=%t dkvs_write_allowed=%t",
				stage, status.DiscoveryStatus, status.DKVSStatus, status.ActivationPending, status.RecoveryConfigured, status.DKVSWriteAllowed)
		}
		return err
	}
	select {
	case <-audit.firstBlockedBinding:
	case <-time.After(20 * time.Second):
		t.Fatal("root import did not reach the real binding CAS")
	}
	require.False(t, manager.GetAccountManagementStatus().RecoveryConfigured)
	root := manager.GetWallet()
	require.NotNil(t, root)
	expectedKey, err := dkvs.AccountMappingKey(wallet.GetChainParam().Name, root.GetAddress())
	require.NoError(t, err)
	require.Equal(t, expectedKey, audit.getBindingKey())
	client := dkvsClientForNode(t, network.Core)
	_, err = client.GetRecord(expectedKey)
	require.ErrorIs(t, err, wallet.ErrDKVSRecordNotFound, "503 must not reach the backend")
	blocked, forwarded, unexpected, rows := audit.safeCounts()
	require.Equal(t, 1, blocked, "activation must never write the binding")
	require.Zero(t, forwarded)
	require.Zero(t, unexpected)
	for _, row := range rows {
		require.Equal(t, "core_binding", row.Class)
		require.Equal(t, uint64(1), row.Seq)
		require.Zero(t, row.TTL)
		require.Equal(t, "none", row.FeeMode)
		require.Equal(t, "rejected_before_backend_http_503", row.Result)
	}
	audit.blockBinding.Store(false)
	retryStatus := manager.GetAccountManagementStatus()
	require.Equal(t, identity.AccountID, retryStatus.AccountID)
	require.Equal(t, identity.RootWalletID, retryStatus.RootWalletID)
	require.True(t, retryStatus.LocalOnly)
	require.False(t, retryStatus.RecoveryConfigured)
	require.False(t, retryStatus.ActivationPending)
	require.NotEqual(t, "active", retryStatus.DiscoveryStatus)
	require.NotEqual(t, "active-waiting-backup", retryStatus.DiscoveryStatus)
	retryGrant, err := manager.PendingAccountStorageAuthorization()
	require.NoError(t, err)
	require.NotNil(t, retryGrant)
	require.Equal(t, grant.ID, retryGrant.ID, "retry must retain the original storage authorization")
	audit.trackReceive.Store(true)
	audit.blockReceive.Store(true)
	// The failed registration revoked write readiness. Keep receiving offline
	// for the first activation attempt; it must preserve the original grant.
	retryErr := activate()
	var responseErr *wallet.HTTPResponseError
	require.ErrorAs(t, retryErr, &responseErr, "retry must reach real online receiving, not reuse an offline grant")
	require.Equal(t, http.StatusServiceUnavailable, responseErr.StatusCode)
	require.Positive(t, audit.blockedReceives.Load())
	blockedAfter, forwardedAfter, unexpectedAfter, _ := audit.safeCounts()
	require.Equal(t, blocked, blockedAfter, "failed receiving must not submit another binding")
	require.Zero(t, forwardedAfter)
	require.Zero(t, unexpectedAfter, "failed receiving must not publish a recovery package")
	retryStatus = manager.GetAccountManagementStatus()
	require.Equal(t, identity.AccountID, retryStatus.AccountID)
	require.Equal(t, identity.RootWalletID, retryStatus.RootWalletID)
	require.False(t, retryStatus.RecoveryConfigured)
	require.False(t, retryStatus.ActivationPending)
	require.False(t, retryStatus.DKVSWriteAllowed)
	retryGrant, err = manager.PendingAccountStorageAuthorization()
	require.NoError(t, err)
	require.Equal(t, grant.ID, retryGrant.ID)
	require.NotEmpty(t, audit.takeReceives())
	audit.blockReceive.Store(false)
	// An obsolete grant or a different endpoint cannot even prepare the
	// binding. The original grant must remain available for its real retry.
	beforeRequests := requests.Load()
	beforePrefixes, err := manager.ListSubscribedDKVSPrefixes()
	require.NoError(t, err)
	obsolete := *grant
	obsolete.ID = ""
	_, err = manager.NewAccountRepositoryForStorage(obsolete)
	require.ErrorIs(t, err, wallet.ErrAccountStorageAuthorizationMissing)
	otherNode := *grant
	otherNode.Location.Host = "other.invalid"
	_, err = manager.NewAccountRepositoryForStorage(otherNode)
	require.ErrorIs(t, err, dkvs.ErrEndpointMismatch)
	afterPrefixes, err := manager.ListSubscribedDKVSPrefixes()
	require.NoError(t, err)
	require.Equal(t, beforePrefixes, afterPrefixes)
	require.Equal(t, beforeRequests, requests.Load())
	// The approved retry reopens the verified session but still cannot
	// publish a recovery package until root registration succeeds.
	require.ErrorIs(t, activate(), dkvs.ErrRecordNotFound)
	receives := audit.takeReceives()
	manager.NotifyNetworkOnline()
	require.Eventually(t, func() bool {
		record, err := client.GetRecord(expectedKey)
		return err == nil && record != nil
	}, 20*time.Second, 50*time.Millisecond, "online event must finish initial Core registration")
	require.NoError(t, activate())
	require.NoError(t, manager.StartDKVSSync())
	var committed *wire.DKVSRecord
	committed, err = client.GetRecord(expectedKey)
	require.NoError(t, err)
	require.NotNil(t, committed)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, manager.WaitAccountManagedDataReady(ctx))
	boundNetwork, boundAddress, descriptor, err := dkvs.ValidateAccountMappingBindingRecord(committed)
	require.NoError(t, err)
	parsed, err := dkvs.ParseKey(expectedKey)
	require.NoError(t, err)
	require.NotEmpty(t, parsed.Segments)
	require.Equal(t, parsed.Segments[0], boundNetwork)
	require.Equal(t, root.GetAddress(), boundAddress)
	remoteClient := dkvsClientForNode(t, network.Core)
	remoteConfig, err := remoteClient.GetDKVSClientConfig()
	require.NoError(t, err)
	require.Equal(t, remoteConfig.EndpointID, descriptor.CoreNodeID)
	accountKey, err := dkvs.AccountPersonalKey(identity.AccountID, "account/root")
	require.NoError(t, err)
	accountPrefix, err := dkvs.CollectionPathForKey(accountKey)
	require.NoError(t, err)
	bindingPrefix, err := dkvs.CollectionPathForKey(expectedKey)
	require.NoError(t, err)
	wanted := map[string]bool{accountPrefix: false, bindingPrefix: false}
	for _, request := range receives {
		require.Equal(t, remoteConfig.EndpointID, request.EndpointID, "reconnect must stay on the original node")
		require.Contains(t, wanted, request.Scope.Prefix, "reconnect must not add unrelated subscriptions")
		require.Empty(t, request.Scope.Keys)
		require.False(t, request.Full, "reconnect must reuse the verified generation baseline")
		wanted[request.Scope.Prefix] = true
	}
	for prefix, received := range wanted {
		require.True(t, received, "reconnect did not verify registered prefix %s", prefix)
	}
	require.Equal(t, uint64(1), committed.Seq)
	require.Zero(t, committed.TTL)
	require.Empty(t, committed.FeeProof)
	require.True(t, manager.GetAccountManagementStatus().RecoveryConfigured)
	require.NotEmpty(t, manager.GetAccountManagementStatus().PackageID)
	require.NotEmpty(t, manager.GetAccountManagementStatus().PublicLocator)
	require.Zero(t, manager.GetAccountManagementStatus().PendingChanges)
	require.False(t, manager.GetAccountManagementStatus().ManagedDataDirty)
	_, forwarded, _, rows = audit.safeCounts()
	require.Equal(t, 1, forwarded, "successful binding must be submitted only once")
	for _, row := range rows {
		require.Equal(t, uint64(1), row.Seq)
		require.Zero(t, row.TTL)
		require.Equal(t, "none", row.FeeMode)
	}

}

type bootstrapCASObservation struct {
	Class     string
	Namespace string
	Seq       uint64
	TTL       uint64
	FeeMode   string
	Result    string
}

type bootstrapCASResponseRecorder struct {
	http.ResponseWriter
	status int
}

func (w *bootstrapCASResponseRecorder) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *bootstrapCASResponseRecorder) Write(value []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(value)
}

func (w *bootstrapCASResponseRecorder) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *bootstrapCASResponseRecorder) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

type bootstrapCASAudit struct {
	blockBinding        atomic.Bool
	blockReceive        atomic.Bool
	trackReceive        atomic.Bool
	blockedReceives     atomic.Int32
	firstBlockedOnce    sync.Once
	firstBlockedBinding chan struct{}
	mu                  sync.Mutex
	bindingKey          string
	observations        []bootstrapCASObservation
	receives            []dkvs.ActiveSyncRequest
}

func newBootstrapCASAudit() *bootstrapCASAudit {
	audit := &bootstrapCASAudit{firstBlockedBinding: make(chan struct{})}
	audit.blockBinding.Store(true)
	return audit
}

func summarizeBootstrapCAS(batch dkvscore.BatchCASRequest) ([]bootstrapCASObservation, string, bool) {
	rows := make([]bootstrapCASObservation, 0, len(batch.Mutations))
	bindingKey := ""
	for _, mutation := range batch.Mutations {
		record := mutation.Record
		if record == nil {
			return nil, "", false
		}
		parsed, err := dkvs.ParseKey(record.Key)
		if err != nil {
			return nil, "", false
		}
		row := bootstrapCASObservation{Class: "other", Namespace: parsed.Namespace,
			Seq: record.Seq, TTL: record.TTL, FeeMode: "none"}
		if dkvs.IsAccountMappingBindingKey(record.Key) {
			row.Class = "core_binding"
			bindingKey = record.Key
		} else if parsed.Namespace == "personal" && len(parsed.Segments) >= 3 &&
			parsed.Segments[1] == "account" && parsed.Segments[2] == "recovery" {
			row.Class = "account_backup"
		} else if parsed.Namespace == "personal" && len(parsed.Segments) >= 3 &&
			parsed.Segments[1] == "account" {
			row.Class = "account_managed_state"
		} else if parsed.Namespace == "blob" && len(parsed.Segments) == 2 &&
			len(parsed.Segments[1]) == 64 {
			if _, err := hex.DecodeString(parsed.Segments[1]); err == nil {
				row.Class = "account_backup"
			}
		}
		if len(record.FeeProof) != 0 {
			proof, parseErr := dkvs.ParseFeeProof(record.FeeProof)
			if parseErr != nil {
				return nil, "", false
			}
			row.FeeMode = proof.Mode
		}
		rows = append(rows, row)
	}
	return rows, bindingKey, len(rows) != 0
}

func (a *bootstrapCASAudit) append(rows []bootstrapCASObservation, result string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, row := range rows {
		row.Result = result
		a.observations = append(a.observations, row)
	}
}

func (a *bootstrapCASAudit) recordReceive(request dkvs.ActiveSyncRequest) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.receives = append(a.receives, request)
}

func (a *bootstrapCASAudit) takeReceives() []dkvs.ActiveSyncRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	requests := append([]dkvs.ActiveSyncRequest(nil), a.receives...)
	a.receives = nil
	return requests
}

func (a *bootstrapCASAudit) setBindingKey(key string) {
	a.mu.Lock()
	a.bindingKey = key
	a.mu.Unlock()
}

func (a *bootstrapCASAudit) getBindingKey() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.bindingKey
}

func (a *bootstrapCASAudit) signalFirstBlockedBinding() {
	a.firstBlockedOnce.Do(func() { close(a.firstBlockedBinding) })
}

func (a *bootstrapCASAudit) safeCounts() (blocked, forwarded, unexpected int, bindings []bootstrapCASObservation) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, row := range a.observations {
		switch {
		case row.Class == "core_binding" && row.Result == "rejected_before_backend_http_503":
			blocked++
			bindings = append(bindings, row)
		case row.Class == "core_binding" && strings.HasPrefix(row.Result, "forwarded_http_"):
			if row.Result == "forwarded_http_200" {
				forwarded++
			}
			bindings = append(bindings, row)
		case row.Class != "core_binding":
			unexpected++
		}
	}
	return blocked, forwarded, unexpected, bindings
}

func (a *bootstrapCASAudit) hasSuccessfulBindingForward() bool {
	_, forwarded, _, _ := a.safeCounts()
	return forwarded > 0
}
