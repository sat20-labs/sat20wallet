package wallet

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"reflect"
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

func (s *memoryAccountRootWrapperStore) WaitReady(_ ...string) error {
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
	manager, remote, pkg, auth, secret := newLocalActivationFixture(t)
	if err := manager.ActivateAccountManagement(secret, "password", *auth, pkg.Envelope.Locator, ""); err != nil {
		t.Fatal(err)
	}
	if err := manager.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	store := &memoryAccountRootWrapperStore{records: make(map[string]*dkvsValue)}
	for key, record := range snapshotRGB11MemoryRecords(remote) {
		store.records[key] = &dkvsValue{Key: key, Value: bytes.Clone(record.Value), Seq: record.Seq,
			TTL: record.TTL, record: cloneDKVSRecord(record)}
	}
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

func TestAccountRootWrapperUpdatesFormalRecoveryAndStorageMetadataOnce(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager, _, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	remote := manager.http.(*rgb11MemoryDKVSHTTP)
	root, _ := manager.accountManagementRootWallet()
	key, _ := accountRootWrapperKey(root)
	before := snapshotRGB11MemoryRecords(remote)[key]
	pkg, _, packageSecret := rereviewRecoveryPackage(t, manager)
	defer zeroBytes(packageSecret)
	location, err := manager.AccountIndexerLocation()
	if err != nil {
		t.Fatal(err)
	}
	pool := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet()).AutopayContract
	auth := AccountStorageAuthorization{Mode: AccountStoragePaid, Location: location,
		Autopay: &DKVSAutopayOptions{AddressParams: GetChainParam_SatsNet(), PoolContract: pool}}
	publicLocator, err := buildAccountPublicLocator(root, _chain, location, accountRootWrapperPayload{
		PackageID: pkg.Envelope.Locator.PackageID, StorageMode: AccountStoragePaid, AutopayContract: pool,
	}, pkg.Envelope.Locator)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ActivateAccountManagement(secret, "password", auth, pkg.Envelope.Locator, publicLocator); err != nil {
		t.Fatal(err)
	}
	value := snapshotRGB11MemoryRecords(remote)[key]
	payload, err := openAccountRootWrapper(root, _chain, manager.accountProfile.AccountID, value.Value)
	if err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(payload.Secret)
	if payload.PackageID != pkg.Envelope.Locator.PackageID || payload.StorageMode != AccountStoragePaid ||
		payload.RecordTTL != 0 || payload.AutopayContract != pool || value.Seq != before.Seq+1 ||
		!bytes.Equal(payload.Secret, secret) {
		t.Fatalf("activation did not publish the new wrapper policy: seq=%d", value.Seq)
	}
	status := manager.GetAccountManagementStatus()
	if !status.RecoveryConfigured || manager.accountProfile.PublicLocator != publicLocator ||
		manager.accountProfile.RecoveryMode != pkg.Envelope.Locator.RecoveryMode {
		t.Fatal("public recovery configuration was lost when reducing the wrapper payload")
	}
	public, err := DecodeAccountPublicLocator(status.PublicLocator, _chain)
	if err != nil || public.Locator != pkg.Envelope.Locator || public.StorageLocation != location ||
		public.StorageMode != AccountStoragePaid || public.RecordTTL != 0 || public.AutopayContract != pool {
		t.Fatal("signed public locator lost its recovery identity or paid storage policy")
	}
	if err := manager.SyncAccountRootWrapper(context.Background()); err != nil {
		t.Fatal(err)
	}
	if after := snapshotRGB11MemoryRecords(remote)[key]; !reflect.DeepEqual(value, after) {
		t.Fatal("ordinary wrapper sync rewrote the activated configuration")
	}
}

