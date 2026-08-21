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
	corewallet "github.com/sat20-labs/rgb11/wallet"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"

	"slices"
	"strconv"
	"strings"
	"time"
)

const rgb11AddressMailboxPageSize = 256

func (p *rgb11Manager) configuredRGB11Store() (*dkvsStore, error) {
	if p == nil || p.ensureDKVSManager() == nil {
		return nil, ErrRGB11Inconsistent
	}
	return p.ensureDKVSManager().primaryStore()
}

func (p *rgb11Manager) configureRGB11AddressCapabilityRetention(store *dkvsStore,
	record *dkvsindexer.RecordOptions) error {

	return p.configureRGB11AddressTransientRetention(store, record)
}

// Address capabilities, deliveries and acknowledgments are transient protocol
// messages. Their retention is configured by the connected service node and
// read through GET /v3/dkvs/config; the wallet embeds no fallback duration.
func (p *rgb11Manager) configureRGB11AddressTransientRetention(store *dkvsStore,
	record *dkvsindexer.RecordOptions) error {

	if store == nil || record == nil {
		return ErrDKVSPathNotSynced
	}
	_, err := store.ConfigureFreeLocalRetention(record)
	return err
}

func rgb11AddressStoragePolicy(options dkvsindexer.RecordOptions) dkvsStoragePolicy {
	return dkvsStoragePolicy{TTL: options.TTL, FreeLocal: true}
}

func (p *rgb11Manager) EnableConfiguredRGB11AddressReceive(options RGB11ReceiveCapabilityOptions) (*RGB11AddressEndpoint, error) {
	store, err := p.configuredRGB11Store()
	if err != nil {
		return nil, err
	}
	if err := p.configureRGB11AddressCapabilityRetention(store, &options.RecordOptions); err != nil {
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
	if err := p.configureRGB11AddressTransientRetention(store, &options.RecordOptions); err != nil {
		return nil, err
	}
	result, err := p.deliverRGB11AddressTransferStore(store, transferID, options)
	if err != nil {
		return nil, err
	}
	txID, err := p.BroadcastRGB11AddressTransfer(transferID)
	if err != nil {
		return result, err
	}
	result.TxID = txID
	return result, nil
}

func rgb11AddressProcessedMetadata(kind, messageID string) string {
	return "address-" + kind + "-" + messageID
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

// SyncConfiguredRGB11AddressMailbox processes the current subaccount mailbox.
// A Consignment whose witness transaction is not visible is intentionally left
// unacknowledged so a later DKVS notify or sync can retry it. Processed cursors
// are device-local cache and are not included in wallet recovery snapshots.
func (p *rgb11Manager) SyncConfiguredRGB11AddressMailbox(ctx context.Context,
	verify dkvsindexer.RecordVerificationOptions,
	ackOptions RGB11AddressDeliveryOptions) (*RGB11AddressMailboxSyncResult, error) {
	if p == nil || p.wallet == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return nil, ErrRGB11Inconsistent
	}
	result := &RGB11AddressMailboxSyncResult{}
	store, err := p.configuredRGB11Store()
	if err != nil {
		return nil, err
	}
	if err := p.configureRGB11AddressTransientRetention(store, &ackOptions.RecordOptions); err != nil {
		return nil, err
	}
	accountID, err := dkvsAccountID(p.wallet)
	if err != nil {
		return nil, err
	}
	records, err := store.ListMailboxVerified(accountID, verify)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		result.Scanned++
		_, _, messageID, parseErr := parseRGB11AddressMailboxKey(record.record)
		if parseErr != nil {
			result.Invalid++
			result.ErrorDetails = append(result.ErrorDetails, parseErr.Error())
			continue
		}
		if len(record.Value) == 4 {
			if p.rgb11AddressMessageProcessed("ack", messageID) {
				result.AlreadyDone++
				continue
			}
			if _, err := p.AcceptRGB11AddressACK(record.record, verify); err != nil {
				result.Invalid++
				result.ErrorDetails = append(result.ErrorDetails,
					fmt.Sprintf("ack %s: %v", messageID, err))
				continue
			}
			if err := p.markRGB11AddressMessageProcessed("ack", messageID); err != nil {
				return nil, err
			}
			result.ACKs++
			continue
		}
		if p.rgb11AddressMessageProcessed("consignment", messageID) {
			result.AlreadyDone++
			continue
		}
		if _, _, err := p.acceptRGB11AddressMailboxStore(ctx, store, record, ackOptions); err != nil {
			if errors.Is(err, ErrRGB11AddressTxNotSeen) {
				result.WaitingTx++
				continue
			}
			result.Invalid++
			result.ErrorDetails = append(result.ErrorDetails,
				fmt.Sprintf("consignment %s: %v", messageID, err))
			continue
		}
		if err := p.markRGB11AddressMessageProcessed("consignment", messageID); err != nil {
			return nil, err
		}
		result.Received++
	}
	return result, nil
}

