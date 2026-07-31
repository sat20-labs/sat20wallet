package rgb11wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"sort"
	"strconv"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/rgb11/seals"
	strict "github.com/sat20-labs/rgb11/strict_encoding"
)

const NativeEngineBuildID = "rgb11-go-0.11.1-rc.11+sat20.1"

// NativeConsensusValidator is the production Go validator frozen against the
// official 0.11.1-rc.11 Rust vectors.
type NativeConsensusValidator struct {
	Reveals []seals.GraphBlindSeal
}

func NewNativeConsensusValidator() *NativeConsensusValidator { return &NativeConsensusValidator{} }

func NewNativeConsensusValidatorWithReveals(reveals ...seals.GraphBlindSeal) *NativeConsensusValidator {
	return &NativeConsensusValidator{Reveals: append([]seals.GraphBlindSeal(nil), reveals...)}
}

func (v NativeConsensusValidator) ValidateConsignment(ctx context.Context, raw []byte, evidence BitcoinEvidenceProvider) (*ValidationReceipt, error) {
	receipt, _, _, err := v.validateConsignment(ctx, raw, evidence, false)
	return receipt, err
}

func (v NativeConsensusValidator) ValidatePreparedConsignment(ctx context.Context, raw []byte,
	evidence BitcoinEvidenceProvider) (*PreparedValidation, error) {
	receipt, outputs, witnessTxIDs, err := v.validateConsignment(ctx, raw, evidence, true)
	if err != nil {
		return nil, err
	}
	return &PreparedValidation{Receipt: receipt, Outputs: outputs, WitnessTxIDs: witnessTxIDs}, nil
}

func (v NativeConsensusValidator) validateConsignment(ctx context.Context, raw []byte,
	evidence BitcoinEvidenceProvider, prepared bool) (*ValidationReceipt, map[string]*BitcoinUTXO, []string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, err
	}
	container, err := coreconsignment.Decode(raw)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(v.Reveals) > 0 {
		if _, err := container.RevealGraphSeals(v.Reveals); err != nil {
			return nil, nil, nil, err
		}
	}
	resolver := coreEvidenceResolver{ctx: ctx, provider: evidence}
	var report coreconsignment.Validation
	if prepared {
		report, err = container.ValidatePrepared(resolver)
	} else {
		report, err = container.Validate(resolver)
	}
	if err != nil {
		return nil, nil, nil, err
	}
	if (!prepared && !report.ConsensusValid) ||
		(prepared && (!report.PreparedValid || report.PreparedWitnesses == 0)) {
		return nil, nil, nil, ErrValidationReceipt
	}
	descriptor, err := schemas.ByKind(container.GenesisReport.Kind)
	if err != nil {
		return nil, nil, nil, err
	}
	schemaValue, _ := container.Value.Field("schema")
	typeSystem, _ := container.Value.Field("types")
	genesisValue, _ := container.Value.Field("genesis")
	metadata, err := schemas.ExtractGenesisAssetMetadata(schemaValue, typeSystem, genesisValue)
	if err != nil {
		return nil, nil, nil, err
	}
	assetType := indexer.ASSET_TYPE_FT
	if !descriptor.Fungible {
		assetType = indexer.ASSET_TYPE_NFT
	}
	assetName, err := NewCanonicalAssetName(container.ContractID, metadata.Ticker, assetType)
	if err != nil {
		return nil, nil, nil, err
	}
	allocations := make([]ValidatedAllocation, 0, len(report.CurrentStates))
	outputs := make(map[string]*BitcoinUTXO)
	for _, state := range report.CurrentStates {
		allocationAssetName := assetName
		// Standard schemas use assignment type 4000 for the transferable
		// asset. Control assignments remain tracked and locked without being
		// merged into the spendable asset balance.
		if state.Reference.AssignmentType != 4000 {
			allocationAssetName.Type = "control"
		}
		amount := state.State.Amount
		if !descriptor.Fungible {
			amount = 1
		}
		var carrierInternalKey, tapretRoot []byte
		if state.CarrierBinding.CommitmentMethod == "tapret1st" {
			carrierInternalKey = append([]byte(nil), state.CarrierBinding.InternalKey[:]...)
			tapretRoot = append([]byte(nil), state.CarrierBinding.TapretRoot[:]...)
		}
		witnessTxID := ""
		if state.WitnessTxID != ([32]byte{}) {
			witnessTxID = formatTxID(state.WitnessTxID)
		}
		allocations = append(allocations, ValidatedAllocation{
			OutPoint:           formatOutpoint(state.Outpoint),
			AssetName:          allocationAssetName,
			Amount:             indexer.Decimal{Precision: int(metadata.Precision), Value: new(big.Int).SetUint64(amount)},
			OperationID:        hex.EncodeToString(state.Reference.OperationID[:]),
			AssignmentType:     uint32(state.Reference.AssignmentType),
			AssignmentIndex:    uint32(state.Reference.Index),
			StateClass:         state.State.Class,
			StateData:          append([]byte(nil), state.State.Data...),
			SealDisclosure:     append([]byte(nil), state.SealDisclosure...),
			SealBlinding:       state.SealBlinding,
			WitnessTxPtr:       state.WitnessTxPtr,
			CommitmentMethod:   state.CarrierBinding.CommitmentMethod,
			CarrierInternalKey: carrierInternalKey,
			TapretRoot:         tapretRoot,
			TapretProof:        append([]byte(nil), state.CarrierBinding.TapretProof...),
			WitnessTxID:        witnessTxID,
		})
		if state.WitnessPrepared {
			outpoint := formatOutpoint(state.Outpoint)
			outputs[outpoint] = &BitcoinUTXO{
				OutPoint: outpoint, Value: state.OutputValue,
				PkScript: append([]byte(nil), state.OutputScript...),
			}
		}
	}
	sort.Slice(allocations, func(i, j int) bool {
		if allocations[i].OutPoint != allocations[j].OutPoint {
			return allocations[i].OutPoint < allocations[j].OutPoint
		}
		if allocations[i].OperationID != allocations[j].OperationID {
			return allocations[i].OperationID < allocations[j].OperationID
		}
		return allocations[i].AssignmentIndex < allocations[j].AssignmentIndex
	})
	stateHash, err := hashAllocations(container.ContractID, container.SchemaID, allocations)
	if err != nil {
		return nil, nil, nil, err
	}
	consignmentHash := sha256.Sum256(raw)
	status := "valid"
	if prepared {
		status = "prepared"
	}
	witnessTxIDs := make([]string, 0, len(report.PreparedWitnessTxIDs))
	for _, txID := range report.PreparedWitnessTxIDs {
		witnessTxIDs = append(witnessTxIDs, formatTxID(txID))
	}
	return &ValidationReceipt{
		Version: 1, EngineBuildID: NativeEngineBuildID,
		ConsignmentHash: hex.EncodeToString(consignmentHash[:]), ContractID: container.ContractID, SchemaID: container.SchemaID,
		TransferID: container.Armor.ID, StateHash: stateHash, Allocations: allocations, Status: status,
	}, outputs, witnessTxIDs, nil
}

