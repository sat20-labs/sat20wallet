package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const accountReviewPassword = "123456"

// No public endpoints, no injected account state and no background binding job.
// Only isolated node/mining/Bitcoin evidence setup uses the E2E harness; all
// account business operations below enter public SDK interfaces.
func accountReviewDevice(t *testing.T, network *realSatoshiNet, mnemonic string,
	observe ...**accountBackupTrace) (*wallet.Manager, wallet.AccountIndexerLocation) {
	t.Helper()
	config, location := accountReviewConfig(t, network)
	databaseDir := t.TempDir()
	database := indexerdb.NewKVDB(databaseDir)
	require.NotNil(t, database)
	manager := wallet.NewManager(config, database)
	require.NotNil(t, manager)
	t.Cleanup(func() { manager.Close(); database.Close() })
	if len(observe) != 0 {
		trace := &accountBackupTrace{t: t, inner: wallet.NewHTTPClient(), db: database,
			namespace: config.Env + ":" + config.Chain, device: accountTraceTag(databaseDir)}
		manager.SetDKVSHttpClient(trace)
		*observe[0] = trace
	}
	if mnemonic != "" {
		_, err := manager.ImportWallet(mnemonic, accountReviewPassword)
		require.NoError(t, err)
	}
	return manager, location
}

func accountReviewConfig(t *testing.T, network *realSatoshiNet) (*sdkcommon.Config, wallet.AccountIndexerLocation) {
	t.Helper()
	locationFor := func(node *testHarness) wallet.AccountIndexerLocation {
		raw, err := node.IndexerURL("testnet")
		require.NoError(t, err)
		u, err := url.Parse(raw)
		require.NoError(t, err)
		return wallet.AccountIndexerLocation{Scheme: u.Scheme, Host: u.Host, Proxy: strings.Trim(u.Path, "/")}
	}
	location := locationFor(network.Core)
	bootstrap := locationFor(network.Bootstrap)
	config := &sdkcommon.Config{
		Env: "test", Chain: "testnet",
		IndexerL1: &sdkcommon.Indexer{Scheme: bootstrap.Scheme, Host: bootstrap.Host, Proxy: bootstrap.Proxy},
		IndexerL2: &sdkcommon.Indexer{Scheme: location.Scheme, Host: location.Host, Proxy: location.Proxy},
		Peers: []string{
			"s@" + network.Core.nodePubKey + "@http://" + network.Core.stpAddr + "/testnet",
			"b@" + network.Bootstrap.nodePubKey + "@http://" + network.Bootstrap.stpAddr + "/testnet",
		},
	}
	return config, location
}

type accountReviewFixture struct {
	network       *realSatoshiNet
	manager       *wallet.Manager
	location      wallet.AccountIndexerLocation
	authorization *wallet.AccountStorageAuthorization
	pkg           *account.RecoveryPackage
	secret        []byte
	rootMnemonic  string
	trace         *accountBackupTrace
}

func prepareAccountReview(t *testing.T, network *realSatoshiNet, bound bool) *accountReviewFixture {
	t.Helper()
	return prepareAccountReviewWithMnemonic(t, network, dkvsClientMnemonic, bound)
}

