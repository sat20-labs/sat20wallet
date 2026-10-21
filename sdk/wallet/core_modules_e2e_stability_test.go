package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// Metrics describe business phases only, after the caller's shared fixtures.
// Failed cases retain their observations; these are baselines, not speed gates.
func coreLogE2EMetrics(t *testing.T, scenario string, samples *[]map[string]any) {
	t.Helper()
	t.Cleanup(func() {
		status := "pass"
		if t.Failed() {
			status = "fail"
		}
		for _, sample := range *samples {
			sample["case"] = scenario
			sample["status"] = status
			sample["environment"] = map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH, "cpu_count": runtime.NumCPU(), "go_version": runtime.Version(), "network": "testnet", "isolated_fixture": true, "fixture_kind": "temporary SatoshiNet and controlled Bitcoin evidence", "workers": 1}
			raw, err := json.Marshal(sample)
			if err != nil {
				t.Errorf("encode E2E metric: %v", err)
				continue
			}
			t.Logf("core-e2e-metric: %s", raw)
		}
	})
}

func coreE2EDuration(phase string, started time.Time) map[string]any {
	return map[string]any{"phase": phase, "duration_ms": float64(time.Since(started).Microseconds()) / 1000}
}

// This wrapper deliberately embeds only HttpClient. Promoted context methods
// would let the SDK bypass the fault at its higher-priority transport branch.
type coreCommittedACKLossHTTP struct {
	HttpClient
	mu                                 sync.Mutex
	get, post, batch, successful, lost int
	afterCommit                        func()
}

func (h *coreCommittedACKLossHTTP) SendGetRequest(url *URL) ([]byte, error) {
	h.mu.Lock()
	h.get++
	h.mu.Unlock()
	return h.HttpClient.SendGetRequest(url)
}
func (h *coreCommittedACKLossHTTP) SendPostRequest(url *URL, body []byte) ([]byte, error) {
	batch := strings.HasSuffix(url.Path, "/v3/dkvs/records/batch-cas")
	h.mu.Lock()
	h.post++
	if batch {
		h.batch++
	}
	h.mu.Unlock()
	raw, err := h.HttpClient.SendPostRequest(url, body)
	var result DKVSBatchCASResult
	if !batch || err != nil || decodeDKVSApplicationResponse(raw, &result) != nil {
		return raw, err
	}
	h.mu.Lock()
	h.successful++
	drop := h.lost == 0
	if drop {
		h.lost++
	}
	h.mu.Unlock()
	if drop {
		h.afterCommit()
		return nil, &HTTPResponseError{StatusCode: 503, Body: []byte("core E2E committed CAS ACK intentionally lost")}
	}
	return raw, nil
}
func (h *coreCommittedACKLossHTTP) counts() map[string]int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return map[string]int{"get": h.get, "post": h.post, "batch_cas": h.batch, "successful_cas_responses": h.successful, "lost_ack": h.lost}
}

type coreRecordVersion struct {
	Seq  uint64
	Hash string
}

// Independent direct reads verify signatures and ETags, rather than trusting
// the intercepted HTTP response or the sender's optimistic local view.
func coreCommittedRecords(t *testing.T, source *Manager, cfg coreE2EConfig) (map[string]coreRecordVersion, account.ManagedState) {
	t.Helper()
	root, err := source.accountManagementRootWallet()
	require.NoError(t, err)
	stateKey, err := source.accountManagedStateKey(root)
	require.NoError(t, err)
	blobKey, err := source.accountManagedDataBlobKey(root)
	require.NoError(t, err)
	wrapperKey, err := accountRootWrapperKey(root)
	require.NoError(t, err)
	client := NewSatsNetDKVSClient(cfg.Core.Scheme, cfg.Core.Host, cfg.Core.Proxy, nil)
	versions := map[string]coreRecordVersion{}
	var state account.ManagedState
	for _, key := range []string{stateKey, blobKey, wrapperKey} {
		record, err := client.GetRecordDirect(key)
		require.NoError(t, err)
		versions[key] = coreRecordVersion{Seq: record.Seq, Hash: dkvsindexer.RecordHash(record).String()}
		if key == stateKey {
			state, err = account.OpenManagedState(source.accountSecret, source.GetAccountManagementStatus().AccountID, record.Value)
			require.NoError(t, err)
		}
	}
	return versions, state
}

