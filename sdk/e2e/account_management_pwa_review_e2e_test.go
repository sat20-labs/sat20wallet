package e2e

import (
	"context"
	"encoding/json"
	"errors"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"strings"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestSDKAccountPWATemporaryExpiry(t *testing.T) {
	fixture := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t))
	network := fixture.Network
	waitForDKVSPeerReady(t, network)
	gas := contractcommon.GetGasAssetName()
	actor := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	heartbeatAmount := int64(1_000_000)
	heartbeatInput := splitToDKVSKeyPathActors(t, fixture, fixture.gasAnchor, gas,
		[]int64{heartbeatAmount}, []int64{100_000}, []*dkvsKeyPathActor{actor})[0]
	// POS requires a nonempty block. Advance actual heights with ordinary,
	// signed transfers through the existing isolated-network mining helper.
	advanceBlock := func() {
		heartbeatAmount -= 10_000
		tx := buildDKVSKeyPathAssetTransfer(t, actor, heartbeatInput, gas,
			heartbeatAmount, heartbeatInput.Output.Value-1000, actor)
		network.sendAndMine(t, tx, 0)
		heartbeatInput = dkvsPrevOut{Point: wire.OutPoint{Hash: tx.TxHash(), Index: 0}, Output: cloneDKVSTxOut(tx.TxOut[0])}
	}
	f := prepareAccountReview(t, network, true)
	f.activate(t)
	root := f.manager.GetWallet()
	client := dkvsClientForNode(t, network.Core)
	stateKey, err := dkvs.PersonalKey(root.GetPubKey().SerializeCompressed(), "account/state")
	require.NoError(t, err)
	dataKey, err := dkvs.BlobKey(f.pkg.Envelope.Locator.AccountID, "account-managed-data")
	require.NoError(t, err)
	wrapperKey, err := dkvs.PersonalKey(root.GetPubKey().SerializeCompressed(), "account/root-key-wrapper/current")
	require.NoError(t, err)
	packageKey, err := dkvs.PersonalKey(root.GetPubKey().SerializeCompressed(), "account/recovery/"+f.pkg.Envelope.Locator.PackageID)
	require.NoError(t, err)

	t.Run("ExpiredRemoteBlobWithLiveStateFailsClosed", func(t *testing.T) {
		original := waitForDKVSRecord(t, network.Core, stateKey)
		blob := waitForDKVSRecord(t, network.Core, dataKey)
		_, err := client.PutSignedRecordFreeLocal(root, blob.Key, blob.Value, dkvs.RecordOptions{Seq: blob.Seq + 1, TTL: 2})
		require.NoError(t, err)
		for i := 0; i < 3; i++ {
			advanceBlock()
		}
		require.Eventually(t, func() bool {
			_, err := client.GetRecordDirect(dataKey)
			return errors.Is(err, dkvs.ErrRecordNotFound)
		}, 15*time.Second, 200*time.Millisecond)
		require.ErrorIs(t, f.manager.SyncAccountManagementState(context.Background()), wallet.ErrDKVSRecordNotFound)
		current := waitForDKVSRecord(t, network.Core, stateKey)
		require.Equal(t, dkvs.RecordHash(original), dkvs.RecordHash(current))
		_, err = client.GetRecordDirect(dataKey)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
	})
	t.Run("EntireTemporaryBackupExpiresThenOriginalDeviceRepublishes", func(t *testing.T) {
		// The negative case already expired the blob. Expire every remaining
		// account record through real short leases and actual node block heights.
		for _, key := range []string{stateKey, wrapperKey, packageKey} {
			current := waitForDKVSRecord(t, network.Core, key)
			_, err := client.PutSignedRecordFreeLocal(root, current.Key, current.Value, dkvs.RecordOptions{Seq: current.Seq + 1, TTL: 2})
			require.NoError(t, err)
		}
		for i := 0; i < 3; i++ {
			advanceBlock()
		}
		for _, key := range []string{stateKey, dataKey, wrapperKey, packageKey} {
			require.Eventually(t, func() bool { _, err := client.GetRecordDirect(key); return errors.Is(err, dkvs.ErrRecordNotFound) },
				15*time.Second, 200*time.Millisecond, "temporary record must expire: %s", key)
		}
		before := f.manager.GetWalletCatalog()
		require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
		// Rebuild recovery material through the normal SDK setup, retaining the
		// existing AccountSecret. Sync alone does not recreate expired shares.
		_, err := f.manager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
		require.NoError(t, err)
		backup, err := f.manager.ExportAccountBackupForPWA(accountReviewPassword, nil)
		require.NoError(t, err)
		f.pkg, err = f.manager.CreateAccountRecoveryPackage(account.CreateOptions{
			AccountID: f.manager.GetAccountManagementStatus().AccountID, Backup: backup,
			RecoveryMode: account.RecoveryMode2Of2, Questions: e2eKnowledgeQuestions()})
		require.NoError(t, err)
		f.activate(t)
		device, recovered := f.recover(t)
		_, err = device.RestoreAccountManagementState(*recovered, f.secret, accountReviewPassword,
			f.pkg.Envelope.Locator, f.restoreOptions())
		require.NoError(t, err)
		catalog := device.GetWalletCatalog()
		require.Len(t, catalog, len(before))
		for i := range before {
			require.Equal(t, before[i].Fingerprint, catalog[i].Fingerprint)
			require.Equal(t, before[i].Accounts, catalog[i].Accounts)
		}
	})
}