func prepareAccountReviewWithMnemonic(t *testing.T, network *realSatoshiNet, mnemonic string, bound bool) *accountReviewFixture {
	t.Helper()
	var trace *accountBackupTrace
	manager, location := accountReviewDevice(t, network, mnemonic, &trace)
	if mnemonic == "" {
		_, createdMnemonic, err := manager.CreateWallet(accountReviewPassword)
		require.NoError(t, err)
		mnemonic = createdMnemonic
	}
	require.NoError(t, manager.InitializeAccountManagement(accountReviewPassword))
	require.NoError(t, manager.StartDKVSSync())
	t.Cleanup(manager.StopDKVSSync)
	require.Eventually(t, func() bool {
		status := manager.GetAccountManagementStatus()
		return status.LocalOnly || status.RecoveryConfigured && status.DKVSWriteAllowed
	}, 20*time.Second, 20*time.Millisecond, "establish authenticated account discovery before fixture edits")
	root := manager.GetWalletCatalog()[0]
	require.NoError(t, manager.UpdateWalletName(root.ID, "Review Root"))
	require.NoError(t, manager.EnsureAccount(root.ID, 2, "Savings", "did:review:2"))
	if bound {
		require.NoError(t, manager.BindAccountToCurrentCoreNode())
	}
	auth, err := manager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
	require.NoError(t, err)
	backup, err := manager.ExportAccountBackupForPWA(accountReviewPassword, nil)
	require.NoError(t, err)
	pkg, err := manager.CreateAccountRecoveryPackage(account.CreateOptions{
		AccountID: manager.GetAccountManagementStatus().AccountID,
		Backup:    backup, RecoveryMode: account.RecoveryMode2Of2, Questions: e2eKnowledgeQuestions(),
	})
	require.NoError(t, err)
	share, err := account.RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle, e2eKnowledgeAnswers())
	require.NoError(t, err)
	_, secret, err := account.RecoverAccount(pkg.Envelope, pkg.UserShare, share)
	require.NoError(t, err)
	t.Cleanup(func() { clearBytes(secret) })
	return &accountReviewFixture{network: network, manager: manager, location: location, authorization: auth, pkg: pkg, secret: secret, rootMnemonic: mnemonic, trace: trace}
}

func (f *accountReviewFixture) activate(t *testing.T) {
	t.Helper()
	phase := "claim-authorization"
	err := f.manager.UseAccountStorageAuthorization(wallet.AccountStoragePurposeRecovery,
		func(auth *wallet.AccountStorageAuthorization) error {
			phase = "prepare-repository"
			repository, err := f.manager.NewAccountRepositoryForStorage(*auth)
			if err != nil {
				return err
			}
			phase = "publish-recovery-package"
			if err := account.NewManager(repository).Publish(context.Background(), *f.pkg); err != nil {
				return err
			}
			phase = "activate-configuration"
			err = f.manager.ActivateAccountManagement(f.secret, accountReviewPassword, *auth,
				f.pkg.Envelope.Locator, "account://"+f.pkg.Envelope.Locator.PackageID)
			if err == nil {
				phase = "finish-authorization"
			}
			return err
		})
	if err != nil {
		status := f.manager.GetAccountManagementStatus()
		t.Logf("account review activation: phase=%s discovery=%s active=%t configured=%t activation_pending=%t write_allowed=%t dirty=%t pending=%d",
			phase, status.DiscoveryStatus, status.Active, status.RecoveryConfigured,
			status.ActivationPending, status.DKVSWriteAllowed, status.ManagedDataDirty, status.PendingChanges)
	}
	require.NoError(t, err, "recovery workflow phase=%s", phase)
	if f.trace != nil {
		f.trace.checkpoint("before-wait", nil)
	}
	requireE2EFirstBackup(t, f.manager)
	if f.trace != nil {
		f.trace.checkpoint("after-wait", nil)
	}
}

func (f *accountReviewFixture) restoreOptions() wallet.AccountManagementRestoreOptions {
	return wallet.AccountManagementRestoreOptions{Location: f.location, StorageMode: wallet.AccountStorageTemporary,
		RecordTTL: f.authorization.RecordOptions.TTL, PublicLocator: "account://" + f.pkg.Envelope.Locator.PackageID}
}

func (f *accountReviewFixture) recover(t *testing.T) (*wallet.Manager, *wallet.RecoveredAccountManagementState) {
	t.Helper()
	device, _ := accountReviewDevice(t, f.network, "")
	recovered, err := device.LoadAccountManagementStateForRecovery(f.location, f.pkg.Envelope.Locator, f.secret, f.rootMnemonic)
	require.NoError(t, err)
	require.NotNil(t, recovered)
	return device, recovered
}

func accountReviewClone(t *testing.T, value *wallet.RecoveredAccountManagementState) wallet.RecoveredAccountManagementState {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	var clone wallet.RecoveredAccountManagementState
	require.NoError(t, json.Unmarshal(encoded, &clone))
	return clone
}

