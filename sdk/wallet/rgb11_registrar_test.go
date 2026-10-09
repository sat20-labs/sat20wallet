package wallet

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

type registrarTestBackend struct {
	secondRead chan struct{}
	indexer    *dkvsindexer.Indexer
	reads      atomic.Int64
	puts       atomic.Int64
	entered    chan struct{}
	release    chan struct{}
	failure    error
}

func (b *registrarTestBackend) GetDKVSPathSnapshot(prefix string) (*dkvsindexer.PathSnapshot, error) {
	n := b.reads.Add(1)
	snapshot, err := b.indexer.GetPathSnapshot(prefix)
	if n == 2 && b.secondRead != nil {
		close(b.secondRead)
	}
	return snapshot, err
}
func (b *registrarTestBackend) PutDKVSInternalContract(r *wire.DKVSRecord) (bool, error) {
	if b.puts.Add(1) == 1 && b.entered != nil {
		close(b.entered)
		<-b.release
	}
	if b.failure != nil {
		return false, b.failure
	}
	return b.indexer.PutInternalContract(r)
}
func newRegistrarTest(t *testing.T, backend *registrarTestBackend) *RGB11Registrar {
	t.Helper()
	core := NewInternalWalletWithMnemonic(rgb11RegistryTestMnemonic, "", &chaincfg.TestNet4Params)
	authority := dkvsindexer.StaticSystemVerifier{Keys: [][]byte{core.GetPubKey().SerializeCompressed()}}
	resolver := dkvsindexer.StaticDIDResolver{Names: map[string]dkvsindexer.DIDIdentity{
		"alice":   {CanonicalName: "alice", Active: true, SigningKeys: [][]byte{core.GetPubKey().SerializeCompressed()}},
		"company": {CanonicalName: "company", Active: true, SigningKeys: [][]byte{core.GetPubKey().SerializeCompressed()}},
	}}
	registrar, err := NewRGB11Registrar(backend, core, resolver, authority)
	require.NoError(t, err)
	return registrar
}
func registrarContent(t *testing.T, r *wire.DKVSRecord) []byte {
	t.Helper()
	v, e := rgb11wallet.DecodeRGB11RegistryValue(r.Value)
	require.NoError(t, e)
	return v.ContractContent
}
func TestRGB11RegistrarConcurrentAllocation(t *testing.T) {
	source, makeRecord := newRGB11RegistryE2ESource(t)
	backend := &registrarTestBackend{indexer: source.indexer, entered: make(chan struct{}), release: make(chan struct{}), secondRead: make(chan struct{})}
	registrar := newRegistrarTest(t, backend)
	pub := registrar.signer.GetPubKey().SerializeCompressed()
	contentA := registrarContent(t, makeRecord("alice", "USD", "f", 1, 1101))
	contentB := registrarContent(t, makeRecord("alice", "USD", "o", 1, 1102))
	type result struct {
		reg *RGB11NameRegistration
		err error
	}
	results := make(chan result, 2)
	go func() { reg, e := registrar.Register("alice", pub, contentA); results <- result{reg, e} }()
	select {
	case <-backend.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("first registration never reached commit")
	}
	go func() { reg, e := registrar.Register("alice", pub, contentB); results <- result{reg, e} }()
	// Hold the first commit so the old unsynchronized allocation deterministically
	// reads the empty view for both different ContractIDs.
	select {
	case <-backend.secondRead:
	case <-time.After(2 * time.Second):
	}
	close(backend.release)
	ordinals := map[uint64]bool{}
	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			require.NoError(t, r.err)
			require.False(t, ordinals[r.reg.Ordinal], "duplicate allocated ordinal")
			ordinals[r.reg.Ordinal] = true
		case <-time.After(10 * time.Second):
			t.Fatal("registration deadlocked")
		}
	}
	require.Equal(t, map[uint64]bool{1: true, 2: true}, ordinals)
	snapshot, e := source.indexer.GetPathSnapshot(rgb11wallet.RGB11RegistryPath)
	require.NoError(t, e)
	_, e = rgb11wallet.ValidateRGB11Registry(snapshot.Records, registrar.authority)
	require.NoError(t, e)
}
func TestRGB11RegistrarRestartRetryAndFailedCommit(t *testing.T) {
	source, makeRecord := newRGB11RegistryE2ESource(t)
	backend := &registrarTestBackend{indexer: source.indexer}
	r := newRegistrarTest(t, backend)
	pub := r.signer.GetPubKey().SerializeCompressed()
	a := registrarContent(t, makeRecord("alice", "USD", "f", 1, 1201))
	b := registrarContent(t, makeRecord("alice", "USD", "o", 1, 1202))
	backend.failure = errors.New("injected commit failure")
	_, e := r.Register("alice", pub, a)
	require.ErrorContains(t, e, "injected commit failure")
	backend.failure = nil
	first, e := r.Register("alice", pub, a)
	require.NoError(t, e)
	require.EqualValues(t, 1, first.Ordinal)
	attempts := backend.puts.Load()
	retry, e := r.Register("alice", pub, a)
	require.NoError(t, e)
	require.Equal(t, first, retry)
	require.Equal(t, attempts, backend.puts.Load())
	restarted := newRegistrarTest(t, backend)
	second, e := restarted.Register("alice", pub, b)
	require.NoError(t, e)
	require.Equal(t, "rgb11:o:usd_2@alice", second.AssetName)
	_, e = restarted.Register("company", pub, a)
	require.ErrorIs(t, e, dkvsindexer.ErrWriteConflict)
	_, e = restarted.Register("alice", []byte{1, 2, 3}, b)
	require.ErrorIs(t, e, dkvsindexer.ErrPermissionDenied)
	_, e = restarted.Register("unowned", pub, b)
	require.Error(t, e)
	_, e = restarted.Register("alice", pub, []byte("not an RGB contract"))
	require.Error(t, e)
	snapshot, e := source.indexer.GetPathSnapshot(rgb11wallet.RGB11RegistryPath)
	require.NoError(t, e)
	require.Len(t, snapshot.Records, 2)
}
func TestRGB11RegistryBusinessRejectsOrdinalGapsAndCollisions(t *testing.T) {
	source, makeRecord := newRGB11RegistryE2ESource(t)
	r := newRegistrarTest(t, &registrarTestBackend{indexer: source.indexer})
	first := makeRecord("alice", "USD", "f", 1, 1301)
	second := makeRecord("alice", "USD", "o", 2, 1302)
	third := makeRecord("alice", "USD", "f", 3, 1303)
	duplicate := makeRecord("alice", "USD", "o", 1, 1304)
	_, e := rgb11wallet.ValidateRGB11Registry([]*wire.DKVSRecord{first, third}, r.authority)
	require.ErrorIs(t, e, dkvsindexer.ErrInvalidSequence)
	_, e = rgb11wallet.ValidateRGB11Registry([]*wire.DKVSRecord{first, duplicate}, r.authority)
	require.ErrorIs(t, e, dkvsindexer.ErrWriteConflict)
	_, e = rgb11wallet.ValidateRGB11Registry([]*wire.DKVSRecord{first, first}, r.authority)
	require.ErrorIs(t, e, dkvsindexer.ErrInvalidRecord)
	_, e = rgb11wallet.ValidateRGB11Registry([]*wire.DKVSRecord{second, first, third}, r.authority)
	require.NoError(t, e)
	// DKVS deliberately stores trusted opaque bytes, even when a business
	// collection is invalid. The registrar refuses to allocate from that view.
	_, e = source.indexer.PutInternalContract(third)
	require.NoError(t, e)
	_, e = r.Register("alice", r.signer.GetPubKey().SerializeCompressed(), registrarContent(t, first))
	require.ErrorIs(t, e, dkvsindexer.ErrInvalidSequence)
	snapshot, e := source.indexer.GetPathSnapshot(rgb11wallet.RGB11RegistryPath)
	require.NoError(t, e)
	require.Len(t, snapshot.Records, 1)
}
