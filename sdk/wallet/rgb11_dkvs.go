package wallet

import (
	"bytes"

	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/invoicing"
	corerelay "github.com/sat20-labs/rgb11/relay"
	coresync "github.com/sat20-labs/rgb11/sync"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"

	"math/big"
	"strconv"
	"strings"
	"time"
)

const (
	rgb11AddressMailboxPageSize = 256
	rgb11AddressTemporaryTTL    = uint64((24 * time.Hour) / time.Millisecond)
)

func (p *rgb11Manager) configuredRGB11Store() (*dkvsStore, error) {
	if p == nil || p.ensureDKVSManager() == nil {
		return nil, ErrRGB11Inconsistent
	}
	return p.ensureDKVSManager().primaryStore()
}

func (p *rgb11Manager) configureRGB11AddressCapabilityRetention(store *dkvsStore,
	record *dkvsindexer.RecordOptions, autopay **DKVSAutopayOptions) {

	if record == nil || autopay == nil {
		return
	}
	if *autopay == nil {
		if paid, err := p.hasActiveRGB11Autopay(); err == nil && paid {
			*autopay = &DKVSAutopayOptions{AddressParams: GetChainParam_SatsNet()}
		}
	}
	if *autopay != nil {
		record.TTL = 0
		record.ExpiryHeight = 0
		return
	}
	maxTTL := rgb11AddressTemporaryTTL
	if store != nil {
		if policy, err := store.Config(); err == nil && policy != nil && policy.Enabled && policy.MaxTTL > 0 {
			maxTTL = policy.MaxTTL
		}
	}
	if record.TTL == 0 || record.TTL > maxTTL {
		record.TTL = maxTTL
	}
	record.ExpiryHeight = 0
	p.setRGB11BackupRetention("temporary", record.TTL)
}

// Address deliveries and acknowledgments are transient protocol messages.
// AUTOPAY may fund the write, but must never turn transport data into a
// permanent wallet record.
func (p *rgb11Manager) configureRGB11AddressTransientRetention(store *dkvsStore,
	record *dkvsindexer.RecordOptions) {

	if record == nil {
		return
	}
	maxTTL := rgb11AddressTemporaryTTL
	if store != nil {
		if policy, err := store.Config(); err == nil && policy != nil && policy.Enabled && policy.MaxTTL > 0 {
			maxTTL = policy.MaxTTL
		}
	}
	if record.TTL == 0 || record.TTL > maxTTL {
		record.TTL = maxTTL
	}
	record.ExpiryHeight = 0
}

func rgb11AddressStoragePolicy(options dkvsindexer.RecordOptions,
	autopay *DKVSAutopayOptions) dkvsStoragePolicy {

	policy := dkvsStoragePolicy{
		TTL: options.TTL, ExpiryHeight: options.ExpiryHeight, Autopay: autopay,
	}
	if autopay == nil && options.TTL > 0 {
		policy.FreeLocal = true
	}
	return policy
}