type accountReviewPilotSnapshot struct {
	height         int32
	tip            string
	bindingPresent bool
	bindingSeq     uint64
	bindingHash    string
}

func captureAccountReviewPilotSnapshot(t *testing.T, network *realSatoshiNet, mnemonic string) accountReviewPilotSnapshot {
	t.Helper()
	key := keyFromMnemonic(t, mnemonic, 0)
	actor := newDKVSKeyPathActor(t, key)
	bindingKey, err := dkvs.AccountMappingKey("testnet", actor.Address)
	require.NoError(t, err)

	tip, height, err := network.Core.Client.GetBestBlock()
	require.NoError(t, err)
	record, err := dkvsClientForNode(t, network.Core).GetRecordDirect(bindingKey)
	if errors.Is(err, dkvs.ErrRecordNotFound) {
		return accountReviewPilotSnapshot{height: height, tip: tip.String()}
	}
	require.NoError(t, err)
	require.NotNil(t, record)
	return accountReviewPilotSnapshot{height: height, tip: tip.String(), bindingPresent: true,
		bindingSeq: record.Seq, bindingHash: dkvs.RecordHash(record).String()}
}

func requireAccountReviewBindingDelta(t *testing.T, network *realSatoshiNet, mnemonic string,
	before accountReviewPilotSnapshot) {
	t.Helper()
	after := captureAccountReviewPilotSnapshot(t, network, mnemonic)
	t.Logf("account-review-group-state: height=%d->%d tip_changed=%t binding=%t:%d->%t:%d hash_changed=%t",
		before.height, after.height, before.tip != after.tip,
		before.bindingPresent, before.bindingSeq, after.bindingPresent, after.bindingSeq,
		before.bindingHash != after.bindingHash)
	require.GreaterOrEqual(t, after.height, before.height, "the shared chain must not move backwards")
	require.True(t, after.bindingPresent, "the case must leave its expected account binding")
	require.Greater(t, after.bindingSeq, before.bindingSeq, "the case must advance its namespaced binding")
	require.NotEqual(t, before.bindingHash, after.bindingHash, "the binding state must change")
}

func runAccountReviewCaseWithBindingDelta(t *testing.T, network *realSatoshiNet, mnemonic string,
	run func(*testing.T, *realSatoshiNet, string)) {
	t.Helper()
	before := captureAccountReviewPilotSnapshot(t, network, mnemonic)
	require.False(t, before.bindingPresent, "a fresh case identity must have no prior remote binding")
	t.Cleanup(func() { requireAccountReviewBindingDelta(t, network, mnemonic, before) })
	run(t, network, mnemonic)
}

// These review cases use identical no-plugin nodes; unique mnemonics keep their
// remote account records independent while the network and block history grow.
func TestSDKAccountReviewSharedNetworkPilot(t *testing.T) {
	setupDone := e2ePhase(t, "account_review_pilot_network_setup")
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	setupDone()
	runSDKAccountReviewSharedNetworkPilot(t, network)
}

func sharedE2ERerunSelector(t *testing.T, child string) string {
	t.Helper()
	parts := append(strings.Split(t.Name(), "/"), child)
	for i, part := range parts {
		parts[i] = "^" + part + "$"
	}
	return strings.Join(parts, "/")
}

func sharedE2ERootRerunSelector(t *testing.T) string {
	t.Helper()
	return "^" + strings.SplitN(t.Name(), "/", 2)[0] + "$"
}

