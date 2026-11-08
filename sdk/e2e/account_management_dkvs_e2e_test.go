package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	templateruntime "github.com/sat20-labs/satoshinet/contract/template"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func clearAccountBackup(value *account.Backup) {
	if value == nil {
		return
	}
	for index := range value.Wallets {
		clearBytes(value.Wallets[index].Secret)
		value.Wallets[index].Secret = nil
	}
}

type e2eAccountManagedProvider struct{}

func (*e2eAccountManagedProvider) ID() string { return "e2e.module" }

func (*e2eAccountManagedProvider) Export(catalog wallet.AccountManagedDataCatalog) (
	[]wallet.AccountManagedDataPayload, error) {

	return []wallet.AccountManagedDataPayload{{
		Scope:   wallet.AccountManagedDataGlobalScope,
		Payload: []byte("e2e-module-required-data|" + catalog.AccountID),
	}}, nil
}

func (*e2eAccountManagedProvider) Validate(catalog wallet.AccountManagedDataCatalog,
	payloads []wallet.AccountManagedDataPayload) error {

	if len(payloads) != 1 || payloads[0].Scope != wallet.AccountManagedDataGlobalScope ||
		string(payloads[0].Payload) != "e2e-module-required-data|"+catalog.AccountID {
		return fmt.Errorf("invalid e2e account-managed payload")
	}
	return nil
}

func (*e2eAccountManagedProvider) Import(catalog wallet.AccountManagedDataCatalog,
	payloads []wallet.AccountManagedDataPayload) error {

	return (&e2eAccountManagedProvider{}).Validate(catalog, payloads)
}

func accountReviewBlobLimit(t *testing.T, manager *wallet.Manager) uint32 {
	t.Helper()
	options, err := manager.GetAccountStorageOptions()
	require.NoError(t, err)
	for _, option := range options {
		if option.Mode == wallet.AccountStoragePaid {
			require.True(t, option.Available)
			require.GreaterOrEqual(t, option.BlobKeyLimit, option.RequiredBlobKeys)
			return option.BlobKeyLimit
		}
	}
	t.Fatal("paid account storage option is missing")
	return 0
}

func TestRealSatoshiNetAccountManagementAutopaySync(t *testing.T) {
	defaults := dkvsindexer.NetworkDefaultsForParams(&chaincfg.TestNetParams)
	fixture := newDKVSNoPluginTemplateFixtureWithArgs(t,
		map[string]int64{defaults.AutopayFeeAssetName: 20000}, nil, nil, dkvsMinerArgs(t))
	waitForDKVSPeerReady(t, fixture.Network)
	setup := prepareAccountManagementAutopaySync(t, fixture, "")
	runAccountManagementAutopaySync(t, setup)
}

type accountManagementAutopaySyncSetup struct {
	fixture           *templateFixture
	owner             *dkvsKeyPathActor
	guardianMnemonic  string
	guardianActor     *dkvsKeyPathActor
	successor         *dkvsKeyPathActor
	contractAddress   contractcommon.ContractAddress
	gasOuts           []dkvsPrevOut
	feeOuts           []dkvsPrevOut
	walletManager     *wallet.Manager
	location          wallet.AccountIndexerLocation
	guardianManager   *wallet.Manager
	guardianLocation  wallet.AccountIndexerLocation
	ownerBlobLimit    uint32
	guardianBlobLimit uint32
	accountID         string
	guardianID        string
	packageKey        string
	guardianKey       string
}

