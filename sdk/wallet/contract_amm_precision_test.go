package wallet

import (
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func TestAmmSellPreservesFeeAdjustedPrecision(t *testing.T) {
	for _, tc := range []struct {
		name       string
		precision  int
		amount     string
		wantValue  int64
		wantPrice  string
		wantAssets string
	}{
		{"integer asset", 0, "1", 110, "110.88", "10"},
		{"fractional asset", 2, "0.1", 12, "120.9677", "9.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := &rollbackContractManager{Manager: &Manager{
				db: newMemoryKVDB(), cfg: &common.Config{Mode: "test"},
			}}
			runtime := NewAmmContractRuntime(manager)
			runtime.Status = CONTRACT_STATUS_READY
			runtime.ChannelAddr = "amm-sell-precision"
			runtime.Divisibility = tc.precision
			runtime.dealDivisibility = tc.precision
			runtime.Contract.(*AmmContract).AssetName = swire.AssetName{
				Protocol: "ordx", Type: "f", Ticker: "vvcc",
			}
			runtime.AssetAmtInPool = indexer.NewDecimal(9, tc.precision)
			runtime.SatsValueInPool = 1111
			runtime.k = indexer.NewDecimal(9999, tc.precision+2)
			amount, err := indexer.NewDecimalFromString(tc.amount, tc.precision)
			if err != nil {
				t.Fatal(err)
			}
			item := &SwapHistoryItem{
				OrderType: ORDERTYPE_SELL, InUtxo: "sell-precision:0", Address: "seller",
				InValue: 10, ServiceFee: 10, InAmt: amount.Clone(), RemainingAmt: amount.Clone(),
				ExpectedAmt: indexer.NewDefaultDecimal(0), UnitPrice: indexer.NewDefaultDecimal(0),
			}
			runtime.addItem(item)
			if !runtime.swap(runtime.AssetAmtInPool.Clone(), runtime.SatsValueInPool) {
				t.Fatal("sell was refunded instead of dealt")
			}
			if item.Reason != INVOKE_REASON_NORMAL || item.OutValue != tc.wantValue || item.RemainingAmt != nil {
				t.Fatalf("unexpected sell result: %+v", item)
			}
			if runtime.LastDealPrice == nil || runtime.LastDealPrice.String() != tc.wantPrice ||
				item.UnitPrice == nil || item.UnitPrice.String() != tc.wantPrice {
				t.Fatalf("deal price=%v item price=%v, want %s", runtime.LastDealPrice, item.UnitPrice, tc.wantPrice)
			}
			if runtime.AssetAmtInPool.String() != tc.wantAssets || runtime.SatsValueInPool != 1111-tc.wantValue {
				t.Fatalf("unexpected pool=%s/%d", runtime.AssetAmtInPool, runtime.SatsValueInPool)
			}
			if amount.String() != tc.amount {
				t.Fatal("sell calculation mutated the input asset amount")
			}
		})
	}
}