func runSDKAccountReviewSharedNetworkPilot(t *testing.T, network *realSatoshiNet) {
	t.Helper()
	cases := []struct {
		name string
		seed byte
		run  func(*testing.T, *realSatoshiNet, string)
	}{
		{name: "ReviewLifecycle", seed: 250, run: runSDKAccountReviewLifecycle},
		{name: "ReviewRestoreValidation", seed: 251, run: runSDKAccountReviewRestoreValidation},
		{name: "ReviewActivationWithoutPrebinding", seed: 253, run: runSDKAccountReviewActivationWithoutPrebinding},
	}
	for _, testCase := range cases {
		testCase := testCase
		passed := t.Run(testCase.name, func(t *testing.T) {
			mnemonic := accountSyncMnemonic(t, testCase.seed)
			before := captureAccountReviewPilotSnapshot(t, network, mnemonic)
			require.False(t, before.bindingPresent, "a fresh case identity must have no prior remote binding")
			t.Cleanup(func() {
				after := captureAccountReviewPilotSnapshot(t, network, mnemonic)
				t.Logf("account-review-pilot-state: case=%s height=%d->%d tip_changed=%t binding=%t:%d->%t:%d binding_hash_changed=%t",
					testCase.name, before.height, after.height, before.tip != after.tip,
					before.bindingPresent, before.bindingSeq, after.bindingPresent, after.bindingSeq,
					before.bindingHash != after.bindingHash)
				require.GreaterOrEqual(t, after.height, before.height, "the shared chain must not move backwards")
				require.True(t, after.bindingPresent, "the case must leave its expected account binding")
				require.Greater(t, after.bindingSeq, before.bindingSeq, "the case must advance its namespaced binding")
				require.NotEqual(t, before.bindingHash, after.bindingHash, "the binding state must change")
			})
			testCase.run(t, network, mnemonic)
		})
		if !passed {
			onlySelector := sharedE2ERerunSelector(t, testCase.name)
			rootSelector := sharedE2ERootRerunSelector(t)
			t.Fatalf("stopped shared-network pilot after %s failed; rerun only it on a fresh network with -run '%s' or rerun the whole fresh group with -run '%s'", testCase.name, onlySelector, rootSelector)
		}
	}
}

// The original standalone tests below remain individually selectable. This
// group shares one fresh no-plugin network across activation and recovery
// paths; every root identity and local manager database stays case-scoped.
func TestSDKAccountNoPluginRecoverySharedNetworkGroup(t *testing.T) {
	setupDone := e2ePhase(t, "account_no_plugin_recovery_group_setup")
	fixture := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t))
	network := fixture.Network
	waitForDKVSPeerReady(t, network)
	setupDone()

	cases := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "ActivationRetry", run: func(t *testing.T) {
			mnemonic := accountSyncMnemonic(t, 249)
			runAccountReviewCaseWithBindingDelta(t, network, mnemonic, runSDKAccountActivationRetry)
		}},
		{name: "ReviewLifecycleRestoreAndActivation", run: func(t *testing.T) {
			runSDKAccountReviewSharedNetworkPilot(t, network)
		}},
		{name: "BackupKeyRecreatedFromRoot", run: func(t *testing.T) {
			mnemonic := accountSyncMnemonic(t, 219)
			runAccountReviewCaseWithBindingDelta(t, network, mnemonic, runSDKAccountBackupKeyRecreatedFromRoot)
		}},
		{name: "GuardianIdentitySurvivesAccountRecovery", run: func(t *testing.T) {
			guardianMnemonic := accountSyncMnemonic(t, 217)
			userMnemonic := accountSyncMnemonic(t, 218)
			runAccountReviewCaseWithBindingDelta(t, network, guardianMnemonic, func(t *testing.T,
				network *realSatoshiNet, _ string) {
				runSDKAccountGuardianIdentitySurvivesAccountRecovery(t, network, guardianMnemonic, userMnemonic)
			})
		}},
		{name: "DKVSReplicaAndLocalRecovery", run: func(t *testing.T) {
			runSDKDKVSNoPluginSharedNetworkGroup(t, fixture)
		}},
		{name: "DKVSSyncGate", run: func(t *testing.T) {
			runSDKAccountDKVSSyncGate(t, fixture)
		}},
	}
	for _, testCase := range cases {
		testCase := testCase
		if !t.Run(testCase.name, testCase.run) {
			onlySelector := sharedE2ERerunSelector(t, testCase.name)
			rootSelector := sharedE2ERootRerunSelector(t)
			t.Fatalf("stopped shared-network group after %s failed; rerun only it on a fresh network with -run '%s', or rerun the whole group with -run '%s'",
				testCase.name, onlySelector, rootSelector)
		}
	}
}