// The regular root prepares its own isolated pool. The shared group passes a
// successor mnemonic to add the DKVS paid delegate from the same asset bank.
func prepareAccountManagementAutopaySync(t *testing.T, fixture *templateFixture,
	successorMnemonic string) *accountManagementAutopaySyncSetup {
	t.Helper()
	defaults := dkvsindexer.NetworkDefaultsForParams(&chaincfg.TestNetParams)
	gas := contractcommon.GetGasAssetName()
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	guardianMnemonic := accountSyncMnemonic(t, 241)
	guardianActor := newDKVSKeyPathActor(t, keyFromMnemonic(t, guardianMnemonic, 0))
	var successor *dkvsKeyPathActor
	if successorMnemonic != "" {
		successor = newDKVSKeyPathActor(t, keyFromMnemonic(t, successorMnemonic, 0))
	}
	require.Equal(t, defaults.AutopayDeployer, owner.Address)
	require.Empty(t, defaults.AutopayRecipient)
	require.Equal(t, "1", defaults.AutopayMinAmountPerBlock)
	gasOuts := splitToDKVSKeyPathActors(t, fixture, fixture.gasAnchor, gas,
		[]int64{300000, 300000, 300000, 300000}, []int64{10000, 10000, 10000, 10000},
		[]*dkvsKeyPathActor{owner, owner, guardianActor, owner})
	feeAmounts := []int64{5000, 10000}
	feeValues := []int64{10000, 10000}
	feeRecipients := []*dkvsKeyPathActor{owner, guardianActor}
	if successor != nil {
		feeAmounts = []int64{5000, 5000, 10000}
		feeValues = []int64{10000, 10000, 10000}
		feeRecipients = []*dkvsKeyPathActor{owner, successor, guardianActor}
	}
	feeOuts := splitToDKVSKeyPathActors(t, fixture, fixture.assetAnchors[defaults.AutopayFeeAssetName],
		defaults.AutopayFeeAssetName, feeAmounts, feeValues, feeRecipients)

	content, err := defaults.AutopayContent()
	require.NoError(t, err)
	deployAssets := txAsset(gas, 290000)
	require.NoError(t, deployAssets.Merge(txAsset(defaults.AutopayFeeAssetName, 5000)))
	deploy, contractAddress := buildDKVSKeyPathTemplateDeploy(t, owner,
		contractcommon.TemplateAutopay, content, owner.Address, defaults.AutopayDeployNonce,
		[]dkvsPrevOut{gasOuts[0], feeOuts[0]}, wire.TxOut{Value: 10000, Assets: deployAssets})
	fixture.Network.sendManyAndMine(t, []*wire.MsgTx{deploy}, 0)

	pubKey := owner.Wallet.GetPubKey().SerializeCompressed()
	locationForNode := func(node *testHarness) wallet.AccountIndexerLocation {
		base, locationErr := node.IndexerURL("testnet")
		require.NoError(t, locationErr)
		parsed, locationErr := url.Parse(base)
		require.NoError(t, locationErr)
		return wallet.AccountIndexerLocation{
			Scheme: parsed.Scheme, Host: parsed.Host, Proxy: strings.Trim(parsed.Path, "/"),
		}
	}
	bootstrapLocation := locationForNode(fixture.Network.Bootstrap)
	coreLocation := locationForNode(fixture.Network.Core)
	database := indexerdb.NewKVDB(t.TempDir())
	require.NotNil(t, database)
	t.Cleanup(func() { _ = database.Close() })
	walletConfig := &sdkcommon.Config{
		Env: "test", Chain: "testnet",
		IndexerL1: &sdkcommon.Indexer{
			Scheme: bootstrapLocation.Scheme, Host: bootstrapLocation.Host, Proxy: bootstrapLocation.Proxy,
		},
		IndexerL2: &sdkcommon.Indexer{
			Scheme: coreLocation.Scheme, Host: coreLocation.Host, Proxy: coreLocation.Proxy,
		},
	}
	walletConfig.Peers = []string{"s@" + fixture.Network.Core.nodePubKey + "@http://" + fixture.Network.Core.stpAddr + "/testnet"}
	walletManager := wallet.NewManager(walletConfig, database)
	require.NotNil(t, walletManager)
	t.Cleanup(walletManager.Close)
	require.NoError(t, walletManager.RegisterAccountManagedDataProvider(&e2eAccountManagedProvider{}))
	_, err = walletManager.ImportWallet(dkvsClientMnemonic, "123456")
	require.NoError(t, err)
	require.NoError(t, walletManager.InitializeAccountManagement("123456"))
	require.Equal(t, pubKey, walletManager.GetWallet().GetPubKey().SerializeCompressed())
	guardianManager, guardianLocation := accountReviewDevice(t, fixture.Network, guardianMnemonic)
	require.NoError(t, guardianManager.RegisterAccountManagedDataProvider(&e2eAccountManagedProvider{}))
	ownerBlobLimit := accountReviewBlobLimit(t, walletManager)
	guardianBlobLimit := accountReviewBlobLimit(t, guardianManager)
	if successor != nil {
		delegateParam, delegateErr := (&contractcommon.TemplateAutopayConfigInvokeParam{
			AmountPerBlock: defaults.AutopayMinAmountPerBlock,
		}).Encode()
		require.NoError(t, delegateErr)
		delegateBaseFee, feeErr := contractcommon.GasFeeAtHeight(contractcommon.InvokeBaseGas, 0)
		require.NoError(t, feeErr)
		bootstrapDelegate := buildDKVSKeyPathTemplateInvoke(t, successor, contractAddress, 1,
			contractcommon.TemplateInvokeAPIConfig, delegateParam, []dkvsPrevOut{feeOuts[1]},
			wire.TxOut{Value: 10000, Assets: txAsset(defaults.AutopayFeeAssetName, 5000-delegateBaseFee)})
		fixture.Network.sendManyAndMine(t, []*wire.MsgTx{bootstrapDelegate}, 0)
	}

	// The owner and Guardian each pay for their own signed records. The compact
	// recovery package uses one personal slot plus one independent mailbox slot.
	// The owner is also the deployer: mark the operator share explicitly,
	// separately from this delegate's per-block business payment.
	gasFundingAmount := "280000"
	if successor != nil {
		// The shared paid-DKVS group reuses this reserve for both paid roots.
		gasFundingAmount = "289950"
	}
	config := &contractcommon.TemplateAutopayConfigInvokeParam{
		AmountPerBlock: "10", BlobKeyLimit: ownerBlobLimit, GasFundingAmount: gasFundingAmount,
	}
	configParam, err := config.Encode()
	require.NoError(t, err)
	configTx := buildDKVSKeyPathTemplateInvoke(t, owner, contractAddress, 1,
		contractcommon.TemplateInvokeAPIConfig, configParam, []dkvsPrevOut{gasOuts[1]},
		wire.TxOut{Value: 9000, Assets: txAsset(gas, 290000)})
	fixture.Network.sendManyAndMine(t, []*wire.MsgTx{configTx}, 0)
	guardianFeeOut := feeOuts[1]
	if successor != nil {
		guardianFeeOut = feeOuts[2]
	}
	guardianFunding := buildDKVSKeyPathTemplateDefaultInvoke(t, guardianActor, contractAddress,
		[]dkvsPrevOut{guardianFeeOut}, wire.TxOut{Value: 10000, Assets: txAsset(defaults.AutopayFeeAssetName, 10000)})
	fixture.Network.sendManyAndMine(t, []*wire.MsgTx{guardianFunding}, 0)
	guardianConfig, err := (&contractcommon.TemplateAutopayConfigInvokeParam{AmountPerBlock: "10", BlobKeyLimit: guardianBlobLimit}).Encode()
	require.NoError(t, err)
	guardianConfigTx := buildDKVSKeyPathTemplateInvoke(t, guardianActor, contractAddress, 1,
		contractcommon.TemplateInvokeAPIConfig, guardianConfig, []dkvsPrevOut{gasOuts[2]},
		wire.TxOut{Value: 9000, Assets: txAsset(gas, 290000)})
	fixture.Network.sendManyAndMine(t, []*wire.MsgTx{guardianConfigTx}, 0)

	// The next block performs the first per-block storage payment. AUTOPAY
	// records are accepted only after this payment is visible in contract state.
	heartbeat := buildDKVSKeyPathAssetTransfer(t, owner, gasOuts[3], gas, 290000, 9000, owner)
	fixture.Network.sendManyAndMine(t, []*wire.MsgTx{heartbeat}, 0)
	state := fetchTemplateAutopayView(t, fixture.Network.Bootstrap, contractAddress.MustEncode())
	require.Equal(t, templateruntime.AutopayStatusActive, state.Status)
	require.NotEmpty(t, state.GasBalance)
	require.NotEqual(t, "0", state.GasBalance)
	require.Empty(t, state.Recipient)
	require.Equal(t, "1", state.MinAmountPerBlock)
	require.GreaterOrEqual(t, state.PaidBlocks, int64(1))
	delegate, ok := state.Delegates[owner.Address]
	require.True(t, ok)
	require.Equal(t, "10", delegate.AmountPerBlock)
	require.Equal(t, ownerBlobLimit, delegate.BlobKeyLimit)
	require.GreaterOrEqual(t, delegate.LastPayHeight, state.CurrentBlock)

	return &accountManagementAutopaySyncSetup{
		fixture: fixture, owner: owner, guardianMnemonic: guardianMnemonic, guardianActor: guardianActor,
		successor: successor, contractAddress: contractAddress, gasOuts: gasOuts, feeOuts: feeOuts,
		walletManager: walletManager, location: coreLocation, guardianManager: guardianManager,
		guardianLocation: guardianLocation, ownerBlobLimit: ownerBlobLimit, guardianBlobLimit: guardianBlobLimit,
		accountID: dkvsindexer.AccountID(pubKey),
	}
}

