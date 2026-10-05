package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Always-discovered SDK E2Es use isolated real nodes and public SDK binding,
// signing, RPC and current-state reads. Negative scenarios never relax admission.
func TestSDKDKVSModuleLocal(t *testing.T) {
	f := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t))
	waitForDKVSPeerReady(t, f.Network)
	bindDKVSReviewWallet(t, f.Network.Core, dkvsClientMnemonic)
	client := dkvsClientForNode(t, f.Network.Core)
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	ownerWriter := client.WithWriteSigner(owner.Wallet)
	other := newDKVSKeyPathActor(t, keyFromMnemonic(t, bootstrapMnemonic, 2))
	key := func(t *testing.T, path string) string {
		t.Helper()
		k, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), path)
		require.NoError(t, err)
		return k
	}
	heartbeatPrevious := f.gasAnchor
	advanceHeightTo := func(t *testing.T, target uint64) {
		t.Helper()
		const heartbeatFee = int64(10000)
		for step := 0; step < 64 && sdkDKVSReviewHeight(t, client) < target; step++ {
			heartbeat := wire.NewMsgTx(2)
			heartbeat.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: heartbeatPrevious.TxHash(), Index: 0}})
			output := cloneDKVSTxOut(heartbeatPrevious.TxOut[0])
			require.Greater(t, output.Value, heartbeatFee)
			output.Value -= heartbeatFee
			heartbeat.AddTxOut(output)
			signTaprootInputs(t, heartbeat, f.A.Key, f.A.RedeemScript, f.A.ControlBlock)
			f.Network.sendAndMine(t, heartbeat, 0)
			heartbeatPrevious = heartbeat
		}
		require.GreaterOrEqual(t, sdkDKVSReviewHeight(t, client), target)
	}

	t.Run("BasicLifecycle", func(t *testing.T) {
		k := key(t, "review-basic/value")
		state, err := client.GetKeyState(k)
		require.NoError(t, err)
		require.Equal(t, dkvs.KeyStateNeverSeen, state.Status)
		value := []byte{0, 1, 2, 0xff, 0x80, '\n'}
		first := sdkDKVSReviewPutFree(t, client, owner, k, value)
		require.Equal(t, uint64(1), first.Seq)
		verified, err := client.GetVerifiedRecord(k, dkvs.RecordVerificationOptions{
			Height: sdkDKVSReviewHeight(t, client), CheckHash: true, ExpectedHash: dkvs.RecordHash(first),
		})
		require.NoError(t, err)
		require.Equal(t, value, verified.Value)
		second := sdkDKVSReviewPutFree(t, client, owner, k, []byte("second"))
		require.Equal(t, uint64(2), second.Seq)
		prefix, err := dkvs.CollectionPathForKey(k)
		require.NoError(t, err)
		records, total, err := client.ListVerifiedRecords(prefix, 0, 10, dkvs.RecordVerificationOptions{Height: sdkDKVSReviewHeight(t, client)})
		require.NoError(t, err)
		require.Equal(t, 1, total)
		require.Len(t, records, 1)
		usage, err := client.GetUsage(prefix)
		require.NoError(t, err)
		require.Equal(t, uint64(1), usage.ActiveRecords)
		require.Equal(t, uint64(dkvs.RecordSize(second)), usage.ActiveTotalSize)
		removed, err := client.DeleteCurrentRecord(owner.Wallet, k, sdkDKVSReviewHeight(t, client))
		require.NoError(t, err)
		require.Equal(t, uint64(3), removed.Seq)
		_, err = client.GetRecord(k)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
		records, total, err = client.ListRecords(prefix, 0, 0)
		require.NoError(t, err)
		require.Zero(t, total)
		require.Empty(t, records)
		fresh := dkvsClientForNode(t, f.Network.Core)
		state, err = fresh.GetKeyState(k)
		require.NoError(t, err)
		require.Equal(t, dkvs.KeyStateNeverSeen, state.Status)
		require.Zero(t, state.Seq)
		rewritten := sdkDKVSReviewPutFree(t, fresh, owner, k, []byte("recreated"))
		require.Equal(t, uint64(1), rewritten.Seq, "physical deletion leaves no lifetime sequence floor")
		require.Equal(t, []byte("recreated"), rewritten.Value)
		sdkDKVSReviewUnchanged(t, fresh, rewritten)
	})

	t.Run("FreeLocalIsolation", func(t *testing.T) {
		record := sdkDKVSReviewPutFree(t, client, owner, key(t, "review-local/value"), []byte("endpoint-only"))
		deadline := time.Now().Add(1200 * time.Millisecond)
		for time.Now().Before(deadline) {
			for _, node := range []*testHarness{f.Network.Bootstrap, f.Network.Miner} {
				_, err := dkvsClientForNode(t, node).GetRecord(record.Key)
				require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
			}
			time.Sleep(100 * time.Millisecond)
		}
		sdkDKVSReviewUnchanged(t, client, record)
	})

	t.Run("BatchAtomicityAndIdempotency", func(t *testing.T) {
		a := sdkDKVSReviewFreeRecord(t, client, owner, key(t, "review-batch/a"), []byte("a1"), 1)
		b := sdkDKVSReviewFreeRecord(t, client, owner, key(t, "review-batch/b"), []byte("b1"), 1)
		batch := []dkvs.CASMutation{sdkDKVSReviewAbsent(a), sdkDKVSReviewAbsent(b)}
		result, err := ownerWriter.PutRecordBatchCAS(batch)
		require.NoError(t, err)
		require.Equal(t, 2, result.Applied)
		prefix, err := dkvs.CollectionPathForKey(a.Key)
		require.NoError(t, err)
		before, err := sdkDKVSReviewActivePage(client, prefix)
		require.NoError(t, err)
		replay, err := ownerWriter.PutRecordBatchCAS(batch)
		require.NoError(t, err)
		require.Zero(t, replay.Applied)
		a2 := sdkDKVSReviewPutFree(t, client, owner, a.Key, []byte("a2"))
		committed, err := sdkDKVSReviewActivePage(client, prefix)
		require.NoError(t, err)
		require.Greater(t, committed.Meta.Generation, before.Meta.Generation)
		staleA := sdkDKVSReviewFreeRecord(t, client, owner, a.Key, []byte("stale-a"), 3)
		nextB := sdkDKVSReviewFreeRecord(t, client, owner, b.Key, []byte("must-not-commit"), 2)
		result, err = ownerWriter.PutRecordBatchCAS([]dkvs.CASMutation{
			sdkDKVSReviewExpected(staleA, a), sdkDKVSReviewExpected(nextB, b),
		})
		require.ErrorIs(t, err, dkvs.ErrWriteConflict)
		require.Nil(t, result)
		sdkDKVSReviewUnchanged(t, client, a2)
		sdkDKVSReviewUnchanged(t, client, b)
		after, err := sdkDKVSReviewActivePage(client, prefix)
		require.NoError(t, err)
		require.Equal(t, committed.Meta.Generation, after.Meta.Generation, "rejected batch must not publish a generation")
		_, err = ownerWriter.PutRecordBatchCAS([]dkvs.CASMutation{sdkDKVSReviewAbsent(a), sdkDKVSReviewAbsent(a)})
		require.Error(t, err)
	})

	t.Run("ConcurrentCASHasOneWinner", func(t *testing.T) {
		original := sdkDKVSReviewPutFree(t, client, owner, key(t, "review-concurrent/value"), []byte("original"))
		a := sdkDKVSReviewFreeRecord(t, client, owner, original.Key, []byte("writer-a"), 2)
		b := sdkDKVSReviewFreeRecord(t, client, owner, original.Key, []byte("writer-b"), 2)
		type outcome struct {
			record *wire.DKVSRecord
			err    error
		}
		outcomes := make(chan outcome, 2)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for _, mutation := range []dkvs.CASMutation{sdkDKVSReviewExpected(a, original), sdkDKVSReviewExpected(b, original)} {
			wg.Add(1)
			go func(m dkvs.CASMutation) {
				defer wg.Done()
				<-start
				record, err := ownerWriter.PutRecordCAS(m.Record, m.Precondition)
				outcomes <- outcome{record, err}
			}(mutation)
		}
		close(start)
		wg.Wait()
		close(outcomes)
		winners, conflicts := 0, 0
		var winner *wire.DKVSRecord
		for result := range outcomes {
			if result.err == nil {
				winners++
				winner = result.record
			} else {
				// The losing request may observe either the original CAS conflict or
				// the prefix generation advanced by the winner. Both require a sync
				// before rebuilding the next mutation.
				require.True(t, errors.Is(result.err, dkvs.ErrWriteConflict) ||
					errors.Is(result.err, dkvs.ErrStaleGeneration), "unexpected loser error: %v", result.err)
				conflicts++
			}
		}
		require.Equal(t, 1, winners)
		require.Equal(t, 1, conflicts)
		sdkDKVSReviewUnchanged(t, client, winner)
	})

	t.Run("RejectInvalidWritesWithoutPartialState", func(t *testing.T) {
		original := sdkDKVSReviewPutFree(t, client, owner, key(t, "review-invalid/original"), []byte("protected"))
		wrongOwner := sdkDKVSReviewFreeRecord(t, client, other, original.Key, []byte("wrong-owner"), 2)
		_, err := ownerWriter.PutRecordCAS(wrongOwner, sdkDKVSReviewExpected(wrongOwner, original).Precondition)
		require.Error(t, err)
		sdkDKVSReviewUnchanged(t, client, original)
		tampered := sdkDKVSReviewFreeRecord(t, client, owner, key(t, "review-invalid/tampered"), []byte("signed"), 1)
		tampered.Value = []byte("not-signed")
		good := sdkDKVSReviewFreeRecord(t, client, owner, key(t, "review-invalid/good"), []byte("also-must-not-commit"), 1)
		_, err = ownerWriter.PutRecordBatchCAS([]dkvs.CASMutation{sdkDKVSReviewAbsent(good), sdkDKVSReviewAbsent(tampered)})
		require.Error(t, err)
		for _, k := range []string{good.Key, tampered.Key} {
			state, err := client.GetKeyState(k)
			require.NoError(t, err)
			require.Equal(t, dkvs.KeyStateNeverSeen, state.Status)
		}
		skipped := sdkDKVSReviewFreeRecord(t, client, owner, original.Key, []byte("skip-seq"), 3)
		_, err = ownerWriter.PutRecordCAS(skipped, sdkDKVSReviewExpected(skipped, original).Precondition)
		require.ErrorIs(t, err, dkvs.ErrInvalidSequence)
		otherKey, err := dkvs.PersonalKey(other.Wallet.GetPubKey().SerializeCompressed(), "review-mixed/value")
		require.NoError(t, err)
		theirs := sdkDKVSReviewFreeRecord(t, client, other, otherKey, []byte("other"), 1)
		_, err = ownerWriter.PutRecordBatchCAS([]dkvs.CASMutation{sdkDKVSReviewAbsent(good), sdkDKVSReviewAbsent(theirs)})
		require.ErrorIs(t, err, dkvs.ErrPermissionDenied)
		_, err = client.PutSignedRecordFreeLocal(owner.Wallet, key(t, "review-invalid/ttl"), nil, dkvs.RecordOptions{})
		require.Error(t, err)
		tooMany := make([]dkvs.CASMutation, dkvs.MaxBatchCASMutations+1)
		for i := range tooMany {
			tooMany[i] = sdkDKVSReviewAbsent(good)
		}
		_, err = ownerWriter.PutRecordBatchCAS(tooMany)
		require.Error(t, err)
		sdkDKVSReviewUnchanged(t, client, original)
	})

	t.Run("CurrentDeltaDeletionAndEndpointPin", func(t *testing.T) {
		a := sdkDKVSReviewPutFree(t, client, owner, key(t, "review-prefix/a"), []byte("a"))
		neighbor := sdkDKVSReviewPutFree(t, client, owner, key(t, "review-prefix-neighbor/a"), []byte("neighbor"))
		prefix, err := dkvs.CollectionPathForKey(a.Key)
		require.NoError(t, err)
		config, err := client.GetDKVSClientConfig()
		require.NoError(t, err)
		scope := dkvs.ActiveScope{Prefix: prefix}
		snapshot, err := client.GetActivePage(context.Background(), dkvs.ActiveSyncRequest{Scope: scope, EndpointID: config.EndpointID, Full: true})
		require.NoError(t, err)
		require.True(t, snapshot.Complete)
		require.Len(t, snapshot.Records, 1)
		require.Equal(t, a.Key, snapshot.Records[0].Key)
		b := sdkDKVSReviewPutFree(t, client, owner, key(t, "review-prefix/b"), []byte("b"))
		_, err = client.DeleteCurrentRecord(owner.Wallet, a.Key, sdkDKVSReviewHeight(t, client))
		require.NoError(t, err)
		delta, err := client.GetActivePage(context.Background(), dkvs.ActiveSyncRequest{Scope: scope, EndpointID: config.EndpointID, After: snapshot.Meta.Generation})
		require.NoError(t, err)
		require.True(t, delta.Complete)
		require.Greater(t, delta.Meta.Generation, snapshot.Meta.Generation)
		require.NotEqual(t, snapshot.Meta.Root, delta.Meta.Root)
		require.Len(t, delta.Records, 1)
		require.Equal(t, b.Key, delta.Records[0].Key)
		// Deleted keys have no delta row. A complete current set resolves the
		// missing key; the root-based SDK reconciliation is tested separately.
		full, err := client.GetActivePage(context.Background(), dkvs.ActiveSyncRequest{Scope: scope, EndpointID: config.EndpointID, Full: true})
		require.NoError(t, err)
		require.True(t, full.Complete)
		require.Equal(t, delta.Meta.Root, full.Meta.Root)
		require.Len(t, full.Records, 1)
		require.Equal(t, b.Key, full.Records[0].Key)
		_, err = client.GetActivePage(context.Background(), dkvs.ActiveSyncRequest{Scope: scope, EndpointID: "not-this-endpoint", Full: true})
		require.ErrorIs(t, err, dkvs.ErrEndpointMismatch)
		sdkDKVSReviewUnchanged(t, client, neighbor)
	})

	t.Run("BlobRoundTripAndOversizeRejection", func(t *testing.T) {
		payload := bytes.Repeat([]byte{0x00, 0xff, 0x7f, 0x80}, 512)
		options := dkvs.RecordOptions{Seq: 1, IssueHeight: sdkDKVSReviewHeight(t, client), TTL: 600}
		record, err := client.PutBlobFreeLocal(owner.Wallet, "review-binary", payload, nil, options)
		require.NoError(t, err)
		accountID := dkvs.AccountID(owner.Wallet.GetPubKey().SerializeCompressed())
		read, _, err := client.GetBlob(accountID, "review-binary", dkvs.RecordVerificationOptions{
			Height: sdkDKVSReviewHeight(t, client), CheckHash: true, ExpectedHash: dkvs.RecordHash(record),
		})
		require.NoError(t, err)
		require.Equal(t, record.Value, read.Value)
		config, err := client.GetDKVSClientConfig()
		require.NoError(t, err)
		require.Greater(t, config.Blob.MaxValueSize, 0)
		_, err = client.PutBlobFreeLocal(owner.Wallet, "review-oversize", make([]byte, config.Blob.MaxValueSize+1), nil, options)
		require.ErrorIs(t, err, dkvs.ErrRecordTooLarge)
		rejectedKey, err := dkvs.BlobKey(accountID, "review-oversize")
		require.NoError(t, err)
		state, err := client.GetKeyState(rejectedKey)
		require.NoError(t, err)
		require.Equal(t, dkvs.KeyStateNeverSeen, state.Status)
	})

	t.Run("QuotaFailureIsAtomic", func(t *testing.T) {
		bindDKVSReviewWallet(t, f.Network.Core, minerMnemonic)
		quotaOwner := newDKVSKeyPathActor(t, keyFromMnemonic(t, minerMnemonic, 0))
		quotaWriter := client.WithWriteSigner(quotaOwner.Wallet)
		config, err := client.GetDKVSClientConfig()
		require.NoError(t, err)
		limit := int(config.FreeLocal.MaxRecordsPerSigner)
		require.Greater(t, limit, 1)
		require.LessOrEqual(t, limit, 256, "fixture quota unexpectedly too large")
		var all []*wire.DKVSRecord
		for i := 0; i < limit; i++ {
			k, err := dkvs.PersonalKey(quotaOwner.Wallet.GetPubKey().SerializeCompressed(), fmt.Sprintf("review-quota/k%03d", i))
			require.NoError(t, err)
			all = append(all, sdkDKVSReviewFreeRecord(t, client, quotaOwner, k, []byte("q"), 1))
		}
		for offset := 0; offset < len(all); offset += dkvs.MaxBatchCASMutations {
			end := offset + dkvs.MaxBatchCASMutations
			if end > len(all) {
				end = len(all)
			}
			var batch []dkvs.CASMutation
			for _, record := range all[offset:end] {
				batch = append(batch, sdkDKVSReviewAbsent(record))
			}
			_, err := quotaWriter.PutRecordBatchCAS(batch)
			require.NoError(t, err)
		}
		extraKey, err := dkvs.PersonalKey(quotaOwner.Wallet.GetPubKey().SerializeCompressed(), "review-quota/extra")
		require.NoError(t, err)
		extra := sdkDKVSReviewFreeRecord(t, client, quotaOwner, extraKey, []byte("overflow"), 1)
		update := sdkDKVSReviewFreeRecord(t, client, quotaOwner, all[0].Key, []byte("must-not-change"), 2)
		_, err = quotaWriter.PutRecordBatchCAS([]dkvs.CASMutation{sdkDKVSReviewExpected(update, all[0]), sdkDKVSReviewAbsent(extra)})
		require.ErrorIs(t, err, dkvs.ErrFreeLocalQuotaExceeded)
		sdkDKVSReviewUnchanged(t, client, all[0])
		state, err := client.GetKeyState(extraKey)
		require.NoError(t, err)
		require.Equal(t, dkvs.KeyStateNeverSeen, state.Status)
	})

	t.Run("ContextCancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		started := time.Now()
		_, err := client.GetRecordDirectContext(ctx, key(t, "review-cancel/value"))
		require.ErrorIs(t, err, context.Canceled)
		require.Less(t, time.Since(started), 2*time.Second)
		_, err = client.ReadPrefixContext(ctx, key(t, "review-cancel/value"))
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("LostWriteAckRetriesExactRequest", func(t *testing.T) {
		record := sdkDKVSReviewFreeRecord(t, client, owner, key(t, "review-write-ack/value"), []byte("once"), 1)
		replayClient, transport := sdkDKVSReviewReplayClient(client, owner.Wallet)
		result, err := replayClient.PutRecordBatchCAS([]dkvs.CASMutation{sdkDKVSReviewAbsent(record)})
		require.NoError(t, err)
		require.Equal(t, 2, transport.attempts)
		require.Equal(t, 1, transport.firstApplied)
		require.Zero(t, result.Applied)
		sdkDKVSReviewUnchanged(t, client, record)
	})

	t.Run("LostDeleteAckRetriesExactRequest", func(t *testing.T) {
		original := sdkDKVSReviewPutFree(t, client, owner, key(t, "review-delete-ack/value"), []byte("delete-once"))
		command, err := wallet.NewDKVSDeleteCommand(owner.Wallet, original, sdkDKVSReviewHeight(t, client))
		require.NoError(t, err)
		replayClient, transport := sdkDKVSReviewReplayClient(client, owner.Wallet)
		result, retryErr := replayClient.PutRecordBatchCAS([]dkvs.CASMutation{sdkDKVSReviewExpected(command, original)})
		_, readErr := client.GetRecord(original.Key)
		t.Logf("dkvs-review: delete retry attempts=%d first_applied=%d remote_absent=%t retry_error=%v",
			transport.attempts, transport.firstApplied, errors.Is(readErr, dkvs.ErrRecordNotFound), retryErr)
		require.Equal(t, 2, transport.attempts)
		require.Equal(t, 1, transport.firstApplied)
		require.ErrorIs(t, readErr, dkvs.ErrRecordNotFound)
		require.NoError(t, retryErr)
		require.Zero(t, result.Applied, "retry acknowledges current absence without retaining a receipt")
	})

	t.Run("RenewalAdvancesSequence", func(t *testing.T) {
		original := sdkDKVSReviewPutFree(t, client, owner, key(t, "review-renew/value"), []byte("retain"))
		renewed, err := client.RenewPersonalRecord(owner.Wallet, "review-renew/value", dkvs.RecordOptions{
			IssueHeight: sdkDKVSReviewHeight(t, client), TTL: original.TTL + 100,
		})
		require.NoError(t, err)
		require.Equal(t, original.Seq+1, renewed.Seq)
		require.Equal(t, original.Value, renewed.Value)
		require.Greater(t, dkvs.RecordExpiryHeight(renewed), dkvs.RecordExpiryHeight(original))
		renewedProof, err := dkvs.ParseFeeProof(renewed.FeeProof)
		require.NoError(t, err)
		require.Equal(t, dkvs.FeeModeFreeLocal, renewedProof.Mode)
		require.NotEqual(t, dkvs.RecordHash(original), dkvs.RecordHash(renewed))
		require.False(t, bytes.Equal(original.Signature, renewed.Signature), "renewal must sign the new sequence/height/TTL")
		sdkDKVSReviewUnchanged(t, client, renewed)
	})

	t.Run("PaginationOverflowDoesNotPanic", func(t *testing.T) {
		a := sdkDKVSReviewPutFree(t, client, owner, key(t, "review-page/a"), []byte("a"))
		b := sdkDKVSReviewPutFree(t, client, owner, key(t, "review-page/b"), []byte("b"))
		prefix, err := dkvs.CollectionPathForKey(a.Key)
		require.NoError(t, err)
		maxInt := int(^uint(0) >> 1)
		var page []*wire.DKVSRecord
		var total int
		require.NotPanics(t, func() { page, total, err = client.ListRecords(prefix, 1, maxInt) })
		require.NoError(t, err)
		require.Equal(t, 2, total)
		require.Len(t, page, 1)
		require.Equal(t, b.Key, page[0].Key)
	})

	t.Run("RenewedLeaseSurvivesOriginalExpiry", func(t *testing.T) {
		k := key(t, "review-renew-lifecycle/value")
		original, err := client.PutSignedRecordFreeLocal(owner.Wallet, k, []byte("lease"), dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, client), TTL: 8})
		require.NoError(t, err)
		oldExpiry := dkvs.RecordExpiryHeight(original)
		require.Greater(t, oldExpiry, uint64(4))
		advanceHeightTo(t, oldExpiry-4)
		renewed, err := client.RenewPersonalRecord(owner.Wallet, "review-renew-lifecycle/value", dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, client), TTL: 40})
		require.NoError(t, err)
		require.Equal(t, original.Seq+1, renewed.Seq)
		newExpiry := dkvs.RecordExpiryHeight(renewed)
		require.Greater(t, newExpiry, oldExpiry)
		advanceHeightTo(t, oldExpiry+1)
		require.Less(t, sdkDKVSReviewHeight(t, client), newExpiry)
		fresh := dkvsClientForNode(t, f.Network.Core)
		actual, err := fresh.GetRecord(k)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(renewed), dkvs.RecordHash(actual))
	})

	t.Run("ExpiryIsAbsentToFreshSDKReader", func(t *testing.T) {
		k := key(t, "review-expiry/value")
		record, err := client.PutSignedRecordFreeLocal(owner.Wallet, k, []byte("expires"), dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, client), TTL: 8})
		require.NoError(t, err)
		prefix, err := dkvs.CollectionPathForKey(k)
		require.NoError(t, err)
		before, err := sdkDKVSReviewActivePage(client, prefix)
		require.NoError(t, err)
		require.Len(t, before.Records, 1)
		advanceHeightTo(t, dkvs.RecordExpiryHeight(record))
		fresh := dkvsClientForNode(t, f.Network.Core)
		_, err = fresh.GetRecord(k)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
		after, err := sdkDKVSReviewActivePage(fresh, prefix)
		require.NoError(t, err)
		require.Empty(t, after.Records)
		require.GreaterOrEqual(t, after.Meta.ViewHeight, dkvs.RecordExpiryHeight(record))
		require.GreaterOrEqual(t, after.Meta.Generation, before.Meta.Generation)
		recreated := sdkDKVSReviewPutFree(t, fresh, owner, k, []byte("after-expiry"))
		require.Equal(t, uint64(1), recreated.Seq)
		sdkDKVSReviewUnchanged(t, fresh, recreated)
	})
}