func TestSDKAccountReviewLifecycle(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	runSDKAccountReviewLifecycle(t, network, dkvsClientMnemonic)
}

func runSDKAccountReviewLifecycle(t *testing.T, network *realSatoshiNet, mnemonic string) {
	f := prepareAccountReviewWithMnemonic(t, network, mnemonic, true)
	f.activate(t)

	t.Run("BasicPublishedRecoveryRoundTrip", func(t *testing.T) {
		device, recovered := f.recover(t)
		result, err := device.RestoreAccountManagementState(*recovered, f.secret, accountReviewPassword, f.pkg.Envelope.Locator, f.restoreOptions())
		require.NoError(t, err)
		require.Len(t, result, 1)
		catalog := device.GetWalletCatalog()
		require.Len(t, catalog, 1)
		require.Equal(t, "Review Root", catalog[0].Name)
		require.Len(t, catalog[0].Accounts, 3)
		require.Equal(t, "Savings", catalog[0].Accounts[2].Name)
		require.Equal(t, "did:review:2", catalog[0].Accounts[2].DID)
		require.Equal(t, f.manager.GetWalletCatalog()[0].Accounts, catalog[0].Accounts)
		status := device.GetAccountManagementStatus()
		require.True(t, status.Active)
		require.True(t, status.RecoveryConfigured)
		require.Equal(t, f.pkg.Envelope.Locator.AccountID, status.AccountID)
		require.Equal(t, recovered.Seq, status.StateSeq)
		require.Zero(t, status.PendingChanges)
	})

	t.Run("ReadOnlyRecoveryRejectsWrongCredentials", func(t *testing.T) {
		device, _ := accountReviewDevice(t, network, "")
		wrong := append([]byte(nil), f.secret...)
		wrong[0] ^= 1
		defer clearBytes(wrong)
		_, err := device.LoadAccountManagementStateForRecovery(f.location, f.pkg.Envelope.Locator, wrong, dkvsClientMnemonic)
		require.Error(t, err)
		_, err = device.LoadAccountManagementStateForRecovery(f.location, f.pkg.Envelope.Locator, f.secret, coreMnemonic)
		require.Error(t, err)
		require.Empty(t, device.GetWalletCatalog())
		require.False(t, device.GetAccountManagementStatus().Active)
	})

	t.Run("NoOpSyncDoesNotCreateRevision", func(t *testing.T) {
		before := f.manager.GetAccountManagementStatus()
		_, beforeCopies := f.recover(t)
		for i := 0; i < 3; i++ {
			require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
		}
		after := f.manager.GetAccountManagementStatus()
		require.Equal(t, before.StateSeq, after.StateSeq)
		_, afterCopies := f.recover(t)
		beforeRecords := make(map[string]string)
		for _, copy := range beforeCopies.Copies {
			require.NotNil(t, copy.Record)
			beforeRecords[copy.Entry.Key] = dkvs.RecordHash(copy.Record).String()
		}
		afterRecords := make(map[string]string)
		for _, copy := range afterCopies.Copies {
			require.NotNil(t, copy.Record)
			afterRecords[copy.Entry.Key] = dkvs.RecordHash(copy.Record).String()
		}
		require.Equal(t, beforeRecords, afterRecords)
		require.Zero(t, after.PendingChanges)
	})

	t.Run("RestoreCannotOverwriteExistingWallet", func(t *testing.T) {
		_, recovered := f.recover(t)
		device, _ := accountReviewDevice(t, network, coreMnemonic)
		before := device.GetWalletCatalog()
		_, err := device.RestoreAccountManagementState(*recovered, f.secret, accountReviewPassword, f.pkg.Envelope.Locator, f.restoreOptions())
		require.Error(t, err)
		require.Equal(t, before, device.GetWalletCatalog())
	})

	t.Run("RootAndInvalidCatalogueOperationsAreProtected", func(t *testing.T) {
		device, recovered := f.recover(t)
		_, err := device.RestoreAccountManagementState(*recovered, f.secret, accountReviewPassword, f.pkg.Envelope.Locator, f.restoreOptions())
		require.NoError(t, err)
		require.NoError(t, device.SyncAccountManagementState(context.Background()))
		root := device.GetWalletCatalog()[0]
		_, err = device.ImportWallet(coreMnemonic, accountReviewPassword)
		require.NoError(t, err)
		before := device.GetWalletCatalog()
		require.Error(t, device.DeleteWallet(root.ID))
		require.Error(t, device.UpdateAccountMetadata(root.ID, 99, "invalid", ""))
		require.Error(t, device.UpdateWalletName(root.ID, "  "))
		_, err = device.ImportWallet(f.rootMnemonic, accountReviewPassword)
		require.ErrorIs(t, err, wallet.ErrWalletAlreadyExists)
		require.Equal(t, before, device.GetWalletCatalog())
	})
}

