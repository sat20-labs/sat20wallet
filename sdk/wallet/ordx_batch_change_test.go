package wallet

import (
	"fmt"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/stretchr/testify/require"
)

type ordxBatchChangeIndexer struct {
	IndexerRPCClient
	asset *TxOutput
	plain *TxOutput
}

func (c *ordxBatchChangeIndexer) GetUtxoListWithTicker(_ string, name *indexer.AssetName) []*indexer.AssetsInUtxo {
	if indexer.IsPlainAsset(name) {
		return []*indexer.AssetsInUtxo{c.plain.ToAssetsInUtxo()}
	}
	return []*indexer.AssetsInUtxo{c.asset.ToAssetsInUtxo()}
}

func TestOrdxBatchChangeAccountsForPlainSatsConsumedByDustPadding(t *testing.T) {
	for _, spec := range []struct{ asset, padding int64 }{{1100, 30}, {1100, 500}, {1300, 30}, {1300, 0}} {
		t.Run(fmt.Sprintf("asset-%d-padding-%d", spec.asset, spec.padding), func(t *testing.T) {
			manager := safetyTestManager(t, &Channel{ChannelInDB: *NewChannelInDB()})
			address := manager.wallet.GetAddress()
			script, err := GetPkScriptFromAddress(address)
			require.NoError(t, err)
			name := AssetName{AssetName: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ORDX, Type: indexer.ASSET_TYPE_FT, Ticker: "padding"}, N: 1}
			asset := indexer.NewTxOutput(spec.asset + spec.padding)
			asset.OutPointStr = fmt.Sprintf("%064x:0", 1)
			asset.OutValue.PkScript = script
			asset.Assets = indexer.TxAssets{{Name: name.AssetName, Amount: *indexer.NewDefaultDecimal(spec.asset), BindingSat: 1}}
			asset.Offsets[name.AssetName] = indexer.AssetOffsets{{Start: 0, End: spec.asset}}
			plain := indexer.NewTxOutput(10000)
			plain.OutPointStr = fmt.Sprintf("%064x:0", 2)
			plain.OutValue.PkScript = script
			client := &ordxBatchChangeIndexer{asset: asset, plain: plain}
			manager.l1IndexerClient = &IndexerRPCClientMgr{active: client}
			manager.utxoLockerL1 = NewUtxoLocker(newMemoryKVDB(), client, L1_NETWORK_BITCOIN)
			tx, fetcher, fee, err := manager.BuildBatchSendTx_ordx(address, address, &name, indexer.NewDefaultDecimal(400), 2, 1, nil)
			require.NoError(t, err)
			require.GreaterOrEqual(t, len(tx.TxOut), 3)
			require.EqualValues(t, 400, tx.TxOut[0].Value)
			require.EqualValues(t, 400, tx.TxOut[1].Value)
			change := spec.asset - 800
			if change < 330 {
				change = 330
			}
			require.Equal(t, change, tx.TxOut[2].Value)
			combined := indexer.NewTxOutput(0)
			var inputValue, outputValue int64
			for _, input := range tx.TxIn {
				prev := asset
				if input.PreviousOutPoint.String() == plain.OutPointStr {
					prev = plain
				}
				require.NoError(t, combined.Append(prev))
				inputValue += prev.Value()
			}
			for i, output := range tx.TxOut {
				part, rest, err := combined.Cut(output.Value)
				require.NoError(t, err)
				combined = rest
				outputValue += output.Value
				if i < 2 {
					require.Equal(t, "400", part.GetAsset(&name.AssetName).String())
				} else if i == 2 {
					require.Equal(t, fmt.Sprint(spec.asset-800), part.GetAsset(&name.AssetName).String())
				} else {
					require.Empty(t, part.Assets)
				}
			}
			require.Equal(t, fee, inputValue-outputValue)
			require.Greater(t, fee, int64(0))
			require.Empty(t, combined.Assets, "network fee must never consume ORDX")
			signed, err := SignTxWithWallet(manager.wallet, tx, fetcher)
			require.NoError(t, err)
			require.NoError(t, VerifySignedTx(signed, fetcher))
			require.Equal(t, fmt.Sprint(spec.asset), asset.GetAsset(&name.AssetName).String())
		})
	}
}
