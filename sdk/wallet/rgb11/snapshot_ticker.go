package rgb11wallet

import (
	"crypto/sha256"
	"encoding/hex"

	indexer "github.com/sat20-labs/indexer/common"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/schemas"
)

// A validated contract receipt has one presentation ticker, but its live
// allocations may also contain control rights. Preserve the established
// receipt-based lookup when a normal ownership allocation is present. Decode
// genesis only when a control-only receipt cannot supply the primary type.
func snapshotTickerRef(objects map[string][]byte, receipt *ValidationReceipt) (SnapshotTickerRef, error) {
	if receipt == nil || len(receipt.Allocations) == 0 {
		return SnapshotTickerRef{}, ErrValidationReceipt
	}
	// An exported metadata helper must verify its referenced object even when
	// its caller has not performed complete wallet-snapshot validation first.
	raw := objects[receipt.ConsignmentHash]
	digest := sha256.Sum256(raw)
	if len(raw) == 0 || hex.EncodeToString(digest[:]) != receipt.ConsignmentHash {
		return SnapshotTickerRef{}, ErrValidationReceipt
	}
	var name indexer.AssetName
	found := false
	for _, allocation := range receipt.Allocations {
		if allocation.AssetName.Type == "control" {
			continue
		}
		if found && allocation.AssetName != name {
			return SnapshotTickerRef{}, ErrValidationReceipt
		}
		name, found = allocation.AssetName, true
	}
	if !found {
		var err error
		name, err = snapshotPrimaryAssetFromGenesis(raw, receipt)
		if err != nil {
			return SnapshotTickerRef{}, err
		}
	}
	if name.Protocol != Protocol || (name.Type != indexer.ASSET_TYPE_FT && name.Type != indexer.ASSET_TYPE_NFT) {
		return SnapshotTickerRef{}, ErrValidationReceipt
	}
	for _, allocation := range receipt.Allocations {
		expected := name
		if allocation.AssignmentType != 4000 {
			expected.Type = "control"
		}
		if allocation.AssetName != expected {
			return SnapshotTickerRef{}, ErrValidationReceipt
		}
	}
	return SnapshotTickerRef{ContractID: receipt.ContractID, AssetName: name.String()}, nil
}

func snapshotPrimaryAssetFromGenesis(raw []byte, receipt *ValidationReceipt) (indexer.AssetName, error) {
	container, err := coreconsignment.Decode(raw)
	if err != nil || container.ContractID != receipt.ContractID || container.SchemaID != receipt.SchemaID {
		return indexer.AssetName{}, ErrValidationReceipt
	}
	descriptor, err := schemas.ByKind(container.GenesisReport.Kind)
	if err != nil {
		return indexer.AssetName{}, err
	}
	schema, schemaOK := container.Value.Field("schema")
	types, typesOK := container.Value.Field("types")
	genesis, genesisOK := container.Value.Field("genesis")
	if !schemaOK || !typesOK || !genesisOK {
		return indexer.AssetName{}, ErrValidationReceipt
	}
	metadata, err := schemas.ExtractGenesisAssetMetadata(schema, types, genesis)
	if err != nil {
		return indexer.AssetName{}, err
	}
	assetType := indexer.ASSET_TYPE_FT
	if !descriptor.Fungible {
		assetType = indexer.ASSET_TYPE_NFT
	}
	return NewCanonicalAssetName(receipt.ContractID, metadata.Ticker, assetType)
}
