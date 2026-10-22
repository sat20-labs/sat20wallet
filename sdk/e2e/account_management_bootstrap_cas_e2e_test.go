package e2e

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// This is an SDK/bootstrap diagnostic, not a browser or PWA acceptance case.
// It holds only the first-writer bootstrap boundary: the real SDK request is
// decoded in memory, classified without retaining values/signatures, rejected
// with a transient 503 before reaching the node, then allowed through after
// direct remote absence and local readiness state have been checked.
func TestSDKAccountBootstrapBindingCASTransientFailureRetries_NotUI(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)

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

	mnemonic := accountSyncMnemonic(t, 249)
	_, err = manager.RecoverAccountManagementFromRootMnemonic(context.Background(), mnemonic, accountReviewPassword)
	require.ErrorIs(t, err, wallet.ErrRootAccountNotFound,
		"an empty temporary node must authorize only this mnemonic's first-wallet import")

	importDone := make(chan error, 1)
	go func() {
		_, importErr := manager.ImportWallet(mnemonic, accountReviewPassword)
		importDone <- importErr
	}()
	importFinished := false
	select {
	case <-audit.firstBlockedBinding:
	case importErr := <-importDone:
		require.NoError(t, importErr)
		importFinished = true
		select {
		case <-audit.firstBlockedBinding:
		case <-time.After(20 * time.Second):
			t.Fatal("SDK bootstrap did not submit its account binding CAS")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("SDK bootstrap did not submit its account binding CAS")
	}
	if !importFinished {
		select {
		case importErr := <-importDone:
			require.NoError(t, importErr)
		case <-time.After(20 * time.Second):
			t.Fatal("wallet import did not finish while the bootstrap CAS was rejected")
		}
	}

	status := manager.GetAccountManagementStatus()
	require.True(t, status.Active, "first-wallet import may activate local account management")
	require.False(t, status.RecoveryConfigured, "a local active profile is not remote recovery readiness")
	require.Empty(t, status.PackageID)
	require.Empty(t, status.PublicLocator)
	require.Equal(t, wallet.AccountStorageTemporary, status.StorageMode)
	require.Eventually(t, func() bool {
		current := manager.GetAccountManagementStatus()
		return current.LastDKVSSyncError != "" && current.LastDKVSSyncErrorCode != "" && current.LastDKVSSyncErrorAt > 0
	}, 5*time.Second, 50*time.Millisecond, "the background sync status must report the controlled transport failure")

	root := manager.GetWallet()
	require.NotNil(t, root)
	expectedBindingKey, err := dkvs.AccountMappingKey(wallet.GetChainParam().Name, root.GetAddress())
	require.NoError(t, err)
	observedBindingKey := audit.getBindingKey()
	require.True(t, observedBindingKey != "" && observedBindingKey == expectedBindingKey,
		"the blocked CAS must be the selected root's canonical account mapping")
	blockedRecord, readErr := dkvsClientForNode(t, network.Core).GetRecord(observedBindingKey)
	require.ErrorIs(t, readErr, wallet.ErrDKVSRecordNotFound,
		"a controlled 503 must not reach or commit at the CoreNode")
	require.Nil(t, blockedRecord)
	blocked, forwarded, unexpected, bindingRows := audit.safeCounts()
	require.GreaterOrEqual(t, blocked, 1)
	require.Zero(t, forwarded, "no binding request may reach the backend while the failure gate is closed")
	require.Zero(t, unexpected, "bootstrap-only synchronization must not submit any non-binding CAS")
	require.NotEmpty(t, bindingRows)
	for _, row := range bindingRows {
		require.Equal(t, "core_binding", row.Class)
		require.Equal(t, "account", row.Namespace)
		require.Equal(t, uint64(1), row.Seq)
		require.Equal(t, uint64(0), row.TTL)
		require.Equal(t, "none", row.FeeMode)
		require.Equal(t, "rejected_before_backend_http_503", row.Result)
	}
	t.Logf("bootstrap CAS held: class=core_binding namespace=account attempts=%d backend_binding=0 non_binding=0 seq=1 ttl=0 fee_mode=none local_recovery_configured=false", len(bindingRows))

	// Only the normal worker retries this original domain job; the test never
	// calls SyncAccountManagementState to manufacture a successful result.
	audit.blockBinding.Store(false)
	client := dkvsClientForNode(t, network.Core)
	var committed *wire.DKVSRecord
	require.Eventually(t, func() bool {
		committed, err = client.GetRecord(observedBindingKey)
		return err == nil && committed != nil
	}, 30*time.Second, 100*time.Millisecond, "the existing background retry must commit the actual binding")
	require.Eventually(t, func() bool {
		return audit.hasSuccessfulBindingForward()
	}, 5*time.Second, 50*time.Millisecond, "the retry must traverse the real reverse-proxy backend")
	boundNetwork, boundAddress, descriptor, err := dkvs.ValidateAccountMappingBindingRecord(committed)
	require.NoError(t, err, "the committed mapping must have a valid account-service signature")
	parsedBinding, err := dkvs.ParseKey(expectedBindingKey)
	require.NoError(t, err)
	require.True(t, len(parsedBinding.Segments) > 0 && boundNetwork == parsedBinding.Segments[0],
		"the signed mapping must use the configured network namespace")
	require.True(t, boundAddress == root.GetAddress(), "the committed mapping must target the imported root")
	remoteClient, err := manager.GetDKVSClient()
	require.NoError(t, err)
	remoteConfig, err := remoteClient.GetDKVSClientConfig()
	require.NoError(t, err)
	require.NotNil(t, remoteConfig)
	require.True(t, descriptor.CoreNodeID == remoteConfig.EndpointID,
		"the binding must identify the selected CoreNode")
	require.Equal(t, uint64(1), committed.Seq)
	require.Zero(t, committed.TTL)
	require.Empty(t, committed.FeeProof)

	status = manager.GetAccountManagementStatus()
	require.False(t, status.RecoveryConfigured,
		"a successful root-to-CoreNode binding still does not configure remote account recovery")
	require.Empty(t, status.PackageID)
	require.Empty(t, status.PublicLocator)
	blocked, forwarded, unexpected, bindingRows = audit.safeCounts()
	require.GreaterOrEqual(t, blocked, 1)
	require.GreaterOrEqual(t, forwarded, 1)
	require.Zero(t, unexpected, "bootstrap-only synchronization must not submit any non-binding CAS")
	for _, row := range bindingRows {
		if strings.HasPrefix(row.Result, "forwarded_http_") {
			require.Equal(t, "forwarded_http_200", row.Result)
			require.Equal(t, "core_binding", row.Class)
			require.Equal(t, "account", row.Namespace)
			require.Equal(t, uint64(1), row.Seq)
			require.Equal(t, uint64(0), row.TTL)
			require.Equal(t, "none", row.FeeMode)
		}
	}
	t.Logf("bootstrap CAS retry: class=core_binding namespace=account backend_binding=%d non_binding=0 seq=%d ttl=%d fee_mode=none recovery_configured=false",
		forwarded, committed.Seq, committed.TTL)
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
			parsed.Segments[1] == "account-managed-data" {
			row.Class = "account_backup"
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
