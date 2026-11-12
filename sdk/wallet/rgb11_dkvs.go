package wallet

import (
	"bytes"

	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/baid64"
	"github.com/sat20-labs/rgb11/consensus"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/invoicing"
	"github.com/sat20-labs/rgb11/operations"
	"github.com/sat20-labs/rgb11/seals"
	corestorage "github.com/sat20-labs/rgb11/storage"
	"github.com/sat20-labs/rgb11/strict_types"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"

	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const rgb11AddressMailboxPageSize = 256

var ErrRGB11DirectRootRequired = errors.New("RGB11 direct address transport requires the account-management root wallet")

func (p *rgb11Manager) requireAccountManagementRoot() error {
	owner := p.accountManagementOwner()
	if owner == nil || !owner.rgb11ManagerIsRoot(p) {
		return ErrRGB11DirectRootRequired
	}
	return nil
}

func (p *rgb11Manager) configuredRGB11Store() (*dkvsStore, error) {
	owner := p.accountManagementOwner()
	if owner == nil || owner.ensureDKVSManager() == nil {
		return nil, ErrRGB11Inconsistent
	}
	return owner.ensureDKVSManager().primaryStore()
}

// Deliveries and acknowledgments are transient protocol messages. Their
// retention is configured by the connected service node and read through GET
// /v3/dkvs/config; the wallet embeds no fallback duration.
func (p *rgb11Manager) configureRGB11AddressTransientRetention(store *dkvsStore,
	record *dkvsindexer.RecordOptions) error {

	if store == nil || record == nil {
		return ErrDKVSPathNotSynced
	}
	_, err := store.ConfigureFreeLocalRetention(record)
	return err
}

func (p *rgb11Manager) EnableConfiguredRGB11AddressReceive(options RGB11ReceiveCapabilityOptions) (*RGB11AddressEndpoint, error) {
	if err := p.requireAccountManagementRoot(); err != nil {
		return nil, err
	}
	store, err := p.configuredRGB11Store()
	if err != nil {
		return nil, err
	}
	return p.enableRGB11AddressReceiveStore(store, options)
}

func (p *rgb11Manager) ResolveConfiguredRGB11AddressEndpoint(address string,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11AddressEndpoint, error) {
	store, err := p.configuredRGB11Store()
	if err != nil {
		return nil, err
	}
	return p.resolveRGB11AddressEndpointStore(store, address, verify)
}

func (p *rgb11Manager) PrepareConfiguredRGB11AddressTransfer(ctx context.Context, request RGB11AddressSendRequest,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11PreparedTransfer, *RGB11AddressEndpoint, error) {
	store, err := p.configuredRGB11Store()
	if err != nil {
		return nil, nil, err
	}
	endpoint, err := p.resolveRGB11AddressEndpointStore(store, request.ReceiverAddress, verify)
	if err != nil {
		return nil, nil, err
	}
	return p.prepareRGB11AddressTransferForEndpoint(ctx, request, endpoint)
}

func (p *rgb11Manager) DeliverAndBroadcastConfiguredRGB11AddressTransfer(transferID string,
	options RGB11AddressDeliveryOptions) (*RGB11AddressDeliveryResult, error) {
	store, err := p.configuredRGB11Store()
	if err != nil {
		return nil, err
	}
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return nil, err
	}
	result := &RGB11AddressDeliveryResult{
		TransferID: transferID, Mode: pending.State.DeliveryMode,
		RecordKey: pending.State.DeliveryRecordKey, RecordHash: pending.State.DeliveryRecordHash,
		ObjectID: pending.State.DeliveryObjectID, Temporary: pending.State.DeliveryTemporary,
	}
	if rgb11BroadcastCompleteStatus(pending.State.Status) {
		result.TxID = pending.State.WitnessTxID
		result.Broadcast = true
		return result, nil
	}
	if pending.State.Status == "prepared" {
		result, err = p.deliverRGB11AddressTransferStore(store, transferID, options)
		if err != nil {
			return nil, err
		}
		pending, err = p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
		if err != nil {
			return nil, err
		}
	}
	if pending.State.Status != rgb11StatusBroadcastAttempted &&
		(!pending.State.DeliveryAcknowledged || pending.State.AckStatus != "accepted") {
		result.AwaitingACK = true
		return result, nil
	}
	txID, err := p.BroadcastRGB11AddressTransfer(transferID)
	if err != nil {
		return result, err
	}
	result.TxID = txID
	result.Broadcast = true
	return result, nil
}

func rgb11AddressProcessedMetadata(kind, messageID string) string {
	return "address-" + kind + "-" + messageID
}

func rgb11MailboxCursorMetadata(accountID, namespace string) string {
	scope := sha256.Sum256([]byte(accountID + "\x00" + namespace))
	return "address-mailbox-cursor-" + hex.EncodeToString(scope[:])
}

func rgb11MailboxPage(values []*dkvsValue, cursor string) ([]*dkvsValue, string) {
	if len(values) == 0 {
		return nil, ""
	}
	start := 0
	if cursor != "" {
		start = sort.Search(len(values), func(i int) bool {
			return values[i] != nil && values[i].Key > cursor
		})
		if start == len(values) {
			start = 0
		}
	}
	count := min(len(values), rgb11AddressMailboxPageSize)
	page := make([]*dkvsValue, 0, count)
	for i := 0; i < count; i++ {
		page = append(page, values[(start+i)%len(values)])
	}
	last := page[len(page)-1]
	if last == nil {
		return page, ""
	}
	return page, last.Key
}

func (p *rgb11Manager) rgb11AddressMessageProcessed(kind, messageID string) bool {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return false
	}
	value, err := p.rgbManager.projectionStore.LoadLocalMetadata(
		rgb11AddressProcessedMetadata(kind, messageID),
	)
	return err == nil && len(value) == 1 && value[0] == 1
}

func (p *rgb11Manager) markRGB11AddressMessageProcessed(kind, messageID string) error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return ErrRGB11Inconsistent
	}
	return p.rgbManager.projectionStore.SaveLocalMetadata(
		rgb11AddressProcessedMetadata(kind, messageID), []byte{1},
	)
}

// SyncConfiguredRGB11AddressMailbox processes the account-management root
// mailbox. It first checks the existing managed-prefix generations so a
// changed mailbox is installed before the local replica is read.
// A valid unbroadcast consignment is durably staged before its ACK is sent;
// chain reconciliation projects the allocation after the sender broadcasts.
// Processed cursors are device-local cache and are not included in recovery.
func (p *rgb11Manager) SyncConfiguredRGB11AddressMailbox(ctx context.Context,
	verify dkvsindexer.RecordVerificationOptions,
	ackOptions RGB11AddressDeliveryOptions) (*RGB11AddressMailboxSyncResult, error) {
	if p == nil || p.wallet == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return nil, ErrRGB11Inconsistent
	}
	if err := p.requireAccountManagementRoot(); err != nil {
		return nil, err
	}
	result := &RGB11AddressMailboxSyncResult{}
	store, err := p.configuredRGB11Store()
	if err != nil {
		return nil, err
	}
	owner := p.accountManagementOwner()
	if owner == nil || owner.ensureDKVSManager() == nil {
		return nil, ErrDKVSPathNotSynced
	}
	accountID, err := dkvsAccountID(p.wallet)
	if err != nil {
		return nil, err
	}
	mailboxTarget, err := mailboxSubscriptionTarget(accountID)
	if err != nil {
		return nil, err
	}
	if err := owner.SubscribeDKVSPrefix(mailboxTarget); err != nil {
		return nil, err
	}
	if err := owner.ensureDKVSManager().ensureCurrentSubscription(store.client); err != nil {
		return nil, err
	}
	values, err := store.ListMailboxVerified(accountID, verify)
	if err != nil {
		return nil, err
	}
	cursorValue, _ := p.rgbManager.projectionStore.LoadLocalMetadata(
		rgb11MailboxCursorMetadata(accountID, store.client.replicaNamespace),
	)
	values, nextCursor := rgb11MailboxPage(values, string(cursorValue))
	for _, value := range values {
		result.Scanned++
		if value == nil || value.record == nil {
			result.Invalid++
			continue
		}
		rawRecord := value.record

		// AccountBound mailbox records carry the authenticated DirectMessage.
		// A malformed or different format is an invalid message, never a second
		// delivery path for this account.
		direct, directErr := decodeWalletDirectRecord(p.wallet, rawRecord)
		if directErr != nil || direct == nil {
			result.Invalid++
			result.ErrorDetails = append(result.ErrorDetails, fmt.Sprintf("direct message: %v", directErr))
			continue
		}
		messageID := direct.Payload.ApplicationID
		switch direct.Payload.Kind {
		case AccountMessageKindRGB11ACK:
			if p.rgb11AddressMessageProcessed("ack", messageID) {
				result.AlreadyDone++
				continue
			}
			if _, err := p.acceptRGB11AddressACKDirect(direct); err != nil {
				result.Invalid++
				result.ErrorDetails = append(result.ErrorDetails, fmt.Sprintf("ack %s: %v", messageID, err))
				continue
			}
			if err := p.markRGB11AddressMessageProcessed("ack", messageID); err != nil {
				return nil, err
			}
			result.ACKs++
		case AccountMessageKindRGB11Consignment:
			if p.rgb11AddressMessageProcessed("consignment", messageID) {
				result.AlreadyDone++
				continue
			}
			receipt, ackRecord, err := p.acceptRGB11AddressDirect(ctx, store, direct, ackOptions)
			if err != nil {
				result.Invalid++
				result.ErrorDetails = append(result.ErrorDetails, fmt.Sprintf("consignment %s: %v", messageID, err))
				continue
			}
			if err := p.markRGB11AddressMessageProcessed("consignment", messageID); err != nil {
				return nil, err
			}
			if receipt != nil && ackRecord == nil {
				// An exactly verified settled receive needs no new ACK or business write.
				result.AlreadyDone++
			} else {
				result.Received++
			}
		}
	}
	if nextCursor != "" {
		if err := p.rgbManager.projectionStore.SaveLocalMetadata(
			rgb11MailboxCursorMetadata(accountID, store.client.replicaNamespace), []byte(nextCursor),
		); err != nil {
			return nil, err
		}
	}
	// Direct send progression is owned by the durable RGB reservation state.
	// A restarted UI does not need to remember whether it already tried to
	// broadcast: accepted ACKs and broadcast-attempted are persisted before this
	// helper crosses the irreversible boundary.
	if err := p.resumeReadyRGB11AddressTransfers(); err != nil {
		return result, err
	}
	return result, nil
}

func (p *rgb11Manager) resumeReadyRGB11AddressTransfers() error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return ErrRGB11Inconsistent
	}
	states, err := p.rgbManager.projectionStore.ListTransfers()
	if err != nil {
		return err
	}
	seen := make(map[string]struct{})
	for _, state := range states {
		if state == nil || state.Direction != "send" || !state.AddressMode ||
			state.TransferID == "" || rgb11BroadcastCompleteStatus(state.Status) {
			continue
		}
		key := state.WitnessTxID
		if key == "" {
			key = state.BatchID
		}
		if key == "" {
			key = state.TransferID
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if _, err := p.continueRGB11AddressSend(state.TransferID); err != nil {
			return err
		}
	}
	return nil
}

const (
	RGB11ReceiveCapabilityAddress = rgb11wallet.ReceiveCapabilityAddress
	RGB11ReceiveCapabilityAny     = rgb11wallet.ReceiveCapabilityAny
)

var ErrRGB11TraditionalReceiveRequired = rgb11wallet.ErrTraditionalReceiveRequired

type RGB11ReceiveCapabilityOptions struct {
	RecordOptions dkvsindexer.RecordOptions `json:"record_options"`
	Flags         uint8                     `json:"flags"`
}

func (p *rgb11Manager) enableRGB11AddressReceiveStore(store *dkvsStore,
	options RGB11ReceiveCapabilityOptions) (*RGB11AddressEndpoint, error) {

	if p == nil || store == nil || p.wallet == nil {
		return nil, ErrRGB11Inconsistent
	}
	wallet, ok := p.wallet.(*InternalWallet)
	if !ok {
		return nil, fmt.Errorf("RGB11 address receive requires an internal wallet")
	}
	if options.Flags != 0 && options.Flags&RGB11ReceiveCapabilityAddress == 0 {
		return nil, ErrRGB11TraditionalReceiveRequired
	}
	if err := p.accountManagementOwner().bindAccountToCurrentCoreNode(wallet); err != nil {
		return nil, fmt.Errorf("bind RGB11 receive account to current CoreNode: %w", err)
	}
	address := wallet.GetAddress()
	// bindAccountToCurrentCoreNode publishes the one free, globally replicated
	// account service record and records the CoreNode's local acceptance. RGB11
	// address receive is represented by a capability bit in that same value.
	return p.resolveRGB11AddressEndpointStore(store, address,
		dkvsindexer.RecordVerificationOptions{})
}

func (p *rgb11Manager) resolveRGB11AddressEndpointStore(store *dkvsStore, address string,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11AddressEndpoint, error) {

	if store == nil || address == "" {
		return nil, ErrRGB11TraditionalReceiveRequired
	}
	mappingKey, err := dkvsindexer.AccountMappingKey(GetChainParam().Name, address)
	if err != nil {
		return nil, ErrRGB11TraditionalReceiveRequired
	}
	mapping, err := store.GetVerified(mappingKey, verify)
	if err != nil {
		return nil, ErrRGB11TraditionalReceiveRequired
	}
	descriptor, err := dkvsindexer.DecodeAccountServiceDescriptor(mapping.Value)
	if err != nil {
		return nil, ErrRGB11TraditionalReceiveRequired
	}
	if descriptor.Capabilities&accountServiceCapabilityRGB11Direct == 0 {
		return nil, ErrRGB11TraditionalReceiveRequired
	}
	accountID := descriptor.AccountID
	pubKey, err := dkvsindexer.AccountPubKey(accountID)
	if err != nil {
		return nil, err
	}
	pkScript, err := AddrToPkScript(address, GetChainParam())
	if err != nil {
		return nil, err
	}
	return &RGB11AddressEndpoint{
		AccountID: accountID, Address: address, MailboxID: accountID,
		CompressedPubKey: pubKey, PkScript: pkScript,
		CapabilityFlags:     RGB11ReceiveCapabilityAddress | RGB11ReceiveCapabilityAny,
		CapabilityRecordKey: mappingKey, CapabilityRecordHash: mapping.Hash,
		Temporary: false, IssueHeight: mapping.IssueHeight, TTL: mapping.TTL,
	}, nil
}