func (p *rgb11Manager) EnableConfiguredRGB11AddressReceive(options RGB11ReceiveCapabilityOptions) (*RGB11AddressEndpoint, error) {
	store, err := p.configuredRGB11Store()
	if err != nil {
		return nil, err
	}
	p.configureRGB11AddressCapabilityRetention(store, &options.RecordOptions, &options.Autopay)
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
	p.configureRGB11AddressTransientRetention(store, &options.RecordOptions)
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
	if !p.rgb11DKVSConfigured() {
		return result, nil
	}
	store, err := p.configuredRGB11Store()
	if err != nil {
		return nil, err
	}
	p.configureRGB11AddressTransientRetention(store, &ackOptions.RecordOptions)
	accountID, err := dkvsAccountID(p.wallet)
	if err != nil {
		return nil, err
	}
	prefix := "/mail/" + accountID + "/msg"
	if _, err := dkvsindexer.ParsePrefix(prefix); err != nil {
		return nil, err
	}
	if verify.Now == 0 {
		verify.Now = uint64(time.Now().UnixMilli())
	}
	records, err := store.List(prefix)
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
	Autopay       *DKVSAutopayOptions       `json:"autopay,omitempty"`
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
	policy := rgb11AddressStoragePolicy(options.RecordOptions, options.Autopay)
	if _, err := store.Update([]string{mappingKey, capabilityKey},
		func(current map[string]*dkvsValue, _ map[string]uint64) ([]dkvsValueMutation, error) {
			mutations := make([]dkvsValueMutation, 0, 2)
			for _, key := range []string{mappingKey, capabilityKey} {
				existing := current[key]
				if existing != nil && bytes.Equal(existing.Value, values[key]) &&
					options.RecordOptions.ExpiryHeight <= existing.ExpiryHeight &&
					options.RecordOptions.TTL <= existing.TTL {
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
		dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())})
}

func (p *rgb11Manager) resolveRGB11AddressEndpointStore(store *dkvsStore, address string,
	_ dkvsindexer.RecordVerificationOptions) (*RGB11AddressEndpoint, error) {

	if store == nil || address == "" {
		return nil, ErrRGB11TraditionalReceiveRequired
	}
	mappingKey, err := dkvsindexer.AccountMappingKey(GetChainParam().Name, address)
	if err != nil {
		return nil, ErrRGB11TraditionalReceiveRequired
	}
	mapping, err := store.Get(mappingKey)
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
	capabilityValue, err := store.Get(capabilityKey)
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
		ExpiryHeight:         capabilityValue.ExpiryHeight,
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
	Autopay       *DKVSAutopayOptions       `json:"autopay,omitempty"`
	InlineLimit   int                       `json:"inline_limit,omitempty"`
}

type rgb11AccountPayloadCryptor interface {
	EncryptToAccount(accountID string, plaintext []byte) ([]byte, error)
	DecryptFromAccount(accountID string, ciphertext []byte) ([]byte, error)
}

func rgb11AddressRecordCovers(record *dkvsValue, options dkvsindexer.RecordOptions) bool {
	if record == nil {
		return false
	}
	ttlCovered := record.TTL == 0 || (options.TTL > 0 && record.TTL >= options.TTL)
	heightCovered := record.ExpiryHeight == 0 ||
		(options.ExpiryHeight > 0 && record.ExpiryHeight >= options.ExpiryHeight)
	return ttlCovered && heightCovered
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
	covered := rgb11AddressRecordCovers(mailRecord, options)
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
		covered = covered && rgb11AddressRecordCovers(blobRecord, options)
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
	policy := rgb11AddressStoragePolicy(options.RecordOptions, options.Autopay)
	written, err := store.Update(keys, func(current map[string]*dkvsValue,
		_ map[string]uint64) ([]dkvsValueMutation, error) {
		mutations := make([]dkvsValueMutation, 0, len(keys))
		for _, key := range keys {
			existing := current[key]
			if existing != nil && bytes.Equal(existing.Value, values[key]) &&
				options.RecordOptions.ExpiryHeight <= existing.ExpiryHeight &&
				options.RecordOptions.TTL <= existing.TTL {
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
	p.autoBackupRGB11AfterMutation()
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
	pending.State.DeliveryExpiryHeight = mailRecord.ExpiryHeight
	pending.State.DeliveryTTL = mailRecord.TTL
	pending.State.RelayExpiry = int64(mailRecord.ExpiryHeight)
	pending.State.RelayDurability = "DKVS_PERSISTENT"
	if pending.State.DeliveryTemporary {
		pending.State.RelayDurability = "DKVS_TEMP"
	}
	pending.State.Status = "delivered"
}

// BroadcastRGB11AddressTransfer broadcasts without waiting for ACK. Delivery
// must already be durable enough for the selected DKVS TTL/autopay policy.
func (p *rgb11Manager) BroadcastRGB11AddressTransfer(transferID string) (string, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil ||
		p.rgbManager.evidence == nil || transferID == "" {
		return "", ErrRGB11AddressDeliveryRequired
	}
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return "", err
	}
	if !pending.State.AddressMode || pending.State.Status != "delivered" ||
		pending.State.DeliveryRecordHash == "" || pending.State.DeliveryRecordKey == "" {
		return "", ErrRGB11AddressDeliveryRequired
	}
	if err := p.requireLatestRGB11WalletState(); err != nil {
		return "", err
	}
	txID, err := p.rgbManager.evidence.Broadcast(pending.SignedTx)
	if err != nil {
		return "", err
	}
	if txID != "" && txID != pending.State.WitnessTxID {
		return "", fmt.Errorf("RGB11 backend returned witness txid %s, expected %s", txID, pending.State.WitnessTxID)
	}
	pending.State.Status = "broadcast"
	pending.State.AckStatus = "awaiting-persistence"
	if err := p.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
		return "", err
	}
	p.autoBackupRGB11AfterMutation()
	return pending.State.WitnessTxID, nil
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
		ExpectedKey: value.Key, Now: uint64(time.Now().UnixMilli()),
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
	request, err := p.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode:        "witness",
		ContractID:  receipt.ContractID,
		SchemaID:    receipt.SchemaID,
		AmountRaw:   strconv.FormatUint(amount, 10),
		Expiry:      time.Now().Add(24 * time.Hour).Unix(),
		WitnessVout: vout,
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
	state.DeliveryTemporary = len(record.FeeProof) == 0
	state.DeliveryExpiryHeight = record.ExpiryHeight
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
		Policy:    rgb11AddressStoragePolicy(options.RecordOptions, options.Autopay),
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
	if verify.Now == 0 {
		verify.Now = uint64(time.Now().UnixMilli())
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
	p.autoBackupRGB11AfterMutation()
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

var rgb11AddressMessageDomain = []byte("SAT20-RGB11-ADDRESS-MESSAGE-V1")

func randomRGB11TmpKey() (string, error) {
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return dkvsindexer.TmpKey(hex.EncodeToString(entropy[:]))
}

// rgb11AddressMessageID maps the canonical RGB transfer identifier to the
// fixed-size, lower-case DKVS message segment. The canonical transfer ID remains
// inside the encrypted Consignment and is never replaced by this transport ID.
func rgb11AddressMessageID(transferID string) (string, error) {
	transferID = strings.TrimSpace(transferID)
	if transferID == "" {
		return "", ErrRGB11AddressMailbox
	}
	input := make([]byte, 0, len(rgb11AddressMessageDomain)+len(transferID))
	input = append(input, rgb11AddressMessageDomain...)
	input = append(input, transferID...)
	sum := sha256.Sum256(input)
	return hex.EncodeToString(sum[:]), nil
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
	relayKey, err := randomRGB11TmpKey()
	if err != nil {
		return "", err
	}
	ackKey, err := randomRGB11TmpKey()
	if err != nil {
		return "", err
	}
	invoice := invoicing.Invoice{
		Contract:    &contractID,
		Assignment:  &invoicing.InvoiceState{Kind: invoicing.StateAmount, Amount: invoicing.Amount(amount)},
		Beneficiary: beneficiary,
		Expiry:      &expiry,
		UnknownQuery: []invoicing.QueryParam{
			{Key: "sat20_recipient", Value: hex.EncodeToString(endpoint.CompressedPubKey)},
			{Key: "sat20_relay", Value: relayKey},
			{Key: "sat20_ack", Value: ackKey},
		},
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

	p.autoBackupRGB11AfterMutation()
	prepared.State = &pending.State
	prepared.States = []*rgb11wallet.TransferState{&pending.State}
	return prepared, endpoint, nil
}

const (
	rgb11WalletSnapshotVersion  = rgb11wallet.WalletSnapshotVersion
	rgb11WalletStorageNamespace = "rgb11-"
	rgb11AutoBackupMetadataName = "autobackup-policy"
)

type rgb11SnapshotCryptor interface {
	EncryptTo(pubKeyBytes []byte, plaintext []byte) ([]byte, error)
	Decrypt(data []byte, pubKeyBytes []byte) ([]byte, error)
}

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
	encoded, err := rgb11wallet.EncodeWalletSnapshotPayload(snapshot)
	return snapshot, encoded, err
}

func (p *rgb11Manager) rgb11DKVSConfigured() bool {
	return p != nil && p.cfg != nil && p.http != nil && p.cfg.IndexerL2 != nil && p.cfg.IndexerL2.Host != ""
}

// requireLatestRGB11WalletState is the last guard before an irreversible
// external effect. When DKVS is configured, the local state must still match
// the wallet-signed head currently selected for this wallet.
func (p *rgb11Manager) requireLatestRGB11WalletState() error {
	if !p.rgb11DKVSConfigured() {
		return nil
	}
	store, err := p.ensureDKVSManager().primaryStore()
	if err != nil {
		return err
	}
	_, headKey, snapshotKey, err := p.rgb11StateKeys()
	if err != nil {
		return err
	}
	if err := store.WaitReady(headKey, snapshotKey); err != nil {
		return err
	}
	if err := p.reconcileRGB11StateFromStore(store); err != nil {
		return err
	}
	needsPersist := p.rgb11ScopeState().Status == "pending"
	if !needsPersist && p.rgbManager.head == nil {
		walletID, walletErr := p.RGB11WalletID()
		if walletErr != nil {
			return walletErr
		}
		snapshot, _, exportErr := p.exportRGB11WalletSnapshot(walletID)
		if exportErr != nil {
			return exportErr
		}
		needsPersist = rgb11SnapshotHasState(snapshot)
	}
	if needsPersist {
		if _, err := p.persistRGB11StateToStore(store); err != nil {
			return err
		}
	}
	if p.rgb11ScopeState().Status != "synced" {
		return ErrDKVSPathNotSynced
	}
	if err := p.enableRGB11AutoBackupFromStore(store); err != nil {
		p.setRGB11DKVSStatus("warning")
		return err
	}
	return nil
}

func (p *rgb11Manager) SyncRGB11WalletState(walletID string, opts dkvsindexer.RecordOptions) (*coresync.WalletHead, error) {
	stableID, err := p.RGB11WalletID()
	if err != nil {
		return nil, err
	}
	if walletID != "" && walletID != stableID {
		return nil, coresync.ErrHeadWallet
	}
	store, err := p.ensureDKVSManager().primaryStore()
	if err != nil {
		return nil, err
	}
	head, err := p.persistRGB11StateToStore(store)
	if err != nil {
		return nil, err
	}
	if err := p.enableRGB11AutoBackupFromStore(store); err != nil {
		p.setRGB11DKVSStatus("warning")
		return nil, err
	}
	return head, nil
}

func (p *rgb11Manager) RestoreLatestRGB11WalletState(walletID string,
	verifyOpts dkvsindexer.RecordVerificationOptions) (*coresync.WalletHead, error) {
	stableID, err := p.RGB11WalletID()
	if err != nil {
		return nil, err
	}
	if walletID != "" && walletID != stableID {
		return nil, coresync.ErrHeadWallet
	}
	store, err := p.ensureDKVSManager().primaryStore()
	if err != nil {
		return nil, err
	}
	_, headKey, snapshotKey, err := p.rgb11StateKeys()
	if err != nil {
		return nil, err
	}
	if err := store.WaitReady(headKey, snapshotKey); err != nil {
		return nil, err
	}
	lock := p.backupLock()
	lock.Lock()
	head, err := p.restoreRGB11StateFromStoreLocked(store)
	lock.Unlock()
	if err != nil {
		return nil, err
	}
	if err := p.enableRGB11AutoBackupFromStore(store); err != nil {
		p.setRGB11DKVSStatus("warning")
		return nil, err
	}
	p.scheduleRGB11ChainReconciliation()
	return head, nil
}

// ActivateRGB11WalletState reconciles the selected local scope with the
// manager-owned replica. The manager has already synchronized and validated
// the path before this domain method reads it.
func (p *rgb11Manager) ActivateRGB11WalletState(verifyOpts dkvsindexer.RecordVerificationOptions) (*RGB11ActivationResult, error) {
	result := &RGB11ActivationResult{}
	before := p.rgbManager.head
	walletID, err := p.RGB11WalletID()
	if err != nil {
		return nil, err
	}
	localSnapshot, _, err := p.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		return nil, err
	}
	hadLocalState := rgb11SnapshotHasState(localSnapshot)
	if err := p.loadSynchronizedRGB11State(); err != nil {
		p.setRGB11DKVSStatus("warning")
		return nil, err
	}
	result.Head = p.rgbManager.head
	result.Found = result.Head != nil
	result.Restored = before == nil && !hadLocalState && result.Head != nil
	policy := p.rgb11AutoBackupPolicy()
	result.AutoBackup = policy != nil && policy.Enabled
	p.scheduleRGB11ChainReconciliation()
	return result, nil
}

func (p *rgb11Manager) enableRGB11AutoBackupFromStore(store *dkvsStore) error {
	_, headKey, _, err := p.rgb11StateKeys()
	if err != nil {
		return err
	}
	headValue, err := store.Get(headKey)
	if err != nil {
		return err
	}
	return p.enableRGB11AutoBackup(dkvsindexer.RecordOptions{
		TTL: headValue.TTL, ExpiryHeight: headValue.ExpiryHeight,
	})
}

// hasActiveRGB11Autopay checks the same active delegate properties required by
// the DKVS AUTOPAY verifier. The subsequent DKVS write remains authoritative.
func (p *rgb11Manager) hasActiveRGB11Autopay() (bool, error) {
	if p == nil || p.wallet == nil || p.wallet.GetPubKey() == nil || p.l2IndexerClient == nil {
		return false, nil
	}
	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	if !defaults.Enabled || defaults.AutopayContract == "" {
		return false, nil
	}
	raw, err := p.l2IndexerClient.GetContractStateJSON(defaults.AutopayContract)
	if err != nil {
		return false, err
	}
	state, err := dkvsindexer.DecodeAutopayContractState([]byte(raw), defaults.AutopayContract)
	if err != nil {
		return false, err
	}
	if state == nil || state.TemplateName != TEMPLATE_CONTRACT_AUTOPAY || state.Closed ||
		strings.EqualFold(strings.TrimSpace(state.Status), "closed") ||
		strings.EqualFold(strings.TrimSpace(state.Status), "expired") ||
		!strings.EqualFold(strings.TrimSpace(state.ServiceName), defaults.AutopayServiceName) ||
		!strings.EqualFold(strings.TrimSpace(state.Recipient), defaults.AutopayRecipient) ||
		strings.TrimSpace(state.FeeAssetName) != defaults.AutopayFeeAssetName {
		return false, nil
	}
	payer := PublicKeyToP2TRAddress_SatsNet(p.wallet.GetPubKey())
	delegate, ok := state.Delegates[payer]
	if !ok {
		return false, nil
	}
	amount, amountOK := new(big.Rat).SetString(strings.TrimSpace(delegate.AmountPerBlock))
	fullRecordFee, feeOK := new(big.Rat).SetString(strings.TrimSpace(defaults.FullRecordFeePerBlock))
	if !amountOK || !feeOK || amount.Sign() <= 0 || fullRecordFee.Sign() <= 0 ||
		amount.Cmp(fullRecordFee) < 0 || state.CurrentBlock <= 0 {
		return false, nil
	}
	return delegate.LastPayHeight >= state.CurrentBlock, nil
}

func (p *rgb11Manager) setRGB11BackupRetention(mode string, ttl uint64) {
	p.updateRGB11ScopeState(func(state *rgb11ScopeBackupState) {
		state.Mode = mode
		state.TTL = ttl
	})
}

func (p *rgb11Manager) enableRGB11AutoBackup(opts dkvsindexer.RecordOptions) error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return ErrRGB11Inconsistent
	}
	policy := &RGB11AutoBackupPolicy{
		Version: 1, Enabled: true, TTL: opts.TTL, ExpiryHeight: opts.ExpiryHeight,
	}
	encoded, err := rgb11wallet.EncodeAutoBackupPolicy(policy)
	if err != nil {
		return err
	}
	if err := p.rgbManager.projectionStore.SaveLocalMetadata(rgb11AutoBackupMetadataName, encoded); err != nil {
		return err
	}
	p.updateRGB11ScopeState(func(state *rgb11ScopeBackupState) {
		state.AutoBackup = policy
	})
	return nil
}

func (p *rgb11Manager) loadRGB11AutoBackupPolicy() (*RGB11AutoBackupPolicy, error) {
	encoded, err := p.rgbManager.projectionStore.LoadLocalMetadata(rgb11AutoBackupMetadataName)
	if err != nil {
		return nil, err
	}
	return rgb11wallet.DecodeAutoBackupPolicy(encoded)
}

func (p *rgb11Manager) autoBackupRGB11AfterMutation() {
	if p == nil || p.rgbManager == nil {
		return
	}
	policy := p.rgb11AutoBackupPolicy()
	enabled := policy != nil && policy.Enabled
	if !enabled {
		return
	}
	p.scheduleRGB11StoreWrite()
}

func hashBytes(value []byte) []byte {
	hash := sha256.Sum256(value)
	return hash[:]
}

func (p *rgb11Manager) reloadPersistedRGB11WalletHeadLocked() error {
	if p == nil || p.projectionStore == nil || p.wallet == nil || p.wallet.GetPubKey() == nil {
		return ErrRGB11Inconsistent
	}
	encoded, err := p.projectionStore.LoadLocalMetadata("wallet-head")
	if errors.Is(err, indexer.ErrKeyNotFound) {
		p.head = nil
		return nil
	}
	if err != nil {
		return err
	}
	walletID, err := p.RGB11WalletID()
	if err != nil {
		return err
	}
	head, err := rgb11wallet.DecodeWalletHead(encoded)
	if err != nil || head.Validate(walletID) != nil {
		p.setRGB11DKVSStatus("conflict")
		return coresync.ErrHeadConflict
	}
	p.head = head
	return nil
}

func (p *rgb11Manager) persistRGB11WalletHead(head *coresync.WalletHead) error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || head == nil {
		return ErrRGB11Inconsistent
	}
	encoded, err := head.StrictEncode()
	if err != nil {
		return err
	}
	return p.rgbManager.projectionStore.SaveLocalMetadata("wallet-head", encoded)
}

// NewRGB11WalletHead creates the compact head payload. dkvsManager applies the
// owning wallet's signature once, to the enclosing DKVS record.
func NewRGB11WalletHead(walletID string, stateHash, operationID [32]byte, previous *coresync.WalletHead) (*coresync.WalletHead, error) {
	head := &coresync.WalletHead{
		Version:     coresync.HeadVersion,
		WalletID:    walletID,
		Seq:         1,
		StateHash:   stateHash,
		OperationID: operationID,
	}
	if previous != nil {
		head.Seq = previous.Seq + 1
	}
	if err := head.ValidateSuccessor(previous); err != nil {
		return nil, err
	}
	return head, nil
}

func VerifyRGB11WalletHead(head *coresync.WalletHead, walletID string) error {
	if head == nil {
		return coresync.ErrHeadField
	}
	return head.Validate(walletID)
}

func RGB11WalletHeadPath(walletID string) string {
	return "rgb11/" + dkvsindexer.NormalizeNameID(walletID) + "/head"
}

func RGB11WalletSnapshotBlobKey(walletID string) string {
	return dkvsindexer.NormalizeNameID("rgb11-wallet-snapshot:" + walletID)
}

// BuildRGB11RelayRecord builds the signed temporary locator. Consignment
// bytes and private seal disclosures are deliberately excluded.
func (p *rgb11Manager) BuildRGB11RelayRecord(transferID, sourcePeerID string) (*corerelay.RelayRecord, error) {
	if p == nil || p.wallet == nil || p.rgbManager.projectionStore == nil {
		return nil, ErrRGB11Inconsistent
	}
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return nil, err
	}
	if pending.State.TransportMode != "sat20-dkvs" {
		return nil, ErrRGB11SAT20RelayRequired
	}
	objectHash, err := decodeRGB11Hash(pending.State.ConsignmentHash)
	if err != nil {
		return nil, err
	}
	if sourcePeerID == "" {
		sourcePeerID = "sat20-wallet"
	}
	record := &corerelay.RelayRecord{
		Version: corerelay.RecordVersion, TransferID: pending.State.TransferID,
		RecipientID: pending.State.RecipientID, ObjectHash: objectHash,
		ObjectSize:    uint64(len(pending.RecipientConsignment)),
		LocalObjectID: pending.State.ConsignmentHash, SourcePeerID: sourcePeerID,
		WitnessTxID: pending.State.WitnessTxID, AckRecordKey: pending.State.AckRecordKey,
		Expiry: pending.State.Expiry,
	}
	if err := SignRGB11RelayRecord(p.wallet, record); err != nil {
		return nil, err
	}
	return record, nil
}