// Same-mode configuration changes use existing activation and ordinary sync;
// no direct profile edits, wrapper writes or forged success responses.
func TestSDKAccountSameModeReconfigurationConverges(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	f := prepareAccountReview(t, network, true)
	f.activate(t)
	second, recovered := f.recover(t)
	_, err := second.RestoreAccountManagementState(*recovered, f.secret, accountReviewPassword, f.pkg.Envelope.Locator, f.restoreOptions())
	require.NoError(t, err)
	before := f.manager.GetAccountManagementStatus()
	require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
	_, err = f.manager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
	require.NoError(t, err)
	backup, err := f.manager.ExportAccountBackupForPWA(accountReviewPassword, nil)
	require.NoError(t, err)
	f.pkg, err = f.manager.CreateAccountRecoveryPackage(account.CreateOptions{AccountID: before.AccountID, Backup: backup, RecoveryMode: account.RecoveryMode2Of2, Questions: e2eKnowledgeQuestions()})
	require.NoError(t, err)
	f.activate(t)
	current := f.manager.GetAccountManagementStatus()
	require.NotEqual(t, before.PackageID, current.PackageID)
	key, err := dkvs.PersonalKey(f.manager.GetWallet().GetPubKey().SerializeCompressed(), "account/root-key-wrapper/current")
	require.NoError(t, err)
	client := dkvsClientForNode(t, network.Core)
	wrapper, err := client.GetRecordDirect(key)
	require.NoError(t, err)
	for round := 0; round < 3; round++ {
		for _, device := range []*wallet.Manager{second, f.manager} {
			require.NoError(t, device.SyncAccountManagementState(context.Background()))
			require.NoError(t, device.SyncAccountRootWrapper(context.Background()))
			require.Equal(t, current.PackageID, device.GetAccountManagementStatus().PackageID)
			require.Equal(t, current.PublicLocator, device.GetAccountManagementStatus().PublicLocator)
		}
		actual, err := client.GetRecordDirect(key)
		require.NoError(t, err)
		require.Equal(t, wrapper.Seq, actual.Seq, "idle devices republished current wrapper")
		require.Equal(t, dkvs.RecordHash(wrapper), dkvs.RecordHash(actual))
	}
	third, _ := accountReviewDevice(t, network, "")
	_, err = third.RecoverAccountManagementFromRootMnemonic(context.Background(), f.rootMnemonic, accountReviewPassword)
	require.NoError(t, err)
	require.Equal(t, current.PackageID, third.GetAccountManagementStatus().PackageID)
	require.Equal(t, current.PublicLocator, third.GetAccountManagementStatus().PublicLocator)
	require.Len(t, third.GetWalletCatalog(), len(f.manager.GetWalletCatalog()))
	for _, expected := range f.manager.GetWalletCatalog() {
		actual := accountReviewFind(t, third, expected.Fingerprint)
		require.Equal(t, expected.Name, actual.Name)
		require.Equal(t, expected.Accounts, actual.Accounts)
	}
}

