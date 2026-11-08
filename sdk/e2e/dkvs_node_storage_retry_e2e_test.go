package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	core "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type nodeReadFaultDB struct {
	indexercommon.KVDB
	target string
	armed  atomic.Bool
	hits   atomic.Int32
}

func (d *nodeReadFaultDB) Read(key []byte) ([]byte, error) {
	if d.armed.Load() && string(key) == "dkvs:record:"+d.target {
		d.hits.Add(1)
		return nil, syscall.EIO
	}
	return d.KVDB.Read(key)
}

type nodeRetryHTTP struct {
	inner         wallet.HttpClient
	mu            sync.Mutex
	status        int
	code          string
	requestBodies [][]byte
}

func (h *nodeRetryHTTP) SendGetRequest(u *wallet.URL) ([]byte, error) {
	body, err := h.inner.SendGetRequest(u)
	h.observe(err)
	return body, err
}

func (h *nodeRetryHTTP) SendPostRequest(u *wallet.URL, body []byte) ([]byte, error) {
	h.observeRequest(u, body)
	response, err := h.inner.SendPostRequest(u, body)
	h.observe(err)
	return response, err
}

func (h *nodeRetryHTTP) SendGetRequestContext(ctx context.Context, u *wallet.URL) ([]byte, error) {
	if client, ok := h.inner.(wallet.ContextHttpClient); ok {
		body, err := client.SendGetRequestContext(ctx, u)
		h.observe(err)
		return body, err
	}
	return h.SendGetRequest(u)
}

func (h *nodeRetryHTTP) SendPostRequestContext(ctx context.Context, u *wallet.URL, body []byte) ([]byte, error) {
	if client, ok := h.inner.(wallet.ContextHttpClient); ok {
		h.observeRequest(u, body)
		response, err := client.SendPostRequestContext(ctx, u, body)
		h.observe(err)
		return response, err
	}
	return h.SendPostRequest(u, body)
}

func (h *nodeRetryHTTP) observeRequest(u *wallet.URL, body []byte) {
	if u == nil || !strings.HasSuffix(u.Path, "/v3/dkvs/records/batch-cas") {
		return
	}
	h.mu.Lock()
	h.requestBodies = append(h.requestBodies, append([]byte(nil), body...))
	h.mu.Unlock()
}

func (h *nodeRetryHTTP) observe(err error) {
	var response *wallet.HTTPResponseError
	if !errors.As(err, &response) || response == nil {
		return
	}
	var body struct {
		ErrorCode string `json:"error_code"`
	}
	_ = json.Unmarshal(response.Body, &body)
	h.mu.Lock()
	h.status, h.code = response.StatusCode, body.ErrorCode
	h.mu.Unlock()
}

func (h *nodeRetryHTTP) reset() {
	h.mu.Lock()
	h.status, h.code = 0, ""
	h.requestBodies = nil
	h.mu.Unlock()
}

func (h *nodeRetryHTTP) result() (int, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status, h.code
}

func (h *nodeRetryHTTP) requests() [][]byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	bodies := make([][]byte, len(h.requestBodies))
	for i, body := range h.requestBodies {
		bodies[i] = append([]byte(nil), body...)
	}
	return bodies
}

func nodeTargetOutbox(t *testing.T, db indexercommon.KVDB, key string) (*core.BatchOutboxEntry, int) {
	t.Helper()
	entries, err := core.NewReplicaStore(db).LoadOutbox("test:testnet")
	require.NoError(t, err)
	for _, entry := range entries {
		mutations, err := entry.DecodeMutations()
		require.NoError(t, err)
		for _, mutation := range mutations {
			if mutation.Record != nil && mutation.Record.Key == key {
				return entry, len(entries)
			}
		}
	}
	return nil, len(entries)
}

