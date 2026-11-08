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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	core "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// The rename is saved locally, but its upload never reaches the node. Real
// blocks advance during the outage; replay receives the node's actual refusal.
func TestSDKBackupReconnectHeight(t *testing.T) {
	runDKVSHeightReconnect(t, false)
}

func TestSDKHeightRetryStorage(t *testing.T) {
	runDKVSHeightReconnect(t, true)
}

func runDKVSHeightReconnect(t *testing.T, failStorage bool) {
	fixture := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t))
	network := fixture.Network
	waitForDKVSPeerReady(t, network)
	gas := contractcommon.GetGasAssetName()
	actor := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	amount := int64(1_000_000)
	input := splitToDKVSKeyPathActors(t, fixture, fixture.gasAnchor, gas,
		[]int64{amount}, []int64{100_000}, []*dkvsKeyPathActor{actor})[0]
	f := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 217), true)
	f.activate(t)
	var fault *heightRetryDB
	if failStorage {
		config, _ := accountReviewConfig(t, network)
		database := indexerdb.NewKVDB(t.TempDir())
		require.NotNil(t, database)
		fault = &heightRetryDB{KVDB: database}
		manager := wallet.NewManager(config, fault)
		require.NotNil(t, manager)
		t.Cleanup(func() { manager.Close(); database.Close() })
		recovered, err := manager.LoadAccountManagementStateForRecovery(f.location, f.pkg.Envelope.Locator,
			f.secret, f.rootMnemonic)
		require.NoError(t, err)
		_, err = manager.RestoreAccountManagementState(*recovered, f.secret, accountReviewPassword,
			f.pkg.Envelope.Locator, f.restoreOptions())
		require.NoError(t, err)
		accountFailureReady(t, manager)
		f.manager.StopDKVSSync()
		f.manager = manager
	}
	status := f.manager.GetAccountManagementStatus()
	key, err := dkvs.AccountPersonalKey(status.AccountID, "account/state")
	require.NoError(t, err)
	original := waitForDKVSRecord(t, network.Core, key)
	target, err := url.Parse("http://" + f.location.Host)
	require.NoError(t, err)
	proxy := httputil.NewSingleHostReverseProxy(target)
	var offline atomic.Bool
	var dropped atomic.Int32
	var refused atomic.Int32
	var mu sync.Mutex
	var requests []core.BatchCASRequest
	var firstOnline core.BatchCASRequest
	proxy.ModifyResponse = func(response *http.Response) error {
		if response.Request.Header.Get("X-E2E-Account-State") != "1" {
			return nil
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			return err
		}
		response.Body = io.NopCloser(bytes.NewReader(body))
		var result struct {
			Code string `json:"error_code"`
		}
		if json.Unmarshal(body, &result) == nil && result.Code == string(dkvs.ErrorCodeHeightTooLow) {
			refused.Add(1)
		}
		return nil
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/records/batch-cas") {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "read request", http.StatusBadRequest)
				return
			}
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
			var batch core.BatchCASRequest
			if json.Unmarshal(body, &batch) == nil {
				for _, mutation := range batch.Mutations {
					if mutation.Record == nil || mutation.Record.Key != key {
						continue
					}
					r.Header.Set("X-E2E-Account-State", "1")
					mu.Lock()
					requests = append(requests, batch)
					if !offline.Load() && firstOnline.RequestID == "" {
						firstOnline = batch
					}
					mu.Unlock()
					if offline.Load() {
						connection, _, err := w.(http.Hijacker).Hijack()
						if err == nil {
							dropped.Add(1)
							connection.Close()
						}
						return
					}
					break
				}
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	defer server.Close()
	proxyURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := wallet.NewHTTPClient().(*wallet.NetClient)
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	client.Client.Transport = transport
	f.manager.SetDKVSHttpClient(client)
	require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
	offline.Store(true)
	name := "Rename before three offline blocks"
	require.NoError(t, f.manager.UpdateWalletName(status.RootWalletID, name))
	require.Eventually(t, func() bool { return dropped.Load() > 0 }, 10*time.Second, 20*time.Millisecond)
	mu.Lock()
	first := requests[0]
	mu.Unlock()
	require.Len(t, first.Mutations, 1)
	issueHeight := first.Mutations[0].Record.IssueHeight
	if fault != nil {
		fault.target.Store(string(core.OutboxKey("test:testnet", first.RequestID)))
		fault.fail.Store(true)
	}
	// A different key uses the same signer/public submission path. Capture its
	// valid signed value before the outage, then submit after real blocks pass.
	root := newDKVSKeyPathActor(t, keyFromMnemonic(t, f.rootMnemonic, 0))
	raw := dkvsClientForNode(t, network.Core)
	personalKey, err := dkvs.PersonalKey(root.Wallet.GetPubKey().SerializeCompressed(), "height-review/value")
	require.NoError(t, err)
	personal := sdkDKVSReviewFreeRecord(t, raw, root, personalKey, []byte("offline value"), 1)
	for i := uint64(0); i <= dkvs.WalletWriteHeightTolerance; i++ {
		amount -= 10_000
		tx := buildDKVSKeyPathAssetTransfer(t, actor, input, gas, amount, input.Output.Value-1000, actor)
		network.sendAndMine(t, tx, 0)
		input = dkvsPrevOut{Point: wire.OutPoint{Hash: tx.TxHash(), Index: 0}, Output: cloneDKVSTxOut(tx.TxOut[0])}
	}
	_, height, err := network.Core.Client.GetBestBlock()
	require.NoError(t, err)
	require.Greater(t, uint64(height)-issueHeight, dkvs.WalletWriteHeightTolerance)
	unchanged := waitForDKVSRecord(t, network.Core, key)
	require.Equal(t, dkvs.RecordHash(original), dkvs.RecordHash(unchanged), "dropped upload must not reach the node")
	offline.Store(false)
	require.NoError(t, f.manager.RetryDKVSSync())
	require.Eventually(t, func() bool { return refused.Load() > 0 }, 15*time.Second, 50*time.Millisecond,
		"the real node must reject the old signed IssueHeight")
	mu.Lock()
	replay := firstOnline
	mu.Unlock()
	require.Equal(t, first.RequestID, replay.RequestID, "unknown delivery must replay the original request first")
	require.Equal(t, first.EndpointID, replay.EndpointID)
	require.Equal(t, first.Authorization, replay.Authorization)
	require.Len(t, replay.Mutations, 1)
	// The binary round trip normalizes nil/empty fields; compare the actual
	// protocol bytes and CAS condition instead of their Go slice shapes.
	signed, err := dkvs.MarshalRecord(first.Mutations[0].Record)
	require.NoError(t, err)
	replayed, err := dkvs.MarshalRecord(replay.Mutations[0].Record)
	require.NoError(t, err)
	require.True(t, bytes.Equal(signed, replayed), "replay must retain every signed record byte")
	require.Equal(t, first.Mutations[0].ExpectedETag, replay.Mutations[0].ExpectedETag)
	require.Equal(t, first.Mutations[0].ExpectAbsent, replay.Mutations[0].ExpectAbsent)
	if fault != nil {
		require.Eventually(t, func() bool { return fault.failures.Load() > 0 }, 15*time.Second, 50*time.Millisecond)
		entries, err := core.NewReplicaStore(f.manager.GetDB()).LoadOutbox("test:testnet")
		require.NoError(t, err)
		var retained *core.BatchOutboxEntry
		for _, entry := range entries {
			if entry.RequestID == first.RequestID {
				retained = entry
			}
		}
		require.NotNil(t, retained, "failed atomic replacement must preserve the original request")
		require.Equal(t, first.Authorization, retained.Authorization)
		decoded, err := retained.DecodeMutations()
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(first.Mutations[0].Record), dkvs.RecordHash(decoded[0].Record))
		mu.Lock()
		captured := append([]core.BatchCASRequest(nil), requests...)
		mu.Unlock()
		for _, batch := range captured {
			require.Equal(t, first.RequestID, batch.RequestID, "unpersisted replacement must never be sent")
		}
		current := waitForDKVSRecord(t, network.Core, key)
		require.Equal(t, dkvs.RecordHash(original), dkvs.RecordHash(current))
		fault.fail.Store(false)
	}
	// Also exercise the PWA's explicit retry after the definite response.
	require.NoError(t, f.manager.RetryDKVSSync())
	t.Logf("reconnect evidence: signed_height=%d tip=%d dropped=%d height_refusals=%d",
		issueHeight, height, dropped.Load(), refused.Load())
	if !waitBackupRecovery(t, f.manager) {
		entries, err := core.NewReplicaStore(f.manager.GetDB()).LoadOutbox("test:testnet")
		require.NoError(t, err)
		for _, entry := range entries {
			if entry.RequestID == first.RequestID {
				t.Logf("retained original request: domain=%s state=%s error_code=%s", entry.OriginDomain, entry.State, entry.LastErrorCode)
			}
		}
		return
	}
	current := waitForDKVSRecord(t, network.Core, key)
	require.NotEqual(t, dkvs.RecordHash(original), dkvs.RecordHash(current), "successful backup must publish the locally saved rename")
	mu.Lock()
	captured := append([]core.BatchCASRequest(nil), requests...)
	mu.Unlock()
	var refreshed core.BatchCASRequest
	for _, batch := range captured {
		if batch.RequestID != first.RequestID {
			refreshed = batch
			break
		}
	}
	require.NotEmpty(t, refreshed.RequestID)
	require.Equal(t, first.EndpointID, refreshed.EndpointID)
	require.Equal(t, first.Authorization.Context, refreshed.Authorization.Context)
	require.NotEqual(t, first.Authorization.Signature, refreshed.Authorization.Signature)
	require.Len(t, refreshed.Mutations, 1)
	require.Equal(t, first.Mutations[0].ExpectedETag, refreshed.Mutations[0].ExpectedETag)
	require.Equal(t, first.Mutations[0].ExpectAbsent, refreshed.Mutations[0].ExpectAbsent)
	assertHeightOnlyChange(t, first.Mutations[0].Record, refreshed.Mutations[0].Record)
	require.Equal(t, dkvs.RecordHash(refreshed.Mutations[0].Record), dkvs.RecordHash(current))
	// Fresh generic submissions also recover, without an account outbox origin.
	published, err := raw.WithWriteSigner(root.Wallet).PutRecordCAS(personal, dkvs.WritePrecondition{ExpectAbsent: true})
	require.NoError(t, err)
	assertHeightOnlyChange(t, personal, published)
	actual := waitForDKVSRecord(t, network.Core, personalKey)
	require.Equal(t, dkvs.RecordHash(published), dkvs.RecordHash(actual))
}