const (
	rgb11AddressEnvelopeInline = rgb11wallet.AddressEnvelopeInline
	rgb11AddressEnvelopeBlob   = rgb11wallet.AddressEnvelopeBlob

	RGB11AddressACKAccepted   = rgb11wallet.AddressACKAccepted
	RGB11AddressACKNeedResend = rgb11wallet.AddressACKNeedResend
	RGB11AddressACKRejected   = rgb11wallet.AddressACKRejected

	rgb11AddressInlineLimit = 10 * 1024
)

// Direct RGB transfers advertise this bit in the otherwise opaque account
// service capability bitmap. Its meaning belongs to the Wallet SDK.
const accountServiceCapabilityRGB11Direct = uint64(1 << 0)

var (
	ErrRGB11AddressDeliveryRequired = errors.New("RGB11 address consignment must be delivered before broadcast")
	ErrRGB11AddressMailbox          = rgb11wallet.ErrAddressMailbox
)

type RGB11AddressDeliveryOptions struct {
	RecordOptions dkvsindexer.RecordOptions `json:"record_options"`
	InlineLimit   int                       `json:"inline_limit,omitempty"`
}

type rgb11AccountPayloadCryptor interface {
	EncryptToAccount(accountID string, plaintext []byte) ([]byte, error)
	DecryptFromAccount(accountID string, ciphertext []byte) ([]byte, error)
}

func rgb11AddressRecordCovers(record *dkvsValue, options dkvsindexer.RecordOptions,
	currentHeight uint64) bool {
	if record == nil {
		return false
	}
	if record.TTL == 0 {
		return options.TTL == 0
	}
	if options.TTL == 0 || record.IssueHeight > ^uint64(0)-record.TTL ||
		currentHeight > ^uint64(0)-options.TTL {
		return false
	}
	return record.IssueHeight+record.TTL >= currentHeight+options.TTL
}

func (p *rgb11Manager) loadExistingRGB11AddressDelivery(store *dkvsStore,
	pending *rgb11wallet.PendingTransfer, mailKey, messageID string,
	options dkvsindexer.RecordOptions) (*dkvsValue, []byte, uint8, bool, error) {

	if pending == nil || pending.State.Status != "delivered" {
		return nil, nil, 0, false, nil
	}
	mailRecord, err := store.Get(mailKey)
	if errors.Is(err, ErrDKVSRecordNotFound) {
		return nil, nil, 0, false, nil
	}
	if err != nil {
		return nil, nil, 0, false, err
	}
	mode, ciphertext, err := rgb11wallet.DecodeAddressEnvelope(mailRecord.Value)
	if err != nil {
		return nil, nil, 0, false, err
	}
	currentHeight, known := p.ensureDKVSManager().verificationHeight()
	if !known {
		return nil, nil, 0, false, ErrDKVSVerificationHeightRequired
	}
	covered := rgb11AddressRecordCovers(mailRecord, options, currentHeight)
	if mode == rgb11AddressEnvelopeBlob {
		blobKey, err := dkvsindexer.BlobKey(pending.State.SenderAccountID, messageID)
		if err != nil {
			return nil, nil, 0, false, err
		}
		blobRecord, err := store.Get(blobKey)
		if errors.Is(err, ErrDKVSRecordNotFound) {
			return mailRecord, nil, mode, false, nil
		}
		if err != nil {
			return nil, nil, 0, false, err
		}
		ciphertext = append([]byte(nil), blobRecord.Value...)
		covered = covered && rgb11AddressRecordCovers(blobRecord, options, currentHeight)
	}
	return mailRecord, ciphertext, mode, covered, nil
}

func (p *rgb11Manager) deliverRGB11AddressTransferStore(store *dkvsStore, transferID string,
	options RGB11AddressDeliveryOptions) (*RGB11AddressDeliveryResult, error) {
	if p == nil || store == nil || p.wallet == nil || p.rgbManager == nil ||
		p.rgbManager.projectionStore == nil || transferID == "" {
		return nil, ErrRGB11AddressDeliveryRequired
	}
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return nil, err
	}
	if pending.State.Status == "delivered" && pending.State.DeliveryRecordKey != "" &&
		pending.State.DeliveryRecordHash != "" {
		return &RGB11AddressDeliveryResult{
			TransferID: transferID, Mode: pending.State.DeliveryMode,
			RecordKey: pending.State.DeliveryRecordKey, RecordHash: pending.State.DeliveryRecordHash,
			ObjectID: pending.State.DeliveryObjectID, Temporary: pending.State.DeliveryTemporary,
		}, nil
	}
	if !pending.State.AddressMode || pending.State.TransportMode != RGB11AddressTransport ||
		pending.State.ReceiverAccountID == "" || len(pending.RecipientConsignment) == 0 ||
		pending.State.Status != "prepared" && pending.State.Status != "delivered" {
		return nil, ErrRGB11AddressDeliveryRequired
	}
	messageID := pending.State.AddressMessageID
	if messageID == "" {
		messageID, err = rgb11AddressMessageID(pending.State.TransferID)
		if err != nil {
			return nil, err
		}
		pending.State.AddressMessageID = messageID
	}
	// Persist the prepared transition before exposing its consignment to the
	// receiver. A lost device can then resume the same signed transaction.
	if owner := p.accountManagementOwner(); owner != nil {
		if err := p.syncRGB11OperationRecovery(context.Background()); err != nil {
			return nil, err
		}
	}
	expected := pending
	pending, err = p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return nil, err
	}
	if !sameRGB11BroadcastIntent(expected, pending) {
		return nil, ErrRGB11Inconsistent
	}
	if pending.State.Status == "delivered" && pending.State.DeliveryRecordKey != "" &&
		pending.State.DeliveryRecordHash != "" {
		return &RGB11AddressDeliveryResult{
			TransferID: transferID, Mode: pending.State.DeliveryMode,
			RecordKey: pending.State.DeliveryRecordKey, RecordHash: pending.State.DeliveryRecordHash,
			ObjectID: pending.State.DeliveryObjectID, Temporary: pending.State.DeliveryTemporary,
		}, nil
	}
	if pending.State.Status != "prepared" {
		return nil, ErrRGB11AddressDeliveryRequired
	}
	baseline := cloneRGB11BroadcastIntent(pending)
	message, err := p.accountManagementOwner().sendWalletDirectMessage(
		p.wallet, messageID, AccountMessageKindRGB11Consignment,
		pending.State.ReceiverAccountID, pending.RecipientConsignment,
	)
	if err != nil {
		return nil, err
	}
	encoded, err := swire.SerializeDirectMessage(message, true)
	if err != nil {
		return nil, err
	}
	actualKey, err := dkvsindexer.MailMsgKey(
		pending.State.ReceiverAccountID, message.SenderAccount,
		message.MessageID,
	)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	pending.State.DeliveryMode = "direct"
	pending.State.DeliveryObjectID = ""
	// Bind the actual mailbox receipt without replacing the prepared
	// application-ID relay identity.
	pending.State.DeliveryRecordKey = actualKey
	pending.State.DeliveryRecordHash = hex.EncodeToString(digest[:])
	pending.State.DeliveryTemporary = true
	pending.State.DeliveryIssueHeight = 0
	pending.State.DeliveryTTL = 0 // retention is controlled by the recipient CoreNode mailbox policy
	pending.State.RelayDurability = "ACCOUNT_BOUND"
	pending.State.Status = "delivered"
	if err := p.rgbManager.projectionStore.CompareAndSavePendingTransferStates([]*rgb11wallet.PendingTransfer{baseline}, []*rgb11wallet.PendingTransfer{pending}); err != nil {
		return nil, err
	}
	if err := p.autoBackupRGB11AfterMutation(); err != nil {
		return nil, err
	}
	_ = options // retained in the public API for source compatibility; MessageManager owns mailbox retention.
	return &RGB11AddressDeliveryResult{
		TransferID: transferID, Mode: "direct", RecordKey: actualKey,
		RecordHash: pending.State.DeliveryRecordHash, Temporary: true,
	}, nil
}

func (p *rgb11Manager) applyRGB11AddressDeliveryState(pending *rgb11wallet.PendingTransfer,
	mailRecord *dkvsValue, modeName, objectID string) {

	pending.State.DeliveryMode = modeName
	pending.State.DeliveryObjectID = objectID
	pending.State.DeliveryRecordKey = mailRecord.Key
	pending.State.RelayRecordKey = mailRecord.Key
	pending.State.DeliveryRecordHash = mailRecord.Hash
	pending.State.DeliveryTemporary = mailRecord.TTL > 0
	pending.State.DeliveryIssueHeight = mailRecord.IssueHeight
	pending.State.DeliveryTTL = mailRecord.TTL
	pending.State.RelayExpiry = int64(dkvsindexer.RecordExpiryHeight(mailRecord.record))
	pending.State.RelayDurability = "DKVS_PERSISTENT"
	if pending.State.DeliveryTemporary {
		pending.State.RelayDurability = "DKVS_TEMP"
	}
	pending.State.Status = "delivered"
}

type rgb11AddressSendResult struct {
	txID  string
	ready bool
}

// One live owner advances a transaction at a time. All scoped views share the
// registry; neither its map mutex nor a wallet data lock spans network work.
func (p *rgb11Manager) continueRGB11AddressSend(transferID string) (rgb11AddressSendResult, error) {
	var empty rgb11AddressSendResult
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil ||
		p.rgbManager.evidence == nil || transferID == "" {
		return empty, ErrRGB11AddressDeliveryRequired
	}
	if p.scopeStates == nil {
		return empty, ErrRGB11Inconsistent
	}
	// This first read supplies a grouping key only, never an executable intent.
	release := p.beginRGB11ReservationRead()
	probe, err := p.projectionStore.LoadPendingTransfer(transferID)
	release()
	if err != nil {
		return empty, err
	}
	if probe.State.TransferID != transferID || probe.State.WitnessTxID == "" {
		return empty, ErrRGB11Inconsistent
	}
	if !probe.State.AddressMode {
		return empty, ErrRGB11AddressDeliveryRequired
	}
	ids := probe.State.BatchTransferIDs
	if len(ids) == 0 {
		ids = []string{transferID}
	}
	if probe.State.Direction != "send" || len(ids) > 32 || !slices.Contains(ids, transferID) ||
		len(ids) > 1 && (probe.State.BatchID == "" || probe.State.BatchSize != len(ids)) {
		return empty, ErrRGB11Inconsistent
	}
	if len(ids) > 1 {
		for i, id := range ids {
			if id == transferID && (id != rgb11BatchRecipientID(probe.State.BatchID, uint32(i)) ||
				probe.State.RecipientVout != uint32(i+1)) {
				return empty, ErrRGB11Inconsistent
			}
		}
	}
	key := p.rgb11ScopeKey() + "\x00" + probe.State.WitnessTxID + "\x00" + probe.State.BatchID +
		"\x00" + strings.Join(ids, "\x00")
	value, err, shared := p.scopeStates.sendFlights.Do(key, func() (any, error) {
		if err := p.checkRGB11OperationSource(); err != nil {
			return empty, err
		}
		pending, batch, err := p.readRGB11AddressBatch(transferID)
		if err != nil {
			return empty, err
		}
		if pending.State.WitnessTxID != probe.State.WitnessTxID || pending.State.BatchID != probe.State.BatchID ||
			!slices.Equal(pending.State.BatchTransferIDs, probe.State.BatchTransferIDs) {
			return empty, ErrRGB11Inconsistent
		}
		for _, item := range batch {
			if item == nil || !item.State.AddressMode || item.State.Direction != "send" {
				return empty, ErrRGB11Inconsistent
			}
			if rgb11BroadcastCompleteStatus(item.State.Status) {
				continue
			}
			if (item.State.Status != "delivered" && item.State.Status != rgb11StatusBroadcastAttempted) ||
				item.State.DeliveryRecordHash == "" || item.State.DeliveryRecordKey == "" ||
				(item.State.Status != rgb11StatusBroadcastAttempted &&
					(!item.State.DeliveryAcknowledged || item.State.AckStatus != "accepted")) {
				return empty, nil
			}
		}
		txID, err := p.broadcastRGB11PendingBatch(batch, nil)
		return rgb11AddressSendResult{txID: txID, ready: true}, err
	})
	if shared {
		Log.Infof("RGB send flight shared: manager=%d scope_tag=%s tx_tag=%s",
			p.rgb11ReservationOwner().debugID, accountTraceScope(p.rgb11ScopeKey()), accountTraceScope(probe.State.WitnessTxID))
	}
	result, ok := value.(rgb11AddressSendResult)
	if !ok {
		return empty, errors.Join(ErrRGB11Inconsistent, err)
	}
	return result, err
}

// BroadcastRGB11AddressTransfer crosses the irreversible broadcast boundary
// only after the receiver has persisted and acknowledged the consignment.
func (p *rgb11Manager) BroadcastRGB11AddressTransfer(transferID string) (string, error) {
	result, err := p.continueRGB11AddressSend(transferID)
	if err != nil {
		return result.txID, err
	}
	if !result.ready {
		return "", ErrRGB11AddressDeliveryRequired
	}
	return result.txID, nil
}