func (p *rgb11Manager) PublishRGB11RelayRecord(transferID, sourcePeerID string,
	opts dkvsindexer.RecordOptions) (*corerelay.RelayRecord, *swire.DKVSRecord, error) {
	if err := p.requireLatestRGB11WalletState(); err != nil {
		return nil, nil, err
	}
	if opts.TTL == 0 {
		return nil, nil, fmt.Errorf("RGB11 relay TTL is required")
	}
	store, err := p.configuredRGB11Store()
	if err != nil {
		return nil, nil, err
	}
	record, err := p.BuildRGB11RelayRecord(transferID, sourcePeerID)
	if err != nil {
		return nil, nil, err
	}
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return nil, nil, err
	}
	if err := corerelay.ValidateTemporaryKey(pending.State.RelayRecordKey); err != nil {
		return nil, nil, err
	}
	if err := record.Verify(rgb11wallet.WalletPubKey(p.wallet), time.Now().Unix(),
		rgb11wallet.VerifyWalletSignature); err != nil {
		return nil, nil, err
	}
	encoded, err := record.MarshalBinary()
	if err != nil {
		return nil, nil, err
	}
	written, err := store.Put(dkvsValueMutation{
		Key: pending.State.RelayRecordKey, Value: encoded, Owner: p.wallet,
		Policy: dkvsStoragePolicy{
			TTL: opts.TTL, ExpiryHeight: opts.ExpiryHeight, FreeLocal: true,
		},
		Signature: dkvsSignatureLegacy,
	})
	if err != nil {
		return nil, nil, err
	}
	pending.State.Status = "relayed"
	pending.State.RelayDurability = "RELAYED_TEMP"
	pending.State.RelayExpiry = record.Expiry
	if err := p.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
		return nil, nil, err
	}
	p.autoBackupRGB11AfterMutation()
	return record, written.record, nil
}