type coreEvidenceResolver struct {
	ctx      context.Context
	provider BitcoinEvidenceProvider
}

func (r coreEvidenceResolver) ResolveRGB11Witness(txid [32]byte) (coreconsignment.WitnessEvidence, error) {
	if err := r.ctx.Err(); err != nil {
		return coreconsignment.WitnessEvidence{}, err
	}
	if r.provider == nil {
		return coreconsignment.WitnessEvidence{}, ErrConsensusValidatorUnavailable
	}
	text := formatTxID(txid)
	raw, err := r.provider.GetRawTx(text)
	if err != nil {
		return coreconsignment.WitnessEvidence{}, err
	}
	status, err := r.provider.GetTxStatus(text)
	if err != nil {
		return coreconsignment.WitnessEvidence{}, err
	}
	result := coreconsignment.WitnessEvidence{RawTx: raw}
	if status != nil && status.Confirmed {
		result.State = coreconsignment.WitnessMined
		if status.BlockHeight >= 0 {
			result.BlockHeight = uint32(status.BlockHeight)
		}
		result.BlockHash = status.BlockHash
	} else if status != nil && status.InMempool {
		result.State = coreconsignment.WitnessTentative
	} else {
		result.State = coreconsignment.WitnessUnknown
	}
	return result, nil
}

func (r coreEvidenceResolver) ResolveRGB11Outpoint(outpoint coreconsignment.Outpoint) (coreconsignment.OutpointEvidence, error) {
	if err := r.ctx.Err(); err != nil {
		return coreconsignment.OutpointEvidence{}, err
	}
	if r.provider == nil {
		return coreconsignment.OutpointEvidence{}, ErrConsensusValidatorUnavailable
	}
	text := formatOutpoint(outpoint)
	outspend, err := r.provider.GetOutspend(text)
	if err != nil || outspend == nil {
		return coreconsignment.OutpointEvidence{}, err
	}
	result := coreconsignment.OutpointEvidence{Known: true}
	if outspend.Spent {
		result.Exists, result.Spent = true, true
		if outspend.SpendingTx != "" {
			spending, parseErr := chainhash.NewHashFromStr(outspend.SpendingTx)
			if parseErr != nil {
				return coreconsignment.OutpointEvidence{}, parseErr
			}
			spendingID := [32]byte(*spending)
			result.SpendingTxID = &spendingID
		}
		return result, nil
	}
	utxo, err := r.provider.GetUTXO(text)
	if err != nil || utxo == nil {
		return coreconsignment.OutpointEvidence{}, coreconsignment.ErrOutpointUnknown
	}
	result.Exists = true
	return result, nil
}

func formatTxID(txid [32]byte) string {
	hash := chainhash.Hash(txid)
	return hash.String()
}

func formatOutpoint(outpoint coreconsignment.Outpoint) string {
	return formatTxID(outpoint.TxID) + ":" + strconv.FormatUint(uint64(outpoint.Vout), 10)
}

func hashAllocations(contractID, schemaID string, allocations []ValidatedAllocation) ([32]byte, error) {
	var buf bytes.Buffer
	e := strict.NewEncoder(&buf)
	if err := encodeText(e, contractID); err != nil {
		return [32]byte{}, err
	}
	if err := encodeText(e, schemaID); err != nil {
		return [32]byte{}, err
	}
	if err := e.Length(uint64(len(allocations)), rgb11StoreMaxRecords); err != nil {
		return [32]byte{}, err
	}
	for index := range allocations {
		if err := encodeValidatedAllocation(e, &allocations[index]); err != nil {
			return [32]byte{}, err
		}
	}
	return consensus.StrictValueHash(buf.Bytes()), nil
}

var _ ConsensusValidator = (*NativeConsensusValidator)(nil)
var _ PreparedConsensusValidator = (*NativeConsensusValidator)(nil)
