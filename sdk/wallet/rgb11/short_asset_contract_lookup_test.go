package rgb11wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/rgb11/seals"
)

func TestFindValidatedContractByCanonicalAssetName(t *testing.T) {
	db := indexerdb.NewKVDB(t.TempDir())
	defer db.Close()
	store := NewProjectionStore(db, nil)
	if err := store.SetScope("short-name-a"); err != nil {
		t.Fatal(err)
	}
	var tx [32]byte
	tx[0] = 1
	seal, err := seals.NewGraphBlindSeal(tx[:], 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := issuance.Issue(issuance.Spec{
		Kind: schemas.NIA, Network: issuance.BitcoinRegtest,
		Ticker: "NAMED", Name: "Canonical lookup contract", Precision: 2,
		Allocations: []issuance.Allocation{{Seal: seal, Amount: 20_000}}, Timestamp: 1_730_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(issued.Armor)
	reference, err := AddContractObjectSnapshotReference(nil, issued.ContractID, raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ImportSnapshot(reference); err != nil {
		t.Fatal(err)
	}
	expected, err := NewContractAssetKey(issued.ContractID, indexer.ASSET_TYPE_FT, "NAMED")
	if err != nil {
		t.Fatal(err)
	}
	id, material, err := store.FindValidatedContractByAssetName(expected)
	if err != nil || id != issued.ContractID || !bytes.Equal(material, raw) {
		t.Fatalf("cold name lookup returned the wrong identity: id=%s err=%v", id, err)
	}
	other, err := NewContractAssetKey(issued.ContractID, indexer.ASSET_TYPE_FT, "OTHER")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.FindValidatedContractByAssetName(other); !errors.Is(err, indexer.ErrKeyNotFound) {
		t.Fatalf("ticker with matching suffix but different genesis was accepted: %v", err)
	}
	idHex, err := RGB11ContractIDHex(issued.ContractID)
	if err != nil {
		t.Fatal(err)
	}
	old := expected
	old.Ticker = idHex
	if _, _, err := store.FindValidatedContractByAssetName(old); !errors.Is(err, ErrInvalidRGB11Asset) {
		t.Fatalf("old name returned an identity: %v", err)
	}
	if err := store.SetScope("short-name-b"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.FindValidatedContractByAssetName(expected); !errors.Is(err, indexer.ErrKeyNotFound) {
		t.Fatalf("lookup leaked an asset in another scope: %v", err)
	}
}

func TestProjectionSnapshotRejectsCollidingShortAssetNames(t *testing.T) {
	const left = "rgb:Ar4ouaLv-b7f7Dc_-z5EMvtu-FA5KNh1-nlae~jk-8xMBo7E"
	const right = "rgb:Br4ouaLv-b7f7Dc_-z5EMvtu-FA5KNh1-nlae~jk-8xMBo7E"
	leftName, err := NewContractAssetKey(left, indexer.ASSET_TYPE_FT, "TWIN")
	if err != nil {
		t.Fatal(err)
	}
	rightName, err := NewContractAssetKey(right, indexer.ASSET_TYPE_FT, "TWIN")
	if err != nil || rightName != leftName {
		t.Fatalf("invalid collision fixture: %s %s %v", leftName, rightName, err)
	}
	var records []SnapshotRecord
	for _, id := range []string{left, right} {
		raw := []byte("synthetic validation projection for " + id)
		hash := sha256.Sum256(raw)
		hashHex := hex.EncodeToString(hash[:])
		receipt := &ValidationReceipt{EngineBuildID: RGB11EngineBuildID, Version: 1, ContractID: id,
			SchemaID: "schema", ConsignmentHash: hashHex, Status: "valid", ValidatedAt: 1,
			Allocations: []ValidatedAllocation{{AssetName: leftName, Amount: *indexer.NewDefaultDecimal(1),
				OutPoint: "test:0", OperationID: "operation", AssignmentType: 4000, StateClass: "fungible",
				SealDisclosure: []byte{1}}}}
		value, err := encode(receipt)
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, SnapshotRecord{Key: "object-" + hashHex, Value: raw},
			SnapshotRecord{Key: "validation-" + hashHex, Value: value})
	}
	if err := validateProjectionSnapshot(records[:2]); err != nil {
		t.Fatalf("one validated asset should be usable: %v", err)
	}
	if err := validateProjectionSnapshot(records); !errors.Is(err, ErrValidationReceipt) {
		t.Fatalf("restore accepted two contracts under one asset name: %v", err)
	}
}
