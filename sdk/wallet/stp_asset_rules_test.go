package wallet

import (
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
)

func TestSTPAssetRulesRejectUnsupportedIdentityAndOperation(t *testing.T) {
	plain := indexer.ASSET_PLAIN_SAT
	malformedPlain := indexer.AssetName{Type: indexer.ASSET_TYPE_FT, Ticker: "bitcoin"}
	ordxName := indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ORDX, Type: indexer.ASSET_TYPE_NS, Ticker: "alice.sats"}
	runeNFT := indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_RUNES, Type: indexer.ASSET_TYPE_NFT, Ticker: "ART"}
	brcNFT := indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_BRC20, Type: indexer.ASSET_TYPE_NFT, Ticker: "ordi"}
	rgbName := indexer.AssetName{Protocol: "rgb11", Type: indexer.ASSET_TYPE_FT, Ticker: "ordi"}
	tests := []struct {
		name      string
		rules     STPAssetRules
		asset     *indexer.AssetName
		operation STPAssetOperation
	}{
		{"nil-bitcoin", btcSTPRules{}, nil, STPOpCommit},
		{"malformed-bitcoin", btcSTPRules{}, &malformedPlain, STPOpTransfer},
		{"unknown-operation", btcSTPRules{}, &plain, STPAssetOperation("future-operation")},
		{"name-contract", ordxSTPRules{}, &ordxName, STPOpContract},
		{"rune-nft", runesSTPRules{}, &runeNFT, STPOpSpliceIn},
		{"brc20-nft", brc20STPRules{}, &brcNFT, STPOpForceClose},
		{"different-protocol", brc20STPRules{}, &rgbName, STPOpCommit},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.rules.ValidateAsset(test.asset, test.operation); err == nil {
				t.Fatal("unsupported STP capability was accepted")
			}
		})
	}
}

func TestSTPAssetRulesKeepExistingChannelAssetClasses(t *testing.T) {
	operations := []STPAssetOperation{STPOpTransfer, STPOpLock, STPOpUnlock, STPOpSpliceIn,
		STPOpSpliceOut, STPOpExpand, STPOpCommit, STPOpCoopClose, STPOpForceClose, STPOpPunish}
	assets := []struct {
		rules STPAssetRules
		name  indexer.AssetName
	}{
		{btcSTPRules{}, indexer.ASSET_PLAIN_SAT},
		{ordxSTPRules{}, indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ORDX, Type: indexer.ASSET_TYPE_FT, Ticker: "dogcoin"}},
		{ordxSTPRules{}, indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ORDX, Type: indexer.ASSET_TYPE_NS, Ticker: "alice.sats"}},
		{ordxSTPRules{}, indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ORDX, Type: indexer.ASSET_TYPE_NFT}},
		{ordxSTPRules{}, indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ORDX, Type: indexer.ASSET_TYPE_EXOTIC, Ticker: "uncommon"}},
		{runesSTPRules{}, indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_RUNES, Type: indexer.ASSET_TYPE_FT, Ticker: "CHANNEL•TEST"}},
		{brc20STPRules{}, indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_BRC20, Type: indexer.ASSET_TYPE_FT, Ticker: "ordi"}},
	}
	for _, asset := range assets {
		for _, operation := range operations {
			if err := asset.rules.ValidateAsset(&asset.name, operation); err != nil {
				t.Errorf("existing asset %s cannot perform %s: %v", asset.name.String(), operation, err)
			}
		}
	}
}

func TestSTPAssetContractNormalizationPreservesIdentityRules(t *testing.T) {
	tests := []struct {
		name  string
		rules STPAssetRules
		asset indexer.AssetName
		want  string
		valid bool
	}{
		{"bitcoin", btcSTPRules{}, indexer.ASSET_PLAIN_SAT, "", true},
		{"ordx-case", ordxSTPRules{}, indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ORDX, Type: indexer.ASSET_TYPE_FT, Ticker: "DogCoin"}, "dogcoin", true},
		{"brc20-case", brc20STPRules{}, indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_BRC20, Type: indexer.ASSET_TYPE_FT, Ticker: "ORDI"}, "ordi", true},
		{"brc20-five-bytes", brc20STPRules{}, indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_BRC20, Type: indexer.ASSET_TYPE_FT, Ticker: "ABCDE"}, "abcde", true},
		{"rune-spacers", runesSTPRules{}, indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_RUNES, Type: indexer.ASSET_TYPE_FT, Ticker: "CHANNEL•TEST"}, "CHANNEL•TEST", true},
		{"rune-dot", runesSTPRules{}, indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_RUNES, Type: indexer.ASSET_TYPE_FT, Ticker: "CHANNEL.TEST"}, "", false},
		{"rune-case", runesSTPRules{}, indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_RUNES, Type: indexer.ASSET_TYPE_FT, Ticker: "channel"}, "", false},
		{"short-brc20", brc20STPRules{}, indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_BRC20, Type: indexer.ASSET_TYPE_FT, Ticker: "AB"}, "", false},
		{"invalid-ordx", ordxSTPRules{}, indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ORDX, Type: indexer.ASSET_TYPE_FT, Ticker: "invalid name"}, "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := test.asset
			err := test.rules.NormalizeContract(&test.asset)
			if !test.valid {
				if err == nil {
					t.Fatal("invalid contract asset was accepted")
				}
				if test.asset != before {
					t.Fatal("failed normalization changed asset identity")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.asset.Ticker != test.want || test.asset.Protocol != before.Protocol || test.asset.Type != before.Type {
				t.Fatalf("unexpected normalized asset: %+v", test.asset)
			}
		})
	}
}
