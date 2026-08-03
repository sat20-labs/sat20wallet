package e2e

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func TestRealSatoshiNetDKVSManagerFreeLocalIsolationAndRecovery(t *testing.T) {
	fixture := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t))
	waitForDKVSPeerReady(t, fixture.Network)

	primary, primaryLocation := newWalletManagerForNode(t, fixture.Network.Bootstrap, dkvsClientMnemonic)
	authorization, err := primary.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
	require.NoError(t, err)
	require.Equal(t, wallet.AccountStorageTemporary, authorization.Mode)
	require.Greater(t, authorization.RecordOptions.TTL, uint64(0))

	// Boundary checks must fail before any state-changing network operation.
	_, err = primary.ConfirmAccountStorage("unknown", 0)
	require.Error(t, err)
	_, err = primary.ConfirmAccountStorage(wallet.AccountStoragePaid, 99)
	require.ErrorContains(t, err, "at least 100 records")

	root := primary.GetWallet()
	require.NotNil(t, root)
	accountID := dkvsindexer.AccountID(root.GetPubKey().SerializeCompressed())
	repository, err := primary.NewAccountRepositoryForStorage(*authorization)
	require.NoError(t, err)

	questions := e2eKnowledgeQuestions()
	backup := account.Backup{Version: account.Version, Wallets: []account.WalletBackup{{
		Name: "Free Local Root", Mnemonic: dkvsClientMnemonic, AccountCount: 2,
		SubAccounts: []account.SubAccount{{Index: 0, Name: "Primary", DID: "did:free:0"},
			{Index: 1, Name: "Savings", DID: "did:free:1"}},
	}}}
	accountManager := account.NewManager(repository)
	pkg, secret, err := accountManager.CreateRecoveryPackageWithSecret(account.CreateOptions{
		AccountID: accountID, Backup: backup, RecoveryMode: account.RecoveryMode2Of2,
		Questions: questions,
	})
	require.NoError(t, err)
	defer clearBytes(secret)
	require.NoError(t, accountManager.Publish(context.Background(), *pkg))

	values := recoveryPackageValues(t, root.GetPubKey().SerializeCompressed(), pkg)
	for key, expected := range values {
		requireDKVSValue(t, fixture.Network.Bootstrap, key, expected)
		requireDKVSAbsent(t, fixture.Network.Core, key)
		requireDKVSAbsent(t, fixture.Network.Miner, key)
	}

	// A fresh SDK manager connected to the same endpoint must rebuild its local
	// replica and recover the complete atomic package.
	sameEndpoint, _ := newWalletManagerForNode(t, fixture.Network.Bootstrap, dkvsClientMnemonic)
	loaded, err := sameEndpoint.LoadAccountRecoveryPackage(primaryLocation, pkg.Envelope.Locator)
	require.NoError(t, err)
	dkvsShare, err := account.RecoverDKVSShare(loaded.DKVSShareCapsule, loaded.KnowledgeBundle,
		e2eKnowledgeAnswers())
	require.NoError(t, err)
	restored, recoveredSecret, err := account.RecoverAccount(loaded.Envelope, dkvsShare, pkg.UserShare)
	require.NoError(t, err)
	defer clearBytes(recoveredSecret)
	require.Equal(t, backup.Wallets[0].Name, restored.Wallets[0].Name)
	require.Equal(t, uint32(2), restored.Wallets[0].AccountCount)

	_, err = account.RecoverDKVSShare(loaded.DKVSShareCapsule, loaded.KnowledgeBundle,
		[]account.AnswerAttempt{{QuestionID: "book", Answer: "wrong"},
			{QuestionID: "note", Answer: "also wrong"}})
	require.Error(t, err)

	// The same account and locator on a different endpoint must not expose the
	// endpoint-local FREE_LOCAL package.
	otherEndpoint, coreLocation := newWalletManagerForNode(t, fixture.Network.Core, dkvsClientMnemonic)
	_, err = otherEndpoint.LoadAccountRecoveryPackage(coreLocation, pkg.Envelope.Locator)
	require.Error(t, err)
	require.True(t, errors.Is(err, wallet.ErrDKVSRecordNotFound) ||
		errors.Is(err, wallet.ErrDKVSPathNotSynced), "unexpected cross-endpoint error: %v", err)
}

func newWalletManagerForNode(t *testing.T, node *testHarness, mnemonic string) (*wallet.Manager, wallet.AccountIndexerLocation) {
	t.Helper()
	base, err := node.IndexerURL("testnet")
	require.NoError(t, err)
	parsed, err := url.Parse(base)
	require.NoError(t, err)
	location := wallet.AccountIndexerLocation{
		Scheme: parsed.Scheme, Host: parsed.Host, Proxy: strings.Trim(parsed.Path, "/"),
	}
	database := indexerdb.NewKVDB(t.TempDir())
	require.NotNil(t, database)
	manager := wallet.NewManager(&sdkcommon.Config{
		Env: "test", Chain: "testnet",
		IndexerL1: &sdkcommon.Indexer{Scheme: location.Scheme, Host: location.Host, Proxy: location.Proxy},
		IndexerL2: &sdkcommon.Indexer{Scheme: location.Scheme, Host: location.Host, Proxy: location.Proxy},
	}, database)
	require.NotNil(t, manager)
	t.Cleanup(manager.Close)
	if mnemonic != "" {
		_, err = manager.ImportWallet(mnemonic, "123456")
		require.NoError(t, err)
	}
	return manager, location
}

func e2eKnowledgeQuestions() []account.QuestionAnswer {
	return []account.QuestionAnswer{
		{Question: account.KnowledgeQuestion{ID: "book", Prompt: "book phrase", IgnorePunctuation: true},
			Answer: "moonlight over the old bridge", Confirmation: "moonlight over the old bridge"},
		{Question: account.KnowledgeQuestion{ID: "note", Prompt: "private note", IgnorePunctuation: true},
			Answer: "yellow bicycle beside the river", Confirmation: "yellow bicycle beside the river"},
		{Question: account.KnowledgeQuestion{ID: "family", Prompt: "family phrase", IgnorePunctuation: true},
			Answer: "meet under the tree at six", Confirmation: "meet under the tree at six"},
	}
}

func e2eKnowledgeAnswers() []account.AnswerAttempt {
	return []account.AnswerAttempt{
		{QuestionID: "book", Answer: "moonlight over the old bridge"},
		{QuestionID: "note", Answer: "yellow bicycle beside the river"},
	}
}

func recoveryPackageValues(t *testing.T, pubKey []byte, pkg *account.RecoveryPackage) map[string][]byte {
	t.Helper()
	encoded, err := account.EncodeRecoveryPackageStorage(*pkg)
	require.NoError(t, err)
	key, err := dkvsindexer.PersonalKey(pubKey,
		"account/recovery/"+pkg.Envelope.Locator.PackageID)
	require.NoError(t, err)
	return map[string][]byte{key: encoded}
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