func parseRGB11AddressMailboxKey(record *swire.DKVSRecord) (receiverID, senderID, messageID string, err error) {
	if record == nil {
		return "", "", "", ErrRGB11AddressMailbox
	}
	parsed, err := dkvsindexer.ParseKey(record.Key)
	if err != nil || parsed.Namespace != "mail" || len(parsed.Segments) != 4 ||
		parsed.Segments[1] != "msg" {
		return "", "", "", ErrRGB11AddressMailbox
	}
	receiverID, senderID, messageID = parsed.Segments[0], parsed.Segments[2], parsed.Segments[3]
	if len(messageID) != 64 {
		return "", "", "", ErrRGB11AddressMailbox
	}
	if decoded, decodeErr := hex.DecodeString(messageID); decodeErr != nil || len(decoded) != 32 {
		return "", "", "", ErrRGB11AddressMailbox
	}
	return receiverID, senderID, messageID, nil
}

func (p *rgb11Manager) findPreparedRGB11AddressAllocation(prepared *rgb11wallet.PreparedValidation, messageIDs ...string) (
	*rgb11wallet.ValidatedAllocation, string, error,
) {
	if prepared == nil || prepared.Receipt == nil || len(prepared.WitnessTxIDs) != 1 ||
		p.rgbManager == nil || p.rgbManager.evidence == nil {
		return nil, "", ErrRGB11AddressMailbox
	}
	for index := range prepared.Receipt.Allocations {
		allocation := &prepared.Receipt.Allocations[index]
		if !allocation.WitnessTxPtr || allocation.AssignmentType != 4000 {
			continue
		}
		output := prepared.Outputs[allocation.OutPoint]
		if output == nil {
			continue
		}
		if !rgb11AllocationControlledByWallet(p.wallet, allocation, output.PkScript) {
			continue
		}
		if txID := allocationOutpointTxID(allocation.OutPoint); txID != prepared.WitnessTxIDs[0] {
			continue
		}
		if len(messageIDs) != 0 {
			canonical, err := rgb11AddressMessageID(prepared.Receipt.TransferID)
			if err != nil {
				return nil, "", err
			}
			if canonical != messageIDs[0] {
				vout, ok := outpointVout(allocation.OutPoint)
				if !ok || vout == 0 || vout > 32 {
					continue
				}
				child, err := rgb11AddressMessageID(rgb11BatchRecipientID(prepared.Receipt.TransferID, vout-1))
				if err != nil || child != messageIDs[0] {
					continue
				}
			}
		}
		return allocation, prepared.WitnessTxIDs[0], nil
	}
	return nil, "", ErrRGB11NoAllocation
}

func (p *rgb11Manager) confirmedRGB11AddressState(ctx context.Context,
	raw []byte) (*rgb11wallet.PreparedValidation, error) {

	latestOps, err := rgb11LatestBundleOps(raw)
	if err != nil {
		return nil, err
	}
	receipt, err := rgb11wallet.ValidateWith(
		ctx, rgb11wallet.NewNativeConsensusValidator(), raw, p.rgbManager.evidence,
	)
	if err != nil {
		return nil, err
	}
	outputs := make(map[string]*rgb11wallet.BitcoinUTXO)
	witnesses := make(map[string]struct{})
	for _, allocation := range receipt.Allocations {
		output, err := p.rgbManager.evidence.GetUTXO(allocation.OutPoint)
		if err != nil || output == nil {
			return nil, ErrRGB11AddressMailbox
		}
		outputs[allocation.OutPoint] = output
		_, latest := latestOps[allocation.OperationID]
		if latest && allocation.WitnessTxPtr && allocation.AssignmentType == 4000 {
			txID := allocation.WitnessTxID
			if txID == "" {
				txID = allocationOutpointTxID(allocation.OutPoint)
			}
			if txID == "" {
				return nil, ErrRGB11AddressMailbox
			}
			witnesses[txID] = struct{}{}
		}
	}
	if len(witnesses) != 1 {
		return nil, ErrRGB11AddressMailbox
	}
	witnessTxIDs := make([]string, 0, 1)
	for txID := range witnesses {
		witnessTxIDs = append(witnessTxIDs, txID)
	}
	return &rgb11wallet.PreparedValidation{
		Receipt: receipt, Outputs: outputs, WitnessTxIDs: witnessTxIDs,
	}, nil
}

func rgb11LatestBundleOps(raw []byte) (map[string]struct{}, error) {
	container, err := coreconsignment.Decode(raw)
	if err != nil {
		return nil, err
	}
	bundles, ok := container.Value.Field("bundles")
	bundles = bundles.Unwrap()
	if !ok || len(bundles.Items) == 0 {
		return nil, ErrRGB11AddressMailbox
	}
	bundle, ok := bundles.Items[len(bundles.Items)-1].Unwrap().Field("bundle")
	if !ok {
		return nil, ErrRGB11AddressMailbox
	}
	committed, err := operations.CommitBundle(bundle)
	if err != nil {
		return nil, err
	}
	result := make(map[string]struct{}, len(committed.Transitions))
	for _, transition := range committed.Transitions {
		commitment, err := operations.CommitTransition(transition)
		if err != nil {
			return nil, err
		}
		result[hex.EncodeToString(commitment.OperationID[:])] = struct{}{}
	}
	if len(result) == 0 {
		return nil, ErrRGB11AddressMailbox
	}
	return result, nil
}

func (p *rgb11Manager) acceptRGB11AddressDirect(ctx context.Context, store *dkvsStore,
	item *AccountDirectMessage, ackOptions RGB11AddressDeliveryOptions) (
	*rgb11wallet.ValidationReceipt, *swire.DKVSRecord, error) {
	if p == nil || store == nil || item == nil || item.Payload == nil || item.Direct == nil || item.Record == nil ||
		item.Payload.Kind != AccountMessageKindRGB11Consignment {
		return nil, nil, ErrRGB11AddressMailbox
	}
	legacyKey, err := dkvsindexer.MailMsgKey(
		item.Direct.RecipientAccount, item.Direct.SenderAccount, item.Payload.ApplicationID,
	)
	if err != nil {
		return nil, nil, err
	}
	synthetic := *item.Record
	synthetic.Key = legacyKey
	receipt, ackRecord, err := p.acceptRGB11AddressMailboxDecodedWithMetadata(
		ctx, &synthetic, item.Record, item.Payload.Body, "direct",
		func(senderID, messageID string, ack RGB11AddressACK) (*swire.DKVSRecord, error) {
			return p.sendRGB11AddressACKDirect(senderID, messageID, ack, ackOptions)
		},
	)
	if err != nil && errors.Is(err, ErrRGB11AddressMailbox) {
		rejected, rejectErr := p.sendRGB11AddressACKDirect(
			item.Direct.SenderAccount, item.Payload.ApplicationID,
			RGB11AddressACK{Status: RGB11AddressACKRejected}, ackOptions,
		)
		return receipt, rejected, errors.Join(err, rejectErr)
	}
	if err != nil || receipt == nil {
		return receipt, ackRecord, err
	}
	return receipt, ackRecord, nil
}

func (p *rgb11Manager) acceptRGB11AddressMailboxStore(ctx context.Context, store *dkvsStore,
	value *dkvsValue, ackOptions RGB11AddressDeliveryOptions) (
	*rgb11wallet.ValidationReceipt, *swire.DKVSRecord, error) {

	if p == nil || store == nil || value == nil || value.record == nil {
		return nil, nil, ErrRGB11AddressMailbox
	}
	if direct, err := decodeWalletDirectRecord(p.wallet, value.record); err == nil && direct != nil &&
		direct.Payload != nil && direct.Payload.Kind == AccountMessageKindRGB11Consignment {
		return p.acceptRGB11AddressDirect(ctx, store, direct, ackOptions)
	}
	return nil, nil, ErrRGB11AddressMailbox
}

func rgb11AddressACKStateIdentityMatches(expected, current *rgb11wallet.TransferState,
	senderID, receiverID, messageID, consignmentHash, witnessTxID, output string) bool {
	if expected == nil || current == nil || current.TransferID == "" ||
		current.Direction != "receive" || !current.AddressMode ||
		current.TransportMode != RGB11AddressTransport || current.Status == "rejected" ||
		current.Status == "cancelled" || current.Status == "expired" ||
		current.SenderAccountID != senderID ||
		current.ReceiverAccountID != receiverID || current.ConsignmentHash != consignmentHash ||
		current.WitnessTxID != witnessTxID || current.ReceiveRequestID == "" ||
		!slices.Contains(current.OutputOutPoints, output) {
		return false
	}
	if current.Status == "settled" {
		// Terminal recovery omits delivery cache; its protocol identity remains.
		derived, err := rgb11AddressMessageID(current.TransferID)
		if err != nil || derived != messageID ||
			current.AddressMessageID != "" && current.AddressMessageID != messageID {
			return false
		}
	} else if current.AddressMessageID != messageID {
		return false
	}
	return expected.TransferID == current.TransferID && expected.BatchID == current.BatchID &&
		expected.TransportMode == current.TransportMode && expected.Direction == current.Direction &&
		expected.RecipientID == current.RecipientID && expected.ReceiveRequestID == current.ReceiveRequestID &&
		expected.Invoice == current.Invoice && slices.Equal(expected.BatchTransferIDs, current.BatchTransferIDs) &&
		slices.Equal(expected.InputOutPoints, current.InputOutPoints) &&
		slices.Equal(expected.OutputOutPoints, current.OutputOutPoints) &&
		expected.ConsignmentHash == current.ConsignmentHash &&
		expected.WitnessTxID == current.WitnessTxID && expected.AddressMode == current.AddressMode &&
		expected.AddressMessageID == current.AddressMessageID &&
		expected.SenderAccountID == current.SenderAccountID &&
		expected.ReceiverAccountID == current.ReceiverAccountID &&
		expected.ReceiverAddress == current.ReceiverAddress &&
		expected.DeliveryMode == current.DeliveryMode &&
		expected.DeliveryRecordKey == current.DeliveryRecordKey &&
		expected.DeliveryRecordHash == current.DeliveryRecordHash &&
		expected.DeliveryObjectID == current.DeliveryObjectID
}

func sameRGB11AddressReceiveRequest(expected, current *corewallet.ReceiveRequest) bool {
	if expected == nil || current == nil {
		return expected == current
	}
	expectedSeal, expectedErr := expected.Seal.StrictBytes()
	currentSeal, currentErr := current.Seal.StrictBytes()
	return expectedErr == nil && currentErr == nil &&
		expected.Version == current.Version && expected.Mode == current.Mode &&
		expected.RequestID == current.RequestID && expected.RecipientID == current.RecipientID &&
		bytes.Equal(expectedSeal, currentSeal) && bytes.Equal(expected.WitnessScript, current.WitnessScript) &&
		expected.Invoice == current.Invoice && expected.RelayKey == current.RelayKey &&
		expected.AckKey == current.AckKey && expected.CreatedAt == current.CreatedAt &&
		expected.Expiry == current.Expiry && expected.TransferID == current.TransferID &&
		expected.ObjectHash == current.ObjectHash && expected.WitnessTxID == current.WitnessTxID
}

func rgb11AddressACKRequestMatchesState(request *corewallet.ReceiveRequest,
	state *rgb11wallet.TransferState, receiverID, consignmentHash, witnessTxID string,
	witnessScript []byte) bool {
	if request == nil || state == nil ||
		(request.Status != corewallet.ReceiveAcknowledged && request.Status != corewallet.ReceiveAccepted) ||
		request.Mode != corewallet.ReceiveWitness || request.RequestID == "" ||
		request.RecipientID != receiverID || !bytes.Equal(request.WitnessScript, witnessScript) ||
		state.ReceiveRequestID != request.RequestID || state.TransferID != request.TransferID ||
		state.ConsignmentHash != consignmentHash || request.ObjectHash != consignmentHash ||
		state.WitnessTxID != witnessTxID ||
		(request.WitnessTxID != "" && request.WitnessTxID != witnessTxID) {
		return false
	}
	switch state.Status {
	case "awaiting_broadcast", "pending", "settled", "orphaned":
		return true
	default:
		return false
	}
}

func (p *rgb11Manager) acceptRGB11AddressMailboxDecoded(ctx context.Context,
	record *swire.DKVSRecord, raw []byte, mode string,
	sendACK func(string, string, RGB11AddressACK) (*swire.DKVSRecord, error)) (
	*rgb11wallet.ValidationReceipt, *swire.DKVSRecord, error) {
	return p.acceptRGB11AddressMailboxDecodedWithMetadata(ctx, record, record, raw, mode, sendACK)
}

func (p *rgb11Manager) acceptRGB11AddressMailboxDecodedWithMetadata(ctx context.Context,
	record, metadataRecord *swire.DKVSRecord, raw []byte, mode string,
	sendACK func(string, string, RGB11AddressACK) (*swire.DKVSRecord, error)) (
	*rgb11wallet.ValidationReceipt, *swire.DKVSRecord, error) {

	if p == nil || record == nil || metadataRecord == nil || sendACK == nil || p.wallet == nil || p.rgbManager == nil {
		return nil, nil, ErrRGB11AddressMailbox
	}
	if ctx == nil {
		ctx = context.Background()
	}
	walletID, err := p.RGB11WalletID()
	if err != nil {
		return nil, nil, err
	}
	return p.acceptRGB11MailboxAttempt(ctx, record, metadataRecord, bytes.Clone(raw), mode, sendACK,
		walletID, p.rgb11ScopeKey(), 0)
}

