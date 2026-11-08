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
	indexer *dkvsindexer.Indexer
	reads   atomic.Int64
	puts    atomic.Int64
	entered chan struct{}
	release chan struct{}
	failure error
}

func (b *registrarTestBackend) GetDKVSPathSnapshot(prefix string) (*dkvsindexer.PathSnapshot, error) {
	b.reads.Add(1)
	return b.indexer.GetPathSnapshot(prefix)
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
	registrar, err := NewRGB11Registrar(backend, core, authority)
	require.NoError(t, err)
	return registrar
}
func registrarContent(t *testing.T, r *wire.DKVSRecord) []byte {
	t.Helper()
	v, err := rgb11wallet.DecodeRGB11RegistryValue(r.Value)
	require.NoError(t, err)
	return v.ContractContent
}

func TestRGB11RegistrarConcurrentTechnicalContracts(t *testing.T) {
	source, makeRecord := newRGB11RegistryE2ESource(t)
	backend := &registrarTestBackend{indexer: source.indexer, entered: make(chan struct{}), release: make(chan struct{})}
	registrar := newRegistrarTest(t, backend)
	contentA := registrarContent(t, makeRecord("alice", "USD", "f", 1, 1101))
	contentB := registrarContent(t, makeRecord("someone-else", "USD", "f", 1, 1102))
	type result struct {
		reg *RGB11TechnicalRegistration
		err error
	}
	results := make(chan result, 2)
	go func() { reg, err := registrar.Register(contentA); results <- result{reg, err} }()
	select {
	case <-backend.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("first technical registration never reached commit")
	}
	go func() { reg, err := registrar.Register(contentB); results <- result{reg, err} }()
	close(backend.release)
	ids := make(map[string]bool)
	for i := 0; i < 2; i++ {
		select {
		case result := <-results:
			require.NoError(t, result.err)
			require.False(t, ids[result.reg.ContractID])
			ids[result.reg.ContractID] = true
			require.Equal(t, "usd", result.reg.BaseTicker)
		case <-time.After(10 * time.Second):
			t.Fatal("registration deadlocked")
		}
	}
	require.Len(t, ids, 2, "same ticker must not merge distinct ContractIDs")
	snapshot, err := source.indexer.GetPathSnapshot(rgb11wallet.RGB11RegistryPath)
	require.NoError(t, err)
	_, err = rgb11wallet.ValidateRGB11Registry(snapshot.Records, registrar.authority)
	require.NoError(t, err)
}

func TestRGB11RegistrarRetryAndFailedCommit(t *testing.T) {
	source, makeRecord := newRGB11RegistryE2ESource(t)
	backend := &registrarTestBackend{indexer: source.indexer}
	registrar := newRegistrarTest(t, backend)
	a := registrarContent(t, makeRecord("alice", "USD", "f", 1, 1201))
	b := registrarContent(t, makeRecord("unowned", "USD", "f", 1, 1202))
	backend.failure = errors.New("injected commit failure")
	_, err := registrar.Register(a)
	require.ErrorContains(t, err, "injected commit failure")
	backend.failure = nil
	first, err := registrar.Register(a)
	require.NoError(t, err)
	puts := backend.puts.Load()
	retry, err := registrar.Register(a)
	require.NoError(t, err)
	require.Equal(t, first, retry)
	require.Equal(t, puts, backend.puts.Load())
	other, err := newRegistrarTest(t, backend).Register(b)
	require.NoError(t, err, "technical registration is not issuer-DID gated")
	require.NotEqual(t, first.ContractID, other.ContractID)
	_, err = registrar.Register([]byte("not an RGB contract"))
	require.Error(t, err)
	snapshot, err := source.indexer.GetPathSnapshot(rgb11wallet.RGB11RegistryPath)
	require.NoError(t, err)
	require.Len(t, snapshot.Records, 2)
}

func TestRGB11RegistryRejectsDuplicateTechnicalKey(t *testing.T) {
	source, makeRecord := newRGB11RegistryE2ESource(t)
	registrar := newRegistrarTest(t, &registrarTestBackend{indexer: source.indexer})
	first := makeRecord("alice", "USD", "f", 1, 1301)
	second := makeRecord("company", "USD", "f", 1, 1302)
	_, err := rgb11wallet.ValidateRGB11Registry([]*wire.DKVSRecord{first, second}, registrar.authority)
	require.NoError(t, err, "ticker collision belongs to naming, not technical registration")
	_, err = rgb11wallet.ValidateRGB11Registry([]*wire.DKVSRecord{first, first}, registrar.authority)
	require.ErrorIs(t, err, dkvsindexer.ErrInvalidRecord)
}
