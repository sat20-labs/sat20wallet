package rgb11wallet

import (
	"encoding/hex"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
)

// Local display names are mutable before SatoshiNet registration. Validation
// receipts retain the full contract ID independently. The allocation name
// uses the committed genesis ticker rather than the mutable display alias.
func TestRGB11NamingIdentitySurvivesLocalRename(t *testing.T) {
	const id = "rgb:Ar4ouaLv-b7f7Dc_-z5EMvtu-FA5KNh1-nlae~jk-8xMBo7E"
	local := LocalNameMetadata{ContractID: id, LocalName: "usdt@123456789012"}
	renamed, err := local.Rename("usdt@alice")
	if err != nil || renamed.ContractID != id || renamed.LocalName == local.LocalName {
		t.Fatalf("local rename failed: %+v %v", renamed, err)
	}
	before, err := NewContractAssetKey(local.ContractID, indexer.ASSET_TYPE_FT, "TEST")
	if err != nil {
		t.Fatal(err)
	}
	after, err := NewContractAssetKey(renamed.ContractID, indexer.ASSET_TYPE_FT, "TEST")
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
	idHex := hex.EncodeToString(contract[:])
	if before.Ticker != "test@"+idHex[len(idHex)-4:] {
		t.Fatalf("allocation name must use the committed short ticker: %s", before.Ticker)
	}
}