func assertHeightOnlyChange(t *testing.T, before, after *wire.DKVSRecord) {
	t.Helper()
	require.Greater(t, after.IssueHeight, before.IssueHeight)
	require.NotEqual(t, before.Signature, after.Signature)
	copyRecord := *after
	copyRecord.IssueHeight, copyRecord.Signature = before.IssueHeight, before.Signature
	original, err := dkvs.MarshalRecord(before)
	require.NoError(t, err)
	updated, err := dkvs.MarshalRecord(&copyRecord)
	require.NoError(t, err)
	require.Equal(t, original, updated, "only IssueHeight and signatures may change")
}

type heightRetryDB struct {
	indexercommon.KVDB
	target   atomic.Value
	fail     atomic.Bool
	failures atomic.Int32
}

func (d *heightRetryDB) NewAtomicWriteBatch() indexercommon.WriteBatch {
	return &heightRetryBatch{WriteBatch: d.KVDB.(indexercommon.AtomicWriteBatchDB).NewAtomicWriteBatch(), db: d}
}

type heightRetryBatch struct {
	indexercommon.WriteBatch
	db        *heightRetryDB
	replacing bool
}

func (b *heightRetryBatch) Delete(key []byte) error {
	target, _ := b.db.target.Load().(string)
	b.replacing = b.replacing || string(key) == target
	return b.WriteBatch.Delete(key)
}

func (b *heightRetryBatch) Flush() error {
	if b.replacing && b.db.fail.Load() {
		b.db.failures.Add(1)
		return errAccountE2EProfileWrite
	}
	return b.WriteBatch.Flush()
}
