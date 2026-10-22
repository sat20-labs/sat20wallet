package fakeindexer

import (
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/stretchr/testify/require"
)

func atomicalsFixture(t *testing.T, quantity, value int64) (*Client, *TxOutput, indexer.AssetName) {
	t.Helper()
	network := NewNetwork("bitcoin", 900_000)
	name := indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ATOM, Type: indexer.ASSET_TYPE_FT, Ticker: "quark"}
	network.State.Tickers[name.String()] = &indexer.TickerInfo{
		AssetName: name, N: 1, Divisibility: 0, InscriptionId: fmt.Sprintf("%064xi0", 1),
	}
	input := indexer.NewTxOutput(value)
	input.OutPointStr = fmt.Sprintf("%064x:0", 2)
	input.UtxoId = indexer.ToUtxoId(899_999, 0, 0)
	input.OutValue.PkScript = []byte{0x51}
	input.Assets = indexer.TxAssets{{Name: name, Amount: *indexer.NewDefaultDecimal(quantity), BindingSat: 1}}
	require.NoError(t, network.SeedOutput(input))
	return NewClient(network), input, name
}

func TestAtomicalsFixtureRetainsNativeQuantitiesWithoutOrdinalOffsets(t *testing.T) {
	client, input, name := atomicalsFixture(t, 1000, 1400)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(input.TxIn())
	tx.AddTxOut(wire.NewTxOut(0, []byte{0x6a, 1, 42}))
	tx.AddTxOut(wire.NewTxOut(900, []byte{0x51}))
	tx.AddTxOut(wire.NewTxOut(400, []byte{0x52}))
	_, err := client.BroadCastTx(tx)
	require.NoError(t, err)
	for position, quantity := range []int64{0, 900, 100} {
		output, err := client.GetTxOutput(fmt.Sprintf("%s:%d", tx.TxID(), position))
		require.NoError(t, err)
		require.Equal(t, quantity, output.GetAsset(&name).Int64())
		require.Empty(t, output.Offsets)
	}
}

func TestAtomicalsFixtureRejectsBurnBeforeMutation(t *testing.T) {
	client, input, name := atomicalsFixture(t, 1000, 1400)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(input.TxIn())
	tx.AddTxOut(wire.NewTxOut(900, []byte{0x51}))
	_, err := client.BroadCastTx(tx)
	require.ErrorContains(t, err, "burn")
	require.Empty(t, client.Network.TxBroadcasted)
	require.Empty(t, client.Network.UtxoUsed)
	unchanged, err := client.GetTxOutput(input.OutPointStr)
	require.NoError(t, err)
	require.EqualValues(t, 1000, unchanged.GetAsset(&name).Int64())
	// Rejection must leave the original input available for a valid retry.
	tx.TxOut[0].Value = 1300
	_, err = client.BroadCastTx(tx)
	require.NoError(t, err)
}
