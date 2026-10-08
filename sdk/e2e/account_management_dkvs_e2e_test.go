package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	templateruntime "github.com/sat20-labs/satoshinet/contract/template"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

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

func TestRealSatoshiNetAccountManagementAutopaySync(t *testing.T) {
	defaults := dkvsindexer.NetworkDefaultsForParams(&chaincfg.TestNetParams)
	fixture := newDKVSNoPluginTemplateFixtureWithArgs(t,
		map[string]int64{defaults.AutopayFeeAssetName: 20000}, nil, nil, dkvsMinerArgs(t))
	waitForDKVSPeerReady(t, fixture.Network)

	gas := contractcommon.GetGasAssetName()
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	guardianMnemonic := accountSyncMnemonic(t, 241)
	guardianActor := newDKVSKeyPathActor(t, keyFromMnemonic(t, guardianMnemonic, 0))
	require.Equal(t, defaults.AutopayDeployer, owner.Address)
	require.Empty(t, defaults.AutopayRecipient)
	require.Equal(t, "1", defaults.AutopayMinAmountPerBlock)
	gasOuts := splitToDKVSKeyPathActors(t, fixture, fixture.gasAnchor, gas,
		[]int64{300000, 300000, 300000, 300000}, []int64{10000, 10000, 10000, 10000},
		[]*dkvsKeyPathActor{owner, owner, guardianActor, owner})
	feeOuts := splitToDKVSKeyPathActors(t, fixture, fixture.assetAnchors[defaults.AutopayFeeAssetName],
		defaults.AutopayFeeAssetName, []int64{5000, 10000}, []int64{10000, 10000}, []*dkvsKeyPathActor{owner, guardianActor})

	content, err := defaults.AutopayContent()
	require.NoError(t, err)
	deployAssets := txAsset(gas, 290000)
	require.NoError(t, deployAssets.Merge(txAsset(defaults.AutopayFeeAssetName, 5000)))
	deploy, contractAddress := buildDKVSKeyPathTemplateDeploy(t, owner,
		contractcommon.TemplateAutopay, content, owner.Address, defaults.AutopayDeployNonce,
		[]dkvsPrevOut{gasOuts[0], feeOuts[0]}, wire.TxOut{Value: 10000, Assets: deployAssets})
	fixture.Network.sendManyAndMine(t, []*wire.MsgTx{deploy}, 0)

	// The owner and Guardian each pay for their own signed records. The compact
	// recovery package uses one personal slot plus one independent mailbox slot.
	// The owner is also the deployer: mark the operator share explicitly,
	// separately from this delegate's per-block business payment.
	config := &contractcommon.TemplateAutopayConfigInvokeParam{AmountPerBlock: "10", GasFundingAmount: "280000"}
	configParam, err := config.Encode()
	require.NoError(t, err)
	configTx := buildDKVSKeyPathTemplateInvoke(t, owner, contractAddress, 1,
		contractcommon.TemplateInvokeAPIConfig, configParam, []dkvsPrevOut{gasOuts[1]},
		wire.TxOut{Value: 9000, Assets: txAsset(gas, 290000)})
	fixture.Network.sendManyAndMine(t, []*wire.MsgTx{configTx}, 0)
	guardianFunding := buildDKVSKeyPathTemplateDefaultInvoke(t, guardianActor, contractAddress,
		[]dkvsPrevOut{feeOuts[1]}, wire.TxOut{Value: 10000, Assets: txAsset(defaults.AutopayFeeAssetName, 10000)})
	fixture.Network.sendManyAndMine(t, []*wire.MsgTx{guardianFunding}, 0)
	guardianConfig, err := (&contractcommon.TemplateAutopayConfigInvokeParam{AmountPerBlock: "10"}).Encode()
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
	require.GreaterOrEqual(t, delegate.LastPayHeight, state.CurrentBlock)

	pubKey := owner.Wallet.GetPubKey().SerializeCompressed()
	accountID := dkvsindexer.AccountID(pubKey)
	prefix, err := dkvsindexer.AccountPersonalKey(accountID, "account/recovery")
	require.NoError(t, err)

	guardianManager, guardianLocation := accountReviewDevice(t, fixture.Network, guardianMnemonic)
	require.NoError(t, guardianManager.InitializeAccountManagement(accountReviewPassword))
	guardianIdentity, err := guardianManager.GetOrCreateAccountGuardianIdentity(accountReviewPassword)
	require.NoError(t, err)
	guardianPublic, err := base64.RawURLEncoding.DecodeString(guardianIdentity.PublicKey)
	require.NoError(t, err)
	guardianPrivate, err := guardianManager.LoadAccountGuardianPrivateKey(accountReviewPassword)
	require.NoError(t, err)
	defer clearBytes(guardianPrivate)
	guardianID := guardianIdentity.MailboxID
	require.NotEqual(t, accountID, guardianID)
	questions := []account.QuestionAnswer{
		{Question: account.KnowledgeQuestion{ID: "book", Prompt: "指定版本书籍第十页最后十个字", IgnorePunctuation: true}, Answer: "月光落在安静的旧桥上", Confirmation: "月光落在安静的旧桥上"},
		{Question: account.KnowledgeQuestion{ID: "note", Prompt: "私人纸条中的指定句子", IgnorePunctuation: true}, Answer: "yellow bicycle beside the winter river", Confirmation: "yellow bicycle beside the winter river"},
		{Question: account.KnowledgeQuestion{ID: "family", Prompt: "未公开的家庭约定", IgnorePunctuation: true}, Answer: "周日傍晚六点在老树下见", Confirmation: "周日傍晚六点在老树下见"},
	}
	backup := account.Backup{Version: account.Version, Wallets: []account.WalletBackup{{
		Name: "Primary", Mnemonic: "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		AccountCount: 2, SubAccounts: []account.SubAccount{{Index: 0, DID: "alice"}, {Index: 1, DID: "alice-work"}},
	}}}
	autopay := wallet.DKVSAutopayOptions{AddressParams: &chaincfg.TestNetParams, PoolContract: contractAddress.MustEncode()}
	recordOptions := dkvsindexer.RecordOptions{Seq: 1}
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
	defer database.Close()
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
	defer walletManager.Close()
	require.NoError(t, walletManager.RegisterAccountManagedDataProvider(&e2eAccountManagedProvider{}))
	_, err = walletManager.ImportWallet(dkvsClientMnemonic, "123456")
	require.NoError(t, err)
	require.NoError(t, walletManager.InitializeAccountManagement("123456"))
	require.Equal(t, pubKey, walletManager.GetWallet().GetPubKey().SerializeCompressed())
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
	guardianAuthorization, err := guardianManager.ReusePaidAccountStorage(100)
	require.NoError(t, err)
	require.NoError(t, guardianManager.PutGuardianCapsuleForStorage(
		*guardianAuthorization, guardianID, *pkg.GuardianCapsule,
	))

	packageBytes, err := account.EncodeRecoveryPackageStorage(*pkg)
	require.NoError(t, err)
	packageKey, err := dkvsindexer.PersonalKey(pubKey,
		"account/recovery/"+pkg.Envelope.Locator.PackageID)
	require.NoError(t, err)
	guardianBytes, err := account.EncodeGuardianCapsuleStorage(*pkg.GuardianCapsule)
	require.NoError(t, err)
	guardianKey, err := dkvsindexer.MailShareKey(guardianID, pkg.GuardianCapsule.PackageID, pkg.GuardianCapsule.ShareID)
	require.NoError(t, err)
	// Canonical recovery package data relays normally. The guardian share is
	// AccountBound mailbox data and remains only on the selected CoreNode even
	// when another miner subscribes to /mail.
	requireDKVSValue(t, fixture.Network.Core, packageKey, packageBytes)
	requireDKVSValue(t, fixture.Network.Bootstrap, packageKey, packageBytes)
	requireDKVSValue(t, fixture.Network.Core, guardianKey, guardianBytes)
	requireDKVSAbsent(t, fixture.Network.Bootstrap, guardianKey)

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
		secret, "123456", authorization, pkg.Envelope.Locator, "sat20account1:e2e",
	))
	t.Run("PWAReadyPaidStorageDoesNotFundTwiceOrDowngrade", func(t *testing.T) {
		confirmed, err := walletManager.ConfirmAccountStorage(wallet.AccountStoragePaid, 100)
		require.NoError(t, err)
		require.Empty(t, confirmed.TransactionID)
		status, err := walletManager.GetAccountAutopayFundingStatus()
		require.NoError(t, err)
		require.True(t, status.Required)
		require.True(t, status.Ready)
		funding, err := walletManager.FundAccountAutopay(*status)
		require.NoError(t, err)
		require.True(t, funding.Reused)
		require.Empty(t, funding.TransactionID)
		options, err := walletManager.GetAccountStorageOptions()
		require.NoError(t, err)
		for _, option := range options {
			require.NotEqual(t, wallet.AccountStorageTemporary, option.Mode)
		}
		_, err = walletManager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
		require.ErrorIs(t, err, wallet.ErrAccountStorageModeDowngrade)
	})
	stateKey, err := dkvsindexer.PersonalKey(pubKey, "account/state")
	require.NoError(t, err)
	managedDataKey, err := dkvsindexer.BlobKey(accountID, "account-managed-data")
	require.NoError(t, err)
	stateRecord := waitForDKVSRecord(t, fixture.Network.Bootstrap, stateKey)
	managedDataRecord := waitForDKVSRecord(t, fixture.Network.Bootstrap, managedDataKey)
	for _, record := range []*wire.DKVSRecord{stateRecord, managedDataRecord} {
		require.Zero(t, record.TTL)
		proof, proofErr := dkvsindexer.ParseFeeProof(record.FeeProof)
		require.NoError(t, proofErr)
		require.Equal(t, dkvsindexer.FeeModeAutopay, proof.Mode)
		require.Equal(t, contractAddress.MustEncode(), proof.PoolContract)
	}
	managedState, err := account.OpenManagedState(secret, accountID, stateRecord.Value)
	require.NoError(t, err)
	managedBlob, err := wallet.DecodeDKVSBlobValue(managedDataRecord.Value)
	require.NoError(t, err)
	managedBundle, err := account.OpenManagedDataBundle(secret, accountID, managedBlob.Data)
	require.NoError(t, err)
	require.Equal(t, managedState.DataRevision, managedBundle.Revision)
	managedHash, err := account.ManagedDataBundleHash(managedBundle)
	require.NoError(t, err)
	require.Equal(t, managedState.DataHash, managedHash)
	// RGB11 receive requests, pending transaction tasks and other engine state
	// are intentionally wallet-local. This fixture has no durable RGB11
	// allocation proof, so the built-in RGB11 provider contributes no item.
	require.Len(t, managedBundle.Items, 1)
	moduleItem := managedBundle.Items[0]
	require.Equal(t, "e2e.module", moduleItem.Provider)
	require.Equal(t, wallet.AccountManagedDataGlobalScope, moduleItem.Scope)
	require.Equal(t, "e2e-module-required-data|"+accountID, string(moduleItem.Payload))
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
