package wallet

import (
	"testing"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/stretchr/testify/require"
)

func TestGetCommitTxAssetInfoResolvesSplicingStubsAfterSecondOutput(t *testing.T) {
	for _, protocol := range []string{indexer.PROTOCOL_NAME_ORDX, indexer.PROTOCOL_NAME_BRC20} {
		t.Run(protocol, func(t *testing.T) {
			channel := &Channel{ChannelInDB: *NewChannelInDB()}
			channel.ChannelId = "channel-stub-" + protocol
			channel.RedeemScript = []byte{0x51}
			channel.ChanPoint = indexer.NewTxOutput(1_000)
			channel.ChanPoint.OutPointStr = "known-channel-point:0"
			previous := safetyTestTx(31)
			for len(previous.TxOut) < 4 {
				previous.AddTxOut(wire.NewTxOut(330, channel.GetChannelPkScript()))
			}
			stub := indexer.GenerateTxOutput(previous, 3)
			name := AssetName{AssetName: indexer.AssetName{Protocol: protocol, Type: indexer.ASSET_TYPE_FT, Ticker: "stub"}, N: 1}
			channel.StubUtxos[name] = []*TxOutput{stub}
			channel.LocalCommitment = NewChannelCommitment()
			channel.LocalCommitment.PrevTxs = []*wire.MsgTx{previous}
			channel.LocalCommitment.CommitTx = wire.NewMsgTx(2)
			channel.LocalCommitment.CommitTx.AddTxIn(wire.NewTxIn(stub.OutPoint(), nil, nil))
			channel.LocalCommitment.CommitTx.AddTxOut(wire.NewTxOut(330, []byte{0x51}))
			if protocol == indexer.PROTOCOL_NAME_BRC20 {
				funding := indexer.NewTxOutput(330)
				funding.OutPointStr = "known-brc20-funding:0"
				channel.FundingUtxos[name] = []*TxOutput{funding}
				channel.LocalCommitment.LocalBalance[name] = indexer.NewDecimal(500, 0)
				channel.LocalCommitment.RemoteBalance[name] = indexer.NewDecimal(0, 0)
			}
			info, err := safetyTestManager(t, channel).GetCommitTxAssetInfo(channel.ChannelId)
			require.NoError(t, err)
			require.Len(t, info.InputAssets, 1)
			require.Equal(t, stub.OutPointStr, info.InputAssets[0].OutPoint)
			require.EqualValues(t, 330, info.InputAssets[0].Value)
			require.Equal(t, stub.OutValue.PkScript, info.InputAssets[0].PkScript)
			require.Len(t, info.OutputAssets, 1)
			if protocol == indexer.PROTOCOL_NAME_BRC20 {
				require.Len(t, info.InputAssets[0].Assets, 1)
				require.Equal(t, "500", info.InputAssets[0].Assets[0].Amount)
				require.Equal(t, "500", info.OutputAssets[0].Assets[0].Amount)
			} else {
				require.Empty(t, info.InputAssets[0].Assets)
				require.Empty(t, info.OutputAssets[0].Assets)
			}
			require.Empty(t, stub.Assets, "asset projection must not mutate the persisted stub")
		})
	}
}