func assertNodeCASReplay(t *testing.T, originalBody, replayBody []byte, replayNum int) {
	t.Helper()
	var original, replay core.BatchCASRequest
	require.NoError(t, json.Unmarshal(originalBody, &original))
	require.NoError(t, json.Unmarshal(replayBody, &replay))
	assert.Equal(t, original.RequestID, replay.RequestID, "replay %d must retain the batch request ID", replayNum)
	assert.Equal(t, original.EndpointID, replay.EndpointID, "replay %d must retain the endpoint binding", replayNum)
	assert.Equal(t, original.Authorization, replay.Authorization, "replay %d must retain the full signed authorization", replayNum)
	require.Equal(t, len(original.Mutations), len(replay.Mutations), "replay %d must retain the mutation count and order", replayNum)
	for i := range original.Mutations {
		want := original.Mutations[i]
		got := replay.Mutations[i]
		require.NotNil(t, want.Record)
		require.NotNil(t, got.Record)
		t.Logf("replay PubKey form replay=%d mutation=%d original_nil=%t original_len=%d retry_nil=%t retry_len=%d",
			replayNum, i, want.Record.PubKey == nil, len(want.Record.PubKey), got.Record.PubKey == nil, len(got.Record.PubKey))
		wantRecord, err := dkvs.MarshalRecord(want.Record)
		require.NoError(t, err)
		gotRecord, err := dkvs.MarshalRecord(got.Record)
		require.NoError(t, err)
		assert.True(t, bytes.Equal(wantRecord, gotRecord), "replay %d mutation %d must retain the full protocol record bytes", replayNum, i)
		assert.Equal(t, want.ExpectedETag, got.ExpectedETag, "replay %d mutation %d must retain its CAS ETag", replayNum, i)
		assert.Equal(t, want.ExpectAbsent, got.ExpectAbsent, "replay %d mutation %d must retain its absent precondition", replayNum, i)
		if len(want.Record.PubKey) == 0 {
			want.Record.PubKey = nil
		}
		if len(got.Record.PubKey) == 0 {
			got.Record.PubKey = nil
		}
	}
	assert.Equal(t, original, replay, "replay %d must preserve the entire normalized batch CAS request", replayNum)
}