// AcceptRGB11RelayConsignment authenticates the relay envelope, validates the
// consignment locally and returns an ACK signed only by the receiving wallet.
func (p *rgb11Manager) AcceptRGB11RelayConsignment(ctx context.Context, requestID string,
	record *corerelay.RelayRecord, raw []byte) (*rgb11wallet.ValidationReceipt, *corerelay.AckRecord, error) {
	if p == nil || p.wallet == nil || p.rgbManager.engine == nil || record == nil {
		return nil, nil, ErrRGB11Inconsistent
	}
	request, err := p.rgbManager.engine.LoadReceive(requestID)
	if err != nil {
		return nil, nil, err
	}
	invoice, err := invoicing.Parse(request.Invoice)
	if err != nil {
		return nil, nil, err
	}
	if transport, err := rgb11InvoiceTransportMode(invoice); err != nil || transport != "sat20-dkvs" {
		return nil, nil, ErrRGB11SAT20RelayRequired
	}
	if request.RecipientID != record.RecipientID || record.AckRecordKey != request.AckKey {
		return nil, nil, ErrRGB11InvoiceMismatch
	}
	if err := record.Verify(record.SenderPubKey, time.Now().Unix(), rgb11wallet.VerifyWalletSignature); err != nil {
		return nil, nil, err
	}
	hash := sha256.Sum256(raw)
	if hash != record.ObjectHash || uint64(len(raw)) != record.ObjectSize {
		return nil, nil, ErrRGB11InvoiceMismatch
	}
	receipt, err := p.acceptRGB11Consignment(ctx, requestID, raw, false, record.WitnessTxID, nil)
	if errors.Is(err, coreconsignment.ErrWitnessUnresolved) ||
		errors.Is(err, coreconsignment.ErrOutpointUnknown) {
		receipt, err = p.prepareRGB11Consignment(
			ctx, requestID, raw, record.WitnessTxID, nil, false,
		)
	}
	if err != nil {
		var violation *RGB11RejectListViolation
		if !errors.As(err, &violation) {
			return nil, nil, err
		}
		ack, nackErr := p.buildRGB11RecipientDecision(record, false, RGB11RejectReasonList)
		if nackErr != nil {
			return nil, nil, nackErr
		}
		if nackErr := p.rgbManager.projectionStore.DiscardValidatedObject(hex.EncodeToString(record.ObjectHash[:])); nackErr != nil {
			return nil, nil, nackErr
		}
		if nackErr := p.recordRGB11ReceiveRejection(requestID, request.Invoice, record,
			RGB11RejectReasonList, []string{violation.Rejected.String()}); nackErr != nil {
			return nil, nil, nackErr
		}
		return nil, ack, nil
	}
	if state, loadErr := p.rgbManager.projectionStore.LoadTransferState(receipt.TransferID); loadErr == nil {
		state.RelayDurability = "RELAYED_TEMP"
		state.RelayExpiry = record.Expiry
		if err := p.rgbManager.projectionStore.SaveTransferState(state); err != nil {
			return nil, nil, err
		}
		p.autoBackupRGB11AfterMutation()
	}
	ack, err := p.buildRGB11RecipientDecision(record, true, "")
	if err != nil {
		return nil, nil, err
	}
	return receipt, ack, nil
}