func coreCommittedCASLostACKE2E(t *testing.T, sender *Manager, cfg coreE2EConfig, chain *coreE2EChain, material *coreRecoveryMaterial) {
	t.Helper()
	var samples []map[string]any
	coreLogE2EMetrics(t, "account_sync_committed_lost_ack", &samples)
	before, stateBefore := coreCommittedRecords(t, sender, cfg)
	broadcasts := chain.broadcastCount()
	store, err := sender.accountDKVSStore()
	require.NoError(t, err)
	original := store.client.Http
	observer := &coreCommittedACKLossHTTP{HttpClient: original}
	store.client.Http = observer
	defer func() { store.client.Http = original }()
	var committed map[string]coreRecordVersion
	var stateCommitted account.ManagedState
	changedName := "Committed once despite lost ACK"
	started := time.Now()
	observer.afterCommit = func() {
		committed, stateCommitted = coreCommittedRecords(t, sender, cfg)
		require.NotEqual(t, before, committed, "fault must follow a genuine remote commit")
		require.Equal(t, stateBefore.Revision+1, stateCommitted.Revision)
		found := false
		for _, entry := range stateCommitted.Wallets {
			if entry.Name == changedName {
				found = true
			}
		}
		require.True(t, found, "lost ACK must belong to the actual name mutation")
		samples = append(samples, coreE2EDuration("edit_to_observed_commit", started))
	}
	rootID := sender.GetAccountManagementStatus().RootWalletID
	require.NoError(t, sender.UpdateWalletName(rootID, changedName))
	err = sender.SyncAccountManagementState(context.Background())
	sample := coreE2EDuration("edit_to_first_caller_return", started)
	sample["counts"] = observer.counts()
	samples = append(samples, sample)
	require.Equal(t, 1, observer.counts()["lost_ack"], "post-commit fault was not reached")
	if err != nil {
		// Lost acknowledgements may surface directly or leave a pending outbox.
		require.True(t, strings.Contains(err.Error(), "ACK intentionally lost") || errors.Is(err, ErrDKVSPathNotSynced), "unexpected sync failure: %v", err)
	}
	retryStarted := time.Now()
	// Keep counting traffic while the one-shot fault has already been consumed.
	coreReplayManagedOutbox(t, sender)
	samples = append(samples, coreE2EDuration("explicit_retry_to_convergence", retryStarted))
	final, stateFinal := coreCommittedRecords(t, sender, cfg)
	require.Equal(t, committed, final, "retry must reconcile the original commit without republishing")
	require.Equal(t, stateCommitted.Revision, stateFinal.Revision)
	require.Zero(t, sender.GetAccountManagementStatus().PendingChanges)
	require.False(t, sender.GetAccountManagementStatus().ManagedDataDirty)
	entries, err := newDKVSReplicaStore(sender.db).LoadOutbox(store.client.replicaNamespace)
	require.NoError(t, err)
	require.Empty(t, entries)
	cold := coreRestoreE2E(t, cfg, chain, material)
	require.Equal(t, coreCanonicalCatalog(sender), coreCanonicalCatalog(cold))
	catalogBefore := coreCanonicalCatalog(sender)
	countsBefore := observer.counts()
	warm := make([]float64, 0, 10)
	for i := 0; i < 10; i++ {
		started := time.Now()
		err := sender.SyncAccountManagementState(context.Background())
		sample := coreE2EDuration("warm_noop_sync", started)
		sample["sample_index"] = i
		sample["temperature"] = "warm"
		samples = append(samples, sample)
		warm = append(warm, sample["duration_ms"].(float64))
		require.NoError(t, err)
	}
	countsAfter := observer.counts()
	require.Equal(t, countsBefore["batch_cas"], countsAfter["batch_cas"], "idle sync must not write batches")
	require.Equal(t, broadcasts, chain.broadcastCount(), "sync/recovery must not broadcast")
	require.Equal(t, catalogBefore, coreCanonicalCatalog(sender))
	afterWarm, _ := coreCommittedRecords(t, sender, cfg)
	require.Equal(t, final, afterWarm)
	deltas := map[string]int{}
	for key, value := range countsAfter {
		deltas[key] = value - countsBefore[key]
	}
	deltas["broadcast"] = chain.broadcastCount() - broadcasts
	sort.Float64s(warm)
	samples = append(samples, map[string]any{"phase": "warm_noop_sync_summary", "samples": 10, "unit": "ms", "summary": map[string]float64{"min": warm[0], "median": (warm[4] + warm[5]) / 2, "max": warm[9]}, "counts": deltas})
	countsAfter["distinct_committed_states"] = 1
	samples = append(samples, map[string]any{"phase": "scenario_counts", "counts": countsAfter, "workload": map[string]int{"wallets": len(catalogBefore), "operations": 1}})
}