// Rebuild only a rejected precommit candidate. Every committed-target and ACK
// path returns directly, including its persistence or backup errors.
func (p *rgb11Manager) acceptRGB11MailboxAttempt(ctx context.Context,
	record, metadataRecord *swire.DKVSRecord, raw []byte, mode string,
	sendACK func(string, string, RGB11AddressACK) (*swire.DKVSRecord, error),
	walletID, scope string, attempt int) (*rgb11wallet.ValidationReceipt, *swire.DKVSRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	actualID, err := p.RGB11WalletID()
	if err != nil {
		return nil, nil, err
	}
	if actualID != walletID || p.rgb11ScopeKey() != scope {
		return nil, nil, errAccountSnapshotChanged
	}
	if err := p.checkRGB11OperationSource(); err != nil {
		return nil, nil, err
	}
	receiverID, senderID, messageID, err := parseRGB11AddressMailboxKey(record)
	if err != nil {
		return nil, nil, err
	}
	localID, err := dkvsAccountID(p.wallet)
	if err != nil || receiverID != localID {
		return nil, nil, ErrRGB11AddressMailbox
	}
	container, err := coreconsignment.Decode(raw)
	if err != nil || container.Armor.ID == "" {
		return nil, nil, ErrRGB11AddressMailbox
	}
	canonicalTransferID := container.Armor.ID
	prepared, err := rgb11wallet.ValidatePreparedWith(
		ctx, rgb11wallet.NewNativeConsensusValidator(), raw, p.rgbManager.evidence,
	)
	confirmed := false
	if errors.Is(err, rgb11wallet.ErrValidationReceipt) {
		prepared, err = p.confirmedRGB11AddressState(ctx, raw)
		confirmed = err == nil
	}
	if err != nil {
		return nil, nil, err
	}
	receipt := prepared.Receipt
	if receipt.TransferID == "" || receipt.TransferID != canonicalTransferID {
		return nil, nil, ErrRGB11AddressMailbox
	}
	allocation, witnessTxID, err := p.findPreparedRGB11AddressAllocation(prepared, messageID)
	if err != nil {
		return nil, nil, err
	}
	amount, err := decimalUint64(&allocation.Amount)
	if err != nil {
		return nil, nil, err
	}
	vout, ok := outpointVout(allocation.OutPoint)
	if !ok {
		return nil, nil, ErrRGB11AddressMailbox
	}
	pubkey := p.wallet.GetPubKey()
	if pubkey == nil || len(pubkey.SerializeCompressed()) != 33 {
		return nil, nil, ErrRGB11WalletLocked
	}
	pkScript, err := AddrToPkScript(p.wallet.GetAddress(), GetChainParam())
	if err != nil {
		return nil, nil, err
	}
	var internal [32]byte
	copy(internal[:], pubkey.SerializeCompressed()[1:])
	localTransferID := canonicalTransferID
	canonicalMessageID, _ := rgb11AddressMessageID(canonicalTransferID)
	if canonicalMessageID != messageID {
		localTransferID = rgb11BatchRecipientID(canonicalTransferID, vout-1)
	}
	if state, loadErr := p.rgbManager.projectionStore.LoadTransferState(localTransferID); loadErr == nil {
		if !rgb11AddressACKStateIdentityMatches(state, state, senderID, receiverID, messageID,
			receipt.ConsignmentHash, witnessTxID, allocation.OutPoint) {
			return nil, nil, ErrRGB11AddressMailbox
		}
		expectedState := *state
		expectedRequest, err := p.rgbManager.engine.LoadReceive(state.ReceiveRequestID)
		if err != nil || !rgb11AddressACKRequestMatchesState(expectedRequest, state, receiverID,
			receipt.ConsignmentHash, witnessTxID, pkScript) {
			return nil, nil, ErrRGB11AddressMailbox
		}
		if owner := p.accountManagementOwner(); owner != nil && state.Status != "settled" {
			if err := p.syncRGB11OperationRecovery(context.Background()); err != nil {
				return nil, nil, err
			}
		}
		state, err = p.rgbManager.projectionStore.LoadTransferState(localTransferID)
		if err != nil || !rgb11AddressACKStateIdentityMatches(&expectedState, state,
			senderID, receiverID, messageID, receipt.ConsignmentHash, witnessTxID, allocation.OutPoint) {
			return nil, nil, ErrRGB11AddressMailbox
		}
		request, err := p.rgbManager.engine.LoadReceive(state.ReceiveRequestID)
		if err != nil || !sameRGB11AddressReceiveRequest(expectedRequest, request) ||
			!rgb11AddressACKRequestMatchesState(request, state, receiverID,
				receipt.ConsignmentHash, witnessTxID, pkScript) {
			return nil, nil, ErrRGB11AddressMailbox
		}
		if state.Status == "settled" {
			// The proof, authenticated parties, original output and complete receive
			// request were revalidated above. Confirmation already ended delivery;
			// do not recreate ACK cache or alter this journal's recovery generation.
			managerID := uint64(0)
			if owner := p.accountManagementOwner(); owner != nil {
				managerID = owner.debugID
			}
			Log.Infof("RGB settled receive replay: manager=%d message_tag=%s transfer_tag=%s consignment_tag=%s witness_tag=%s proof_verified=true identity_match=true request_match=true cached_message_present=%t",
				managerID, accountTraceScope(messageID), accountTraceScope(state.TransferID),
				accountTraceScope(receipt.ConsignmentHash), accountTraceScope(witnessTxID), state.AddressMessageID != "")
			return receipt, nil, nil
		}
		ackBaseline := *state
		ackRecord, err := sendACK(senderID, messageID,
			RGB11AddressACK{Status: RGB11AddressACKAccepted})
		if err != nil {
			return nil, nil, err
		}
		state.AckStatus = "ack-sent"
		state.DeliveryAcknowledged = true
		if err := p.saveRGB11ReceiveACK(&ackBaseline, state, request, "existing"); err != nil {
			return nil, nil, err
		}
		if err := p.autoBackupRGB11AfterMutation(); err != nil {
			return nil, nil, err
		}
		return receipt, ackRecord, nil
	} else if !errors.Is(loadErr, indexer.ErrKeyNotFound) {
		return nil, nil, loadErr
	}
	// Build the request and its address metadata in memory. A capacity failure
	// must leave neither an orphan request nor a partial receive reservation.
	receiveEngine, err := corewallet.NewEngine(corestorage.NewMemoryStore())
	if err != nil {
		return nil, nil, err
	}
	request, err := receiveEngine.CreateReceive(corewallet.ReceiveParams{
		Mode: corewallet.ReceiveWitness, ContractID: receipt.ContractID, SchemaID: receipt.SchemaID,
		Network: rgb11InvoiceNetwork(GetChainParam()), Amount: &amount,
		AssignmentName: "assetOwner", RecipientID: receiverID, WitnessVout: vout,
		WitnessScript: pkScript, InternalXOnly: &internal,
		Expiry: time.Now().Add(24 * time.Hour).Unix(), StandardOnly: true,
	})
	if err != nil {
		return nil, nil, err
	}
	current, err := p.readRGB11Local(walletID)
	if err != nil {
		return nil, nil, err
	}
	candidate := *current
	candidate.EngineRecords, err = rgb11wallet.UpsertEngineSnapshotReceive(candidate.EngineRecords, request)
	if err != nil {
		return nil, nil, err
	}
	var next *RGB11WalletSnapshot
	var accepted *rgb11wallet.ValidationReceipt
	if confirmed {
		next, accepted, _, err = p.buildRGB11AcceptedConsignmentCandidateWithID(
			ctx, &candidate, request.RequestID, raw, witnessTxID, &vout, localTransferID,
		)
	} else {
		next, accepted, err = p.buildRGB11PreparedReceiveCandidate(
			ctx, &candidate, request.RequestID, raw, witnessTxID, &vout, localTransferID,
		)
	}
	if err != nil {
		return nil, nil, err
	}
	state, err := rgb11TransferFromSnapshot(next.ProjectionRecords, localTransferID)
	if err != nil {
		return nil, nil, err
	}
	recordHash := dkvsindexer.RecordHash(metadataRecord)
	state.AddressMode = true
	state.AddressMessageID = messageID
	state.TransportMode = RGB11AddressTransport
	state.SenderAccountID = senderID
	state.ReceiverAccountID = receiverID
	state.ReceiverAddress = p.wallet.GetAddress()
	state.Invoice = ""
	state.SyntheticInvoiceRemoved = true
	state.DeliveryMode = mode
	state.DeliveryRecordKey = metadataRecord.Key
	state.DeliveryRecordHash = hex.EncodeToString(recordHash[:])
	state.DeliveryTemporary = metadataRecord.TTL > 0
	state.DeliveryIssueHeight = metadataRecord.IssueHeight
	state.DeliveryTTL = metadataRecord.TTL
	state.AckStatus = "persisted"
	if !confirmed {
		state.Status = "awaiting_broadcast"
	}
	// This row was just built in memory and has never been published. Replace
	// that provisional identity with the complete address receive identity;
	// existing durable receives are handled by the retry branch above.
	next.ProjectionRecords, err = rgb11wallet.RemoveLocalKeys(next.ProjectionRecords, "transfer-"+state.TransferID)
	if err != nil {
		return nil, nil, err
	}
	next.ProjectionRecords, err = rgb11wallet.UpsertLocalTransfer(next.ProjectionRecords, state)
	if err != nil {
		return nil, nil, err
	}
	if owner := p.accountManagementOwner(); owner != nil {
		scopeID, scopeErr := p.rgb11AccountManagedScopeID()
		if scopeErr != nil {
			return nil, nil, scopeErr
		}
		if _, err := owner.preflightRGB11TerminalRecovery(scopeID, next); err != nil {
			return nil, nil, err
		}
	}
	if _, err := p.applyRGB11CandidateSnapshot(ctx, current, next); err != nil {
		if errors.Is(err, errRGB11StaleLocal) {
			Log.Warnf("RGB candidate operation rejected: op=direct_receive_new manager=%d scope_tag=%s message_tag=%s transfer_tag=%s confirmed=%t attempt=%d",
				p.rgb11ReservationOwner().debugID, accountTraceScope(p.rgb11ScopeKey()), accountTraceScope(messageID),
				accountTraceScope(localTransferID), confirmed, attempt+1)
			if attempt < 2 {
				// This candidate never committed. Re-read the same message's
				// durable target before building another complete local candidate.
				return p.acceptRGB11MailboxAttempt(ctx, record, metadataRecord, raw, mode, sendACK, walletID, scope, attempt+1)
			}
		}
		return nil, nil, err
	}
	expectedState := *state
	expectedRequest, err := p.rgbManager.engine.LoadReceive(state.ReceiveRequestID)
	if err != nil || !rgb11AddressACKRequestMatchesState(expectedRequest, state,
		receiverID, receipt.ConsignmentHash, witnessTxID, pkScript) {
		return nil, nil, ErrRGB11AddressMailbox
	}
	// ACK means the receiver promises it can recover the prepared transition.
	// Make that promise true before sending the ACK, never afterwards.
	if owner := p.accountManagementOwner(); owner != nil {
		if err := p.syncRGB11OperationRecovery(context.Background()); err != nil {
			return nil, nil, err
		}
	}
	state, err = p.rgbManager.projectionStore.LoadTransferState(localTransferID)
	if err != nil || !rgb11AddressACKStateIdentityMatches(&expectedState, state,
		senderID, receiverID, messageID, receipt.ConsignmentHash, witnessTxID, allocation.OutPoint) {
		return nil, nil, ErrRGB11AddressMailbox
	}
	request, err = p.rgbManager.engine.LoadReceive(state.ReceiveRequestID)
	if err != nil || !sameRGB11AddressReceiveRequest(expectedRequest, request) ||
		!rgb11AddressACKRequestMatchesState(request, state,
			receiverID, receipt.ConsignmentHash, witnessTxID, pkScript) {
		return nil, nil, ErrRGB11AddressMailbox
	}
	ackBaseline := *state
	ackRecord, err := sendACK(senderID, messageID,
		RGB11AddressACK{Status: RGB11AddressACKAccepted})
	if err != nil {
		return nil, nil, err
	}
	state.AckStatus = "ack-sent"
	state.DeliveryAcknowledged = true
	if err := p.saveRGB11ReceiveACK(&ackBaseline, state, request, "new"); err != nil {
		return nil, nil, err
	}
	if err := p.autoBackupRGB11AfterMutation(); err != nil {
		return nil, nil, err
	}
	return accepted, ackRecord, nil
}

// A concurrent receive may finish the same ACK while this caller sends it.
// Only an exact local result completes this mark; never repeat the network ACK.
func (p *rgb11Manager) saveRGB11ReceiveACK(expected, desired *rgb11wallet.TransferState,
	request *corewallet.ReceiveRequest, phase string) error {
	err := p.rgbManager.projectionStore.CompareAndSaveTransferState(expected, desired)
	if !errors.Is(err, rgb11wallet.ErrLocalSnapshotChanged) {
		return err
	}
	current, readErr := p.rgbManager.projectionStore.LoadTransferState(desired.TransferID)
	wanted, wantErr := rgb11wallet.UpsertProjectionSnapshotTransfer(nil, desired)
	observed, currentErr := rgb11wallet.UpsertProjectionSnapshotTransfer(nil, current)
	currentRequest, requestErr := p.rgbManager.engine.LoadReceive(desired.ReceiveRequestID)
	requestEqual := requestErr == nil && request != nil && currentRequest != nil &&
		request.Status == currentRequest.Status && request.FailureCode == currentRequest.FailureCode &&
		sameRGB11AddressReceiveRequest(request, currentRequest)
	stateEqual := readErr == nil && wantErr == nil && currentErr == nil &&
		len(wanted) == 1 && len(observed) == 1 && sameRGB11SnapshotRecords(wanted, observed)
	owner := p.rgb11ReservationOwner()
	scopeTag := accountTraceScope(p.rgb11ScopeKey())
	if stateEqual && requestEqual && desired.Direction == "receive" && desired.AddressMode &&
		desired.AckStatus == "ack-sent" && desired.DeliveryAcknowledged {
		Log.Infof("RGB receive ACK mark already complete: manager=%d scope_tag=%s phase=%s message_tag=%s transfer_tag=%s full_state_equal=true request_equal=true",
			owner.debugID, scopeTag, phase, accountTraceScope(desired.AddressMessageID), accountTraceScope(desired.TransferID))
		return nil
	}
	Log.Warnf("RGB receive ACK mark conflict: manager=%d scope_tag=%s phase=%s message_tag=%s transfer_tag=%s full_state_equal=%t request_equal=%t read_error=%T desired_encode_error=%T current_encode_error=%T request_read_error=%T",
		owner.debugID, scopeTag, phase, accountTraceScope(desired.AddressMessageID), accountTraceScope(desired.TransferID),
		stateEqual, requestEqual, readErr, wantErr, currentErr, requestErr)
	if wantErr == nil && currentErr == nil {
		logRGB11LifeDiff(owner.debugID, scopeTag, wanted, observed)
	}
	return err
}