// RejectRGB11RelayConsignment records an explicit user refusal and returns a
// wallet-signed NACK. The consignment body is not required or persisted.
func (p *rgb11Manager) RejectRGB11RelayConsignment(requestID string,
	record *corerelay.RelayRecord) (*corerelay.AckRecord, error) {
	if p == nil || p.wallet == nil || p.rgbManager.engine == nil || record == nil {
		return nil, ErrRGB11Inconsistent
	}
	request, err := p.rgbManager.engine.LoadReceive(requestID)
	if err != nil {
		return nil, err
	}
	invoice, err := invoicing.Parse(request.Invoice)
	if err != nil {
		return nil, err
	}
	if transport, err := rgb11InvoiceTransportMode(invoice); err != nil || transport != "sat20-dkvs" {
		return nil, ErrRGB11SAT20RelayRequired
	}
	if request.RecipientID != record.RecipientID || request.AckKey != record.AckRecordKey {
		return nil, ErrRGB11InvoiceMismatch
	}
	if err := record.Verify(record.SenderPubKey, time.Now().Unix(), rgb11wallet.VerifyWalletSignature); err != nil {
		return nil, err
	}
	ack, err := p.buildRGB11RecipientDecision(record, false, RGB11RejectReasonUser)
	if err != nil {
		return nil, err
	}
	if err := p.recordRGB11ReceiveRejection(requestID, request.Invoice, record,
		RGB11RejectReasonUser, nil); err != nil {
		return nil, err
	}
	return ack, nil
}

