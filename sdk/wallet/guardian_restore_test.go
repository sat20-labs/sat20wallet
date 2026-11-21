//go:build guardian

package wallet

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/stretchr/testify/require"
)

func TestAccountGuardianIdentitySurvivesAccountRecovery(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	remote := newRGB11MemoryDKVSHTTP()
	makeActive := func(mnemonic string) (*Manager, *AccountStorageAuthorization, []byte) {
		manager := newAccountManagementAutoTestManager(t)
		configureRGB11DKVSTestManager(manager, remote)
		service := newRGB11MessageNodeClient(remote)
		manager.serverNode = NewNode(service, "message.test", SERVER_NODE, service.CoreNodePubKey(), service.CoreNodePubKey())
		_, err := manager.ImportWallet(mnemonic, "password")
		require.NoError(t, err)
		require.NoError(t, manager.InitializeAccountManagement("password"))
		require.NoError(t, manager.refreshDKVSRegistrations())
		_, err = manager.syncDKVSOnce()
		require.NoError(t, err)
		pkg, auth, secret := rereviewRecoveryPackage(t, manager)
		root, err := manager.accountManagementRootWallet()
		require.NoError(t, err)
		publicLocator, err := buildAccountPublicLocator(root, _chain, auth.Location,
			accountRootWrapperPayload{PackageID: pkg.Envelope.Locator.PackageID,
				StorageMode: auth.Mode, RecordTTL: auth.RecordOptions.TTL}, pkg.Envelope.Locator)
		require.NoError(t, err)
		require.NoError(t, manager.ActivateAccountManagement(secret, "password", *auth,
			pkg.Envelope.Locator, publicLocator))
		require.NoError(t, manager.SyncAccountManagementState(context.Background()))
		return manager, auth, secret
	}
	source, _, secret := makeActive(accountRootWrapperTestMnemonic)
	defer zeroBytes(secret)
	guardian, _, guardianSecret := makeActive("legal winner thank year wave sausage worth useful legal winner thank yellow")
	defer zeroBytes(guardianSecret)
	identity, err := source.GetOrCreateAccountGuardianIdentity("password")
	require.NoError(t, err)
	ownerKey, err := source.LoadAccountGuardianPrivateKey("password")
	require.NoError(t, err)
	defer zeroWalletBytes(ownerKey)
	guardianIdentity, err := guardian.GetOrCreateAccountGuardianIdentity("password")
	require.NoError(t, err)
	require.NotEqual(t, identity.MailboxID, guardianIdentity.MailboxID,
		"the recovery Guardian must have an independent account identity")
	guardianPrivate, err := guardian.LoadAccountGuardianPrivateKey("password")
	require.NoError(t, err)
	defer zeroWalletBytes(guardianPrivate)
	guardianPublic, err := base64.RawURLEncoding.DecodeString(guardianIdentity.PublicKey)
	require.NoError(t, err)

	backup, err := source.ExportAccountBackupForPWA("password", nil)
	require.NoError(t, err)
	bootstrap, err := account.RootBootstrapBackup(backup)
	clearAccountBackup(&backup)
	require.NoError(t, err)
	status := source.GetAccountManagementStatus()
	pkg, err := source.CreateAccountRecoveryPackage(account.CreateOptions{
		AccountID: status.AccountID, Backup: bootstrap, RecoveryMode: account.RecoveryMode2Of3,
		Questions: coreQuestions(), GuardianMailboxID: guardianIdentity.MailboxID,
		GuardianPublicKey: guardianPublic,
	})
	clearAccountBackup(&bootstrap)
	require.NoError(t, err)
	require.NotNil(t, pkg.GuardianCapsule)
	require.NotNil(t, pkg.Manifest.Guardian)

	ownerStorage, err := source.ConfirmAccountStorage(AccountStorageTemporary, 0)
	require.NoError(t, err)
	location := ownerStorage.Location
	var publicLocator string
	var ownerAuthorization AccountStorageAuthorization
	err = source.UseAccountStorageAuthorization(AccountStoragePurposeRecovery, func(auth *AccountStorageAuthorization) error {
		repository, err := source.NewAccountRepositoryForStorage(*auth)
		if err != nil {
			return err
		}
		if err := account.NewManager(repository).Publish(context.Background(), *pkg); err != nil {
			return err
		}
		root, err := source.accountManagementRootWallet()
		if err != nil {
			return err
		}
		autopayContract := ""
		if auth.Autopay != nil {
			autopayContract = auth.Autopay.PoolContract
		}
		publicLocator, err = buildAccountPublicLocator(root, _chain, auth.Location,
			accountRootWrapperPayload{PackageID: pkg.Envelope.Locator.PackageID,
				StorageMode: auth.Mode, RecordTTL: auth.RecordOptions.TTL,
				AutopayContract: autopayContract}, pkg.Envelope.Locator)
		if err != nil {
			return err
		}
		ownerAuthorization = cloneAccountStorageAuthorization(*auth)
		return source.ActivateAccountManagement(secret, "password", *auth,
			pkg.Envelope.Locator, publicLocator)
	})
	require.NoError(t, err, "publish the recovery package at its real locator")
	require.Equal(t, ownerStorage.Location, ownerAuthorization.Location,
		"the locator and published recovery package must use the same confirmed storage location")
	require.NoError(t, source.SyncAccountManagementState(context.Background()))
	updated := source.GetAccountManagementStatus()
	require.Equal(t, pkg.Envelope.Locator.PackageID, updated.PackageID,
		"the source wrapper must reference the published two-of-three package")
	require.Equal(t, publicLocator, updated.PublicLocator)
	decodedLocator, err := DecodeAccountPublicLocator(updated.PublicLocator, _chain)
	require.NoError(t, err, "the public recovery locator must be canonical and signed")
	require.Equal(t, pkg.Envelope.Locator, decodedLocator.Locator)
	require.Equal(t, ownerAuthorization.Location, decodedLocator.StorageLocation)
	require.Equal(t, ownerAuthorization.Mode, decodedLocator.StorageMode)
	require.Equal(t, ownerAuthorization.RecordOptions.TTL, decodedLocator.RecordTTL)
	if ownerAuthorization.Autopay != nil {
		require.Equal(t, ownerAuthorization.Autopay.PoolContract, decodedLocator.AutopayContract)
	} else {
		require.Empty(t, decodedLocator.AutopayContract)
	}
	guardianStorage, err := guardian.ConfirmAccountStorage(AccountStorageTemporary, 0)
	require.NoError(t, err)
	guardianLocation, err := guardian.AccountIndexerLocation()
	require.NoError(t, err)
	require.Equal(t, guardianStorage.Location, guardianLocation,
		"the Guardian mailbox must be published under the independent Guardian account location")
	err = guardian.UseAccountStorageAuthorization(AccountStoragePurposeGuardian, func(auth *AccountStorageAuthorization) error {
		return guardian.PutGuardianCapsuleForStorage(*auth, guardianIdentity.MailboxID, *pkg.GuardianCapsule)
	})
	require.NoError(t, err, "publish the capsule through the independent Guardian account")

	target := newAccountManagementAutoTestManager(t)
	configureRGB11DKVSTestManager(target, remote)
	loaded, err := target.LoadAccountRecoveryPackage(location, pkg.Envelope.Locator)
	require.NoError(t, err)
	questions, err := account.OpenRecoveryKnowledge(*loaded, pkg.UserShare)
	require.NoError(t, err)
	share, err := account.RecoverDKVSShare(loaded.DKVSShareCapsule, questions, coreAnswers())
	require.NoError(t, err)
	ref := loaded.Manifest.Guardian
	require.NotNil(t, ref)
	capsuleBytes, err := target.LoadAccountGuardianCapsule(guardianStorage.Location, ref.MailboxID,
		loaded.Envelope.Locator.PackageID, ref.ShareID)
	require.NoError(t, err)
	var publishedCapsule account.GuardianShareCapsule
	require.NoError(t, json.Unmarshal(capsuleBytes, &publishedCapsule))
	guardianShare, err := account.DecryptGuardianShare(publishedCapsule, guardianPrivate)
	require.NoError(t, err, "the independently published Guardian capsule must be readable and decryptable")
	require.Equal(t, guardianIdentity.MailboxID, ref.MailboxID)
	recoveredBackup, recoveredSecret, err := account.RecoverAccount(loaded.Envelope, share, guardianShare)
	require.NoError(t, err)
	defer clearAccountBackup(&recoveredBackup)
	defer zeroBytes(recoveredSecret)
	require.Equal(t, secret, recoveredSecret, "published 2-of-3 recovery must restore the same account secret")
	require.NotEmpty(t, recoveredBackup.Wallets)
	state, err := target.LoadAccountManagementStateForRecovery(location, loaded.Envelope.Locator,
		recoveredSecret, string(recoveredBackup.Wallets[0].Secret))
	require.NoError(t, err)
	_, err = target.RestoreAccountManagementState(*state, recoveredSecret, "new-password", loaded.Envelope.Locator,
		AccountManagementRestoreOptions{Location: location, StorageMode: AccountStorageTemporary,
			RecordTTL: ownerStorage.RecordOptions.TTL})
	require.NoError(t, err)

	restoredOwnerKey, err := target.LoadAccountGuardianPrivateKey("new-password")
	require.NoError(t, err)
	defer zeroWalletBytes(restoredOwnerKey)
	_, err = target.LoadAccountGuardianPrivateKey("password")
	require.Error(t, err, "the pre-recovery password must not unlock the restored Guardian identity")
	require.Equal(t, ownerKey, restoredOwnerKey)
	stable, err := target.GetOrCreateAccountGuardianIdentity("new-password")
	require.NoError(t, err)
	require.Equal(t, identity, stable)
}
