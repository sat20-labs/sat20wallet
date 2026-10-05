package wallet

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	core "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	"github.com/sat20-labs/satoshinet/chaincfg"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// Reuse the endpoint fixture. Hooks only inject transport failures or delay an
// already captured response; replica/outbox/worker behavior stays in production.
type reviewAcceptanceHTTP struct {
	*rgb11MemoryDKVSHTTP
	post func(context.Context, string, []byte) ([]byte, error)
	get  func(context.Context, string, map[string]string) ([]byte, error)
}

func (h *reviewAcceptanceHTTP) SendDKVSPostContext(ctx context.Context, path string, body []byte) ([]byte, error) {
	if h.post != nil {
		return h.post(ctx, path, body)
	}
	return h.rgb11MemoryDKVSHTTP.SendDKVSPostContext(ctx, path, body)
}
func (h *reviewAcceptanceHTTP) SendDKVSGetContext(ctx context.Context, path string, query map[string]string) ([]byte, error) {
	if h.get != nil {
		return h.get(ctx, path, query)
	}
	return h.rgb11MemoryDKVSHTTP.SendDKVSGetContext(ctx, path, query)
}

func TestDKVSReviewFundingPausedOutboxStillReceives(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	configureRGB11DKVSTestManager(manager, http)
	manager.l2IndexerClient = &IndexerRPCClientMgr{active: NewIndexerClient("http", "dkvs.test", "testnet", http)}
	client, err := manager.ensureDKVSManager().primaryClient()
	require.NoError(t, err)
	key := accountTestKey(t, manager, "review-receive/value")
	seed := finalDKVSSeedClient(manager, remote)
	first, err := seed.PutRecord(freeLocalRecord(t, manager, key, 1, "confirmed-v1"))
	require.NoError(t, err)
	prefix, err := dkvs.CollectionPathForKey(key)
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
	// A different collection is waiting for AUTOPAY funds. Existing subscribed
	// data must continue receiving even while this original request stays pending.
	paidKey := accountTestKey(t, manager, "review-paid/pending")
	pending, err := newSignedRecordWithAutopay(manager.wallet, paidKey, []byte("pending"),
		dkvs.RecordOptions{Seq: 1, IssueHeight: 1}, DKVSAutopayOptions{AddressParams: &chaincfg.TestNetParams})
	require.NoError(t, err)
	mutations := []dkvs.CASMutation{{Record: pending, Precondition: dkvs.WritePrecondition{ExpectAbsent: true}}}
	entry, err := core.NewBatchOutboxEntry(client.replicaNamespace, mutations, remote.endpointID, core.OutboxOrigin{})
	require.NoError(t, err)
	entry.Authorization, err = client.WithWriteSigner(manager.wallet).prepareWriteAuthorization(mutations, entry.EndpointID, entry.RequestID)
	require.NoError(t, err)
	replica := core.NewReplicaStore(manager.db)
	require.NoError(t, replica.QueueOutbox(entry))
	defaults := dkvs.NetworkDefaultsForParams(GetChainParam_SatsNet())
	manager.accountProfile = &accountManagementProfile{StorageMode: AccountStoragePaid, RootFingerprint: walletFingerprint(manager.wallet)}
	remote.autopayState = &dkvs.AutopayContractState{Contract: defaults.AutopayContract,
		TemplateName: TEMPLATE_CONTRACT_AUTOPAY, CurrentBlock: 1, Status: "active",
		ServiceName: defaults.AutopayServiceName, Recipient: defaults.AutopayRecipient, FeeAssetName: defaults.AutopayFeeAssetName,
		Delegates: map[string]dkvs.AutopayDelegateState{}}
	status, err := manager.GetAccountAutopayFundingStatus()
	require.NoError(t, err)
	require.True(t, status.NeedsFunding)
	http.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if path == "/v3/dkvs/records/batch-cas" {
			return nil, dkvs.ErrInvalidFeeProof
		}
		return remote.SendDKVSPostContext(ctx, path, body)
	}
	manager.dkvs.start()
	defer manager.dkvs.stopAndWait()
	require.Eventually(t, func() bool {
		entries, e := replica.LoadOutbox(client.replicaNamespace)
		return e == nil && len(entries) == 1 && entries[0].LastError != ""
	}, 2*time.Second, 10*time.Millisecond)
	var entries []*core.BatchOutboxEntry
	require.NoError(t, manager.dkvs.runTransport(func() error {
		entries, err = replica.LoadOutbox(client.replicaNamespace)
		return err
	}))
	require.Len(t, entries, 1)
	require.Contains(t, entries[0].LastError, ErrAccountAutopayFundingRequired.Error(), "the worker must reach the actual funding-paused branch")
	require.Equal(t, entry.RequestID, entries[0].RequestID)
	require.Equal(t, entry.Authorization, entries[0].Authorization)
	require.Equal(t, core.DKVSOutboxPending, entries[0].State)
	// Commit remotely after the worker has observed the funding failure.
	updated, err := seed.PutRecord(freeLocalRecord(t, manager, key, 2, "remote-v2"))
	require.NoError(t, err)
	require.NotEqual(t, dkvs.RecordHash(first), dkvs.RecordHash(updated))
	require.Eventually(t, func() bool {
		local, e := replica.LoadSubscriptionRecord(client.replicaNamespace, key)
		return e == nil && dkvs.RecordHash(local) == dkvs.RecordHash(updated)
	}, 2*time.Second, 10*time.Millisecond, "waiting for funds must not stop receiving an unrelated managed collection")
	require.NoError(t, manager.dkvs.runTransport(func() error {
		entries, err = replica.LoadOutbox(client.replicaNamespace)
		return err
	}))
	require.Len(t, entries, 1)
	require.Equal(t, entry.RequestID, entries[0].RequestID)
	require.Equal(t, entry.Authorization, entries[0].Authorization, "notification wakes may replay only the original authorization")
	require.Equal(t, core.DKVSOutboxPending, entries[0].State)
	_, lastError, _ := manager.dkvs.lastSyncErrorStatus()
	require.Contains(t, lastError, ErrAccountAutopayFundingRequired.Error(), "successful receiving must retain the funding prompt")
}

