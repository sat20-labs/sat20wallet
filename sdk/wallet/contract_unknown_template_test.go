package wallet

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnknownTemplateRejectedBeforeIndexerOrFunding(t *testing.T) {
	for _, mode := range []string{"estimate", "deploy"} {
		t.Run(mode, func(t *testing.T) {
			manager := safetyTestManager(t, &Channel{ChannelInDB: *NewChannelInDB()})
			request := &ContractDeployRequest{ContractType: ContractTypeTemplate,
				SubType: "not-a-template.tc", DeployNonce: 987654322,
				ContractContent: "e30=", ContentEncoding: "base64"}
			var result *ContractTxResult
			var err error
			require.NotPanics(t, func() {
				if mode == "estimate" {
					result, err = manager.EstimateDeployUnifiedContract(request)
				} else {
					result, err = manager.DeployUnifiedContract(request)
				}
			}, "invalid template must fail before accessing absent indexer or funding")
			require.ErrorContains(t, err, "not found")
			require.Nil(t, result)
		})
	}
}
