package wallet

import (
	"testing"

	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/stretchr/testify/require"
)

func TestContractDeploymentEscrowBudget(t *testing.T) {
	manager := &Manager{}
	for _, module := range []string{"evm", "template"} {
		t.Run(module, func(t *testing.T) {
			limit := int64(contract.DeployBaseGas)
			if module == "evm" { limit = 5000000 }
			quote := manager.evmGasAssetAmount
			if module == "template" { quote = manager.templateGasAssetAmount }
			base, err := contract.GasFeeAtHeight(contract.DeployBaseGas, 0); require.NoError(t, err)
			needed, _, err := quote(limit, true, 0); require.NoError(t, err)
			require.Greater(t, needed, base)
			for _, insufficient := range []int64{base, needed-1} {
				_, _, err := quote(limit, true, insufficient)
				require.Error(t, err, "release-review: nonzero override bypassed execution/Result budget")
			}
			amount, _, err := quote(limit, true, needed); require.NoError(t, err); require.Equal(t, needed, amount)
			amount, _, err = quote(limit, true, needed+100); require.NoError(t, err); require.Equal(t, needed+100, amount)
		})
	}
}
