// Package rgb11wallet adapts the standalone RGB11 engine to SAT20 Wallet SDK
// data structures. It does not define a parallel asset or balance model.
package rgb11wallet

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"slices"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/rgb11/seals"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const (
	Protocol = "rgb11"
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

// RGB11ContractIDHex returns the full immutable identity, separate from AssetName.
func RGB11ContractIDHex(contractID string) (string, error) {
	if id, err := consensus.IDFromHex(contractID); err == nil {
		return hex.EncodeToString(id[:]), nil
	}
	id, err := consensus.ParseContractID(contractID)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

// NewContractAssetKey derives an AssetName from committed genesis metadata.
// The full ID is not recoverable from the resulting short ticker.
func NewContractAssetKey(contractID, assetType, genesisTicker string) (indexer.AssetName, error) {
	id, err := RGB11ContractIDHex(contractID)
	if err != nil {
		return indexer.AssetName{}, err
	}
	if assetType == "" {
		assetType = indexer.ASSET_TYPE_FT
	}
	if !rgb11AssetType(assetType) {
		return indexer.AssetName{}, ErrInvalidRGB11Asset
	}
	ticker, err := dkvsindexer.RGB11NameTicker(genesisTicker, id)
	if err != nil {
		return indexer.AssetName{}, err
	}
	return indexer.AssetName{Protocol: Protocol, Type: assetType, Ticker: ticker}, nil
}

func rgb11AssetType(assetType string) bool {
	return assetType == indexer.ASSET_TYPE_FT || assetType == indexer.ASSET_TYPE_NFT || assetType == "control"
}

// IsCanonicalAssetName checks the representation only. Use checked contract
// data as well when establishing identity or authorizing a spend.
func IsCanonicalAssetName(name indexer.AssetName) bool {
	if name.Protocol != Protocol || !rgb11AssetType(name.Type) {
		return false
	}
	_, err := dkvsindexer.RGB11NameKey(name.Ticker)
	return err == nil
}

// ContractAssetKeyMatches compares the derived short name only. It cannot
// substitute for comparison of two full validated ContractIDs.
func ContractAssetKeyMatches(name indexer.AssetName, contractID, genesisTicker string) bool {
	expected, err := NewContractAssetKey(contractID, name.Type, genesisTicker)
	return err == nil && name == expected
}

// ContractAssetKeyFromContainer derives the exact name from the semantic
// genesis data. An empty assetType derives the primary allocation type.
func ContractAssetKeyFromContainer(container *coreconsignment.Container, assetType string) (indexer.AssetName, error) {
	if container == nil || !container.GenesisValid {
		return indexer.AssetName{}, ErrInvalidRGB11Asset
	}
	descriptor, err := schemas.ByKind(container.GenesisReport.Kind)
	if err != nil {
		return indexer.AssetName{}, err
	}
	primaryType := indexer.ASSET_TYPE_FT
	if !descriptor.Fungible {
		primaryType = indexer.ASSET_TYPE_NFT
	}
	if assetType == "" {
		assetType = primaryType
	}
	if assetType != primaryType && assetType != "control" {
		return indexer.AssetName{}, ErrInvalidRGB11Asset
	}
	genesis, ok := container.Value.Field("genesis")
	if !ok {
		return indexer.AssetName{}, ErrInvalidRGB11Asset
	}
	schema, types := container.SchemaValues()
	metadata, err := schemas.ExtractGenesisAssetMetadata(schema, types, genesis)
	if err != nil {
		return indexer.AssetName{}, err
	}
	return NewContractAssetKey(container.ContractID, assetType, metadata.Ticker)
}

type TickerExt struct {
	AssetName        indexer.AssetName `json:"asset_name"`
	Ticker           string            `json:"ticker,omitempty"`
	CanonicalName    string            `json:"canonical_name,omitempty"`
	NormalizedTicker string            `json:"normalized_ticker,omitempty"`
	GenesisAddress   string            `json:"genesis_address,omitempty"`
	GenesisOutpoint  string            `json:"genesis_outpoint,omitempty"`
	NamingStatus     string            `json:"naming_status,omitempty"`
	DisplayTicker    string            `json:"display_ticker,omitempty"`
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
	Version       uint8  `json:"version"`
	RequestID     string `json:"request_id"`
	OutPoint      string `json:"outpoint"`
	ReservationID string `json:"reservation_id,omitempty"`
	Expiry        int64  `json:"expiry"`
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
	ReceiveRequestID string            `json:"receive_request_id,omitempty"`
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
	DeliveryIssueHeight     uint64 `json:"delivery_issue_height,omitempty"`
	DeliveryTTL             uint64 `json:"delivery_ttl,omitempty"`
	DeliveryAcknowledged    bool   `json:"delivery_acknowledged,omitempty"`
	DeliveryCacheCompacted  bool   `json:"delivery_cache_compacted,omitempty"`
	SyntheticInvoiceRemoved bool   `json:"synthetic_invoice_removed,omitempty"`
}

// PendingTransfer is private wallet state. Seal reveals and signed transaction
// bytes never enter the public relay record or wallet head payload.
type PendingTransfer struct {
	State                TransferState `json:"state"`
	RecipientConsignment []byte        `json:"-"`
	LocalConsignment     []byte        `json:"-"`
	RecipientObjectHash  string        `json:"-"`
	LocalObjectHash      string        `json:"-"`
	// SignedTx holds the fixed unsigned transaction until the broadcast boundary.
	// SignedPSBT retains its original unsigned PSBT, including previous outputs.
	// ChannelSend.Signed additionally tracks the existing joint-signing phase.
	SignedTx         []byte                 `json:"-"`
	SignedPSBT       []byte                 `json:"-"`
	ChangeSeals      []seals.GraphBlindSeal `json:"-"`
	PreservedChanges []LocalChange          `json:"-"`
	ReservationID    string                 `json:"reservation_id,omitempty"`
	CreatedAt        int64                  `json:"created_at"`
	ChannelSend      *ChannelSendData       `json:"-"`
}

// LocalChange is the private consignment and seal disclosures of one contract.
// A preserved contract's bytes are kept in the content-addressed object store.
type LocalChange struct {
	ObjectHash  string
	Consignment []byte
	Seals       []seals.GraphBlindSeal
}

func (c *LocalChange) bindObject() error {
	if len(c.Consignment) == 0 {
		return ErrValidationReceipt
	}
	hash := sha256.Sum256(c.Consignment)
	object := hex.EncodeToString(hash[:])
	if c.ObjectHash != "" && c.ObjectHash != object {
		return ErrValidationReceipt
	}
	c.ObjectHash = object
	return nil
}

func (p *PendingTransfer) LocalChanges() []LocalChange {
	changes := []LocalChange{{
		ObjectHash: p.LocalObjectHash, Consignment: p.LocalConsignment, Seals: p.ChangeSeals,
	}}
	return append(changes, p.PreservedChanges...)
}

func (p *PendingTransfer) HasLocalObject(hash string) bool {
	if hash == "" {
		return false
	}
	if p.LocalObjectHash == hash {
		return true
	}
	for _, change := range p.PreservedChanges {
		if change.ObjectHash == hash {
			return true
		}
	}
	return false
}

func (p *PendingTransfer) LoadPreservedChanges(load func(string) ([]byte, error)) error {
	for i := range p.PreservedChanges {
		change := &p.PreservedChanges[i]
		raw, err := load(change.ObjectHash)
		if err != nil {
			return err
		}
		change.Consignment = raw
		if err := change.bindObject(); err != nil {
			return err
		}
	}
	return nil
}

func (p *PendingTransfer) SamePreservedChanges(other *PendingTransfer) bool {
	return slices.EqualFunc(p.PreservedChanges, other.PreservedChanges, func(a, b LocalChange) bool {
		return a.ObjectHash == b.ObjectHash && reflect.DeepEqual(a.Seals, b.Seals)
	})
}

// ChannelSendData keeps the existing STP signing request private until every
// recipient has acknowledged the proof. The Bitcoin txid is fixed before the
// peer is allowed to obtain our signatures.
type ChannelSendData struct {
	ChannelID     string
	WitnessScript []byte
	PeerPubKey    []byte
	Reason        string
	MoreData      []byte
	PayFeeByLocal bool
	Signed        bool
}

type OutputView struct {
	Output *indexer.TxOutput                      `json:"output"`
	Proofs map[indexer.AssetName]*AllocationProof `json:"proofs"`
}
