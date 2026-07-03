package e2e

import (
	"testing"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/stretchr/testify/require"
)

func TestEVMGasPlanUsesGasAssetConversion(t *testing.T) {
	deploy, err := EVMDeployGasPlan(contractcommon.DeployBaseGas, 0, 10000)
	require.NoError(t, err)
	require.EqualValues(t, 5000, deploy.BaseFeeGasAsset)
	require.EqualValues(t, 50, deploy.ContractFundingGasAsset)
	require.EqualValues(t, 5050, deploy.RequiredInputGasAsset)
	require.EqualValues(t, 4950, deploy.ChangeGasAsset)

	invoke, err := EVMInvokeGasPlan(100000, 0, false, 100000)
	require.NoError(t, err)
	require.EqualValues(t, 100, invoke.BaseFeeGasAsset)
	require.EqualValues(t, 0, invoke.ContractFundingGasAsset)
	require.EqualValues(t, 100, invoke.RequiredInputGasAsset)
	require.EqualValues(t, 99900, invoke.ChangeGasAsset)

	invokeWithResult, err := EVMInvokeGasPlan(100000, 0, true, 100000)
	require.NoError(t, err)
	require.EqualValues(t, 50, invokeWithResult.ResultBaseFeeGasAsset)
	require.EqualValues(t, 50, invokeWithResult.ContractFundingGasAsset)
	require.EqualValues(t, 150, invokeWithResult.RequiredInputGasAsset)
}