// The runtime has no external MaxTTL configuration knob. Advertise a smaller
// initial policy at the HTTP boundary, with leases accepted by the real node;
// remove that projection after restarting the same node/database. No account
// record, signing, submission, ACK or success result is replaced.
type accountReviewTTLPolicyTransport struct {
	inner wallet.HttpClient
	ttl   uint64
	hits  int
}

func (h *accountReviewTTLPolicyTransport) SendGetRequest(u *wallet.URL) ([]byte, error) {
	raw, err := h.inner.SendGetRequest(u)
	if err != nil || !strings.HasSuffix(u.Path, "/v3/dkvs/config") {
		return raw, err
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	var config dkvs.ClientConfig
	if err := json.Unmarshal(response["data"], &config); err != nil {
		return nil, err
	}
	config.FreeLocal.MaxTTL = h.ttl
	h.hits++
	response["data"], err = json.Marshal(config)
	if err != nil {
		return nil, err
	}
	return json.Marshal(response)
}
func (h *accountReviewTTLPolicyTransport) SendPostRequest(u *wallet.URL, body []byte) ([]byte, error) {
	return h.inner.SendPostRequest(u, body)
}
func TestSDKAccountTemporaryTTLPolicyChangeConverges(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	actualClient := dkvsClientForNode(t, network.Core)
	config, err := actualClient.GetDKVSClientConfig()
	require.NoError(t, err)
	require.Greater(t, config.FreeLocal.MaxTTL, uint64(2))
	oldTTL := config.FreeLocal.MaxTTL / 2
	manager, location := accountReviewDevice(t, network, dkvsClientMnemonic)
	projection := &accountReviewTTLPolicyTransport{inner: wallet.NewHTTPClient(), ttl: oldTTL}
	manager.SetDKVSHttpClient(projection)
	require.NoError(t, manager.InitializeAccountManagement(accountReviewPassword))
	_, err = manager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
	require.NoError(t, err)
	backup, err := manager.ExportAccountBackupForPWA(accountReviewPassword, nil)
	require.NoError(t, err)
	pkg, err := manager.CreateAccountRecoveryPackage(account.CreateOptions{AccountID: manager.GetAccountManagementStatus().AccountID, Backup: backup, RecoveryMode: account.RecoveryMode2Of2, Questions: e2eKnowledgeQuestions()})
	require.NoError(t, err)
	share, err := account.RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle, []account.AnswerAttempt{{QuestionID: e2eKnowledgeQuestions()[0].Question.ID, Answer: e2eKnowledgeQuestions()[0].Answer}, {QuestionID: e2eKnowledgeQuestions()[1].Question.ID, Answer: e2eKnowledgeQuestions()[1].Answer}})
	require.NoError(t, err)
	_, secret, err := account.RecoverAccount(pkg.Envelope, pkg.UserShare, share)
	require.NoError(t, err)
	f := &accountReviewFixture{network: network, manager: manager, location: location, pkg: pkg, secret: secret, rootMnemonic: dkvsClientMnemonic}
	f.activate(t)
	before := manager.GetAccountManagementStatus()
	root := manager.GetWallet()
	stateKey, err := dkvs.PersonalKey(root.GetPubKey().SerializeCompressed(), "account/state")
	require.NoError(t, err)
	dataKey, err := dkvs.BlobKey(before.AccountID, "account-managed-data")
	require.NoError(t, err)
	wrapperKey, err := dkvs.PersonalKey(root.GetPubKey().SerializeCompressed(), "account/root-key-wrapper/current")
	require.NoError(t, err)
	keys := []string{stateKey, dataKey, wrapperKey}
	for _, key := range keys {
		record, err := actualClient.GetRecordDirect(key)
		require.NoError(t, err)
		require.Equal(t, oldTTL, record.TTL)
	}
	require.Greater(t, projection.hits, 0)
	restartTestHarness(t, network.Core)
	require.NoError(t, connectNode(network.Core, network.Bootstrap))
	require.NoError(t, joinBlocks(network.Nodes))
	waitForDKVSPeerReady(t, network)
	manager.SetDKVSHttpClient(wallet.NewHTTPClient())
	for _, key := range keys {
		record, err := actualClient.GetRecordDirect(key)
		require.NoError(t, err)
		require.Equal(t, oldTTL, record.TTL, "old records must still be valid")
	}
	require.NoError(t, manager.UpdateWalletName(manager.GetCurrentWalletId(), "After policy change"))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(t, manager.SyncAccountManagementState(ctx))
	require.Zero(t, manager.GetAccountManagementStatus().PendingChanges)
	require.False(t, manager.GetAccountManagementStatus().ManagedDataDirty)
	require.Equal(t, before.PublicLocator, manager.GetAccountManagementStatus().PublicLocator)
	for _, key := range keys {
		record, err := actualClient.GetRecordDirect(key)
		require.NoError(t, err)
		require.Equal(t, config.FreeLocal.MaxTTL, record.TTL)
	}
	require.NoError(t, manager.SyncAccountRootWrapper(context.Background()))
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	third, _ := accountReviewDevice(t, network, "")
	_, err = third.RecoverAccountManagementFromRootMnemonic(context.Background(), dkvsClientMnemonic, accountReviewPassword)
	require.NoError(t, err)
	require.Equal(t, "After policy change", third.GetWalletCatalog()[0].Name)
	require.Equal(t, before.PublicLocator, third.GetAccountManagementStatus().PublicLocator)
}

