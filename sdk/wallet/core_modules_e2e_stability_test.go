package wallet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
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

// Install before the selected sender starts. Both HTTP entry points use the
// same observer, retaining cancellation without a promoted-method bypass.
type coreCommittedACKLossHTTP struct {
	HttpClient
	mu                                 sync.Mutex
	get, post, batch, successful, lost int
	hold                               *coreACKHold
	requestIDs, stateHashes            map[string]bool
	auditPhase                         string
	auditRows                          []coreCASAudit
	auditVersions                      map[string]coreRecordVersion
}

type coreACKCommit struct {
	requestID string
	versions  map[string]coreRecordVersion
	err       error
}

type coreACKHold struct {
	endpoint, stateKey string
	keys               map[string]bool
	before             coreRecordVersion
	entered            chan coreACKCommit
	release, finished  chan struct{}
	once               sync.Once
	chosen             bool
}

func (h *coreACKHold) unblock() { h.once.Do(func() { close(h.release) }) }

func (h *coreCommittedACKLossHTTP) arm(endpoint, stateKey string,
	keys map[string]bool, before coreRecordVersion, baseline map[string]coreRecordVersion) *coreACKHold {
	hold := &coreACKHold{endpoint: endpoint, stateKey: stateKey, keys: keys, before: before,
		entered: make(chan coreACKCommit, 1), release: make(chan struct{}), finished: make(chan struct{})}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.get, h.post, h.batch, h.successful, h.lost = 0, 0, 0, 0, 0
	h.requestIDs, h.stateHashes = make(map[string]bool), make(map[string]bool)
	h.hold = hold
	h.auditPhase, h.auditRows = "mutation", nil
	h.auditVersions = make(map[string]coreRecordVersion, len(baseline))
	for key, version := range baseline {
		h.auditVersions[key] = version
	}
	return hold
}

func (h *coreCommittedACKLossHTTP) disarm(hold *coreACKHold) {
	h.mu.Lock()
	if h.hold == hold {
		h.hold = nil
	}
	chosen := hold.chosen
	h.mu.Unlock()
	hold.unblock()
	if chosen {
		<-hold.finished
	}
}

