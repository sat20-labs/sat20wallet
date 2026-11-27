package rgb11wallet

import (
	"encoding/hex"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
)

func TestContractAssetKeyUsesCanonicalShortName(t *testing.T) {
	const contractA = "rgb:Ar4ouaLv-b7f7Dc_-z5EMvtu-FA5KNh1-nlae~jk-8xMBo7E"
	const contractB = "rgb:k0vsa6zj-CLYfnru-63unuJv-qZ2IVJ5-zlENzlF-MkiJNuw"
	name, err := NewContractAssetKey(contractA, indexer.ASSET_TYPE_FT, "USD")
	if err != nil {
		t.Fatal(err)
	}
	id, err := consensus.ParseContractID(contractA)
	if err != nil {
		t.Fatal(err)
	}
	full := hex.EncodeToString(id[:])
	if name.Protocol != Protocol || name.Type != indexer.ASSET_TYPE_FT || name.Ticker != "usd@"+full[len(full)-4:] {
		t.Fatalf("not the canonical short name: %+v", name)
	}
	if !ContractAssetKeyMatches(name, contractA, "USD") || ContractAssetKeyMatches(name, contractB, "USD") {
		t.Fatal("wrong derived name comparison")
	}
	for _, rejected := range []string{full, "usd@alice", "USD@" + full[len(full)-4:], "usd@@1234"} {
		forged := name
		forged.Ticker = rejected
		if IsCanonicalAssetName(forged) || ContractAssetKeyMatches(forged, contractA, "USD") {
			t.Fatalf("malformed or old name accepted: %s", rejected)
		}
	}
	forged := name
	forged.Protocol = "other"
	if ContractAssetKeyMatches(forged, contractA, "USD") {
		t.Fatal("wrong protocol accepted")
	}
	if _, err := NewContractAssetKey("not-a-contract", "f", "USD"); err == nil {
		t.Fatal("invalid ContractID accepted")
	}
	if _, err := NewContractAssetKey(contractA, "alice", "USD"); err == nil {
		t.Fatal("provider was accepted as an asset type")
	}
}

func TestNormalizeTicker(t *testing.T) {
	for input, want := range map[string]string{
		" USDT  2026 ":         "usdt-2026",
		"----":                 "asset",
		"ABCDEFGHIJKLMNOPQRST": "abcdefghijklmnopqrst",
	} {
		if got := NormalizeTicker(input); got != want {
			t.Errorf("NormalizeTicker(%q) = %q, want %q", input, got, want)
		}
	}
}
