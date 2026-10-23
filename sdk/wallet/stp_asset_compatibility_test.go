package wallet

import (
	"errors"
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
)

func TestSTPChannelAssetCompatibility(t *testing.T) {
	rune := AssetName{AssetName: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_RUNES, Type: indexer.ASSET_TYPE_FT, Ticker: "TOKEN"}}
	rgb := AssetName{AssetName: indexer.AssetName{Protocol: "rgb11", Type: indexer.ASSET_TYPE_FT, Ticker: strings.Repeat("a", 64)}, N: 2}
	arc := AssetName{AssetName: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ATOM, Type: indexer.ASSET_TYPE_FT, Ticker: "quark"}, N: 1}
	for _, operation := range []STPAssetOperation{STPOpSpliceIn, STPOpExpand} {
		t.Run(string(operation), func(t *testing.T) {
			for _, pair := range []struct {
				name string
				existing, incoming AssetName
				conflict bool
			}{
				{"runes-then-rgb", rune, rgb, true},
				{"rgb-then-runes", rgb, rune, true},
				{"runes-then-runes", rune, rune, false},
				{"rgb-then-rgb", rgb, rgb, false},
				{"arc20-then-rgb", arc, rgb, false},
				{"rgb-then-arc20", rgb, arc, false},
				{"arc20-then-runes", arc, rune, false},
				{"runes-then-arc20", rune, arc, false},
			} {
				t.Run(pair.name, func(t *testing.T) {
					for _, source := range []string{"funding", "local-balance", "remote-balance"} {
						t.Run(source, func(t *testing.T) {
							channel := &Channel{ChannelInDB: ChannelInDB{
								FundingUtxos: make(map[AssetName][]*TxOutput),
								LocalCommitment: NewChannelCommitment(), RemoteCommitment: NewChannelCommitment(),
							}}
							switch source {
							case "funding":
								channel.FundingUtxos[pair.existing] = []*TxOutput{indexer.NewTxOutput(1000)}
							case "local-balance":
								channel.LocalCommitment.LocalBalance[pair.existing] = indexer.NewDefaultDecimal(1)
							case "remote-balance":
								channel.RemoteCommitment.RemoteBalance[pair.existing] = indexer.NewDefaultDecimal(1)
							}
							err := ValidateSTPAssetForChannel(channel, &pair.incoming.AssetName, operation)
							if pair.conflict {
								if !errors.Is(err, ErrSTPAssetConflict) {
									t.Fatalf("expected explicit conflict before funding, got %v", err)
								}
							} else if err != nil {
								t.Fatalf("compatible assets rejected: %v", err)
							}
						})
					}
				})
			}
		})
	}
	t.Run("retained-proof-is-not-active-funding", func(t *testing.T) {
		channel := &Channel{ChannelInDB: ChannelInDB{
			FundingUtxos: map[AssetName][]*TxOutput{rgb: nil},
			AssetFunding: map[string][]byte{"rgb11": []byte("retained recovery journal")},
			LocalCommitment: NewChannelCommitment(), RemoteCommitment: NewChannelCommitment(),
		}}
		channel.LocalCommitment.LocalBalance[rgb] = indexer.NewDefaultDecimal(0)
		if err := ValidateSTPAssetForChannel(channel, &rune.AssetName, STPOpSpliceIn); err != nil {
			t.Fatalf("full native exit must permit new funding: %v", err)
		}
	})
	if err := ValidateSTPAssetForChannel(nil, &rgb.AssetName, STPOpExpand); err == nil {
		t.Fatal("missing channel was accepted")
	}
}
