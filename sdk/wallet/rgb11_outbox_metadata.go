package wallet

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// These fixed errors deliberately do not wrap storage errors, record contents,
// paths or caller input. They are safe to pass through the ordinary WASM bridge.
var (
	ErrRGB11OutboxUnavailable      = errors.New("RGB11_OUTBOX_UNAVAILABLE")
	ErrRGB11OutboxInvalidID        = errors.New("RGB11_OUTBOX_INVALID_TRANSFER_ID")
	ErrRGB11OutboxTransferNotFound = errors.New("RGB11_OUTBOX_TRANSFER_NOT_FOUND")
	ErrRGB11OutboxReadFailed       = errors.New("RGB11_OUTBOX_READ_FAILED")
	ErrRGB11OutboxCorrupt          = errors.New("RGB11_OUTBOX_CORRUPT")
	ErrRGB11OutboxScopeChanged     = errors.New("RGB11_OUTBOX_SCOPE_CHANGED")
)

// RGB11TransferOutboxMetadata is a local observation, not a delivery receipt.
// Absence can mean either never queued or removed after acceptance. SourceCoreNode
// identifies the sending Core; it does not identify the receiving Core. No
// historical broadcast-attempt or acceptance-expiry conclusion can be drawn here.
type RGB11TransferOutboxMetadata struct {
	ReadStatus           string `json:"read_status"`
	OutboxExists         bool   `json:"outbox_exists"`
	ApplicationID        string `json:"application_id"`
	TransportMessageID   string `json:"transport_message_id,omitempty"`
	SenderMsgID          string `json:"sender_msg_id,omitempty"`
	Sender               string `json:"sender"`
	Recipient            string `json:"recipient"`
	SourceCoreNode       string `json:"source_core_node,omitempty"`
	GeneratedAtUnixMicro string `json:"generated_at_unix_micro,omitempty"`
	GeneratedAtUTC       string `json:"generated_at_utc,omitempty"`
}

func rgb11MetadataHex(value string, size int) ([]byte, bool) {
	if len(value) != size*2 || strings.ToLower(value) != value {
		return nil, false
	}
	raw, err := hex.DecodeString(value)
	return raw, err == nil && len(raw) == size
}

// Preserve the distinction between a storage failure and malformed projection
// bytes without exposing either error or changing the shared store loader.
type rgb11MetadataProjectionDB struct {
	indexer.KVDB
	readErr error
}

func (db *rgb11MetadataProjectionDB) Read(key []byte) ([]byte, error) {
	raw, err := db.KVDB.Read(key)
	db.readErr = err
	return raw, err
}

