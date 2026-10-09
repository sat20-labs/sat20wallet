package rgb11wallet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
)

func TestLocalNamePersistenceDoesNotChangeValidatedAssetState(t *testing.T) {
	const contractID = "rgb:Ar4ouaLv-b7f7Dc_-z5EMvtu-FA5KNh1-nlae~jk-8xMBo7E"
	db := indexerdb.NewKVDB(t.TempDir())
	defer db.Close()
	store := NewProjectionStore(db, &recordingLocker{})
	if err := store.SetScope("naming-account-a"); err != nil {
		t.Fatal(err)
	}
	identity, err := NewContractAssetKey(contractID, indexer.ASSET_TYPE_FT)
	if err != nil {
		t.Fatal(err)
	}
	amount, err := indexer.NewDecimalFromString("42", 0)
	if err != nil {
		t.Fatal(err)
	}
	output := indexer.NewTxOutput(1000)
	output.OutPointStr = "0000000000000000000000000000000000000000000000000000000000000001:0"
	raw := []byte("validated-naming-regression-fixture")
	digest := sha256.Sum256(raw)
	receipt, err := store.ValidateAndStoreConsignment(context.Background(), testValidator{receipt: &ValidationReceipt{
		Version: 1, EngineBuildID: "naming-test", ConsignmentHash: hex.EncodeToString(digest[:]),
		ContractID: contractID, SchemaID: "schema", StateHash: [32]byte{1}, Status: "valid",
		Allocations: []ValidatedAllocation{{OutPoint: output.OutPointStr, AssetName: identity,
			Amount: *amount.Clone(), OperationID: "op", AssignmentType: 4000, StateClass: "fungible", SealDisclosure: []byte{1}}},
	}}, testEvidence{}, raw)
	if err != nil {
		t.Fatal(err)
	}
	beforeHash, err := receipt.Hash()
	if err != nil {
		t.Fatal(err)
	}
	asset := &indexer.AssetInfo{Name: identity, Amount: *amount.Clone()}
	proof := &AllocationProof{OutPoint: output.OutPointStr, AssetName: identity, OperationID: "op", AssignmentType: 4000,
		StateClass: "fungible", SealDisclosure: []byte{1}, Status: "valid", ConsignmentHash: receipt.ConsignmentHash, ValidationHash: beforeHash}
	if err := store.CommitProjection(output, asset, proof); err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{"usdt@123456789012", "usdt@alice", "my local label"} {
		if err := store.SaveLocalAssetName(contractID, alias); err != nil {
			t.Fatal(err)
		}
		if got, err := store.LoadLocalAssetName(contractID); err != nil || got != alias {
			t.Fatalf("local alias %q: got=%q err=%v", alias, got, err)
		}
		if err := store.AssertConsistent(output.OutPointStr, identity); err != nil {
			t.Fatalf("rename broke projection: %v", err)
		}
		balance, err := store.Balance(identity)
		if err != nil || balance.Cmp(amount) != 0 {
			t.Fatalf("rename changed balance: %v %v", balance, err)
		}
		loaded, err := store.LoadValidationReceipt(receipt.ConsignmentHash)
		if err != nil {
			t.Fatal(err)
		}
		afterHash, err := loaded.Hash()
		if err != nil || afterHash != beforeHash {
			t.Fatalf("rename changed validation receipt: before=%s after=%s err=%v", beforeHash, afterHash, err)
		}
	}
	reopened := NewProjectionStore(db, &recordingLocker{})
	if err := reopened.SetScope("naming-account-a"); err != nil {
		t.Fatal(err)
	}
	if got, err := reopened.LoadLocalAssetName(contractID); err != nil || got != "my local label" {
		t.Fatalf("alias not persisted: %q %v", got, err)
	}
	if err := reopened.SetScope("naming-account-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.LoadLocalAssetName(contractID); !errors.Is(err, indexer.ErrKeyNotFound) {
		t.Fatalf("alias leaked across accounts: %v", err)
	}
	reopened.ClearScope()
	if err := reopened.SaveLocalAssetName(contractID, "new"); !errors.Is(err, ErrWalletScope) {
		t.Fatalf("locked wallet accepted rename: %v", err)
	}
}

func TestLocalNameStoreRejectsInvalidIdentityAndCorruption(t *testing.T) {
	const contractID = "rgb:Ar4ouaLv-b7f7Dc_-z5EMvtu-FA5KNh1-nlae~jk-8xMBo7E"
	db := indexerdb.NewKVDB(t.TempDir())
	defer db.Close()
	store := NewProjectionStore(db, nil)
	if err := store.SetScope("naming"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLocalAssetName("not-a-contract", "label"); err == nil {
		t.Fatal("invalid ContractID accepted")
	}
	key, err := store.localAssetNameKey(contractID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Write(key, []byte{0xff}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadLocalAssetName(contractID); !errors.Is(err, ErrRGB11Inconsistent) {
		t.Fatalf("corrupt alias silently hidden: %v", err)
	}
}

func TestRegisteredNameRejectsUnauthenticatedCacheAndSnapshot(t *testing.T) {
	const contractID = "rgb:Ar4ouaLv-b7f7Dc_-z5EMvtu-FA5KNh1-nlae~jk-8xMBo7E"
	db := indexerdb.NewKVDB(t.TempDir())
	defer db.Close()
	store := NewProjectionStore(db, nil)
	if err := store.SetScope("naming"); err != nil {
		t.Fatal(err)
	}
	key, err := store.registeredAssetNameKey(contractID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Write(key, []byte("rgb11:f:usd@alice")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadRegisteredAssetName(contractID); !errors.Is(err, ErrRGB11Inconsistent) {
		t.Fatalf("unsigned name became registered: %v", err)
	}
	identity, err := NewContractAssetKey(contractID, indexer.ASSET_TYPE_FT)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []SnapshotRecord{
		{Key: "registered-name-" + identity.Ticker, Value: []byte("rgb11:f:usd@alice")},
		{Key: "local-name-" + identity.Ticker, Value: []byte("orphan alias")},
		{Key: "local-name-" + identity.Ticker, Value: []byte{0xff}},
	} {
		if err := store.ValidateSnapshot([]SnapshotRecord{record}); err == nil {
			t.Fatalf("invalid naming snapshot accepted: %s", record.Key)
		}
	}
}