func (p *rgb11Manager) sendRGB11AddressACKDirect(senderAccountID, messageID string,
	ack RGB11AddressACK, options RGB11AddressDeliveryOptions) (*swire.DKVSRecord, error) {
	if p == nil || p.wallet == nil || senderAccountID == "" || messageID == "" {
		return nil, ErrRGB11AddressMailbox
	}
	body, err := rgb11wallet.EncodeAddressACK(ack)
	if err != nil {
		return nil, err
	}
	message, err := p.accountManagementOwner().sendWalletDirectMessage(
		p.wallet, messageID, AccountMessageKindRGB11ACK, senderAccountID, body,
	)
	if err != nil {
		return nil, err
	}
	encoded, err := swire.SerializeDirectMessage(message, true)
	if err != nil {
		return nil, err
	}
	key, err := dkvsindexer.MailMsgKey(
		senderAccountID, message.SenderAccount, message.MessageID,
	)
	if err != nil {
		return nil, err
	}
	_ = options
	return &swire.DKVSRecord{Version: dkvsindexer.Version, Key: key, Value: encoded, Seq: 1}, nil
}

// AcceptRGB11AddressACK records receiver persistence. Delivery cache is only
// compacted once the witness transaction is also confirmed.
func (p *rgb11Manager) AcceptRGB11AddressACK(record *swire.DKVSRecord,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11AddressACK, error) {
	_ = verify
	if p == nil || record == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return nil, ErrRGB11AddressMailbox
	}
	if direct, err := decodeWalletDirectRecord(p.wallet, record); err == nil && direct != nil &&
		direct.Payload != nil && direct.Payload.Kind == AccountMessageKindRGB11ACK {
		return p.acceptRGB11AddressACKDirect(direct)
	}
	return nil, ErrRGB11AddressMailbox
}

func (p *rgb11Manager) acceptRGB11AddressACKDirect(item *AccountDirectMessage) (*RGB11AddressACK, error) {
	if p == nil || item == nil || item.Payload == nil || item.Direct == nil ||
		item.Payload.Kind != AccountMessageKindRGB11ACK {
		return nil, ErrRGB11AddressMailbox
	}
	ack, err := rgb11wallet.DecodeAddressACK(item.Payload.Body)
	if err != nil {
		return nil, err
	}
	return p.acceptRGB11AddressACKDecoded(
		item.Direct.RecipientAccount, item.Direct.SenderAccount,
		item.Payload.ApplicationID, ack,
	)
}

func (p *rgb11Manager) acceptRGB11AddressACKDecoded(senderID, receiverID, messageID string,
	ack RGB11AddressACK) (*RGB11AddressACK, error) {
	localID, err := dkvsAccountID(p.wallet)
	if err != nil || senderID != localID {
		return nil, ErrRGB11AddressMailbox
	}
	switch ack.Status {
	case RGB11AddressACKAccepted, RGB11AddressACKNeedResend, RGB11AddressACKRejected:
	default:
		return nil, ErrRGB11AddressMailbox
	}
	states, err := p.rgbManager.projectionStore.ListTransfers()
	if err != nil {
		return nil, err
	}
	var pending *rgb11wallet.PendingTransfer
	for _, state := range states {
		if state == nil || !state.AddressMode || state.Direction != "send" {
			continue
		}
		// TransferID is the persistent protocol identity. Terminal recovery
		// intentionally omits the redundant runtime delivery/ACK cache.
		expectedID, err := rgb11AddressMessageID(state.TransferID)
		if err != nil || expectedID != messageID {
			continue
		}
		pending, err = p.rgbManager.projectionStore.LoadPendingTransfer(state.TransferID)
		if err != nil {
			return nil, err
		}
		break
	}
	if pending == nil || !pending.State.AddressMode || pending.State.Direction != "send" ||
		pending.State.ReceiverAccountID != receiverID || pending.State.SenderAccountID != senderID {
		return nil, ErrRGB11AddressMailbox
	}
	expectedID, err := rgb11AddressMessageID(pending.State.TransferID)
	if err != nil || expectedID != messageID {
		return nil, ErrRGB11AddressMailbox
	}
	switch pending.State.Status {
	case "settled", "rejected", "orphaned":
		// Authenticated late ACKs cannot change a terminal journal, recreate
		// its delivery cache, stage a backup mutation or resume a broadcast.
		return &ack, nil
	}
	// Active obligations still retain and require their exact delivery binding.
	if pending.State.AddressMessageID != messageID {
		return nil, ErrRGB11AddressMailbox
	}
	baseline := cloneRGB11BroadcastIntent(pending)
	if ack.Status != RGB11AddressACKAccepted {
		pending.State.AckStatus = "need-resend"
		if ack.Status == RGB11AddressACKRejected {
			if pending.State.Status == "delivered" {
				batch, err := p.loadRGB11AddressBatch(pending)
				if err != nil {
					return nil, err
				}
				if err := p.cancelRGB11RejectedPendingBatch(
					batch, "recipient-rejected", nil,
				); err != nil {
					return nil, err
				}
				return &ack, nil
			}
			pending.State.AckStatus = "rejected-after-broadcast"
		}
		if err := p.rgbManager.projectionStore.CompareAndSavePendingTransferStates([]*rgb11wallet.PendingTransfer{baseline}, []*rgb11wallet.PendingTransfer{pending}); err != nil {
			return nil, err
		}
		return &ack, nil
	}
	pending.State.AckStatus = "accepted"
	pending.State.DeliveryAcknowledged = true
	if err := p.rgbManager.projectionStore.CompareAndSavePendingTransferStates([]*rgb11wallet.PendingTransfer{baseline}, []*rgb11wallet.PendingTransfer{pending}); err != nil {
		return nil, err
	}
	if err := p.autoBackupRGB11AfterMutation(); err != nil {
		return nil, err
	}
	if rgb11BroadcastCompleteStatus(pending.State.Status) {
		if err := p.compactRGB11AddressDeliveryIfFinal(pending); err != nil {
			return nil, err
		}
	}
	return &ack, nil
}

func (p *rgb11Manager) compactRGB11AddressDeliveryIfFinal(pending *rgb11wallet.PendingTransfer) error {
	if pending == nil || !pending.State.AddressMode || !pending.State.DeliveryAcknowledged {
		return nil
	}
	baseline := cloneRGB11BroadcastIntent(pending)
	status, err := p.rgbManager.evidence.GetTxStatus(pending.State.WitnessTxID)
	if err != nil || status == nil || !status.Confirmed ||
		status.Confirmations < int64(pending.State.MinConfirmations) {
		return err
	}
	pending.State.Status = "settled"
	pending.State.DeliveryCacheCompacted = true
	if err := p.rgbManager.projectionStore.CompareAndSavePendingTransferStates([]*rgb11wallet.PendingTransfer{baseline}, []*rgb11wallet.PendingTransfer{pending}); err != nil {
		return err
	}
	if err := p.finalizeRGB11PendingChangeReservation(pending); err != nil {
		return err
	}
	return p.rgbManager.projectionStore.CompactSettledRecipientConsignments([]string{pending.State.TransferID})
}

// RefreshRGB11AddressACK scans only messages sent by the expected receiver to
// the local mailbox. General mailbox subscription and pagination remain DKVS
// concerns; this helper processes one fetched ACK record at a time.
func (p *rgb11Manager) RefreshRGB11AddressACK(record *swire.DKVSRecord,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11AddressACK, error) {
	return p.AcceptRGB11AddressACK(record, verify)
}

// RGB11AddressCarrierWarning is exposed to UI layers whenever an address owns
// RGB allocations. Spending the carrier with a wallet that does not understand
// RGB may permanently destroy the asset state.
const RGB11AddressCarrierWarning = "This address contains RGB11 assets. Spending its UTXO with a non-RGB-aware wallet may permanently destroy those assets."

func stringEqual(a, b []byte) bool { return string(a) == string(b) }

var _ = indexer.AssetName{}

const RGB11AddressTransport = "address-dkvs"

// rgb11AddressMessageID converts the protocol-defined canonical RGB
// Consignment/Transfer ID into a DKVS-safe representation of that same ID. It
// decodes BAID64 and renders the existing 32-byte identifier as lowercase hex;
// it never hashes the transfer contents or the textual ID to invent a new ID.
func rgb11AddressMessageID(transferID string) (string, error) {
	transferID = strings.TrimSpace(transferID)
	if transferID == "" {
		return "", ErrRGB11AddressMailbox
	}
	// Native batch children already use a domain-separated 32-byte hex ID.
	if len(transferID) == 64 && transferID == strings.ToLower(transferID) {
		if decoded, err := hex.DecodeString(transferID); err == nil && len(decoded) == 32 {
			return transferID, nil
		}
	}
	canonical, err := baid64.Decode32(transferID, baid64.ConsignmentIDOptions())
	if err != nil {
		return "", ErrRGB11AddressMailbox
	}
	return hex.EncodeToString(canonical[:]), nil
}

func (p *rgb11Manager) synthesizeRGB11AddressInvoice(endpoint *RGB11AddressEndpoint, asset indexer.AssetName,
	amount uint64, expiry int64) (string, error) {
	if endpoint == nil || endpoint.AccountID == "" || len(endpoint.PkScript) == 0 || amount == 0 {
		return "", ErrRGB11TraditionalReceiveRequired
	}
	officialID, err := p.rgb11ContractIDForAssetName(asset)
	if err != nil {
		return "", err
	}
	contractID, err := consensus.ParseContractID(officialID)
	if err != nil {
		return "", err
	}
	xonly, err := hex.DecodeString(endpoint.AccountID)
	if err != nil || len(xonly) != 32 {
		return "", ErrRGB11TraditionalReceiveRequired
	}
	var internal [32]byte
	copy(internal[:], xonly)
	beneficiary, err := invoicing.NewWitnessBeneficiary(
		rgb11InvoiceNetwork(GetChainParam()), endpoint.PkScript, &internal,
	)
	if err != nil {
		return "", err
	}
	invoice := invoicing.Invoice{
		Contract:    &contractID,
		Assignment:  &invoicing.InvoiceState{Kind: invoicing.StateAmount, Amount: invoicing.Amount(amount)},
		Beneficiary: beneficiary,
		Expiry:      &expiry,
	}
	if err := invoice.Validate(time.Now().Unix()); err != nil {
		return "", err
	}
	return invoice.String(), nil
}

func (p *rgb11Manager) prepareRGB11AddressTransferForEndpoint(ctx context.Context,
	request RGB11AddressSendRequest, endpoint *RGB11AddressEndpoint) (
	*RGB11PreparedTransfer, *RGB11AddressEndpoint, error,
) {
	if p == nil || endpoint == nil || request.ReceiverAddress == "" ||
		request.AssetName.Protocol != rgb11wallet.Protocol {
		return nil, nil, ErrRGB11TraditionalReceiveRequired
	}
	if endpoint.CapabilityFlags&RGB11ReceiveCapabilityAny == 0 {
		return nil, nil, ErrRGB11TraditionalReceiveRequired
	}
	amount, err := strconv.ParseUint(request.AmountRaw, 10, 64)
	if err != nil || amount == 0 {
		return nil, nil, fmt.Errorf("invalid RGB11 amount")
	}
	if request.Expiry == 0 {
		request.Expiry = time.Now().Add(24 * time.Hour).Unix()
	}
	invoice, err := p.synthesizeRGB11AddressInvoice(endpoint, request.AssetName, amount, request.Expiry)
	if err != nil {
		return nil, nil, err
	}
	prepared, err := p.prepareRGB11TransferForEndpoints(ctx, RGB11SendRequest{
		Invoice:          invoice,
		FeeRate:          request.FeeRate,
		MinConfirmations: request.MinConfirmations,
	}, false, []*RGB11AddressEndpoint{endpoint})
	if err != nil {
		return nil, nil, err
	}

	return prepared, endpoint, nil
}

const (
	rgb11WalletSnapshotVersion  = rgb11wallet.WalletSnapshotVersion
	rgb11WalletStorageNamespace = "rgb11-"
)

func (p *rgb11Manager) RGB11WalletID() (string, error) {
	if p == nil || p.wallet == nil || p.wallet.GetPubKey() == nil {
		return "", ErrRGB11WalletLocked
	}
	return rgb11WalletStorageNamespace + hex.EncodeToString(p.wallet.GetPubKey().SerializeCompressed()), nil
}

func (p *rgb11Manager) exportRGB11WalletSnapshot(walletID string) (*RGB11WalletSnapshot, []byte, error) {
	return p.readRGB11StrictSnapshot(walletID, false)
}

// Keep the existing strict export validation and proof-fault gate. A local
// publisher additionally needs the exact raw DB baseline, including rehydrated
// pending payloads. Both local reads share the same reservation read gate;
// the raw record values are never derived by re-encoding the backup view.
func (p *rgb11Manager) readRGB11StrictSnapshot(walletID string, rawLocal bool) (*RGB11WalletSnapshot, []byte, error) {
	snapshot, _, err := p.readRGB11StrictViews(walletID, rawLocal)
	return snapshot, nil, err
}