func (p *rgb11Manager) buildRGB11RecipientDecision(record *corerelay.RelayRecord,
	accepted bool, reason string) (*corerelay.AckRecord, error) {
	relayHash, err := record.Hash()
	if err != nil {
		return nil, err
	}
	ack := &corerelay.AckRecord{
		Version: corerelay.RecordVersion, TransferID: record.TransferID,
		RecipientID: record.RecipientID, RelayRecordHash: relayHash,
		ConsignmentHash: record.ObjectHash, Accepted: accepted, ReasonCode: reason,
	}
	if err := SignRGB11AckRecord(p.wallet, ack); err != nil {
		return nil, err
	}
	return ack, nil
}

func (p *rgb11Manager) recordRGB11ReceiveRejection(requestID, invoice string,
	record *corerelay.RelayRecord, reason string, rejectedOpouts []string) error {
	objectHash := hex.EncodeToString(record.ObjectHash[:])
	if err := p.rgbManager.engine.MarkRelayRejected(requestID, record.TransferID, objectHash, reason); err != nil {
		return err
	}
	state := &rgb11wallet.TransferState{
		TransferID: record.TransferID, Direction: "receive", RecipientID: record.RecipientID,
		Invoice: invoice, ConsignmentHash: objectHash, WitnessTxID: record.WitnessTxID,
		AckStatus: "rejected", Status: "rejected", RelayRecordKey: "",
		AckRecordKey: record.AckRecordKey, RelayDurability: "RELAYED_TEMP", RelayExpiry: record.Expiry,
		RejectReason: reason, RejectedOpouts: append([]string(nil), rejectedOpouts...),
		TransportMode: "sat20-dkvs",
	}
	if err := p.rgbManager.projectionStore.SaveTransferState(state); err != nil {
		return err
	}
	p.autoBackupRGB11AfterMutation()
	return nil
}

func (p *rgb11Manager) PublishRGB11AckRecord(key string, ack *corerelay.AckRecord,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	if err := p.requireLatestRGB11WalletState(); err != nil {
		return nil, err
	}
	if p == nil || p.wallet == nil || ack == nil || opts.TTL == 0 {
		return nil, ErrRGB11Inconsistent
	}
	state, err := p.rgbManager.projectionStore.LoadTransferState(ack.TransferID)
	if err != nil {
		return nil, err
	}
	if state.TransportMode != "sat20-dkvs" || state.AckRecordKey != key {
		return nil, ErrRGB11SAT20RelayRequired
	}
	if err := corerelay.ValidateTemporaryKey(key); err != nil {
		return nil, err
	}
	if err := ack.Verify(rgb11wallet.WalletPubKey(p.wallet), rgb11wallet.VerifyWalletSignature); err != nil {
		return nil, err
	}
	encoded, err := ack.MarshalBinary()
	if err != nil {
		return nil, err
	}
	store, err := p.configuredRGB11Store()
	if err != nil {
		return nil, err
	}
	written, err := store.Put(dkvsValueMutation{
		Key: key, Value: encoded, Owner: p.wallet,
		Policy: dkvsStoragePolicy{
			TTL: opts.TTL, ExpiryHeight: opts.ExpiryHeight, FreeLocal: true,
		},
		Signature: dkvsSignatureLegacy,
	})
	if err != nil {
		return nil, err
	}
	return written.record, nil
}

func (p *rgb11Manager) FetchRGB11AckRecord(transferID string,
	verifyOpts dkvsindexer.RecordVerificationOptions) (*corerelay.AckRecord, *swire.DKVSRecord, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return nil, nil, ErrRGB11Inconsistent
	}
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return nil, nil, err
	}
	if pending.State.TransportMode != "sat20-dkvs" {
		return nil, nil, ErrRGB11SAT20RelayRequired
	}
	recipientPubKey, err := hex.DecodeString(pending.State.RecipientID)
	if err != nil {
		return nil, nil, ErrRGB11AckRequired
	}
	store, err := p.configuredRGB11Store()
	if err != nil {
		return nil, nil, err
	}
	if err := corerelay.ValidateTemporaryKey(pending.State.AckRecordKey); err != nil {
		return nil, nil, err
	}
	value, err := store.Get(pending.State.AckRecordKey)
	if err != nil {
		return nil, nil, err
	}
	if !bytes.Equal(value.Signer, recipientPubKey) {
		return nil, nil, dkvsindexer.ErrPermissionDenied
	}
	ack, err := corerelay.UnmarshalAckRecord(value.Value)
	if err != nil {
		return nil, nil, err
	}
	if err := ack.Verify(recipientPubKey, rgb11wallet.VerifyWalletSignature); err != nil {
		return nil, nil, err
	}
	return ack, value.record, nil
}

