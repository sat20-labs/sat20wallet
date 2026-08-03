package rgb11wallet

import (
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
)

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
	if !strings.HasPrefix(nameA.Ticker, "usd-t-coin@") || len(nameA.Ticker) != len("usd-t-coin@")+DefaultFingerprintLength {
		t.Fatalf("unexpected canonical ticker %q", nameA.Ticker)
	}
	if nameA.Ticker == nameB.Ticker {
		t.Fatalf("same issuer ticker must not share a SAT20 asset key: %q", nameA.Ticker)
	}
	if got := DisplayTicker("USD T!! Coin", strings.TrimPrefix(nameA.Ticker, "usd-t-coin@"), false); got != nameA.Ticker {
		t.Fatalf("unverified display=%q want=%q", got, nameA.Ticker)
	}
	if got := DisplayTicker("USDT", "abcdefgh", true); got != "USDT" {
		t.Fatalf("verified display=%q", got)
	}
	extended, err := NewCanonicalAssetNameWithFingerprintLength(
		contractA, "USD T!! Coin", indexer.ASSET_TYPE_FT, 10,
	)
	if err != nil || !strings.HasPrefix(extended.Ticker, nameA.Ticker) ||
		!CanonicalAssetNameMatches(extended, contractA, "USD T!! Coin") {
		t.Fatalf("extended canonical name=%q err=%v", extended.Ticker, err)
	}
}

func TestNormalizeTicker(t *testing.T) {
	for input, expected := range map[string]string{
		" USDT  2026 ":         "usdt-2026",
		"----":                 "asset",
		"ABCDEFGHIJKLMNOPQRST": "abcdefghijklmnopqrst",
	} {
		if actual := NormalizeTicker(input); actual != expected {
			t.Errorf("NormalizeTicker(%q)=%q want=%q", input, actual, expected)
		}
	}
}