// Retain the normalized backup beside the selected local view from the same
// strict read. Recovery comparisons use backup bytes; atomic CAS uses raw bytes.
func (p *rgb11Manager) readRGB11StrictViews(walletID string, rawLocal bool) (*RGB11WalletSnapshot, *RGB11WalletSnapshot, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || p.rgbManager.engineStore == nil || p.wallet == nil || walletID == "" {
		return nil, nil, ErrRGB11Inconsistent
	}
	releaseRGB11Snapshot := p.beginRGB11ReservationRead()
	defer releaseRGB11Snapshot()
	snapshot, _, err := p.exportRGB11WalletSnapshotLocked(walletID)
	backup := snapshot
	if err == nil {
		faults := p.rgb11ScopeState().ProofFaults
		if len(faults) != 0 {
			causes := []error{ErrRGB11Inconsistent}
			for _, fault := range faults {
				causes = append(causes, fault)
			}
			return nil, nil, fmt.Errorf("complete RGB11 backup pending: %w", errors.Join(causes...))
		}
	}
	if err == nil && rawLocal {
		snapshot, err = p.readRGB11LocalLocked(walletID)
	}
	if err != nil {
		// A strict backup failure must not revoke independently fresh A.
		// Unknown/unreadable material still closes the entire scope gate.
		local, readErr := p.readRGB11LocalLocked(walletID)
		var view *rgb11wallet.LocalProjection
		if readErr == nil {
			view, readErr = rgb11wallet.SplitLocalProjection(local.ProjectionRecords)
		}
		if readErr != nil || len(view.Faults) == 0 {
			p.markRGB11ProjectionUnavailable()
		} else {
			p.noteRGB11ProofFaults(view)
		}
		p.consistencyStatus = "broken"
	}
	return snapshot, backup, err
}

// exportRGB11WalletSnapshotLocked is local-only. The caller owns the shared
// reservation read/commit gate, so a commit-time baseline check does not try
// to reacquire that same RWMutex.
func (p *rgb11Manager) exportRGB11WalletSnapshotLocked(walletID string) (*RGB11WalletSnapshot, []byte, error) {
	projection, err := p.rgbManager.projectionStore.ExportSnapshot()
	if err != nil {
		return nil, nil, err
	}
	engine, err := p.rgbManager.engineStore.ExportSnapshot()
	if err != nil {
		return nil, nil, err
	}
	snapshot := &RGB11WalletSnapshot{
		Version: rgb11WalletSnapshotVersion, WalletID: walletID,
		AccountIndex: p.wallet.GetSubAccount(), EngineBuildID: rgb11wallet.NativeEngineBuildID,
		ProjectionRecords: projection, EngineRecords: engine,
	}
	return snapshot, nil, nil
}

func (p *rgb11Manager) accountManagementOwner() *Manager {
	if p == nil {
		return nil
	}
	if p.accountOwner != nil {
		return p.accountOwner
	}
	return p.Manager
}

func (p *rgb11Manager) hasActiveRGB11Transition() (bool, error) {
	if p == nil {
		return false, ErrRGB11Inconsistent
	}
	walletID, err := p.RGB11WalletID()
	if err != nil {
		return false, err
	}
	full, _, err := p.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		return false, err
	}
	active, err := rgb11wallet.ActiveRecoveryPackageFromSnapshot(full)
	if err != nil {
		return false, err
	}
	return rgb11wallet.ActiveRecoveryPackageHasTransition(active), nil
}

// autoBackupRGB11AfterMutation marks the complete stable recovery closure
// dirty. Pending RGB material is part of that closure; an active mailbox is
// only an optional cache and is never the recovery authority.
func (p *rgb11Manager) autoBackupRGB11AfterMutation() error {
	if owner := p.accountManagementOwner(); owner != nil {
		// The reservation transaction already persisted the dirty generation
		// with its business records. This hook only coalesces background work.
		if inOperation, _ := p.Manager.noteRGB11ManagedOperationMutation(); !inOperation {
			owner.scheduleAccountManagedStateSync()
		}
	}
	return nil
}

func (p *rgb11Manager) ResumeRGB11PreparedTransfer(transferID string) (*RGB11PreparedTransferPackage, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return nil, ErrRGB11Inconsistent
	}
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return nil, err
	}
	if pending.State.TransportMode != RGB11AddressTransport &&
		pending.State.TransportMode != RGB11ProxyTransport &&
		pending.State.TransportMode != "out-of-band" {
		return nil, ErrRGB11Inconsistent
	}
	if pending.State.Direction != "send" ||
		(pending.State.Status != "prepared" && pending.State.Status != "delivered" &&
			pending.State.Status != "relayed") ||
		len(pending.RecipientConsignment) == 0 || pending.State.WitnessTxID == "" {
		return nil, ErrRGB11Inconsistent
	}
	p.traceRGB11Broadcast("cold-package-loaded", pending.State.WitnessTxID, nil)
	return rgb11RecipientTransferPackage(pending, pending.RecipientConsignment)
}

// ExportRGB11SettledConsignment retrieves the immutable recipient object after
// runtime delivery bytes have been compacted. It never resumes or changes the
// settled transfer, so a sender can hand the same consignment to a receiver
// whose out-of-band delivery was missed before account recovery.
func (p *rgb11Manager) ExportRGB11SettledConsignment(transferID string) (*RGB11PreparedTransferPackage, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return nil, ErrRGB11Inconsistent
	}
	store := p.rgbManager.projectionStore
	pending, err := store.LoadPendingTransfer(transferID)
	if err != nil {
		return nil, err
	}
	if pending.State.TransportMode != "out-of-band" || pending.State.Direction != "send" ||
		pending.State.Status != "settled" || pending.State.WitnessTxID == "" ||
		pending.State.ConsignmentHash == "" || pending.RecipientObjectHash != pending.State.ConsignmentHash {
		return nil, ErrRGB11Inconsistent
	}
	recipient, err := store.LoadObject(pending.RecipientObjectHash)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(recipient)
	if hex.EncodeToString(hash[:]) != pending.RecipientObjectHash {
		return nil, ErrRGB11Inconsistent
	}
	if err := validateRGB11PendingTransaction(pending); err != nil {
		return nil, err
	}
	decoded, err := coreconsignment.DecodeArmor(string(recipient))
	if err != nil || decoded == nil || decoded.Armor == nil || decoded.Armor.Type != "transfer" {
		return nil, ErrRGB11Inconsistent
	}
	if err := rgb11SettledRecipientMatches(pending, decoded); err != nil {
		return nil, err
	}
	// The recipient needs the invoice, amount and public witness, not the
	// sender's batch siblings, local outputs or delivery bookkeeping.
	selected := *pending
	selected.State = rgb11wallet.TransferState{
		TransferID: pending.State.TransferID, Direction: pending.State.Direction,
		Status: pending.State.Status, TransportMode: pending.State.TransportMode,
		Asset: pending.State.Asset, RecipientID: pending.State.RecipientID,
		RecipientVout: pending.State.RecipientVout, Invoice: pending.State.Invoice,
		ConsignmentHash: pending.State.ConsignmentHash,
		WitnessTxID:     pending.State.WitnessTxID,
	}
	return rgb11RecipientTransferPackage(&selected, recipient)
}

// This is a local proof-binding check for historical delivery, not a new
// chain-validation or receive path. No remote evidence or wallet-owned seal is
// required to hand the original recipient its existing transfer object.
func rgb11SettledRecipientMatches(pending *rgb11wallet.PendingTransfer,
	decoded *coreconsignment.Container) error {
	if pending == nil || decoded == nil || decoded.Armor == nil {
		return ErrRGB11Inconsistent
	}
	invoice, err := invoicing.Parse(pending.State.Invoice)
	if err != nil {
		return ErrRGB11InvoiceMismatch
	}
	storedAmount, err := decimalUint64(&pending.State.Asset.Amount)
	if err != nil || storedAmount == 0 {
		return ErrRGB11InvoiceMismatch
	}
	storedContract, err := consensus.ParseContractID(decoded.ContractID)
	if err != nil {
		return ErrRGB11InvoiceMismatch
	}
	contract, amount, recipientID, _, _, transport, err := validateRGB11SendInvoice(invoice, &storedContract, &storedAmount)
	if err != nil || contract.String() != decoded.ContractID || transport != "out-of-band" ||
		recipientID != pending.State.RecipientID ||
		!rgb11wallet.ContractAssetKeyMatches(pending.State.Asset.Name, decoded.ContractID) {
		return ErrRGB11InvoiceMismatch
	}
	if storedAmount != amount {
		return ErrRGB11InvoiceMismatch
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	if tx.Deserialize(bytes.NewReader(pending.SignedTx)) != nil {
		return ErrRGB11Inconsistent
	}
	if invoice.Beneficiary.Kind == invoicing.BeneficiaryWitnessVout {
		script, err := invoice.Beneficiary.WitnessScript()
		if err != nil || pending.State.RecipientVout == 0 ||
			int(pending.State.RecipientVout) >= len(tx.TxOut) ||
			!bytes.Equal(script, tx.TxOut[pending.State.RecipientVout].PkScript) {
			return ErrRGB11InvoiceMismatch
		}
	} else if pending.State.RecipientVout != 0 {
		return ErrRGB11InvoiceMismatch
	}
	bundles, ok := decoded.Value.Field("bundles")
	bundles = bundles.Unwrap()
	if !ok || bundles.Kind != strict_types.ValueList || len(bundles.Items) == 0 {
		return ErrRGB11Inconsistent
	}
	last := bundles.Items[len(bundles.Items)-1].Unwrap()
	witness, witnessOK := last.Field("pubWitness")
	bundleValue, bundleOK := last.Field("bundle")
	if !witnessOK || !bundleOK || !rgb11EmbeddedWitnessMatches(witness, tx) {
		return ErrRGB11Inconsistent
	}
	bundle, err := operations.CommitBundle(bundleValue)
	if err != nil {
		return ErrRGB11Inconsistent
	}
	matches := 0
	for _, transition := range bundle.Transitions {
		contractField, ok := transition.Field("contractId")
		contractBytes, bytesOK := contractField.Bytes()
		if !ok || !bytesOK || !bytes.Equal(contractBytes, contract[:]) {
			return ErrRGB11Inconsistent
		}
		assignments, ok := transition.Field("assignments")
		assignments = assignments.Unwrap()
		if !ok || assignments.Kind != strict_types.ValueMap {
			return ErrRGB11Inconsistent
		}
		for _, group := range assignments.Entries {
			assignmentType, ok := rgb11StrictUint(group.Key)
			if !ok || assignmentType != 4000 {
				continue
			}
			typed := group.Value.Unwrap()
			if typed.Kind != strict_types.ValueUnion || typed.Inner == nil {
				return ErrRGB11Inconsistent
			}
			items := typed.Inner.Unwrap()
			if items.Kind != strict_types.ValueList && items.Kind != strict_types.ValueSet {
				return ErrRGB11Inconsistent
			}
			for _, item := range items.Items {
				item = item.Unwrap()
				if item.Kind != strict_types.ValueUnion || item.Inner == nil {
					return ErrRGB11Inconsistent
				}
				fields := item.Inner.Unwrap()
				sealValue, sealOK := fields.Field("seal")
				stateValue, stateOK := fields.Field("state")
				if !sealOK || !stateOK || !rgb11SettledAmountMatches(stateValue, typed.Name, amount) {
					continue
				}
				switch invoice.Beneficiary.Kind {
				case invoicing.BeneficiaryBlindedSeal:
					secret, ok := sealValue.Bytes()
					if item.Name == "confidentialSeal" && ok &&
						bytes.Equal(secret, invoice.Beneficiary.BlindedSeal[:]) {
						matches++
					}
				case invoicing.BeneficiaryWitnessVout:
					graph, err := seals.DecodeGraphBlindSeal(sealValue.Encoded)
					if item.Name == "revealed" && err == nil && graph.TxID == nil &&
						graph.Vout == pending.State.RecipientVout {
						matches++
					}
				}
			}
		}
	}
	if matches != 1 {
		return ErrRGB11InvoiceMismatch
	}
	return nil
}

func rgb11SettledAmountMatches(value strict_types.Value, class string, amount uint64) bool {
	if class == "structured" {
		return amount == 1
	}
	value = value.Unwrap()
	if class != "fungible" || value.Kind != strict_types.ValueUnion || value.Tag != 8 || value.Inner == nil {
		return false
	}
	actual, ok := rgb11StrictUint(*value.Inner)
	return ok && actual == amount
}

func rgb11StrictUint(value strict_types.Value) (uint64, bool) {
	value = value.Unwrap()
	if value.Kind == strict_types.ValueUnion && value.Inner != nil {
		return rgb11StrictUint(*value.Inner)
	}
	return value.Uint64()
}

// Match the embedded public witness to the original locally retained tx.
// RGB commitments bind its txid; the output check above then binds vout/script.
func rgb11EmbeddedWitnessMatches(value strict_types.Value, tx *wire.MsgTx) bool {
	value = value.Unwrap()
	if tx == nil || value.Kind != strict_types.ValueUnion || value.Name != "tx" || value.Inner == nil {
		return false
	}
	fields := value.Inner.Unwrap()
	version, versionOK := fields.Field("version")
	lockTime, lockOK := fields.Field("lockTime")
	inputs, inputsOK := fields.Field("inputs")
	outputs, outputsOK := fields.Field("outputs")
	inputs, outputs = inputs.Unwrap(), outputs.Unwrap()
	version = version.Unwrap()
	versionNumber := int64(0)
	if version.Signed != nil {
		versionNumber = *version.Signed
	} else if number, ok := version.Uint64(); ok && number <= 1<<31-1 {
		versionNumber = int64(number)
	} else {
		return false
	}
	lockNumber, validLock := rgb11StrictUint(lockTime)
	if !versionOK || !lockOK || !inputsOK || !outputsOK || !validLock ||
		versionNumber != int64(tx.Version) || lockNumber != uint64(tx.LockTime) ||
		inputs.Kind != strict_types.ValueList || outputs.Kind != strict_types.ValueList ||
		len(inputs.Items) != len(tx.TxIn) || len(outputs.Items) != len(tx.TxOut) {
		return false
	}
	for index, entry := range inputs.Items {
		entry = entry.Unwrap()
		prev, prevOK := entry.Field("prevOutput")
		scriptValue, scriptOK := entry.Field("sigScript")
		sequenceValue, sequenceOK := entry.Field("sequence")
		prev = prev.Unwrap()
		hashValue, hashOK := prev.Field("txid")
		voutValue, voutOK := prev.Field("vout")
		hash, hashBytesOK := hashValue.Bytes()
		vout, validVout := rgb11StrictUint(voutValue)
		sequence, validSequence := rgb11StrictUint(sequenceValue)
		script, scriptBytesOK := scriptValue.Bytes()
		if !prevOK || !scriptOK || !sequenceOK || !hashOK || !voutOK || !hashBytesOK ||
			!validVout || !validSequence || !scriptBytesOK ||
			!bytes.Equal(hash, tx.TxIn[index].PreviousOutPoint.Hash[:]) ||
			vout != uint64(tx.TxIn[index].PreviousOutPoint.Index) ||
			sequence != uint64(tx.TxIn[index].Sequence) ||
			!bytes.Equal(script, tx.TxIn[index].SignatureScript) {
			return false
		}
	}
	for index, entry := range outputs.Items {
		entry = entry.Unwrap()
		amountValue, amountOK := entry.Field("value")
		scriptValue, scriptOK := entry.Field("scriptPubkey")
		amount, validAmount := rgb11StrictUint(amountValue)
		script, scriptBytesOK := scriptValue.Bytes()
		if !amountOK || !scriptOK || !validAmount || !scriptBytesOK ||
			amount != uint64(tx.TxOut[index].Value) || !bytes.Equal(script, tx.TxOut[index].PkScript) {
			return false
		}
	}
	return true
}

func rgb11RecipientTransferPackage(pending *rgb11wallet.PendingTransfer, recipient []byte) (*RGB11PreparedTransferPackage, error) {
	decoded, err := coreconsignment.DecodeArmor(string(recipient))
	if err != nil {
		return nil, err
	}
	if decoded.Armor == nil || decoded.Armor.Type != "transfer" {
		return nil, ErrRGB11Inconsistent
	}
	transferFile, err := coreconsignment.EncodeFile(decoded)
	if err != nil {
		return nil, err
	}
	state := pending.State
	state.InputOutPoints = append([]string(nil), pending.State.InputOutPoints...)
	state.OutputOutPoints = append([]string(nil), pending.State.OutputOutPoints...)
	state.BatchTransferIDs = append([]string(nil), pending.State.BatchTransferIDs...)
	return &RGB11PreparedTransferPackage{
		State: &state, RecipientConsignment: string(recipient),
		RecipientConsignmentBase64: base64.StdEncoding.EncodeToString(transferFile),
		TxID:                       pending.State.WitnessTxID,
	}, nil
}

func (p *rgb11Manager) BroadcastRGB11OutOfBand(transferIDs []string) (string, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil ||
		p.rgbManager.evidence == nil || len(transferIDs) == 0 {
		return "", ErrRGB11BatchAckRequired
	}
	pendingList := make([]*rgb11wallet.PendingTransfer, 0, len(transferIDs))
	seen := make(map[string]struct{}, len(transferIDs))
	for _, transferID := range transferIDs {
		if _, ok := seen[transferID]; transferID == "" || ok {
			return "", ErrRGB11BatchAckRequired
		}
		seen[transferID] = struct{}{}
		pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
		if err != nil {
			return "", err
		}
		pendingList = append(pendingList, pending)
	}
	first := pendingList[0]
	expectedIDs := first.State.BatchTransferIDs
	if len(expectedIDs) == 0 {
		expectedIDs = []string{first.State.TransferID}
	}
	if len(expectedIDs) != len(transferIDs) {
		return "", ErrRGB11BatchAckRequired
	}
	for _, expected := range expectedIDs {
		if _, ok := seen[expected]; !ok {
			return "", ErrRGB11BatchAckRequired
		}
	}
	baseline := cloneRGB11PendingBatch(pendingList)
	allComplete := true
	for _, pending := range pendingList {
		if pending.State.TransportMode != "out-of-band" ||
			pending.State.WitnessTxID != first.State.WitnessTxID || pending.State.BatchID != first.State.BatchID ||
			!bytes.Equal(pending.SignedTx, first.SignedTx) {
			return "", ErrRGB11BatchAckRequired
		}
		if rgb11BroadcastCompleteStatus(pending.State.Status) {
			continue
		}
		allComplete = false
		if pending.State.Status != "prepared" && pending.State.Status != rgb11StatusBroadcastAttempted {
			return "", ErrRGB11BatchAckRequired
		}
		if pending.State.Expiry <= time.Now().Unix() {
			return "", ErrRGB11BatchAckRequired
		}
		pending.State.AckStatus = "accepted-out-of-band"
	}
	if allComplete {
		return first.State.WitnessTxID, nil
	}
	// Persist the user's out-of-band acceptance before the irreversible
	// broadcast intent. A storage failure here remains safely retryable.
	if err := p.rgbManager.projectionStore.CompareAndSavePendingTransferStates(baseline, pendingList); err != nil {
		return "", err
	}
	return p.broadcastRGB11PendingBatch(pendingList, nil)
}

func (p *rgb11Manager) CancelRGB11OutOfBandTransfer(transferID string) error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || transferID == "" {
		return ErrRGB11OutOfBandRequired
	}
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return err
	}
	ids := pending.State.BatchTransferIDs
	if len(ids) == 0 {
		ids = []string{pending.State.TransferID}
	}
	pendingList := make([]*rgb11wallet.PendingTransfer, 0, len(ids))
	for _, id := range ids {
		item, err := p.rgbManager.projectionStore.LoadPendingTransfer(id)
		if err != nil {
			return err
		}
		if item.State.TransportMode != "out-of-band" || item.State.Status != "prepared" ||
			item.State.WitnessTxID != pending.State.WitnessTxID || item.State.BatchID != pending.State.BatchID {
			return ErrRGB11OutOfBandRequired
		}
		pendingList = append(pendingList, item)
	}
	// A standard acknowledged consignment contains only the unsigned public
	// witness. While every batch member is still prepared, the signed
	// transaction has never crossed the durable broadcast-attempted boundary
	// and only exists in this sender store, so cancellation is entirely local.
	// Once broadcast has been attempted the status check above rejects the
	// operation and chain reconciliation owns the outcome.
	return p.cancelRGB11PendingBatch(pendingList, RGB11RejectReasonUser, nil)
}

