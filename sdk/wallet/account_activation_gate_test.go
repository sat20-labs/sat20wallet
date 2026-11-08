package wallet

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/account"
)

func newLocalActivationFixture(t *testing.T) (*Manager, *rgb11MemoryDKVSHTTP,
	*account.RecoveryPackage, *AccountStorageAuthorization, []byte) {
	t.Helper()
	remote := newRGB11MemoryDKVSHTTP()
	manager := newAccountManagementAutoTestManager(t)
	configureRGB11DKVSTestManager(manager, remote)
	client := newRGB11MessageNodeClient(remote)
	manager.serverNode = NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
	if _, err := manager.ImportWallet(accountRootWrapperTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	if err := manager.InitializeAccountManagement("password"); err != nil {
		t.Fatal(err)
	}
	if err := manager.refreshDKVSRegistrations(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
	pkg, auth, secret := rereviewRecoveryPackage(t, manager)
	return manager, remote, pkg, auth, secret
}

func TestActivationMarkerColdResume(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	manager, remote, pkg, auth, secret := newLocalActivationFixture(t)
	defer zeroBytes(secret)
	public := "account://" + pkg.Envelope.Locator.PackageID
	beforeRemote := snapshotRGB11MemoryRecords(remote)
	fault := errors.New("injected initial baseline failure")
	setActivationTestHTTP(t, manager, &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote,
		post: func(ctx context.Context, path string, body []byte) ([]byte, error) {
			if path == "/v3/dkvs/active/sync" {
				return nil, fault
			}
			return remote.SendDKVSPostContext(ctx, path, body)
		}})
	if err := manager.ActivateAccountManagement(secret, "password", *auth, pkg.Envelope.Locator, public); !errors.Is(err, fault) {
		t.Fatalf("initial activation error=%v", err)
	}
	status := manager.GetAccountManagementStatus()
	if !status.ActivationPending || status.LocalOnly || status.RecoveryConfigured {
		t.Fatalf("activation marker status=%+v", status)
	}
	encoded, err := manager.db.Read(accountManagementProfileKey())
	if err != nil {
		t.Fatal(err)
	}
	var staged accountManagementProfile
	if err := DecodeFromBytes(encoded, &staged); err != nil {
		t.Fatal(err)
	}
	if !staged.ManagedEntered || staged.Activation == nil || len(staged.Activation.Wrapper) == 0 ||
		staged.Activation.AccountID != staged.AccountID || staged.Activation.RootFingerprint != staged.RootFingerprint {
		t.Fatal("activation marker was not durable before transport")
	}
	if err := manager.UpdateWalletName(manager.GetCurrentWalletId(), "must wait"); !errors.Is(err, ErrDKVSPathNotSynced) {
		t.Fatalf("pending activation allowed a local edit: %v", err)
	}
	after, err := manager.db.Read(accountManagementProfileKey())
	if err != nil || !bytes.Equal(encoded, after) {
		t.Fatalf("rejected edit changed marker: %v", err)
	}
	if !reflect.DeepEqual(beforeRemote, snapshotRGB11MemoryRecords(remote)) {
		t.Fatal("failed initial baseline published account records")
	}

	reopened := newAccountManagementAutoTestManager(t)
	reopened.db = manager.db
	configureRGB11DKVSTestManager(reopened, remote)
	reopened.serverNode = manager.serverNode
	if err := reopened.initDB(); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.UnlockWallet("password"); err != nil {
		t.Fatalf("cold unlock of pending activation: %v", err)
	}
	other := pkg.Envelope.Locator
	other.PackageID += "-different"
	if err := reopened.ActivateAccountManagement(secret, "password", *auth, other, public); !errors.Is(err, errAccountSnapshotChanged) {
		t.Fatalf("different target was accepted: %v", err)
	}
	if !reflect.DeepEqual(beforeRemote, snapshotRGB11MemoryRecords(remote)) {
		t.Fatal("different target published account records")
	}
	if err := reopened.ActivateAccountManagement(secret, "password", *auth, pkg.Envelope.Locator, public); err != nil {
		t.Fatalf("resume exact activation target: %v", err)
	}
	if status := reopened.GetAccountManagementStatus(); !status.RecoveryConfigured || status.ActivationPending {
		t.Fatalf("cold activation did not finish: %+v", status)
	}
}

type activationMarkerFaultDB struct {
	indexercommon.KVDB
	armed          bool
	configuredOnly bool
	err            error
	hits           atomic.Int64
}

func (db *activationMarkerFaultDB) NewAtomicWriteBatch() indexercommon.WriteBatch {
	return &activationMarkerFaultBatch{WriteBatch: db.KVDB.(indexercommon.AtomicWriteBatchDB).NewAtomicWriteBatch(), db: db}
}

func (db *activationMarkerFaultDB) reject(key, value []byte) error {
	if db.armed && bytes.Equal(key, accountManagementProfileKey()) {
		if !db.configuredOnly {
			db.hits.Add(1)
			return db.err
		}
		var profile accountManagementProfile
		if err := DecodeFromBytes(value, &profile); err != nil || profile.RecoveryConfigured {
			db.hits.Add(1)
			return db.err
		}
	}
	return nil
}

func (db *activationMarkerFaultDB) Write(key, value []byte) error {
	if err := db.reject(key, value); err != nil {
		return err
	}
	return db.KVDB.Write(key, value)
}

type activationMarkerFaultBatch struct {
	indexercommon.WriteBatch
	db *activationMarkerFaultDB
}

func (b *activationMarkerFaultBatch) Put(key, value []byte) error {
	if err := b.db.reject(key, value); err != nil {
		return err
	}
	return b.WriteBatch.Put(key, value)
}

func TestActivationMarkerWriteFault(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	manager, remote, pkg, auth, secret := newLocalActivationFixture(t)
	defer zeroBytes(secret)
	beforeRemote := snapshotRGB11MemoryRecords(remote)
	originalDB := manager.db
	fault := &activationMarkerFaultDB{KVDB: originalDB, armed: true,
		err: errors.New("injected marker write failure")}
	manager.db = fault
	err := manager.ActivateAccountManagement(secret, "password", *auth,
		pkg.Envelope.Locator, "account://"+pkg.Envelope.Locator.PackageID)
	if !errors.Is(err, fault.err) {
		t.Fatalf("marker write error=%v", err)
	}
	if fault.hits.Load() == 0 {
		t.Fatal("activation intent persistence fault was not reached")
	}
	if !reflect.DeepEqual(beforeRemote, snapshotRGB11MemoryRecords(remote)) {
		t.Fatal("remote publication preceded durable activation marker")
	}
	manager.db = originalDB
	if status := manager.GetAccountManagementStatus(); !status.LocalOnly || status.ActivationPending {
		t.Fatalf("failed marker changed local eligibility: %+v", status)
	}
	if err := manager.UpdateWalletName(manager.GetCurrentWalletId(), "local after failure"); err != nil {
		t.Fatalf("failed marker blocked ordinary local edit: %v", err)
	}
}

func TestActivationAckColdFinalize(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	manager, remote, pkg, auth, secret := newLocalActivationFixture(t)
	defer zeroBytes(secret)
	public := "account://" + pkg.Envelope.Locator.PackageID
	originalDB := manager.db
	fault := &activationMarkerFaultDB{KVDB: originalDB, armed: true,
		configuredOnly: true, err: errors.New("injected final profile failure")}
	manager.db = fault
	if err := manager.ActivateAccountManagement(secret, "password", *auth,
		pkg.Envelope.Locator, public); !errors.Is(err, fault.err) {
		t.Fatalf("final profile write error=%v", err)
	}
	status := manager.GetAccountManagementStatus()
	if !status.ActivationPending || status.RecoveryConfigured || status.LocalOnly {
		t.Fatalf("remote ACK lost activation marker: %+v", status)
	}
	root, err := manager.accountManagementRootWallet()
	if err != nil {
		t.Fatal(err)
	}
	indexKey, err := account.ManagedIndexKey(manager.accountProfile.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	walletModule, err := accountEntry(manager.accountProfile.AccountID, "wallets", AccountManagedDataGlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	walletModuleKey := walletModule.Key
	wrapperKey, err := accountRootWrapperKey(root)
	if err != nil {
		t.Fatal(err)
	}
	records := snapshotRGB11MemoryRecords(remote)
	if records[indexKey] != nil || records[walletModuleKey] != nil || records[wrapperKey] == nil {
		t.Fatal("service activation must commit the wrapper without waiting for private backup")
	}
	manager.db = originalDB
	reopened := newAccountManagementAutoTestManager(t)
	reopened.db = originalDB
	configureRGB11DKVSTestManager(reopened, remote)
	reopened.serverNode = manager.serverNode
	if err := reopened.initDB(); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.UnlockWallet("password"); err != nil {
		t.Fatalf("cold unlock after committed ACK: %v", err)
	}
	if err := reopened.ActivateAccountManagement(secret, "password", *auth,
		pkg.Envelope.Locator, public); err != nil {
		t.Fatalf("finalize original committed activation: %v", err)
	}
	if status := reopened.GetAccountManagementStatus(); !status.RecoveryConfigured || status.ActivationPending {
		t.Fatalf("committed activation did not finalize: %+v", status)
	}
}

// These fixtures have no running worker. Swap only their transport so the
// next real request observes the fault; keep the authenticated pre-fault view.
func setActivationTestHTTP(t *testing.T, manager *Manager, transport HttpClient) {
	t.Helper()
	client, err := manager.GetDKVSClient()
	if err != nil {
		t.Fatal(err)
	}
	manager.http = transport
	client.Http = transport
}
