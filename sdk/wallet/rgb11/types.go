// Package rgb11wallet adapts the standalone RGB11 engine to SAT20 Wallet SDK
// data structures. It does not define a parallel asset or balance model.
package rgb11wallet

import (
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	"github.com/sat20-labs/rgb11/seals"
)

const (
	Protocol          = "rgb11"
	LockReasonRGB     = "rgb"
	LockReasonPending = "pending-rgb"

	// DefaultFingerprintLength is the registry's initial collision-resistant
	// contract suffix. A server-side registry may extend an individual suffix
	// to 10, 12, 14, or 16 characters on collision without changing its prefix.
	DefaultFingerprintLength = 8
	MaxFingerprintLength     = 16
)

var (
	ErrInvalidRGB11Asset   = errors.New("invalid RGB11 asset")
	ErrRGB11Inconsistent   = errors.New("RGB11 wallet state is inconsistent")
	ErrRGB11STPUnavailable = errors.New("RGB11 is L1-only until full STP support is available")
)

// NormalizeTicker produces the deterministic human-readable portion of a
// SatoshiNet RGB11 asset name. It intentionally accepts arbitrary issuer
// metadata, but never lets that metadata become an unqualified asset key.
func NormalizeTicker(ticker string) string {
	var normalized strings.Builder
	lastDash := false
	for index := 0; index < len(ticker); index++ {
		char := ticker[index]
		if char >= 'A' && char <= 'Z' {
			char += 'a' - 'A'
		}
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			normalized.WriteByte(char)
			lastDash = false
			continue
		}
		if normalized.Len() > 0 && !lastDash {
			normalized.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(normalized.String(), "-")
	if result == "" {
		return "asset"
	}
	return result
}

// ContractFingerprint binds the display-safe name to canonical RGB contract
// bytes. The raw contract id never becomes the SAT20 AssetName ticker.
func ContractFingerprint(contractID string, length int) (string, error) {
	contract, err := consensus.ParseContractID(contractID)
	if err != nil {
		return "", err
	}
	if length == 0 {
		length = DefaultFingerprintLength
	}
	if length < DefaultFingerprintLength || length > MaxFingerprintLength || length%2 != 0 {
		return "", ErrInvalidRGB11Asset
	}
	payload := make([]byte, 0, len("satoshinet:rgb11:")+len(contract))
	payload = append(payload, "satoshinet:rgb11:"...)
	payload = append(payload, contract[:]...)
	sum := sha256.Sum256(payload)
	encoded := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:]))
	return encoded[:length], nil
}

// NewCanonicalAssetName creates the default asset key for a newly created or
// imported RGB11 contract: rgb11:<type>:<normalized ticker>@<fingerprint>.
func NewCanonicalAssetName(contractID, ticker, assetType string) (indexer.AssetName, error) {
	return NewCanonicalAssetNameWithFingerprintLength(
		contractID, ticker, assetType, DefaultFingerprintLength,
	)
}

// NewCanonicalAssetNameWithFingerprintLength lets the finalized registry
// extend only colliding fingerprint prefixes while preserving the canonical
// ticker and contract-derived prefix.
func NewCanonicalAssetNameWithFingerprintLength(contractID, ticker, assetType string,
	fingerprintLength int) (indexer.AssetName, error) {

	if assetType == "" {
		assetType = indexer.ASSET_TYPE_FT
	}
	if strings.Contains(assetType, ":") {
		return indexer.AssetName{}, ErrInvalidRGB11Asset
	}
	fingerprint, err := ContractFingerprint(contractID, fingerprintLength)
	if err != nil {
		return indexer.AssetName{}, err
	}
	return indexer.AssetName{
		Protocol: Protocol,
		Type:     assetType,
		Ticker:   NormalizeTicker(ticker) + "@" + fingerprint,
	}, nil
}

// CanonicalAssetNameMatches accepts every collision-extension length allowed
// by the RGB11 registry design and rejects names not derived from contractID.
func CanonicalAssetNameMatches(name indexer.AssetName, contractID, ticker string) bool {
	for length := DefaultFingerprintLength; length <= MaxFingerprintLength; length += 2 {
		expected, err := NewCanonicalAssetNameWithFingerprintLength(
			contractID, ticker, name.Type, length,
		)
		if err != nil {
			return false
		}
		if expected == name {
			return true
		}
	}
	return false
}

