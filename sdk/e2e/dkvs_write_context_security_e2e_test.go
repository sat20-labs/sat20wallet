package e2e

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	core "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// This tap preserves the exact request produced by the public SDK. Replays
// below reuse those bytes, not a server-side mock or newly signed operation.
type writeContextSecurityTap struct {
	inner             wallet.HttpClient
	request           []byte
	beforeWrite       func()
	writeContextCalls int
}

func (c *writeContextSecurityTap) SendGetRequest(url *wallet.URL) ([]byte, error) {
	return c.inner.SendGetRequest(url)
}
func (c *writeContextSecurityTap) SendPostRequest(url *wallet.URL, body []byte) ([]byte, error) {
	if strings.Contains(url.Path, "/write-context") {
		c.writeContextCalls++
	}
	if strings.HasSuffix(url.Path, "/records/batch-cas") {
		c.request = bytes.Clone(body)
		if c.beforeWrite != nil {
			hook := c.beforeWrite
			c.beforeWrite = nil
			hook()
		}
	}
	return c.inner.SendPostRequest(url, body)
}

func TestSDKDKVSWriteContextSecurity(t *testing.T) {
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	fixture := func(t *testing.T) (*boundRPCFixture, *wallet.SatsNetDKVSClient, *writeContextSecurityTap, string) {
		t.Helper()
		f := newBoundRPCFixture(t)
		f.bind(t, owner, f.coreID)
		tap := &writeContextSecurityTap{inner: f.client.Http}
		writer := wallet.NewSatsNetDKVSClient(f.client.Scheme, f.client.Host, f.client.Proxy, tap).WithWriteSigner(owner.Wallet)
		key, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), "write-context/value")
		require.NoError(t, err)
		return f, writer, tap, key
	}
	put := func(t *testing.T, writer *wallet.SatsNetDKVSClient, key string) *dkvs.Record {
		t.Helper()
		r, err := writer.PutSignedRecordFreeLocal(owner.Wallet, key, []byte("value"), dkvs.RecordOptions{IssueHeight: 100, TTL: 100})
		require.NoError(t, err)
		return r
	}
	replay := func(t *testing.T, f *boundRPCFixture, body []byte) (int, string, *dkvs.WriteResult) {
		t.Helper()
		raw, err := f.client.Http.SendPostRequest(f.client.GetUrl("/v3/dkvs/records/batch-cas"), body)
		// NetClient returns a structured HTTP error for a rejected request.
		if err != nil {
			var response *wallet.HTTPResponseError
			require.ErrorAs(t, err, &response)
			require.GreaterOrEqual(t, response.StatusCode, 400)
			require.Less(t, response.StatusCode, 500)
			return -1, "http-rejected", nil
		}
		var result struct {
			Code      int               `json:"code"`
			ErrorCode string            `json:"error_code"`
			Data      *dkvs.WriteResult `json:"data"`
		}
		require.NoError(t, json.Unmarshal(raw, &result))
		return result.Code, result.ErrorCode, result.Data
	}
	assertAbsent := func(t *testing.T, f *boundRPCFixture, key string) {
		t.Helper()
		_, err := f.client.GetRecordDirect(key)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
	}

	t.Run("ExactPresentRetryDoesNotAdvanceGeneration", func(t *testing.T) {
		f, writer, tap, key := fixture(t)
		current := put(t, writer, key)
		request := bytes.Clone(tap.request)
		prefix, err := dkvs.CollectionPathForKey(key)
		require.NoError(t, err)
		before, err := f.store.GetPathMeta(prefix)
		require.NoError(t, err)
		code, _, result := replay(t, f, request)
		require.Zero(t, code)
		require.NotNil(t, result)
		require.Zero(t, result.Applied)
		after, err := f.store.GetPathMeta(prefix)
		require.NoError(t, err)
		require.Equal(t, before.EndpointGeneration, after.EndpointGeneration)
		actual, err := f.client.GetRecordDirect(key)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(current), dkvs.RecordHash(actual))
	})

	t.Run("CapturedAuthorizedCreateCannotResurrectSameBlockDeletion", func(t *testing.T) {
		f, writer, tap, key := fixture(t)
		put(t, writer, key)
		captured := bytes.Clone(tap.request)
		_, err := writer.DeleteCurrentRecord(owner.Wallet, key, 100)
		require.NoError(t, err)
		assertAbsent(t, f, key)
		code, _, _ := replay(t, f, captured)
		require.NotZero(t, code, "even a fully signed captured request must be stale after deletion")
		assertAbsent(t, f, key)
	})

	t.Run("NewOwnerIntentMayRecreateAtSequenceOne", func(t *testing.T) {
		f, writer, tap, key := fixture(t)
		old := put(t, writer, key)
		captured := bytes.Clone(tap.request)
		_, err := writer.DeleteCurrentRecord(owner.Wallet, key, 100)
		require.NoError(t, err)
		fresh, err := writer.PutSignedRecordFreeLocal(owner.Wallet, key, []byte("new-life"), dkvs.RecordOptions{IssueHeight: 100, TTL: 100})
		require.NoError(t, err)
		require.Equal(t, uint64(1), fresh.Seq)
		require.Equal(t, old.IssueHeight, fresh.IssueHeight)
		code, _, _ := replay(t, f, captured)
		require.NotZero(t, code)
		actual, err := f.client.GetRecordDirect(key)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(fresh), dkvs.RecordHash(actual))
	})

	for _, field := range []string{"generation", "request-id", "cas", "missing-authorization"} {
		t.Run("TamperedEnvelope_"+field, func(t *testing.T) {
			f, writer, tap, key := fixture(t)
			put(t, writer, key)
			var request core.BatchCASRequest
			require.NoError(t, json.Unmarshal(tap.request, &request))
			require.NotNil(t, request.Authorization)
			_, err := writer.DeleteCurrentRecord(owner.Wallet, key, 100)
			require.NoError(t, err)
			switch field {
			case "generation":
				request.Authorization.Context.Prefixes[0].Generation += 2
			case "request-id":
				request.RequestID += "-forged"
			case "cas":
				request.Mutations[0].ExpectAbsent = false
				request.Mutations[0].ExpectedETag = strings.Repeat("0", 64)
			case "missing-authorization":
				request.Authorization = nil
			}
			encoded, err := json.Marshal(request)
			require.NoError(t, err)
			code, _, _ := replay(t, f, encoded)
			require.NotZero(t, code)
			assertAbsent(t, f, key)
		})
	}

	t.Run("PutDoesNotUseWriteContextEndpoint", func(t *testing.T) {
		_, writer, tap, key := fixture(t)
		put(t, writer, key)
		require.Zero(t, tap.writeContextCalls)
		var request core.BatchCASRequest
		require.NoError(t, json.Unmarshal(tap.request, &request))
		require.NotNil(t, request.Authorization)
		require.NotEmpty(t, request.Authorization.Context.Prefixes)
	})

	for _, offset := range []uint64{0, 1, 2, 3} {
		t.Run("IssueHeightWindow_"+string(rune('0'+offset)), func(t *testing.T) {
			f, writer, _, key := fixture(t)
			_, err := writer.PutSignedRecordFreeLocal(owner.Wallet, key, []byte("bounded-height"), dkvs.RecordOptions{IssueHeight: 100 - offset, TTL: 100})
			if offset <= 2 {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, dkvs.ErrStaleEndpoint)
				assertAbsent(t, f, key)
			}
		})
	}

	t.Run("HeightIsRecheckedAtActualCommit", func(t *testing.T) {
		f, writer, tap, key := fixture(t)
		tap.beforeWrite = func() { f.height.Store(103) }
		_, err := writer.PutSignedRecordFreeLocal(owner.Wallet, key, []byte("late"), dkvs.RecordOptions{IssueHeight: 100, TTL: 100})
		require.ErrorIs(t, err, dkvs.ErrStaleEndpoint)
		assertAbsent(t, f, key)
	})

	t.Run("AuthorizationDoesNotBypassStrictSequence", func(t *testing.T) {
		f, writer, _, key := fixture(t)
		current := put(t, writer, key)
		bad := sdkDKVSReviewFreeRecord(t, writer, owner, key, []byte("skip"), current.Seq+2)
		_, err := writer.PutRecordCAS(bad, sdkDKVSReviewExpected(bad, current).Precondition)
		require.ErrorIs(t, err, dkvs.ErrInvalidSequence)
		actual, err := f.client.GetRecordDirect(key)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(current), dkvs.RecordHash(actual))
	})

	t.Run("DiskReopenPreservesExactSignedAuthorization", func(t *testing.T) {
		_, writer, tap, key := fixture(t)
		put(t, writer, key)
		var request core.BatchCASRequest
		require.NoError(t, json.Unmarshal(tap.request, &request))
		require.NotNil(t, request.Authorization)
		mutations := []dkvs.CASMutation{sdkDKVSReviewAbsent(request.Mutations[0].Record)}
		entry, err := core.NewBatchOutboxEntry("auth-reopen", mutations, request.EndpointID, core.OutboxOrigin{})
		require.NoError(t, err)
		entry.RequestID, entry.Authorization = request.RequestID, request.Authorization
		directory := t.TempDir()
		db := indexerdb.NewKVDB(directory)
		require.NotNil(t, db)
		store := core.NewReplicaStore(db)
		require.NoError(t, store.QueueOutbox(entry))
		require.NoError(t, db.Close())
		db = indexerdb.NewKVDB(directory)
		require.NotNil(t, db)
		t.Cleanup(func() { _ = db.Close() })
		entries, err := core.NewReplicaStore(db).LoadOutbox("auth-reopen")
		require.NoError(t, err)
		require.Len(t, entries, 1)
		require.Equal(t, request.Authorization, entries[0].Authorization)
		require.Equal(t, request.RequestID, entries[0].RequestID)
		_, err = dkvs.VerifyWalletWriteAuthorization(mutations, dkvs.BatchCASOptions{EndpointID: request.EndpointID, RequestID: request.RequestID}, entries[0].Authorization)
		require.NoError(t, err)
	})
}
