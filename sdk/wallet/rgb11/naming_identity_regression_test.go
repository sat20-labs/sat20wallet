package rgb11wallet

import (
	"encoding/hex"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
)

// Local display names are mutable before SatoshiNet registration. Validation
// receipts and persisted allocation keys must therefore use the full contract
// identity, not a ticker-dependent name or a truncated fingerprint.
func TestRGB11NamingIdentitySurvivesLocalRename(t *testing.T) {
	const id = "rgb:Ar4ouaLv-b7f7Dc_-z5EMvtu-FA5KNh1-nlae~jk-8xMBo7E"
	before, err := NewCanonicalAssetName(id, "USDT", indexer.ASSET_TYPE_FT)
	if err != nil {
		t.Fatal(err)
	}
	after, err := NewCanonicalAssetName(id, "renamed locally", indexer.ASSET_TYPE_FT)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("local rename changed contract identity: %s -> %s", before.String(), after.String())
	}
	contract, err := consensus.ParseContractID(id)
	if err != nil {
		t.Fatal(err)
	}
	if before.Ticker != hex.EncodeToString(contract[:]) {
		t.Fatalf("allocation key must contain the full ContractID, not a fingerprint: %s", before.Ticker)
	}
}
