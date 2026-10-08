package wallet

import (
	"bytes"
	"testing"

	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/stretchr/testify/require"
)

func TestAccountSecretRecreatedFromRootAcrossDevices(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	devices := make([]*Manager, 2)
	passwords := []string{"first-password", "new-device-password"}
	for index := range devices {
		devices[index] = newAccountManagementAutoTestManager(t)
		_, err := devices[index].ImportWallet(accountRootWrapperTestMnemonic, passwords[index])
		require.NoError(t, err)
		require.NoError(t, devices[index].InitializeAccountManagement(passwords[index]))
	}
	first, second := devices[0], devices[1]
	require.Equal(t, first.accountProfile.AccountID, second.accountProfile.AccountID)
	require.Len(t, first.accountSecret, 32)
	require.True(t, bytes.Equal(first.accountSecret, second.accountSecret), "same root on a fresh device must recreate the same account backup key")
	require.False(t, bytes.Equal(first.accountProfile.SecretCipher, second.accountProfile.SecretCipher), "local password wrapping retains independent salts and nonces")
	original := append([]byte(nil), first.accountSecret...)
	defer zeroWalletBytes(original)
	require.NoError(t, first.ChangePassword(passwords[0], "replacement-password"))
	require.True(t, bytes.Equal(original, first.accountSecret))
	first.mutex.Lock()
	restored, err := first.decryptAccountManagementSecretLocked("replacement-password")
	first.mutex.Unlock()
	require.NoError(t, err)
	defer zeroWalletBytes(restored)
	require.True(t, bytes.Equal(original, restored))
	first.mutex.Lock()
	_, err = first.decryptAccountManagementSecretLocked(passwords[0])
	first.mutex.Unlock()
	require.Error(t, err)
}

func TestTopicKeyRecreatedOnColdDevice(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	remote := newRGB11MemoryDKVSHTTP()
	client := newTopicSDKMessageClient(remote)
	var keys [][]byte
	for range 2 {
		manager, root, crypto := newTopicCryptoTestManager(t, accountRootWrapperTestMnemonic)
		manager.walletInfoMap = map[int64]*WalletInfo{root.GetId(): {
			WalletInDB: WalletInDB{Id: root.GetId(), Accounts: 1, Type: WALLET_TYPE_MNEMONIC}, Wallet: root,
		}}
		manager.cfg = &sdkcommon.Config{Env: "test", Chain: "testnet", IndexerL2: &sdkcommon.Indexer{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"}}
		manager.http = remote
		manager.serverNode = NewNode(client, "topic.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
		_, err := crypto.LoadTopicKey("developers", 1)
		require.Error(t, err, "a fresh device must start with no stored topic key")
		snapshot, err := manager.CreateMessageTopic("developers", "Developers", 16)
		require.NoError(t, err)
		require.EqualValues(t, 1, snapshot.State.KeySeq)
		key, err := crypto.LoadTopicKey("developers", 1)
		require.NoError(t, err)
		defer zeroWalletBytes(key)
		keys = append(keys, key)
	}
	require.True(t, bytes.Equal(keys[0], keys[1]), "same owner, topic and epoch must recreate the same group key without copying a database")
}

func TestManagedKeyDerivationPurposeAndTopicEpochIsolation(t *testing.T) {
	root := NewInternalWalletWithMnemonic(accountRootWrapperTestMnemonic, "", GetChainParam())
	require.NotNil(t, root)
	accountID, err := dkvsAccountID(root)
	require.NoError(t, err)
	outputs := map[string][]byte{}
	outputs["backup"], err = deriveAccountBackupSecret(root, "testnet", accountID)
	require.NoError(t, err)
	outputs["guardian"], err = deriveAccountGuardianPrivateKey(root, "testnet", accountID)
	require.NoError(t, err)
	outputs["root-wrapper"], err = deriveAccountRootWrapperKey(root, "testnet", accountID)
	require.NoError(t, err)
	outputs["topic-one-1"], err = deriveTopicGroupKey(root, "testnet", "developers", 1)
	require.NoError(t, err)
	outputs["topic-one-2"], err = deriveTopicGroupKey(root, "testnet", "developers", 2)
	require.NoError(t, err)
	outputs["topic-two-1"], err = deriveTopicGroupKey(root, "testnet", "designers", 1)
	require.NoError(t, err)
	outputs["backup-mainnet"], err = deriveAccountBackupSecret(root, "mainnet", accountID)
	require.NoError(t, err)
	outputs["topic-mainnet"], err = deriveTopicGroupKey(root, "mainnet", "developers", 1)
	require.NoError(t, err)
	defer func() {
		for _, value := range outputs {
			zeroWalletBytes(value)
		}
	}()
	for left, first := range outputs {
		require.Len(t, first, 32)
		for right, second := range outputs {
			if left < right {
				require.False(t, bytes.Equal(first, second), "purpose/network/topic/epoch collision: %s vs %s", left, right)
			}
		}
	}
	root.SetSubAccount(3)
	stable, err := deriveTopicGroupKey(root, "testnet", "Developers", 2)
	require.NoError(t, err)
	defer zeroWalletBytes(stable)
	require.True(t, bytes.Equal(outputs["topic-one-2"], stable), "normalized topic, same epoch and root survive selection changes")
	_, err = deriveTopicGroupKey(root, "testnet", "developers", 0)
	require.Error(t, err)
	_, err = deriveTopicGroupKey(root, "testnet", "", 1)
	require.Error(t, err)
	_, err = deriveAccountBackupSecret(nil, "testnet", accountID)
	require.Error(t, err)
}
