package e2e

import (
	"context"
	"encoding/json"
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
func accountReviewDevice(t *testing.T, network *realSatoshiNet, mnemonic string) (*wallet.Manager, wallet.AccountIndexerLocation) {
	t.Helper()
	config, location := accountReviewConfig(t, network)
	database := indexerdb.NewKVDB(t.TempDir())
	require.NotNil(t, database)
	manager := wallet.NewManager(config, database)
	require.NotNil(t, manager)
	t.Cleanup(func() { manager.Close(); database.Close() })
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
}

func prepareAccountReview(t *testing.T, network *realSatoshiNet, bound bool) *accountReviewFixture {
	t.Helper()
	return prepareAccountReviewWithMnemonic(t, network, dkvsClientMnemonic, bound)
}

func prepareAccountReviewWithMnemonic(t *testing.T, network *realSatoshiNet, mnemonic string, bound bool) *accountReviewFixture {
	t.Helper()
	manager, location := accountReviewDevice(t, network, mnemonic)
	if mnemonic == "" {
		_, createdMnemonic, err := manager.CreateWallet(accountReviewPassword)
		require.NoError(t, err)
		mnemonic = createdMnemonic
	}
	require.NoError(t, manager.InitializeAccountManagement(accountReviewPassword))
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
	return &accountReviewFixture{network: network, manager: manager, location: location, authorization: auth, pkg: pkg, secret: secret, rootMnemonic: mnemonic}
}

func (f *accountReviewFixture) activate(t *testing.T) {
	t.Helper()
	require.NoError(t, f.manager.UseAccountStorageAuthorization(wallet.AccountStoragePurposeRecovery,
		func(auth *wallet.AccountStorageAuthorization) error {
			repository, err := f.manager.NewAccountRepositoryForStorage(*auth)
			if err != nil {
				return err
			}
			if err := account.NewManager(repository).Publish(context.Background(), *f.pkg); err != nil {
				return err
			}
			return f.manager.ActivateAccountManagement(f.secret, accountReviewPassword, *auth,
				f.pkg.Envelope.Locator, "account://"+f.pkg.Envelope.Locator.PackageID)
		}))
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

func TestSDKAccountReviewLifecycle(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	f := prepareAccountReview(t, network, true)
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
		for i := 0; i < 3; i++ {
			require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
		}
		after := f.manager.GetAccountManagementStatus()
		require.Equal(t, before.StateSeq, after.StateSeq)
		require.Equal(t, before.ManagedDataRevision, after.ManagedDataRevision)
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
		root := device.GetWalletCatalog()[0]
		_, err = device.ImportWallet(coreMnemonic, accountReviewPassword)
		require.NoError(t, err)
		before := device.GetWalletCatalog()
		require.Error(t, device.DeleteWallet(root.ID))
		require.Error(t, device.UpdateAccountMetadata(root.ID, 99, "invalid", ""))
		require.Error(t, device.UpdateWalletName(root.ID, "  "))
		_, err = device.ImportWallet(dkvsClientMnemonic, accountReviewPassword)
		require.ErrorIs(t, err, wallet.ErrWalletAlreadyExists)
		require.Equal(t, before, device.GetWalletCatalog())
	})
}

// These assertions intentionally fail if the public restore boundary accepts
// mismatched authenticated and decoded inputs. They are not opt-in/skip tests.
func TestSDKAccountReviewRestoreValidation(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	f := prepareAccountReview(t, network, true)
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
		{"MismatchedRevision", func(v *wallet.RecoveredAccountManagementState, _ []byte, _ *account.Locator) { v.Seq++ }},
		{"CorruptManagedDataCiphertext", func(v *wallet.RecoveredAccountManagementState, _ []byte, _ *account.Locator) {
			v.ManagedDataEnvelope[len(v.ManagedDataEnvelope)-1] ^= 1
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
			require.NotEmpty(t, value.ManagedDataEnvelope)
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
	f := prepareAccountReview(t, network, false)
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
