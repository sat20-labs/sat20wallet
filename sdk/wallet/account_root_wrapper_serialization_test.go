package wallet

import (
	"bytes"
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

type blockingAccountRootWrapperStore struct {
	*memoryAccountRootWrapperStore
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (s *blockingAccountRootWrapperStore) WaitReady(keys ...string) error {
	s.once.Do(func() {
		close(s.started)
		<-s.release
	})
	return s.memoryAccountRootWrapperStore.WaitReady(keys...)
}

func TestAccountRootWrapperSyncRevalidatesPaidActivationAfterBlockedRefresh(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	source, _, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	source.accountBackgroundWG.Wait()
	remote := source.http.(*rgb11MemoryDKVSHTTP)
	manager := newAccountManagementAutoTestManager(t)
	configureRGB11DKVSTestManager(manager, remote)
	manager.serverNode = source.serverNode
	if _, err := manager.RecoverAccountManagementFromRootMnemonic(context.Background(),
		accountRootWrapperTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	manager.accountBackgroundWG.Wait()
	root, err := source.accountManagementRootWallet()
	if err != nil {
		t.Fatal(err)
	}
	key, err := accountRootWrapperKey(root)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotRGB11MemoryRecords(remote)[key]
	if before == nil || manager.GetAccountManagementStatus().StorageMode != AccountStorageTemporary {
		t.Fatal("both devices must begin with the same verified temporary account")
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
	publicLocator, err := buildAccountPublicLocator(root, _chain, location, accountRootWrapperPayload{
		PackageID: pkg.Envelope.Locator.PackageID, StorageMode: AccountStoragePaid, AutopayContract: pool,
	}, pkg.Envelope.Locator)
	if err != nil {
		t.Fatal(err)
	}

	// Pause the other device's real refresh after its local snapshot was
	// captured. The original device can still publish a paid reconfiguration.
	blocked := &blockedAccountConfigHTTP{
		rgb11MemoryDKVSHTTP: remote,
		started:             make(chan struct{}), release: make(chan struct{}), continueAfterRelease: true,
	}
	manager.http = blocked
	manager.SetDKVSHttpClient(blocked)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(blocked.release) }) }
	defer release()

	done := make(chan error, 1)
	go func() {
		done <- manager.SyncAccountRootWrapper(context.Background())
	}()
	select {
	case <-blocked.started:
	case err := <-done:
		t.Fatalf("wrapper sync ended before its refresh was blocked: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("wrapper sync did not reach its real refresh")
	}

	// The network refresh is blocked, but the coordinator mutex must remain
	// available to unrelated local state changes.
	if !manager.accountSyncMu.TryLock() {
		t.Fatal("account coordinator mutex is held across remote refresh")
	}
	manager.accountSyncMu.Unlock()

	if err := source.ActivateAccountManagement(secret, "password", auth, pkg.Envelope.Locator, publicLocator); err != nil {
		t.Fatal(err)
	}
	if err := source.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	source.accountBackgroundWG.Wait()
	paid := snapshotRGB11MemoryRecords(remote)[key]
	if paid == nil || paid.Seq != before.Seq+1 || paid.TTL != 0 {
		t.Fatal("real paid activation did not publish exactly one new wrapper version")
	}
	release()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("root-wrapper sync did not converge after the blocked refresh resumed")
	}
	value := snapshotRGB11MemoryRecords(remote)[key]
	if !reflect.DeepEqual(paid, value) {
		t.Fatal("the older refresh changed the paid wrapper's signed record or sequence")
	}
	manager.mutex.RLock()
	profile := *manager.accountProfile
	identityUnchanged := profile.AccountID == pkg.Envelope.Locator.AccountID &&
		profile.RootFingerprint == source.GetAccountManagementStatus().RootFingerprint &&
		profile.Location == location && bytes.Equal(manager.accountSecret, secret)
	manager.mutex.RUnlock()
	if !identityUnchanged || profile.StorageMode != AccountStoragePaid || profile.RecordTTL != 0 ||
		profile.AutopayContract != pool || profile.PackageID != pkg.Envelope.Locator.PackageID {
		t.Fatal("the refreshed device did not retain the same account and adopt the verified paid policy")
	}
	store, err := manager.accountDKVSStore()
	if err != nil {
		t.Fatal(err)
	}
	verified, err := store.GetAuthoritative(key)
	if err != nil {
		t.Fatal(err)
	}
	if !accountRecordMatchesStorage(verified, &profile) {
		t.Fatal("the signed wrapper lost its paid fee proof or storage policy")
	}
	if err := dkvsindexer.VerifySignature(value); err != nil {
		t.Fatal(err)
	}
	payload, err := openAccountRootWrapper(root, _chain, profile.AccountID, value.Value)
	if err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(payload.Secret)
	if !accountRootWrapperMetadataMatchesProfile(payload, profile) || !bytes.Equal(payload.Secret, secret) {
		t.Fatal("the remote wrapper no longer matches the verified account policy and secret")
	}
	if err := manager.SyncAccountRootWrapper(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(paid, snapshotRGB11MemoryRecords(remote)[key]) {
		t.Fatal("a repeated wrapper refresh rewrote the verified paid policy")
	}
}
