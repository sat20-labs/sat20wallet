package rgb11wallet

import (
	"strings"
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

func TestCanonicalAssetNameNormalizesTickerAndBindsContract(t *testing.T) {
	contractA := "rgb:Ar4ouaLv-b7f7Dc_-z5EMvtu-FA5KNh1-nlae~jk-8xMBo7E"
	contractB := "rgb:k0vsa6zj-CLYfnru-63unuJv-qZ2IVJ5-zlENzlF-MkiJNuw"
	nameA, err := NewCanonicalAssetName(contractA, "  USD T!! Coin ", indexer.ASSET_TYPE_FT)
	if err != nil {
		t.Fatal(err)
	}
	nameB, err := NewCanonicalAssetName(contractB, "USD T!! Coin", indexer.ASSET_TYPE_FT)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(nameA.Ticker, "usd-t-coin_") || len(nameA.Ticker) != len("usd-t-coin_")+DefaultFingerprintLength {
		t.Fatalf("unexpected canonical ticker %q", nameA.Ticker)
	}
	if nameA.Ticker == nameB.Ticker {
		t.Fatalf("same issuer ticker must not share a SAT20 asset key: %q", nameA.Ticker)
	}
	if _, err := OfficialAssetID(nameA); err == nil {
		t.Fatalf("canonical name must resolve through local contract metadata, not reversible ticker encoding")
	}
	if got := DisplayTicker("USD T!! Coin", strings.TrimPrefix(nameA.Ticker, "usd-t-coin_"), false); got != nameA.Ticker {
		t.Fatalf("unverified display=%q want=%q", got, nameA.Ticker)
	}
}

func TestNormalizeTicker(t *testing.T) {
	for input, expected := range map[string]string{
		" USDT  2026 ":         "usdt-2026",
		"----":                 "asset",
		"ABCDEFGHIJKLMNOPQRST": "abcdefghijklmnop",
	} {
		if actual := NormalizeTicker(input); actual != expected {
			t.Errorf("NormalizeTicker(%q)=%q want=%q", input, actual, expected)
		}
	}
}
