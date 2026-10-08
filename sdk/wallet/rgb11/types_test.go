package rgb11wallet

import (
	"encoding/hex"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
)

func TestContractAssetKeyUsesCompleteIdentity(t *testing.T) {
	const contractA = "rgb:Ar4ouaLv-b7f7Dc_-z5EMvtu-FA5KNh1-nlae~jk-8xMBo7E"
	const contractB = "rgb:k0vsa6zj-CLYfnru-63unuJv-qZ2IVJ5-zlENzlF-MkiJNuw"
	nameA, err := NewContractAssetKey(contractA, indexer.ASSET_TYPE_FT)
	if err != nil {
		t.Fatal(err)
	}
	id, err := consensus.ParseContractID(contractA)
	if err != nil {
		t.Fatal(err)
	}
	if nameA.Protocol != Protocol || nameA.Type != indexer.ASSET_TYPE_FT || nameA.Ticker != hex.EncodeToString(id[:]) {
		t.Fatalf("not the full contract identity: %+v", nameA)
	}
	nameB, err := NewContractAssetKey(contractB, indexer.ASSET_TYPE_FT)
	if err != nil || nameA == nameB {
		t.Fatalf("different contracts collided: %+v %+v %v", nameA, nameB, err)
	}
	if !ContractAssetKeyMatches(nameA, contractA) || ContractAssetKeyMatches(nameA, contractB) {
		t.Fatal("contract-key verification accepted a different contract")
	}
	forged := nameA
	forged.Ticker = "usdt@alice"
	if ContractAssetKeyMatches(forged, contractA) {
		t.Fatal("a readable alias was accepted as a contract identity")
	}
	forged = nameA
	forged.Protocol = "other"
	if ContractAssetKeyMatches(forged, contractA) {
		t.Fatal("wrong protocol accepted")
	}
	if _, err := NewContractAssetKey("not-a-contract", "f"); err == nil {
		t.Fatal("invalid ContractID accepted")
	}
	if _, err := NewContractAssetKey(contractA, "alice"); err == nil {
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
