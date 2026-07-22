package e2e

import (
	"testing"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func buildDKVSKeyPathTemplateInvoke(t *testing.T, actor *dkvsKeyPathActor,
	contract contractcommon.ContractAddress, callNonce uint64, action string, param []byte,
	inputs []dkvsPrevOut, funding wire.TxOut) *wire.MsgTx {

	t.Helper()
	tx, err := contractcommon.BuildInvokeTx(contractcommon.InvokeTxBuildRequest{
		Contract:  contract,
		GasLimit:  contractcommon.InvokeBaseGas,
		CallNonce: callNonce,
		Action:    action,
		Param:     param,
		Funding:   funding,
		Inputs:    dkvsPrevOutPoints(inputs),
	})
	require.NoError(t, err)
	signDKVSKeyPathInputs(t, tx, actor, inputs)
	return tx
}
