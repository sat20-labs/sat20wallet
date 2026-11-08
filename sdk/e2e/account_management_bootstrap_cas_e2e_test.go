package e2e

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// Initial activation uses the real PWA recovery workflow. A controlled 503
// blocks its binding, then ordinary retry/re-entry completes service activation
// and the independent private backup. Import itself performs no remote CAS.
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	t.Cleanup(func() {
		audit.blockBinding.Store(false)
		manager.StopDKVSSync()
		manager.Close()
		db.Close()
	})
	require.NoError(t, manager.StartDKVSSync())

	_, err = manager.RecoverAccountManagementFromRootMnemonic(context.Background(), mnemonic, accountReviewPassword)
	require.ErrorIs(t, err, wallet.ErrRootAccountNotFound,
		"an empty temporary node must authorize only this mnemonic's first-wallet import")

	_, err = manager.ImportWallet(mnemonic, accountReviewPassword)
	require.NoError(t, err)
	require.NoError(t, manager.InitializeAccountManagement(accountReviewPassword))
	require.Eventually(t, func() bool { return manager.GetAccountManagementStatus().LocalOnly }, 20*time.Second, 20*time.Millisecond)
	blocked, forwarded, unexpected, _ := audit.safeCounts()
	require.Zero(t, blocked)
	require.Zero(t, forwarded)
	require.Zero(t, unexpected, "inactive discovery must not publish")
	_, err = manager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
	require.NoError(t, err)
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
	require.Error(t, activate(), "the explicit activation must expose the controlled binding failure")
	select {
	case <-audit.firstBlockedBinding:
	case <-time.After(20 * time.Second):
		t.Fatal("activation did not reach the real binding CAS")
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
	require.Positive(t, blocked)
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
	// The 503 invalidated the online session before service activation began.
	// Re-enter the real discovery path and wait for verified inactive status;
	// only preparation is retried, never package publication or activation.
	retryIdentity := manager.GetAccountManagementStatus()
	retryCtx, retryCancel := context.WithTimeout(context.Background(), 30*time.Second)
	attempts := 0
	retryErr := func() error {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			if err := retryCtx.Err(); err != nil {
				return err
			}
			attempts++
			_, err := manager.RecoverAccountManagementFromCurrentWallet(retryCtx, accountReviewPassword)
			status := manager.GetAccountManagementStatus()
			if status.AccountID != retryIdentity.AccountID || status.RootWalletID != retryIdentity.RootWalletID {
				return fmt.Errorf("activation preparation changed account identity")
			}
			if status.RecoveryConfigured || status.ActivationPending ||
				status.DiscoveryStatus == "active" || status.DiscoveryStatus == "active-waiting-backup" {
				return fmt.Errorf("activation preparation unexpectedly found an active or pending service")
			}
			if errors.Is(err, wallet.ErrRootAccountNotFound) && status.DiscoveryStatus == "inactive" && status.LocalOnly {
				return nil
			}
			if !errors.Is(err, wallet.ErrRootAccountNotFound) &&
				!errors.Is(err, wallet.ErrRootAccountDiscoveryPending) && !errors.Is(err, wallet.ErrDKVSPathNotSynced) {
				if err == nil {
					return fmt.Errorf("activation preparation unexpectedly completed account recovery")
				}
				return fmt.Errorf("activation discovery preparation: %w", err)
			}
			select {
			case <-retryCtx.Done():
				return retryCtx.Err()
			case <-ticker.C:
			}
		}
	}()
	retryCancel()
	retryStatus := manager.GetAccountManagementStatus()
	t.Logf("activation retry preparation attempts=%d discovery=%s dkvs_status=%s activation_pending=%t recovery_configured=%t local_only=%t error_type=%T",
		attempts, retryStatus.DiscoveryStatus, retryStatus.DKVSStatus, retryStatus.ActivationPending, retryStatus.RecoveryConfigured, retryStatus.LocalOnly, retryErr)
	require.NoError(t, retryErr)
	require.Equal(t, "inactive", retryStatus.DiscoveryStatus)
	require.True(t, retryStatus.LocalOnly)
	require.False(t, retryStatus.RecoveryConfigured)
	require.False(t, retryStatus.ActivationPending)
	// The raw binding has no outbox. Resume its unfinished explicit workflow
	// once, after the real online discovery baseline is usable again.
	require.NoError(t, activate())
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
	remoteClient, err := manager.GetDKVSClient()
	require.NoError(t, err)
	remoteConfig, err := remoteClient.GetDKVSClientConfig()
	require.NoError(t, err)
	require.Equal(t, remoteConfig.EndpointID, descriptor.CoreNodeID)
	require.Equal(t, uint64(1), committed.Seq)
	require.Zero(t, committed.TTL)
	require.Empty(t, committed.FeeProof)
	require.True(t, manager.GetAccountManagementStatus().RecoveryConfigured)
	require.NotEmpty(t, manager.GetAccountManagementStatus().PackageID)
	require.NotEmpty(t, manager.GetAccountManagementStatus().PublicLocator)
	require.Zero(t, manager.GetAccountManagementStatus().PendingChanges)
	require.False(t, manager.GetAccountManagementStatus().ManagedDataDirty)

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
	firstBlockedOnce    sync.Once
	firstBlockedBinding chan struct{}
	mu                  sync.Mutex
	bindingKey          string
	observations        []bootstrapCASObservation
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