func TestDKVSReviewLateReadCannotRefillInvalidatedCache(t *testing.T) {
	for _, mode := range []string{"directory", "record"} {
		t.Run(mode, func(t *testing.T) {
			manager, remote := finalDKVSTestManager(t)
			http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
			configureRGB11DKVSTestManager(manager, http)
			client, err := manager.ensureDKVSManager().primaryClient()
			require.NoError(t, err)
			_, err = client.GetDKVSClientConfig()
			require.NoError(t, err)
			key := accountTestKey(t, manager, "review-cache/value")
			prefix, err := dkvs.CollectionPathForKey(key)
			require.NoError(t, err)
			if mode == "record" {
				_, err = finalDKVSSeedClient(manager, remote).PutRecord(freeLocalRecord(t, manager, key, 1, "old"))
				require.NoError(t, err)
			}
			captured, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			releaseRead := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(releaseRead)
			var blocked atomic.Bool
			delay := func(ctx context.Context, raw []byte, err error) ([]byte, error) {
				if err != nil || !blocked.CompareAndSwap(false, true) {
					return raw, err
				}
				close(captured)
				select {
				case <-release:
					return raw, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			http.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
				raw, e := remote.SendDKVSPostContext(ctx, path, body)
				if mode == "directory" && path == "/v3/dkvs/active/sync" {
					return delay(ctx, raw, e)
				}
				return raw, e
			}
			http.get = func(ctx context.Context, path string, query map[string]string) ([]byte, error) {
				raw, e := remote.SendDKVSGetContext(ctx, path, query)
				if mode == "record" && path == "/v3/dkvs/record" {
					return delay(ctx, raw, e)
				}
				return raw, e
			}
			read := func() error {
				if mode == "directory" {
					_, _, e := client.ListRecords(prefix, 0, 0)
					return e
				}
				_, e := client.GetRecord(key)
				return e
			}
			readDone := make(chan error, 1)
			go func() { readDone <- read() }()
			select {
			case <-captured:
			case <-time.After(2 * time.Second):
				t.Fatal("old response was not captured")
			}
			writeDone := make(chan error, 1)
			go func() {
				_, e := client.PutSignedRecordFreeLocal(manager.wallet, key, []byte("new"), dkvs.RecordOptions{IssueHeight: 1, TTL: 100})
				writeDone <- e
			}()
			// Permit a repair that serializes the read with writes: release the old
			// read if the existing coordinator holds the new write behind it.
			var writeErr error
			writeFinished := false
			select {
			case writeErr = <-writeDone:
				writeFinished = true
			case <-time.After(500 * time.Millisecond):
			}
			releaseRead()
			require.NoError(t, <-readDone)
			if !writeFinished {
				writeErr = <-writeDone
			}
			require.NoError(t, writeErr)
			if mode == "directory" {
				records, total, e := client.ListRecords(prefix, 0, 0)
				require.NoError(t, e)
				require.Equal(t, 1, total, "late old response repopulated the invalidated empty directory")
				require.Equal(t, "new", string(records[0].Value))
			} else {
				record, e := client.GetRecord(key)
				require.NoError(t, e)
				require.Equal(t, "new", string(record.Value), "late old response repopulated the invalidated key cache")
			}
		})
	}
}