func (h *coreCommittedACKLossHTTP) SendGetRequest(url *URL) ([]byte, error) {
	return h.SendGetRequestContext(context.Background(), url)
}
func (h *coreCommittedACKLossHTTP) SendGetRequestContext(ctx context.Context, url *URL) ([]byte, error) {
	h.mu.Lock()
	if h.hold != nil {
		h.get++
	}
	h.mu.Unlock()
	if client, ok := h.HttpClient.(ContextHttpClient); ok {
		return client.SendGetRequestContext(ctx, url)
	}
	return h.HttpClient.SendGetRequest(url)
}
func (h *coreCommittedACKLossHTTP) SendPostRequest(url *URL, body []byte) ([]byte, error) {
	return h.SendPostRequestContext(context.Background(), url, body)
}
func (h *coreCommittedACKLossHTTP) SendPostRequestContext(ctx context.Context, url *URL, body []byte) ([]byte, error) {
	batch := strings.HasSuffix(url.Path, "/v3/dkvs/records/batch-cas")
	h.mu.Lock()
	hold := h.hold
	phase, ordinal := h.auditPhase, 0
	if hold != nil {
		h.post++
		if batch {
			h.batch++
			ordinal = h.batch
		}
	}
	h.mu.Unlock()
	var request DKVSBatchCASRequest
	stateRequest, eligible := false, false
	decoded := batch && hold != nil && json.Unmarshal(body, &request) == nil
	if decoded && request.EndpointID == hold.endpoint {
		allowed := request.RequestID != "" && request.Authorization != nil
		seen := make(map[string]bool)
		for _, item := range request.Mutations {
			if item.Record == nil {
				allowed = false
				continue
			}
			key := item.Record.Key
			allowed = allowed && hold.keys[key] && !seen[key]
			seen[key] = true
			if key == hold.stateKey {
				stateRequest = true
				eligible = !item.ExpectAbsent && item.ExpectedETag == hold.before.Hash &&
					item.Record.Seq == hold.before.Seq+1 && dkvsindexer.RecordHash(item.Record).String() != hold.before.Hash
			}
		}
		eligible = eligible && allowed
	}
	if stateRequest {
		h.mu.Lock()
		if h.hold == hold {
			h.requestIDs[request.RequestID] = true
		}
		h.mu.Unlock()
	}
	slot := -1
	if batch && hold != nil {
		slot = h.startCAS(ordinal, phase, request, decoded, hold.endpoint)
	}
	var raw []byte
	var err error
	if client, ok := h.HttpClient.(ContextHttpClient); ok {
		raw, err = client.SendPostRequestContext(ctx, url, body)
	} else {
		raw, err = h.HttpClient.SendPostRequest(url, body)
	}
	if slot >= 0 {
		h.finishCAS(slot, hold, request, decoded, raw, err)
	}
	var reply dkvsWriteResultData
	if !batch || err != nil || decodeDKVSApplicationResponse(raw, &reply) != nil {
		return raw, err
	}
	h.mu.Lock()
	if h.hold == hold && hold != nil {
		h.successful++
	}
	h.mu.Unlock()
	if !stateRequest {
		return raw, nil
	}
	observed := coreACKCheckReply(request, reply, hold.endpoint)
	h.mu.Lock()
	if h.hold != hold {
		h.mu.Unlock()
		return raw, nil
	}
	if observed.err == nil {
		if state, ok := observed.versions[hold.stateKey]; ok {
			h.stateHashes[state.Hash] = true
		}
	}
	if !eligible || hold.chosen {
		h.mu.Unlock()
		return raw, nil
	}
	hold.chosen = true
	h.mu.Unlock()
	defer close(hold.finished)
	hold.entered <- observed
	select {
	case <-hold.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if observed.err != nil {
		return raw, nil
	}
	h.mu.Lock()
	h.lost++ // Count only the branch that actually returns the injected error.
	h.mu.Unlock()
	return nil, &HTTPResponseError{StatusCode: 503, Body: []byte("core E2E committed CAS ACK intentionally lost")}
}

func coreACKCheckReply(request DKVSBatchCASRequest, reply dkvsWriteResultData, endpoint string) coreACKCommit {
	observed := coreACKCommit{requestID: request.RequestID}
	if reply.Data == nil || reply.Data.LocalOnly || reply.Data.EndpointID != endpoint || reply.Data.RequestID != request.RequestID {
		observed.err = errors.New("target CAS response identity mismatch")
		return observed
	}
	mutations := make([]dkvsindexer.CASMutation, len(request.Mutations))
	for index, item := range request.Mutations {
		if item.Record == nil {
			observed.err = errors.New("target CAS request record missing")
			return observed
		}
		mutations[index].Record = item.Record
	}
	if dkvscore.VerifyWriteResult(mutations, request.RequestID, reply.Data) != nil {
		observed.err = errors.New("target CAS response record verification failed")
		return observed
	}
	observed.versions = make(map[string]coreRecordVersion, len(reply.Data.Records))
	for _, record := range reply.Data.Records {
		observed.versions[record.Key] = coreRecordVersion{Seq: record.Seq, Hash: dkvsindexer.RecordHash(record).String()}
	}
	return observed
}

// In-memory diagnostic rows contain no raw keys, IDs, record values or errors.
type coreCASKeyAudit struct {
	KeyTag        string `json:"key_tag"`
	Role          string `json:"role"`
	BeforeKnown   bool   `json:"before_known"`
	BeforeSeq     uint64 `json:"before_seq"`
	RequestSeq    uint64 `json:"request_seq"`
	ReplySeq      uint64 `json:"reply_seq"`
	RequestSame   bool   `json:"request_same_as_expected"`
	SeqChanged    bool   `json:"seq_changed"`
	RecordChanged bool   `json:"record_changed"`
}
type coreCASAudit struct {
	Batch         int               `json:"batch"`
	Phase         string            `json:"phase"`
	RequestTag    string            `json:"request_tag"`
	EndpointTag   string            `json:"endpoint_tag"`
	EndpointMatch bool              `json:"endpoint_match"`
	Decoded       bool              `json:"decoded"`
	TransportOK   bool              `json:"transport_ok"`
	Completed     bool              `json:"completed"`
	Verified      bool              `json:"verified"`
	Keys          []coreCASKeyAudit `json:"keys"`
}

func coreCASTag(value string) string {
	if value == "" {
		return "none"
	}
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:6])
}
func coreCASKeyRole(key string) string {
	parsed, err := dkvsindexer.ParseKey(key)
	if err != nil {
		return "invalid"
	}
	path := ""
	if len(parsed.Segments) > 1 {
		path = strings.Join(parsed.Segments[1:], "/")
	}
	module := false
	if parsed.Namespace == "blob" && len(parsed.Segments) == 2 && len(path) == sha256.Size*2 {
		_, err := hex.DecodeString(path)
		module = err == nil
	}
	switch {
	case parsed.Namespace == "account":
		return "public-binding"
	case parsed.Namespace == "personal" && path == accountManagedStatePath:
		return "state"
	case parsed.Namespace == "personal" && path == "account/index":
		return "account-index"
	case parsed.Namespace == "personal" && path == accountRootWrapperPath:
		return "root-wrapper"
	case parsed.Namespace == "personal" && strings.HasPrefix(path, "account/recovery/"):
		return "recovery-package"
	case module:
		return "managed-module"
	case parsed.Namespace == "mail":
		return "mailbox"
	case parsed.Namespace == "svc":
		return "service"
	default:
		return "other"
	}
}
func (h *coreCommittedACKLossHTTP) setAuditPhase(phase string) {
	h.mu.Lock()
	h.auditPhase = phase
	h.mu.Unlock()
}
func (h *coreCommittedACKLossHTTP) audit() []coreCASAudit {
	h.mu.Lock()
	defer h.mu.Unlock()
	rows := append([]coreCASAudit(nil), h.auditRows...)
	for i := range rows {
		rows[i].Keys = append([]coreCASKeyAudit(nil), rows[i].Keys...)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Batch < rows[j].Batch })
	return rows
}
func (h *coreCommittedACKLossHTTP) startCAS(ordinal int, phase string,
	request DKVSBatchCASRequest, decoded bool, endpoint string) int {
	row := coreCASAudit{Batch: ordinal, Phase: phase, RequestTag: coreCASTag(request.RequestID),
		EndpointTag: coreCASTag(request.EndpointID), EndpointMatch: request.EndpointID == endpoint, Decoded: decoded}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, item := range request.Mutations {
		if item.Record == nil {
			row.Keys = append(row.Keys, coreCASKeyAudit{Role: "missing-record"})
			continue
		}
		key := item.Record.Key
		before, known := h.auditVersions[key]
		known = known && item.ExpectedETag != "" && before.Hash == item.ExpectedETag
		requestedHash := dkvsindexer.RecordHash(item.Record).String()
		same := item.ExpectedETag != "" && requestedHash == item.ExpectedETag
		// An unchanged binding carries its exact precondition record, so the
		// previous sequence is provable even on its first observation.
		if !known && same {
			before = coreRecordVersion{Seq: item.Record.Seq, Hash: requestedHash}
			known = true
		}
		entry := coreCASKeyAudit{KeyTag: coreCASTag(key), Role: coreCASKeyRole(key), BeforeKnown: known,
			RequestSeq: item.Record.Seq, RequestSame: same}
		if known {
			entry.BeforeSeq = before.Seq
		}
		row.Keys = append(row.Keys, entry)
	}
	slot := len(h.auditRows)
	h.auditRows = append(h.auditRows, row)
	return slot
}
func (h *coreCommittedACKLossHTTP) finishCAS(slot int, hold *coreACKHold,
	request DKVSBatchCASRequest, decoded bool, raw []byte, transportErr error) {
	var reply dkvsWriteResultData
	var observed coreACKCommit
	verified := false
	if decoded && transportErr == nil && decodeDKVSApplicationResponse(raw, &reply) == nil {
		observed = coreACKCheckReply(request, reply, hold.endpoint)
		verified = observed.err == nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	row := &h.auditRows[slot]
	row.Completed, row.TransportOK, row.Verified = true, transportErr == nil, verified
	if !verified {
		return
	}
	for index, item := range request.Mutations {
		if item.Record == nil {
			continue
		}
		key := item.Record.Key
		if after, ok := observed.versions[key]; ok {
			entry := &row.Keys[index]
			entry.ReplySeq = after.Seq
			entry.SeqChanged = entry.BeforeKnown && entry.BeforeSeq != after.Seq
			entry.RecordChanged = entry.BeforeKnown && !entry.RequestSame
			if prior, exists := h.auditVersions[key]; !exists || after.Seq >= prior.Seq {
				h.auditVersions[key] = after
			}
		}
	}
}

func (h *coreCommittedACKLossHTTP) counts() map[string]int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return map[string]int{"get": h.get, "post": h.post, "batch_cas": h.batch,
		"successful_cas_responses": h.successful, "lost_ack": h.lost,
		"distinct_target_requests": len(h.requestIDs), "committed_state_versions": len(h.stateHashes)}
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
	accountID, err := dkvsAccountID(root)
	require.NoError(t, err)
	indexKey, err := account.ManagedIndexKey(accountID)
	require.NoError(t, err)
	wrapperKey, err := accountRootWrapperKey(root)
	require.NoError(t, err)
	source.mutex.RLock()
	secret := append([]byte(nil), source.accountSecret...)
	network := source.cfg.Chain
	source.mutex.RUnlock()
	defer zeroBytes(secret)
	client := NewSatsNetDKVSClient(cfg.Core.Scheme, cfg.Core.Host, cfg.Core.Proxy, nil)
	indexRecord, err := client.GetRecordDirect(indexKey)
	require.NoError(t, err)
	require.NotNil(t, indexRecord)
	require.NoError(t, dkvsindexer.VerifySignature(indexRecord))
	index, err := account.OpenManagedIndex(secret, accountID, network, indexRecord.Value)
	require.NoError(t, err)
	keys := []string{indexKey, wrapperKey}
	walletsFound := false
	for _, entry := range index.Entries {
		if entry.Deleted {
			continue
		}
		keys = append(keys, entry.Key)
		if entry.Provider == "wallets" && entry.Scope == AccountManagedDataGlobalScope {
			require.Equal(t, stateKey, entry.Key)
			walletsFound = true
		}
	}
	require.True(t, walletsFound, "directory must include the actual wallet catalog")
	versions := map[string]coreRecordVersion{}
	var state account.ManagedState
	for _, key := range keys {
		record, err := client.GetRecordDirect(key)
		require.NoError(t, err)
		require.NotNil(t, record)
		require.NoError(t, dkvsindexer.VerifySignature(record))
		versions[key] = coreRecordVersion{Seq: record.Seq, Hash: dkvsindexer.RecordHash(record).String()}
		if key == stateKey {
			state, err = account.OpenManagedState(secret, accountID, record.Value)
			require.NoError(t, err)
		}
		for _, entry := range index.Entries {
			if entry.Deleted || entry.Key != key || entry.Provider == "wallets" {
				continue
			}
			blob, err := DecodeDKVSBlobValue(record.Value)
			require.NoError(t, err)
			module, err := account.OpenManagedModule(secret, accountID, network, entry.Key, blob.Data)
			require.NoError(t, err)
			require.Equal(t, entry.Provider, module.Provider)
			require.Equal(t, entry.Scope, module.Scope)
			require.Equal(t, entry.Codec, module.Codec)
		}
	}
	return versions, state
}