func sdkDKVSReviewHeight(t *testing.T, client *wallet.SatsNetDKVSClient) uint64 {
	t.Helper()
	height, err := client.GetBestHeight()
	require.NoError(t, err)
	return height
}

func sdkDKVSReviewPutFree(t *testing.T, client *wallet.SatsNetDKVSClient, actor *dkvsKeyPathActor, key string, value []byte) *wire.DKVSRecord {
	t.Helper()
	record, err := client.PutSignedRecordFreeLocal(actor.Wallet, key, value, dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, client), TTL: 600})
	require.NoError(t, err)
	return record
}

func sdkDKVSReviewFreeRecord(t *testing.T, client *wallet.SatsNetDKVSClient, actor *dkvsKeyPathActor, key string, value []byte, seq uint64) *wire.DKVSRecord {
	t.Helper()
	record, err := wallet.NewDKVSSignedRecord(actor.Wallet, key, value, dkvs.RecordOptions{Seq: seq, IssueHeight: sdkDKVSReviewHeight(t, client), TTL: 600})
	require.NoError(t, err)
	parsed, err := dkvs.ParseKey(key)
	require.NoError(t, err)
	proof, err := dkvs.NewFreeLocalFeeProof(key, parsed.Namespace, wire.MaxDKVSRecordSize, dkvs.RecordExpiryHeight(record))
	require.NoError(t, err)
	require.NoError(t, wallet.AttachDKVSFeeProof(record, proof))
	require.NoError(t, wallet.SignDKVSRecord(actor.Wallet, record))
	return record
}

