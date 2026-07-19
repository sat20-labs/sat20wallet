package rgb11wallet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
)

var (
	ErrConsensusValidatorUnavailable = errors.New("RGB11 consensus validator is unavailable")
	ErrValidationReceipt             = errors.New("invalid RGB11 validation receipt")
)

// ValidatedAllocation is an allocation produced by the consensus validator,
// not by the Indexer or UI.
type ValidatedAllocation struct {
	OutPoint           string            `json:"outpoint"`
	AssetName          indexer.AssetName `json:"asset_name"`
	Amount             indexer.Decimal   `json:"amount"`
	OperationID        string            `json:"operation_id"`
	AssignmentType     uint32            `json:"assignment_type"`
	AssignmentIndex    uint32            `json:"assignment_index"`
	StateClass         string            `json:"state_class"`
	StateData          []byte            `json:"state_data,omitempty"`
	SealDisclosure     []byte            `json:"seal_disclosure"`
	SealBlinding       uint64            `json:"seal_blinding"`
	WitnessTxPtr       bool              `json:"witness_tx_ptr"`
	CommitmentMethod   string            `json:"commitment_method,omitempty"`
	CarrierInternalKey []byte            `json:"carrier_internal_key,omitempty"`
	TapretRoot         []byte            `json:"tapret_root,omitempty"`
	TapretProof        []byte            `json:"tapret_proof,omitempty"`
}

// ValidationReceipt binds an accepted consignment to the exact allocations
// the Go consensus engine validated. ProjectionStore requires this receipt,
// so a caller cannot turn an armored transport blob into wallet balance by
// setting proof.Status = "valid".
type ValidationReceipt struct {
	Version         uint32                `json:"version"`
	EngineBuildID   string                `json:"engine_build_id"`
	ConsignmentHash string                `json:"consignment_hash"`
	ContractID      string                `json:"contract_id"`
	SchemaID        string                `json:"schema_id"`
	TransferID      string                `json:"transfer_id,omitempty"`
	StateHash       [32]byte              `json:"state_hash"`
	ValidatedAt     int64                 `json:"validated_at"`
	Allocations     []ValidatedAllocation `json:"allocations"`
	Status          string                `json:"status"`
}

type ConsensusValidator interface {
	ValidateConsignment(ctx context.Context, raw []byte, evidence BitcoinEvidenceProvider) (*ValidationReceipt, error)
}

func ValidateWith(ctx context.Context, validator ConsensusValidator, raw []byte, evidence BitcoinEvidenceProvider) (*ValidationReceipt, error) {
	if validator == nil || evidence == nil {
		return nil, ErrConsensusValidatorUnavailable
	}
	if len(raw) == 0 {
		return nil, ErrValidationReceipt
	}
	receipt, err := validator.ValidateConsignment(ctx, raw, evidence)
	if err != nil {
		return nil, err
	}
	want := sha256.Sum256(raw)
	if err := receipt.validate(hex.EncodeToString(want[:])); err != nil {
		return nil, err
	}
	return receipt, nil
}

func (r *ValidationReceipt) validate(expectedHash string) error {
	if r == nil || r.Version != 1 || r.Status != "valid" || r.EngineBuildID == "" ||
		r.ConsignmentHash != expectedHash || r.ContractID == "" || r.SchemaID == "" {
		return ErrValidationReceipt
	}
	if r.ValidatedAt == 0 {
		r.ValidatedAt = time.Now().Unix()
	}
	for _, allocation := range r.Allocations {
		if allocation.OutPoint == "" || allocation.AssetName.Protocol != Protocol ||
			allocation.AssetName.Ticker == "" || allocation.Amount.Sign() < 0 ||
			allocation.OperationID == "" || allocation.AssignmentType > 0xffff ||
			len(allocation.SealDisclosure) == 0 ||
			(allocation.StateClass != "declarative" && allocation.StateClass != "fungible" && allocation.StateClass != "structured") {
			return fmt.Errorf("%w: invalid allocation", ErrValidationReceipt)
		}
	}
	return nil
}

func (r *ValidationReceipt) Hash() (string, error) {
	encoded, err := encode(r)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}
