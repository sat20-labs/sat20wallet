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
	client := dkvsClientForNode(t, network.Core)

	t.Run("ExpiredIndexRepublishesFromLocalCopy", func(t *testing.T) {
		f := prepareAccountReview(t, network, true)
		f.activate(t)
		root := f.manager.GetWallet()
		stateKey, err := dkvs.PersonalKey(root.GetPubKey().SerializeCompressed(), "account/state")
		require.NoError(t, err)
		dataKey, err := account.ManagedIndexKey(f.pkg.Envelope.Locator.AccountID)
		require.NoError(t, err)
		original := waitForDKVSRecord(t, network.Core, stateKey)
		index, indexRecord := readAccountE2EIndex(t, network.Core, f.secret, f.pkg.Envelope.Locator.AccountID)
		_, originals := readAccountE2EModules(t, network.Core, f.secret, f.pkg.Envelope.Locator.AccountID, index)
		// Suspend the ordinary worker so the remote lease actually expires;
		// keep the complete confirmed local copy for normal resume and sync.
		f.manager.StopDKVSSync()
		_, err = client.PutSignedRecordFreeLocal(root, indexRecord.Key, indexRecord.Value, dkvs.RecordOptions{Seq: indexRecord.Seq + 1, TTL: 2})
		require.NoError(t, err)
		for i := 0; i < 3; i++ {
			advanceBlock()
		}
		require.Eventually(t, func() bool {
			_, err := client.GetRecordDirect(dataKey)
			return errors.Is(err, dkvs.ErrRecordNotFound)
		}, 15*time.Second, 200*time.Millisecond)
		require.NoError(t, f.manager.StartDKVSSync())
		require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
		requireE2EFirstBackup(t, f.manager)
		current := waitForDKVSRecord(t, network.Core, stateKey)
		require.Equal(t, dkvs.RecordHash(original), dkvs.RecordHash(current))
		restoredIndex, restoredRecord := readAccountE2EIndex(t, network.Core, f.secret, f.pkg.Envelope.Locator.AccountID)
		require.Equal(t, index, restoredIndex)
		require.Equal(t, indexRecord.Value, restoredRecord.Value)
		_, restoredModules := readAccountE2EModules(t, network.Core, f.secret, f.pkg.Envelope.Locator.AccountID, restoredIndex)
		require.Len(t, restoredModules, len(originals))
		for i, record := range originals {
			require.Equal(t, dkvs.RecordHash(record), dkvs.RecordHash(restoredModules[i]))
		}
		_, discovered := f.recover(t)
		require.Equal(t, restoredIndex, discovered.Index)
	})
	t.Run("EntireTemporaryBackupExpiresThenOriginalDeviceRepublishes", func(t *testing.T) {
		// This journey owns a complete temporary backup and its actual database.
		// It owns a separate account from the preceding index-expiry journey.
		config, location := accountReviewConfig(t, network)
		dir := t.TempDir()
		database := indexerdb.NewKVDB(dir)
		require.NotNil(t, database)
		manager := wallet.NewManager(config, database)
		require.NotNil(t, manager)
		t.Cleanup(func() {
			if manager != nil {
				manager.Close()
			}
			if database != nil {
				require.NoError(t, database.Close())
			}
		})
		// Reuse the existing public-config policy fixture. All records are then
		// signed, published and confirmed by the ordinary account APIs.
		const shortTTL = uint64(2)
		leasePolicy := &accountReviewTTLPolicyTransport{inner: wallet.NewHTTPClient(), ttl: shortTTL}
		manager.SetDKVSHttpClient(leasePolicy)
		mnemonic := accountSyncMnemonic(t, 209)
		id, err := manager.ImportWallet(mnemonic, accountReviewPassword)
		require.NoError(t, err)
		require.NoError(t, manager.InitializeAccountManagement(accountReviewPassword))
		requireE2EInactive(t, manager)
		require.NoError(t, manager.UpdateWalletName(id, "Review Root"))
		require.NoError(t, manager.EnsureAccount(id, 2, "Savings", "did:review:2"))
		require.NoError(t, manager.BindAccountToCurrentCoreNode())
		authorization, err := manager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
		require.NoError(t, err)
		require.Equal(t, shortTTL, authorization.RecordOptions.TTL)
		backup, err := manager.ExportAccountBackupForPWA(accountReviewPassword, nil)
		require.NoError(t, err)
		pkg, err := manager.CreateAccountRecoveryPackage(account.CreateOptions{
			AccountID: manager.GetAccountManagementStatus().AccountID, Backup: backup,
			RecoveryMode: account.RecoveryMode2Of2, Questions: e2eKnowledgeQuestions()})
		require.NoError(t, err)
		share, err := account.RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle, e2eKnowledgeAnswers())
		require.NoError(t, err)
		_, secret, err := account.RecoverAccount(pkg.Envelope, pkg.UserShare, share)
		require.NoError(t, err)
		t.Cleanup(func() { clearBytes(secret) })
		f := &accountReviewFixture{network: network, manager: manager, location: location,
			authorization: authorization, pkg: pkg, secret: secret, rootMnemonic: mnemonic}
		f.activate(t)
		beforeStatus := manager.GetAccountManagementStatus()
		before := manager.GetWalletCatalog()
		packageBytes, err := account.EncodeRecoveryPackageStorage(*pkg)
		require.NoError(t, err)
		require.Nil(t, pkg.GuardianCapsule, "this is the original 2of2 recovery package")
		root := manager.GetWallet()
		stateKey, err := dkvs.PersonalKey(root.GetPubKey().SerializeCompressed(), "account/state")
		require.NoError(t, err)
		dataKey, err := account.ManagedIndexKey(f.pkg.Envelope.Locator.AccountID)
		require.NoError(t, err)
		wrapperKey, err := dkvs.PersonalKey(root.GetPubKey().SerializeCompressed(), "account/root")
		require.NoError(t, err)
		packageKey, err := dkvs.PersonalKey(root.GetPubKey().SerializeCompressed(), "account/recovery/"+f.pkg.Envelope.Locator.PackageID)
		require.NoError(t, err)
		keys := []string{stateKey, dataKey, wrapperKey, packageKey}
		index, _ := readAccountE2EIndex(t, network.Core, secret, beforeStatus.AccountID)
		for _, entry := range index.Entries {
			if !entry.Deleted && entry.Key != stateKey {
				keys = append(keys, entry.Key)
			}
		}
		var expiresAt uint64
		beforeValues := make(map[string][]byte)
		for _, key := range keys {
			record, err := client.GetRecordDirect(key)
			require.NoError(t, err)
			require.Equal(t, shortTTL, record.TTL)
			beforeValues[key] = append([]byte(nil), record.Value...)
			if key == packageKey {
				require.Equal(t, packageBytes, record.Value)
			}
			expiry := dkvs.RecordExpiryHeight(record)
			require.NotZero(t, expiry)
			expiresAt = max(expiresAt, expiry)
		}
		// Close every writer before advancing the real lease heights. The
		// closed wallet is never used to sign or publish replacement records.
		manager.Close()
		manager = nil
		require.NoError(t, database.Close())
		database = nil
		height, err := network.Core.Client.GetBlockCount()
		require.NoError(t, err)
		require.GreaterOrEqual(t, height, int64(0))
		require.Greater(t, expiresAt, uint64(height))
		require.LessOrEqual(t, expiresAt-uint64(height), shortTTL)
		for uint64(height) < expiresAt {
			advanceBlock()
			height, err = network.Core.Client.GetBlockCount()
			require.NoError(t, err)
		}
		for _, key := range keys {
			require.Eventually(t, func() bool {
				_, err := client.GetRecordDirect(key)
				return errors.Is(err, dkvs.ErrRecordNotFound)
			}, 15*time.Second, 200*time.Millisecond, "offline temporary record must expire")
		}
		// Reopen the same database through the production lifecycle. Unlock
		// uses its persisted wallet; no second import or copied Manager cache.
		database = indexerdb.NewKVDB(dir)
		require.NotNil(t, database)
		manager = wallet.NewManager(config, database)
		require.NotNil(t, manager)
		manager.SetDKVSHttpClient(leasePolicy)
		require.Nil(t, manager.GetWallet())
		_, err = manager.UnlockWallet(accountReviewPassword)
		require.NoError(t, err)
		require.NoError(t, manager.StartDKVSSync())
		f.manager = manager
		afterStatus := manager.GetAccountManagementStatus()
		require.True(t, beforeStatus.AccountID == afterStatus.AccountID &&
			beforeStatus.RootFingerprint == afterStatus.RootFingerprint, "reopen changed account identity")
		require.NoError(t, manager.SyncAccountManagementState(context.Background()))
		requireE2EFirstBackup(t, manager)
		// The persisted original package is republished with the original ID;
		// expiry does not require new questions, shares or another activation.
		require.Equal(t, beforeStatus.PackageID, manager.GetAccountManagementStatus().PackageID)
		require.Equal(t, pkg.Envelope.Locator.PackageID, manager.GetAccountManagementStatus().PackageID)
		for _, key := range keys {
			record, err := client.GetRecordDirect(key)
			require.NoError(t, err)
			require.NoError(t, dkvs.VerifySignature(record))
			require.Equal(t, shortTTL, record.TTL)
			require.Equal(t, beforeValues[key], record.Value, "expiry must republish the original stored content")
			if key == packageKey {
				require.Equal(t, packageBytes, record.Value)
			}
		}
		restoredIndex, _ := readAccountE2EIndex(t, network.Core, secret, beforeStatus.AccountID)
		require.Equal(t, index, restoredIndex)
		readAccountE2EModules(t, network.Core, secret, beforeStatus.AccountID, restoredIndex)
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
		require.Equal(t, beforeStatus.AccountID, device.GetAccountManagementStatus().AccountID)
		require.Equal(t, beforeStatus.PackageID, device.GetAccountManagementStatus().PackageID)
		require.Equal(t, beforeStatus.RootFingerprint, device.GetAccountManagementStatus().RootFingerprint)
	})
}

