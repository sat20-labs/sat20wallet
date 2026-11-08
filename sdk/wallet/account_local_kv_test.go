package wallet

import (
	"bytes"
	"context"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func TestAccountPackageCopyColdOpen(t *testing.T) {
	manager, remote, service, closeManager := newAccountGroup(t)
	_, err := manager.ImportWallet(accountRootWrapperTestMnemonic, "password")
	require.NoError(t, err)
	require.NoError(t, manager.InitializeAccountManagement("password"))
	pkg, auth, _ := rereviewRecoveryPackage(t, manager)
	encoded, err := account.EncodeRecoveryPackageStorage(*pkg)
	require.NoError(t, err)
	repository, err := manager.NewAccountRepositoryForStorage(*auth)
	require.NoError(t, err)
	require.NoError(t, repository.SaveRecoveryPackage(context.Background(), *pkg))
	key, err := dkvsindexer.AccountPersonalKey(pkg.Envelope.Locator.AccountID,
		accountPackagePath(pkg.Envelope.Locator.PackageID))
	require.NoError(t, err)
	copy, err := manager.readAccountLocal(pkg.Envelope.Locator.AccountID, "account", key)
	require.NoError(t, err)
	require.NotNil(t, copy)
	require.True(t, bytes.Equal(copy.Value, encoded), "published recovery material differs from the complete local copy")
	require.Equal(t, copy.Generation, copy.Captured)
	cfg := manager.cfg
	closeManager()
	cold, _ := openAccountGroup(t, cfg, remote, service)
	_, err = cold.UnlockWallet("password")
	require.NoError(t, err)
	reopened, err := cold.readAccountLocal(pkg.Envelope.Locator.AccountID, "account", key)
	require.NoError(t, err)
	require.NotNil(t, reopened)
	require.True(t, bytes.Equal(reopened.Value, encoded), "cold reopen lost the original recovery package")
	decoded, err := account.DecodeRecoveryPackageStorage(reopened.Value)
	require.NoError(t, err)
	require.True(t, decoded.Envelope.Locator == pkg.Envelope.Locator, "cold copy changed recovery identity")
}

func TestAccountChangeBatchColdOpen(t *testing.T) {
	manager, remote, service, closeManager := newAccountGroup(t)
	_, err := manager.ImportWallet(accountRootWrapperTestMnemonic, "password")
	require.NoError(t, err)
	require.NoError(t, manager.InitializeAccountManagement("password"))
	accountID := manager.GetAccountManagementStatus().AccountID
	batch, err := NewAtomicWriteBatch(manager.db)
	require.NoError(t, err)
	finish, err := manager.StageAccountChange(batch, "notes", "global")
	require.NoError(t, err)
	defer finish(false)
	require.NoError(t, batch.Put([]byte("account-local-copy-test"), []byte("saved")))
	require.NoError(t, batch.Flush())
	finish(true)
	batch.Close()
	queued, err := manager.readAccountLocal(accountID, "notes", "global")
	require.NoError(t, err)
	require.NotNil(t, queued)
	require.Equal(t, uint64(1), queued.Generation)
	require.Zero(t, queued.Captured, "a notification cannot claim an unexported snapshot")
	require.Zero(t, queued.Confirmed)

	abort, err := NewAtomicWriteBatch(manager.db)
	require.NoError(t, err)
	finishAbort, err := manager.StageAccountChange(abort, "notes", "global")
	require.NoError(t, err)
	abort.Close() // No Flush: business data and the new generation both abort.
	finishAbort(false)
	cfg := manager.cfg
	closeManager()
	cold, _ := openAccountGroup(t, cfg, remote, service)
	_, err = cold.UnlockWallet("password")
	require.NoError(t, err)
	persisted, err := cold.readAccountLocal(accountID, "notes", "global")
	require.NoError(t, err)
	require.NotNil(t, persisted)
	require.Equal(t, uint64(1), persisted.Generation, "aborted generation became visible after a real DB reopen")
	require.Zero(t, persisted.Captured)
	row, err := cold.db.Read([]byte("account-local-copy-test"))
	require.NoError(t, err)
	require.True(t, bytes.Equal(row, []byte("saved")))
}
