package wallet

import (
	"fmt"
	"sort"
	"testing"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/stretchr/testify/require"
)

func addExpansionTestOutput(t *testing.T, rpc *channelHistoryRPC, seed byte, pkScript []byte,
	value int64, asset *indexer.AssetName, amount int64, invalid, lucky bool) string {
	t.Helper()
	tx := wire.NewMsgTx(2)
	input := &wire.OutPoint{Hash: chainhash.Hash{seed}}
	if lucky {
		input = &wire.OutPoint{Index: ^uint32(0)}
	}
	tx.AddTxIn(wire.NewTxIn(input, []byte("height1-satoshinet"), nil))
	tx.AddTxOut(wire.NewTxOut(value, pkScript))
	raw, err := EncodeMsgTx(tx)
	require.NoError(t, err)
	output := indexer.NewTxOutput(value)
	output.OutPointStr = tx.TxID() + ":0"
	output.OutValue = *tx.TxOut[0]
	if asset != nil {
		info := indexer.AssetInfo{Name: *asset, Amount: *indexer.NewDefaultDecimal(amount)}
		output.Assets = indexer.TxAssets{info}
		if invalid {
			output.Invalids[*asset] = true
		}
	}
	if rpc.outputs == nil {
		rpc.outputs = make(map[string]*TxOutput)
	}
	if rpc.raw == nil {
		rpc.raw = make(map[string]string)
	}
	rpc.outputs[output.OutPointStr] = output
	rpc.raw[tx.TxID()] = raw
	return output.OutPointStr
}

func TestUnmanagedChannelExpansionInputsExcludesControlledLockedAndLucky(t *testing.T) {
	p, l1, l2, address, _ := channelHistoryFixture(t)
	l1.outputs, l1.raw = make(map[string]*TxOutput), make(map[string]string)
	channel := &Channel{ChannelInDB: *NewChannelInDB()}
	channel.Address, channel.ChannelId = address, address
	pkScript, err := GetPkScriptFromAddress(address)
	require.NoError(t, err)

	localA := addExpansionTestOutput(t, l1, 1, pkScript, 1_000, nil, 0, false, false)
	localB := addExpansionTestOutput(t, l1, 2, pkScript, 2_000, nil, 0, false, false)
	controlled := addExpansionTestOutput(t, l1, 3, pkScript, 3_000, nil, 0, false, false)
	pending := addExpansionTestOutput(t, l1, 4, pkScript, 4_000, nil, 0, false, false)
	stub := addExpansionTestOutput(t, l1, 5, pkScript, 5_000, nil, 0, false, false)
	locked := addExpansionTestOutput(t, l1, 6, pkScript, 6_000, nil, 0, false, false)
	lucky := addExpansionTestOutput(t, l1, 7, pkScript, 7_000, nil, 0, false, true)

	channel.FundingUtxos[PLAIN_ASSET] = []*TxOutput{l1.outputs[controlled]}
	channel.PendingUtxos[pending] = l1.outputs[pending]
	channel.StubUtxos[PLAIN_ASSET] = []*TxOutput{l1.outputs[stub]}
	p.utxoLockerL1 = NewUtxoLocker(nil, l1, L1_NETWORK_BITCOIN)
	p.utxoLockerL1.lockmap[locked] = &LockedUtxo{Reason: "test reservation"}
	// Even an independently ascended Lucky reward remains outside local
	// expansion candidates.
	l2.ledger = []*sindexer.ChannelLedgerEntry{{ChannelId: address, Direction: "ascending", L2TxId: "lucky-anchor", L1Outpoints: []string{lucky}, L2Height: 1, Value: 7_000}}

	got, err := p.unmanagedChannelExpansionInputs(channel, &PLAIN_ASSET.AssetName)
	require.NoError(t, err)
	want := []string{localA, localB}
	sort.Strings(want)
	require.Equal(t, want, got)
}

func TestUnmanagedChannelExpansionInputsPreservesAccountBasedCarrierSemantics(t *testing.T) {
	p, l1, _, address, _ := channelHistoryFixture(t)
	l1.outputs, l1.raw = make(map[string]*TxOutput), make(map[string]string)
	channel := &Channel{ChannelInDB: *NewChannelInDB()}
	channel.Address, channel.ChannelId = address, address
	pkScript, err := GetPkScriptFromAddress(address)
	require.NoError(t, err)

	brc := indexer.AssetName{Protocol: "brc20", Type: "f", Ticker: "ordi"}
	goodBRC := addExpansionTestOutput(t, l1, 11, pkScript, 1_000, &brc, 10, false, false)
	historicalBRC := addExpansionTestOutput(t, l1, 12, pkScript, 1_000, &brc, 5, true, false)
	noBRC := addExpansionTestOutput(t, l1, 13, pkScript, 1_000, nil, 0, false, false)
	ordx := indexer.AssetName{Protocol: "ordx", Type: "f", Ticker: "pizza"}
	goodORDX := addExpansionTestOutput(t, l1, 14, pkScript, 1_000, &ordx, 20, false, false)
	invalidORDX := addExpansionTestOutput(t, l1, 15, pkScript, 1_000, &ordx, 5, true, false)

	got, err := p.unmanagedChannelExpansionInputs(channel, &brc)
	require.NoError(t, err)
	want := []string{goodBRC, historicalBRC}
	sort.Strings(want)
	require.Equal(t, want, got, "BRC20 historical carrier behavior must match existing expansion selection")

	got, err = p.unmanagedChannelExpansionInputs(channel, &ordx)
	require.NoError(t, err)
	require.Equal(t, []string{goodORDX}, got)
	require.NotContains(t, got, noBRC)
	require.NotContains(t, got, invalidORDX)
}

func TestExcludeBTCLuckyL1OutputsPreservesOtherExclusions(t *testing.T) {
	p, l1, _, address, _ := channelHistoryFixture(t)
	l1.outputs, l1.raw = make(map[string]*TxOutput), make(map[string]string)
	pkScript, err := GetPkScriptFromAddress(address)
	require.NoError(t, err)
	ordinary := addExpansionTestOutput(t, l1, 21, pkScript, 1_000, nil, 0, false, false)
	lucky := addExpansionTestOutput(t, l1, 22, pkScript, 1_000, nil, 0, false, true)
	preExcluded := fmt.Sprintf("%064x:0", 23)
	l1.outputs[preExcluded] = indexer.NewTxOutput(1_000)
	l1.outputs[preExcluded].OutPointStr = preExcluded
	excluded := map[string]bool{preExcluded: true}

	require.NoError(t, p.excludeBTCLuckyL1Outputs(address, excluded))
	require.Equal(t, map[string]bool{preExcluded: true, lucky: true}, excluded)
	require.False(t, excluded[ordinary])
}

func TestExcludeBTCLuckyL1OutputsFailsWhenSourceCannotBeRead(t *testing.T) {
	p, l1, _, address, _ := channelHistoryFixture(t)
	l1.outputs, l1.raw = make(map[string]*TxOutput), make(map[string]string)
	pkScript, err := GetPkScriptFromAddress(address)
	require.NoError(t, err)
	point := addExpansionTestOutput(t, l1, 31, pkScript, 1_000, nil, 0, false, false)
	delete(l1.raw, point[:64])
	require.ErrorContains(t, p.excludeBTCLuckyL1Outputs(address, map[string]bool{}), "unknown raw transaction")
}