const rgb11RejectReasonInvoiceExpired = "invoice-expired"

func equalRGB11StringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	values := make(map[string]struct{}, len(left))
	for _, value := range left {
		if value == "" {
			return false
		}
		if _, exists := values[value]; exists {
			return false
		}
		values[value] = struct{}{}
	}
	for _, value := range right {
		if _, exists := values[value]; !exists {
			return false
		}
	}
	return true
}

func rgb11ObjectHash(raw []byte) string {
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func (p *rgb11Manager) loadExpiredRGB11Batch(transferID string, now int64) (
	[]*rgb11wallet.PendingTransfer, bool, error,
) {
	first, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return nil, false, err
	}
	ids := append([]string(nil), first.State.BatchTransferIDs...)
	if len(ids) == 0 {
		ids = []string{first.State.TransferID}
	}
	if len(ids) == 0 || !slices.Contains(ids, transferID) {
		return nil, false, fmt.Errorf("%w: invalid pending batch membership", ErrRGB11Inconsistent)
	}
	pendingList := make([]*rgb11wallet.PendingTransfer, 0, len(ids))
	allTerminal := true
	for _, id := range ids {
		pending, loadErr := p.rgbManager.projectionStore.LoadPendingTransfer(id)
		if loadErr != nil {
			return nil, false, loadErr
		}
		if pending.State.Status != "rejected" || pending.State.RejectReason != rgb11RejectReasonInvoiceExpired {
			allTerminal = false
		}
		pendingList = append(pendingList, pending)
	}
	if allTerminal {
		locks := p.utxoLockerL1.GetLockedUtxoList()
		for _, pending := range pendingList {
			if pending.State.TransferID == "" || pending.State.Direction != "send" ||
				pending.State.Status != "rejected" || pending.State.AckStatus != "rejected" ||
				pending.State.RejectReason != rgb11RejectReasonInvoiceExpired ||
				!pending.State.AddressMode || pending.State.TransportMode != RGB11AddressTransport ||
				pending.State.RelayDurability != "LOCAL_ONLY" ||
				pending.State.WitnessTxID == "" || pending.State.WitnessTxID != first.State.WitnessTxID ||
				pending.State.BatchID != first.State.BatchID ||
				!slices.Equal(pending.State.BatchTransferIDs, first.State.BatchTransferIDs) ||
				pending.State.BatchSize != len(ids) || pending.ReservationID == "" ||
				pending.ReservationID != first.ReservationID || len(pending.State.InputOutPoints) == 0 ||
				len(pending.State.OutputOutPoints) == 0 || pending.State.ConsignmentHash == "" ||
				len(pending.SignedTx) != 0 || len(pending.SignedPSBT) != 0 ||
				len(pending.RecipientConsignment) != 0 || len(pending.LocalConsignment) != 0 ||
				pending.RecipientObjectHash != "" || pending.LocalObjectHash != "" ||
				len(pending.ChangeSeals) != 0 {
				return nil, false, fmt.Errorf("%w: expired batch tombstone mismatch", ErrRGB11Inconsistent)
			}
			for _, lock := range locks {
				if lock != nil && lock.ReservationID == pending.ReservationID {
					return nil, false, fmt.Errorf("%w: expired batch tombstone still owns a reservation", ErrRGB11Inconsistent)
				}
			}
		}
		return pendingList, true, nil
	}
	if first.State.BatchSize != len(ids) || first.State.Direction != "send" ||
		first.State.Status != "prepared" || !first.State.AddressMode ||
		first.State.TransportMode != RGB11AddressTransport ||
		first.State.AckStatus != "awaiting-persistence" || first.State.RelayDurability != "LOCAL_ONLY" {
		return nil, false, ErrRGB11ExpiredCancel
	}
	if !equalRGB11StringSet(ids, first.State.BatchTransferIDs) || first.ReservationID == "" {
		return nil, false, fmt.Errorf("%w: invalid expired batch identity", ErrRGB11Inconsistent)
	}
	if err := validateRGB11PendingTransaction(first); err != nil {
		return nil, false, err
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	reader := bytes.NewReader(first.SignedTx)
	if err := tx.Deserialize(reader); err != nil || reader.Len() != 0 {
		return nil, false, fmt.Errorf("%w: decode expired witness transaction", ErrRGB11Inconsistent)
	}
	actualInputs := make([]string, 0, len(tx.TxIn))
	for _, input := range tx.TxIn {
		actualInputs = append(actualInputs, input.PreviousOutPoint.String())
	}
	packet, err := psbt.NewFromRawBytes(bytes.NewReader(first.SignedPSBT), false)
	if err != nil || packet.UnsignedTx == nil || packet.UnsignedTx.TxHash().String() != first.State.WitnessTxID {
		return nil, false, fmt.Errorf("%w: pending PSBT does not match witness transaction", ErrRGB11Inconsistent)
	}
	seenRelayKeys := make(map[string]struct{}, len(pendingList))
	seenAckKeys := make(map[string]struct{}, len(pendingList))
	for _, pending := range pendingList {
		state := &pending.State
		if state.Direction != "send" || state.Status != "prepared" || !state.AddressMode ||
			state.TransportMode != RGB11AddressTransport || state.AckStatus != "awaiting-persistence" ||
			state.RelayDurability != "LOCAL_ONLY" {
			return nil, false, ErrRGB11ExpiredCancel
		}
		if state.TransferID == "" || state.BatchSize != len(ids) || state.BatchID != first.State.BatchID ||
			!slices.Equal(state.BatchTransferIDs, first.State.BatchTransferIDs) ||
			state.WitnessTxID != first.State.WitnessTxID || pending.ReservationID != first.ReservationID ||
			!bytes.Equal(pending.SignedTx, first.SignedTx) || !bytes.Equal(pending.SignedPSBT, first.SignedPSBT) ||
			!equalRGB11StringSet(state.InputOutPoints, actualInputs) ||
			!bytes.Equal(pending.RecipientConsignment, first.RecipientConsignment) ||
			!bytes.Equal(pending.LocalConsignment, first.LocalConsignment) {
			return nil, false, fmt.Errorf("%w: expired batch metadata mismatch", ErrRGB11Inconsistent)
		}
		if len(pending.RecipientConsignment) == 0 || len(pending.LocalConsignment) == 0 ||
			len(pending.SignedTx) == 0 || len(pending.SignedPSBT) == 0 ||
			state.ConsignmentHash != rgb11ObjectHash(pending.RecipientConsignment) ||
			pending.RecipientObjectHash != state.ConsignmentHash ||
			pending.LocalObjectHash != rgb11ObjectHash(pending.LocalConsignment) {
			return nil, false, fmt.Errorf("%w: pending consignment metadata mismatch", ErrRGB11Inconsistent)
		}
		if state.Invoice != "" || !state.SyntheticInvoiceRemoved || state.Expiry <= 0 ||
			now < state.Expiry || state.RelayExpiry != state.Expiry {
			return nil, false, ErrRGB11ExpiredCancel
		}
		messageID, messageErr := rgb11AddressMessageID(state.TransferID)
		wantRelay, relayErr := dkvsindexer.MailMsgKey(state.ReceiverAccountID, state.SenderAccountID, messageID)
		wantAck, ackErr := dkvsindexer.MailMsgKey(state.SenderAccountID, state.ReceiverAccountID, messageID)
		if state.NetworkBackupRef != "" || state.DKVSOperationID != "" ||
			state.SenderAccountID == "" || state.ReceiverAccountID == "" ||
			state.AddressMessageID != messageID || messageErr != nil || relayErr != nil || ackErr != nil ||
			state.RelayRecordKey != wantRelay || state.AckRecordKey != wantAck ||
			state.RelayRecordKey == state.AckRecordKey || state.DeliveryRecordKey != "" ||
			state.DeliveryRecordHash != "" || state.DeliveryMode != "" || state.DeliveryObjectID != "" ||
			state.DeliveryIssueHeight != 0 || state.DeliveryTTL != 0 ||
			state.DeliveryAcknowledged || state.DeliveryCacheCompacted {
			return nil, false, fmt.Errorf("%w: invalid local relay metadata", ErrRGB11Inconsistent)
		}
		if _, exists := seenRelayKeys[state.RelayRecordKey]; exists {
			return nil, false, fmt.Errorf("%w: duplicate relay key", ErrRGB11Inconsistent)
		}
		if _, exists := seenAckKeys[state.AckRecordKey]; exists {
			return nil, false, fmt.Errorf("%w: duplicate ACK key", ErrRGB11Inconsistent)
		}
		seenRelayKeys[state.RelayRecordKey] = struct{}{}
		seenAckKeys[state.AckRecordKey] = struct{}{}
		seenOutputs := make(map[string]struct{}, len(state.OutputOutPoints))
		for _, outpoint := range state.OutputOutPoints {
			parsed, parseErr := wire.NewOutPointFromString(outpoint)
			if parseErr != nil || parsed.Hash.String() != state.WitnessTxID || int(parsed.Index) >= len(tx.TxOut) {
				return nil, false, fmt.Errorf("%w: invalid pending output %s", ErrRGB11Inconsistent, outpoint)
			}
			if _, exists := seenOutputs[outpoint]; exists {
				return nil, false, fmt.Errorf("%w: duplicate pending output %s", ErrRGB11Inconsistent, outpoint)
			}
			seenOutputs[outpoint] = struct{}{}
		}
		if state.RecipientVout != 0 {
			recipientOutpoint := fmt.Sprintf("%s:%d", state.WitnessTxID, state.RecipientVout)
			if _, exists := seenOutputs[recipientOutpoint]; !exists {
				return nil, false, fmt.Errorf("%w: recipient output is absent", ErrRGB11Inconsistent)
			}
		}
	}
	return pendingList, false, nil
}

func (p *rgb11Manager) verifyExpiredRGB11Reservation(pendingList []*rgb11wallet.PendingTransfer) error {
	reservationID, expected, err := rgb11PendingReservationOutpoints(pendingList)
	if err != nil || reservationID == "" || len(expected) == 0 {
		return fmt.Errorf("%w: invalid pending reservation", ErrRGB11Inconsistent)
	}
	locks := p.utxoLockerL1.GetLockedUtxoList()
	expectedSet := make(map[string]struct{}, len(expected))
	for _, outpoint := range expected {
		expectedSet[outpoint] = struct{}{}
		lock := locks[outpoint]
		if lock == nil || lock.ReservationID != reservationID || lock.Reason != rgb11wallet.LockReasonPending {
			return fmt.Errorf("%w: pending reservation owner mismatch for %s", ErrRGB11Inconsistent, outpoint)
		}
	}
	for outpoint, lock := range locks {
		if lock == nil || lock.ReservationID != reservationID {
			continue
		}
		if _, exists := expectedSet[outpoint]; !exists {
			return fmt.Errorf("%w: pending reservation owns unexpected UTXO %s", ErrRGB11Inconsistent, outpoint)
		}
	}
	return nil
}

func (p *rgb11Manager) verifyExpiredRGB11BitcoinEvidence(pending *rgb11wallet.PendingTransfer) error {
	status, err := p.rgbManager.evidence.GetTxStatus(pending.State.WitnessTxID)
	if err != nil {
		return fmt.Errorf("verify expired RGB11 witness status: %w", err)
	}
	if status == nil {
		return fmt.Errorf("verify expired RGB11 witness status: missing transaction status")
	}
	if status.TxID != "" && status.TxID != pending.State.WitnessTxID {
		return fmt.Errorf("%w: Bitcoin status transaction id mismatch", ErrRGB11Inconsistent)
	}
	if status.InMempool || status.Confirmed {
		return ErrRGB11AlreadyBroadcast
	}
	for _, outpoint := range pending.State.InputOutPoints {
		outspend, outspendErr := p.rgbManager.evidence.GetOutspend(outpoint)
		if outspendErr != nil {
			return fmt.Errorf("verify expired RGB11 input %s: %w", outpoint, outspendErr)
		}
		if outspend == nil {
			return fmt.Errorf("verify expired RGB11 input %s: missing outspend status", outpoint)
		}
		if outspend.Spent {
			return ErrRGB11AlreadyBroadcast
		}
		utxo, utxoErr := p.rgbManager.evidence.GetUTXO(outpoint)
		if utxoErr != nil {
			return fmt.Errorf("verify expired RGB11 input UTXO %s: %w", outpoint, utxoErr)
		}
		if utxo == nil || utxo.OutPoint != outpoint {
			return fmt.Errorf("verify expired RGB11 input UTXO %s: missing or mismatched UTXO", outpoint)
		}
	}
	return nil
}

func (p *rgb11Manager) verifyNoLocalRGB11RelayEvidence(pendingList []*rgb11wallet.PendingTransfer) error {
	if p == nil || p.Manager == nil || p.Manager.cfg == nil || p.Manager.cfg.IndexerL2 == nil {
		return nil
	}
	store, err := p.configuredRGB11Store()
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(pendingList)*2)
	for _, pending := range pendingList {
		keys = append(keys, pending.State.RelayRecordKey, pending.State.AckRecordKey)
	}
	found, err := store.hasLocalRecordEvidence(keys...)
	if err != nil {
		return err
	}
	if found {
		return ErrRGB11RelayEvidence
	}
	return nil
}