// GetRGB11TransferOutboxMetadata reads only the currently selected managed root's
// exact send reservation, projection and existing outbox. It neither initializes
// a manager/store nor calls a sync, bind, recovery or transport operation.
//
// Scope changes use channelIdentityMu -> rgbOperationMu -> mutex. Reservation
// publication uses rgbReservationMu -> mutex. Never hold mutex while acquiring
// the reservation gate, and never take messageSendMu (its owner may do network I/O).
// The reservation is fixed while one atomic KVDB.Read captures the outbox; an
// independent outbox delete afterwards does not invalidate this observation.
func (p *Manager) GetRGB11TransferOutboxMetadata(transferID string) (result *RGB11TransferOutboxMetadata, err error) {
	if p == nil {
		return nil, ErrRGB11OutboxUnavailable
	}
	applicationID, idErr := rgb11AddressMessageID(transferID)
	if idErr != nil || transferID != strings.TrimSpace(transferID) {
		return nil, ErrRGB11OutboxInvalidID
	}
	p.channelIdentityMu.RLock()
	defer p.channelIdentityMu.RUnlock()
	p.rgbOperationMu.RLock()
	defer p.rgbOperationMu.RUnlock()
	p.mutex.RLock()
	if p.cfg == nil || p.db == nil || p.wallet == nil || p.status == nil || p.rgbManager == nil ||
		p.accountProfile == nil || len(p.accountSecret) != 32 ||
		p.cfg.Chain != _chain || p.cfg.Env != _env || p.cfg.Mode != _mode ||
		p.status.CurrentAccount != 0 || p.wallet.GetSubAccount() != 0 {
		p.mutex.RUnlock()
		return nil, ErrRGB11OutboxUnavailable
	}
	info := p.walletInfoMap[p.status.CurrentWallet]
	internal, internalOK := p.wallet.(*InternalWallet)
	sender, accountErr := dkvsAccountID(p.wallet)
	if !internalOK || internal.netParamsL1 == nil || internal.netParamsL1.Net != GetChainParam().Net ||
		accountErr != nil || info == nil || info.Id != p.status.CurrentWallet || info.Wallet != p.wallet ||
		!p.isAccountManagementRootLocked(info) || sender != p.accountProfile.AccountID {
		p.mutex.RUnlock()
		return nil, ErrRGB11OutboxUnavailable
	}
	walletID, generation := p.status.CurrentWallet, p.accountGeneration
	database, selected, profile := p.db, p.wallet, p.accountProfile
	rootFingerprint := profile.RootFingerprint
	network, environment, mode, prefix := _chain, _env, _mode, GetDBKeyPrefix()
	p.mutex.RUnlock()
	p.rgbReservationMu.RLock()
	defer p.rgbReservationMu.RUnlock()
	defer func() {
		if result == nil {
			return
		}
		p.mutex.RLock()
		defer p.mutex.RUnlock()
		if p.db != database || p.wallet != selected || p.status == nil ||
			p.status.CurrentWallet != walletID || p.status.CurrentAccount != 0 ||
			p.accountGeneration != generation || len(p.accountSecret) != 32 ||
			p.accountProfile != profile || profile.AccountID != sender || profile.RootFingerprint != rootFingerprint ||
			p.cfg == nil || p.cfg.Chain != network || p.cfg.Env != environment || p.cfg.Mode != mode ||
			_chain != network || _env != environment || _mode != mode || GetDBKeyPrefix() != prefix {
			result, err = nil, ErrRGB11OutboxScopeChanged
		}
	}()

	scope := rgb11StorageScope(walletID, 0)
	identity := "send:" + transferID
	reservationID := rgb11TransferResvID(scope, identity)
	key := prefix + DB_KEY_RESV + RESV_TYPE_RGB11 + "-" + strconv.FormatInt(reservationID, 10)
	raw, readErr := database.Read([]byte(key))
	if errors.Is(readErr, indexer.ErrKeyNotFound) {
		return nil, ErrRGB11OutboxTransferNotFound
	}
	if readErr != nil {
		return nil, ErrRGB11OutboxReadFailed
	}
	var reservation RGB11TransferReservation
	if DecodeFromBytes(raw, &reservation) != nil || reservation.State == nil ||
		reservation.Id != reservationID || reservation.Scope != scope || reservation.Identity != identity ||
		reservation.WalletId.Id != walletID || reservation.WalletId.SubAccountId != 0 {
		return nil, ErrRGB11OutboxCorrupt
	}
	state := reservation.State
	if state.TransferID != transferID || state.Direction != "send" || !state.AddressMode ||
		state.TransportMode != RGB11AddressTransport || state.AddressMessageID != applicationID ||
		state.SenderAccountID != sender || state.Asset.Name.Protocol != rgb11wallet.Protocol {
		return nil, ErrRGB11OutboxCorrupt
	}
	if _, ok := rgb11MetadataHex(state.ReceiverAccountID, 32); !ok {
		return nil, ErrRGB11OutboxCorrupt
	}
	_, validContract := rgb11MetadataHex(state.Asset.Name.Ticker, 32)
	if !validContract || state.Asset.Name.Type != indexer.ASSET_TYPE_FT && state.Asset.Name.Type != indexer.ASSET_TYPE_NFT && state.Asset.Name.Type != "control" {
		return nil, ErrRGB11OutboxCorrupt
	}
	// This constructor only creates a local key view. No engine, bucket, scan or
	// recovery is initialized. The authoritative reservation and projection must
	// agree on the immutable asset/transport association before inspecting outbox.
	projectionDB := &rgb11MetadataProjectionDB{KVDB: database}
	projection := rgb11wallet.NewProjectionStore(projectionDB, nil)
	if projection.SetScope(scope) != nil {
		return nil, ErrRGB11OutboxCorrupt
	}
	projected, projectionErr := projection.LoadTransferState(transferID)
	if projectionErr != nil {
		if projectionDB.readErr != nil && !errors.Is(projectionDB.readErr, indexer.ErrKeyNotFound) {
			return nil, ErrRGB11OutboxReadFailed
		}
		return nil, ErrRGB11OutboxCorrupt
	}
	if projected.TransferID != transferID || projected.Direction != "send" ||
		projected.Asset.Name != state.Asset.Name || !projected.AddressMode ||
		projected.TransportMode != state.TransportMode || projected.AddressMessageID != applicationID ||
		projected.SenderAccountID != sender || projected.ReceiverAccountID != state.ReceiverAccountID {
		return nil, ErrRGB11OutboxCorrupt
	}
	result = &RGB11TransferOutboxMetadata{ReadStatus: "not_found", ApplicationID: applicationID, Sender: sender, Recipient: state.ReceiverAccountID}
	raw, readErr = database.Read([]byte(prefix + accountMessageOutboxPrefix + sender + "/" + applicationID))
	if errors.Is(readErr, indexer.ErrKeyNotFound) {
		return result, nil
	}
	if readErr != nil {
		return nil, ErrRGB11OutboxReadFailed
	}
	var outbox accountMessageOutboxRecord
	if DecodeFromBytes(raw, &outbox) != nil || outbox.Version != accountMessageOutboxVersion ||
		outbox.ApplicationID != applicationID || outbox.Kind != AccountMessageKindRGB11Consignment ||
		outbox.Direct.SenderAccount != sender || outbox.Recipient != state.ReceiverAccountID ||
		outbox.Direct.RecipientAccount != outbox.Recipient {
		return nil, ErrRGB11OutboxCorrupt
	}
	core, validCore := rgb11MetadataHex(outbox.SourceCoreNode, 33)
	if !validCore {
		return nil, ErrRGB11OutboxCorrupt
	}
	if _, keyErr := btcec.ParsePubKey(core); keyErr != nil {
		return nil, ErrRGB11OutboxCorrupt
	}
	messageID, validID := rgb11MetadataHex(outbox.Direct.MessageID, 16)
	if !validID {
		return nil, ErrRGB11OutboxCorrupt
	}
	generated := binary.BigEndian.Uint64(messageID[:8])
	// Bound conversion to positive RFC3339 years without truncating uint64.
	if generated == 0 || generated > 253402300799999999 {
		return nil, ErrRGB11OutboxCorrupt
	}
	public, _ := hex.DecodeString(sender)
	pubkey, pubErr := schnorr.ParsePubKey(public)
	signature, sigErr := schnorr.ParseSignature(outbox.Direct.SenderSignature)
	digest, hashErr := swire.DirectMessageSigningHash(&outbox.Direct)
	if pubErr != nil || sigErr != nil || hashErr != nil || !signature.Verify(digest[:], pubkey) {
		return nil, ErrRGB11OutboxCorrupt
	}
	result.ReadStatus, result.OutboxExists = "present", true
	result.TransportMessageID = outbox.Direct.MessageID
	result.SenderMsgID = strconv.FormatUint(outbox.Direct.SenderMsgID, 10)
	result.SourceCoreNode = outbox.SourceCoreNode
	result.GeneratedAtUnixMicro = strconv.FormatUint(generated, 10)
	result.GeneratedAtUTC = time.UnixMicro(int64(generated)).UTC().Format(time.RFC3339Nano)
	return result, nil
}