// BroadcastRGB11Transfer is ACK-gated. The signed witness transaction was
// already persisted by PrepareRGB11Transfer, so a process restart cannot lose
// the transfer object or its local change seals between ACK and broadcast.
func (p *rgb11Manager) BroadcastRGB11Transfer(transferID string, relayRecord *corerelay.RelayRecord,
	ack *corerelay.AckRecord) (string, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || relayRecord == nil || ack == nil {
		return "", ErrRGB11AckRequired
	}
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return "", err
	}
	if pending.State.BatchSize > 1 {
		return "", ErrRGB11BatchAckRequired
	}
	return p.BroadcastRGB11Batch(
		[]string{transferID}, []*corerelay.RelayRecord{relayRecord}, []*corerelay.AckRecord{ack},
	)
}

// BroadcastRGB11Batch verifies an ACK from every recipient before publishing
// the single shared Bitcoin transaction. The caller must provide exactly the
// sibling transfer IDs persisted by PrepareRGB11Transfer.
func (p *rgb11Manager) BroadcastRGB11Batch(transferIDs []string, relayRecords []*corerelay.RelayRecord,
	acks []*corerelay.AckRecord) (string, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || p.rgbManager.evidence == nil || p.wallet == nil ||
		len(transferIDs) == 0 || len(transferIDs) != len(relayRecords) || len(transferIDs) != len(acks) {
		return "", ErrRGB11BatchAckRequired
	}
	pendingList := make([]*rgb11wallet.PendingTransfer, 0, len(transferIDs))
	seen := make(map[string]struct{}, len(transferIDs))
	for index, transferID := range transferIDs {
		if transferID == "" || relayRecords[index] == nil || acks[index] == nil {
			return "", ErrRGB11BatchAckRequired
		}
		if _, ok := seen[transferID]; ok {
			return "", ErrRGB11BatchAckRequired
		}
		seen[transferID] = struct{}{}
		pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
		if err != nil {
			return "", err
		}
		if pending.State.TransportMode != "sat20-dkvs" {
			return "", ErrRGB11SAT20RelayRequired
		}
		pendingList = append(pendingList, pending)
	}
	first := pendingList[0]
	expectedIDs := first.State.BatchTransferIDs
	if len(expectedIDs) == 0 {
		expectedIDs = []string{first.State.TransferID}
	}
	if len(expectedIDs) != len(transferIDs) || (first.State.BatchSize > 0 && first.State.BatchSize != len(transferIDs)) {
		return "", ErrRGB11BatchAckRequired
	}
	for _, expected := range expectedIDs {
		if _, ok := seen[expected]; !ok {
			return "", ErrRGB11BatchAckRequired
		}
	}
	for index, pending := range pendingList {
		if pending.State.WitnessTxID != first.State.WitnessTxID || pending.State.BatchID != first.State.BatchID ||
			pending.State.ConsignmentHash != first.State.ConsignmentHash ||
			!bytes.Equal(pending.SignedTx, first.SignedTx) {
			return "", ErrRGB11BatchAckRequired
		}
		if err := p.verifyRGB11RecipientDecision(pending, relayRecords[index], acks[index]); err != nil {
			return "", err
		}
		if !acks[index].Accepted {
			if err := p.cancelRGB11PendingBatch(pendingList, acks[index].ReasonCode, nil); err != nil {
				return "", err
			}
			return "", ErrRGB11Rejected
		}
	}
	if err := p.requireLatestRGB11WalletState(); err != nil {
		return "", err
	}
	txID, err := p.rgbManager.evidence.Broadcast(first.SignedTx)
	if err != nil {
		return "", err
	}
	if txID != "" && txID != first.State.WitnessTxID {
		return "", fmt.Errorf("RGB11 backend returned witness txid %s, expected %s", txID, first.State.WitnessTxID)
	}
	for _, pending := range pendingList {
		pending.State.AckStatus = "accepted"
		pending.State.Status = "broadcast"
	}
	if err := p.rgbManager.projectionStore.SavePendingTransferStates(pendingList); err != nil {
		return "", err
	}
	p.autoBackupRGB11AfterMutation()
	return first.State.WitnessTxID, nil
}