func coreCommittedCASLostACKE2E(t *testing.T, sender *Manager, cfg coreE2EConfig, chain *coreE2EChain, material *coreRecoveryMaterial, observer *coreCommittedACKLossHTTP) {
	t.Helper()
	var samples []map[string]any
	coreLogE2EMetrics(t, "account_sync_committed_lost_ack", &samples)
	coreReplayManagedOutbox(t, sender)
	before, stateBefore := coreCommittedRecords(t, sender, cfg)
	broadcasts := chain.broadcastCount()
	store, err := sender.accountDKVSStore()
	require.NoError(t, err)
	config, err := store.client.GetDKVSClientConfig()
	require.NoError(t, err)
	require.NotNil(t, config)
	require.NotEmpty(t, config.EndpointID)
	root, err := sender.accountManagementRootWallet()
	require.NoError(t, err)
	stateKey, err := sender.accountManagedStateKey(root)
	require.NoError(t, err)
	keys := make(map[string]bool, len(before))
	for key := range before {
		keys[key] = true
	}
	hold := observer.arm(config.EndpointID, stateKey, keys, before[stateKey], before)
	defer observer.disarm(hold)
	t.Cleanup(func() {
		for _, row := range observer.audit() {
			raw, err := json.Marshal(row)
			if err != nil {
				t.Errorf("encode CAS audit: %T", err)
				continue
			}
			t.Logf("core-cas-audit: %s", raw)
		}
	})
	changedName := "Committed once despite lost ACK"
	started := time.Now()
	rootID := sender.GetAccountManagementStatus().RootWalletID
	require.NoError(t, sender.UpdateWalletName(rootID, changedName))
	// Reuse the replay phase's existing 20s bound; no transport timeout grows.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	done := make(chan struct{})
	var syncErr error
	go func() {
		defer close(done)
		syncErr = sender.SyncAccountManagementState(ctx)
	}()
	defer func() {
		hold.unblock()
		cancel()
		<-done
	}()
	var observed coreACKCommit
	select {
	case observed = <-hold.entered:
	case <-ctx.Done():
		t.Fatalf("target account CAS did not reach the committed ACK boundary: counts=%v", observer.counts())
	}
	require.NoError(t, observed.err)
	require.NotEmpty(t, observed.requestID)
	committed, stateCommitted := coreCommittedRecords(t, sender, cfg)
	require.NotEqual(t, before, committed, "fault must follow a genuine remote commit")
	require.Equal(t, before[stateKey].Seq+1, committed[stateKey].Seq)
	require.NotEqual(t, stateBefore, stateCommitted, "the committed catalog must contain the real edit")
	for key, version := range observed.versions {
		require.Equal(t, version, committed[key], "independent read must match the exact request's committed record")
	}
	found := false
	for _, entry := range stateCommitted.Wallets {
		if entry.Name == changedName {
			found = true
		}
	}
	require.True(t, found, "lost ACK must belong to the actual name mutation")
	samples = append(samples, coreE2EDuration("edit_to_observed_commit", started))
	hold.unblock()
	select {
	case <-hold.finished:
	case <-ctx.Done():
		t.Fatal("target CAS transport did not return after ACK release")
	}
	require.Equal(t, 1, observer.counts()["lost_ack"], "verified target CAS did not return the injected 503")
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("initial synchronization did not finish after the injected 503")
	}
	err = syncErr
	sample := coreE2EDuration("edit_to_first_caller_return", started)
	sample["counts"] = observer.counts()
	samples = append(samples, sample)
	if err != nil {
		// Lost acknowledgements may surface directly or leave a pending outbox.
		require.True(t, strings.Contains(err.Error(), "ACK intentionally lost") || errors.Is(err, ErrDKVSPathNotSynced), "unexpected sync failure: %v", err)
	}
	observer.setAuditPhase("replay")
	retryStarted := time.Now()
	// Keep counting traffic while the one-shot fault has already been consumed.
	coreReplayManagedOutbox(t, sender)
	samples = append(samples, coreE2EDuration("explicit_retry_to_convergence", retryStarted))
	final, stateFinal := coreCommittedRecords(t, sender, cfg)
	require.Equal(t, committed, final, "retry must reconcile the original commit without republishing")
	require.Equal(t, committed[stateKey].Seq, final[stateKey].Seq)
	require.Equal(t, stateCommitted, stateFinal)
	require.Equal(t, 1, observer.counts()["distinct_target_requests"], "recovery must retain the original private-state request ID")
	require.Equal(t, 1, observer.counts()["committed_state_versions"], "recovery must not commit another state version")
	require.Zero(t, sender.GetAccountManagementStatus().PendingChanges)
	require.False(t, sender.GetAccountManagementStatus().ManagedDataDirty)
	entries, err := newDKVSReplicaStore(sender.db).LoadOutbox(store.client.replicaNamespace)
	require.NoError(t, err)
	require.Empty(t, entries)
	observer.setAuditPhase("cold-restore")
	cold := coreRestoreE2E(t, cfg, chain, material)
	require.Equal(t, coreCanonicalCatalog(sender), coreCanonicalCatalog(cold))
	catalogBefore := coreCanonicalCatalog(sender)
	observer.setAuditPhase("warm-baseline")
	countsBefore := observer.counts()
	warm := make([]float64, 0, 10)
	for i := 0; i < 10; i++ {
		observer.setAuditPhase(fmt.Sprintf("warm-%d", i))
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
	require.Equal(t, 1, countsAfter["distinct_target_requests"])
	require.Equal(t, 1, countsAfter["committed_state_versions"])
	countsAfter["distinct_committed_states"] = countsAfter["committed_state_versions"]
	samples = append(samples, map[string]any{"phase": "scenario_counts", "counts": countsAfter, "workload": map[string]int{"wallets": len(catalogBefore), "operations": 1}})
}
