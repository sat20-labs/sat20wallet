package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	walletdkvs "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
	"github.com/tyler-smith/go-bip39"
)

// This is the account-management regression gate for DKVS synchronization
// changes. It has no build tag, environment gate or Skip path: normal
// "go test ./e2e" always discovers and runs all twelve scenarios below.
func TestSDKAccountDKVSSyncGate(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)

	t.Run("01_FreshActivationWithoutPrebinding", func(t *testing.T) {
		mnemonic := accountSyncMnemonic(t, 1)
		f := prepareAccountReviewWithMnemonic(t, network, mnemonic, false)
		root := f.manager.GetWalletCatalog()[0]
		bindingKey, err := dkvs.AccountMappingKey("testnet", root.Accounts[0].Address)
		require.NoError(t, err)
		_, err = dkvsClientForNode(t, network.Core).GetRecordDirect(bindingKey)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)

		require.NoError(t, f.manager.UseAccountStorageAuthorization(wallet.AccountStoragePurposeRecovery,
			func(auth *wallet.AccountStorageAuthorization) error {
				return f.manager.ActivateAccountManagement(f.secret, accountReviewPassword, *auth,
					f.pkg.Envelope.Locator, "account://"+f.pkg.Envelope.Locator.PackageID)
			}))
		require.True(t, f.manager.GetAccountManagementStatus().RecoveryConfigured)

		binding := waitForDKVSRecord(t, network.Core, bindingKey)
		_, _, descriptor, err := dkvs.ValidateAccountMappingBindingRecord(binding)
		require.NoError(t, err)
		require.Equal(t, f.pkg.Envelope.Locator.AccountID, descriptor.AccountID)
		require.Equal(t, network.Core.nodePubKey, descriptor.CoreNodeID)
		accountReviewRequireBindingPropagation(t, network.Bootstrap, bindingKey, binding.Value)
		accountReviewRequireBindingPropagation(t, network.Miner, bindingKey, binding.Value)
	})

	t.Run("02_StateDataAndRootWrapperPublishTogether", func(t *testing.T) {
		f := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 2), true)
		f.activate(t)

		root := f.manager.GetWalletCatalog()[0]
		pubKey := f.manager.GetWallet().GetPubKey().SerializeCompressed()
		accountID := f.manager.GetAccountManagementStatus().AccountID
		stateKey, err := dkvs.PersonalKey(pubKey, "account/state")
		require.NoError(t, err)
		dataKey, err := dkvs.BlobKey(accountID, "account-managed-data")
		require.NoError(t, err)
		wrapperKey, err := dkvs.PersonalKey(pubKey, "account/root-key-wrapper/current")
		require.NoError(t, err)

		client := dkvsClientForNode(t, network.Core)
		stateRecord, err := client.GetRecordDirect(stateKey)
		require.NoError(t, err)
		dataRecord, err := client.GetRecordDirect(dataKey)
		require.NoError(t, err)
		wrapperRecord, err := client.GetRecordDirect(wrapperKey)
		require.NoError(t, err)
		require.NotEmpty(t, wrapperRecord.Value)

		state, err := account.OpenManagedState(f.secret, accountID, stateRecord.Value)
		require.NoError(t, err)
		blob, err := wallet.DecodeDKVSBlobValue(dataRecord.Value)
		require.NoError(t, err)
		bundle, err := account.OpenManagedDataBundle(f.secret, accountID, blob.Data)
		require.NoError(t, err)
		hash, err := account.ManagedDataBundleHash(bundle)
		require.NoError(t, err)
		require.Equal(t, state.DataRevision, bundle.Revision)
		require.Equal(t, state.DataHash, hash)
		require.Equal(t, root.Fingerprint, state.RootFingerprint)
	})

	t.Run("03_DeviceAChangeReachesDeviceB", func(t *testing.T) {
		f := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 3), true)
		f.activate(t)
		device, recovered := f.recover(t)
		_, err := device.RestoreAccountManagementState(*recovered, f.secret, accountReviewPassword,
			f.pkg.Envelope.Locator, f.restoreOptions())
		require.NoError(t, err)

		rootFingerprint := f.manager.GetAccountManagementStatus().RootFingerprint
		rootA := accountReviewFind(t, f.manager, rootFingerprint)
		require.NoError(t, f.manager.UpdateWalletName(rootA.ID, "Synced From Device A"))
		require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
		require.NoError(t, device.SyncAccountManagementState(context.Background()))

		rootB := accountReviewFind(t, device, rootFingerprint)
		require.Equal(t, "Synced From Device A", rootB.Name)
		require.Equal(t, f.manager.GetAccountManagementStatus().StateSeq,
			device.GetAccountManagementStatus().StateSeq)
	})

	t.Run("04_ConcurrentDevicesRebaseWithoutLoss", func(t *testing.T) {
		f := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 4), true)
		f.activate(t)
		device, recovered := f.recover(t)
		_, err := device.RestoreAccountManagementState(*recovered, f.secret, accountReviewPassword,
			f.pkg.Envelope.Locator, f.restoreOptions())
		require.NoError(t, err)

		rootFingerprint := f.manager.GetAccountManagementStatus().RootFingerprint
		rootA := accountReviewFind(t, f.manager, rootFingerprint)
		rootB := accountReviewFind(t, device, rootFingerprint)
		require.NoError(t, f.manager.UpdateWalletName(rootA.ID, "Concurrent Rename"))
		require.NoError(t, device.EnsureAccount(rootB.ID, 3, "Concurrent Account", "did:sync-gate:3"))
		require.NoError(t, device.SyncAccountManagementState(context.Background()))
		require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
		require.NoError(t, device.SyncAccountManagementState(context.Background()))

		for _, manager := range []*wallet.Manager{f.manager, device} {
			root := accountReviewFind(t, manager, rootFingerprint)
			require.Equal(t, "Concurrent Rename", root.Name)
			require.Len(t, root.Accounts, 4)
			require.Equal(t, "Concurrent Account", root.Accounts[3].Name)
			require.Equal(t, "did:sync-gate:3", root.Accounts[3].DID)
			require.Zero(t, manager.GetAccountManagementStatus().PendingChanges)
		}
	})

	t.Run("05_DeletionNeverResurrectsOnOtherOrFreshDevice", func(t *testing.T) {
		f := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 5), true)
		secondaryID, err := f.manager.ImportWallet(accountSyncMnemonic(t, 105), accountReviewPassword)
		require.NoError(t, err)
		var secondaryFingerprint string
		for _, entry := range f.manager.GetWalletCatalog() {
			if entry.ID == secondaryID {
				secondaryFingerprint = entry.Fingerprint
				break
			}
		}
		require.NotEmpty(t, secondaryFingerprint)
		f.activate(t)

		device, recovered := f.recover(t)
		_, err = device.RestoreAccountManagementState(*recovered, f.secret, accountReviewPassword,
			f.pkg.Envelope.Locator, f.restoreOptions())
		require.NoError(t, err)
		require.Len(t, device.GetWalletCatalog(), 2)

		require.NoError(t, f.manager.DeleteWallet(secondaryID))
		require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
		require.NoError(t, device.SyncAccountManagementState(context.Background()))
		require.Len(t, device.GetWalletCatalog(), 1)
		accountSyncRequireFingerprintAbsent(t, device, secondaryFingerprint)

		third, newest := f.recover(t)
		_, err = third.RestoreAccountManagementState(*newest, f.secret, accountReviewPassword,
			f.pkg.Envelope.Locator, f.restoreOptions())
		require.NoError(t, err)
		require.Len(t, third.GetWalletCatalog(), 1)
		accountSyncRequireFingerprintAbsent(t, third, secondaryFingerprint)
	})

	t.Run("06_MissedNotificationsRecoverFromCurrentState", func(t *testing.T) {
		f := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 6), true)
		f.activate(t)
		device, recovered := f.recover(t)
		_, err := device.RestoreAccountManagementState(*recovered, f.secret, accountReviewPassword,
			f.pkg.Envelope.Locator, f.restoreOptions())
		require.NoError(t, err)

		rootFingerprint := f.manager.GetAccountManagementStatus().RootFingerprint
		rootA := accountReviewFind(t, f.manager, rootFingerprint)
		require.NoError(t, f.manager.UpdateWalletName(rootA.ID, "Missed Update One"))
		require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
		require.NoError(t, f.manager.UpdateWalletName(rootA.ID, "Missed Update Two"))
		require.NoError(t, f.manager.EnsureAccount(rootA.ID, 3, "Missed Account", "did:missed:3"))
		require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))

		// Device B has never started the background DKVS runtime, so it cannot
		// rely on either notify being delivered. One explicit sync must converge
		// directly to the latest current-state view.
		before := accountReviewFind(t, device, rootFingerprint)
		require.NotEqual(t, "Missed Update Two", before.Name)
		require.NoError(t, device.SyncAccountManagementState(context.Background()))
		after := accountReviewFind(t, device, rootFingerprint)
		require.Equal(t, "Missed Update Two", after.Name)
		require.Len(t, after.Accounts, 4)
		require.Equal(t, "Missed Account", after.Accounts[3].Name)
	})

	t.Run("07_CoreNodeRestartPreservesAccountSync", func(t *testing.T) {
		f := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 7), true)
		f.activate(t)
		before := f.manager.GetAccountManagementStatus()

		restartTestHarness(t, network.Core)
		require.NoError(t, connectNode(network.Core, network.Bootstrap))
		require.NoError(t, joinBlocks(network.Nodes))
		waitForDKVSPeerReady(t, network)

		device, recovered := f.recover(t)
		require.Equal(t, before.StateSeq, recovered.Seq)
		_, err := device.RestoreAccountManagementState(*recovered, f.secret, accountReviewPassword,
			f.pkg.Envelope.Locator, f.restoreOptions())
		require.NoError(t, err)

		rootFingerprint := f.manager.GetAccountManagementStatus().RootFingerprint
		rootA := accountReviewFind(t, f.manager, rootFingerprint)
		require.NoError(t, f.manager.UpdateWalletName(rootA.ID, "After Core Restart"))
		require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
		require.NoError(t, device.SyncAccountManagementState(context.Background()))
		require.Equal(t, "After Core Restart", accountReviewFind(t, device, rootFingerprint).Name)
	})

	t.Run("08_EndpointSwitchFailsClosedAndFreshSourceReplicaRecovers", func(t *testing.T) {
		f := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 8), true)
		f.activate(t)
		bootstrapLocation := accountSyncLocation(t, network.Bootstrap)

		// A replica cursor is source-endpoint scoped. Once this recovery Manager
		// observes Bootstrap, it must not silently reinterpret that cursor as the
		// CoreNode's generation space.
		pinned, _ := accountReviewDevice(t, network, "")
		_, err := pinned.LoadAccountManagementStateForRecovery(bootstrapLocation,
			f.pkg.Envelope.Locator, f.secret, f.rootMnemonic)
		require.Error(t, err, "temporary account state is endpoint-local")
		_, err = pinned.LoadAccountManagementStateForRecovery(f.location,
			f.pkg.Envelope.Locator, f.secret, f.rootMnemonic)
		require.ErrorIs(t, err, dkvs.ErrEndpointMismatch,
			"an endpoint-pinned replica must fail closed instead of comparing generations across sources")

		// Switching source therefore starts with a fresh source-local replica.
		// The canonical account payload must still be recoverable from its original
		// CoreNode without inheriting Bootstrap's cursor.
		fresh, _ := accountReviewDevice(t, network, "")
		recovered, err := fresh.LoadAccountManagementStateForRecovery(f.location,
			f.pkg.Envelope.Locator, f.secret, f.rootMnemonic)
		require.NoError(t, err)
		require.Equal(t, f.manager.GetAccountManagementStatus().StateSeq, recovered.Seq)
	})

	t.Run("09_WriteAckDoesNotBecomeConfirmedReplica", func(t *testing.T) {
		fixture := newBoundRPCFixture(t)
		owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, accountSyncMnemonic(t, 9), 0))
		fixture.bind(t, owner, fixture.coreID)
		key, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), "account/state")
		require.NoError(t, err)
		prefix, err := dkvs.CollectionPathForKey(key)
		require.NoError(t, err)

		db := indexerdb.NewKVDB(t.TempDir())
		require.NotNil(t, db)
		t.Cleanup(func() { _ = db.Close() })
		store := walletdkvs.NewReplicaStore(db)
		const namespace = "account-sync-gate-ack"
		meta := sdkDKVSReviewInstallActive(t, fixture.client, store, namespace, prefix)

		capture := &sdkDKVSReviewAckCapture{inner: fixture.client.Http}
		writer := wallet.NewSatsNetDKVSClient(fixture.client.Scheme, fixture.client.Host, fixture.client.Proxy, capture)
		submitted, err := writer.PutSignedRecordFreeLocal(owner.Wallet, key, []byte("account-state-after-ack"),
			dkvs.RecordOptions{IssueHeight: 100, TTL: 10})
		require.NoError(t, err)
		require.NotNil(t, capture.receipt)

		entry, err := walletdkvs.NewBatchOutboxEntry(namespace,
			[]dkvs.CASMutation{sdkDKVSReviewAbsent(submitted)}, meta.EndpointID, walletdkvs.OutboxOrigin{})
		require.NoError(t, err)
		entry.RequestID = capture.receipt.RequestID
		require.NoError(t, store.QueueOutbox(entry))
		require.NoError(t, store.ApplyWriteResultAndAck(entry, capture.receipt))

		_, readErr := store.LoadSubscriptionRecord(namespace, key)
		require.ErrorIs(t, readErr, indexercommon.ErrKeyNotFound,
			"write ACK must not install account state into the confirmed replica")

		_, err = writer.SyncActiveScope(context.Background(), store, namespace, meta.Scope, false)
		require.NoError(t, err)
		actual, err := store.LoadSubscriptionRecord(namespace, key)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(submitted), dkvs.RecordHash(actual))
	})

	t.Run("10_RecoveryAndGuardianAreImmediatelyAuthoritative", func(t *testing.T) {
		mnemonic := accountSyncMnemonic(t, 10)
		manager, location := accountReviewDevice(t, network, mnemonic)
		require.NoError(t, manager.InitializeAccountManagement(accountReviewPassword))
		require.NoError(t, manager.BindAccountToCurrentCoreNode())
		auth, err := manager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
		require.NoError(t, err)
		backup, err := manager.ExportAccountBackupForPWA(accountReviewPassword, nil)
		require.NoError(t, err)
		guardianPrivate, guardianPublic, err := account.GenerateGuardianKey(nil)
		require.NoError(t, err)
		accountID := manager.GetAccountManagementStatus().AccountID
		pkg, err := manager.CreateAccountRecoveryPackage(account.CreateOptions{
			AccountID: accountID, Backup: backup, RecoveryMode: account.RecoveryMode2Of3,
			Questions: e2eKnowledgeQuestions(), GuardianMailboxID: accountID,
			GuardianPublicKey: guardianPublic,
		})
		require.NoError(t, err)
		require.NotNil(t, pkg.GuardianCapsule)
		repository, err := manager.NewAccountRepositoryForStorage(*auth)
		require.NoError(t, err)
		require.NoError(t, account.NewManager(repository).Publish(context.Background(), *pkg))
		require.NoError(t, manager.PutGuardianCapsuleForStorage(*auth, accountID, *pkg.GuardianCapsule))

		loaded, err := manager.LoadAccountRecoveryPackage(location, pkg.Envelope.Locator)
		require.NoError(t, err, "recovery read must not wait for the local active replica")
		guardianRaw, err := manager.LoadAccountGuardianCapsule(location, accountID,
			pkg.GuardianCapsule.PackageID, pkg.GuardianCapsule.ShareID)
		require.NoError(t, err, "guardian read must not wait for the local active replica")

		var guardian account.GuardianShareCapsule
		require.NoError(t, json.Unmarshal(guardianRaw, &guardian))
		guardianShare, err := account.DecryptGuardianShare(guardian, guardianPrivate)
		require.NoError(t, err)
		dkvsShare, err := account.RecoverDKVSShare(loaded.DKVSShareCapsule,
			loaded.KnowledgeBundle, e2eKnowledgeAnswers())
		require.NoError(t, err)
		restored, secret, err := account.RecoverAccount(loaded.Envelope, dkvsShare, guardianShare)
		require.NoError(t, err)
		defer clearBytes(secret)
		require.Equal(t, backup.Wallets, restored.Wallets)
	})

	t.Run("11_BindingReplicatesButAdmissionRemainsLocal", func(t *testing.T) {
		mnemonic := accountSyncMnemonic(t, 11)
		f := prepareAccountReviewWithMnemonic(t, network, mnemonic, false)
		require.NoError(t, f.manager.UseAccountStorageAuthorization(wallet.AccountStoragePurposeRecovery,
			func(auth *wallet.AccountStorageAuthorization) error {
				return f.manager.ActivateAccountManagement(f.secret, accountReviewPassword, *auth,
					f.pkg.Envelope.Locator, "account://"+f.pkg.Envelope.Locator.PackageID)
			}))

		owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, mnemonic, 0))
		bindingKey, err := dkvs.AccountMappingKey("testnet", owner.Address)
		require.NoError(t, err)
		binding := waitForDKVSRecord(t, network.Core, bindingKey)
		accountReviewRequireBindingPropagation(t, network.Bootstrap, bindingKey, binding.Value)

		key, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), "account/admission-probe")
		require.NoError(t, err)
		_, err = dkvsClientForNode(t, network.Bootstrap).PutSignedRecordFreeLocal(owner.Wallet, key,
			[]byte("must-be-rejected"), dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t,
				dkvsClientForNode(t, network.Bootstrap)), TTL: 10})
		require.ErrorIs(t, err, dkvs.ErrPermissionDenied,
			"replicated mapping must not create local AcceptedBinding on another CoreNode")

		_, err = dkvsClientForNode(t, network.Core).PutSignedRecordFreeLocal(owner.Wallet, key,
			[]byte("accepted-by-bound-core"), dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t,
				dkvsClientForNode(t, network.Core)), TTL: 10})
		require.NoError(t, err)
	})

	t.Run("12_TamperedActiveSyncRecordIsRejectedBeforeRestore", func(t *testing.T) {
		f := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 12), true)
		f.activate(t)
		device, _ := accountReviewDevice(t, network, "")
		device.SetDKVSHttpClient(&accountReviewTamperTransport{inner: wallet.NewHTTPClient(), mode: "active-sync"})

		_, err := device.LoadAccountManagementStateForRecovery(f.location,
			f.pkg.Envelope.Locator, f.secret, f.rootMnemonic)
		require.Error(t, err, "signed account data corrupted in active sync must never enter recovery state")
		require.Empty(t, device.GetWalletCatalog())
		require.False(t, device.GetAccountManagementStatus().Active)
	})
}

func accountSyncMnemonic(t *testing.T, marker byte) string {
	t.Helper()
	entropy := bytes.Repeat([]byte{marker}, 16)
	mnemonic, err := bip39.NewMnemonic(entropy)
	require.NoError(t, err)
	return mnemonic
}

func accountSyncLocation(t *testing.T, node *testHarness) wallet.AccountIndexerLocation {
	t.Helper()
	raw, err := node.IndexerURL("testnet")
	require.NoError(t, err)
	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	return wallet.AccountIndexerLocation{
		Scheme: parsed.Scheme,
		Host:   parsed.Host,
		Proxy:  strings.Trim(parsed.Path, "/"),
	}
}

func accountSyncRequireFingerprintAbsent(t *testing.T, manager *wallet.Manager, fingerprint string) {
	t.Helper()
	for _, entry := range manager.GetWalletCatalog() {
		require.NotEqual(t, fingerprint, entry.Fingerprint, "deleted wallet must not reappear")
	}
}