// CancelExpiredRGB11Transfer terminates one complete, never-delivered address
// mailbox batch after its transfer expiry. All external evidence is
// checked before the atomic terminal state is persisted and locks are released.
func (p *rgb11Manager) CancelExpiredRGB11Transfer(transferID string) error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil ||
		p.rgbManager.evidence == nil || p.utxoLockerL1 == nil || strings.TrimSpace(transferID) == "" {
		return ErrRGB11ExpiredCancel
	}
	pendingList, terminal, err := p.loadExpiredRGB11Batch(strings.TrimSpace(transferID), time.Now().Unix())
	if err != nil {
		return err
	}
	if terminal {
		// A compacted terminal tombstone is already complete. In particular, do
		// not release the reservation or compact the payload a second time.
		return nil
	}
	if err := p.verifyExpiredRGB11Reservation(pendingList); err != nil {
		return err
	}
	if err := p.verifyNoLocalRGB11RelayEvidence(pendingList); err != nil {
		return err
	}
	if err := p.verifyExpiredRGB11BitcoinEvidence(pendingList[0]); err != nil {
		return err
	}
	return p.cancelRGB11PendingBatch(pendingList, rgb11RejectReasonInvoiceExpired, nil)
}

func (p *rgb11Manager) cancelRGB11PendingBatch(pendingList []*rgb11wallet.PendingTransfer,
	reason string, rejectedOpouts []string) error {
	return p.cancelRGB11PendingBatchMode(pendingList, reason, rejectedOpouts, false)
}

// Only verified recipient rejection handlers may cancel a batch containing
// accepted recipients. Ordinary user/expiry cancellation remains conservative.
func (p *rgb11Manager) cancelRGB11RejectedPendingBatch(pendingList []*rgb11wallet.PendingTransfer,
	reason string, rejectedOpouts []string) error {
	return p.cancelRGB11PendingBatchMode(pendingList, reason, rejectedOpouts, true)
}

func (p *rgb11Manager) cancelRGB11PendingBatchMode(pendingList []*rgb11wallet.PendingTransfer,
	reason string, rejectedOpouts []string, recipientRejected bool) error {
	if len(pendingList) == 0 {
		return ErrRGB11BatchAckRequired
	}
	if reason == "" {
		reason = RGB11RejectReasonUser
	}
	// Refresh the same-account CAS baseline immediately before cancellation.
	// A pending pointer loaded before another device's broadcast intent must not
	// be allowed to overwrite that newer phase or release its inputs.
	if owner := p.accountManagementOwner(); owner != nil {
		if err := p.syncRGB11OperationRecovery(context.Background()); err != nil {
			return err
		}
	}
	for index, expected := range pendingList {
		if expected == nil || expected.State.TransferID == "" {
			return ErrRGB11Inconsistent
		}
		current, err := p.rgbManager.projectionStore.LoadPendingTransfer(expected.State.TransferID)
		if err != nil {
			return err
		}
		if !sameRGB11BroadcastIntent(expected, current) ||
			current.State.Status != "prepared" && current.State.Status != "delivered" &&
				current.State.Status != "relayed" ||
			!recipientRejected && (current.State.AckStatus == "accepted" ||
				current.State.AckStatus == "accepted-out-of-band" ||
				current.State.DeliveryAcknowledged) ||
			recipientRejected && current.ChannelSend != nil && current.ChannelSend.Signed {
			return ErrRGB11BatchAckRequired
		}
		pendingList[index] = current
	}
	if recipientRejected {
		ids := make([]string, 0, len(pendingList))
		for _, pending := range pendingList {
			ids = append(ids, pending.State.TransferID)
		}
		first := pendingList[0]
		for _, pending := range pendingList {
			expected := pending.State.BatchTransferIDs
			if len(expected) == 0 {
				expected = []string{pending.State.TransferID}
			}
			if !equalRGB11StringSet(ids, expected) ||
				pending.State.WitnessTxID != first.State.WitnessTxID ||
				pending.State.BatchID != first.State.BatchID ||
				!bytes.Equal(pending.SignedTx, first.SignedTx) ||
				validateRGB11PendingTransaction(pending) != nil {
				return ErrRGB11Inconsistent
			}
		}
		if err := p.verifyExpiredRGB11Reservation(pendingList); err != nil {
			return err
		}
	}
	baseline := cloneRGB11PendingBatch(pendingList)
	ids := make([]string, 0, len(pendingList))
	for _, pending := range pendingList {
		pending.State.AckStatus = "rejected"
		pending.State.Status = "rejected"
		pending.State.RejectReason = reason
		pending.State.RejectedOpouts = append([]string(nil), rejectedOpouts...)
		ids = append(ids, pending.State.TransferID)
	}
	// Persist the terminal state before unlocking. A failure after this point is
	// fail-closed and is repaired by reservation reconciliation on restart.
	if err := p.rgbManager.projectionStore.CompareAndSavePendingTransferStates(baseline, pendingList); err != nil {
		return err
	}
	if err := p.autoBackupRGB11AfterMutation(); err != nil {
		return err
	}
	if owner := p.accountManagementOwner(); owner != nil {
		if err := p.syncRGB11OperationRecovery(context.Background()); err != nil {
			return err
		}
	}
	for index, expected := range pendingList {
		current, err := p.rgbManager.projectionStore.LoadPendingTransfer(expected.State.TransferID)
		if err != nil {
			return err
		}
		if !sameRGB11BroadcastIntent(expected, current) || current.State.Status != "rejected" ||
			current.State.AckStatus != "rejected" || current.State.RejectReason != reason ||
			!slices.Equal(current.State.RejectedOpouts, rejectedOpouts) {
			return ErrRGB11BatchAckRequired
		}
		pendingList[index] = current
	}
	if err := p.releaseRGB11PendingReservation(pendingList); err != nil {
		return err
	}
	if err := p.rgbManager.projectionStore.CompactRejectedTransfers(ids); err != nil {
		return err
	}
	if err := p.rebuildRGB11Locks(); err != nil {
		return err
	}
	return p.autoBackupRGB11AfterMutation()
}

// Complete local bytes, including damaged records; never used by backup Export.
// The caller owns the reservation read or commit gate.
func (p *rgb11Manager) readRGB11LocalLocked(walletID string) (*RGB11WalletSnapshot, error) {
	projection, err := p.projectionStore.ReadLocalRecords()
	if err != nil {
		return nil, err
	}
	engine, err := p.engineStore.ExportSnapshot()
	if err != nil {
		return nil, err
	}
	return &RGB11WalletSnapshot{Version: rgb11WalletSnapshotVersion, WalletID: walletID,
		AccountIndex: p.wallet.GetSubAccount(), EngineBuildID: rgb11wallet.NativeEngineBuildID,
		ProjectionRecords: projection, EngineRecords: engine}, nil
}

func (p *rgb11Manager) readRGB11Local(walletID string) (*RGB11WalletSnapshot, error) {
	release := p.beginRGB11ReservationRead()
	defer release()
	snapshot, err := p.readRGB11LocalLocked(walletID)
	if err == nil {
		var view *rgb11wallet.LocalProjection
		view, err = rgb11wallet.SplitLocalProjection(snapshot.ProjectionRecords)
		if err == nil {
			p.noteRGB11ProofFaults(view)
		}
	}
	if err != nil {
		p.markRGB11ProjectionUnavailable()
		return nil, err
	}
	return snapshot, nil
}

func (p *rgb11Manager) localRGB11ProofView() ([]*rgb11wallet.AllocationProof, map[string]bool, error) {
	return p.fundingRGB11ProofView(nil)
}

func (p *rgb11Manager) fundingRGB11ProofView(observed map[string]indexer.AssetName) ([]*rgb11wallet.AllocationProof, map[string]bool, error) {
	walletID, err := p.RGB11WalletID()
	if err != nil {
		return nil, nil, err
	}
	full, err := p.readRGB11LocalLocked(walletID)
	if err != nil {
		p.markRGB11ProjectionUnavailable()
		return nil, nil, err
	}
	faults := p.rgb11ScopeState().ProofFaults
	if len(observed) != 0 {
		faults = slices.DeleteFunc(slices.Clone(faults), func(f rgb11wallet.ProofFault) bool {
			name, ok := observed[f.OutPoint]
			return ok && name == f.AssetName && errors.Is(f.Cause, errRGB11SpenderUnknown)
		})
	}
	view, err := rgb11wallet.SplitLocalProjection(full.ProjectionRecords, faults...)
	if err != nil {
		p.markRGB11ProjectionUnavailable()
		return nil, nil, err
	}
	if len(observed) == 0 {
		p.noteRGB11ProofFaults(view)
	}
	proofs, err := rgb11wallet.ProofsFromProjectionSnapshot(view.Records)
	return proofs, view.Points, err
}

func (p *rgb11Manager) readRGB11ProofView() ([]*rgb11wallet.AllocationProof, map[string]bool, error) {
	release := p.beginRGB11ReservationRead()
	defer release()
	return p.localRGB11ProofView()
}