func TestSDKNodeStorageRetry(t *testing.T) {
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	rawNodeDB := indexerdb.NewKVDB(t.TempDir())
	require.NotNil(t, rawNodeDB)
	nodeDB := &nodeReadFaultDB{KVDB: rawNodeDB}
	t.Cleanup(func() { rawNodeDB.Close() })
	f := newBoundRPCFixtureDB(t, nodeDB)
	f.bind(t, owner, f.coreID)

	base, err := url.Parse(f.server.URL)
	require.NoError(t, err)
	endpoint := &sdkcommon.Indexer{Scheme: base.Scheme, Host: base.Host, Proxy: "testnet"}
	config := &sdkcommon.Config{Env: "test", Chain: "testnet", IndexerL1: endpoint, IndexerL2: endpoint}
	sdkDB := indexerdb.NewKVDB(t.TempDir())
	require.NotNil(t, sdkDB)
	manager := wallet.NewManager(config, sdkDB)
	require.NotNil(t, manager)
	t.Cleanup(func() { manager.StopDKVSSync(); manager.Close(); sdkDB.Close() })
	httpTrace := &nodeRetryHTTP{inner: wallet.NewHTTPClient()}
	manager.SetDKVSHttpClient(httpTrace)
	client, err := manager.GetDKVSClient()
	require.NoError(t, err)
	writer := client.WithWriteSigner(owner.Wallet)

	key, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), "storage-retry/value")
	require.NoError(t, err)
	initial := sdkDKVSReviewFreeRecord(t, f.client, owner, key, []byte("before"), 1)
	_, err = writer.PutRecordCAS(initial, dkvs.WritePrecondition{ExpectAbsent: true})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		current, readErr := f.client.GetRecord(key)
		return readErr == nil && dkvs.RecordHash(current) == dkvs.RecordHash(initial)
	}, 10*time.Second, 20*time.Millisecond, "initial value must be committed through Wallet RPC")
	entry, _ := nodeTargetOutbox(t, sdkDB, key)
	require.Nil(t, entry, "successful initial write must clear its outbox")

	updated := sdkDKVSReviewFreeRecord(t, f.client, owner, key, []byte("after"), 2)
	initialHash := dkvs.RecordHash(initial)
	nodeDB.target = key
	var bindingChecked atomic.Bool
	f.mu.Lock()
	f.afterBindingRead = func() {
		bindingChecked.Store(true)
		nodeDB.armed.Store(true)
	}
	f.mu.Unlock()
	httpTrace.reset()
	_, writeErr := writer.PutRecordCAS(updated, dkvs.WritePrecondition{ExpectedHash: &initialHash})
	require.Error(t, writeErr)
	var response *wallet.HTTPResponseError
	require.ErrorAs(t, writeErr, &response)
	var dkvsErr *core.DKVSError
	require.ErrorAs(t, writeErr, &dkvsErr)
	require.Equal(t, dkvs.ErrorCodeStorageUnavailable, dkvsErr.Code)
	require.ErrorIs(t, writeErr, dkvs.ErrStorageUnavailable)
	require.True(t, bindingChecked.Load(), "fault must arm only after Wallet RPC binding admission")
	require.Positive(t, nodeDB.hits.Load(), "node target record read boundary must observe the injected error")
	status, code := httpTrace.result()
	t.Logf("node read fault: http_status=%d error_code=%s read_failures=%d", status, code, nodeDB.hits.Load())
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Equal(t, string(dkvs.ErrorCodeStorageUnavailable), code)
	requestBodies := httpTrace.requests()
	require.Len(t, requestBodies, 1, "the failed write must send one target batch CAS request")

	nodeDB.armed.Store(false)
	current, err := f.client.GetRecord(key)
	require.NoError(t, err)
	require.Equal(t, dkvs.RecordHash(initial), dkvs.RecordHash(current), "failed node read must not change the remote value")
	entry, outboxCount := nodeTargetOutbox(t, sdkDB, key)
	require.NotNil(t, entry, "failed durable write must retain an outbox entry")
	t.Logf("outbox after HTTP refusal: state=%s error_code=%s total=%d", entry.State, entry.LastErrorCode, outboxCount)
	assert.Equal(t, core.DKVSOutboxPending, entry.State, "local I/O failure must remain retryable")

	require.NoError(t, manager.RetryDKVSSync())
	recovered := assert.Eventually(t, func() bool {
		latest, readErr := f.client.GetRecord(key)
		if readErr != nil || dkvs.RecordHash(latest) != dkvs.RecordHash(updated) {
			return false
		}
		pending, _ := nodeTargetOutbox(t, sdkDB, key)
		return pending == nil
	}, 5*time.Second, 50*time.Millisecond, "explicit SDK retry must publish the update and clear its outbox ACK")
	allBodies := httpTrace.requests()
	assert.GreaterOrEqual(t, len(allBodies), 2, "retry must send the target batch CAS request again")
	if len(allBodies) >= 2 {
		for i, body := range allBodies[1:] {
			assertNodeCASReplay(t, requestBodies[0], body, i+1)
		}
	}
	latest, err := f.client.GetRecord(key)
	require.NoError(t, err)
	entry, outboxCount = nodeTargetOutbox(t, sdkDB, key)
	state, lastCode := "", ""
	if entry != nil {
		state, lastCode = entry.State, entry.LastErrorCode
	}
	t.Logf("retry result: recovered=%t record_matches_update=%t outbox_state=%s error_code=%s total=%d",
		recovered, dkvs.RecordHash(latest) == dkvs.RecordHash(updated), state, lastCode, outboxCount)
	assert.Equal(t, dkvs.RecordHash(updated), dkvs.RecordHash(latest), "retry must publish the update")
	assert.Nil(t, entry, "successful retry must clear the outbox")
}
