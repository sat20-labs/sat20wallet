package e2e

import (
	"context"
	"encoding/json"
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

func TestRealSatoshiNetAccountManagementAutopaySync(t *testing.T) {
	defaults := dkvsindexer.NetworkDefaultsForParams(&chaincfg.TestNetParams)
	fixture := newDKVSNoPluginTemplateFixtureWithArgs(t,
		map[string]int64{defaults.AutopayFeeAssetName: 20000}, nil, nil, dkvsMinerArgs(t))
	waitForDKVSPeerReady(t, fixture.Network)

	gas := contractcommon.GetGasAssetName()
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	require.Equal(t, defaults.AutopayDeployer, owner.Address)
	require.Empty(t, defaults.AutopayRecipient)
	require.Equal(t, "1", defaults.AutopayMinAmountPerBlock)
	gasOuts := splitToDKVSKeyPathActors(t, fixture, fixture.gasAnchor, gas,
		[]int64{300000, 300000, 300000}, []int64{10000, 10000, 10000},
		[]*dkvsKeyPathActor{owner, owner, owner})
	feeOuts := splitToDKVSKeyPathActors(t, fixture, fixture.assetAnchors[defaults.AutopayFeeAssetName],
		defaults.AutopayFeeAssetName, []int64{5000}, []int64{10000}, []*dkvsKeyPathActor{owner})

	content, err := defaults.AutopayContent()
	require.NoError(t, err)
	deployAssets := txAsset(gas, 290000)
	deployAssets = append(deployAssets, txAsset(defaults.AutopayFeeAssetName, 5000)...)
	deploy, contractAddress := buildDKVSKeyPathTemplateDeploy(t, owner,
		contractcommon.TemplateAutopay, content, owner.Address, defaults.AutopayDeployNonce,
		[]dkvsPrevOut{gasOuts[0], feeOuts[0]}, wire.TxOut{Value: 10000, Assets: deployAssets})
	fixture.Network.sendManyAndMine(t, []*wire.MsgTx{deploy}, 0)

	// This fixture uses the same wallet as account owner and Guardian. The
	// compact recovery package uses one personal slot plus one mailbox slot.
	config := &contractcommon.TemplateAutopayConfigInvokeParam{AmountPerBlock: "5"}
	configParam, err := config.Encode()
	require.NoError(t, err)
	configTx := buildDKVSKeyPathTemplateInvoke(t, owner, contractAddress, 1,
		contractcommon.TemplateInvokeAPIConfig, configParam, []dkvsPrevOut{gasOuts[1]},
		wire.TxOut{Value: 9000, Assets: txAsset(gas, 290000)})
	fixture.Network.sendManyAndMine(t, []*wire.MsgTx{configTx}, 0)

	// The next block performs the first per-block storage payment. AUTOPAY
	// records are accepted only after this payment is visible in contract state.
	heartbeat := buildDKVSKeyPathAssetTransfer(t, owner, gasOuts[2], gas, 290000, 9000, owner)
	fixture.Network.sendManyAndMine(t, []*wire.MsgTx{heartbeat}, 0)
	state := fetchTemplateAutopayView(t, fixture.Network.Bootstrap, contractAddress.MustEncode())
	require.Equal(t, templateruntime.AutopayStatusActive, state.Status)
	require.Empty(t, state.Recipient)
	require.Equal(t, "1", state.MinAmountPerBlock)
	require.GreaterOrEqual(t, state.PaidBlocks, int64(1))
	delegate, ok := state.Delegates[owner.Address]
	require.True(t, ok)
	require.Equal(t, "5", delegate.AmountPerBlock)
	require.GreaterOrEqual(t, delegate.LastPayHeight, state.CurrentBlock)

	pubKey := owner.Wallet.GetPubKey().SerializeCompressed()
	accountID := dkvsindexer.AccountID(pubKey)
	prefix, err := dkvsindexer.AccountPersonalKey(accountID, "account/recovery")
	require.NoError(t, err)
	minerClient := dkvsClientForNode(t, fixture.Network.Miner)
	_, _, err = minerClient.SubscribePrefix(prefix)
	require.NoError(t, err)
	_, _, err = minerClient.SubscribeMailbox(accountID)
	require.NoError(t, err)
	require.NoError(t, connectNode(fixture.Network.Miner, fixture.Network.Core))

	guardianPrivate, guardianPublic, err := account.GenerateGuardianKey(nil)
	require.NoError(t, err)
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
	defer database.Close()
	walletManager := wallet.NewManager(&sdkcommon.Config{
		Env: "test", Chain: "testnet",
		IndexerL1: &sdkcommon.Indexer{
			Scheme: bootstrapLocation.Scheme, Host: bootstrapLocation.Host, Proxy: bootstrapLocation.Proxy,
		},
		IndexerL2: &sdkcommon.Indexer{
			Scheme: bootstrapLocation.Scheme, Host: bootstrapLocation.Host, Proxy: bootstrapLocation.Proxy,
		},
	}, database)
	require.NotNil(t, walletManager)
	defer walletManager.Close()
	_, err = walletManager.ImportWallet(dkvsClientMnemonic, "123456")
	require.NoError(t, err)
	require.Equal(t, pubKey, walletManager.GetWallet().GetPubKey().SerializeCompressed())
	authorization := wallet.AccountStorageAuthorization{
		ID: wallet.AccountStoragePaid, Mode: wallet.AccountStoragePaid,
		RecordOptions: recordOptions, Autopay: &autopay, Location: bootstrapLocation,
	}
	repository, err := walletManager.NewAccountRepositoryForStorage(authorization)
	require.NoError(t, err)
	manager := account.NewManager(repository)
	pkg, err := manager.CreateRecoveryPackage(account.CreateOptions{AccountID: accountID, Backup: backup,
		RecoveryMode: account.RecoveryMode2Of3, Questions: questions, GuardianMailboxID: accountID,
		GuardianPublicKey: guardianPublic})
	require.NoError(t, err)
	require.NoError(t, manager.Publish(context.Background(), *pkg))
	require.NoError(t, walletManager.PutGuardianCapsuleForStorage(
		authorization, accountID, *pkg.GuardianCapsule,
	))

	packageBytes, err := account.EncodeRecoveryPackageStorage(*pkg)
	require.NoError(t, err)
	packageKey, err := dkvsindexer.PersonalKey(pubKey,
		"account/recovery/"+pkg.Envelope.Locator.PackageID)
	require.NoError(t, err)
	guardianBytes, err := account.EncodeGuardianCapsuleStorage(*pkg.GuardianCapsule)
	require.NoError(t, err)
	guardianKey, err := dkvsindexer.MailShareKey(accountID, pkg.GuardianCapsule.PackageID, pkg.GuardianCapsule.ShareID)
	require.NoError(t, err)
	for key, value := range map[string][]byte{
		packageKey: packageBytes, guardianKey: guardianBytes,
	} {
		requireDKVSValue(t, fixture.Network.Bootstrap, key, value)
		requireDKVSValue(t, fixture.Network.Core, key, value)
		requireDKVSValue(t, fixture.Network.Miner, key, value)
	}

	coreClient := dkvsClientForNode(t, fixture.Network.Core)
	loaded, err := walletManager.LoadAccountRecoveryPackage(coreLocation, pkg.Envelope.Locator)
	require.NoError(t, err)
	guardianValue, err := walletManager.LoadAccountGuardianCapsule(
		coreLocation, accountID, pkg.GuardianCapsule.PackageID, pkg.GuardianCapsule.ShareID,
	)
	require.NoError(t, err)
	guardianKey, err = dkvsindexer.MailShareKey(accountID, pkg.GuardianCapsule.PackageID, pkg.GuardianCapsule.ShareID)
	require.NoError(t, err)
	guardianRecord, err := coreClient.GetRecord(guardianKey)
	require.NoError(t, err)
	require.Zero(t, guardianRecord.TTL)
	require.Zero(t, guardianRecord.ExpiryHeight)
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
		require.Zero(t, record.ExpiryHeight)
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
