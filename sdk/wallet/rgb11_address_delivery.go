package wallet

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const (
	rgb11AddressEnvelopeVersion = uint8(1)
	rgb11AddressEnvelopeInline  = uint8(1)
	rgb11AddressEnvelopeBlob    = uint8(2)

	RGB11AddressACKAccepted   = uint8(1)
	RGB11AddressACKNeedResend = uint8(2)
	RGB11AddressACKRejected   = uint8(3)

	rgb11AddressInlineLimit = 10 * 1024
)

var (
	ErrRGB11AddressDeliveryRequired = errors.New("RGB11 address consignment must be delivered before broadcast")
	ErrRGB11AddressTxNotSeen        = errors.New("RGB11 address witness transaction is not visible yet")
	ErrRGB11AddressMailbox          = errors.New("invalid RGB11 address mailbox message")
)

type RGB11AddressDeliveryOptions struct {
	RecordOptions dkvsindexer.RecordOptions `json:"record_options"`
	Autopay       *DKVSAutopayOptions       `json:"autopay,omitempty"`
	InlineLimit   int                       `json:"inline_limit,omitempty"`
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

type rgb11AccountPayloadCryptor interface {
	EncryptToAccount(accountID string, plaintext []byte) ([]byte, error)
	DecryptFromAccount(accountID string, ciphertext []byte) ([]byte, error)
}

func encodeRGB11AddressEnvelope(mode uint8, ciphertext []byte) ([]byte, error) {
	if mode != rgb11AddressEnvelopeInline && mode != rgb11AddressEnvelopeBlob {
		return nil, ErrRGB11AddressMailbox
	}
	if mode == rgb11AddressEnvelopeInline && len(ciphertext) == 0 {
		return nil, ErrRGB11AddressMailbox
	}
	value := []byte{rgb11AddressEnvelopeVersion, mode}
	if mode == rgb11AddressEnvelopeInline {
		value = append(value, ciphertext...)
	}
	return value, nil
}

func decodeRGB11AddressEnvelope(value []byte) (uint8, []byte, error) {
	if len(value) < 2 || value[0] != rgb11AddressEnvelopeVersion {
		return 0, nil, ErrRGB11AddressMailbox
	}
	mode := value[1]
	switch mode {
	case rgb11AddressEnvelopeInline:
		if len(value) == 2 {
			return 0, nil, ErrRGB11AddressMailbox
		}
		return mode, append([]byte(nil), value[2:]...), nil
	case rgb11AddressEnvelopeBlob:
		if len(value) != 2 {
			return 0, nil, ErrRGB11AddressMailbox
		}
		return mode, nil, nil
	default:
		return 0, nil, ErrRGB11AddressMailbox
	}
}

func encodeRGB11AddressACK(ack RGB11AddressACK) ([]byte, error) {
	if ack.Status != RGB11AddressACKAccepted && ack.Status != RGB11AddressACKNeedResend &&
		ack.Status != RGB11AddressACKRejected {
		return nil, ErrRGB11AddressMailbox
	}
	value := make([]byte, 4)
	value[0] = rgb11AddressEnvelopeVersion
	value[1] = ack.Status
	binary.BigEndian.PutUint16(value[2:], ack.Code)
	return value, nil
}

func decodeRGB11AddressACK(value []byte) (RGB11AddressACK, error) {
	if len(value) != 4 || value[0] != rgb11AddressEnvelopeVersion {
		return RGB11AddressACK{}, ErrRGB11AddressMailbox
	}
	ack := RGB11AddressACK{Status: value[1], Code: binary.BigEndian.Uint16(value[2:])}
	if _, err := encodeRGB11AddressACK(ack); err != nil {
		return RGB11AddressACK{}, err
	}
	return ack, nil
}

func normalizeRGB11AddressRecordOptions(opts dkvsindexer.RecordOptions) dkvsindexer.RecordOptions {
	if opts.Seq == 0 {
		opts.Seq = 1
	}
	if opts.IssueTime == 0 {
		opts.IssueTime = uint64(time.Now().UnixMilli())
	}
	return opts
}

// DeliverRGB11AddressTransfer encrypts the recipient Consignment and writes it
// before any Bitcoin broadcast. Small ciphertexts are carried directly by the
// mailbox record; larger ones use the native sender-owned DKVS blob and a
// two-byte mailbox locator.
func (p *Manager) DeliverRGB11AddressTransfer(client *SatsNetDKVSClient, transferID string,
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
	opts := normalizeRGB11AddressRecordOptions(options.RecordOptions)
	inlineLimit := options.InlineLimit
	if inlineLimit <= 0 || inlineLimit > rgb11AddressInlineLimit {
		inlineLimit = rgb11AddressInlineLimit
	}
	mode := rgb11AddressEnvelopeInline
	objectID := ""
	if len(ciphertext)+2 > inlineLimit {
		mode = rgb11AddressEnvelopeBlob
		objectID = transferID
		if _, _, err := client.PutAccountBlob(
			p.wallet, objectID, ciphertext, nil, opts, options.Autopay,
		); err != nil {
			return nil, err
		}
	}
	mailValue, err := encodeRGB11AddressEnvelope(mode, ciphertext)
	if err != nil {
		return nil, err
	}
	if mode == rgb11AddressEnvelopeBlob {
		mailValue, _ = encodeRGB11AddressEnvelope(mode, nil)
	}
	record, err := client.SendAccountMailboxMessage(
		p.wallet, pending.State.ReceiverAccountID, transferID, mailValue, opts, options.Autopay,
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
func (p *Manager) BroadcastRGB11AddressTransfer(transferID string) (string, error) {
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

func (p *Manager) DeliverAndBroadcastRGB11AddressTransfer(client *SatsNetDKVSClient, transferID string,
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

func parseRGB11AddressMailboxKey(record *swire.DKVSRecord) (receiverID, senderID, transferID string, err error) {
	if record == nil {
		return "", "", "", ErrRGB11AddressMailbox
	}
	parsed, err := dkvsindexer.ParseKey(record.Key)
	if err != nil || parsed.Namespace != "mail" || len(parsed.Segments) != 4 ||
		parsed.Segments[1] != "msg" {
		return "", "", "", ErrRGB11AddressMailbox
	}
	receiverID, senderID, transferID = parsed.Segments[0], parsed.Segments[2], parsed.Segments[3]
	if len(transferID) != 64 {
		return "", "", "", ErrRGB11AddressMailbox
	}
	if decoded, decodeErr := hex.DecodeString(transferID); decodeErr != nil || len(decoded) != 32 {
		return "", "", "", ErrRGB11AddressMailbox
	}
	return receiverID, senderID, transferID, nil
}

func (p *Manager) readRGB11AddressConsignment(client *SatsNetDKVSClient, record *swire.DKVSRecord,
	senderID, transferID string, verify dkvsindexer.RecordVerificationOptions) ([]byte, string, error) {
	mode, encrypted, err := decodeRGB11AddressEnvelope(record.Value)
	if err != nil {
		return nil, "", err
	}
	modeName := "inline"
	if mode == rgb11AddressEnvelopeBlob {
		modeName = "blob"
		_, encrypted, err = client.GetAccountBlob(senderID, transferID, dkvsindexer.DefaultBlobPolicy(), verify)
		if err != nil {
			return nil, "", err
		}
	}
	cryptor, ok := p.wallet.(rgb11AccountPayloadCryptor)
	if !ok {
		return nil, "", fmt.Errorf("active wallet does not support RGB11 account decryption")
	}
	plain, err := cryptor.DecryptFromAccount(senderID, encrypted)
	return plain, modeName, err
}

func (p *Manager) findRGB11AddressAllocation(receipt *rgb11wallet.ValidationReceipt) (
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
func (p *Manager) AcceptRGB11AddressMailbox(ctx context.Context, client *SatsNetDKVSClient,
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
	receiverID, senderID, transferID, err := parseRGB11AddressMailboxKey(record)
	if err != nil {
		return nil, nil, err
	}
	localID, err := dkvsAccountID(p.wallet)
	if err != nil || receiverID != localID {
		return nil, nil, ErrRGB11AddressMailbox
	}
	raw, mode, err := p.readRGB11AddressConsignment(client, record, senderID, transferID, verify)
	if err != nil {
		return nil, nil, err
	}
	receipt, err := p.ValidateRGB11Consignment(ctx, raw)
	if err != nil {
		return nil, nil, err
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
	ackRecord, err := p.SendRGB11AddressACK(client, senderID, transferID,
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

func (p *Manager) SendRGB11AddressACK(client *SatsNetDKVSClient, senderAccountID, transferID string,
	ack RGB11AddressACK, options RGB11AddressDeliveryOptions) (*swire.DKVSRecord, error) {
	if p == nil || client == nil || senderAccountID == "" || transferID == "" {
		return nil, ErrRGB11AddressMailbox
	}
	value, err := encodeRGB11AddressACK(ack)
	if err != nil {
		return nil, err
	}
	opts := normalizeRGB11AddressRecordOptions(options.RecordOptions)
	return client.SendAccountMailboxMessage(p.wallet, senderAccountID, transferID, value, opts, options.Autopay)
}

// AcceptRGB11AddressACK records receiver persistence. Delivery cache is only
// compacted once the witness transaction is also confirmed.
func (p *Manager) AcceptRGB11AddressACK(record *swire.DKVSRecord,
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
	senderID, receiverID, transferID, err := parseRGB11AddressMailboxKey(record)
	if err != nil {
		return nil, err
	}
	localID, err := dkvsAccountID(p.wallet)
	if err != nil || senderID != localID {
		return nil, ErrRGB11AddressMailbox
	}
	ack, err := decodeRGB11AddressACK(record.Value)
	if err != nil {
		return nil, err
	}
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		return nil, err
	}
	if !pending.State.AddressMode || pending.State.ReceiverAccountID != receiverID ||
		pending.State.SenderAccountID != senderID {
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

func (p *Manager) compactRGB11AddressDeliveryIfFinal(pending *rgb11wallet.PendingTransfer) error {
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
func (p *Manager) RefreshRGB11AddressACK(record *swire.DKVSRecord,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11AddressACK, error) {
	return p.AcceptRGB11AddressACK(record, verify)
}

// RGB11AddressCarrierWarning is exposed to UI layers whenever an address owns
// RGB allocations. Spending the carrier with a wallet that does not understand
// RGB may permanently destroy the asset state.
const RGB11AddressCarrierWarning = "This address contains RGB11 assets. Spending its UTXO with a non-RGB-aware wallet may permanently destroy those assets."

func stringEqual(a, b []byte) bool { return string(a) == string(b) }

var _ = indexer.AssetName{}