// These assertions intentionally fail if the public restore boundary accepts
// mismatched authenticated and decoded inputs. They are not opt-in/skip tests.
func TestSDKAccountReviewRestoreValidation(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	runSDKAccountReviewRestoreValidation(t, network, dkvsClientMnemonic)
}

func runSDKAccountReviewRestoreValidation(t *testing.T, network *realSatoshiNet, mnemonic string) {
	f := prepareAccountReviewWithMnemonic(t, network, mnemonic, true)
	f.activate(t)
	_, original := f.recover(t)
	tests := []struct {
		name   string
		mutate func(*wallet.RecoveredAccountManagementState, []byte, *account.Locator)
	}{
		{"WrongSecret", func(_ *wallet.RecoveredAccountManagementState, secret []byte, _ *account.Locator) { secret[0] ^= 1 }},
		{"CorruptCiphertext", func(v *wallet.RecoveredAccountManagementState, _ []byte, _ *account.Locator) {
			v.Envelope[len(v.Envelope)-1] ^= 1
		}},
		{"PlaintextNotMatchingCiphertext", func(v *wallet.RecoveredAccountManagementState, _ []byte, _ *account.Locator) {
			v.State.Wallets[0].Name = "not-authenticated"
		}},
		{"DifferentAccountLocator", func(_ *wallet.RecoveredAccountManagementState, _ []byte, locator *account.Locator) {
			locator.AccountID = strings.Repeat("0", 64)
		}},
		{"MismatchedSeq", func(v *wallet.RecoveredAccountManagementState, _ []byte, _ *account.Locator) { v.Seq++ }},
		{"CorruptModuleCiphertext", func(v *wallet.RecoveredAccountManagementState, _ []byte, _ *account.Locator) {
			for i := range v.Copies {
				copy := &v.Copies[i]
				if copy.Entry.Provider != "account" && copy.Entry.Provider != "wallets" && len(copy.Value) != 0 {
					copy.Value[len(copy.Value)-1] ^= 1
					return
				}
			}
			t.Fatal("recovery fixture has no module ciphertext")
		}},
	}
	for _, scenario := range tests {
		t.Run(scenario.name, func(t *testing.T) {
			device, _ := accountReviewDevice(t, network, "")
			value := accountReviewClone(t, original)
			secret := append([]byte(nil), f.secret...)
			defer clearBytes(secret)
			locator := f.pkg.Envelope.Locator
			require.NotEmpty(t, value.Envelope)
			require.NotEmpty(t, value.Index.Entries)
			require.NotEmpty(t, value.Copies)
			scenario.mutate(&value, secret, &locator)
			_, err := device.RestoreAccountManagementState(value, secret, accountReviewPassword, locator, f.restoreOptions())
			t.Logf("account-review: case=%s restore_error=%v wallets_after=%d active_after=%t", scenario.name, err, len(device.GetWalletCatalog()), device.GetAccountManagementStatus().Active)
			assert.Error(t, err, "restore must authenticate all supplied state before committing")
			assert.Empty(t, device.GetWalletCatalog(), "failed validation must not install wallet keys")
			assert.False(t, device.GetAccountManagementStatus().Active, "failed validation must not activate a profile")
		})
	}
}

