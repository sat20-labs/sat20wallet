package e2e

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	rpcindexer "github.com/sat20-labs/satoshinet/indexer/rpcserver/indexer"
	shareIndexer "github.com/sat20-labs/satoshinet/indexer/share/indexer"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Real SDK -> HTTP -> production SatoshiNet router/admission -> real Indexer
// and Pebble. Only the on-chain Core role and current account mapping are
// fixtures; no write/authorization semantics are reimplemented by the router.
func TestSDKDKVSBoundRPCHTTP(t *testing.T) {
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	key := func(t *testing.T, suffix string) string {
		t.Helper()
		k, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), suffix)
		require.NoError(t, err)
		return k
	}
	put := func(f *boundRPCFixture, k, value string) (*wire.DKVSRecord, error) {
		return f.client.PutSignedRecordFreeLocal(owner.Wallet, k, []byte(value), dkvs.RecordOptions{IssueHeight: 100, TTL: 10})
	}

	t.Run("UnboundWalletIsDeniedEvenWithCorrectEndpoint", func(t *testing.T) {
		f := newBoundRPCFixture(t)
		k := key(t, "bound-deny/value")
		_, err := put(f, k, "must-not-exist")
		require.ErrorIs(t, err, dkvs.ErrPermissionDenied)
		_, err = f.store.Get(k)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
		require.Zero(t, f.unguarded.Load())
	})

	t.Run("CurrentMappingToThisCoreIsTheBinding", func(t *testing.T) {
		f := newBoundRPCFixture(t)
		f.bind(t, owner, f.coreID)
		_, err := put(f, key(t, "bound-current/value"), "allowed")
		require.NoError(t, err)
	})

	t.Run("WrongCoreAndNonCoreAreDenied", func(t *testing.T) {
		f := newBoundRPCFixture(t)
		other, err := btcec.NewPrivateKey()
		require.NoError(t, err)
		f.bind(t, owner, hex.EncodeToString(other.PubKey().SerializeCompressed()))
		_, err = put(f, key(t, "bound-wrong-core/value"), "denied")
		require.ErrorIs(t, err, dkvs.ErrPermissionDenied)
		f.bind(t, owner, f.coreID)
		f.core.Store(false)
		_, err = put(f, key(t, "bound-non-core/value"), "denied")
		require.ErrorIs(t, err, dkvs.ErrPermissionDenied)
	})

	t.Run("BoundWalletCRUDRenewalAndPhysicalDelete", func(t *testing.T) {
		f := newBoundRPCFixture(t)
		f.bind(t, owner, f.coreID)
		k := key(t, "bound-basic/value")
		first, err := put(f, k, "v1")
		require.NoError(t, err)
		second, err := put(f, k, "v2")
		require.NoError(t, err)
		require.Equal(t, first.Seq+1, second.Seq)
		renewed, err := f.client.RenewRecord(owner.Wallet, second, dkvs.RecordOptions{IssueHeight: 100, TTL: 30})
		require.NoError(t, err)
		require.Equal(t, second.Seq+1, renewed.Seq)
		command, err := f.client.DeleteCurrentRecord(owner.Wallet, k, 100)
		require.NoError(t, err)
		state, err := f.client.GetKeyState(k)
		require.NoError(t, err)
		require.Equal(t, dkvs.KeyStateNeverSeen, state.Status)
		writer := f.client.WithWriteSigner(owner.Wallet)
		_, err = writer.PutRecordBatchCAS([]dkvs.CASMutation{sdkDKVSReviewExpected(command, renewed)})
		require.NoError(t, err)
		recreated, err := put(f, k, "recreated")
		require.NoError(t, err)
		require.Equal(t, uint64(1), recreated.Seq)
		_, err = writer.PutRecordBatchCAS([]dkvs.CASMutation{sdkDKVSReviewExpected(command, renewed)})
		require.ErrorIs(t, err, dkvs.ErrWriteConflict)
		require.Zero(t, f.unguarded.Load())
	})

	t.Run("InvalidSignatureCannotBorrowBoundIdentityOrPartiallyCommit", func(t *testing.T) {
		f := newBoundRPCFixture(t)
		f.bind(t, owner, f.coreID)
		one := sdkDKVSReviewFreeRecord(t, f.client, owner, key(t, "bound-batch/one"), []byte("valid"), 1)
		two := sdkDKVSReviewFreeRecord(t, f.client, owner, key(t, "bound-batch/two"), []byte("invalid"), 1)
		two.Signature[0] ^= 1
		_, err := f.client.WithWriteSigner(owner.Wallet).PutRecordBatchCAS([]dkvs.CASMutation{sdkDKVSReviewAbsent(one), sdkDKVSReviewAbsent(two)})
		require.Error(t, err)
		for _, k := range []string{one.Key, two.Key} {
			_, err := f.store.Get(k)
			require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
		}
		require.Zero(t, f.unguarded.Load())
	})

	t.Run("BindingChangedBeforeCommitRejectsWholeBatch", func(t *testing.T) {
		f := newBoundRPCFixture(t)
		f.bind(t, owner, f.coreID)
		other, err := btcec.NewPrivateKey()
		require.NoError(t, err)
		f.afterBindingRead = func() { f.bind(t, owner, hex.EncodeToString(other.PubKey().SerializeCompressed())) }
		k := key(t, "bound-race/value")
		_, err = put(f, k, "must-not-commit")
		require.ErrorIs(t, err, dkvs.ErrPermissionDenied)
		_, err = f.store.Get(k)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
	})

	t.Run("StandaloneBindingWriteCanRefreshButCannotRedirect", func(t *testing.T) {
		f := newBoundRPCFixture(t)
		binding := f.bind(t, owner, f.coreID)

		// /account is the one bootstrap exception. A standalone signed refresh
		// that still targets this CoreNode is a binding operation, not a generic
		// business KV write.
		refreshed := *binding
		refreshed.Seq++
		require.NoError(t, wallet.SignDKVSAccountRecord(owner.Wallet, &refreshed))
		_, err := f.client.PutRecordBatchCAS([]dkvs.CASMutation{sdkDKVSReviewExpected(&refreshed, binding)})
		require.NoError(t, err)

		other, err := btcec.NewPrivateKey()
		require.NoError(t, err)
		account := dkvs.AccountID(owner.Wallet.GetPubKey().SerializeCompressed())
		value, err := dkvs.EncodeAccountServiceDescriptor(dkvs.AccountServiceDescriptor{
			AccountID: account, CoreNodeID: hex.EncodeToString(other.PubKey().SerializeCompressed()),
		})
		require.NoError(t, err)
		redirected, err := wallet.NewDKVSAccountSignedRecord(owner.Wallet, binding.Key, value,
			dkvs.RecordOptions{Seq: refreshed.Seq + 1, IssueHeight: 100})
		require.NoError(t, err)
		_, err = f.client.PutRecordBatchCAS([]dkvs.CASMutation{sdkDKVSReviewExpected(redirected, &refreshed)})
		require.ErrorIs(t, err, dkvs.ErrPermissionDenied)

		current, err := f.store.Get(binding.Key)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(&refreshed), dkvs.RecordHash(current))
	})

	t.Run("NonCoreAcceptsP2PButNotWalletRPC", func(t *testing.T) {
		f := newBoundRPCFixture(t)
		f.core.Store(false)
		k := key(t, "bound-p2p/value")
		source := newReleaseReviewStore(t)
		record, err := source.client.PutSignedRecordWithAutopay(owner.Wallet, k, []byte("replicated"), dkvs.RecordOptions{IssueHeight: 100}, releaseReviewAutopay())
		require.NoError(t, err)
		applied, err := f.store.AcceptCurrentRecord(record)
		require.NoError(t, err)
		require.True(t, applied)
		actual, err := f.client.GetRecord(k)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(record), dkvs.RecordHash(actual))
		_, err = f.client.PutSignedRecordWithAutopay(owner.Wallet, k, []byte("rpc-denied"), dkvs.RecordOptions{IssueHeight: 100}, releaseReviewAutopay())
		require.ErrorIs(t, err, dkvs.ErrPermissionDenied)
	})

	t.Run("LoopbackCannotBypassThroughSnapshotImport", func(t *testing.T) {
		f := newBoundRPCFixture(t)
		for _, path := range []string{"/v3/dkvs/snapshot", "/v3/dkvs/prune", "/v3/dkvs/record"} {
			response, err := f.server.Client().Post(f.server.URL+"/testnet"+path, "application/json", bytes.NewBufferString("{}"))
			require.NoError(t, err)
			require.Equal(t, http.StatusNotFound, response.StatusCode)
			response.Body.Close()
		}
	})
}

