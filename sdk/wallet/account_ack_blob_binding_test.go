package wallet

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func TestACKBlobReferenceBinding(t *testing.T) {
	old := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = old })
	manager, remote, pkg, auth, secret := newLocalActivationFixture(t)
	defer zeroBytes(secret)
	module, err := accountEntry(manager.accountProfile.AccountID, "wallets", AccountManagedDataGlobalScope)
	require.NoError(t, err)
	moduleKey := module.Key
	database := &rgb11LocalCommitFailureDB{KVDB: manager.db}
	manager.db = database
	var arm atomic.Bool
	http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	http.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		response, err := remote.SendDKVSPostContext(ctx, path, body)
		if err != nil || path != "/v3/dkvs/records/batch-cas" {
			return response, err
		}
		var request DKVSBatchCASRequest
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		for _, mutation := range request.Mutations {
			if mutation.Record != nil && mutation.Record.Key == moduleKey && arm.CompareAndSwap(true, false) {
				database.profileFailureTarget.Store(accountStateDigest(mutation.Record.Value))
				database.fail.Store(1)
			}
		}
		return response, nil
	}
	configureRGB11DKVSTestManager(manager, http)
	discoverInactiveAccount(t, manager)
	require.NoError(t, manager.ActivateAccountManagement(secret, "password", *auth, pkg.Envelope.Locator, ""))
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	value, err := manager.readAccountLocal(manager.accountProfile.AccountID, "wallets", AccountManagedDataGlobalScope)
	require.NoError(t, err)
	require.NotNil(t, value)
	require.Equal(t, moduleKey, value.Key)
	require.NoError(t, manager.UpdateWalletName(manager.GetCurrentWalletId(), "Reference N"))
	arm.Store(true)
	require.ErrorIs(t, manager.SyncAccountManagementState(context.Background()), errRGB11LocalCommitInjected)
	require.Positive(t, database.profileFailures.Load())
	require.Equal(t, uint32(1), database.stageFaults.Load(), "the real atomic account-module Put must hit the injected fault")
	client, err := manager.dkvs.primaryClient()
	require.NoError(t, err)
	replica := newDKVSReplicaStore(manager.db)
	entries, err := replica.LoadOutbox(client.replicaNamespace)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	ack := entries[0]
	require.Equal(t, DKVSOutboxAcknowledged, ack.State)
	require.Equal(t, accountKVOutboxDomain, ack.OriginDomain)
	require.Equal(t, value.Key, ack.OriginKey)
	require.NotZero(t, ack.OriginGeneration)
	mutations, err := ack.DecodeMutations()
	require.NoError(t, err)
	require.Len(t, mutations, 1)
	require.NotNil(t, mutations[0].Record)
	require.Equal(t, value.Key, mutations[0].Record.Key)

	profileBefore, err := database.Read(accountManagementProfileKey())
	require.NoError(t, err)
	localKey := accountLocalKey(manager.accountProfile.AccountID, value.Provider, value.Scope)
	localBefore, err := database.Read(localKey)
	require.NoError(t, err)
	badACK := *ack
	badACK.OriginKey = value.Key + "/wrong-module"
	manager.mutex.Lock()
	err = manager.finishAccountManagedACKLocked(client, &badACK)
	manager.mutex.Unlock()
	require.ErrorIs(t, err, dkvsindexer.ErrInvalidRecord)
	profileAfter, err := database.Read(accountManagementProfileKey())
	require.NoError(t, err)
	localAfter, err := database.Read(localKey)
	require.NoError(t, err)
	require.Equal(t, profileBefore, profileAfter, "wrong-key ACK changed account profile state")
	require.Equal(t, localBefore, localAfter, "wrong-key ACK moved the module's confirmed watermark")
	entries, err = replica.LoadOutbox(client.replicaNamespace)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, ack.RequestID, entries[0].RequestID)
	require.Equal(t, DKVSOutboxAcknowledged, entries[0].State)

	database.fail.Store(0)
	require.ErrorIs(t, manager.StartDKVSSync(), ErrAccountDBStopped,
		"restoring storage availability must not revive the failed Manager")
	service := manager.serverNode
	manager.Close()
	manager = newAccountManagementAutoTestManager(t)
	manager.db, manager.serverNode = database, service
	configureRGB11DKVSTestManager(manager, http)
	require.NoError(t, manager.initDB())
	_, err = manager.UnlockWallet("password")
	require.NoError(t, err)
	client, err = manager.dkvs.primaryClient()
	require.NoError(t, err)
	replica = newDKVSReplicaStore(manager.db)
	require.NoError(t, manager.StartDKVSSync())
	t.Cleanup(manager.StopDKVSSync)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(t, manager.WaitAccountManagedDataReady(ctx))
	require.Eventually(t, func() bool {
		status := manager.GetAccountManagementStatus()
		return status.DKVSWriteAllowed && !status.ManagedDataDirty && status.PendingChanges == 0
	}, 15*time.Second, 20*time.Millisecond)
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	confirmed, err := manager.readAccountLocal(manager.accountProfile.AccountID, value.Provider, value.Scope)
	require.NoError(t, err)
	require.NotNil(t, confirmed)
	require.GreaterOrEqual(t, confirmed.Confirmed, ack.OriginGeneration)
	require.Equal(t, confirmed.Generation, confirmed.Confirmed)
	require.Equal(t, mutations[0].Record.Value, confirmed.Value,
		"restarting after the failed receipt must preserve the acknowledged module content")
	require.Equal(t, "Reference N", manager.GetWalletCatalog()[0].Name)
	entries, err = replica.LoadOutbox(client.replicaNamespace)
	require.NoError(t, err)
	for _, entry := range entries {
		require.NotEqual(t, ack.RequestID, entry.RequestID)
	}
}