func accountReviewLogDKVS(t *testing.T, node *testHarness) {
	t.Helper()
	if raw, err := os.ReadFile(node.LogFile()); err == nil {
		lines := strings.Split(string(raw), "\n")
		var selected []string
		for _, line := range lines {
			lower := strings.ToLower(line)
			if strings.Contains(lower, "dkvs") || strings.Contains(lower, "binding") {
				selected = append(selected, line)
			}
		}
		if len(selected) > 80 {
			selected = selected[len(selected)-80:]
		}
		t.Logf("account-review: %s DKVS log tail:\n%s", node.role, strings.Join(selected, "\n"))
	}
}

func accountReviewRequireBindingPropagation(t *testing.T, node *testHarness, key string, expected []byte) {
	t.Helper()
	client := dkvsClientForNode(t, node)
	var lastErr error
	ok := assert.Eventually(t, func() bool {
		record, err := client.GetRecordDirect(key)
		lastErr = err
		return err == nil && record != nil && string(record.Value) == string(expected)
	}, 15*time.Second, 200*time.Millisecond, "binding did not propagate to %s: %v", node.role, lastErr)
	if !ok {
		accountReviewLogDKVS(t, node)
	}
}

func TestSDKAccountReviewActivationWithoutPrebinding(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	runSDKAccountReviewActivationWithoutPrebinding(t, network, dkvsClientMnemonic)
}

func runSDKAccountReviewActivationWithoutPrebinding(t *testing.T, network *realSatoshiNet, mnemonic string) {
	f := prepareAccountReviewWithMnemonic(t, network, mnemonic, false)
	root := f.manager.GetWalletCatalog()[0]
	bindingKey, err := dkvs.AccountMappingKey("testnet", root.Accounts[0].Address)
	require.NoError(t, err)
	_, err = dkvsClientForNode(t, network.Core).GetRecordDirect(bindingKey)
	require.ErrorIs(t, err, dkvs.ErrRecordNotFound, "the test must not accidentally inherit a binding")
	var activationErr error
	require.NotPanics(t, func() {
		err = f.manager.UseAccountStorageAuthorization(wallet.AccountStoragePurposeRecovery,
			func(auth *wallet.AccountStorageAuthorization) error {
				activationErr = f.manager.ActivateAccountManagement(f.secret, accountReviewPassword, *auth,
					f.pkg.Envelope.Locator, "account://"+f.pkg.Envelope.Locator.PackageID)
				return activationErr
			})
	}, "activation must bind before generic KV writes, and must not panic on admission rejection")
	t.Logf("account-review: activation_error=%v use_error=%v recovery_configured=%t",
		activationErr, err, f.manager.GetAccountManagementStatus().RecoveryConfigured)
	require.NoError(t, err, "activation must establish its CoreNode binding before publishing state")
	require.True(t, f.manager.GetAccountManagementStatus().RecoveryConfigured)

	// BindAccount is the one pre-binding write exception. Its accepted mapping
	// is network DKVS state, so peers must learn the same signed binding through
	// ordinary DKVS P2P propagation rather than another service-specific store.
	binding := waitForDKVSRecord(t, network.Core, bindingKey)
	_, _, descriptor, err := dkvs.ValidateAccountMappingBindingRecord(binding)
	require.NoError(t, err)
	require.Equal(t, f.pkg.Envelope.Locator.AccountID, descriptor.AccountID)
	require.Equal(t, network.Core.nodePubKey, descriptor.CoreNodeID)
	accountReviewRequireBindingPropagation(t, network.Bootstrap, bindingKey, binding.Value)
	if !t.Failed() {
		accountReviewRequireBindingPropagation(t, network.Miner, bindingKey, binding.Value)
		if t.Failed() {
			accountReviewLogDKVS(t, network.Core)
			accountReviewLogDKVS(t, network.Bootstrap)
		}
	}
}