func runAccountManagementAutopaySync(t *testing.T, setup *accountManagementAutopaySyncSetup) {
	t.Helper()
	fixture := setup.fixture
	owner := setup.owner
	guardianMnemonic := setup.guardianMnemonic
	contractAddress := setup.contractAddress
	walletManager := setup.walletManager
	coreLocation := setup.location
	guardianManager := setup.guardianManager
	guardianLocation := setup.guardianLocation
	pubKey := owner.Wallet.GetPubKey().SerializeCompressed()
	accountID := setup.accountID

	prefix, err := dkvsindexer.AccountPersonalKey(accountID, "account/recovery")
	require.NoError(t, err)
	coreDKVSClient := dkvsClientForNode(t, fixture.Network.Core)
	ownerBinding, err := dkvsindexer.AccountMappingKey("testnet", owner.Address)
	require.NoError(t, err)
	requireDKVSAbsent(t, fixture.Network.Core, ownerBinding)
	stateKey, err := dkvsindexer.PersonalKey(pubKey, "account/state")
	require.NoError(t, err)
	rootWrapperKey, err := dkvsindexer.PersonalKey(pubKey, "account/root")
	require.NoError(t, err)
	managedIndexKey, err := account.ManagedIndexKey(accountID)
	require.NoError(t, err)
	for _, key := range []string{stateKey, rootWrapperKey, managedIndexKey} {
		requireDKVSAbsent(t, fixture.Network.Core, key)
	}
	initialRecovery, err := coreDKVSClient.ReadPrefixContext(context.Background(), prefix)
	require.NoError(t, err)
	require.Empty(t, initialRecovery.Records, "account recovery namespace must start empty")
	guardianBinding, err := dkvsindexer.AccountMappingKey("testnet", setup.guardianActor.Address)
	require.NoError(t, err)
	requireDKVSAbsent(t, fixture.Network.Core, guardianBinding)

	require.NoError(t, guardianManager.InitializeAccountManagement(accountReviewPassword))
	require.NoError(t, guardianManager.StartDKVSSync())
	t.Cleanup(guardianManager.StopDKVSSync)
	require.Eventually(t, func() bool {
		status := guardianManager.GetAccountManagementStatus()
		return status.DiscoveryStatus == "inactive" && status.LocalOnly &&
			!status.RecoveryConfigured && !status.ActivationPending
	}, 30*time.Second, 100*time.Millisecond,
		"Guardian must establish authenticated inactive discovery before paid storage setup")
	guardianIdentity, err := guardianManager.GetOrCreateAccountGuardianIdentity(accountReviewPassword)
	require.NoError(t, err)
	guardianPublic, err := base64.RawURLEncoding.DecodeString(guardianIdentity.PublicKey)
	require.NoError(t, err)
	guardianPrivate, err := guardianManager.LoadAccountGuardianPrivateKey(accountReviewPassword)
	require.NoError(t, err)
	defer clearBytes(guardianPrivate)
	guardianID := guardianIdentity.MailboxID
	setup.guardianID = guardianID
	require.NotEqual(t, accountID, guardianID)
	initialMailbox, err := coreDKVSClient.ReadPrefixContext(context.Background(), "/mail/"+guardianID)
	require.NoError(t, err)
	require.Empty(t, initialMailbox.Records, "Guardian mailbox must start empty")
	t.Logf("paid-account-mailbox-baseline: guardian_id=%s prefix=/mail/%s records=0", guardianID, guardianID)
	questions := []account.QuestionAnswer{
		{Question: account.KnowledgeQuestion{ID: "book", Prompt: "指定版本书籍第十页最后十个字", IgnorePunctuation: true}, Answer: "月光落在安静的旧桥上", Confirmation: "月光落在安静的旧桥上"},
		{Question: account.KnowledgeQuestion{ID: "note", Prompt: "私人纸条中的指定句子", IgnorePunctuation: true}, Answer: "yellow bicycle beside the winter river", Confirmation: "yellow bicycle beside the winter river"},
		{Question: account.KnowledgeQuestion{ID: "family", Prompt: "未公开的家庭约定", IgnorePunctuation: true}, Answer: "周日傍晚六点在老树下见", Confirmation: "周日傍晚六点在老树下见"},
	}
	// Guardian must itself complete a real paid activation before it stores
	// the Owner's share. The already-active AUTOPAY delegate is reused: this
	// confirms paid storage without weakening the contract's payment rules.
	guardianAuthorization, err := guardianManager.ConfirmAccountStorage(wallet.AccountStoragePaid, 100)
	require.NoError(t, err)
	require.Equal(t, wallet.AccountStoragePaid, guardianAuthorization.Mode)
	require.Empty(t, guardianAuthorization.TransactionID, "ready Guardian AUTOPAY must not be funded twice")
	guardianBackup, err := guardianManager.ExportAccountBackupForPWA("123456", nil)
	require.NoError(t, err)
	defer clearAccountBackup(&guardianBackup)
	guardianPkg, err := guardianManager.CreateAccountRecoveryPackage(account.CreateOptions{
		AccountID: guardianID, Backup: guardianBackup, RecoveryMode: account.RecoveryMode2Of2, Questions: questions,
	})
	require.NoError(t, err)
	guardianRepo, err := guardianManager.NewAccountRepositoryForStorage(*guardianAuthorization)
	require.NoError(t, err)
	require.NoError(t, account.NewManager(guardianRepo).Publish(context.Background(), *guardianPkg))
	guardianAnswers := []account.AnswerAttempt{
		{QuestionID: "book", Answer: "月光落在安静的旧桥上。"},
		{QuestionID: "note", Answer: "yellow bicycle beside the winter river"},
	}
	guardianDKVSShare, err := account.RecoverDKVSShare(guardianPkg.DKVSShareCapsule,
		guardianPkg.KnowledgeBundle, guardianAnswers)
	require.NoError(t, err)
	guardianRestored, guardianSecret, err := account.RecoverAccount(guardianPkg.Envelope,
		guardianPkg.UserShare, guardianDKVSShare)
	require.NoError(t, err)
	defer clearBytes(guardianSecret)
	defer clearAccountBackup(&guardianRestored)
	require.NoError(t, guardianManager.ActivateAccountManagement(guardianSecret,
		"123456", *guardianAuthorization, guardianPkg.Envelope.Locator, ""))
	guardianReadyCtx, guardianReadyCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer guardianReadyCancel()
	require.NoError(t, guardianManager.WaitAccountManagedDataReady(guardianReadyCtx),
		"Guardian first paid backup must reach confirmed DKVS state and data")
	guardianStatus := guardianManager.GetAccountManagementStatus()
	require.Equal(t, "active", guardianStatus.DiscoveryStatus)
	require.True(t, guardianStatus.RecoveryConfigured)
	require.False(t, guardianStatus.ActivationPending)
	require.True(t, guardianStatus.DKVSWriteAllowed)
	require.Equal(t, wallet.AccountStoragePaid, guardianStatus.StorageMode)
	require.False(t, guardianStatus.ManagedDataDirty)
	require.Zero(t, guardianStatus.PendingChanges)
	require.Zero(t, guardianStatus.DKVSPendingRequests)
	guardianLatest, err := guardianManager.LoadAccountManagementStateForRecovery(
		guardianLocation, guardianPkg.Envelope.Locator, guardianSecret, guardianMnemonic)
	require.NoError(t, err, "Guardian's first paid backup must be independently recoverable")
	require.NotZero(t, guardianLatest.Seq)
	require.NotEmpty(t, guardianLatest.Index.Entries)
	guardianIndex, guardianIndexRecord := readAccountE2EIndex(t, fixture.Network.Core, guardianSecret, guardianID)
	guardianModules, guardianRecords := readAccountE2EModules(t, fixture.Network.Core, guardianSecret, guardianID, guardianIndex)
	require.NotEmpty(t, guardianModules)
	guardianPubKey := guardianManager.GetWallet().GetPubKey().SerializeCompressed()
	guardianStateKey, err := dkvsindexer.PersonalKey(guardianPubKey, "account/state")
	require.NoError(t, err)
	guardianDataKey, err := account.ManagedIndexKey(guardianID)
	require.NoError(t, err)
	for _, record := range append(guardianRecords, guardianIndexRecord,
		waitForDKVSRecord(t, fixture.Network.Core, guardianStateKey),
		waitForDKVSRecord(t, fixture.Network.Core, guardianDataKey)) {
		require.Zero(t, record.TTL)
		proof, proofErr := dkvsindexer.ParseFeeProof(record.FeeProof)
		require.NoError(t, proofErr)
		require.Equal(t, dkvsindexer.FeeModeAutopay, proof.Mode)
		require.Equal(t, contractAddress.MustEncode(), proof.PoolContract)
		t.Logf("paid-account-proof: owner=guardian key=%s seq=%d hash=%s mode=%s pool=%s",
			record.Key, record.Seq, dkvsindexer.RecordHash(record).String(), proof.Mode, proof.PoolContract)
	}

	backup := account.Backup{Version: account.BackupVersion, Wallets: []account.WalletBackup{{
		Name: "Primary", Type: account.WalletMnemonic, Secret: []byte("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"),
		AccountCount: 2, SubAccounts: []account.SubAccount{{Index: 0, DID: "alice"}, {Index: 1, DID: "alice-work"}},
	}}}
	autopay := wallet.DKVSAutopayOptions{AddressParams: &chaincfg.TestNetParams, PoolContract: contractAddress.MustEncode()}
	recordOptions := dkvsindexer.RecordOptions{Seq: 1}
	// Recovery publication is a generic KV write too. Establish and verify the
	// real CoreNode binding through the public SDK before constructing outbox
	// operations; neither AUTOPAY payment nor loopback grants RPC admission.
	require.NoError(t, walletManager.BindAccountToCurrentCoreNode())
	bindingKey, err := dkvsindexer.AccountMappingKey("testnet", owner.Address)
	require.NoError(t, err)
	binding, err := dkvsClientForNode(t, fixture.Network.Core).GetRecordDirect(bindingKey)
	require.NoError(t, err)
	_, _, bindingDescriptor, err := dkvsindexer.ValidateAccountMappingBindingRecord(binding)
	require.NoError(t, err)
	require.Equal(t, fixture.Network.Core.nodePubKey, bindingDescriptor.CoreNodeID)
	require.NoError(t, walletManager.StartDKVSSync())
	t.Cleanup(walletManager.StopDKVSSync)
	require.Eventually(t, func() bool {
		status := walletManager.GetAccountManagementStatus()
		return status.DiscoveryStatus == "inactive" && status.LocalOnly &&
			!status.RecoveryConfigured && !status.ActivationPending
	}, 30*time.Second, 100*time.Millisecond,
		"Owner must establish authenticated inactive discovery before paid storage setup")

	// A resumed paid-storage setup must only reuse an already-ready delegate.
	// This path queries the real local SatoshiNet contract/indexer but must never
	// broadcast another funding transaction.
	reusedAuthorization, err := walletManager.ReusePaidAccountStorage(100)
	require.NoError(t, err)
	require.Equal(t, wallet.AccountStoragePaid, reusedAuthorization.Mode)
	require.Empty(t, reusedAuthorization.TransactionID)
	require.Equal(t, uint64(100), reusedAuthorization.Summary.RecordCount)

	authorization := wallet.AccountStorageAuthorization{
		ID: wallet.AccountStoragePaid, Mode: wallet.AccountStoragePaid,
		RecordOptions: recordOptions, Autopay: &autopay, Location: coreLocation,
	}
	repository, err := walletManager.NewAccountRepositoryForStorage(authorization)
	require.NoError(t, err)
	manager := account.NewManager(repository)
	pkg, err := walletManager.CreateAccountRecoveryPackage(account.CreateOptions{AccountID: accountID, Backup: backup,
		RecoveryMode: account.RecoveryMode2Of3, Questions: questions, GuardianMailboxID: guardianID,
		GuardianPublicKey: guardianPublic})
	require.NoError(t, err)
	require.NoError(t, manager.Publish(context.Background(), *pkg))
	guardianCapsuleAuthorization, err := guardianManager.ReusePaidAccountStorage(100)
	require.NoError(t, err)
	require.Empty(t, guardianCapsuleAuthorization.TransactionID)
	require.NoError(t, guardianManager.PutGuardianCapsuleForStorage(
		*guardianCapsuleAuthorization, guardianID, *pkg.GuardianCapsule,
	))

	packageBytes, err := account.EncodeRecoveryPackageStorage(*pkg)
	require.NoError(t, err)
	packageKey, err := dkvsindexer.PersonalKey(pubKey,
		"account/recovery/"+pkg.Envelope.Locator.PackageID)
	require.NoError(t, err)
	setup.packageKey = packageKey
	guardianBytes, err := account.EncodeGuardianCapsuleStorage(*pkg.GuardianCapsule)
	require.NoError(t, err)
	guardianKey, err := dkvsindexer.MailShareKey(guardianID, pkg.GuardianCapsule.PackageID, pkg.GuardianCapsule.ShareID)
	require.NoError(t, err)
	setup.guardianKey = guardianKey
	// Canonical recovery package data relays normally. The guardian share is
	// AccountBound mailbox data and remains only on the selected CoreNode even
	// when another miner subscribes to /mail.
	requireDKVSValue(t, fixture.Network.Core, packageKey, packageBytes)
	requireDKVSValue(t, fixture.Network.Bootstrap, packageKey, packageBytes)
	requireDKVSValue(t, fixture.Network.Core, guardianKey, guardianBytes)
	requireDKVSAbsent(t, fixture.Network.Bootstrap, guardianKey)
	packageRecord := waitForDKVSRecord(t, fixture.Network.Core, packageKey)
	packagePeerRecord := waitForDKVSRecord(t, fixture.Network.Bootstrap, packageKey)
	t.Logf("paid-account-replica: key=%s core_seq=%d core_hash=%s bootstrap_seq=%d bootstrap_hash=%s",
		packageKey, packageRecord.Seq, dkvsindexer.RecordHash(packageRecord).String(),
		packagePeerRecord.Seq, dkvsindexer.RecordHash(packagePeerRecord).String())

	// Subscribe only after the records exist, then add the direct core peer.
	// Canonical personal data repairs to the selective miner; AccountBound mail
	// must not enter the ordinary miner replication path.
	require.NoError(t, subscribeDKVSNodeInternal(t, fixture.Network.Miner, dkvsindexer.Subscription{
		Type: dkvsindexer.SubscriptionPrefix, Target: prefix,
	}))
	require.NoError(t, subscribeDKVSNodeInternal(t, fixture.Network.Miner, dkvsindexer.Subscription{
		Type: dkvsindexer.SubscriptionMailbox, Target: "/mail/" + guardianID,
	}))
	require.NoError(t, connectNode(fixture.Network.Miner, fixture.Network.Core))
	requireDKVSValue(t, fixture.Network.Miner, packageKey, packageBytes)
	requireDKVSAbsent(t, fixture.Network.Miner, guardianKey)
	guardianMailboxRecord := waitForDKVSRecord(t, fixture.Network.Core, guardianKey)
	t.Logf("paid-account-mailbox: key=%s core=present:%d:%s bootstrap=absent miner=absent",
		guardianKey, guardianMailboxRecord.Seq, dkvsindexer.RecordHash(guardianMailboxRecord).String())

	coreClient := dkvsClientForNode(t, fixture.Network.Core)
	loaded, err := walletManager.LoadAccountRecoveryPackage(coreLocation, pkg.Envelope.Locator)
	t.Logf("account-e2e: load_recovery_package_error=%v", err)
	require.NoError(t, err)
	guardianValue, err := guardianManager.LoadAccountGuardianCapsule(
		guardianLocation, guardianID, pkg.GuardianCapsule.PackageID, pkg.GuardianCapsule.ShareID,
	)
	require.NoError(t, err)
	guardianKey, err = dkvsindexer.MailShareKey(guardianID, pkg.GuardianCapsule.PackageID, pkg.GuardianCapsule.ShareID)
	require.NoError(t, err)
	guardianRecord, err := coreClient.GetRecord(guardianKey)
	require.NoError(t, err)
	require.Zero(t, guardianRecord.TTL)
	require.Zero(t, dkvsindexer.RecordExpiryHeight(guardianRecord))
	guardianProof, err := dkvsindexer.ParseFeeProof(guardianRecord.FeeProof)
	require.NoError(t, err)
	require.Equal(t, dkvsindexer.FeeModeAutopay, guardianProof.Mode)
	t.Logf("paid-account-proof: owner=guardian-mailbox key=%s seq=%d hash=%s mode=%s pool=%s",
		guardianKey, guardianRecord.Seq, dkvsindexer.RecordHash(guardianRecord).String(),
		guardianProof.Mode, guardianProof.PoolContract)
	var storedGuardian account.GuardianShareCapsule
	require.NoError(t, json.Unmarshal(guardianValue, &storedGuardian))
	guardianShare, err := account.DecryptGuardianShare(storedGuardian, guardianPrivate)
	require.NoError(t, err)
	dkvsShare, err := account.RecoverDKVSShare(loaded.DKVSShareCapsule, loaded.KnowledgeBundle,
		[]account.AnswerAttempt{{QuestionID: "book", Answer: "月光落在安静的旧桥上。"},
			{QuestionID: "note", Answer: "yellow bicycle beside the winter river"}})
	require.NoError(t, err)
	restored, secret, err := account.RecoverAccount(loaded.Envelope, dkvsShare, guardianShare)
	require.NoError(t, err)
	require.NoError(t, walletManager.ActivateAccountManagement(
		secret, "123456", authorization, pkg.Envelope.Locator, "",
	))
	t.Run("PWAReadyPaidStorageDoesNotFundTwiceOrDowngrade", func(t *testing.T) {
		confirmed, err := walletManager.ConfirmAccountStorage(wallet.AccountStoragePaid, 100)
		require.NoError(t, err)
		require.Empty(t, confirmed.TransactionID)
		status, err := walletManager.GetAccountAutopayFundingStatus()
		require.NoError(t, err)
		require.True(t, status.Required)
		require.True(t, status.Ready)
		transport := &accountPreparedAckTransport{inner: wallet.NewHTTPClient()}
		walletManager.SetIndexerHttpClient_SatsNet(wallet.NewIndexerClient(
			coreLocation.Scheme, coreLocation.Host, coreLocation.Proxy, transport))
		defer walletManager.SetIndexerHttpClient_SatsNet(wallet.NewIndexerClient(
			coreLocation.Scheme, coreLocation.Host, coreLocation.Proxy, nil))
		for attempt := 0; attempt < 2; attempt++ {
			reused, err := walletManager.ConfirmAccountStorage(wallet.AccountStoragePaid, 100)
			require.NoError(t, err)
			require.Equal(t, wallet.AccountStoragePaid, reused.Mode)
			require.Empty(t, reused.TransactionID)
		}
		require.Empty(t, transport.bodies, "reconfirming ready paid storage broadcast another payment")
		options, err := walletManager.GetAccountStorageOptions()
		require.NoError(t, err)
		for _, option := range options {
			require.NotEqual(t, wallet.AccountStorageTemporary, option.Mode)
		}
		_, err = walletManager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
		require.ErrorIs(t, err, wallet.ErrAccountStorageModeDowngrade)
	})
	ownerReadyCtx, ownerReadyCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer ownerReadyCancel()
	require.NoError(t, walletManager.WaitAccountManagedDataReady(ownerReadyCtx),
		"Owner first paid backup must be confirmed locally before checking its peer copy")
	ownerStatus := walletManager.GetAccountManagementStatus()
	require.Equal(t, "active", ownerStatus.DiscoveryStatus)
	require.True(t, ownerStatus.RecoveryConfigured)
	require.False(t, ownerStatus.ActivationPending)
	require.True(t, ownerStatus.DKVSWriteAllowed)
	require.Equal(t, wallet.AccountStoragePaid, ownerStatus.StorageMode)
	require.False(t, ownerStatus.ManagedDataDirty)
	require.Zero(t, ownerStatus.PendingChanges)
	require.Zero(t, ownerStatus.DKVSPendingRequests)
	rootWrapperKey, err = dkvsindexer.PersonalKey(pubKey, "account/root")
	require.NoError(t, err)
	stateKey, err = dkvsindexer.PersonalKey(pubKey, "account/state")
	require.NoError(t, err)
	managedDataKey, err := account.ManagedIndexKey(accountID)
	require.NoError(t, err)
	rootWrapperRecord := waitForDKVSRecord(t, fixture.Network.Bootstrap, rootWrapperKey)
	stateRecord := waitForDKVSRecord(t, fixture.Network.Bootstrap, stateKey)
	managedDataRecord := waitForDKVSRecord(t, fixture.Network.Bootstrap, managedDataKey)
	for _, record := range []*wire.DKVSRecord{rootWrapperRecord, stateRecord, managedDataRecord} {
		require.Zero(t, record.TTL)
		proof, proofErr := dkvsindexer.ParseFeeProof(record.FeeProof)
		require.NoError(t, proofErr)
		require.Equal(t, dkvsindexer.FeeModeAutopay, proof.Mode)
		require.Equal(t, contractAddress.MustEncode(), proof.PoolContract)
		t.Logf("paid-account-proof: owner=root key=%s seq=%d hash=%s mode=%s pool=%s",
			record.Key, record.Seq, dkvsindexer.RecordHash(record).String(), proof.Mode, proof.PoolContract)
	}
	managedState, err := account.OpenManagedState(secret, accountID, stateRecord.Value)
	require.NoError(t, err)
	require.Equal(t, walletManager.GetAccountManagementStatus().RootFingerprint, managedState.RootFingerprint)
	managedIndex, err := account.OpenManagedIndex(secret, accountID, "testnet", managedDataRecord.Value)
	require.NoError(t, err)
	managedModules, moduleRecords := readAccountE2EModules(t, fixture.Network.Bootstrap, secret, accountID, managedIndex)
	for _, record := range moduleRecords {
		require.Zero(t, record.TTL)
		proof, proofErr := dkvsindexer.ParseFeeProof(record.FeeProof)
		require.NoError(t, proofErr)
		require.Equal(t, dkvsindexer.FeeModeAutopay, proof.Mode)
		require.Equal(t, contractAddress.MustEncode(), proof.PoolContract)
		t.Logf("paid-account-proof: owner=managed-module key=%s seq=%d hash=%s mode=%s pool=%s",
			record.Key, record.Seq, dkvsindexer.RecordHash(record).String(), proof.Mode, proof.PoolContract)
	}
	// The built-in provider exports one stable recovery package per catalog
	// scope, including scopes with no RGB ownership. Check the exact set.
	expectedScopes := make(map[string]uint32)
	for _, entry := range walletManager.GetWalletCatalog() {
		for _, subaccount := range entry.Accounts {
			scope := wallet.AccountManagedDataScope{Network: "testnet",
				WalletFingerprint: entry.Fingerprint, AccountIndex: subaccount.Index}
			expectedScopes[scope.ID()] = subaccount.Index
		}
	}
	require.NotEmpty(t, expectedScopes)
	require.Len(t, managedModules, 1+len(expectedScopes))
	moduleCount := 0
	seenScopes := make(map[string]bool)
	for _, item := range managedModules {
		switch item.Provider {
		case "e2e.module":
			moduleCount++
			require.Equal(t, wallet.AccountManagedDataGlobalScope, item.Scope)
			require.Equal(t, "e2e-module-required-data|"+accountID, string(item.Payload))
		case "rgb11":
			index, exists := expectedScopes[item.Scope]
			require.True(t, exists, "unexpected RGB recovery scope")
			require.False(t, seenScopes[item.Scope], "duplicate RGB recovery scope")
			seenScopes[item.Scope] = true
			value, err := rgb11wallet.DecodeRecoveryPackage(item.Payload)
			require.NoError(t, err)
			require.NoError(t, rgb11wallet.ValidateRecoveryPackage(value))
			require.NotEmpty(t, value.WalletID)
			require.Equal(t, index, value.AccountIndex)
			for _, record := range value.ProjectionRecords {
				require.False(t, strings.HasPrefix(record.Key, "proof-"),
					"empty RGB scope invented an allocation proof")
			}
		default:
			t.Fatalf("unexpected account-managed provider %q", item.Provider)
		}
	}
	require.Equal(t, 1, moduleCount)
	require.Len(t, seenScopes, len(expectedScopes))
	requireDKVSValue(t, fixture.Network.Core, rootWrapperKey, rootWrapperRecord.Value)
	requireDKVSValue(t, fixture.Network.Core, stateKey, stateRecord.Value)
	requireDKVSValue(t, fixture.Network.Core, managedDataKey, managedDataRecord.Value)
	for index := range secret {
		secret[index] = 0
	}
	require.Equal(t, backup.Wallets[0].Name, restored.Wallets[0].Name)
	require.Equal(t, uint32(2), restored.Wallets[0].AccountCount)

	packagePrefix, err := dkvsindexer.AccountPersonalKey(accountID, "account/recovery")
	require.NoError(t, err)
	records, total, err := coreClient.ListRecords(packagePrefix, 0, 10)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, records, 1)
	require.Equal(t, packageKey, records[0].Key)
	require.Equal(t, packageBytes, records[0].Value)
	for _, record := range records {
		require.Zero(t, record.TTL)
		require.Zero(t, dkvsindexer.RecordExpiryHeight(record))
		proof, proofErr := dkvsindexer.ParseFeeProof(record.FeeProof)
		require.NoError(t, proofErr)
		require.Equal(t, dkvsindexer.FeeModeAutopay, proof.Mode)
	}
	usage, err := coreClient.GetUsage(packagePrefix)
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.Equal(t, uint64(1), usage.ActiveRecords)
	require.Greater(t, usage.ActiveTotalSize, uint64(0))
	require.True(t, strings.HasPrefix(pkg.Manifest.Locator.AccountID, accountID))
}

func buildDKVSKeyPathTemplateInvoke(t *testing.T, actor *dkvsKeyPathActor,
	contract contractcommon.ContractAddress, nonce uint64, action string, param []byte,
	inputs []dkvsPrevOut, funding wire.TxOut) *wire.MsgTx {

	t.Helper()
	tx, err := contractcommon.BuildInvokeTx(contractcommon.InvokeTxBuildRequest{
		Contract: contract, GasLimit: contractcommon.InvokeBaseGas, CallNonce: nonce,
		Action: action, Param: param, Funding: funding, Inputs: dkvsPrevOutPoints(inputs),
	})
	require.NoError(t, err)
	signDKVSKeyPathInputs(t, tx, actor, inputs)
	return tx
}