func TestSDKAccountRevertedMetadataClearsPending(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	config, _ := accountReviewConfig(t, network)
	dir := t.TempDir()
	database := indexerdb.NewKVDB(dir)
	require.NotNil(t, database)
	manager := wallet.NewManager(config, database)
	require.NotNil(t, manager)
	t.Cleanup(func() { manager.Close(); database.Close() })
	id, err := manager.ImportWallet(dkvsClientMnemonic, accountReviewPassword)
	require.NoError(t, err)
	require.NoError(t, manager.InitializeAccountManagement(accountReviewPassword))
	require.NoError(t, manager.BindAccountToCurrentCoreNode())
	require.NoError(t, manager.UpdateWalletName(id, "A"))
	require.NoError(t, manager.UpdateAccountMetadata(id, 0, "A", "did:A"))
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	require.NoError(t, manager.SyncAccountRootWrapper(context.Background()))
	root := manager.GetWallet()
	stateKey, err := dkvs.PersonalKey(root.GetPubKey().SerializeCompressed(), "account/state")
	require.NoError(t, err)
	dataKey, err := dkvs.BlobKey(manager.GetAccountManagementStatus().AccountID, "account-managed-data")
	require.NoError(t, err)
	wrapperKey, err := dkvs.PersonalKey(root.GetPubKey().SerializeCompressed(), "account/root-key-wrapper/current")
	require.NoError(t, err)
	client := dkvsClientForNode(t, network.Core)
	for _, kind := range []string{"wallet-name", "account-name", "DID"} {
		t.Run(kind, func(t *testing.T) {
			before := map[string]*wire.DKVSRecord{}
			for _, key := range []string{stateKey, dataKey, wrapperKey} {
				record, err := client.GetRecordDirect(key)
				require.NoError(t, err)
				before[key] = record
			}
			mutate := func(value string) error {
				switch kind {
				case "wallet-name":
					return manager.UpdateWalletName(id, value)
				case "account-name":
					return manager.UpdateAccountMetadata(id, 0, value, "did:A")
				default:
					return manager.UpdateAccountMetadata(id, 0, "A", "did:"+value)
				}
			}
			require.NoError(t, mutate("B"))
			require.NoError(t, mutate("A"))
			require.Positive(t, manager.GetAccountManagementStatus().PendingChanges)
			require.True(t, manager.GetAccountManagementStatus().ManagedDataDirty)
			require.NoError(t, manager.SyncAccountManagementState(context.Background()))
			require.Zero(t, manager.GetAccountManagementStatus().PendingChanges)
			require.False(t, manager.GetAccountManagementStatus().ManagedDataDirty)
			for key, record := range before {
				actual, err := client.GetRecordDirect(key)
				require.NoError(t, err)
				require.Equal(t, record.Seq, actual.Seq)
				require.Equal(t, dkvs.RecordHash(record), dkvs.RecordHash(actual))
				require.Equal(t, record.Value, actual.Value)
			}
			manager.Close()
			database.Close()
			database = indexerdb.NewKVDB(dir)
			require.NotNil(t, database)
			manager = wallet.NewManager(config, database)
			require.NotNil(t, manager)
			_, err := manager.UnlockWallet(accountReviewPassword)
			require.NoError(t, err)
			require.Zero(t, manager.GetAccountManagementStatus().PendingChanges)
			require.False(t, manager.GetAccountManagementStatus().ManagedDataDirty)
			catalog := manager.GetWalletCatalog()
			require.Equal(t, "A", catalog[0].Name)
			require.Equal(t, "A", catalog[0].Accounts[0].Name)
			require.Equal(t, "did:A", catalog[0].Accounts[0].DID)
		})
	}
}

