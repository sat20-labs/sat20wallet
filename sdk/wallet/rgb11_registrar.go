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
type RGB11Registrar struct {
	mu        sync.Mutex
	backend   RGB11RegistryBackend
	signer    common.Wallet
	authority dkvsindexer.SystemVerifier
}

func NewRGB11Registrar(backend RGB11RegistryBackend, signer common.Wallet, authority dkvsindexer.SystemVerifier) (*RGB11Registrar, error) {
	if backend == nil || signer == nil || authority == nil {
		return nil, dkvsindexer.ErrPermissionDenied
	}
	if err := authority.CanWriteSystem(rgb11wallet.RGB11RegistryPath+"/"+"1111111111111111111111111111111111111111111111111111111111111111", signer.GetPubKey().SerializeCompressed()); err != nil {
		return nil, err
	}
	return &RGB11Registrar{backend: backend, signer: signer, authority: authority}, nil
}

// Register stores an independently validated public contract. Any caller may
// submit public material through the future Core route; this authority object
// is not exposed by ordinary wallet/WASM APIs and grants no DID name rights.
func (r *RGB11Registrar) Register(content []byte) (*rgb11wallet.RGB11Registration, error) {
	if r == nil {
		return nil, dkvsindexer.ErrPermissionDenied
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	value, proposed, err := rgb11wallet.NewRGB11ContractValue(content)
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
	for n, registration := range registrations {
		if registration.ContractID == proposed.ContractID {
			existing, err := rgb11wallet.DecodeRGB11RegistryValue(snapshot.Records[n].Value)
			if err != nil {
				return nil, err
			}
			if !bytes.Equal(existing.ContractContent, value.ContractContent) {
				return nil, dkvsindexer.ErrWriteConflict
			}
			return registration, nil
		}
	}
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