type boundRPCFixture struct {
	shareIndexer.Indexer
	store            *dkvs.Indexer
	client           *wallet.SatsNetDKVSClient
	server           *httptest.Server
	coreID           string
	core             atomic.Bool
	height           atomic.Uint64
	mu               sync.Mutex
	afterBindingRead func()
	unguarded        atomic.Int64
	policy           *dkvs.WalletRPCAdmission
}

func newBoundRPCFixture(t *testing.T) *boundRPCFixture {
	t.Helper()
	core, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	f := &boundRPCFixture{coreID: hex.EncodeToString(core.PubKey().SerializeCompressed())}
	f.core.Store(true)
	f.height.Store(100)
	db := indexerdb.NewKVDB(t.TempDir())
	require.NotNil(t, db)
	t.Cleanup(func() { db.Close() })
	f.store = dkvs.New(db, dkvs.Config{EndpointID: f.coreID, AllowFreeLocal: true,
		FreeLocalCache: dkvs.DefaultFreeLocalCachePolicy(), CurrentHeight: f.height.Load,
		FeeVerifier: releaseReviewFeeVerifier{JSONFeeVerifier: dkvs.JSONFeeVerifier{AllowFreeLocal: true}, height: &f.height}})
	f.policy = &dkvs.WalletRPCAdmission{Indexer: f.store, IsCoreNode: f.core.Load, CurrentBinding: f.currentBinding}
	router := gin.New()
	rpcindexer.NewService(f).InitRouter(router, "/testnet")
	router.GET("/testnet/btc/block/bestblockheight", func(c *gin.Context) { c.JSON(200, gin.H{"code": 0, "msg": "ok", "data": f.height.Load()}) })
	f.server = httptest.NewServer(router)
	t.Cleanup(f.server.Close)
	u, err := url.Parse(f.server.URL)
	require.NoError(t, err)
	f.client = wallet.NewSatsNetDKVSClient(u.Scheme, u.Host, "testnet", &wallet.NetClient{Client: f.server.Client()})
	return f
}

