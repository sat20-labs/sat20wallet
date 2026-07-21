package wallet

import (
	"bytes"

	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
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
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	rgb11AddressMailboxPageSize = 256
	rgb11AddressTemporaryTTL    = uint64((24 * time.Hour) / time.Millisecond)
	rgb11AddressAutopayTTL      = uint64((365 * 24 * time.Hour) / time.Millisecond)
)

func (p *rgb11Manager) configuredRGB11DKVSClient() (*SatsNetDKVSClient, error) {
	if p == nil {
		return nil, ErrRGB11Inconsistent
	}
	return p.rgb11DKVSClient()
}

// configureRGB11AddressRetention selects AUTOPAY for an active DKVS tenant and
// otherwise applies a temporary TTL. Failure to query the tenant contract does
// not block an address transfer; it safely falls back to temporary retention,
// which is surfaced by RGB11AddressDeliveryResult.Temporary.
func (p *rgb11Manager) configureRGB11AddressRetention(record *dkvsindexer.RecordOptions,
	autopay **DKVSAutopayOptions) {
	if record == nil || autopay == nil {
		return
	}
	if *autopay == nil {
		if paid, err := p.hasActiveRGB11Autopay(); err == nil && paid {
			*autopay = &DKVSAutopayOptions{AddressParams: GetChainParam_SatsNet()}
		}
	}
	if record.TTL == 0 && record.ExpiryHeight == 0 {
		if *autopay != nil {
			record.TTL = rgb11AddressAutopayTTL
		} else {
			record.TTL = rgb11AddressTemporaryTTL
		}
	}
}

func (p *rgb11Manager) EnableConfiguredRGB11AddressReceive(options RGB11ReceiveCapabilityOptions) (*RGB11AddressEndpoint, error) {
	client, err := p.configuredRGB11DKVSClient()
	if err != nil {
		return nil, err
	}
	p.configureRGB11AddressRetention(&options.RecordOptions, &options.Autopay)
	return p.EnableRGB11AddressReceive(client, options)
}

func (p *rgb11Manager) ResolveConfiguredRGB11AddressEndpoint(address string,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11AddressEndpoint, error) {
	client, err := p.configuredRGB11DKVSClient()
	if err != nil {
		return nil, err
	}
	return p.ResolveRGB11AddressEndpoint(client, address, verify)
}

func (p *rgb11Manager) PrepareConfiguredRGB11AddressTransfer(ctx context.Context, request RGB11AddressSendRequest,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11PreparedTransfer, *RGB11AddressEndpoint, error) {
	client, err := p.configuredRGB11DKVSClient()
	if err != nil {
		return nil, nil, err
	}
	return p.PrepareRGB11AddressTransfer(ctx, client, request, verify)
}