func sdkDKVSReviewAbsent(record *wire.DKVSRecord) dkvs.CASMutation {
	return dkvs.CASMutation{Record: record, Precondition: dkvs.WritePrecondition{ExpectAbsent: true}}
}

func sdkDKVSReviewExpected(record, previous *wire.DKVSRecord) dkvs.CASMutation {
	hash := dkvs.RecordHash(previous)
	return dkvs.CASMutation{Record: record, Precondition: dkvs.WritePrecondition{ExpectedHash: &hash}}
}

func sdkDKVSReviewUnchanged(t *testing.T, client *wallet.SatsNetDKVSClient, expected *wire.DKVSRecord) {
	t.Helper()
	require.NotNil(t, expected)
	actual, err := client.GetVerifiedRecord(expected.Key, dkvs.RecordVerificationOptions{
		Height: sdkDKVSReviewHeight(t, client), CheckHash: true, ExpectedHash: dkvs.RecordHash(expected),
	})
	require.NoError(t, err)
	require.Equal(t, expected.Value, actual.Value)
	require.Equal(t, expected.Seq, actual.Seq)
}

// Both attempts reach the real node with byte-identical request ID and body.
// Only the first successful acknowledgement is discarded.
type sdkDKVSReviewReplayTransport struct {
	inner        wallet.HttpClient
	attempts     int
	firstApplied int
}

