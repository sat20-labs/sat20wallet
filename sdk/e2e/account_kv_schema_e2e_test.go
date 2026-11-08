package e2e

import (
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func readAccountE2EIndex(t *testing.T, node *testHarness, secret []byte, accountID string) (account.ManagedIndex, *wire.DKVSRecord) {
	t.Helper()
	key, err := account.ManagedIndexKey(accountID)
	require.NoError(t, err)
	record, err := dkvsClientForNode(t, node).GetRecordDirect(key)
	require.NoError(t, err)
	require.NotNil(t, record)
	require.NoError(t, dkvs.VerifySignature(record))
	index, err := account.OpenManagedIndex(secret, accountID, "testnet", record.Value)
	require.NoError(t, err)
	return index, record
}

func readAccountE2EModules(t *testing.T, node *testHarness, secret []byte, accountID string, index account.ManagedIndex) ([]account.ManagedModule, []*wire.DKVSRecord) {
	t.Helper()
	var modules []account.ManagedModule
	var records []*wire.DKVSRecord
	client := dkvsClientForNode(t, node)
	for _, entry := range index.Entries {
		if entry.Deleted {
			continue
		}
		record, err := client.GetRecordDirect(entry.Key)
		require.NoError(t, err)
		require.NotNil(t, record)
		require.NoError(t, dkvs.VerifySignature(record))
		records = append(records, record)
		if entry.Provider == "wallets" && entry.Scope == "global" {
			_, err := account.OpenManagedState(secret, accountID, record.Value)
			require.NoError(t, err)
			continue
		}
		key, err := account.ManagedModuleKey(accountID, entry.Provider, entry.Scope)
		require.NoError(t, err)
		require.Equal(t, key, entry.Key)
		blob, err := wallet.DecodeDKVSBlobValue(record.Value)
		require.NoError(t, err)
		module, err := account.OpenManagedModule(secret, accountID, "testnet", entry.Key, blob.Data)
		require.NoError(t, err)
		require.Equal(t, entry.Provider, module.Provider)
		require.Equal(t, entry.Scope, module.Scope)
		require.Equal(t, entry.Codec, module.Codec)
		modules = append(modules, module)
	}
	return modules, records
}