func TestTemporaryAccountAdoptsVerifiedRemotePaidPolicy(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	source, _, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	remote := source.http.(*rgb11MemoryDKVSHTTP)
	target := newAccountManagementAutoTestManager(t)
	configureRGB11DKVSTestManager(target, remote)
	target.serverNode = source.serverNode
	if _, err := target.RecoverAccountManagementFromRootMnemonic(context.Background(), accountRootWrapperTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	pkg, _, packageSecret := rereviewRecoveryPackage(t, source)
	defer zeroBytes(packageSecret)
	location, err := source.AccountIndexerLocation()
	if err != nil {
		t.Fatal(err)
	}
	pool := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet()).AutopayContract
	auth := AccountStorageAuthorization{Mode: AccountStoragePaid, Location: location,
		Autopay: &DKVSAutopayOptions{AddressParams: GetChainParam_SatsNet(), PoolContract: pool}}
	if err := source.ActivateAccountManagement(secret, "password", auth, pkg.Envelope.Locator, "paid-locator"); err != nil {
		t.Fatal(err)
	}
	if err := source.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := target.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := target.GetAccountManagementStatus()
	if got.StorageMode != AccountStoragePaid || target.accountProfile.AutopayContract != pool ||
		target.accountProfile.PackageID != pkg.Envelope.Locator.PackageID || !got.RecoveryConfigured ||
		!bytes.Equal(target.accountSecret, secret) {
		t.Fatal("verified paid configuration was not adopted with the original secret")
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
	if store.updates != 0 {
		t.Fatalf("different-secret wrapper was overwritten: updates=%d", store.updates)
	}
}

func TestAccountActivationRefusesDifferentRootSecret(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager, _, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	remote := manager.http.(*rgb11MemoryDKVSHTTP)
	pkg, auth, packageSecret := rereviewRecoveryPackage(t, manager)
	defer zeroBytes(packageSecret)
	before := snapshotRGB11MemoryRecords(remote)
	different := bytes.Repeat([]byte{0x7f}, 32)
	if err := manager.ActivateAccountManagement(different, "password", *auth, pkg.Envelope.Locator, ""); !errors.Is(err, ErrAccountManagementSecretConflict) {
		t.Fatalf("activation accepted another root secret: %v", err)
	}
	if !reflect.DeepEqual(before, snapshotRGB11MemoryRecords(remote)) || !bytes.Equal(manager.accountSecret, secret) {
		t.Fatal("rejected activation replaced local secret or remote records")
	}
	if err := manager.ActivateAccountManagement(secret, "password", *auth, pkg.Envelope.Locator, ""); err != nil {
		t.Fatalf("matching account secret failed activation: %v", err)
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

func TestAccountRootDiscoveryMissingStateDoesNotAuthorizeNewSecret(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	source, remote, pkg, auth, secret := newLocalActivationFixture(t)
	if err := source.ActivateAccountManagement(secret, "password", *auth, pkg.Envelope.Locator, ""); err != nil {
		t.Fatal(err)
	}
	root, err := source.accountManagementRootWallet()
	if err != nil {
		t.Fatal(err)
	}
	stateKey, err := source.accountManagedStateKey(root)
	if err != nil {
		t.Fatal(err)
	}
	wrapperKey, err := accountRootWrapperKey(root)
	if err != nil {
		t.Fatal(err)
	}
	beforeRemote := snapshotRGB11MemoryRecords(remote)
	if beforeRemote[wrapperKey] == nil || beforeRemote[stateKey] != nil {
		t.Fatal("fixture must open the service before its first private backup")
	}
	target := newAccountManagementAutoTestManager(t)
	configureRGB11DKVSTestManager(target, remote)
	target.serverNode = source.serverNode
	walletID, err := target.ImportWallet(accountRootWrapperTestMnemonic, "password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.syncDKVSOnce(); err != nil && !errors.Is(err, ErrRootAccountDiscoveryPending) {
		t.Fatal(err)
	}
	_, discoveryErr := target.RecoverAccountManagementFromRootMnemonic(context.Background(), accountRootWrapperTestMnemonic, "password")
	if !errors.Is(discoveryErr, ErrRootAccountDiscoveryPending) || errors.Is(discoveryErr, ErrRootAccountNotFound) {
		t.Fatalf("existing service with missing backup must remain pending: %v", discoveryErr)
	}
	if target.GetAccountManagementStatus().DiscoveryStatus != "active-waiting-backup" {
		t.Fatal("existing active service was not identified")
	}
	assertPendingImportReadOnly(t, target, walletID)
	if err := target.ActivateAccountManagement(target.accountSecret, "password", *auth, pkg.Envelope.Locator, ""); !errors.Is(err, ErrRootAccountDiscoveryPending) {
		t.Fatalf("existing service permitted reinitialization: %v", err)
	}
	if !reflect.DeepEqual(beforeRemote, snapshotRGB11MemoryRecords(remote)) {
		t.Fatal("pending new device overwrote or initialized remote account data")
	}
}

func TestFreshMainnetImportContinuesAfterRootMetadataRouteNotFound(t *testing.T) {
	oldChain := _chain
	_chain = "mainnet"
	defer func() { _chain = oldChain }()

	for _, scenario := range []struct {
		name   string
		status int
	}{
		{"missing-route", http.StatusNotFound},
		{"upstream-unavailable", http.StatusBadGateway},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			manager := newAccountManagementAutoTestManager(t)
			store := &memoryAccountRootWrapperStore{
				records: make(map[string]*dkvsValue),
				refreshErr: &HTTPResponseError{
					StatusCode: scenario.status,
					Body:       []byte("discovery unavailable"),
				},
			}
			_, discoveryErr := manager.recoverAccountManagementFromRootMnemonic(
				context.Background(), accountRootWrapperTestMnemonic, "password", store,
				AccountIndexerLocation{Scheme: "https", Host: "dkvs.test", Proxy: "satsnet/mainnet"})
			if !errors.Is(discoveryErr, ErrRootAccountDiscoveryPending) {
				t.Fatalf("unavailable discovery error=%v", discoveryErr)
			}
			if errors.Is(discoveryErr, ErrRootAccountNotFound) {
				t.Fatalf("unavailable discovery was treated as a missing root record: %v", discoveryErr)
			}

			walletID, err := manager.ImportWallet(accountRootWrapperTestMnemonic, "password")
			if err != nil || walletID == 0 {
				t.Fatalf("ordinary import after pending discovery: wallet=%d err=%v", walletID, err)
			}
			if manager.GetAccountManagementStatus().DiscoveryStatus != "unknown" {
				t.Fatal("failed network discovery was treated as confirmed inactive")
			}
			assertPendingImportReadOnly(t, manager, walletID)
			if len(store.records) != 0 || store.updates != 0 {
				t.Fatal("unknown discovery published remote account data")
			}
		})
	}
}

func TestRootDiscoveryRecordNotFoundRemainsDistinctFromRouteNotFound(t *testing.T) {
	if err := rootDiscoveryError(ErrDKVSRecordNotFound); !errors.Is(err, ErrRootAccountNotFound) ||
		errors.Is(err, ErrRootAccountDiscoveryPending) {
		t.Fatalf("record-not-found classification=%v", err)
	}
	upstream := &HTTPResponseError{StatusCode: http.StatusBadGateway, Body: []byte("upstream failed")}
	if err := rootDiscoveryError(upstream); !errors.Is(err, ErrRootAccountDiscoveryPending) || errors.Is(err, ErrRootAccountNotFound) {
		t.Fatalf("unavailable upstream authorized a missing root: %v", err)
	}
	refused := &HTTPResponseError{StatusCode: http.StatusForbidden, Body: []byte("forbidden")}
	if err := rootDiscoveryError(refused); !errors.Is(err, refused) {
		t.Fatalf("definite HTTP refusal was reclassified: %v", err)
	}
}

func TestAccountRootDiscoveryRestoresOriginalAccount(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	source, remote, pkg, auth, originalSecret := newLocalActivationFixture(t)
	if err := source.ActivateAccountManagement(originalSecret, "password", *auth, pkg.Envelope.Locator, ""); err != nil {
		t.Fatal(err)
	}
	if err := source.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	target := newAccountManagementAutoTestManager(t)
	configureRGB11DKVSTestManager(target, remote)
	target.serverNode = source.serverNode
	importedID, err := target.ImportWallet(accountRootWrapperTestMnemonic, "password")
	if err != nil {
		t.Fatal(err)
	}
	assertPendingImportReadOnly(t, target, importedID)
	if _, err := target.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
	results, err := target.RecoverAccountManagementFromRootMnemonic(context.Background(), accountRootWrapperTestMnemonic, "password")
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
		!status.RecoveryConfigured || !bytes.Equal(restoredSecret, originalSecret) {
		t.Fatalf("restored status=%+v secretMatches=%v", status,
			bytes.Equal(restoredSecret, originalSecret))
	}
}

func TestAccountRootDiscoveryMnemonicIgnoresCurrentWalletSelection(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	manager, _, _, _, _ := newLocalActivationFixture(t)
	rootID := manager.GetCurrentWalletId()
	childID, err := manager.ImportWallet(
		"legal winner thank year wave sausage worth useful legal winner thank yellow",
		"password",
	)
	if err != nil {
		t.Fatal(err)
	}
	if childID == rootID {
		t.Fatal("fixture did not create a second wallet")
	}
	if err := manager.SwitchWallet(childID, "password"); err != nil {
		t.Fatal(err)
	}

	mnemonic, err := manager.accountManagementRootMnemonic("password")
	if err != nil {
		t.Fatal(err)
	}
	if mnemonic != accountRootWrapperTestMnemonic {
		t.Fatal("root discovery followed current wallet selection")
	}
	if manager.status.CurrentWallet != childID {
		t.Fatal("reading the root mnemonic changed current wallet selection")
	}
}

func TestAccountGuardianIdentityIsRootBoundNotSelectionBound(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	manager, _, _, _, _ := newLocalActivationFixture(t)
	rootID := manager.GetCurrentWalletId()
	childID, err := manager.ImportWallet(
		"legal winner thank year wave sausage worth useful legal winner thank yellow",
		"password",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SwitchWallet(rootID, "password"); err != nil {
		t.Fatal(err)
	}
	rootIdentity, err := manager.GetOrCreateAccountGuardianIdentity("password")
	if err != nil {
		t.Fatal(err)
	}

	if err := manager.SwitchWallet(childID, "password"); err != nil {
		t.Fatal(err)
	}
	childSelectedIdentity, err := manager.GetOrCreateAccountGuardianIdentity("password")
	if err != nil {
		t.Fatal(err)
	}
	if childSelectedIdentity.MailboxID != rootIdentity.MailboxID ||
		childSelectedIdentity.PublicKey != rootIdentity.PublicKey {
		t.Fatalf("guardian identity followed current wallet: root=%+v selected=%+v",
			rootIdentity, childSelectedIdentity)
	}
	if manager.status.CurrentWallet != childID {
		t.Fatal("guardian identity lookup changed current wallet selection")
	}
}

// Ordinary synchronization adopts confirmed configuration; only activation
// is allowed to replace current, including within the same storage mode.
func TestSameModeReconfigurationDoesNotRollBackRootWrapper(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	for _, mode := range []string{AccountStorageTemporary, AccountStoragePaid} {
		t.Run(mode, func(t *testing.T) {
			manager, _, secret := buildRootWrapperSource(t)
			defer zeroBytes(secret)
			remote := manager.http.(*rgb11MemoryDKVSHTTP)
			root, _ := manager.accountManagementRootWallet()
			key, _ := accountRootWrapperKey(root)
			var peer *Manager
			var auth *AccountStorageAuthorization
			for round := 0; round < 2; round++ {
				var pkg *account.RecoveryPackage
				if round == 0 {
					var packageSecret []byte
					pkg, auth, packageSecret = rereviewRecoveryPackage(t, manager)
					zeroBytes(packageSecret)
					if mode == AccountStoragePaid {
						auth.Mode, auth.RecordOptions.TTL = mode, 0
						auth.Autopay = &DKVSAutopayOptions{AddressParams: GetChainParam_SatsNet(),
							PoolContract: dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet()).AutopayContract}
					}
				} else {
					backup, err := manager.ExportAccountBackupForPWA("password", nil)
					if err != nil {
						t.Fatal(err)
					}
					bootstrap, err := account.RootBootstrapBackup(backup)
					clearAccountBackup(&backup)
					if err != nil {
						t.Fatal(err)
					}
					status := manager.GetAccountManagementStatus()
					pkg, err = manager.CreateAccountRecoveryPackage(account.CreateOptions{
						AccountID: status.AccountID, Backup: bootstrap,
						RecoveryMode: account.RecoveryMode(status.RecoveryMode), Questions: coreQuestions(),
					})
					clearAccountBackup(&bootstrap)
					if err != nil {
						t.Fatal(err)
					}
				}
				if err := manager.ActivateAccountManagement(secret, "password", *auth, pkg.Envelope.Locator, ""); err != nil {
					t.Fatal(err)
				}
				if err := manager.SyncAccountManagementState(context.Background()); err != nil {
					t.Fatal(err)
				}
				if round == 0 {
					peer = newAccountManagementAutoTestManager(t)
					configureRGB11DKVSTestManager(peer, remote)
					peer.serverNode = manager.serverNode
					if _, err := peer.RecoverAccountManagementFromRootMnemonic(context.Background(), accountRootWrapperTestMnemonic, "password"); err != nil {
						t.Fatal(err)
					}
				}
			}
			current := snapshotRGB11MemoryRecords(remote)[key]
			for round := 0; round < 3; round++ {
				if err := peer.SyncAccountManagementState(context.Background()); err != nil {
					t.Fatal(err)
				}
				if peer.accountProfile.PackageID != manager.accountProfile.PackageID || peer.accountProfile.StorageMode != mode ||
					!bytes.Equal(peer.accountSecret, secret) {
					t.Fatal("stale device did not adopt the same-mode configuration and original secret")
				}
				if err := peer.SyncAccountRootWrapper(context.Background()); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(current, snapshotRGB11MemoryRecords(remote)[key]) {
					t.Fatal("ordinary sync rewrote current wrapper")
				}
			}
		})
	}
}

func assertPendingImportReadOnly(t *testing.T, manager *Manager, walletID int64) {
	t.Helper()
	status := manager.GetAccountManagementStatus()
	if status.LocalOnly || status.DKVSWriteAllowed || status.RecoveryConfigured || status.ActivationPending {
		t.Fatal("unconfirmed discovery granted business or activation authority")
	}
	if status.DiscoveryStatus != "unknown" && status.DiscoveryStatus != "active-waiting-backup" {
		t.Fatal("discovery lost its pending state")
	}
	before, err := manager.db.Read(accountManagementProfileKey())
	if err != nil {
		t.Fatal(err)
	}
	beforeCatalog := manager.GetWalletCatalog()
	if err := manager.UpdateWalletName(walletID, "must wait for discovery"); err == nil {
		t.Fatal("pending discovery allowed a local edit")
	}
	if err := manager.SyncAccountManagementState(context.Background()); !errors.Is(err, ErrDKVSPathNotSynced) {
		t.Fatalf("pending discovery backup error: %v", err)
	}
	after, err := manager.db.Read(accountManagementProfileKey())
	if err != nil || !bytes.Equal(before, after) || !reflect.DeepEqual(beforeCatalog, manager.GetWalletCatalog()) {
		t.Fatal("rejected pending-discovery business changed local recovery data")
	}
}
