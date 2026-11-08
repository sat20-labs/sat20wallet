package wallet

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// The PWA can reconfigure recovery while a normal local catalog edit occurs.
// Pause the real wrapper request, retain the new edit through its ACK, and
// prove a subsequent ordinary backup/restoration contains both results.
func TestAccountReconfigureKeepsEdit(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	manager, remote, _, _ := accountTTLNoopFixture(t)
	pkg, auth, secret := rereviewRecoveryPackage(t, manager)
	defer zeroBytes(secret)
	previousPackage := manager.GetAccountManagementStatus().PackageID
	require.NotEqual(t, previousPackage, pkg.Envelope.Locator.PackageID)
	gate := &accountActivationNetworkGate{phase: "CAS", started: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(gate.release) }) }
	t.Cleanup(release)
	setActivationTestHTTP(t, manager, &accountActivationGateHTTP{rgb11MemoryDKVSHTTP: remote, gate: gate})
	done := make(chan error, 1)
	go func() {
		done <- manager.ActivateAccountManagement(secret, "password", *auth, pkg.Envelope.Locator, "account://"+pkg.Envelope.Locator.PackageID)
	}()
	select {
	case <-gate.started:
	case err := <-done:
		t.Fatalf("reconfiguration ended before its request: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("wrapper submission did not reach gate")
	}
	require.NoError(t, manager.UpdateWalletName(manager.GetCurrentWalletId(), "PWA edit during recovery setup"))
	generation := manager.accountProfile.ManagedDataGeneration
	release()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(20 * time.Second):
		t.Fatal("reconfiguration did not finish")
	}
	require.Equal(t, pkg.Envelope.Locator.PackageID, manager.accountProfile.PackageID)
	require.Equal(t, "PWA edit during recovery setup", manager.GetWalletCatalog()[0].Name)
	require.True(t, manager.accountProfile.ManagedDataDirty)
	require.Positive(t, len(manager.accountProfile.Pending))
	require.GreaterOrEqual(t, manager.accountProfile.ManagedDataGeneration, generation)
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	peer := newAccountManagementAutoTestManager(t)
	configureRGB11DKVSTestManager(peer, remote)
	recovered, err := peer.LoadAccountManagementStateForRecovery(auth.Location, pkg.Envelope.Locator, secret, accountRootWrapperTestMnemonic)
	require.NoError(t, err)
	_, err = peer.RestoreAccountManagementState(*recovered, secret, "password", pkg.Envelope.Locator, AccountManagementRestoreOptions{Location: auth.Location, StorageMode: auth.Mode, RecordTTL: auth.RecordOptions.TTL, PublicLocator: "account://" + pkg.Envelope.Locator.PackageID})
	require.NoError(t, err)
	require.Equal(t, "PWA edit during recovery setup", peer.GetWalletCatalog()[0].Name)
	require.Equal(t, pkg.Envelope.Locator.PackageID, peer.GetAccountManagementStatus().PackageID)
	require.Zero(t, manager.GetAccountManagementStatus().PendingChanges)
}

// A signed current-wrapper update is deliberately separate from the ordinary
// atomic three-key account writer. This checks the lower-level accepted-write
// boundary; it must not be mistaken for a reproduced normal device workflow.
func TestAccountActivationRejectsNewOwnerSignedWrapperBaseline(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	manager, _, store, keys := accountTTLNoopFixture(t)
	previous := *manager.accountProfile
	pkg, _, secret := rereviewRecoveryPackage(t, manager)
	defer zeroBytes(secret)
	competing := previous
	competing.PackageID = pkg.Envelope.Locator.PackageID
	require.NotEqual(t, previous.PackageID, competing.PackageID)
	root, err := manager.accountManagementRootWallet()
	require.NoError(t, err)
	envelope, err := sealAccountRootWrapper(root, _chain, previous.AccountID, rootWrapperPayload(competing, secret), nil)
	require.NoError(t, err)
	mutation, err := accountRootWrapperMutation(&competing, root, keys[2], envelope)
	require.NoError(t, err)
	ack, err := store.Put(mutation)
	require.NoError(t, err)
	// This is exactly the baseline visibility effect under review, without
	// applying a patch/overlay to the production activation implementation.
	require.NoError(t, store.SyncCurrent(keys...))
	locator := account.Locator{AccountID: previous.AccountID, PackageID: previous.PackageID, RecoveryMode: previous.RecoveryMode}
	authorization := AccountStorageAuthorization{Mode: previous.StorageMode, RecordOptions: dkvs.RecordOptions{TTL: previous.RecordTTL}, Location: previous.Location}
	activationErr := manager.ActivateAccountManagement(secret, "password", authorization, locator, previous.PublicLocator)
	after, err := store.GetAuthoritative(keys[2])
	require.NoError(t, err)
	t.Logf("owner-signed wrapper-only boundary: activation_rejected=%t competing_hash_preserved=%t", activationErr != nil, after.Hash == ack.Hash)
	if activationErr == nil || after.Hash != ack.Hash {
		t.Fatal(fmt.Sprintf("new signed wrapper baseline replaced: activation_rejected=%t competing_hash_preserved=%t", activationErr != nil, after.Hash == ack.Hash))
	}
}
