package wallet

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const (
	AccountMessagePayloadVersion       = uint32(1)
	AccountMessageKindGeneric          = "GENERIC"
	AccountMessageKindOffline          = "OFFLINE"
	AccountMessageKindRGB11Consignment = "RGB11_CONSIGNMENT"
	AccountMessageKindRGB11ACK         = "RGB11_ACK"
	AccountMessageKindManagedActive    = "ACCOUNT_MANAGED_ACTIVE"

	accountMessageOutboxPrefix  = "message-outbox-v1/"
	accountMessageOutboxVersion = uint32(2)
)

var ErrMessageServiceUnavailable = errors.New("CoreNode message service is unavailable")

func signMessageServiceQuery(wallet common.Wallet, request *swire.MessageServiceRequest) error {
	if wallet == nil || request == nil {
		return ErrMessageServiceUnavailable
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	request.Auth = &swire.MessageServiceAuth{
		Nonce: hex.EncodeToString(nonce[:]), ExpiresAtMS: uint64(time.Now().Add(time.Minute).UnixMilli()),
	}
	hash, err := swire.MessageServiceQuerySigningHash(request)
	if err != nil {
		return err
	}
	signer, ok := wallet.(dkvsAccountSchnorrSigner)
	if !ok {
		return fmt.Errorf("account wallet cannot authorize message query")
	}
	request.Auth.Signature, err = signer.SignSchnorrMessage(hash[:])
	return err
}

type MessageServiceRateLimitError struct {
	RetryAfter time.Duration
}

func (e *MessageServiceRateLimitError) Error() string {
	if e == nil || e.RetryAfter <= 0 {
		return "CoreNode temporarily rejected Direct messages"
	}
	return fmt.Sprintf("CoreNode temporarily rejected Direct messages; retry after %s", e.RetryAfter)
}

// newAccountMessageID creates the stable identity signed into a message. The
// first eight bytes are Unix microseconds for time ordering and the remaining
// eight bytes are cryptographic entropy so concurrent senders cannot collide.
func newAccountMessageID() (string, error) {
	var raw [swire.MessageIDHexSize / 2]byte
	now := time.Now().UnixMicro()
	if now <= 0 {
		return "", fmt.Errorf("invalid message generation time")
	}
	binary.BigEndian.PutUint64(raw[:8], uint64(now))
	if _, err := rand.Read(raw[8:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

type AccountMessagePayload struct {
	Version       uint32 `json:"version"`
	Kind          string `json:"kind"`
	ApplicationID string `json:"application_id"`
	Body          []byte `json:"body"`
}

type AccountDirectMessage struct {
	Payload *AccountMessagePayload
	Direct  *swire.DirectMessage
	Record  *swire.DKVSRecord
}

type accountMessageOutboxRecord struct {
	Version        uint32
	ApplicationID  string
	SourceCoreNode string
	Recipient      string
	Kind           string
	Direct         swire.DirectMessage
}

func validMessageApplicationID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') &&
			r != '.' && r != '_' && r != ':' && r != '-' {
			return false
		}
	}
	return true
}

func accountMessageOutboxKey(applicationID string) []byte {
	return []byte(GetDBKeyPrefix() + accountMessageOutboxPrefix + applicationID)
}

func encodeAccountMessagePayload(kind, applicationID string, body []byte) ([]byte, error) {
	kind = strings.TrimSpace(kind)
	if kind == "" || !validMessageApplicationID(applicationID) || len(body) == 0 {
		return nil, fmt.Errorf("invalid account message payload")
	}
	return json.Marshal(AccountMessagePayload{Version: AccountMessagePayloadVersion, Kind: kind, ApplicationID: applicationID, Body: append([]byte(nil), body...)})
}

func decodeAccountMessagePayload(encoded []byte) (*AccountMessagePayload, error) {
	var payload AccountMessagePayload
	if len(encoded) == 0 || json.Unmarshal(encoded, &payload) != nil || payload.Version != AccountMessagePayloadVersion ||
		strings.TrimSpace(payload.Kind) == "" || !validMessageApplicationID(payload.ApplicationID) || len(payload.Body) == 0 {
		return nil, fmt.Errorf("invalid account message payload")
	}
	payload.Body = append([]byte(nil), payload.Body...)
	return &payload, nil
}

func (p *Manager) messageServiceClient() (MessageServiceRPCClient, string, error) {
	if p == nil || p.serverNode == nil || p.serverNode.NodeId == nil || p.serverNode.client == nil {
		return nil, "", ErrMessageServiceUnavailable
	}
	coreID := hex.EncodeToString(p.serverNode.NodeId.SerializeCompressed())
	if p.ServerIsBootstrapNode() {
		return nil, "", fmt.Errorf("%w: bootstrap node is not an account service CoreNode", ErrMessageServiceUnavailable)
	}
	client, ok := p.serverNode.client.(MessageServiceRPCClient)
	if !ok {
		return nil, "", ErrMessageServiceUnavailable
	}
	return client, coreID, nil
}

// bindAccountToCurrentCoreNode creates or reuses the root account's one free
// mapping/binding KV. The connected CoreNode is the only party allowed to
// accept that globally visible record as a local service binding.
func (p *Manager) bindAccountToCurrentCoreNode(root common.Wallet) error {
	if p == nil || root == nil {
		return ErrMessageServiceUnavailable
	}
	client, coreID, err := p.messageServiceClient()
	if err != nil {
		return err
	}
	accountID, err := dkvsAccountID(root)
	if err != nil {
		return err
	}
	address := root.GetAddress()
	key, err := dkvsindexer.AccountMappingKey(GetChainParam().Name, address)
	if err != nil {
		return err
	}
	initialValue, err := dkvsindexer.EncodeAccountServiceDescriptor(dkvsindexer.AccountServiceDescriptor{
		AccountID: accountID, CoreNodeID: coreID,
		Capabilities: dkvsindexer.AccountServiceCapabilityRGB11Direct,
	})
	if err != nil {
		return err
	}
	store, err := p.accountDKVSStore()
	if err != nil {
		return err
	}
	prefix, _, err := dkvsManagedPathForKey(key)
	if err != nil {
		return err
	}
	if err := p.SubscribeDKVSPrefix(prefix); err != nil {
		return err
	}
	values, err := store.Update([]string{key},
		func(current map[string]*dkvsValue, _ map[string]uint64) ([]dkvsValueMutation, error) {
			value := initialValue
			if existing := current[key]; existing != nil && existing.record != nil {
				_, _, descriptor, verifyErr := dkvsindexer.ValidateAccountMappingBindingRecord(existing.record)
				if verifyErr != nil || descriptor.AccountID != accountID {
					return nil, dkvsindexer.ErrInvalidRecord
				}
				descriptor.CoreNodeID = coreID
				descriptor.Capabilities |= dkvsindexer.AccountServiceCapabilityRGB11Direct
				value, verifyErr = dkvsindexer.EncodeAccountServiceDescriptor(*descriptor)
				if verifyErr != nil {
					return nil, verifyErr
				}
				if bytes.Equal(existing.Value, value) {
					return nil, nil
				}
			}
			return []dkvsValueMutation{{
				Key: key, Value: value, Owner: root,
				Signature: dkvsSignatureAccount,
			}}, nil
		})
	if err != nil {
		return err
	}
	if len(values) != 1 || values[0] == nil || values[0].record == nil {
		return dkvsindexer.ErrInvalidRecord
	}
	_, err = client.SendMessageServiceReq(&swire.MessageServiceRequest{
		Action: swire.MessageServiceActionBindAccount, Record: values[0].record,
	})
	return err
}

func (p *Manager) BindAccountToCurrentCoreNode() error {
	root, err := p.accountManagementRootWallet()
	if err != nil {
		return err
	}
	return p.bindAccountToCurrentCoreNode(root)
}

func (p *Manager) saveMessageOutbox(value *accountMessageOutboxRecord) error {
	if p == nil || p.db == nil || value == nil || !validMessageApplicationID(value.ApplicationID) {
		return ErrMessageServiceUnavailable
	}
	encoded, err := EncodeToBytes(value)
	if err != nil {
		return err
	}
	return p.db.Write(accountMessageOutboxKey(value.ApplicationID), encoded)
}
func (p *Manager) loadMessageOutbox(applicationID string) (*accountMessageOutboxRecord, error) {
	if p == nil || p.db == nil || !validMessageApplicationID(applicationID) {
		return nil, ErrMessageServiceUnavailable
	}
	encoded, err := p.db.Read(accountMessageOutboxKey(applicationID))
	if errors.Is(err, indexer.ErrKeyNotFound) || err == nil && len(encoded) == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var value accountMessageOutboxRecord
	if err := DecodeFromBytes(encoded, &value); err != nil {
		return nil, err
	}
	if value.Version != accountMessageOutboxVersion || value.ApplicationID != applicationID || strings.TrimSpace(value.SourceCoreNode) == "" {
		return nil, fmt.Errorf("invalid message outbox")
	}
	return &value, nil
}
func (p *Manager) deleteMessageOutbox(applicationID string) error {
	if p == nil || p.db == nil {
		return nil
	}
	return p.db.Delete(accountMessageOutboxKey(applicationID))
}

func (p *Manager) sendPersistedAccountMessage(outbox *accountMessageOutboxRecord) (*swire.DirectMessage, error) {
	client, coreID, err := p.messageServiceClient()
	if err != nil {
		return nil, err
	}
	if outbox == nil || outbox.SourceCoreNode != coreID {
		return nil, fmt.Errorf("message outbox belongs to a different CoreNode")
	}
	message := outbox.Direct
	_, err = client.SendMessageServiceReq(&swire.MessageServiceRequest{Action: swire.MessageServiceActionSendDirect, Direct: &message})
	if err != nil {
		return &message, err
	}
	if err := p.deleteMessageOutbox(outbox.ApplicationID); err != nil {
		// CoreNode acceptance is already durable. A stale local outbox is safe:
		// retry replays the exact same signed MessageID and is idempotent server-side.
		Log.Warnf("delete accepted message outbox %s failed: %v", outbox.ApplicationID, err)
	}
	return &message, nil
}

// SendAccountDirectMessage encrypts a versioned application payload, obtains
// the next sender sequence from the bound CoreNode, persists the exact signed
// Direct message locally, then submits it. Retrying the same application ID
// always resends the same signed MessageID until CoreNode acceptance succeeds.
func (p *Manager) SendAccountDirectMessage(applicationID, kind, recipientAccount string, body []byte) (*swire.DirectMessage, error) {
	if !validMessageApplicationID(applicationID) || strings.TrimSpace(kind) == "" || len(body) == 0 {
		return nil, fmt.Errorf("invalid account message")
	}
	p.messageSendMu.Lock()
	defer p.messageSendMu.Unlock()
	existing, err := p.loadMessageOutbox(applicationID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.Recipient != recipientAccount || existing.Kind != kind {
			return nil, fmt.Errorf("message application id already used")
		}
		return p.sendPersistedAccountMessage(existing)
	}
	root, err := p.accountManagementRootWallet()
	if err != nil {
		return nil, err
	}
	if err := p.bindAccountToCurrentCoreNode(root); err != nil {
		return nil, fmt.Errorf("bind sender account to current CoreNode: %w", err)
	}
	internal, ok := root.(*InternalWallet)
	if !ok {
		return nil, fmt.Errorf("account message encryption requires internal wallet")
	}
	accountID, err := dkvsAccountID(root)
	if err != nil {
		return nil, err
	}
	payload, err := encodeAccountMessagePayload(kind, applicationID, body)
	if err != nil {
		return nil, err
	}
	ciphertext, err := internal.EncryptToAccount(recipientAccount, payload)
	if err != nil {
		return nil, err
	}
	client, coreID, err := p.messageServiceClient()
	if err != nil {
		return nil, err
	}
	nextRequest := &swire.MessageServiceRequest{Action: swire.MessageServiceActionNextMessage, AccountID: accountID}
	if err := signMessageServiceQuery(root, nextRequest); err != nil {
		return nil, err
	}
	next, err := client.SendMessageServiceReq(nextRequest)
	if err != nil {
		return nil, err
	}
	messageID, err := newAccountMessageID()
	if err != nil {
		return nil, err
	}
	message := swire.DirectMessage{SenderAccount: accountID, SenderMsgID: next.NextSenderMsgID, MessageID: messageID, RecipientAccount: recipientAccount, Ciphertext: ciphertext}
	hash, err := swire.DirectMessageSigningHash(&message)
	if err != nil {
		return nil, err
	}
	signer, ok := root.(dkvsAccountSchnorrSigner)
	if !ok {
		return nil, fmt.Errorf("account wallet cannot sign message")
	}
	sig, err := signer.SignSchnorrMessage(hash[:])
	if err != nil {
		return nil, err
	}
	message.SenderSignature = sig
	outbox := &accountMessageOutboxRecord{Version: accountMessageOutboxVersion, ApplicationID: applicationID, SourceCoreNode: coreID, Recipient: recipientAccount, Kind: kind, Direct: message}
	if err := p.saveMessageOutbox(outbox); err != nil {
		return nil, err
	}
	return p.sendPersistedAccountMessage(outbox)
}

func (p *Manager) RetryAccountDirectMessage(applicationID string) (*swire.DirectMessage, error) {
	p.messageSendMu.Lock()
	defer p.messageSendMu.Unlock()
	outbox, err := p.loadMessageOutbox(applicationID)
	if err != nil || outbox == nil {
		return nil, err
	}
	return p.sendPersistedAccountMessage(outbox)
}

func verifyAccountDirectRecord(localAccount string, record *swire.DKVSRecord) (*swire.DirectMessage, error) {
	if record == nil || record.Version != dkvsindexer.Version || record.Seq != 1 || len(record.Value) == 0 ||
		len(record.PubKey) != 0 || len(record.Signature) != 0 || record.Flags != 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if record.TTL == 0 {
		if len(record.FeeProof) != 0 {
			return nil, dkvsindexer.ErrInvalidRecord
		}
	} else {
		proof, err := dkvsindexer.ParseFeeProof(record.FeeProof)
		if err != nil || proof.Mode != dkvsindexer.FeeModeFreeLocal {
			return nil, dkvsindexer.ErrInvalidRecord
		}
	}
	parsed, err := dkvsindexer.ParseKey(record.Key)
	if err != nil || parsed.Namespace != "mail" || len(parsed.Segments) != 4 || parsed.Segments[0] != localAccount || parsed.Segments[1] != "msg" {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	message, err := swire.DeserializeDirectMessage(record.Value)
	if err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if message.RecipientAccount != localAccount || message.SenderAccount != parsed.Segments[2] || message.MessageID != parsed.Segments[3] {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	hash, err := swire.DirectMessageSigningHash(message)
	if err != nil {
		return nil, err
	}
	pubBytes, err := dkvsindexer.AccountPubKey(message.SenderAccount)
	if err != nil {
		return nil, err
	}
	pub, err := btcec.ParsePubKey(pubBytes)
	if err != nil {
		return nil, dkvsindexer.ErrInvalidSignature
	}
	sig, err := schnorr.ParseSignature(message.SenderSignature)
	if err != nil || !sig.Verify(hash[:], pub) {
		return nil, dkvsindexer.ErrInvalidSignature
	}
	return message, nil
}

func (p *Manager) ReadAccountDirectMessages(start, limit int) ([]*AccountDirectMessage, int, error) {
	root, err := p.accountManagementRootWallet()
	if err != nil {
		return nil, 0, err
	}
	internal, ok := root.(*InternalWallet)
	if !ok {
		return nil, 0, fmt.Errorf("account message decryption requires internal wallet")
	}
	accountID, err := dkvsAccountID(root)
	if err != nil {
		return nil, 0, err
	}
	store, err := p.accountDKVSStore()
	if err != nil {
		return nil, 0, err
	}
	records, total, err := store.client.ReadMailboxMessages(accountID, start, limit)
	if err != nil {
		return nil, 0, err
	}
	result := make([]*AccountDirectMessage, 0, len(records))
	for _, record := range records {
		message, err := verifyAccountDirectRecord(accountID, record)
		if err != nil {
			return nil, 0, err
		}
		plain, err := internal.DecryptFromAccount(message.SenderAccount, message.Ciphertext)
		if err != nil {
			return nil, 0, err
		}
		payload, err := decodeAccountMessagePayload(plain)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, &AccountDirectMessage{Payload: payload, Direct: message, Record: record})
	}
	return result, total, nil
}

func accountMessageOutboxKeyForSender(senderAccount, applicationID string) []byte {
	return []byte(GetDBKeyPrefix() + accountMessageOutboxPrefix + senderAccount + "/" + applicationID)
}

func (p *Manager) loadWalletMessageOutbox(senderAccount, applicationID string) (*accountMessageOutboxRecord, error) {
	if p == nil || p.db == nil || !validMessageApplicationID(applicationID) || len(senderAccount) != 64 {
		return nil, ErrMessageServiceUnavailable
	}
	encoded, err := p.db.Read(accountMessageOutboxKeyForSender(senderAccount, applicationID))
	if errors.Is(err, indexer.ErrKeyNotFound) || err == nil && len(encoded) == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var value accountMessageOutboxRecord
	if err := DecodeFromBytes(encoded, &value); err != nil {
		return nil, err
	}
	if value.Version != accountMessageOutboxVersion || value.ApplicationID != applicationID || value.Direct.SenderAccount != senderAccount || strings.TrimSpace(value.SourceCoreNode) == "" {
		return nil, fmt.Errorf("invalid wallet message outbox")
	}
	return &value, nil
}

func (p *Manager) saveWalletMessageOutbox(senderAccount string, value *accountMessageOutboxRecord) error {
	if p == nil || p.db == nil || value == nil || !validMessageApplicationID(value.ApplicationID) || len(senderAccount) != 64 {
		return ErrMessageServiceUnavailable
	}
	encoded, err := EncodeToBytes(value)
	if err != nil {
		return err
	}
	return p.db.Write(accountMessageOutboxKeyForSender(senderAccount, value.ApplicationID), encoded)
}

func (p *Manager) deleteWalletMessageOutbox(senderAccount, applicationID string) error {
	if p == nil || p.db == nil {
		return nil
	}
	return p.db.Delete(accountMessageOutboxKeyForSender(senderAccount, applicationID))
}

func (p *Manager) sendPersistedWalletMessage(senderAccount string, outbox *accountMessageOutboxRecord) (*swire.DirectMessage, error) {
	client, coreID, err := p.messageServiceClient()
	if err != nil {
		return nil, err
	}
	if outbox == nil || outbox.SourceCoreNode != coreID {
		return nil, fmt.Errorf("message outbox belongs to a different CoreNode")
	}
	message := outbox.Direct
	_, err = client.SendMessageServiceReq(&swire.MessageServiceRequest{Action: swire.MessageServiceActionSendDirect, Direct: &message})
	if err != nil {
		return &message, err
	}
	if err := p.deleteWalletMessageOutbox(senderAccount, outbox.ApplicationID); err != nil {
		Log.Warnf("delete accepted wallet message outbox %s/%s failed: %v", senderAccount, outbox.ApplicationID, err)
	}
	return &message, nil
}

// sendWalletDirectMessage is the common transport for account-management,
// RGB11 and future wallet protocols. The sender wallet identity is also the
// mailbox AccountID and must be bound to the current CoreNode.
func (p *Manager) sendWalletDirectMessage(sender common.Wallet, applicationID, kind, recipientAccount string, body []byte) (*swire.DirectMessage, error) {
	if p == nil || sender == nil || !validMessageApplicationID(applicationID) || strings.TrimSpace(kind) == "" || len(body) == 0 {
		return nil, fmt.Errorf("invalid wallet direct message")
	}
	p.messageSendMu.Lock()
	defer p.messageSendMu.Unlock()
	senderID, err := dkvsAccountID(sender)
	if err != nil {
		return nil, err
	}
	root, err := p.accountManagementRootWallet()
	if err != nil {
		return nil, err
	}
	rootID, err := dkvsAccountID(root)
	if err != nil {
		return nil, err
	}
	if senderID != rootID {
		return nil, dkvsindexer.ErrPermissionDenied
	}
	if err := p.bindAccountToCurrentCoreNode(sender); err != nil {
		return nil, fmt.Errorf("bind sender account to current CoreNode: %w", err)
	}
	existing, err := p.loadWalletMessageOutbox(senderID, applicationID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.Recipient != recipientAccount || existing.Kind != kind {
			return nil, fmt.Errorf("message application id already used")
		}
		return p.sendPersistedWalletMessage(senderID, existing)
	}
	internal, ok := sender.(*InternalWallet)
	if !ok {
		return nil, fmt.Errorf("account message encryption requires internal wallet")
	}
	payload, err := encodeAccountMessagePayload(kind, applicationID, body)
	if err != nil {
		return nil, err
	}
	ciphertext, err := internal.EncryptToAccount(recipientAccount, payload)
	if err != nil {
		return nil, err
	}
	client, coreID, err := p.messageServiceClient()
	if err != nil {
		return nil, err
	}
	nextRequest := &swire.MessageServiceRequest{Action: swire.MessageServiceActionNextMessage, AccountID: senderID}
	if err := signMessageServiceQuery(sender, nextRequest); err != nil {
		return nil, err
	}
	next, err := client.SendMessageServiceReq(nextRequest)
	if err != nil {
		return nil, err
	}
	messageID, err := newAccountMessageID()
	if err != nil {
		return nil, err
	}
	message := swire.DirectMessage{SenderAccount: senderID, SenderMsgID: next.NextSenderMsgID, MessageID: messageID, RecipientAccount: recipientAccount, Ciphertext: ciphertext}
	hash, err := swire.DirectMessageSigningHash(&message)
	if err != nil {
		return nil, err
	}
	signer, ok := sender.(dkvsAccountSchnorrSigner)
	if !ok {
		return nil, fmt.Errorf("account wallet cannot sign message")
	}
	message.SenderSignature, err = signer.SignSchnorrMessage(hash[:])
	if err != nil {
		return nil, err
	}
	outbox := &accountMessageOutboxRecord{Version: accountMessageOutboxVersion, ApplicationID: applicationID, SourceCoreNode: coreID, Recipient: recipientAccount, Kind: kind, Direct: message}
	if err := p.saveWalletMessageOutbox(senderID, outbox); err != nil {
		return nil, err
	}
	return p.sendPersistedWalletMessage(senderID, outbox)
}

func decodeWalletDirectRecord(wallet common.Wallet, record *swire.DKVSRecord) (*AccountDirectMessage, error) {
	if wallet == nil {
		return nil, ErrMessageServiceUnavailable
	}
	accountID, err := dkvsAccountID(wallet)
	if err != nil {
		return nil, err
	}
	message, err := verifyAccountDirectRecord(accountID, record)
	if err != nil {
		return nil, err
	}
	internal, ok := wallet.(*InternalWallet)
	if !ok {
		return nil, fmt.Errorf("account message decryption requires internal wallet")
	}
	plain, err := internal.DecryptFromAccount(message.SenderAccount, message.Ciphertext)
	if err != nil {
		return nil, err
	}
	payload, err := decodeAccountMessagePayload(plain)
	if err != nil {
		return nil, err
	}
	return &AccountDirectMessage{Payload: payload, Direct: message, Record: record}, nil
}

func (p *Manager) readWalletDirectMessages(wallet common.Wallet) ([]*AccountDirectMessage, error) {
	if p == nil || wallet == nil {
		return nil, ErrMessageServiceUnavailable
	}
	accountID, err := dkvsAccountID(wallet)
	if err != nil {
		return nil, err
	}
	store, err := p.accountDKVSStore()
	if err != nil {
		return nil, err
	}
	records, _, err := store.client.ReadMailboxMessages(accountID, 0, 2048)
	if err != nil {
		return nil, err
	}
	result := make([]*AccountDirectMessage, 0, len(records))
	for _, record := range records {
		decoded, err := decodeWalletDirectRecord(wallet, record)
		if err != nil {
			return nil, err
		}
		result = append(result, decoded)
	}
	return result, nil
}

func directMailboxRecord(message *swire.DirectMessage) (*swire.DKVSRecord, error) {
	if message == nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	encoded, err := swire.SerializeDirectMessage(message, true)
	if err != nil {
		return nil, err
	}
	key, err := dkvsindexer.MailMsgKey(message.RecipientAccount, message.SenderAccount, message.MessageID)
	if err != nil {
		return nil, err
	}
	return &swire.DKVSRecord{Version: dkvsindexer.Version, Key: key, Value: encoded, Seq: 1}, nil
}

func (p *Manager) SendOfflineMessage(senderWallet common.Wallet, recipientPubKey []byte, msgID int64,
	encryptedMessage []byte, metadata map[string]string) (*swire.DKVSRecord, error) {
	mailboxID, stableMsgID, value, err := buildOfflineMessage(senderWallet, recipientPubKey, msgID, encryptedMessage, metadata)
	if err != nil {
		return nil, err
	}
	message, err := p.sendWalletDirectMessage(senderWallet, stableMsgID, AccountMessageKindOffline, mailboxID, value)
	if err != nil {
		return nil, err
	}
	return directMailboxRecord(message)
}

func (p *Manager) ReadOfflineMessages(recipientWallet common.Wallet, start, limit int) ([]*DKVSOfflineMessage, []*swire.DKVSRecord, int, error) {
	if p == nil || recipientWallet == nil {
		return nil, nil, 0, ErrMessageServiceUnavailable
	}
	accountID, err := dkvsAccountID(recipientWallet)
	if err != nil {
		return nil, nil, 0, err
	}
	root, err := p.accountManagementRootWallet()
	if err != nil {
		return nil, nil, 0, err
	}
	rootID, err := dkvsAccountID(root)
	if err != nil {
		return nil, nil, 0, err
	}
	if accountID != rootID {
		return nil, nil, 0, dkvsindexer.ErrPermissionDenied
	}
	store, err := p.accountDKVSStore()
	if err != nil {
		return nil, nil, 0, err
	}
	records, total, err := store.client.ReadMailboxMessages(accountID, start, limit)
	if err != nil {
		return nil, nil, 0, err
	}
	messages := make([]*DKVSOfflineMessage, 0)
	matchedRecords := make([]*swire.DKVSRecord, 0)
	for _, record := range records {
		item, err := decodeWalletDirectRecord(recipientWallet, record)
		if err != nil {
			return nil, nil, 0, err
		}
		if item.Payload.Kind != AccountMessageKindOffline {
			continue
		}
		message, err := decodeDKVSOfflineMessage(item.Payload.Body)
		if err != nil || message.Version != dkvsAppValueVersion || message.ToMailboxID != accountID {
			return nil, nil, 0, dkvsindexer.ErrInvalidRecord
		}
		messages = append(messages, message)
		matchedRecords = append(matchedRecords, record)
	}
	return messages, matchedRecords, total, nil
}

// DeleteMailboxMessage deletes one entry owned by mailboxWallet. It signs a
// tombstone for the exact immutable mailbox key and submits it to the bound
// CoreNode MessageManager; arbitrary senders cannot delete recipient history.
func (p *Manager) DeleteMailboxMessage(mailboxWallet common.Wallet, key string) error {
	if p == nil || mailboxWallet == nil || strings.TrimSpace(key) == "" {
		return ErrMessageServiceUnavailable
	}
	accountID, err := dkvsAccountID(mailboxWallet)
	if err != nil {
		return err
	}
	root, err := p.accountManagementRootWallet()
	if err != nil {
		return err
	}
	rootID, err := dkvsAccountID(root)
	if err != nil {
		return err
	}
	if accountID != rootID {
		return dkvsindexer.ErrPermissionDenied
	}
	parsed, err := dkvsindexer.ParseKey(key)
	if err != nil || parsed.Namespace != "mail" || len(parsed.Segments) == 0 || parsed.Segments[0] != accountID {
		return dkvsindexer.ErrPermissionDenied
	}
	if err := p.bindAccountToCurrentCoreNode(mailboxWallet); err != nil {
		return err
	}
	store, err := p.accountDKVSStore()
	if err != nil {
		return err
	}
	state, err := store.client.GetKeyState(key)
	if err != nil {
		return err
	}
	if state.Status == dkvsindexer.KeyStateDeleted {
		return nil
	}
	if state.Status != dkvsindexer.KeyStateActive || state.Seq == ^uint64(0) {
		return ErrDKVSRecordNotFound
	}
	tombstone, err := NewDKVSSignedTombstone(mailboxWallet, key,
		dkvsindexer.RecordOptions{Seq: state.Seq + 1})
	if err != nil {
		return err
	}
	client, _, err := p.messageServiceClient()
	if err != nil {
		return err
	}
	_, err = client.SendMessageServiceReq(&swire.MessageServiceRequest{
		Action: swire.MessageServiceActionDeleteMailbox, Record: tombstone,
	})
	return err
}