func (p *rgb11Manager) DeliverAndBroadcastConfiguredRGB11AddressTransfer(transferID string,
	options RGB11AddressDeliveryOptions) (*RGB11AddressDeliveryResult, error) {
	client, err := p.configuredRGB11DKVSClient()
	if err != nil {
		return nil, err
	}
	p.configureRGB11AddressRetention(&options.RecordOptions, &options.Autopay)
	return p.DeliverAndBroadcastRGB11AddressTransfer(client, transferID, options)
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
	client, err := p.configuredRGB11DKVSClient()
	if err != nil {
		return nil, err
	}
	p.configureRGB11AddressRetention(&ackOptions.RecordOptions, &ackOptions.Autopay)
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
	for start := 0; ; start += rgb11AddressMailboxPageSize {
		records, total, err := client.ListRecords(prefix, start, rgb11AddressMailboxPageSize)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			result.Scanned++
			_, _, messageID, parseErr := parseRGB11AddressMailboxKey(record)
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
				if _, err := p.AcceptRGB11AddressACK(record, verify); err != nil {
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
			if _, _, err := p.AcceptRGB11AddressMailbox(ctx, client, record, verify, ackOptions); err != nil {
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
		if start+len(records) >= total || len(records) == 0 {
			break
		}
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

func nextRGB11CapabilityRecordOptions(client *SatsNetDKVSClient, key string, value []byte,
	opts dkvsindexer.RecordOptions) (dkvsindexer.RecordOptions, *swire.DKVSRecord) {
	if opts.Seq != 0 || client == nil {
		return opts, nil
	}
	existing, err := client.GetRecord(key)
	if err != nil || existing == nil || existing.Version != dkvsindexer.Version {
		opts.Seq = 1
		return opts, nil
	}
	if bytes.Equal(existing.Value, value) {
		opts.Seq = existing.Seq
	} else {
		opts.Seq = existing.Seq + 1
	}
	return opts, existing
}

func putRGB11CapabilityRecord(client *SatsNetDKVSClient, wallet *InternalWallet, key string, value []byte,
	opts dkvsindexer.RecordOptions, autopay *DKVSAutopayOptions) (*swire.DKVSRecord, error) {
	opts, existing := nextRGB11CapabilityRecordOptions(client, key, value, opts)
	if existing != nil && bytes.Equal(existing.Value, value) &&
		opts.ExpiryHeight <= existing.ExpiryHeight && opts.TTL <= existing.TTL {
		return existing, nil
	}
	if autopay != nil {
		return client.PutAccountSignedRecordWithAutopay(wallet, key, value, opts, *autopay)
	}
	return client.PutAccountSignedRecord(wallet, key, value, opts)
}

// EnableRGB11AddressReceive publishes the public address mapping and the
// minimal account capability. Callers should invoke this when DKVS wallet
// communication/storage is enabled for the current SAT20 subaccount.
func (p *rgb11Manager) EnableRGB11AddressReceive(client *SatsNetDKVSClient,
	options RGB11ReceiveCapabilityOptions) (*RGB11AddressEndpoint, error) {
	if p == nil || client == nil || p.wallet == nil {
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
	value, err := rgb11wallet.EncodeReceiveCapability(RGB11ReceiveCapability{
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
	network := GetChainParam().Name
	mappingKey, err := dkvsindexer.AccountMappingKey(network, address)
	if err != nil {
		return nil, err
	}
	mappingValue, err := dkvsindexer.EncodeAccountMappingValue(accountID)
	if err != nil {
		return nil, err
	}
	if _, err := putRGB11CapabilityRecord(
		client, wallet, mappingKey, mappingValue, options.RecordOptions, options.Autopay,
	); err != nil {
		return nil, err
	}
	capabilityKey, err := dkvsindexer.AccountPersonalKey(accountID, RGB11ReceiveCapabilityPath)
	if err != nil {
		return nil, err
	}
	if _, err := putRGB11CapabilityRecord(
		client, wallet, capabilityKey, value, options.RecordOptions, options.Autopay,
	); err != nil {
		return nil, err
	}
	verify := dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())}
	return p.ResolveRGB11AddressEndpoint(client, address, verify)
}

func (p *rgb11Manager) ResolveRGB11AddressEndpoint(client *SatsNetDKVSClient, address string,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11AddressEndpoint, error) {
	if client == nil || address == "" {
		return nil, ErrRGB11TraditionalReceiveRequired
	}
	if verify.Now == 0 {
		verify.Now = uint64(time.Now().UnixMilli())
	}
	accountID, _, err := client.ResolveAccountAddress(GetChainParam().Name, address, verify)
	if err != nil {
		return nil, ErrRGB11TraditionalReceiveRequired
	}
	key, err := dkvsindexer.AccountPersonalKey(accountID, RGB11ReceiveCapabilityPath)
	if err != nil {
		return nil, err
	}
	record, err := client.GetRecord(key)
	if err != nil {
		return nil, ErrRGB11TraditionalReceiveRequired
	}
	verify.ExpectedKey = key
	if err := dkvsindexer.VerifyAccountRecordForClient(record, verify); err != nil {
		return nil, ErrRGB11TraditionalReceiveRequired
	}
	capability, err := rgb11wallet.DecodeReceiveCapability(record.Value)
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
	recordHash := dkvsindexer.RecordHash(record)
	return &RGB11AddressEndpoint{
		AccountID:            accountID,
		Address:              address,
		MailboxID:            accountID,
		CompressedPubKey:     pubKey,
		PkScript:             pkScript,
		CapabilityFlags:      capability.Flags,
		CapabilityRecordKey:  key,
		CapabilityRecordHash: hex.EncodeToString(recordHash[:]),
		Temporary:            len(record.FeeProof) == 0,
		ExpiryHeight:         record.ExpiryHeight,
		TTL:                  record.TTL,
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

func nextRGB11AddressRecordOptions(client *SatsNetDKVSClient, keys []string, opts dkvsindexer.RecordOptions) dkvsindexer.RecordOptions {
	if opts.IssueTime == 0 {
		opts.IssueTime = uint64(time.Now().UnixMilli())
	}
	if opts.Seq != 0 {
		return opts
	}
	var maxSeq uint64
	for _, key := range keys {
		if key == "" || client == nil {
			continue
		}
		existing, err := client.GetRecord(key)
		if err == nil && existing != nil && existing.Seq > maxSeq {
			maxSeq = existing.Seq
		}
	}
	opts.Seq = maxSeq + 1
	return opts
}

// DeliverRGB11AddressTransfer encrypts the recipient Consignment and writes it
// before any Bitcoin broadcast. Small ciphertexts are carried directly by the
// mailbox record; larger ones use the native sender-owned DKVS blob and a
// two-byte mailbox locator.
func (p *rgb11Manager) DeliverRGB11AddressTransfer(client *SatsNetDKVSClient, transferID string,
	options RGB11AddressDeliveryOptions) (*RGB11AddressDeliveryResult, error) {
	if p == nil || client == nil || p.wallet == nil || p.rgbManager == nil ||
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
	cryptor, ok := p.wallet.(rgb11AccountPayloadCryptor)
	if !ok {
		return nil, fmt.Errorf("active wallet does not support RGB11 account encryption")
	}
	ciphertext, err := cryptor.EncryptToAccount(
		pending.State.ReceiverAccountID, pending.RecipientConsignment,
	)
	if err != nil {
		return nil, err
	}
	inlineLimit := options.InlineLimit
	if inlineLimit <= 0 || inlineLimit > rgb11AddressInlineLimit {
		inlineLimit = rgb11AddressInlineLimit
	}
	mode := rgb11AddressEnvelopeInline
	objectID := ""
	revisionKeys := []string{pending.State.DeliveryRecordKey}
	if len(ciphertext)+2 > inlineLimit {
		mode = rgb11AddressEnvelopeBlob
		objectID = messageID
		manifestKey, err := dkvsindexer.BlobManifestKey(pending.State.SenderAccountID, objectID)
		if err != nil {
			return nil, err
		}
		revisionKeys = append(revisionKeys, manifestKey)
	}
	opts := nextRGB11AddressRecordOptions(client, revisionKeys, options.RecordOptions)
	if mode == rgb11AddressEnvelopeBlob {
		if _, _, err := client.PutAccountBlob(
			p.wallet, objectID, ciphertext, nil, opts, options.Autopay,
		); err != nil {
			return nil, err
		}
	}
	mailValue, err := rgb11wallet.EncodeAddressEnvelope(mode, ciphertext)
	if err != nil {
		return nil, err
	}
	if mode == rgb11AddressEnvelopeBlob {
		mailValue, _ = rgb11wallet.EncodeAddressEnvelope(mode, nil)
	}
	record, err := client.SendAccountMailboxMessage(
		p.wallet, pending.State.ReceiverAccountID, messageID, mailValue, opts, options.Autopay,
	)
	if err != nil {
		return nil, err
	}
	recordHash := dkvsindexer.RecordHash(record)
	modeName := "inline"
	if mode == rgb11AddressEnvelopeBlob {
		modeName = "blob"
	}
	pending.State.DeliveryMode = modeName
	pending.State.DeliveryObjectID = objectID
	pending.State.DeliveryRecordKey = record.Key
	pending.State.RelayRecordKey = record.Key
	pending.State.DeliveryRecordHash = hex.EncodeToString(recordHash[:])
	pending.State.DeliveryTemporary = options.Autopay == nil
	pending.State.DeliveryExpiryHeight = record.ExpiryHeight
	pending.State.DeliveryTTL = record.TTL
	pending.State.RelayExpiry = int64(record.ExpiryHeight)
	pending.State.RelayDurability = "DKVS_PERSISTENT"
	if options.Autopay == nil {
		pending.State.RelayDurability = "DKVS_TEMP"
	}
	pending.State.Status = "delivered"
	if err := p.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
		return nil, err
	}
	p.autoBackupRGB11AfterMutation()
	return &RGB11AddressDeliveryResult{
		TransferID: transferID,
		Mode:       modeName,
		RecordKey:  record.Key,
		RecordHash: pending.State.DeliveryRecordHash,
		ObjectID:   objectID,
		Temporary:  pending.State.DeliveryTemporary,
	}, nil
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

func (p *rgb11Manager) DeliverAndBroadcastRGB11AddressTransfer(client *SatsNetDKVSClient, transferID string,
	options RGB11AddressDeliveryOptions) (*RGB11AddressDeliveryResult, error) {
	result, err := p.DeliverRGB11AddressTransfer(client, transferID, options)
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

func (p *rgb11Manager) readRGB11AddressConsignment(client *SatsNetDKVSClient, record *swire.DKVSRecord,
	senderID, messageID string, verify dkvsindexer.RecordVerificationOptions) ([]byte, string, error) {
	mode, encrypted, err := rgb11wallet.DecodeAddressEnvelope(record.Value)
	if err != nil {
		return nil, "", err
	}
	modeName := "inline"
	if mode == rgb11AddressEnvelopeBlob {
		modeName = "blob"
		_, encrypted, err = client.GetAccountBlob(senderID, messageID, dkvsindexer.DefaultBlobPolicy(), verify)
		if err != nil {
			return nil, "", fmt.Errorf("%w: %v", ErrRGB11AddressMailbox, err)
		}
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
	walletScript, err := AddrToPkScript(p.wallet.GetAddress(), GetChainParam())
	if err != nil {
		return nil, nil, err
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
		if string(utxo.PkScript) != string(walletScript) {
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

// AcceptRGB11AddressMailbox validates an address-mode message after its Bitcoin
// transaction is visible, projects the receiver allocation, persists state,
// locks the actual outpoint, and only then emits the minimal ACK.
func (p *rgb11Manager) AcceptRGB11AddressMailbox(ctx context.Context, client *SatsNetDKVSClient,
	record *swire.DKVSRecord, verify dkvsindexer.RecordVerificationOptions,
	ackOptions RGB11AddressDeliveryOptions) (*rgb11wallet.ValidationReceipt, *swire.DKVSRecord, error) {
	if p == nil || client == nil || p.wallet == nil || p.rgbManager == nil {
		return nil, nil, ErrRGB11AddressMailbox
	}
	if verify.Now == 0 {
		verify.Now = uint64(time.Now().UnixMilli())
	}
	if err := dkvsindexer.VerifyAccountRecordForClient(record, verify); err != nil {
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
	raw, mode, err := p.readRGB11AddressConsignment(client, record, senderID, messageID, verify)
	if err != nil {
		return nil, nil, err
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
	accepted, err := p.acceptRGB11Consignment(ctx, request.RequestID, raw, false)
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
	ackRecord, err := p.SendRGB11AddressACK(client, senderID, messageID,
		RGB11AddressACK{Status: RGB11AddressACKAccepted}, ackOptions)
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

func (p *rgb11Manager) SendRGB11AddressACK(client *SatsNetDKVSClient, senderAccountID, messageID string,
	ack RGB11AddressACK, options RGB11AddressDeliveryOptions) (*swire.DKVSRecord, error) {
	if p == nil || client == nil || senderAccountID == "" || messageID == "" {
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
	opts := nextRGB11AddressRecordOptions(client, []string{key}, options.RecordOptions)
	return client.SendAccountMailboxMessage(p.wallet, senderAccountID, messageID, value, opts, options.Autopay)
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

func synthesizeRGB11AddressInvoice(endpoint *RGB11AddressEndpoint, asset indexer.AssetName,
	amount uint64, expiry int64) (string, error) {
	if endpoint == nil || endpoint.AccountID == "" || len(endpoint.PkScript) == 0 || amount == 0 {
		return "", ErrRGB11TraditionalReceiveRequired
	}
	officialID, err := rgb11wallet.OfficialAssetID(asset)
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

// PrepareRGB11AddressTransfer resolves the receiver's account capability and
// synthesizes a private witness invoice solely to reuse the audited RGB11
// transition builder. The synthesized invoice is removed from persisted public
// lifecycle state immediately after preparation.
func (p *rgb11Manager) PrepareRGB11AddressTransfer(ctx context.Context, client *SatsNetDKVSClient,
	request RGB11AddressSendRequest, verify dkvsindexer.RecordVerificationOptions) (
	*RGB11PreparedTransfer, *RGB11AddressEndpoint, error,
) {
	if p == nil || client == nil || request.ReceiverAddress == "" ||
		request.AssetName.Protocol != rgb11wallet.Protocol {
		return nil, nil, ErrRGB11TraditionalReceiveRequired
	}
	if verify.Now == 0 {
		verify.Now = uint64(time.Now().UnixMilli())
	}
	endpoint, err := p.ResolveRGB11AddressEndpoint(client, request.ReceiverAddress, verify)
	if err != nil {
		return nil, nil, err
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
	invoice, err := synthesizeRGB11AddressInvoice(endpoint, request.AssetName, amount, request.Expiry)
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
	rgb11WalletSnapshotVersion  uint32 = 1
	rgb11AutoBackupMetadataName        = "autobackup-policy"
)

type rgb11SnapshotCryptor interface {
	EncryptTo(pubKeyBytes []byte, plaintext []byte) ([]byte, error)
	Decrypt(data []byte, pubKeyBytes []byte) ([]byte, error)
}

func (p *rgb11Manager) RGB11WalletID() (string, error) {
	if p == nil || p.wallet == nil || p.wallet.GetPubKey() == nil {
		return "", ErrRGB11WalletLocked
	}
	return hex.EncodeToString(p.wallet.GetPubKey().SerializeCompressed()), nil
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
	p.mutex.RLock()
	tickers := make([]*indexer.TickerInfo, 0)
	for _, info := range p.tickerInfoMap {
		if info != nil && info.AssetName.Protocol == rgb11wallet.Protocol {
			copy := *info
			tickers = append(tickers, &copy)
		}
	}
	p.mutex.RUnlock()
	sort.Slice(tickers, func(i, j int) bool { return tickers[i].AssetName.String() < tickers[j].AssetName.String() })
	snapshot := &RGB11WalletSnapshot{
		Version: rgb11WalletSnapshotVersion, WalletID: walletID,
		AccountIndex: p.wallet.GetSubAccount(), EngineBuildID: rgb11wallet.NativeEngineBuildID,
		ProjectionRecords: projection, EngineRecords: engine, TickerInfos: tickers,
	}
	encoded, err := rgb11wallet.EncodeWalletSnapshotPayload(snapshot)
	return snapshot, encoded, err
}

// BackupRGB11WalletState publishes an encrypted immutable snapshot and then
// advances the single latest head record. Both records are signed only by the
// owning wallet through DKVS /personal; there is no system/checkpoint key.
func (p *rgb11Manager) BackupRGB11WalletState(client *SatsNetDKVSClient, walletID string,
	previous *coresync.WalletHead, opts dkvsindexer.RecordOptions) (*coresync.WalletHead, *swire.DKVSRecord, error) {
	if client == nil || opts.TTL == 0 || p == nil || p.wallet == nil {
		return nil, nil, ErrRGB11Inconsistent
	}
	stableID, err := p.RGB11WalletID()
	if err != nil {
		return nil, nil, err
	}
	if walletID == "" {
		walletID = stableID
	} else if walletID != stableID {
		return nil, nil, coresync.ErrHeadWallet
	}
	_, plaintext, err := p.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		return nil, nil, err
	}
	stateHash := sha256.Sum256(plaintext)
	operationInput := append([]byte("SAT20-RGB11-WALLET-SNAPSHOT-V1"), stateHash[:]...)
	nextSeq := uint64(1)
	if previous != nil {
		nextSeq = previous.Seq + 1
	}
	var sequence [8]byte
	binary.LittleEndian.PutUint64(sequence[:], nextSeq)
	operationInput = append(operationInput, sequence[:]...)
	operationID := sha256.Sum256(operationInput)
	head, err := NewRGB11WalletHead(walletID, stateHash, operationID, previous)
	if err != nil {
		return nil, nil, err
	}
	cryptor, ok := p.wallet.(rgb11SnapshotCryptor)
	if !ok {
		return nil, nil, fmt.Errorf("active wallet does not support RGB11 snapshot encryption")
	}
	pubkey := p.wallet.GetPubKey().SerializeCompressed()
	ciphertext, err := cryptor.EncryptTo(pubkey, plaintext)
	if err != nil {
		return nil, nil, err
	}
	envelope, err := rgb11wallet.EncodeEncryptedSnapshot(walletID, operationID, ciphertext)
	if err != nil {
		return nil, nil, err
	}
	autopay := DKVSAutopayOptions{AddressParams: GetChainParam_SatsNet()}
	if _, err := client.PutRGB11WalletSnapshotWithAutopay(p.wallet, walletID, operationID, envelope, opts, autopay); err != nil {
		return nil, nil, err
	}
	record, err := client.PutRGB11WalletHeadWithAutopay(p.wallet, head, opts, autopay)
	if err != nil {
		return nil, nil, err
	}
	if err := p.persistRGB11WalletHead(head); err != nil {
		p.rgbManager.dkvsStatus = "warning"
		return nil, nil, err
	}
	p.rgbManager.dkvsStatus = "synced"
	p.rgbManager.head = head
	return head, record, nil
}

// RestoreRGB11WalletState resolves the active latest wallet-signed head,
// decrypts its immutable snapshot and imports it into the current local scope.
func (p *rgb11Manager) RestoreRGB11WalletState(client *SatsNetDKVSClient, walletID string,
	verifyOpts dkvsindexer.RecordVerificationOptions) (*coresync.WalletHead, error) {
	if client == nil || p == nil || p.wallet == nil || p.rgbManager.projectionStore == nil || p.rgbManager.engineStore == nil {
		return nil, ErrRGB11Inconsistent
	}
	stableID, err := p.RGB11WalletID()
	if err != nil {
		return nil, err
	}
	if walletID == "" {
		walletID = stableID
	} else if walletID != stableID {
		return nil, coresync.ErrHeadWallet
	}
	pubkey := p.wallet.GetPubKey().SerializeCompressed()
	head, _, err := client.GetRGB11WalletHead(pubkey, walletID, verifyOpts)
	if err != nil {
		p.rgbManager.dkvsStatus = "offline"
		return nil, err
	}
	raw, _, err := client.GetRGB11WalletSnapshot(pubkey, walletID, head.OperationID, verifyOpts)
	if err != nil {
		p.rgbManager.dkvsStatus = "conflict"
		return nil, err
	}
	envelopeWalletID, envelopeOperationID, ciphertext, err := rgb11wallet.DecodeEncryptedSnapshot(raw)
	if err != nil || envelopeWalletID != walletID || envelopeOperationID != head.OperationID || len(ciphertext) == 0 {
		p.rgbManager.dkvsStatus = "conflict"
		return nil, ErrRGB11Inconsistent
	}
	cryptor, ok := p.wallet.(rgb11SnapshotCryptor)
	if !ok {
		return nil, fmt.Errorf("active wallet does not support RGB11 snapshot decryption")
	}
	plaintext, err := cryptor.Decrypt(ciphertext, pubkey)
	if err != nil || !bytes.Equal(head.StateHash[:], hashBytes(plaintext)) {
		p.rgbManager.dkvsStatus = "conflict"
		return nil, ErrRGB11Inconsistent
	}
	snapshot, err := rgb11wallet.DecodeWalletSnapshotPayload(plaintext)
	if err != nil || snapshot.Version != rgb11WalletSnapshotVersion ||
		snapshot.WalletID != walletID || snapshot.AccountIndex != p.wallet.GetSubAccount() ||
		snapshot.EngineBuildID != rgb11wallet.NativeEngineBuildID {
		p.rgbManager.dkvsStatus = "conflict"
		return nil, ErrRGB11Inconsistent
	}
	if err := p.rgbManager.engineStore.ImportSnapshot(snapshot.EngineRecords); err != nil {
		return nil, err
	}
	if err := p.rgbManager.projectionStore.ImportSnapshot(snapshot.ProjectionRecords); err != nil {
		p.rgbManager.consistencyStatus = "broken"
		return nil, err
	}
	for _, info := range snapshot.TickerInfos {
		if err := p.RegisterRGB11TickerInfo(info); err != nil {
			p.rgbManager.consistencyStatus = "broken"
			return nil, err
		}
	}
	if err := p.rebuildRGB11Locks(); err != nil {
		return nil, err
	}
	if err := p.persistRGB11WalletHead(head); err != nil {
		p.rgbManager.dkvsStatus = "warning"
		return nil, err
	}
	p.rgbManager.dkvsStatus = "synced"
	p.rgbManager.head = head
	return head, nil
}

func (p *rgb11Manager) rgb11DKVSClient() (*SatsNetDKVSClient, error) {
	if !p.rgb11DKVSConfigured() {
		return nil, ErrRGB11Inconsistent
	}
	return NewSatsNetDKVSClient(p.cfg.IndexerL2.Scheme, p.cfg.IndexerL2.Host, p.cfg.IndexerL2.Proxy, p.http), nil
}

func (p *rgb11Manager) rgb11DKVSConfigured() bool {
	return p != nil && p.cfg != nil && p.http != nil && p.cfg.IndexerL2 != nil && p.cfg.IndexerL2.Host != ""
}

// requireLatestRGB11WalletState is the last guard before an irreversible
// external effect. When DKVS is configured, the local state must still match
// the wallet-signed head currently selected for this wallet.
func (p *rgb11Manager) requireLatestRGB11WalletState() error {
	p.waitForRGB11AutoBackup()
	if !p.rgb11DKVSConfigured() {
		return nil
	}
	if p.rgbManager.head == nil {
		p.rgbManager.dkvsStatus = "conflict"
		return coresync.ErrHeadConflict
	}
	walletID, err := p.RGB11WalletID()
	if err != nil {
		return err
	}
	_, plaintext, err := p.exportRGB11WalletSnapshot(walletID)
	if err != nil || sha256.Sum256(plaintext) != p.rgbManager.head.StateHash {
		p.rgbManager.dkvsStatus = "conflict"
		return coresync.ErrHeadConflict
	}
	client, err := p.rgb11DKVSClient()
	if err != nil {
		return err
	}
	active, _, err := client.GetRGB11WalletHead(
		p.wallet.GetPubKey().SerializeCompressed(), walletID,
		dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())},
	)
	if err != nil {
		p.rgbManager.dkvsStatus = "offline"
		return err
	}
	localHash, err := p.rgbManager.head.Hash()
	if err != nil {
		return err
	}
	activeHash, err := active.Hash()
	if err != nil || localHash != activeHash {
		p.rgbManager.dkvsStatus = "conflict"
		return coresync.ErrHeadConflict
	}
	p.rgbManager.dkvsStatus = "synced"
	return nil
}

func (p *rgb11Manager) SyncRGB11WalletState(walletID string, opts dkvsindexer.RecordOptions) (*coresync.WalletHead, error) {
	return p.syncRGB11WalletState(walletID, opts, true)
}

func (p *rgb11Manager) ensureRGB11AddressReceiveForDKVS(client *SatsNetDKVSClient,
	opts dkvsindexer.RecordOptions) error {
	if p == nil || client == nil || p.wallet == nil {
		return ErrRGB11Inconsistent
	}
	autopay := &DKVSAutopayOptions{AddressParams: GetChainParam_SatsNet()}
	_, err := p.EnableRGB11AddressReceive(client, RGB11ReceiveCapabilityOptions{
		RecordOptions: opts,
		Autopay:       autopay,
	})
	return err
}

func (p *rgb11Manager) syncRGB11WalletState(walletID string, opts dkvsindexer.RecordOptions, enableAuto bool) (*coresync.WalletHead, error) {
	p.rgbManager.backupMutex.Lock()
	defer p.rgbManager.backupMutex.Unlock()
	if !enableAuto && p.rgbManager.head != nil {
		stableID, err := p.RGB11WalletID()
		if err == nil {
			_, plaintext, exportErr := p.exportRGB11WalletSnapshot(stableID)
			if exportErr == nil && sha256.Sum256(plaintext) == p.rgbManager.head.StateHash {
				return p.rgbManager.head, nil
			}
		}
	}
	client, err := p.rgb11DKVSClient()
	if err != nil {
		return nil, err
	}
	head, _, err := p.BackupRGB11WalletState(client, walletID, p.rgbManager.head, opts)
	if err != nil {
		return nil, err
	}
	if err := p.ensureRGB11AddressReceiveForDKVS(client, opts); err != nil {
		p.rgbManager.dkvsStatus = "warning"
		return nil, err
	}
	if enableAuto {
		if err := p.enableRGB11AutoBackup(opts); err != nil {
			p.rgbManager.dkvsStatus = "warning"
			return nil, err
		}
	}
	return head, nil
}

func (p *rgb11Manager) RestoreLatestRGB11WalletState(walletID string,
	verifyOpts dkvsindexer.RecordVerificationOptions) (*coresync.WalletHead, error) {
	p.rgbManager.backupMutex.Lock()
	defer p.rgbManager.backupMutex.Unlock()
	client, err := p.rgb11DKVSClient()
	if err != nil {
		return nil, err
	}
	stableID, err := p.RGB11WalletID()
	if err != nil {
		return nil, err
	}
	if walletID == "" {
		walletID = stableID
	}
	_, record, err := client.GetRGB11WalletHead(p.wallet.GetPubKey().SerializeCompressed(), walletID, verifyOpts)
	if err != nil {
		return nil, err
	}
	head, err := p.RestoreRGB11WalletState(client, walletID, verifyOpts)
	if err != nil {
		return nil, err
	}
	retention := dkvsindexer.RecordOptions{TTL: record.TTL, ExpiryHeight: record.ExpiryHeight}
	if err := p.ensureRGB11AddressReceiveForDKVS(client, retention); err != nil {
		p.rgbManager.dkvsStatus = "warning"
		return nil, err
	}
	if err := p.enableRGB11AutoBackup(retention); err != nil {
		p.rgbManager.dkvsStatus = "warning"
		return nil, err
	}
	return head, nil
}

// ActivateRGB11WalletState is called after a wallet/account becomes active. A
// missing wallet-signed head means this wallet has never enabled RGB backup;
// in that case no paid write is attempted and the first backup remains a
// manual user action. When a head exists, the latest snapshot is restored and
// its retention policy enables subsequent automatic backups.
func (p *rgb11Manager) ActivateRGB11WalletState(verifyOpts dkvsindexer.RecordVerificationOptions) (*RGB11ActivationResult, error) {
	result := &RGB11ActivationResult{}
	if !p.rgb11DKVSConfigured() || p.wallet == nil || p.wallet.GetPubKey() == nil {
		return result, nil
	}
	if verifyOpts.Now == 0 {
		verifyOpts.Now = uint64(time.Now().UnixMilli())
	}
	walletID, err := p.RGB11WalletID()
	if err != nil {
		return nil, err
	}
	client, err := p.rgb11DKVSClient()
	if err != nil {
		return nil, err
	}
	_, _, err = client.GetRGB11WalletHead(p.wallet.GetPubKey().SerializeCompressed(), walletID, verifyOpts)
	if errors.Is(err, ErrDKVSRecordNotFound) {
		paid, paidErr := p.hasActiveRGB11Autopay()
		if paidErr != nil {
			p.rgbManager.dkvsStatus = "offline"
			return nil, paidErr
		}
		if !paid {
			p.rgbManager.dkvsStatus = "not_configured"
			return result, nil
		}
		head, syncErr := p.SyncRGB11WalletState("", dkvsindexer.RecordOptions{
			TTL: uint64((365 * 24 * time.Hour) / time.Millisecond),
		})
		if syncErr != nil {
			p.rgbManager.dkvsStatus = "warning"
			return nil, syncErr
		}
		result.Found = true
		result.AutoBackup = true
		result.Head = head
		return result, nil
	}
	if err != nil {
		p.rgbManager.dkvsStatus = "offline"
		return nil, err
	}
	result.Found = true
	head, err := p.RestoreLatestRGB11WalletState(walletID, verifyOpts)
	if err != nil {
		return nil, err
	}
	result.Restored = true
	result.AutoBackup = true
	result.Head = head
	return result, nil
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
	if state == nil || state.TemplateName != TEMPLATE_CONTRACT_AUTOPAY ||
		!strings.EqualFold(strings.TrimSpace(state.Status), "active") || state.Closed ||
		!strings.EqualFold(strings.TrimSpace(state.ServiceName), defaults.AutopayServiceName) ||
		!strings.EqualFold(strings.TrimSpace(state.Recipient), defaults.AutopayRecipient) ||
		strings.TrimSpace(state.FeeAssetName) != defaults.AutopayFeeAssetName {
		return false, nil
	}
	payer := PublicKeyToP2TRAddress_SatsNet(p.wallet.GetPubKey())
	delegate, ok := state.Delegates[payer]
	if !ok || !strings.EqualFold(strings.TrimSpace(delegate.Status), "active") {
		return false, nil
	}
	amount, amountOK := new(big.Rat).SetString(strings.TrimSpace(delegate.AmountPerBlock))
	balance, balanceOK := new(big.Rat).SetString(strings.TrimSpace(delegate.Balance))
	fullRecordFee, feeOK := new(big.Rat).SetString(strings.TrimSpace(defaults.FullRecordFeePerBlock))
	if !amountOK || !balanceOK || !feeOK || amount.Sign() <= 0 || balance.Sign() < 0 ||
		fullRecordFee.Sign() <= 0 || amount.Cmp(fullRecordFee) < 0 {
		return false, nil
	}
	return balance.Cmp(amount) >= 0, nil
}

func (p *rgb11Manager) enableRGB11AutoBackup(opts dkvsindexer.RecordOptions) error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || opts.TTL == 0 {
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
	p.mutex.Lock()
	p.rgbManager.autoBackup = policy
	p.mutex.Unlock()
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
	p.mutex.RLock()
	var policy RGB11AutoBackupPolicy
	if p.rgbManager.autoBackup != nil {
		policy = *p.rgbManager.autoBackup
	}
	p.mutex.RUnlock()
	if !policy.Enabled || policy.TTL == 0 {
		return
	}
	p.rgbManager.autoBackupMutex.Lock()
	p.rgbManager.autoBackupPending = true
	if p.rgbManager.autoBackupRunning {
		p.rgbManager.autoBackupMutex.Unlock()
		return
	}
	p.rgbManager.autoBackupRunning = true
	p.rgbManager.autoBackupDone = make(chan struct{})
	p.rgbManager.autoBackupMutex.Unlock()
	go p.runRGB11AutoBackup()
}

func (p *rgb11Manager) runRGB11AutoBackup() {
	for {
		p.rgbManager.autoBackupMutex.Lock()
		if !p.rgbManager.autoBackupPending {
			p.rgbManager.autoBackupRunning = false
			close(p.rgbManager.autoBackupDone)
			p.rgbManager.autoBackupMutex.Unlock()
			return
		}
		p.rgbManager.autoBackupPending = false
		p.rgbManager.autoBackupMutex.Unlock()

		p.mutex.RLock()
		var policy RGB11AutoBackupPolicy
		if p.rgbManager.autoBackup != nil {
			policy = *p.rgbManager.autoBackup
		}
		p.mutex.RUnlock()
		if !policy.Enabled || policy.TTL == 0 {
			continue
		}

		started := time.Now()
		Log.Infof("automatic RGB11 wallet backup started")
		if _, err := p.syncRGB11WalletState("", dkvsindexer.RecordOptions{
			TTL: policy.TTL, ExpiryHeight: policy.ExpiryHeight,
		}, false); err != nil {
			p.rgbManager.dkvsStatus = "warning"
			Log.Errorf("automatic RGB11 wallet backup failed after %v: %v", time.Since(started), err)
			continue
		}
		Log.Infof("automatic RGB11 wallet backup finished in %v", time.Since(started))
	}
}

func (p *rgb11Manager) waitForRGB11AutoBackup() {
	if p == nil || p.rgbManager == nil {
		return
	}
	p.rgbManager.autoBackupMutex.Lock()
	if !p.rgbManager.autoBackupRunning {
		p.rgbManager.autoBackupMutex.Unlock()
		return
	}
	done := p.rgbManager.autoBackupDone
	p.rgbManager.autoBackupMutex.Unlock()
	if done != nil {
		<-done
	}
}

func hashBytes(value []byte) []byte {
	hash := sha256.Sum256(value)
	return hash[:]
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

// NewRGB11WalletHead creates the compact head payload. PutRGB11WalletHead
// applies the owning wallet's signature once, to the enclosing DKVS record.
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

func (p *SatsNetDKVSClient) PutRGB11WalletHead(wallet common.Wallet, head *coresync.WalletHead, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	return p.putRGB11WalletHead(wallet, head, opts, nil)
}

func (p *SatsNetDKVSClient) PutRGB11WalletHeadWithAutopay(wallet common.Wallet, head *coresync.WalletHead,
	opts dkvsindexer.RecordOptions, autopay DKVSAutopayOptions) (*swire.DKVSRecord, error) {
	return p.putRGB11WalletHead(wallet, head, opts, &autopay)
}

func (p *SatsNetDKVSClient) putRGB11WalletHead(wallet common.Wallet, head *coresync.WalletHead,
	opts dkvsindexer.RecordOptions, autopay *DKVSAutopayOptions) (*swire.DKVSRecord, error) {
	pubKey, err := dkvsWalletPubKey(wallet)
	if err != nil {
		return nil, err
	}
	if err := VerifyRGB11WalletHead(head, head.WalletID); err != nil {
		return nil, err
	}
	value, err := head.StrictEncode()
	if err != nil {
		return nil, err
	}
	opts.Seq = head.Seq
	var posted *swire.DKVSRecord
	if autopay == nil {
		posted, err = p.PutPersonalRecord(wallet, RGB11WalletHeadPath(head.WalletID), value, opts)
	} else {
		posted, err = p.PutPersonalRecordWithAutopay(wallet, RGB11WalletHeadPath(head.WalletID), value, opts, *autopay)
	}
	if err != nil {
		return nil, err
	}
	_, active, err := p.GetRGB11WalletHead(pubKey, head.WalletID, dkvsindexer.RecordVerificationOptions{
		Now: uint64(time.Now().UnixMilli()),
	})
	if err != nil {
		return nil, err
	}
	if dkvsindexer.RecordHash(posted) != dkvsindexer.RecordHash(active) {
		return nil, coresync.ErrHeadConflict
	}
	return active, nil
}

func verifyRGB11DKVSAccountOwner(record *swire.DKVSRecord, walletPubKey []byte) error {
	if record == nil {
		return dkvsindexer.ErrInvalidRecord
	}
	expected, err := dkvsindexer.CanonicalAccountID(walletPubKey)
	if err != nil {
		return dkvsindexer.ErrPermissionDenied
	}
	parsed, err := dkvsindexer.ParseKey(record.Key)
	if err != nil {
		return err
	}
	actual, err := dkvsindexer.RecordSignerAccountID(record, parsed)
	if err != nil || actual != expected {
		return dkvsindexer.ErrPermissionDenied
	}
	return nil
}

func (p *SatsNetDKVSClient) GetRGB11WalletHead(walletPubKey []byte, walletID string, verifyOpts dkvsindexer.RecordVerificationOptions) (*coresync.WalletHead, *swire.DKVSRecord, error) {
	key, err := dkvsindexer.PersonalKey(walletPubKey, RGB11WalletHeadPath(walletID))
	if err != nil {
		return nil, nil, err
	}
	verifyOpts.ExpectedKey = key
	record, err := p.GetVerifiedRecord(key, verifyOpts)
	if err != nil {
		return nil, nil, err
	}
	if err := verifyRGB11DKVSAccountOwner(record, walletPubKey); err != nil {
		return nil, nil, err
	}
	head, err := rgb11wallet.DecodeWalletHead(record.Value)
	if err != nil {
		return nil, nil, err
	}
	if record.Seq != head.Seq {
		return nil, nil, coresync.ErrHeadSequence
	}
	if err := VerifyRGB11WalletHead(head, walletID); err != nil {
		return nil, nil, err
	}
	return head, record, nil
}

func (p *SatsNetDKVSClient) SubscribeRGB11WalletHead(walletPubKey []byte, walletID string) ([]*swire.DKVSRecord, int, error) {
	key, err := dkvsindexer.PersonalKey(walletPubKey, RGB11WalletHeadPath(walletID))
	if err != nil {
		return nil, 0, err
	}
	return p.SubscribeKey(key)
}

func (p *SatsNetDKVSClient) PutRGB11WalletSnapshot(wallet common.Wallet, walletID string,
	operationID [32]byte, value []byte, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	return p.putRGB11WalletSnapshot(wallet, walletID, operationID, value, opts, nil)
}

func (p *SatsNetDKVSClient) PutRGB11WalletSnapshotWithAutopay(wallet common.Wallet, walletID string,
	operationID [32]byte, value []byte, opts dkvsindexer.RecordOptions,
	autopay DKVSAutopayOptions) (*swire.DKVSRecord, error) {
	return p.putRGB11WalletSnapshot(wallet, walletID, operationID, value, opts, &autopay)
}

func (p *SatsNetDKVSClient) putRGB11WalletSnapshot(wallet common.Wallet, walletID string,
	operationID [32]byte, value []byte, opts dkvsindexer.RecordOptions,
	autopay *DKVSAutopayOptions) (*swire.DKVSRecord, error) {
	if len(value) == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	opts.Seq = 1
	var manifest *swire.DKVSRecord
	var err error
	if autopay == nil {
		manifest, _, err = p.PutBlob(wallet, hex.EncodeToString(operationID[:]), value, nil, opts)
	} else {
		manifest, _, err = p.PutBlobWithAutopay(wallet, hex.EncodeToString(operationID[:]), value, nil, opts, *autopay)
	}
	return manifest, err
}

func (p *SatsNetDKVSClient) GetRGB11WalletSnapshot(walletPubKey []byte, walletID string,
	operationID [32]byte, verifyOpts dkvsindexer.RecordVerificationOptions) ([]byte, *swire.DKVSRecord, error) {
	accountID := dkvsindexer.AccountID(walletPubKey)
	objectID := hex.EncodeToString(operationID[:])
	key, err := dkvsindexer.BlobManifestKey(accountID, objectID)
	if err != nil {
		return nil, nil, err
	}
	verifyOpts.ExpectedKey = key
	record, err := p.GetVerifiedRecord(key, verifyOpts)
	if err != nil {
		return nil, nil, err
	}
	if err := verifyRGB11DKVSAccountOwner(record, walletPubKey); err != nil {
		return nil, nil, err
	}
	_, value, err := p.GetBlob(accountID, objectID, dkvsindexer.BlobPolicy{})
	if err != nil {
		return nil, nil, err
	}
	return value, record, nil
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
	if pending.State.TransportMode == "out-of-band" {
		return nil, fmt.Errorf("RGB11 transfer uses out-of-band delivery")
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

func (p *rgb11Manager) RelayRGB11Transfer(client *SatsNetDKVSClient, transferID, sourcePeerID string,
	opts dkvsindexer.RecordOptions) (*corerelay.RelayRecord, *swire.DKVSRecord, error) {
	if client == nil || opts.TTL == 0 {
		return nil, nil, fmt.Errorf("RGB11 relay client and TTL are required")
	}
	record, err := p.BuildRGB11RelayRecord(transferID, sourcePeerID)
	if err != nil {
		return nil, nil, err
	}
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return nil, nil, err
	}
	written, err := client.PutRGB11RelayRecord(p.wallet, pending.State.RelayRecordKey, record, opts)
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
	return record, written, nil
}

func (p *rgb11Manager) PublishRGB11RelayRecord(transferID, sourcePeerID string,
	opts dkvsindexer.RecordOptions) (*corerelay.RelayRecord, *swire.DKVSRecord, error) {
	if err := p.requireLatestRGB11WalletState(); err != nil {
		return nil, nil, err
	}
	client, err := p.rgb11DKVSClient()
	if err != nil {
		return nil, nil, err
	}
	return p.RelayRGB11Transfer(client, transferID, sourcePeerID, opts)
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
	receipt, err := p.acceptRGB11Consignment(ctx, requestID, raw, false)
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
	}
	if err := p.rgbManager.projectionStore.SaveTransferState(state); err != nil {
		return err
	}
	p.autoBackupRGB11AfterMutation()
	return nil
}

func (p *rgb11Manager) RelayRGB11Ack(client *SatsNetDKVSClient, key string, ack *corerelay.AckRecord,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	if p == nil || p.wallet == nil || client == nil || opts.TTL == 0 {
		return nil, ErrRGB11Inconsistent
	}
	return client.PutRGB11AckRecord(p.wallet, key, ack, opts)
}

func (p *rgb11Manager) PublishRGB11AckRecord(key string, ack *corerelay.AckRecord,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	if err := p.requireLatestRGB11WalletState(); err != nil {
		return nil, err
	}
	client, err := p.rgb11DKVSClient()
	if err != nil {
		return nil, err
	}
	return p.RelayRGB11Ack(client, key, ack, opts)
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
	recipientPubKey, err := hex.DecodeString(pending.State.RecipientID)
	if err != nil {
		return nil, nil, ErrRGB11AckRequired
	}
	client, err := p.rgb11DKVSClient()
	if err != nil {
		return nil, nil, err
	}
	return client.GetRGB11AckRecord(pending.State.AckRecordKey, recipientPubKey, verifyOpts)
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
		if item.State.BatchID != pending.State.BatchID || item.State.WitnessTxID != pending.State.WitnessTxID ||
			item.State.ConsignmentHash != pending.State.ConsignmentHash {
			return ErrRGB11BatchAckRequired
		}
		pendingList = append(pendingList, item)
	}
	return p.cancelRGB11PendingBatch(pendingList, nack.ReasonCode, nil)
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

func (p *SatsNetDKVSClient) PutRGB11RelayRecord(wallet common.Wallet, key string, value *corerelay.RelayRecord, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	if err := corerelay.ValidateTemporaryKey(key); err != nil {
		return nil, err
	}
	pubKey := rgb11wallet.WalletPubKey(wallet)
	if err := value.Verify(pubKey, time.Now().Unix(), rgb11wallet.VerifyWalletSignature); err != nil {
		return nil, err
	}
	if opts.TTL == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	encoded, err := value.MarshalBinary()
	if err != nil {
		return nil, err
	}
	record, err := NewDKVSSignedRecord(wallet, key, encoded, opts)
	if err != nil {
		return nil, err
	}
	return p.PutRecord(record)
}

func (p *SatsNetDKVSClient) GetRGB11RelayRecord(key string, expectedSenderPubKey []byte, verifyOpts dkvsindexer.RecordVerificationOptions) (*corerelay.RelayRecord, *swire.DKVSRecord, error) {
	if err := corerelay.ValidateTemporaryKey(key); err != nil {
		return nil, nil, err
	}
	verifyOpts.ExpectedKey = key
	record, err := p.GetVerifiedRecord(key, verifyOpts)
	if err != nil {
		return nil, nil, err
	}
	if !bytes.Equal(record.PubKey, expectedSenderPubKey) {
		return nil, nil, dkvsindexer.ErrPermissionDenied
	}
	value, err := corerelay.UnmarshalRelayRecord(record.Value)
	if err != nil {
		return nil, nil, err
	}
	if err := value.Verify(expectedSenderPubKey, time.Now().Unix(), rgb11wallet.VerifyWalletSignature); err != nil {
		return nil, nil, err
	}
	return value, record, nil
}

func (p *SatsNetDKVSClient) PutRGB11AckRecord(wallet common.Wallet, key string, value *corerelay.AckRecord, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	if err := corerelay.ValidateTemporaryKey(key); err != nil {
		return nil, err
	}
	pubKey := rgb11wallet.WalletPubKey(wallet)
	if err := value.Verify(pubKey, rgb11wallet.VerifyWalletSignature); err != nil {
		return nil, err
	}
	if opts.TTL == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	encoded, err := value.MarshalBinary()
	if err != nil {
		return nil, err
	}
	record, err := NewDKVSSignedRecord(wallet, key, encoded, opts)
	if err != nil {
		return nil, err
	}
	return p.PutRecord(record)
}

func (p *SatsNetDKVSClient) GetRGB11AckRecord(key string, expectedRecipientPubKey []byte, verifyOpts dkvsindexer.RecordVerificationOptions) (*corerelay.AckRecord, *swire.DKVSRecord, error) {
	if err := corerelay.ValidateTemporaryKey(key); err != nil {
		return nil, nil, err
	}
	verifyOpts.ExpectedKey = key
	record, err := p.GetVerifiedRecord(key, verifyOpts)
	if err != nil {
		return nil, nil, err
	}
	if !bytes.Equal(record.PubKey, expectedRecipientPubKey) {
		return nil, nil, dkvsindexer.ErrPermissionDenied
	}
	value, err := corerelay.UnmarshalAckRecord(record.Value)
	if err != nil {
		return nil, nil, err
	}
	if err := value.Verify(expectedRecipientPubKey, rgb11wallet.VerifyWalletSignature); err != nil {
		return nil, nil, err
	}
	return value, record, nil
}

func (p *SatsNetDKVSClient) SubscribeRGB11Transfer(relayKey, ackKey string) ([]*swire.DKVSRecord, error) {
	if relayKey == ackKey {
		return nil, corerelay.ErrInvalidKey
	}
	if err := corerelay.ValidateTemporaryKey(relayKey); err != nil {
		return nil, err
	}
	if err := corerelay.ValidateTemporaryKey(ackKey); err != nil {
		return nil, err
	}
	relayRecords, _, err := p.SubscribeKey(relayKey)
	if err != nil {
		return nil, err
	}
	ackRecords, _, err := p.SubscribeKey(ackKey)
	if err != nil {
		return nil, err
	}
	return append(relayRecords, ackRecords...), nil
}