const (
	RGB11ReceiveCapabilityVersion = rgb11wallet.ReceiveCapabilityVersion
	RGB11ReceiveCapabilityPath    = rgb11wallet.ReceiveCapabilityPath
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
	flags := options.Flags
	if flags == 0 {
		flags = RGB11ReceiveCapabilityAddress | RGB11ReceiveCapabilityAny
	}
	capabilityValue, err := rgb11wallet.EncodeReceiveCapability(RGB11ReceiveCapability{
		Version: RGB11ReceiveCapabilityVersion,
		Flags:   flags,
	})
	if err != nil {
		return nil, err
	}
	accountID, err := dkvsAccountID(wallet)
	if err != nil {
		return nil, err
	}
	address := wallet.GetAddress()
	mappingKey, err := dkvsindexer.AccountMappingKey(GetChainParam().Name, address)
	if err != nil {
		return nil, err
	}
	mappingValue, err := dkvsindexer.EncodeAccountMappingValue(accountID)
	if err != nil {
		return nil, err
	}
	capabilityKey, err := dkvsindexer.AccountPersonalKey(accountID, RGB11ReceiveCapabilityPath)
	if err != nil {
		return nil, err
	}
	values := map[string][]byte{
		mappingKey: mappingValue, capabilityKey: capabilityValue,
	}
	policy := rgb11AddressStoragePolicy(options.RecordOptions)
	if _, err := store.Update([]string{mappingKey, capabilityKey},
		func(current map[string]*dkvsValue, _ map[string]uint64) ([]dkvsValueMutation, error) {
			mutations := make([]dkvsValueMutation, 0, 2)
			for _, key := range []string{mappingKey, capabilityKey} {
				existing := current[key]
				if existing != nil && bytes.Equal(existing.Value, values[key]) &&
					existing.TTL == 0 && options.RecordOptions.TTL == 0 {
					continue
				}
				mutations = append(mutations, dkvsValueMutation{
					Key: key, Value: values[key], Owner: wallet,
					Policy: policy, Signature: dkvsSignatureAccount,
				})
			}
			return mutations, nil
		}); err != nil {
		return nil, err
	}
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
	accountID, err := dkvsindexer.DecodeAccountMappingValue(mapping.Value)
	if err != nil {
		return nil, ErrRGB11TraditionalReceiveRequired
	}
	capabilityKey, err := dkvsindexer.AccountPersonalKey(accountID, RGB11ReceiveCapabilityPath)
	if err != nil {
		return nil, err
	}
	capabilityValue, err := store.GetVerified(capabilityKey, verify)
	if err != nil {
		return nil, ErrRGB11TraditionalReceiveRequired
	}
	capability, err := rgb11wallet.DecodeReceiveCapability(capabilityValue.Value)
	if err != nil {
		return nil, err
	}
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
		CapabilityFlags: capability.Flags, CapabilityRecordKey: capabilityKey,
		CapabilityRecordHash: capabilityValue.Hash,
		Temporary:            capabilityValue.TTL > 0,
		IssueHeight:          capabilityValue.IssueHeight,
		TTL:                  capabilityValue.TTL,
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

var (
	ErrRGB11AddressDeliveryRequired = errors.New("RGB11 address consignment must be delivered before broadcast")
	ErrRGB11AddressTxNotSeen        = errors.New("RGB11 address witness transaction is not visible yet")
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
	inlineLimit := options.InlineLimit
	if inlineLimit <= 0 || inlineLimit > rgb11AddressInlineLimit {
		inlineLimit = rgb11AddressInlineLimit
	}
	mailKey, err := dkvsindexer.MailMsgKey(
		pending.State.ReceiverAccountID, pending.State.SenderAccountID, messageID,
	)
	if err != nil {
		return nil, err
	}
	existingMail, ciphertext, existingMode, covered, err := p.loadExistingRGB11AddressDelivery(
		store, pending, mailKey, messageID, options.RecordOptions,
	)
	if err != nil {
		return nil, err
	}
	if covered && existingMail != nil {
		modeName := "inline"
		objectID := ""
		if existingMode == rgb11AddressEnvelopeBlob {
			modeName = "blob"
			objectID = messageID
		}
		p.applyRGB11AddressDeliveryState(pending, existingMail, modeName, objectID)
		if err := p.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
			return nil, err
		}
		return &RGB11AddressDeliveryResult{
			TransferID: transferID, Mode: modeName, RecordKey: existingMail.Key,
			RecordHash: existingMail.Hash, ObjectID: objectID,
			Temporary: pending.State.DeliveryTemporary,
		}, nil
	}
	if len(ciphertext) == 0 {
		cryptor, ok := p.wallet.(rgb11AccountPayloadCryptor)
		if !ok {
			return nil, fmt.Errorf("active wallet does not support RGB11 account encryption")
		}
		ciphertext, err = cryptor.EncryptToAccount(
			pending.State.ReceiverAccountID, pending.RecipientConsignment,
		)
		if err != nil {
			return nil, err
		}
	}
	mode := rgb11AddressEnvelopeInline
	objectID := ""
	keys := []string{mailKey}
	values := make(map[string][]byte)
	mailValue, err := rgb11wallet.EncodeAddressEnvelope(mode, ciphertext)
	if err != nil {
		return nil, err
	}
	if len(ciphertext)+2 > inlineLimit {
		mode = rgb11AddressEnvelopeBlob
		objectID = messageID
		blobKey, keyErr := dkvsindexer.BlobKey(pending.State.SenderAccountID, objectID)
		if keyErr != nil {
			return nil, keyErr
		}
		keys = append(keys, blobKey)
		values[blobKey] = ciphertext
		mailValue, err = rgb11wallet.EncodeAddressEnvelope(mode, nil)
		if err != nil {
			return nil, err
		}
	}
	values[mailKey] = mailValue
	policy := rgb11AddressStoragePolicy(options.RecordOptions)
	written, err := store.Update(keys, func(current map[string]*dkvsValue,
		_ map[string]uint64) ([]dkvsValueMutation, error) {
		mutations := make([]dkvsValueMutation, 0, len(keys))
		for _, key := range keys {
			existing := current[key]
			if existing != nil && bytes.Equal(existing.Value, values[key]) &&
				existing.TTL == 0 && options.RecordOptions.TTL == 0 {
				continue
			}
			mutations = append(mutations, dkvsValueMutation{
				Key: key, Value: values[key], Owner: p.wallet,
				Policy: policy, Signature: dkvsSignatureAccount,
			})
		}
		return mutations, nil
	})
	if err != nil {
		return nil, err
	}
	var mailRecord *dkvsValue
	for _, value := range written {
		if value != nil && value.Key == mailKey {
			mailRecord = value
			break
		}
	}
	if mailRecord == nil {
		mailRecord, err = store.Get(mailKey)
		if err != nil {
			return nil, err
		}
	}
	modeName := "inline"
	if mode == rgb11AddressEnvelopeBlob {
		modeName = "blob"
	}
	p.applyRGB11AddressDeliveryState(pending, mailRecord, modeName, objectID)
	if err := p.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
		return nil, err
	}
	return &RGB11AddressDeliveryResult{
		TransferID: transferID, Mode: modeName, RecordKey: mailRecord.Key,
		RecordHash: mailRecord.Hash, ObjectID: objectID,
		Temporary: pending.State.DeliveryTemporary,
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

// BroadcastRGB11AddressTransfer broadcasts without waiting for ACK. Delivery
// must already be present under the selected finite DKVS TTL.
func (p *rgb11Manager) BroadcastRGB11AddressTransfer(transferID string) (string, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil ||
		p.rgbManager.evidence == nil || transferID == "" {
		return "", ErrRGB11AddressDeliveryRequired
	}
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return "", err
	}
	if rgb11BroadcastCompleteStatus(pending.State.Status) {
		return pending.State.WitnessTxID, nil
	}
	if !pending.State.AddressMode ||
		(pending.State.Status != "delivered" && pending.State.Status != rgb11StatusBroadcastAttempted) ||
		pending.State.DeliveryRecordHash == "" || pending.State.DeliveryRecordKey == "" {
		return "", ErrRGB11AddressDeliveryRequired
	}
	return p.broadcastRGB11PendingBatch(
		[]*rgb11wallet.PendingTransfer{pending},
		func(item *rgb11wallet.PendingTransfer) {
			item.State.AckStatus = "awaiting-persistence"
		},
	)
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

func (p *rgb11Manager) readRGB11AddressConsignmentStore(store *dkvsStore, record *dkvsValue,
	senderID, messageID string) ([]byte, string, error) {

	mode, encrypted, err := rgb11wallet.DecodeAddressEnvelope(record.Value)
	if err != nil {
		return nil, "", err
	}
	modeName := "inline"
	if mode == rgb11AddressEnvelopeBlob {
		modeName = "blob"
		blobKey, keyErr := dkvsindexer.BlobKey(senderID, messageID)
		if keyErr != nil {
			return nil, "", keyErr
		}
		blob, blobErr := store.Get(blobKey)
		if blobErr != nil || blob == nil {
			return nil, "", fmt.Errorf("%w: %v", ErrRGB11AddressMailbox, blobErr)
		}
		encrypted = blob.Value
	}
	cryptor, ok := p.wallet.(rgb11AccountPayloadCryptor)
	if !ok {
		return nil, "", fmt.Errorf("active wallet does not support RGB11 account decryption")
	}
	plain, err := cryptor.DecryptFromAccount(senderID, encrypted)
	return plain, modeName, err
}

func (p *rgb11Manager) findRGB11AddressAllocation(receipt *rgb11wallet.ValidationReceipt) (
	*rgb11wallet.ValidatedAllocation, *rgb11wallet.BitcoinTxStatus, error,
) {
	if receipt == nil || p.rgbManager == nil || p.rgbManager.evidence == nil {
		return nil, nil, ErrRGB11AddressMailbox
	}
	for index := range receipt.Allocations {
		allocation := &receipt.Allocations[index]
		if !allocation.WitnessTxPtr || allocation.AssignmentType != 4000 {
			continue
		}
		utxo, err := p.rgbManager.evidence.GetUTXO(allocation.OutPoint)
		if err != nil || utxo == nil {
			continue
		}
		if !rgb11AllocationControlledByWallet(p.wallet, allocation, utxo.PkScript) {
			continue
		}
		txID := allocationOutpointTxID(allocation.OutPoint)
		status, err := p.rgbManager.evidence.GetTxStatus(txID)
		if err != nil {
			return nil, nil, err
		}
		if status == nil || !status.InMempool && !status.Confirmed {
			return nil, nil, ErrRGB11AddressTxNotSeen
		}
		return allocation, status, nil
	}
	return nil, nil, ErrRGB11NoAllocation
}

func (p *rgb11Manager) acceptRGB11AddressMailboxStore(ctx context.Context, store *dkvsStore,
	value *dkvsValue, ackOptions RGB11AddressDeliveryOptions) (
	*rgb11wallet.ValidationReceipt, *swire.DKVSRecord, error) {

	if p == nil || store == nil || value == nil || value.record == nil {
		return nil, nil, ErrRGB11AddressMailbox
	}
	verify := dkvsindexer.RecordVerificationOptions{
		ExpectedKey: value.Key,
	}
	if err := dkvsindexer.VerifyAccountRecordForClient(value.record, verify); err != nil {
		return nil, nil, err
	}
	_, senderID, messageID, err := parseRGB11AddressMailboxKey(value.record)
	if err != nil {
		return nil, nil, err
	}
	raw, mode, err := p.readRGB11AddressConsignmentStore(store, value, senderID, messageID)
	if err != nil {
		return nil, nil, err
	}
	return p.acceptRGB11AddressMailboxDecoded(ctx, value.record, raw, mode,
		func(senderID, messageID string, ack RGB11AddressACK) (*swire.DKVSRecord, error) {
			written, err := p.sendRGB11AddressACKStore(store, senderID, messageID, ack, ackOptions)
			if err != nil {
				return nil, err
			}
			return written.record, nil
		})
}

func (p *rgb11Manager) acceptRGB11AddressMailboxDecoded(ctx context.Context,
	record *swire.DKVSRecord, raw []byte, mode string,
	sendACK func(string, string, RGB11AddressACK) (*swire.DKVSRecord, error)) (
	*rgb11wallet.ValidationReceipt, *swire.DKVSRecord, error) {

	if p == nil || record == nil || sendACK == nil || p.wallet == nil || p.rgbManager == nil {
		return nil, nil, ErrRGB11AddressMailbox
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
	expectedMessageID, err := rgb11AddressMessageID(canonicalTransferID)
	if err != nil || expectedMessageID != messageID {
		return nil, nil, ErrRGB11AddressMailbox
	}
	receipt, err := p.ValidateRGB11Consignment(ctx, raw)
	if err != nil {
		return nil, nil, err
	}
	if receipt.TransferID == "" || receipt.TransferID != canonicalTransferID {
		return nil, nil, ErrRGB11AddressMailbox
	}
	allocation, status, err := p.findRGB11AddressAllocation(receipt)
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
	request, err := p.rgbManager.engine.CreateReceive(corewallet.ReceiveParams{
		Mode: corewallet.ReceiveWitness, ContractID: receipt.ContractID, SchemaID: receipt.SchemaID,
		Network: rgb11InvoiceNetwork(GetChainParam()), Amount: &amount,
		AssignmentName: "assetOwner", RecipientID: receiverID, WitnessVout: vout,
		WitnessScript: pkScript, InternalXOnly: &internal,
		Expiry: time.Now().Add(24 * time.Hour).Unix(), StandardOnly: true,
	})
	if err != nil {
		return nil, nil, err
	}
	accepted, err := p.acceptRGB11Consignment(ctx, request.RequestID, raw, false, "", nil)
	if err != nil {
		return nil, nil, err
	}
	state, err := p.rgbManager.projectionStore.LoadTransferState(accepted.TransferID)
	if err != nil {
		return nil, nil, err
	}
	recordHash := dkvsindexer.RecordHash(record)
	state.AddressMode = true
	state.AddressMessageID = messageID
	state.TransportMode = RGB11AddressTransport
	state.SenderAccountID = senderID
	state.ReceiverAccountID = receiverID
	state.ReceiverAddress = p.wallet.GetAddress()
	state.Invoice = ""
	state.SyntheticInvoiceRemoved = true
	state.DeliveryMode = mode
	state.DeliveryRecordKey = record.Key
	state.DeliveryRecordHash = hex.EncodeToString(recordHash[:])
	state.DeliveryTemporary = record.TTL > 0
	state.DeliveryIssueHeight = record.IssueHeight
	state.DeliveryTTL = record.TTL
	state.AckStatus = "persisted"
	if status.Confirmed && status.Confirmations >= int64(state.MinConfirmations) {
		state.Status = "settled"
	} else {
		state.Status = "pending"
	}
	if err := p.rgbManager.projectionStore.SaveTransferState(state); err != nil {
		return nil, nil, err
	}
	lockReason := rgb11wallet.LockReasonPending
	if status.Confirmed {
		lockReason = rgb11wallet.LockReasonRGB
	}
	if err := p.utxoLockerL1.SetLockReason(allocation.OutPoint, lockReason); err != nil {
		return nil, nil, err
	}
	ackRecord, err := sendACK(senderID, messageID,
		RGB11AddressACK{Status: RGB11AddressACKAccepted})
	if err != nil {
		return nil, nil, err
	}
	state.AckStatus = "ack-sent"
	state.DeliveryAcknowledged = true
	if err := p.rgbManager.projectionStore.SaveTransferState(state); err != nil {
		return nil, nil, err
	}
	p.autoBackupRGB11AfterMutation()
	return accepted, ackRecord, nil
}

func (p *rgb11Manager) sendRGB11AddressACKStore(store *dkvsStore, senderAccountID, messageID string,
	ack RGB11AddressACK, options RGB11AddressDeliveryOptions) (*dkvsValue, error) {

	if p == nil || store == nil || senderAccountID == "" || messageID == "" {
		return nil, ErrRGB11AddressMailbox
	}
	value, err := rgb11wallet.EncodeAddressACK(ack)
	if err != nil {
		return nil, err
	}
	receiverAccountID, err := dkvsAccountID(p.wallet)
	if err != nil {
		return nil, err
	}
	key, err := dkvsindexer.MailMsgKey(senderAccountID, receiverAccountID, messageID)
	if err != nil {
		return nil, err
	}
	return store.Put(dkvsValueMutation{
		Key: key, Value: value, Owner: p.wallet,
		Policy:    rgb11AddressStoragePolicy(options.RecordOptions),
		Signature: dkvsSignatureAccount,
	})
}

// AcceptRGB11AddressACK records receiver persistence. Delivery cache is only
// compacted once the witness transaction is also confirmed.
func (p *rgb11Manager) AcceptRGB11AddressACK(record *swire.DKVSRecord,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11AddressACK, error) {
	if p == nil || record == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return nil, ErrRGB11AddressMailbox
	}
	if err := dkvsindexer.VerifyAccountRecordForClient(record, verify); err != nil {
		return nil, err
	}
	senderID, receiverID, messageID, err := parseRGB11AddressMailboxKey(record)
	if err != nil {
		return nil, err
	}
	localID, err := dkvsAccountID(p.wallet)
	if err != nil || senderID != localID {
		return nil, ErrRGB11AddressMailbox
	}
	ack, err := rgb11wallet.DecodeAddressACK(record.Value)
	if err != nil {
		return nil, err
	}
	states, err := p.rgbManager.projectionStore.ListTransfers()
	if err != nil {
		return nil, err
	}
	var pending *rgb11wallet.PendingTransfer
	for _, state := range states {
		if state == nil || !state.AddressMode || state.AddressMessageID != messageID {
			continue
		}
		pending, err = p.rgbManager.projectionStore.LoadPendingTransfer(state.TransferID)
		if err != nil {
			return nil, err
		}
		break
	}
	if pending == nil || !pending.State.AddressMode || pending.State.AddressMessageID != messageID ||
		pending.State.ReceiverAccountID != receiverID || pending.State.SenderAccountID != senderID {
		return nil, ErrRGB11AddressMailbox
	}
	if ack.Status != RGB11AddressACKAccepted {
		pending.State.AckStatus = "need-resend"
		if ack.Status == RGB11AddressACKRejected {
			pending.State.AckStatus = "rejected-after-broadcast"
		}
		_ = p.rgbManager.projectionStore.SavePendingTransferState(pending)
		return &ack, nil
	}
	pending.State.AckStatus = "accepted"
	pending.State.DeliveryAcknowledged = true
	if err := p.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
		return nil, err
	}
	if err := p.compactRGB11AddressDeliveryIfFinal(pending); err != nil {
		return nil, err
	}
	return &ack, nil
}

