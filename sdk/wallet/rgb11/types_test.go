package rgb11wallet

import (
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
)

func TestAssetIDOnlyStripsOfficialPrefix(t *testing.T) {
	official := "rgb:Ar4ouaLv-b7f7Dc_-z5EMvtu-FA5KNh1-nlae~jk-8xMBo7E"
	name, err := NewAssetName(official, indexer.ASSET_TYPE_FT)
	if err != nil {
		t.Fatal(err)
	}
	if name.Ticker != "Ar4ouaLv-b7f7Dc_-z5EMvtu-FA5KNh1-nlae~jk-8xMBo7E" {
		t.Fatalf("asset id was changed: %s", name.Ticker)
	}
	parsed := indexer.NewAssetNameFromString(name.String())
	if *parsed != name {
		t.Fatalf("SAT20 AssetName round trip %+v != %+v", *parsed, name)
	}
	restored, err := OfficialAssetID(name)
	if err != nil || restored != official {
		t.Fatalf("official id %q err=%v", restored, err)
	}
}
