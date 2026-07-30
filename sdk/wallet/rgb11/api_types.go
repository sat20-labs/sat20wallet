package rgb11wallet

import (
	"encoding/json"
	"errors"
	"fmt"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/operations"
	"github.com/sat20-labs/rgb11/rejectlist"
	coresync "github.com/sat20-labs/rgb11/sync"
	"strconv"
	"strings"
)

var ErrRGB11Rejected = errors.New("RGB11 allocation rejected by issuer policy")

// RGB11Output is the serializable projection view exposed to UI clients.
// Internal TxOutput offset maps use structured keys and are intentionally not
// part of this API.
type RGB11Output struct {
	OutPointStr string           `json:"OutPointStr"`
	Assets      indexer.TxAssets `json:"Assets"`
}

type RGB11TickerInfo struct {
	*indexer.TickerInfo
	Ticker        string `json:"ticker"`
	CanonicalName string `json:"canonical_name"`
	ContractID    string `json:"contract_id"`
	Fingerprint   string `json:"fingerprint,omitempty"`
	Verified      bool   `json:"verified"`
}

// RGB11State exposes existing SAT20 assets plus RGB-only proof sidecars.
// Assets is rebuilt from Outputs and is never a second writable balance ledger.
type RGB11State struct {
	Initialized       bool               `json:"initialized"`
	SyncStatus        string             `json:"sync_status"`
	ConsistencyStatus string             `json:"consistency_status"`
	BackupStatus      string             `json:"backup_status"`
	BackupEnabled     bool               `json:"backup_enabled"`
	BackupMode        string             `json:"backup_mode,omitempty"`
	BackupRetention   uint64             `json:"backup_retention_ms,omitempty"`
	TickerInfos       []*RGB11TickerInfo `json:"ticker_infos"`
	Assets            indexer.TxAssets   `json:"assets"`
	AvailableAssets   indexer.TxAssets   `json:"available_assets"`
	PendingAssets     indexer.TxAssets   `json:"pending_assets"`
	Outputs           []*RGB11Output     `json:"outputs"`
	Proofs            []*AllocationProof `json:"proofs"`
	Transfers         []*TransferState   `json:"transfers"`
}

type RGB11IssueRequest struct {
	Schema           string   `json:"schema"`
	Ticker           string   `json:"ticker,omitempty"`
	Name             string   `json:"name"`
	Details          string   `json:"details,omitempty"`
	Precision        uint8    `json:"precision"`
	Terms            string   `json:"terms,omitempty"`
	Amounts          []uint64 `json:"amounts"`
	InflationAmounts []uint64 `json:"inflation_amounts,omitempty"`
	RejectListURL    string   `json:"reject_list_url,omitempty"`
	MinConfirmations int64    `json:"min_confirmations,omitempty"`
}

// UnmarshalJSON accepts atomic u64 amounts as either JSON numbers or decimal
// strings. PWA callers use strings so values above JavaScript's safe-integer
// range remain exact.
func (r *RGB11IssueRequest) UnmarshalJSON(data []byte) error {
	type wireRequest struct {
		Schema           string            `json:"schema"`
		Ticker           string            `json:"ticker"`
		Name             string            `json:"name"`
		Details          string            `json:"details"`
		Precision        uint8             `json:"precision"`
		Terms            string            `json:"terms"`
		Amounts          []json.RawMessage `json:"amounts"`
		InflationAmounts []json.RawMessage `json:"inflation_amounts"`
		RejectListURL    string            `json:"reject_list_url"`
		MinConfirmations int64             `json:"min_confirmations"`
	}
	var wire wireRequest
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	amounts, err := parseRGB11AtomicAmounts(wire.Amounts)
	if err != nil {
		return err
	}
	inflation, err := parseRGB11AtomicAmounts(wire.InflationAmounts)
	if err != nil {
		return err
	}
	*r = RGB11IssueRequest{
		Schema: wire.Schema, Ticker: wire.Ticker, Name: wire.Name, Details: wire.Details,
		Precision: wire.Precision, Terms: wire.Terms, Amounts: amounts, InflationAmounts: inflation,
		RejectListURL: wire.RejectListURL, MinConfirmations: wire.MinConfirmations,
	}
	return nil
}