func (p *rgb11Manager) compactRGB11AddressDeliveryIfFinal(pending *rgb11wallet.PendingTransfer) error {
	if pending == nil || !pending.State.AddressMode || !pending.State.DeliveryAcknowledged {
		return nil
	}
	status, err := p.rgbManager.evidence.GetTxStatus(pending.State.WitnessTxID)
	if err != nil || status == nil || !status.Confirmed ||
		status.Confirmations < int64(pending.State.MinConfirmations) {
		return err
	}
	pending.State.Status = "settled"
	pending.State.DeliveryCacheCompacted = true
	if err := p.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
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
	prepared, err := p.PrepareRGB11Transfer(ctx, RGB11SendRequest{
		Invoice:          invoice,
		FeeRate:          request.FeeRate,
		MinConfirmations: request.MinConfirmations,
	})
	if err != nil {
		return nil, nil, err
	}
	if prepared == nil || prepared.State == nil {
		return nil, nil, ErrRGB11Inconsistent
	}
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		return nil, nil, err
	}
	senderAccountID, err := dkvsAccountID(p.wallet)
	if err != nil {
		return nil, nil, err
	}
	messageID, err := rgb11AddressMessageID(pending.State.TransferID)
	if err != nil {
		return nil, nil, err
	}
	deliveryKey, err := dkvsindexer.MailMsgKey(endpoint.AccountID, senderAccountID, messageID)
	if err != nil {
		return nil, nil, err
	}
	ackKey, err := dkvsindexer.MailMsgKey(senderAccountID, endpoint.AccountID, messageID)
	if err != nil {
		return nil, nil, err
	}
	pending.State.AddressMode = true
	pending.State.AddressMessageID = messageID
	pending.State.TransportMode = RGB11AddressTransport
	pending.State.SenderAccountID = senderAccountID
	pending.State.ReceiverAccountID = endpoint.AccountID
	pending.State.ReceiverAddress = endpoint.Address
	pending.State.ReceiveCapabilityKey = endpoint.CapabilityRecordKey
	pending.State.ReceiveCapabilityHash = endpoint.CapabilityRecordHash
	pending.State.DeliveryRecordKey = deliveryKey
	pending.State.RelayRecordKey = deliveryKey
	pending.State.AckRecordKey = ackKey
	pending.State.RecipientID = endpoint.AccountID
	pending.State.Invoice = ""
	pending.State.SyntheticInvoiceRemoved = true
	pending.State.AckStatus = "awaiting-persistence"
	if err := p.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
		return nil, nil, err
	}

	prepared.State = &pending.State
	prepared.States = []*rgb11wallet.TransferState{&pending.State}
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
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || p.rgbManager.engineStore == nil || p.wallet == nil || walletID == "" {
		return nil, nil, ErrRGB11Inconsistent
	}
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

