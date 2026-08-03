package wallet

import (
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func TestAccountAmountPerBlockCoversRecoveryRecords(t *testing.T) {
	defaults := dkvsindexer.NetworkDefaults{
		Enabled:                  true,
		AutopayMinAmountPerBlock: "1",
		FullRecordFeePerBlock:    "0.1",
	}
	amount, err := accountAmountPerBlock(defaults, 100)
	require.NoError(t, err)
	require.Equal(t, "10", amount)
}

func TestAccountAmountPerBlockRespectsNetworkMinimum(t *testing.T) {
	defaults := dkvsindexer.NetworkDefaults{
		Enabled:                  true,
		AutopayMinAmountPerBlock: "12",
		FullRecordFeePerBlock:    "0.1",
	}
	amount, err := accountAmountPerBlock(defaults, 100)
	require.NoError(t, err)
	require.Equal(t, "12", amount)
}

func TestAccountStorageTestnetDefaultsProduceContinuousQuote(t *testing.T) {
	defaults := dkvsindexer.NetworkDefaultsForParams(&chaincfg.TestNetParams)
	require.Equal(t, "1", defaults.AutopayMinAmountPerBlock)
	require.Equal(t, "0.1", defaults.FullRecordFeePerBlock)
	amount, err := accountAmountPerBlock(defaults, 0)
	require.NoError(t, err)
	require.Equal(t, "10", amount)
	cost, err := multiplyDecimal(amount, accountPaidDefaultFundingBlocks)
	require.NoError(t, err)
	require.Equal(t, "10000", cost)
	annual, err := multiplyDecimal(amount, 2_628_000)
	require.NoError(t, err)
	require.Equal(t, "26280000", annual)
}

func TestAccountRecordCountRejectsInsufficientCapacity(t *testing.T) {
	_, err := normalizeAccountRecordCount(accountMinimumRecordCount - 1)
	require.Error(t, err)
	require.Less(t, accountRequiredRecords, accountMinimumRecordCount)
}

func TestAccountPaidStorageAuthorizationCanReuseActiveDelegate(t *testing.T) {
	defaults := dkvsindexer.NetworkDefaultsForParams(&chaincfg.TestNetParams)
	authorization := accountPaidStorageAuthorization(AccountIndexerLocation{
		Scheme: "https", Host: "indexer.test", Proxy: "satsnet/testnet",
	}, defaults, 100, "10", "10000", "", true)
	require.Empty(t, authorization.TransactionID)
	require.Equal(t, defaults.AutopayContract, authorization.Summary.ContractAddress)
	require.Contains(t, authorization.Summary.Description, "复用")
	require.Equal(t, "10", authorization.Summary.AmountPerBlock)
}