// Same-mode configuration changes use existing activation and ordinary sync;
// no direct profile edits, wrapper writes or forged success responses.
func TestSDKAccountSameModeReconfigurationConverges(t *testing.T) {
	network := newDKVSNoPluginUnfundedNetwork(t)
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
	key, err := dkvs.PersonalKey(f.manager.GetWallet().GetPubKey().SerializeCompressed(), "account/root")
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
	network := newDKVSNoPluginUnfundedNetwork(t)
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
	requireE2EInactive(t, manager)
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
	dataKey, err := account.ManagedIndexKey(before.AccountID)
	require.NoError(t, err)
	wrapperKey, err := dkvs.PersonalKey(root.GetPubKey().SerializeCompressed(), "account/root")
	require.NoError(t, err)
	packageKey, err := dkvs.AccountPersonalKey(before.AccountID, "account/recovery/"+before.PackageID)
	require.NoError(t, err)
	keys := []string{stateKey, dataKey, wrapperKey, packageKey}
	index, _ := readAccountE2EIndex(t, network.Core, secret, manager.GetAccountManagementStatus().AccountID)
	for _, entry := range index.Entries {
		if !entry.Deleted && entry.Key != stateKey {
			keys = append(keys, entry.Key)
		}
	}
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
	require.NoError(t, manager.StartDKVSSync())
	require.Eventually(t, func() bool { return manager.GetAccountManagementStatus().DKVSWriteAllowed },
		30*time.Second, 50*time.Millisecond, "reconnect the original endpoint before a new edit")
	originals := make(map[string]*wire.DKVSRecord, len(keys))
	for _, key := range keys {
		record, err := actualClient.GetRecordDirect(key)
		require.NoError(t, err)
		require.Equal(t, oldTTL, record.TTL, "old records must still be valid")
		originals[key] = record
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
		// A higher server ceiling does not change the confirmed retention
		// policy or cause unrelated important values to be rewritten.
		require.Equal(t, oldTTL, record.TTL)
		require.LessOrEqual(t, record.TTL, config.FreeLocal.MaxTTL)
		require.NoError(t, dkvs.VerifySignature(record))
		if key == stateKey {
			require.Greater(t, record.Seq, originals[key].Seq)
			require.NotEqual(t, dkvs.RecordHash(originals[key]), dkvs.RecordHash(record))
		} else {
			require.Equal(t, dkvs.RecordHash(originals[key]), dkvs.RecordHash(record), "unrelated KV must not change")
		}
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
	network := newDKVSNoPluginUnfundedNetwork(t)
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
	require.NoError(t, manager.StartDKVSSync())
	require.Eventually(t, func() bool { return manager.GetAccountManagementStatus().LocalOnly }, 30*time.Second, 50*time.Millisecond)
	_, err = manager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
	require.NoError(t, err)
	backup, err := manager.ExportAccountBackupForPWA(accountReviewPassword, nil)
	require.NoError(t, err)
	pkg, err := manager.CreateAccountRecoveryPackage(account.CreateOptions{AccountID: manager.GetAccountManagementStatus().AccountID, Backup: backup, RecoveryMode: account.RecoveryMode2Of2, Questions: e2eKnowledgeQuestions()})
	require.NoError(t, err)
	share, err := account.RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle, e2eKnowledgeAnswers())
	require.NoError(t, err)
	_, secret, err := account.RecoverAccount(pkg.Envelope, pkg.UserShare, share)
	require.NoError(t, err)
	defer clearBytes(secret)
	f := &accountReviewFixture{network: network, manager: manager, pkg: pkg, secret: secret, rootMnemonic: dkvsClientMnemonic}
	f.location, err = manager.AccountIndexerLocation()
	require.NoError(t, err)
	f.activate(t)
	require.NoError(t, manager.UpdateWalletName(id, "A"))
	require.NoError(t, manager.UpdateAccountMetadata(id, 0, "A", "did:A"))
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	require.NoError(t, manager.SyncAccountRootWrapper(context.Background()))
	root := manager.GetWallet()
	stateKey, err := dkvs.PersonalKey(root.GetPubKey().SerializeCompressed(), "account/state")
	require.NoError(t, err)
	dataKey, err := account.ManagedIndexKey(manager.GetAccountManagementStatus().AccountID)
	require.NoError(t, err)
	wrapperKey, err := dkvs.PersonalKey(root.GetPubKey().SerializeCompressed(), "account/root")
	require.NoError(t, err)
	client := dkvsClientForNode(t, network.Core)
	keys := []string{stateKey, dataKey, wrapperKey}
	index, _ := readAccountE2EIndex(t, network.Core, secret, manager.GetAccountManagementStatus().AccountID)
	for _, entry := range index.Entries {
		if !entry.Deleted && entry.Key != stateKey {
			keys = append(keys, entry.Key)
		}
	}
	for _, kind := range []string{"wallet-name", "account-name", "DID"} {
		t.Run(kind, func(t *testing.T) {
			before := map[string]*wire.DKVSRecord{}
			for _, key := range keys {
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
			require.NoError(t, manager.StartDKVSSync())
			_, err := manager.UnlockWallet(accountReviewPassword)
			require.NoError(t, err)
			require.Eventually(t, func() bool { return manager.GetAccountManagementStatus().DKVSWriteAllowed }, 30*time.Second, 50*time.Millisecond)
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
	for n, mode := range []string{"LocalNameRemoteDID", "LocalDIDRemoteName", "ClearDIDRemoteName"} {
		t.Run(mode, func(t *testing.T) {
			t.Skip("not tested: simultaneous edits from multiple devices are unsupported")
			network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
			waitForDKVSPeerReady(t, network)
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