func (p *sdkDKVSReviewReplayTransport) SendGetRequest(url *wallet.URL) ([]byte, error) {
	return p.inner.SendGetRequest(url)
}

func (p *sdkDKVSReviewReplayTransport) SendPostRequest(url *wallet.URL, body []byte) ([]byte, error) {
	first, err := p.inner.SendPostRequest(url, body)
	if err != nil || !strings.HasSuffix(url.Path, "/records/batch-cas") {
		return first, err
	}
	p.attempts++
	var ack struct {
		Code int               `json:"code"`
		Data *dkvs.WriteResult `json:"data"`
	}
	if err := json.Unmarshal(first, &ack); err != nil {
		return nil, err
	}
	if ack.Code != 0 || ack.Data == nil {
		return first, nil
	}
	p.firstApplied = ack.Data.Applied
	p.attempts++
	return p.inner.SendPostRequest(url, body)
}

func sdkDKVSReviewReplayClient(base *wallet.SatsNetDKVSClient, signer common.Wallet) (*wallet.SatsNetDKVSClient, *sdkDKVSReviewReplayTransport) {
	transport := &sdkDKVSReviewReplayTransport{inner: base.Http}
	return wallet.NewSatsNetDKVSClient(base.Scheme, base.Host, base.Proxy, transport).WithWriteSigner(signer), transport
}