func parseRGB11AtomicAmounts(values []json.RawMessage) ([]uint64, error) {
	result := make([]uint64, 0, len(values))
	for _, raw := range values {
		text := strings.TrimSpace(string(raw))
		if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
			var decoded string
			if err := json.Unmarshal(raw, &decoded); err != nil {
				return nil, err
			}
			text = decoded
		}
		amount, err := strconv.ParseUint(text, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid RGB11 atomic amount %q: %w", text, err)
		}
		result = append(result, amount)
	}
	return result, nil
}

type RGB11IssueResult struct {
	ContractID                string             `json:"contract_id"`
	SchemaID                  string             `json:"schema_id"`
	AssetName                 indexer.AssetName  `json:"asset_name"`
	Armor                     string             `json:"armor"`
	ContractConsignmentBase64 string             `json:"contract_consignment_base64"`
	OutPoints                 []string           `json:"outpoints"`
	Receipt                   *ValidationReceipt `json:"receipt"`
	Projected                 int                `json:"projected"`
}

type RGB11ImportResult struct {
	ContractID string             `json:"contract_id"`
	SchemaID   string             `json:"schema_id"`
	AssetName  indexer.AssetName  `json:"asset_name"`
	Receipt    *ValidationReceipt `json:"receipt"`
	Projected  int                `json:"projected"`
}

// RGB11RejectListProvider makes the network policy injectable for deterministic
// wallet tests. The default implementation permits plain HTTP only on loopback
// while the wallet is configured for regtest.
type RGB11RejectListProvider interface {
	Fetch(string) (rejectlist.List, error)
}

type RGB11RejectListViolation struct {
	Checked  operations.Opout
	Rejected operations.Opout
}

func (e *RGB11RejectListViolation) Error() string {
	if e == nil {
		return ErrRGB11Rejected.Error()
	}
	return fmt.Sprintf("%s: checked %s, rejected ancestor %s", ErrRGB11Rejected, e.Checked, e.Rejected)
}

func (e *RGB11RejectListViolation) Unwrap() error { return ErrRGB11Rejected }

type RGB11InvoiceRequest struct {
	Mode               string   `json:"mode,omitempty"`
	TransportMode      string   `json:"transport_mode,omitempty"`
	TransportEndpoints []string `json:"transport_endpoints,omitempty"`
	ContractID         string   `json:"contract_id"`
	SchemaID           string   `json:"schema_id"`
	AmountRaw          string   `json:"amount_raw"`
	AssignmentName     string   `json:"assignment_name"`
	Expiry             int64    `json:"expiry"`
	WitnessVout        uint32   `json:"witness_vout"`
}

type RGB11SendRequest struct {
	Invoice          string   `json:"invoice,omitempty"`
	Invoices         []string `json:"invoices,omitempty"`
	ContractID       string   `json:"contract_id,omitempty"`
	AmountRaw        string   `json:"amount_raw,omitempty"`
	FeeRate          int64    `json:"fee_rate"`
	MinConfirmations uint8    `json:"min_confirmations"`
}

type RGB11PreparedTransfer struct {
	State                      *TransferState   `json:"state"`
	States                     []*TransferState `json:"states,omitempty"`
	RecipientConsignment       string           `json:"recipient_consignment"`
	RecipientConsignmentBase64 string           `json:"recipient_consignment_base64"`
	SignedPSBT                 string           `json:"signed_psbt"`
	TxID                       string           `json:"txid"`
}

type RGB11ProxyDeliveryResult struct {
	TransferIDs []string `json:"transfer_ids"`
	Endpoints   []string `json:"endpoints"`
	TxID        string   `json:"txid"`
}

type RGB11ProxyAckResult struct {
	TransferID string `json:"transfer_id"`
	Available  bool   `json:"available"`
	Accepted   bool   `json:"accepted"`
	Endpoint   string `json:"endpoint,omitempty"`
}

