package wallet

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const accountRootWrapperTestMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

type memoryAccountRootWrapperStore struct {
	records    map[string]*dkvsValue
	refreshErr error
	updateErr  error
	updates    int
}

func (s *memoryAccountRootWrapperStore) Refresh(_ ...string) error {
	return s.refreshErr
}

func (s *memoryAccountRootWrapperStore) Get(key string) (*dkvsValue, error) {
	if s.refreshErr != nil {
		return nil, s.refreshErr
	}
	value := s.records[key]
	if value == nil {
		return nil, ErrDKVSRecordNotFound
	}
	clone := *value
	clone.Value = append([]byte(nil), value.Value...)
	return &clone, nil
}

func (s *memoryAccountRootWrapperStore) Update(keys []string,
	builder dkvsUpdateBuilder) ([]*dkvsValue, error) {

	if s.updateErr != nil {
		return nil, s.updateErr
	}
	current := make(map[string]*dkvsValue, len(keys))
	next := make(map[string]uint64, len(keys))
	for _, key := range keys {
		current[key] = s.records[key]
		next[key] = 1
		if current[key] != nil {
			next[key] = current[key].Seq + 1
		}
	}
	mutations, err := builder(current, next)
	if err != nil {
		return nil, err
	}
	result := make([]*dkvsValue, 0, len(mutations))
	for _, mutation := range mutations {
		value := mutation.Value
		if mutation.BuildValue != nil {
			value, err = mutation.BuildValue(next[mutation.Key])
			if err != nil {
				return nil, err
			}
		}
		stored := &dkvsValue{Key: mutation.Key, Value: append([]byte(nil), value...),
			Seq: next[mutation.Key], TTL: mutation.Policy.TTL}
		record := &swire.DKVSRecord{Key: mutation.Key, Value: append([]byte(nil), value...),
			Seq: stored.Seq, TTL: mutation.Policy.TTL}
		var proof *dkvsindexer.FeeProof
		switch {
		case mutation.Policy.FreeLocal:
			proof = &dkvsindexer.FeeProof{Mode: dkvsindexer.FeeModeFreeLocal}
		case mutation.Policy.Autopay != nil:
			proof = &dkvsindexer.FeeProof{Mode: dkvsindexer.FeeModeAutopay,
				PoolContract: mutation.Policy.Autopay.PoolContract}
		}
		if proof != nil {
			if err := AttachDKVSFeeProof(record, proof); err != nil {
				return nil, err
			}
		}
		stored.TTL = record.TTL
		stored.record = record
		s.records[mutation.Key] = stored
		result = append(result, stored)
	}
	s.updates++
	return result, nil
}

