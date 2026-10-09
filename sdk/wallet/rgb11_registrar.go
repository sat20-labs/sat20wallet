package wallet

import (
	"bytes"
	"sync"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

// RGB11RegistryBackend is the local authoritative contract store, not a
// wallet replica or an arbitrary serving peer. IndexerMgr implements it.
type RGB11RegistryBackend interface {
	GetDKVSPathSnapshot(string) (*dkvsindexer.PathSnapshot, error)
	PutDKVSInternalContract(*wire.DKVSRecord) (bool, error)
}

// RGB11Registrar is owned once by the selected CoreNode registration service.
// All registrations for this network must pass through that instance. This is
// a single-writer policy, not coordination between separate service processes.
// Ordinals are derived from committed records; no counter or reverse index is
// persisted. Restart must use the same authoritative database.
type RGB11Registrar struct {
	mu        sync.Mutex
	backend   RGB11RegistryBackend
	signer    common.Wallet
	resolver  dkvsindexer.DIDResolver
	authority dkvsindexer.SystemVerifier
}

func NewRGB11Registrar(backend RGB11RegistryBackend, signer common.Wallet, resolver dkvsindexer.DIDResolver, authority dkvsindexer.SystemVerifier) (*RGB11Registrar, error) {
	if backend == nil || signer == nil || resolver == nil || authority == nil {
		return nil, dkvsindexer.ErrPermissionDenied
	}
	if err := authority.CanWriteSystem(rgb11wallet.RGB11RegistryPath+"/"+"1111111111111111111111111111111111111111111111111111111111111111", signer.GetPubKey().SerializeCompressed()); err != nil {
		return nil, err
	}
	return &RGB11Registrar{backend: backend, signer: signer, resolver: resolver, authority: authority}, nil
}

// Register accepts the provider public key from the service's authenticated
// request context. The caller must first validate the bridge/genesis ownership
// evidence; a caller-supplied public key alone is not request authentication.
// Ordinary wallet and WASM APIs do not expose this authority operation.
func (r *RGB11Registrar) Register(provider string, providerPubKey, content []byte) (*RGB11NameRegistration, error) {
	if r == nil {
		return nil, dkvsindexer.ErrPermissionDenied
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := rgb11wallet.ValidatePrimaryDIDName(provider); err != nil {
		return nil, err
	}
	identity, err := r.resolver.ResolveName(provider)
	if err != nil {
		return nil, err
	}
	if !identity.Active || identity.CanonicalName != provider {
		return nil, dkvsindexer.ErrPermissionDenied
	}
	if err := identity.CanSign(providerPubKey); err != nil {
		return nil, err
	}
	value, proposed, err := rgb11wallet.NewRGB11ContractValue(provider, content)
	if err != nil {
		return nil, err
	}
	snapshot, err := r.backend.GetDKVSPathSnapshot(rgb11wallet.RGB11RegistryPath)
	if err != nil {
		return nil, err
	}
	if snapshot == nil || snapshot.PathMeta == nil || snapshot.Path != rgb11wallet.RGB11RegistryPath || snapshot.PathMeta.Path != snapshot.Path || snapshot.PathMeta.ActiveRecords != uint64(len(snapshot.Records)) {
		return nil, dkvsindexer.ErrInvalidSnapshot
	}
	registrations, err := rgb11wallet.ValidateRGB11Registry(snapshot.Records, r.authority)
	if err != nil {
		return nil, err
	}
	ordinal := uint64(1)
	for n, registration := range registrations {
		if registration.ContractID == proposed.ContractID {
			existing, err := rgb11wallet.DecodeRGB11RegistryValue(snapshot.Records[n].Value)
			if err != nil {
				return nil, err
			}
			if existing.ProviderDID != provider || existing.Ticker != value.Ticker || !bytes.Equal(existing.ContractContent, content) {
				return nil, dkvsindexer.ErrWriteConflict
			}
			return registration, nil
		}
		if registration.ProviderDID == provider && registration.BaseTicker == value.Ticker {
			ordinal++
		}
	}
	value.Ordinal = ordinal
	encoded, err := rgb11wallet.EncodeRGB11RegistryValue(value)
	if err != nil {
		return nil, err
	}
	key, err := rgb11wallet.RGB11RegistryKey(proposed.ContractID)
	if err != nil {
		return nil, err
	}
	record, err := NewDKVSSignedRecord(r.signer, key, encoded, dkvsindexer.RecordOptions{Seq: 1, IssueHeight: snapshot.PathMeta.ViewHeight})
	if err != nil {
		return nil, err
	}
	if _, err = r.backend.PutDKVSInternalContract(record); err != nil {
		return nil, err
	}
	return rgb11wallet.VerifyRGB11RegistrationForClient(record, proposed.ContractID, r.authority)
}