func TestSDKAccountSubaccountIndependentFieldsMerge(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	for n, mode := range []string{"LocalNameRemoteDID", "LocalDIDRemoteName", "ClearDIDRemoteName"} {
		t.Run(mode, func(t *testing.T) {
			f := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, byte(211+n)), true)
			f.activate(t)
			first := f.manager
			second := accountFailureRestore(t, f)
			firstID, secondID := first.GetAccountManagementStatus().RootWalletID, second.GetAccountManagementStatus().RootWalletID
			require.NoError(t, first.UpdateAccountMetadata(firstID, 0, "Baseline", "base.btc"))
			require.NoError(t, first.SyncAccountManagementState(context.Background()))
			require.NoError(t, second.SyncAccountManagementState(context.Background()))
			expectedName, expectedDID := "Remote name", "local.btc"
			if mode == "LocalNameRemoteDID" {
				expectedName, expectedDID = "Local name", "remote.btc"
				require.NoError(t, second.UpdateAccountMetadata(secondID, 0, "Baseline", expectedDID))
				require.NoError(t, first.UpdateAccountMetadata(firstID, 0, expectedName, "base.btc"))
			} else {
				if mode == "ClearDIDRemoteName" {
					expectedDID = ""
				}
				require.NoError(t, second.UpdateAccountMetadata(secondID, 0, expectedName, "base.btc"))
				require.NoError(t, first.UpdateAccountMetadata(firstID, 0, "Baseline", expectedDID))
			}
			require.NoError(t, second.SyncAccountManagementState(context.Background()))
			require.NoError(t, first.SyncAccountManagementState(context.Background()))
			require.NoError(t, second.SyncAccountManagementState(context.Background()))
			// Recover a third device from the actual authoritative remote records.
			third := accountFailureRestore(t, f)
			for _, device := range []*wallet.Manager{first, second, third} {
				rootID := device.GetAccountManagementStatus().RootWalletID
				var root *wallet.WalletCatalogEntry
				for _, entry := range device.GetWalletCatalog() {
					if entry.ID == rootID {
						copyEntry := entry
						root = &copyEntry
						break
					}
				}
				require.NotNil(t, root)
				require.Equal(t, expectedName, root.Accounts[0].Name)
				require.Equal(t, expectedDID, root.Accounts[0].DID)
				require.Zero(t, device.GetAccountManagementStatus().PendingChanges)
			}
		})
	}
}
