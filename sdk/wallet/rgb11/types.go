// Package rgb11wallet adapts the standalone RGB11 engine to SAT20 Wallet SDK
// data structures. It does not define a parallel asset or balance model.
package rgb11wallet

import (
	"errors"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/assetid"
	"github.com/sat20-labs/rgb11/seals"
)

const (
	Protocol          = "rgb11"
	LockReasonRGB     = "rgb"
	LockReasonPending = "pending-rgb"
)

var ErrInvalidRGB11Asset = errors.New("invalid RGB11 asset")

func NewAssetName(officialAssetID, assetType string) (indexer.AssetName, error) {
	ticker, err := assetid.Ticker(officialAssetID)
	if err != nil {
		return indexer.AssetName{}, err
	}
	if assetType == "" {
		assetType = indexer.ASSET_TYPE_FT
	}
	if strings.Contains(assetType, ":") {
		return indexer.AssetName{}, ErrInvalidRGB11Asset
	}
	return indexer.AssetName{Protocol: Protocol, Type: assetType, Ticker: ticker}, nil
}

func OfficialAssetID(name indexer.AssetName) (string, error) {
	if name.Protocol != Protocol || name.Ticker == "" || strings.Contains(name.Ticker, ":") {
		return "", ErrInvalidRGB11Asset
	}
	return assetid.AssetID(name.Ticker)
}

func NewAssetInfo(officialAssetID, assetType string, amount *indexer.Decimal) (*indexer.AssetInfo, error) {
	if amount == nil || amount.Sign() < 0 {
		return nil, ErrInvalidRGB11Asset
	}
	name, err := NewAssetName(officialAssetID, assetType)
	if err != nil {
		return nil, err
	}
	return &indexer.AssetInfo{Name: name, Amount: *amount.Clone(), BindingSat: 0}, nil
}

type TickerExt struct {
	AssetName        indexer.AssetName `json:"asset_name"`
	Ticker           string            `json:"ticker,omitempty"`
	OriginalAssetID  string            `json:"original_asset_id"`
	AssetIDBytes     []byte            `json:"asset_id_bytes"`
	SchemaID         string            `json:"schema_id"`
	ContractID       string            `json:"contract_id"`
	ContractHash     string            `json:"contract_hash"`
	RejectListURL    string            `json:"reject_list_url,omitempty"`
	ControlMode      string            `json:"control_mode"`
	IssuerIdentity   string            `json:"issuer_identity,omitempty"`
	PolicyAdapterID  string            `json:"policy_adapter_id,omitempty"`
	STPAllowed       bool              `json:"stp_allowed"`
	ValidationStatus string            `json:"validation_status"`
}

type CarrierBinding struct {
	DerivationIndex  uint32 `json:"derivation_index"`
	LogicalAddress   string `json:"logical_address"`
	OutPoint         string `json:"outpoint"`
	ActualPkScript   []byte `json:"actual_pk_script"`
	ActualOutputKey  []byte `json:"actual_output_key"`
	InternalPubKey   []byte `json:"internal_pubkey"`
	TapretRoot       []byte `json:"tapret_root,omitempty"`
	TapretProof      []byte `json:"tapret_proof,omitempty"`
	CommitmentMethod string `json:"commitment_method"`
}

type AllocationProof struct {
	OutPoint        string            `json:"outpoint"`
	AssetName       indexer.AssetName `json:"asset_name"`
	OperationID     string            `json:"operation_id"`
	AssignmentType  uint32            `json:"assignment_type"`
	AssignmentIndex uint32            `json:"assignment_index"`
	StateClass      string            `json:"state_class"`
	StateData       []byte            `json:"state_data,omitempty"`
	SealCommitment  string            `json:"seal_commitment"`
	SealDisclosure  []byte            `json:"seal_disclosure"`
	ConsignmentHash string            `json:"consignment_hash"`
	ValidationHash  string            `json:"validation_hash"`
	WitnessTxID     string            `json:"witness_txid"`
	CarrierBinding  *CarrierBinding   `json:"carrier_binding,omitempty"`
	Status          string            `json:"status"`
	Confirmations   int64             `json:"confirmations"`
	PolicyStatus    string            `json:"policy_status,omitempty"`
	PolicyReason    string            `json:"policy_reason,omitempty"`
}

type TransferState struct {
	TransferID       string            `json:"transfer_id"`
	BatchID          string            `json:"batch_id,omitempty"`
	BatchTransferIDs []string          `json:"batch_transfer_ids,omitempty"`
	BatchSize        int               `json:"batch_size,omitempty"`
	RecipientVout    uint32            `json:"recipient_vout,omitempty"`
	TransportMode    string            `json:"transport_mode,omitempty"`
	Direction        string            `json:"direction"`
	Asset            indexer.AssetInfo `json:"asset"`
	RecipientID      string            `json:"recipient_id"`
	Invoice          string            `json:"invoice"`
	InputOutPoints   []string          `json:"input_outpoints"`
	OutputOutPoints  []string          `json:"output_outpoints"`
	MinConfirmations uint8             `json:"min_confirmations"`
	Expiry           int64             `json:"expiry"`
	ConsignmentHash  string            `json:"consignment_hash"`
	WitnessTxID      string            `json:"witness_txid"`
	AckStatus        string            `json:"ack_status"`
	Status           string            `json:"status"`
	RejectReason     string            `json:"reject_reason,omitempty"`
	RejectedOpouts   []string          `json:"rejected_opouts,omitempty"`
	RelayRecordKey   string            `json:"relay_record_key"`
	AckRecordKey     string            `json:"ack_record_key"`
	RelayDurability  string            `json:"relay_durability"`
	RelayExpiry      int64             `json:"relay_expiry"`
	NetworkBackupRef string            `json:"network_backup_ref,omitempty"`
	ParentStateHash  string            `json:"parent_state_hash"`
	DKVSOperationID  string            `json:"dkvs_operation_id"`
}

// PendingTransfer is private wallet state. Seal reveals and signed transaction
// bytes never enter the public relay record or wallet head payload.
type PendingTransfer struct {
	State                TransferState          `json:"state"`
	RecipientConsignment []byte                 `json:"-"`
	LocalConsignment     []byte                 `json:"-"`
	SignedTx             []byte                 `json:"-"`
	SignedPSBT           []byte                 `json:"-"`
	ChangeSeals          []seals.GraphBlindSeal `json:"-"`
	CreatedAt            int64                  `json:"created_at"`
}

type OutputView struct {
	Output *indexer.TxOutput                      `json:"output"`
	Proofs map[indexer.AssetName]*AllocationProof `json:"proofs"`
}