func buildRootWrapperSource(t *testing.T) (*Manager, *memoryAccountRootWrapperStore, []byte) {
	t.Helper()
	manager := newAccountManagementAutoTestManager(t)
	if _, err := manager.ImportWallet(accountRootWrapperTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	if err := manager.InitializeAccountManagement("password"); err != nil {
		t.Fatal(err)
	}

	manager.mutex.Lock()
	secret := append([]byte(nil), manager.accountSecret...)
	profile := manager.accountProfile
	state, err := account.OpenManagedState(secret, profile.AccountID, profile.StateEnvelope)
	if err != nil {
		manager.mutex.Unlock()
		t.Fatal(err)
	}
	bundle := emptyAccountManagedDataBundle(1)
	dataHash, err := accountManagedDataContentHash(bundle.Items)
	if err != nil {
		manager.mutex.Unlock()
		t.Fatal(err)
	}
	dataEnvelope, _, err := account.SealManagedDataBundleWithInfo(secret, profile.AccountID, bundle, nil)
	if err != nil {
		manager.mutex.Unlock()
		t.Fatal(err)
	}
	state.DataRevision = bundle.Revision
	state.DataHash = dataHash
	stateEnvelope, err := account.SealManagedState(secret, profile.AccountID, state, nil)
	if err != nil {
		manager.mutex.Unlock()
		t.Fatal(err)
	}
	profile.StateSeq = state.Revision
	profile.StateEnvelope = stateEnvelope
	profile.StateHash = accountStateDigest(stateEnvelope)
	profile.ManagedDataRevision = bundle.Revision
	profile.ManagedDataHash = dataHash
	profile.ManagedDataEnvelope = dataEnvelope
	profile.ManagedDataDirty = false
	profile.RecordTTL = 144
	manager.mutex.Unlock()

	root, err := manager.accountManagementRootWallet()
	if err != nil {
		t.Fatal(err)
	}
	stateKey, err := manager.accountManagedStateKey(root)
	if err != nil {
		t.Fatal(err)
	}
	dataKey, err := manager.accountManagedDataBlobKey(root)
	if err != nil {
		t.Fatal(err)
	}
	dataValue, err := EncodeDKVSBlobValue(dataEnvelope, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryAccountRootWrapperStore{records: map[string]*dkvsValue{
		stateKey: {Key: stateKey, Value: stateEnvelope, Seq: 1},
		dataKey:  {Key: dataKey, Value: dataValue, Seq: 1},
	}}
	return manager, store, secret
}

func TestAccountRootWrapperRoundTripTamperAndNetworkIsolation(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	root := NewInternalWalletWithMnemonic(accountRootWrapperTestMnemonic, "", GetChainParam())
	accountID, err := dkvsAccountID(root)
	if err != nil {
		t.Fatal(err)
	}
	secret := bytes.Repeat([]byte{0x42}, 32)
	encoded, err := sealAccountRootWrapper(root, "testnet", accountID,
		accountRootWrapperPayload{Secret: secret, StorageMode: AccountStorageTemporary},
		bytes.NewReader(bytes.Repeat([]byte{0x23}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	encodedAgain, err := sealAccountRootWrapper(root, "testnet", accountID,
		accountRootWrapperPayload{Secret: secret, StorageMode: AccountStorageTemporary},
		bytes.NewReader(bytes.Repeat([]byte{0x23}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, encodedAgain) || !bytes.HasPrefix(encoded, []byte(accountRootWrapperMagic)) {
		t.Fatalf("root wrapper encoding is not deterministic compact binary")
	}
	payload, err := openAccountRootWrapper(root, "testnet", accountID, encoded)
	if err != nil || !bytes.Equal(payload.Secret, secret) {
		t.Fatalf("wrapper roundtrip: payload=%+v err=%v", payload, err)
	}
	zeroBytes(payload.Secret)
	tampered := append([]byte(nil), encoded...)
	tampered[len(tampered)-3] ^= 1
	if _, err := openAccountRootWrapper(root, "testnet", accountID, tampered); !errors.Is(err, ErrRootAccountWrapperInvalid) {
		t.Fatalf("tampered wrapper error=%v", err)
	}
	if _, err := openAccountRootWrapper(root, "mainnet", accountID, encoded); !errors.Is(err, ErrRootAccountNetworkMismatch) {
		t.Fatalf("cross-network wrapper error=%v", err)
	}
	if _, err := openAccountRootWrapper(root, "testnet", accountID, []byte(`{"version":1}`)); !errors.Is(err, ErrRootAccountWrapperInvalid) {
		t.Fatalf("JSON wrapper error=%v", err)
	}
	withTrailingByte := append(append([]byte(nil), encoded...), 0)
	if _, err := openAccountRootWrapper(root, "testnet", accountID, withTrailingByte); !errors.Is(err, ErrRootAccountWrapperInvalid) {
		t.Fatalf("wrapper with trailing byte error=%v", err)
	}
}

func TestAccountRootWrapperLegacyProfileMigrationPreservesSecret(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager, store, originalSecret := buildRootWrapperSource(t)
	defer zeroBytes(originalSecret)
	if manager.GetAccountManagementStatus().RecoveryConfigured {
		t.Fatal("legacy fixture unexpectedly has formal recovery configured")
	}
	if err := manager.syncAccountRootWrapper(store); err != nil {
		t.Fatal(err)
	}
	root, _ := manager.accountManagementRootWallet()
	key, _ := accountRootWrapperKey(root)
	value, err := store.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := openAccountRootWrapper(root, _chain,
		manager.GetAccountManagementStatus().AccountID, value.Value)
	if err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(payload.Secret)
	if !bytes.Equal(payload.Secret, originalSecret) || payload.RecoveryConfigured {
		t.Fatal("migration changed the original account secret or recovery state")
	}
	if err := manager.syncAccountRootWrapper(store); err != nil || store.updates != 1 {
		t.Fatalf("idempotent migration: updates=%d err=%v", store.updates, err)
	}
}

func TestAccountRootWrapperUpdatesFormalRecoveryAndStorageMetadataOnce(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager, store, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	if err := manager.syncAccountRootWrapper(store); err != nil {
		t.Fatal(err)
	}

	manager.mutex.Lock()
	manager.accountProfile.PackageID = "formal-package"
	manager.accountProfile.RecoveryMode = account.RecoveryMode2Of3
	manager.accountProfile.StorageMode = AccountStoragePaid
	manager.accountProfile.RecordTTL = 0
	manager.accountProfile.AutopayContract = "autopay-contract"
	manager.accountProfile.PublicLocator = "public-locator"
	manager.accountProfile.RecoveryConfigured = true
	manager.mutex.Unlock()

	if err := manager.syncAccountRootWrapper(store); err != nil {
		t.Fatal(err)
	}
	if store.updates != 2 {
		t.Fatalf("metadata upgrade updates=%d, want 2", store.updates)
	}
	root, _ := manager.accountManagementRootWallet()
	key, _ := accountRootWrapperKey(root)
	value, err := store.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := openAccountRootWrapper(root, _chain,
		manager.GetAccountManagementStatus().AccountID, value.Value)
	if err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(payload.Secret)
	if payload.PackageID != "formal-package" || payload.RecoveryMode != account.RecoveryMode2Of3 ||
		payload.StorageMode != AccountStoragePaid || payload.RecordTTL != 0 ||
		payload.AutopayContract != "autopay-contract" || payload.PublicLocator != "public-locator" ||
		!payload.RecoveryConfigured || value.Seq != 2 {
		t.Fatalf("updated wrapper payload=%+v seq=%d", payload, value.Seq)
	}
	manager.mutex.RLock()
	profile := *manager.accountProfile
	manager.mutex.RUnlock()
	if !accountRecordMatchesStorage(value, &profile) {
		t.Fatal("updated wrapper did not use the current paid storage policy")
	}
	if err := manager.syncAccountRootWrapper(store); err != nil || store.updates != 2 {
		t.Fatalf("updated wrapper was not idempotent: updates=%d err=%v", store.updates, err)
	}
}

func TestAccountRootWrapperRefusesDifferentSecret(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager, store, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	if err := manager.syncAccountRootWrapper(store); err != nil {
		t.Fatal(err)
	}
	root, _ := manager.accountManagementRootWallet()
	key, _ := accountRootWrapperKey(root)
	manager.mutex.RLock()
	profile := *manager.accountProfile
	manager.mutex.RUnlock()
	different := bytes.Repeat([]byte{0x7f}, 32)
	encoded, err := sealAccountRootWrapper(root, _chain, profile.AccountID,
		rootWrapperPayload(profile, different), nil)
	if err != nil {
		t.Fatal(err)
	}
	store.records[key].Value = encoded
	store.records[key].record.Value = append([]byte(nil), encoded...)
	if err := manager.syncAccountRootWrapper(store); !errors.Is(err, ErrRootAccountWrapperInvalid) {
		t.Fatalf("different-secret wrapper error=%v", err)
	}
	if store.updates != 1 {
		t.Fatalf("different-secret wrapper was overwritten: updates=%d", store.updates)
	}
}

func TestAccountRootWrapperMigrationRequiresVerifiedRemoteState(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager, store, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	root, _ := manager.accountManagementRootWallet()
	stateKey, _ := manager.accountManagedStateKey(root)
	store.records[stateKey].Value = []byte("different remote state")
	if err := manager.syncAccountRootWrapper(store); err == nil {
		t.Fatal("wrapper was published without verified remote state")
	}
	wrapperKey, _ := accountRootWrapperKey(root)
	if store.records[wrapperKey] != nil || store.updates != 0 {
		t.Fatal("failed remote verification overwrote wrapper state")
	}
}

func TestAccountRootDiscoveryOfflineIsPending(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager := newAccountManagementAutoTestManager(t)
	store := &memoryAccountRootWrapperStore{records: make(map[string]*dkvsValue),
		refreshErr: ErrDKVSPathNotSynced}
	_, err := manager.recoverAccountManagementFromRootMnemonic(context.Background(),
		accountRootWrapperTestMnemonic, "password", store, AccountIndexerLocation{})
	if !errors.Is(err, ErrRootAccountDiscoveryPending) {
		t.Fatalf("offline discovery error=%v", err)
	}
	if manager.GetAccountManagementStatus().Active {
		t.Fatal("offline discovery initialized a second managed account")
	}
}

func TestAccountRootDiscoveryRestoresOriginalAccount(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	source, store, originalSecret := buildRootWrapperSource(t)
	defer zeroBytes(originalSecret)
	if err := source.syncAccountRootWrapper(store); err != nil {
		t.Fatal(err)
	}
	target := newAccountManagementAutoTestManager(t)
	importedID, err := target.ImportWallet(accountRootWrapperTestMnemonic, "password")
	if err != nil {
		t.Fatal(err)
	}
	if target.GetAccountManagementStatus().Active {
		t.Fatal("root wallet import created a new managed account before discovery")
	}
	results, err := target.recoverAccountManagementFromRootMnemonic(context.Background(),
		accountRootWrapperTestMnemonic, "password", store,
		AccountIndexerLocation{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("root discovery restored no wallets")
	}
	if results[0].ID != importedID {
		t.Fatalf("root recovery replaced imported wallet identity: got %d want %d", results[0].ID, importedID)
	}
	target.mutex.RLock()
	restoredSecret := append([]byte(nil), target.accountSecret...)
	target.mutex.RUnlock()
	defer zeroBytes(restoredSecret)
	status := target.GetAccountManagementStatus()
	if !status.Active || status.AccountID != source.GetAccountManagementStatus().AccountID ||
		status.RecoveryConfigured || !bytes.Equal(restoredSecret, originalSecret) {
		t.Fatalf("restored status=%+v secretMatches=%v", status,
			bytes.Equal(restoredSecret, originalSecret))
	}
}
