package wallet

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

type accountManagedDataSnapshot struct {
	Catalog  AccountManagedDataCatalog
	Bundle   account.ManagedDataBundle
	Hash     string
	Envelope []byte
}

func (p *Manager) buildAccountManagedDataSnapshot(secret []byte, accountID string,
	revision uint64) (*accountManagedDataSnapshot, error) {

	catalog, err := p.accountManagedDataCatalog()
	if err != nil {
		return nil, err
	}
	bundle, hash, err := p.exportAccountManagedData(catalog, revision)
	if err != nil {
		return nil, err
	}
	envelope, err := account.SealManagedDataBundle(secret, accountID, bundle, nil)
	if err != nil {
		return nil, err
	}
	return &accountManagedDataSnapshot{
		Catalog: catalog, Bundle: bundle, Hash: hash, Envelope: envelope,
	}, nil
}

func verifyAccountManagedDataReference(state account.ManagedState,
	bundle account.ManagedDataBundle, expectedHash string) error {

	if state.DataRevision == 0 {
		if state.DataHash != "" || len(bundle.Items) != 0 {
			return fmt.Errorf("managed data reference is inconsistent")
		}
		return nil
	}
	if bundle.Revision != state.DataRevision || expectedHash == "" ||
		state.DataHash != expectedHash {
		return fmt.Errorf("managed data reference does not match account state")
	}
	return nil
}

func openAccountManagedDataValue(secret []byte, accountID string, value *dkvsValue,
	state account.ManagedState) (*accountManagedDataSnapshot, error) {

	if state.DataRevision == 0 {
		return &accountManagedDataSnapshot{
			Bundle: account.ManagedDataBundle{
				Version: account.ManagedDataBundleVersion, Revision: 1,
			},
		}, nil
	}
	if value == nil {
		return nil, fmt.Errorf("account-managed data blob is unavailable")
	}
	blob, err := DecodeDKVSBlobValue(value.Value)
	if err != nil {
		return nil, err
	}
	bundle, err := account.OpenManagedDataBundle(secret, accountID, blob.Data)
	if err != nil {
		return nil, err
	}
	hash, err := accountManagedDataContentHash(bundle.Items)
	if err != nil {
		return nil, err
	}
	if err := verifyAccountManagedDataReference(state, bundle, hash); err != nil {
		return nil, err
	}
	return &accountManagedDataSnapshot{Bundle: bundle, Hash: hash,
		Envelope: append([]byte(nil), blob.Data...)}, nil
}

func (p *Manager) importAccountManagedDataSnapshot(value *accountManagedDataSnapshot) error {
	if value == nil {
		return nil
	}
	catalog, err := p.accountManagedDataCatalog()
	if err != nil {
		return err
	}
	value.Catalog = catalog
	return p.importAccountManagedData(catalog, value.Bundle)
}

func accountManagementMutations(profile *accountManagementProfile, root common.Wallet,
	stateKey string, stateEnvelope []byte, dataKey string, dataEnvelope []byte,
	writeData bool) ([]dkvsValueMutation, error) {

	stateMutation, err := accountStateMutation(profile, root, stateKey, stateEnvelope)
	if err != nil {
		return nil, err
	}
	mutations := []dkvsValueMutation{stateMutation}
	if writeData {
		dataMutation, err := accountManagedDataMutation(profile, root, dataKey, dataEnvelope)
		if err != nil {
			return nil, err
		}
		mutations = append(mutations, dataMutation)
	}
	return mutations, nil
}

func accountRecordMatchesStorage(value *dkvsValue, profile *accountManagementProfile) bool {
	if value == nil || value.record == nil || profile == nil {
		return false
	}
	proof, err := dkvsindexer.ParseFeeProof(value.record.FeeProof)
	if err != nil {
		return false
	}
	switch profile.StorageMode {
	case AccountStorageTemporary:
		return proof.Mode == dkvsindexer.FeeModeFreeLocal &&
			value.record.TTL == profile.RecordTTL && profile.RecordTTL != 0
	case AccountStoragePaid:
		return proof.Mode == dkvsindexer.FeeModeAutopay && value.record.TTL == 0 &&
			strings.EqualFold(strings.TrimSpace(proof.PoolContract),
				strings.TrimSpace(profile.AutopayContract))
	default:
		return false
	}
}

func (p *Manager) verifyAccountManagedStorage(store *dkvsStore,
	profile *accountManagementProfile, stateKey, dataKey string,
	stateEnvelope, dataEnvelope []byte) error {

	if store == nil || profile == nil {
		return fmt.Errorf("account-managed storage is unavailable")
	}
	if err := store.Refresh(stateKey, dataKey); err != nil {
		return err
	}
	if err := store.WaitReady(stateKey, dataKey); err != nil {
		return err
	}
	stateValue, err := store.Get(stateKey)
	if err != nil {
		return err
	}
	dataValue, err := store.Get(dataKey)
	if err != nil {
		return err
	}
	if !bytes.Equal(stateValue.Value, stateEnvelope) ||
		!accountRecordMatchesStorage(stateValue, profile) ||
		!accountRecordMatchesStorage(dataValue, profile) {
		return fmt.Errorf("account-managed storage readback does not match the committed policy")
	}
	blob, err := DecodeDKVSBlobValue(dataValue.Value)
	if err != nil || !bytes.Equal(blob.Data, dataEnvelope) {
		return fmt.Errorf("account-managed data blob readback mismatch")
	}
	return nil
}

func (p *Manager) requireCurrentAccountManagedData() error {
	if p == nil {
		return ErrDKVSPathNotSynced
	}
	p.mutex.RLock()
	if p.accountProfile == nil {
		p.mutex.RUnlock()
		return nil
	}
	profile := *p.accountProfile
	profile.StateEnvelope = append([]byte(nil), p.accountProfile.StateEnvelope...)
	profile.ManagedDataEnvelope = append([]byte(nil), p.accountProfile.ManagedDataEnvelope...)
	p.mutex.RUnlock()
	if profile.ManagedDataDirty || profile.StateSeq == 0 ||
		profile.ManagedDataRevision == 0 {
		return ErrDKVSPathNotSynced
	}
	root, err := p.accountManagementRootWallet()
	if err != nil {
		return err
	}
	stateKey, err := p.accountManagedStateKey(root)
	if err != nil {
		return err
	}
	dataKey, err := p.accountManagedDataBlobKey(root)
	if err != nil {
		return err
	}
	store, err := p.accountDKVSStore()
	if err != nil {
		return err
	}
	if err := store.WaitReady(stateKey, dataKey); err != nil {
		return err
	}
	stateValue, err := store.Get(stateKey)
	if err != nil {
		return err
	}
	dataValue, err := store.Get(dataKey)
	if err != nil {
		return err
	}
	if accountStateDigest(stateValue.Value) != profile.StateHash ||
		!bytes.Equal(stateValue.Value, profile.StateEnvelope) {
		return dkvsindexer.ErrWriteConflict
	}
	blob, err := DecodeDKVSBlobValue(dataValue.Value)
	if err != nil || !bytes.Equal(blob.Data, profile.ManagedDataEnvelope) {
		return dkvsindexer.ErrWriteConflict
	}
	state, err := account.OpenManagedState(p.accountSecret, profile.AccountID,
		stateValue.Value)
	if err != nil || state.DataRevision != profile.ManagedDataRevision ||
		state.DataHash != profile.ManagedDataHash {
		return dkvsindexer.ErrWriteConflict
	}
	return nil
}