// BroadcastRGB11OutOfBand is the official out-of-band ACK counterpart. The
// user calls it only after every external wallet recipient has confirmed that
// it accepted the consignment. No synthetic DKVS ACK or NACK is created.
func (p *rgb11Manager) BroadcastRGB11OutOfBand(transferIDs []string) (string, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || p.rgbManager.evidence == nil || len(transferIDs) == 0 {
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
	for _, pending := range pendingList {
		if pending.State.TransportMode != "out-of-band" || pending.State.Expiry <= time.Now().Unix() ||
			pending.State.WitnessTxID != first.State.WitnessTxID || pending.State.BatchID != first.State.BatchID ||
			!bytes.Equal(pending.SignedTx, first.SignedTx) {
			return "", ErrRGB11BatchAckRequired
		}
		pending.State.AckStatus = "accepted-out-of-band"
	}
	if err := p.rgbManager.projectionStore.SavePendingTransferStates(pendingList); err != nil {
		return "", err
	}
	p.autoBackupRGB11AfterMutation()
	if err := p.requireLatestRGB11WalletState(); err != nil {
		return "", err
	}
	txID, err := p.rgbManager.evidence.Broadcast(first.SignedTx)
	if err != nil {
		return "", err
	}
	if txID != "" && txID != first.State.WitnessTxID {
		return "", fmt.Errorf("RGB11 backend returned witness txid %s, expected %s", txID, first.State.WitnessTxID)
	}
	for _, pending := range pendingList {
		pending.State.Status = "broadcast"
	}
	if err := p.rgbManager.projectionStore.SavePendingTransferStates(pendingList); err != nil {
		return "", err
	}
	p.autoBackupRGB11AfterMutation()
	return first.State.WitnessTxID, nil
}

func (p *rgb11Manager) verifyRGB11RecipientAck(pending *rgb11wallet.PendingTransfer,
	relayRecord *corerelay.RelayRecord, ack *corerelay.AckRecord) error {
	if err := p.verifyRGB11RecipientDecision(pending, relayRecord, ack); err != nil || !ack.Accepted {
		return ErrRGB11AckRequired
	}
	return nil
}

func (p *rgb11Manager) verifyRGB11RecipientDecision(pending *rgb11wallet.PendingTransfer,
	relayRecord *corerelay.RelayRecord, ack *corerelay.AckRecord) error {
	if pending == nil || relayRecord == nil || ack == nil {
		return ErrRGB11AckRequired
	}
	if relayRecord.TransferID != pending.State.TransferID || relayRecord.ObjectHash != ack.ConsignmentHash ||
		ack.TransferID != pending.State.TransferID || ack.RecipientID != pending.State.RecipientID {
		return ErrRGB11AckRequired
	}
	relayHash, err := relayRecord.Hash()
	if err != nil || relayHash != ack.RelayRecordHash {
		return ErrRGB11AckRequired
	}
	recipientPubKey, err := hex.DecodeString(pending.State.RecipientID)
	if err != nil || ack.Verify(recipientPubKey, rgb11wallet.VerifyWalletSignature) != nil {
		return ErrRGB11AckRequired
	}
	if err := relayRecord.Verify(relayRecord.SenderPubKey, time.Now().Unix(), rgb11wallet.VerifyWalletSignature); err != nil {
		return err
	}
	if !bytes.Equal(relayRecord.SenderPubKey, rgb11wallet.WalletPubKey(p.wallet)) {
		return ErrRGB11AckRequired
	}
	return nil
}

// CancelRGB11BatchByNack authenticates one recipient NACK and atomically
// terminates all sibling transfers that share the unbroadcast Bitcoin tx.
func (p *rgb11Manager) CancelRGB11BatchByNack(transferID string, relayRecord *corerelay.RelayRecord,
	nack *corerelay.AckRecord) error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || transferID == "" || nack == nil || nack.Accepted {
		return ErrRGB11AckRequired
	}
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return err
	}
	if pending.State.TransportMode != "sat20-dkvs" {
		return ErrRGB11SAT20RelayRequired
	}
	if err := p.verifyRGB11RecipientDecision(pending, relayRecord, nack); err != nil {
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
		if item.State.TransportMode != "sat20-dkvs" {
			return ErrRGB11SAT20RelayRequired
		}
		if item.State.BatchID != pending.State.BatchID || item.State.WitnessTxID != pending.State.WitnessTxID ||
			item.State.ConsignmentHash != pending.State.ConsignmentHash {
			return ErrRGB11BatchAckRequired
		}
		pendingList = append(pendingList, item)
	}
	return p.cancelRGB11PendingBatch(pendingList, nack.ReasonCode, nil)
}

// CancelRGB11OutOfBandTransfer releases an out-of-band batch that has not
// been broadcast. Bitcoin evidence is checked fail-closed before local input
// locks and private transfer payloads are released.
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

func (p *rgb11Manager) cancelRGB11PendingBatch(pendingList []*rgb11wallet.PendingTransfer,
	reason string, rejectedOpouts []string) error {
	if len(pendingList) == 0 {
		return ErrRGB11BatchAckRequired
	}
	if reason == "" {
		reason = RGB11RejectReasonUser
	}
	ids := make([]string, 0, len(pendingList))
	inputs := make(map[string]struct{})
	for _, pending := range pendingList {
		pending.State.AckStatus = "rejected"
		pending.State.Status = "rejected"
		pending.State.RejectReason = reason
		pending.State.RejectedOpouts = append([]string(nil), rejectedOpouts...)
		ids = append(ids, pending.State.TransferID)
		for _, outpoint := range pending.State.InputOutPoints {
			inputs[outpoint] = struct{}{}
		}
	}
	if err := p.rgbManager.projectionStore.SavePendingTransferStates(pendingList); err != nil {
		return err
	}
	locked := p.utxoLockerL1.GetLockedUtxoList()
	for outpoint := range inputs {
		if item := locked[outpoint]; item != nil && item.Reason == rgb11wallet.LockReasonPending {
			if err := p.utxoLockerL1.UnlockUtxo(outpoint); err != nil {
				return err
			}
		}
	}
	for _, pending := range pendingList {
		for _, seal := range pending.ChangeSeals {
			outpoint := fmt.Sprintf("%s:%d", pending.State.WitnessTxID, seal.Vout)
			if _, err := p.rgbManager.projectionStore.LoadOutput(outpoint); err == nil {
				continue
			} else if !errors.Is(err, indexer.ErrKeyNotFound) {
				return err
			}
			if item := locked[outpoint]; item != nil && item.Reason == rgb11wallet.LockReasonPending {
				if err := p.utxoLockerL1.UnlockUtxo(outpoint); err != nil {
					return err
				}
			}
		}
	}
	if err := p.rgbManager.projectionStore.CompactRejectedTransfers(ids); err != nil {
		return err
	}
	p.autoBackupRGB11AfterMutation()
	return nil
}

func decodeRGB11Hash(value string) ([32]byte, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return [32]byte{}, fmt.Errorf("invalid RGB11 hash")
	}
	var result [32]byte
	copy(result[:], decoded)
	return result, nil
}

func SignRGB11RelayRecord(wallet common.Wallet, record *corerelay.RelayRecord) error {
	if wallet == nil || record == nil {
		return corerelay.ErrInvalidRecord
	}
	record.SenderPubKey = rgb11wallet.WalletPubKey(wallet)
	return record.Sign(rgb11wallet.WalletSigner{Wallet: wallet})
}

func SignRGB11AckRecord(wallet common.Wallet, record *corerelay.AckRecord) error {
	if wallet == nil || record == nil {
		return corerelay.ErrInvalidRecord
	}
	record.RecipientPubKey = rgb11wallet.WalletPubKey(wallet)
	return record.Sign(rgb11wallet.WalletSigner{Wallet: wallet})
}