// autoBackupRGB11AfterMutation only invalidates the account-managed provider
// bundle. Account management owns export, encryption, DKVS CAS, retention and
// AUTOPAY for all wallet/account scopes.
func (p *rgb11Manager) autoBackupRGB11AfterMutation() {
	if owner := p.accountManagementOwner(); owner != nil {
		owner.markAccountManagedDataDirty(rgb11AccountManagedProviderID)
	}
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
	decoded, err := coreconsignment.DecodeArmor(string(pending.RecipientConsignment))
	if err != nil {
		return nil, err
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
		State: &state, RecipientConsignment: string(pending.RecipientConsignment),
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
	if err := p.rgbManager.projectionStore.SavePendingTransferStates(pendingList); err != nil {
		return "", err
	}
	return p.broadcastRGB11PendingBatch(pendingList, nil)
}

func (p *rgb11Manager) CancelRGB11OutOfBandTransfer(transferID string) error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil ||
		p.rgbManager.evidence == nil || transferID == "" {
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
	status, statusErr := p.rgbManager.evidence.GetTxStatus(pending.State.WitnessTxID)
	if statusErr == nil && status != nil && (status.InMempool || status.Confirmed) {
		return ErrRGB11AlreadyBroadcast
	}
	if statusErr != nil {
		for _, outpoint := range pending.State.InputOutPoints {
			outspend, err := p.rgbManager.evidence.GetOutspend(outpoint)
			if err != nil {
				return fmt.Errorf("verify RGB11 out-of-band cancellation input %s: %w", outpoint, err)
			}
			if outspend == nil {
				return fmt.Errorf("verify RGB11 out-of-band cancellation input %s: missing outspend status", outpoint)
			}
			if outspend.Spent {
				return ErrRGB11AlreadyBroadcast
			}
		}
	}
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
			state.RelayRecordKey != wantRelay || state.DeliveryRecordKey != wantRelay ||
			state.AckRecordKey != wantAck || state.RelayRecordKey == state.AckRecordKey {
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
// mailbox batch after its receive capability expiry. All external evidence is
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
	if len(pendingList) == 0 {
		return ErrRGB11BatchAckRequired
	}
	if reason == "" {
		reason = RGB11RejectReasonUser
	}
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
	if err := p.rgbManager.projectionStore.SavePendingTransferStates(pendingList); err != nil {
		return err
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
	return nil
}
