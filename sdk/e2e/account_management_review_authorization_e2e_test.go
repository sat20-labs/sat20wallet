package e2e

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSDKAccountReviewAuthorization(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	newAuthorized := func(t *testing.T) (*wallet.Manager, *wallet.AccountStorageAuthorization) {
		t.Helper()
		manager, _ := accountReviewDevice(t, network, dkvsClientMnemonic)
		require.NoError(t, manager.InitializeAccountManagement(accountReviewPassword))
		auth, err := manager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
		require.NoError(t, err)
		return manager, auth
	}

	t.Run("GrantIsConsumedExactlyOnce", func(t *testing.T) {
		manager, auth := newAuthorized(t)
		called := 0
		require.NoError(t, manager.UseAccountStorageAuthorization(wallet.AccountStoragePurposeRecovery,
			func(value *wallet.AccountStorageAuthorization) error {
				called++
				require.Equal(t, auth.ID, value.ID)
				return nil
			}))
		err := manager.UseAccountStorageAuthorization(wallet.AccountStoragePurposeRecovery,
			func(*wallet.AccountStorageAuthorization) error { called++; return nil })
		require.ErrorIs(t, err, wallet.ErrAccountStorageAuthorizationMissing)
		require.Equal(t, 1, called)
	})

	t.Run("FailedUseKeepsSamePurposeAndImmutableGrant", func(t *testing.T) {
		manager, auth := newAuthorized(t)
		sentinel := errors.New("controlled account operation failure")
		err := manager.UseAccountStorageAuthorization(wallet.AccountStoragePurposeRecovery,
			func(value *wallet.AccountStorageAuthorization) error {
				value.Mode = "tampered"
				value.Location.Host = "invalid.example"
				value.RecordOptions.TTL = 0
				return sentinel
			})
		require.ErrorIs(t, err, sentinel)
		pending, err := manager.PendingAccountStorageAuthorization()
		require.NoError(t, err)
		require.Equal(t, *auth, *pending)
		called := false
		err = manager.UseAccountStorageAuthorization(wallet.AccountStoragePurposeGuardian,
			func(*wallet.AccountStorageAuthorization) error { called = true; return nil })
		require.ErrorIs(t, err, wallet.ErrAccountStoragePurposeMismatch)
		require.False(t, called)
		require.NoError(t, manager.UseAccountStorageAuthorization(wallet.AccountStoragePurposeRecovery,
			func(*wallet.AccountStorageAuthorization) error { return nil }))
	})

	t.Run("PendingObservationCannotAlterAuthorization", func(t *testing.T) {
		manager, auth := newAuthorized(t)
		pending, err := manager.PendingAccountStorageAuthorization()
		require.NoError(t, err)
		pending.ID = "forged"
		pending.Mode = wallet.AccountStoragePaid
		pending.RecordOptions.TTL = 0
		pending.Summary.Title = "mutated"
		require.NoError(t, manager.UseAccountStorageAuthorization(wallet.AccountStoragePurposeGuardian,
			func(value *wallet.AccountStorageAuthorization) error {
				require.Equal(t, *auth, *value)
				return nil
			}))
	})

	t.Run("ConcurrentClaimAndConfirmationAreRejected", func(t *testing.T) {
		manager, _ := newAuthorized(t)
		entered := make(chan struct{})
		release := make(chan struct{})
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		defer unblock()
		done := make(chan error, 1)
		go func() {
			done <- manager.UseAccountStorageAuthorization(wallet.AccountStoragePurposeRecovery,
				func(*wallet.AccountStorageAuthorization) error { close(entered); <-release; return nil })
		}()
		select { case <-entered: case <-time.After(5*time.Second): t.Fatal("authorization callback did not start") }
		called := false
		err := manager.UseAccountStorageAuthorization(wallet.AccountStoragePurposeRecovery,
			func(*wallet.AccountStorageAuthorization) error { called = true; return nil })
		require.ErrorIs(t, err, wallet.ErrAccountStorageAuthorizationBusy)
		require.False(t, called)
		_, err = manager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
		require.ErrorIs(t, err, wallet.ErrAccountStorageAuthorizationBusy)
		unblock()
		select { case err := <-done: require.NoError(t, err); case <-time.After(5*time.Second): t.Fatal("authorization callback did not finish") }
		_, err = manager.PendingAccountStorageAuthorization()
		require.ErrorIs(t, err, wallet.ErrAccountStorageAuthorizationMissing)
	})

	t.Run("CanceledInFlightGrantCannotConsumeReplacement", func(t *testing.T) {
		manager, original := newAuthorized(t)
		entered := make(chan struct{})
		release := make(chan struct{})
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		defer unblock()
		done := make(chan error, 1)
		go func() {
			done <- manager.UseAccountStorageAuthorization(wallet.AccountStoragePurposeRecovery,
				func(*wallet.AccountStorageAuthorization) error { close(entered); <-release; return nil })
		}()
		select { case <-entered: case <-time.After(5*time.Second): t.Fatal("authorization callback did not start") }
		manager.CancelPendingAccountStorageAuthorization()
		replacement, err := manager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
		require.NoError(t, err)
		require.NotEqual(t, original.ID, replacement.ID)
		unblock()
		select {
		case err := <-done: require.ErrorIs(t, err, wallet.ErrAccountStorageAuthorizationMissing)
		case <-time.After(5*time.Second): t.Fatal("canceled operation did not finish")
		}
		pending, err := manager.PendingAccountStorageAuthorization()
		require.NoError(t, err)
		require.Equal(t, replacement.ID, pending.ID)
		require.NoError(t, manager.UseAccountStorageAuthorization(wallet.AccountStoragePurposeGuardian,
			func(*wallet.AccountStorageAuthorization) error { return nil }))
	})

	t.Run("SelectionAndReauthenticationPreserveRootAndGrant", func(t *testing.T) {
		manager, auth := newAuthorized(t)
		root := manager.GetWalletCatalog()[0]
		identity := manager.GetAccountManagementStatus()
		secondaryID, err := manager.ImportWallet(coreMnemonic, accountReviewPassword)
		require.NoError(t, err)
		manager.SwitchAccount(2)
		require.Equal(t, secondaryID, manager.GetCurrentWalletId())
		require.Equal(t, uint32(2), manager.GetCurrentAccountId())
		_, err = manager.UnlockWallet("wrong-password")
		require.Error(t, err)
		_, err = manager.UnlockWallet(accountReviewPassword)
		require.NoError(t, err)
		preflight, err := manager.AccountPreflight(accountReviewPassword, nil)
		require.NoError(t, err)
		require.Equal(t, identity.AccountID, preflight.AccountID)
		require.Equal(t, root.ID, manager.GetAccountManagementStatus().RootWalletID)
		// The actual binding goes through SDK -> Transcend STP HTTP ->
		// SatoshiNet MessageService, and must still represent root/account 0.
		require.NoError(t, manager.BindAccountToCurrentCoreNode())
		key, err := dkvs.AccountMappingKey("testnet", root.Accounts[0].Address)
		require.NoError(t, err)
		record, err := dkvsClientForNode(t, network.Core).GetRecordDirect(key)
		require.NoError(t, err)
		descriptor, err := dkvs.DecodeAccountServiceDescriptor(record.Value)
		require.NoError(t, err)
		require.Equal(t, identity.AccountID, descriptor.AccountID)
		require.Equal(t, network.Core.nodePubKey, descriptor.CoreNodeID)
		require.NoError(t, manager.SwitchWallet(root.ID, ""))
		manager.SwitchAccount(1)
		pending, err := manager.PendingAccountStorageAuthorization()
		require.NoError(t, err)
		require.Equal(t, auth.ID, pending.ID)
		require.Equal(t, identity.AccountID, manager.GetAccountManagementStatus().AccountID)
	})

	t.Run("RejectedChainSwitchPreservesGrant", func(t *testing.T) {
		manager, auth := newAuthorized(t)
		require.ErrorIs(t, manager.SwitchChain("mainnet", accountReviewPassword), wallet.ErrInPlaceChainSwitchDisabled)
		require.Equal(t, "testnet", manager.GetChain())
		pending, err := manager.PendingAccountStorageAuthorization()
		require.NoError(t, err)
		require.Equal(t, auth.ID, pending.ID)
	})

	t.Run("RuntimeStopDiscardsGrant", func(t *testing.T) {
		manager, _ := newAuthorized(t)
		// This is an explicit runtime shutdown, not PWA inactivity/UI locking.
		manager.Stop()
		pending, pendingErr := manager.PendingAccountStorageAuthorization()
		called := false
		useErr := manager.UseAccountStorageAuthorization(wallet.AccountStoragePurposeRecovery,
			func(*wallet.AccountStorageAuthorization) error { called = true; return nil })
		t.Logf("account-review: after_stop pending=%t pending_error=%v use_error=%v callback_called=%t",
			pending != nil, pendingErr, useErr, called)
		assert.Error(t, pendingErr, "runtime shutdown must invalidate a session grant")
		assert.Error(t, useErr, "a stopped session must not claim its old grant")
		assert.False(t, called, "no callback may run under a grant from the stopped session")
		_, confirmErr := manager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
		assert.ErrorIs(t, confirmErr, wallet.ErrAccountStorageRuntimeStopped,
			"a stopped runtime must not mint a replacement storage grant")
	})
}
