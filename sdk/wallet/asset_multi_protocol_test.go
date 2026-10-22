package wallet

import (
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
)

func TestHasMultiAssetPreservesNativeAndLegacyCarriers(t *testing.T) {
	arc := indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ATOM, Type: indexer.ASSET_TYPE_FT, Ticker: "quark"}
	atomNFT := indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ATOM, Type: indexer.ASSET_TYPE_NFT, Ticker: "atomical"}
	rgb := indexer.AssetName{Protocol: "rgb11", Type: indexer.ASSET_TYPE_FT, Ticker: "contract-a"}
	otherRGB := indexer.AssetName{Protocol: "rgb11", Type: indexer.ASSET_TYPE_FT, Ticker: "contract-b"}
	rune := indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_RUNES, Type: indexer.ASSET_TYPE_FT, Ticker: "TOKEN"}
	brc20 := indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_BRC20, Type: indexer.ASSET_TYPE_FT, Ticker: "ordi"}
	ordxFT := indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ORDX, Type: indexer.ASSET_TYPE_FT, Ticker: "sat20"}
	ordxNS := indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ORDX, Type: indexer.ASSET_TYPE_NS, Ticker: "name.sats"}
	ordxNFT := indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ORDX, Type: indexer.ASSET_TYPE_NFT, Ticker: "inscription"}
	ordxExotic := indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ORDX, Type: indexer.ASSET_TYPE_EXOTIC, Ticker: "rare"}
	future := indexer.AssetName{Protocol: "future", Type: indexer.ASSET_TYPE_FT, Ticker: "unit"}

	cases := []struct {
		name   string
		target indexer.AssetName
		other  *indexer.AssetName
		want   bool
	}{
		{"arc20-only", arc, nil, false},
		{"arc20-with-runes", arc, &rune, true},
		{"arc20-with-ordx-inscription", arc, &ordxNFT, true},
		{"arc20-with-rgb11", arc, &rgb, true},
		{"rgb11-only", rgb, nil, false},
		{"rgb11-with-arc20", rgb, &arc, true},
		{"rgb11-with-ordx-exotic", rgb, &ordxExotic, true},
		{"rgb11-with-another-contract", rgb, &otherRGB, true},
		{"future-only", future, nil, false},
		{"future-with-runes", future, &rune, true},
		{"runes-with-ordx-inscription", rune, &ordxNFT, true},
		{"brc20-with-underlying-inscription", brc20, &ordxNFT, false},
		{"brc20-with-other-protocol-nft", brc20, &atomNFT, true},
		{"ordx-ft-with-underlying-inscription", ordxFT, &ordxNFT, false},
		{"ordx-ft-with-underlying-exotic", ordxFT, &ordxExotic, false},
		{"ordx-ft-with-other-protocol-nft", ordxFT, &atomNFT, true},
		{"ordx-ns-with-exotic", ordxNS, &ordxExotic, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			output := &TxOutput{Assets: indexer.TxAssets{
				{Name: tc.target, Amount: *indexer.NewDefaultDecimal(1)},
			}}
			if tc.other != nil {
				output.Assets = append(output.Assets, indexer.AssetInfo{
					Name: *tc.other, Amount: *indexer.NewDefaultDecimal(1),
				})
			}
			if got := HasMultiAsset(output, &tc.target); got != tc.want {
				t.Fatalf("HasMultiAsset(%s, %+v) = %t, want %t", tc.target.String(), tc.other, got, tc.want)
			}
		})
	}
}