type RGB11ProxyReceiveResult struct {
	RequestID string             `json:"request_id"`
	Endpoint  string             `json:"endpoint"`
	TxID      string             `json:"txid"`
	Vout      *uint32            `json:"vout,omitempty"`
	Receipt   *ValidationReceipt `json:"receipt"`
	AckPosted bool               `json:"ack_posted"`
}

type RGB11RefreshResult struct {
	Settled      int      `json:"settled"`
	Pending      int      `json:"pending"`
	Reorged      int      `json:"reorged"`
	Conflicted   int      `json:"conflicted"`
	Unresolved   int      `json:"unresolved"`
	Inconsistent []string `json:"inconsistent,omitempty"`
}
type RGB11AddressMailboxSyncResult struct {
	Scanned      int      `json:"scanned"`
	Received     int      `json:"received"`
	ACKs         int      `json:"acks"`
	WaitingTx    int      `json:"waiting_tx"`
	AlreadyDone  int      `json:"already_done"`
	Invalid      int      `json:"invalid"`
	ErrorDetails []string `json:"error_details,omitempty"`
}

type RGB11ReceiveCapability struct {
	Version uint8 `json:"version"`
	Flags   uint8 `json:"flags"`
}

type RGB11AddressEndpoint struct {
	AccountID string `json:"account_id"`
	Address   string `json:"address"`
	MailboxID string `json:"mailbox_id"`

	CompressedPubKey []byte `json:"compressed_pubkey"`
	PkScript         []byte `json:"pk_script"`

	CapabilityFlags      uint8  `json:"capability_flags"`
	CapabilityRecordKey  string `json:"capability_record_key"`
	CapabilityRecordHash string `json:"capability_record_hash"`
	Temporary            bool   `json:"temporary"`
	ExpiryHeight         uint64 `json:"expiry_height"`
	TTL                  uint64 `json:"ttl"`
}

type RGB11AddressDeliveryResult struct {
	TransferID string `json:"transfer_id"`
	Mode       string `json:"mode"`
	RecordKey  string `json:"record_key"`
	RecordHash string `json:"record_hash"`
	ObjectID   string `json:"object_id,omitempty"`
	Temporary  bool   `json:"temporary"`
	TxID       string `json:"txid,omitempty"`
}

type RGB11AddressACK struct {
	Status uint8  `json:"status"`
	Code   uint16 `json:"code"`
}

type RGB11AddressSendRequest struct {
	ReceiverAddress  string            `json:"receiver_address"`
	AssetName        indexer.AssetName `json:"asset_name"`
	AmountRaw        string            `json:"amount_raw"`
	FeeRate          int64             `json:"fee_rate"`
	MinConfirmations uint8             `json:"min_confirmations"`
	Expiry           int64             `json:"expiry,omitempty"`
}

type RGB11AutoBackupPolicy struct {
	Version      uint32 `json:"version"`
	Enabled      bool   `json:"enabled"`
	TTL          uint64 `json:"ttl"`
	ExpiryHeight uint64 `json:"expiry_height,omitempty"`
}

type RGB11ActivationResult struct {
	Found      bool                 `json:"found"`
	Restored   bool                 `json:"restored"`
	AutoBackup bool                 `json:"auto_backup"`
	Head       *coresync.WalletHead `json:"head,omitempty"`
}

type RGB11WalletSnapshot struct {
	Version           uint32                `json:"version"`
	WalletID          string                `json:"wallet_id"`
	AccountIndex      uint32                `json:"account_index"`
	EngineBuildID     string                `json:"engine_build_id"`
	ProjectionRecords []SnapshotRecord      `json:"projection_records"`
	EngineRecords     []SnapshotRecord      `json:"engine_records"`
	TickerInfos       []*indexer.TickerInfo `json:"ticker_infos"`
}

type RGB11EncryptedSnapshot struct {
	Version     uint32   `json:"version"`
	WalletID    string   `json:"wallet_id"`
	OperationID [32]byte `json:"operation_id"`
	Ciphertext  []byte   `json:"ciphertext"`
}