// DisplayTicker returns the safe UI alias. Until a server-side primary-asset
// registry has verified an issuer, the fingerprint remains visible.
func DisplayTicker(ticker, fingerprint string, primaryVerified bool) string {
	normalized := NormalizeTicker(ticker)
	if primaryVerified {
		if display := strings.TrimSpace(ticker); display != "" {
			return display
		}
		return normalized
	}
	if fingerprint == "" {
		return normalized
	}
	return normalized + "@" + fingerprint
}

type TickerExt struct {
	AssetName        indexer.AssetName `json:"asset_name"`
	Ticker           string            `json:"ticker,omitempty"`
	CanonicalName    string            `json:"canonical_name,omitempty"`
	NormalizedTicker string            `json:"normalized_ticker,omitempty"`
	Fingerprint      string            `json:"fingerprint,omitempty"`
	DisplayTicker    string            `json:"display_ticker,omitempty"`
	PrimaryVerified  bool              `json:"primary_verified,omitempty"`
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

type ReceiveKey struct {
	Version        uint8  `json:"version"`
	RequestID      string `json:"request_id"`
	ScopeIndex     uint32 `json:"scope_index"`
	Change         uint32 `json:"change"`
	Index          uint32 `json:"index"`
	LogicalAddress string `json:"logical_address"`
	WitnessScript  []byte `json:"witness_script"`
	InternalPubKey []byte `json:"internal_pubkey"`
}

type ReceiveReservation struct {
	Version   uint8  `json:"version"`
	RequestID string `json:"request_id"`
	OutPoint  string `json:"outpoint"`
	Expiry    int64  `json:"expiry"`
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

	// Address-mode fields are local lifecycle metadata. They are deliberately
	// not duplicated in DKVS mailbox payloads: sender/receiver accounts and the
	// transfer identifier are encoded by the mailbox key, while txid/vout and
	// asset data are obtained from the encrypted consignment.
	AddressMode             bool   `json:"address_mode,omitempty"`
	AddressMessageID        string `json:"address_message_id,omitempty"`
	SenderAccountID         string `json:"sender_account_id,omitempty"`
	ReceiverAccountID       string `json:"receiver_account_id,omitempty"`
	ReceiverAddress         string `json:"receiver_address,omitempty"`
	ReceiveCapabilityKey    string `json:"receive_capability_key,omitempty"`
	ReceiveCapabilityHash   string `json:"receive_capability_hash,omitempty"`
	DeliveryMode            string `json:"delivery_mode,omitempty"`
	DeliveryObjectID        string `json:"delivery_object_id,omitempty"`
	DeliveryRecordKey       string `json:"delivery_record_key,omitempty"`
	DeliveryRecordHash      string `json:"delivery_record_hash,omitempty"`
	DeliveryTemporary       bool   `json:"delivery_temporary,omitempty"`
	DeliveryExpiryHeight    uint64 `json:"delivery_expiry_height,omitempty"`
	DeliveryTTL             uint64 `json:"delivery_ttl,omitempty"`
	DeliveryAcknowledged    bool   `json:"delivery_acknowledged,omitempty"`
	DeliveryCacheCompacted  bool   `json:"delivery_cache_compacted,omitempty"`
	SyntheticInvoiceRemoved bool   `json:"synthetic_invoice_removed,omitempty"`
}

// PendingTransfer is private wallet state. Seal reveals and signed transaction
// bytes never enter the public relay record or wallet head payload.
type PendingTransfer struct {
	State                TransferState          `json:"state"`
	RecipientConsignment []byte                 `json:"-"`
	LocalConsignment     []byte                 `json:"-"`
	RecipientObjectHash  string                 `json:"-"`
	LocalObjectHash      string                 `json:"-"`
	SignedTx             []byte                 `json:"-"`
	SignedPSBT           []byte                 `json:"-"`
	ChangeSeals          []seals.GraphBlindSeal `json:"-"`
	CreatedAt            int64                  `json:"created_at"`
}

type OutputView struct {
	Output *indexer.TxOutput                      `json:"output"`
	Proofs map[indexer.AssetName]*AllocationProof `json:"proofs"`
}
