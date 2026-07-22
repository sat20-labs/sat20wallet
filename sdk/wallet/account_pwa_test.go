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
		AutopayMinAmountPerBlock: "2",
		FullRecordFeePerBlock:    "1",
	}
	amount, err := accountAmountPerBlock(defaults)
	require.NoError(t, err)
	require.Equal(t, "4", amount)
}

func TestAccountAmountPerBlockRespectsNetworkMinimum(t *testing.T) {
	defaults := dkvsindexer.NetworkDefaults{
		Enabled:                  true,
		AutopayMinAmountPerBlock: "100",
		FullRecordFeePerBlock:    "1",
	}
	amount, err := accountAmountPerBlock(defaults)
	require.NoError(t, err)
	require.Equal(t, "100", amount)
}

func TestAccountStorageTestnetDefaultsProduceQuote(t *testing.T) {
	defaults := dkvsindexer.NetworkDefaultsForParams(&chaincfg.TestNetParams)
	amount, err := accountAmountPerBlock(defaults)
	require.NoError(t, err)
	cost, err := multiplyDecimal(amount, accountPaidDefaultFundingBlocks)
	require.NoError(t, err)
	require.NotEmpty(t, cost)
}
