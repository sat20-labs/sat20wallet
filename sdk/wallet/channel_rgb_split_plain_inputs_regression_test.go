package wallet

import (
	"testing"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
)

// This is the splitter's ordinary caller input shape, not a proof that a
// Bitcoin-only indexer can establish absence of hidden RGB. The RGB source is
// core-issued/validated; plain outputs are materialized from actual BTC tx
// outputs, without clearing fields on a native carrier or bypassing the
// existing STP hidden-input checks.
func TestSTPRGBSplitAllowsCanonicalPlainChannelAndFeeInputs(t *testing.T) {
	fixture := newPrecisionSeparationNativeFixture(t, 2)
	asset := &AssetName{AssetName: fixture.source.AssetName, N: 0}
	ticker, err := fixture.manager.GetSTPFundingTickerInfo(&fixture.source.AssetName, fixture.channel.AssetFunding)
	if err != nil {
		t.Fatal(err)
	}
	amount, err := indexer.NewDecimalFromString("60", ticker.Divisibility)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("actual core source quantity precision=%d authenticated ticker.Divisibility=%d requested60 precision=%d native.BindingSat=%d identity.N=%d", fixture.source.Amount.Precision, ticker.Divisibility, amount.Precision, fixture.output.Assets[0].BindingSat, asset.N)
	if fixture.source.Amount.Precision != 2 || ticker.Divisibility != 2 || amount.Precision != 2 || fixture.output.Assets[0].BindingSat != 0 {
		t.Fatal("real two-decimal fixture was changed instead of preserving the quantity unit")
	}
	script, err := AddrToPkScript(fixture.wallet.GetAddress(), GetChainParam())
	if err != nil {
		t.Fatal(err)
	}
	plainParent := wire.NewMsgTx(2)
	plainParent.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: 99}, nil, nil))
	plainParent.AddTxOut(wire.NewTxOut(12000, fixture.channel.GetChannelPkScript()))
	plainParent.AddTxOut(wire.NewTxOut(3000, script))
	channelInput, feeInput := indexer.GenerateTxOutput(plainParent, 0), indexer.GenerateTxOutput(plainParent, 1)
	for _, input := range []*TxOutput{channelInput, feeInput} {
		if len(input.Assets) != 0 || len(input.Invalids) != 0 || len(input.Offsets) != 0 || len(input.SatBindingMap) != 0 {
			t.Fatal("canonical plain tx output unexpectedly has asset metadata")
		}
	}
	t.Run("native_only_positive_control", func(t *testing.T) {
		output, change, err := GenTxOutput([]*TxOutput{fixture.output.Clone()}, asset, 330, amount)
		if err != nil || output == nil || change == nil {
			t.Fatalf("native-only ordinary control failed: %v", err)
		}
	})
	t.Run("native_plus_channel_and_explicit_fee", func(t *testing.T) {
		inputs := []*TxOutput{fixture.output.Clone(), channelInput.Clone(), feeInput.Clone()}
		output, change, err := GenTxOutput(inputs, asset, 330, amount)
		if err != nil {
			t.Fatalf("ordinary pure BTC channel/fee inputs were mistaken for wrong-unit RGB allocations: %v", err)
		}
		if output == nil || change == nil || output.Value()+change.Value() != fixture.output.Value()+channelInput.Value()+feeInput.Value() {
			t.Fatal("split lost carrier or explicit plain input value")
		}
		allocated := output.GetAsset(&asset.AssetName)
		returned := change.GetAsset(&asset.AssetName)
		if allocated == nil || returned == nil || allocated.Precision != 2 || returned.Precision != 2 || allocated.Cmp(amount) != 0 || allocated.Add(returned).Cmp(&fixture.source.Amount) != 0 || output.Assets[0].BindingSat != 0 || change.Assets[0].BindingSat != 0 {
			t.Fatal("plain fee handling changed native quantity, precision or binding")
		}
	})
	for _, scenario := range []string{"wrong_native_precision", "nonzero_native_binding", "declared_non_rgb_fee_asset", "invalid_asset_marker"} {
		t.Run(scenario, func(t *testing.T) {
			native, fee := fixture.output.Clone(), feeInput.Clone()
			switch scenario {
			case "wrong_native_precision":
				native.Assets[0].Amount.Precision = 1
			case "nonzero_native_binding":
				native.Assets[0].BindingSat = 1
			case "declared_non_rgb_fee_asset":
				fee.Assets = indexer.TxAssets{{Name: indexer.AssetName{Protocol: "ordx", Type: indexer.ASSET_TYPE_FT, Ticker: "declared-old-asset"}, Amount: *indexer.NewDefaultDecimal(1), BindingSat: 1}}
			case "invalid_asset_marker":
				fee.Invalids = map[indexer.AssetName]bool{fixture.source.AssetName: true}
			}
			if _, _, err := GenTxOutput([]*TxOutput{native, channelInput.Clone(), fee}, asset, 330, amount); err == nil {
				t.Fatal("RGB split accepted a mismatched native unit/binding or non-plain input")
			}
		})
	}
}
