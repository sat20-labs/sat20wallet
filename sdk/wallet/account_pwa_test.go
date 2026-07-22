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
		FullRecordFeePerBlock:    "1",
	}
	amount, err := accountAmountPerBlock(defaults)
	require.NoError(t, err)
	require.Equal(t, "5", amount)
}

func TestAccountAmountPerBlockRespectsNetworkMinimum(t *testing.T) {
	defaults := dkvsindexer.NetworkDefaults{
		Enabled:                  true,
		AutopayMinAmountPerBlock: "8",
		FullRecordFeePerBlock:    "1",
	}
	amount, err := accountAmountPerBlock(defaults)
	require.NoError(t, err)
	require.Equal(t, "8", amount)
}

func TestAccountStorageTestnetDefaultsProduceContinuousQuote(t *testing.T) {
	defaults := dkvsindexer.NetworkDefaultsForParams(&chaincfg.TestNetParams)
	require.Equal(t, "1", defaults.AutopayMinAmountPerBlock)
	amount, err := accountAmountPerBlock(defaults)
	require.NoError(t, err)
	require.Equal(t, "5", amount)
	cost, err := multiplyDecimal(amount, accountPaidDefaultFundingBlocks)
	require.NoError(t, err)
	require.Equal(t, "5000", cost)
	annual, err := multiplyDecimal(amount, 2_628_000)
	require.NoError(t, err)
	require.Equal(t, "13140000", annual)
}