func (f *boundRPCFixture) bindingKey(t *testing.T, owner *dkvsKeyPathActor) string {
	t.Helper()
	address, err := dkvs.P2TRAddressFromPubKeyBytes(owner.Wallet.GetPubKey().SerializeCompressed(), &chaincfg.TestNetParams)
	require.NoError(t, err)
	key, err := dkvs.AccountMappingKey("testnet", address)
	require.NoError(t, err)
	return key
}

func (f *boundRPCFixture) bind(t *testing.T, owner *dkvsKeyPathActor, core string) *wire.DKVSRecord {
	t.Helper()
	account := dkvs.AccountID(owner.Wallet.GetPubKey().SerializeCompressed())
	key := f.bindingKey(t, owner)
	seq := uint64(1)
	if old, err := f.store.Get(key); err == nil {
		seq = old.Seq + 1
	}
	value, err := dkvs.EncodeAccountServiceDescriptor(dkvs.AccountServiceDescriptor{AccountID: account, CoreNodeID: core})
	require.NoError(t, err)
	record, err := wallet.NewDKVSAccountSignedRecord(owner.Wallet, key, value, dkvs.RecordOptions{Seq: seq, IssueHeight: 100})
	require.NoError(t, err)
	_, err = f.store.PutLocal(record)
	require.NoError(t, err)
	return record
}

func (f *boundRPCFixture) currentBinding(account string) (*wire.DKVSRecord, error) {
	pub, err := dkvs.AccountPubKey(account)
	if err != nil {
		return nil, err
	}
	address, err := dkvs.P2TRAddressFromPubKeyBytes(pub, &chaincfg.TestNetParams)
	if err != nil {
		return nil, err
	}
	key, err := dkvs.AccountMappingKey("testnet", address)
	if err != nil {
		return nil, err
	}
	record, err := f.store.Get(key)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	hook := f.afterBindingRead
	f.afterBindingRead = nil
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return record, nil
}
func (f *boundRPCFixture) GetDKVSClientConfig() dkvs.ClientConfig { return f.store.ClientConfig() }
func (f *boundRPCFixture) GetDKVSFreeLocalCachePolicy() dkvs.FreeLocalCachePolicy {
	return f.store.ClientConfig().FreeLocal
}
func (f *boundRPCFixture) GetDKVSRecord(key string) (*wire.DKVSRecord, error) {
	return f.store.Get(key)
}
func (f *boundRPCFixture) GetDKVSKeyState(key string) (dkvs.DKVSKeyState, error) {
	return f.store.GetKeyState(key)
}
func (f *boundRPCFixture) PutDKVSRecordBatchCASAuthorized(m []dkvs.CASMutation, o dkvs.BatchCASOptions, authorization *dkvs.WalletWriteAuthorization) (*dkvs.WriteResult, error) {
	return f.policy.PutRecords(m, o, authorization)
}
func (f *boundRPCFixture) PutDKVSRecordBatchCASResultWithOptions([]dkvs.CASMutation, dkvs.BatchCASOptions) (*dkvs.WriteResult, error) {
	f.unguarded.Add(1)
	return nil, fmt.Errorf("unguarded writer must not be reached from RPC")
}
