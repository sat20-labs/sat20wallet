package wallet

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const (
	topicCryptoVersion   = uint32(1)
	topicKeySize         = 32
	topicCryptoDBPrefix  = "topic-crypto/"
	topicMessageMaxPlain = swire.MaxDKVSValueSize - 128
)

var (
	topicMessageKeyDomain = []byte("SAT20-TOPIC-MESSAGE-KEY")
	topicMessageAADDomain = []byte("SAT20-TOPIC-MESSAGE-AAD")
)

type storedTopicKey struct {
	Version   uint32
	TopicName string
	KeySeq    uint64
	Wrapped   []byte
}

type storedTopicCurrent struct {
	Version   uint32
	TopicName string
	KeySeq    uint64
}

type TopicMessage struct {
	Publish   *swire.TopicPublishMessage
	Plaintext []byte
	Record    *swire.DKVSRecord
}

type TopicCryptoManager struct {
	manager *Manager
	wallet  *InternalWallet
}

func NewTopicCryptoManager(manager *Manager, wallet *InternalWallet) (*TopicCryptoManager, error) {
	if manager == nil || manager.db == nil || wallet == nil {
		return nil, ErrMessageServiceUnavailable
	}
	if _, err := dkvsAccountID(wallet); err != nil {
		return nil, err
	}
	return &TopicCryptoManager{manager: manager, wallet: wallet}, nil
}

func normalizeTopicID(topicName string) (string, error) {
	normalized := dkvsindexer.NormalizeNameID(topicName)
	if normalized == "" {
		return "", fmt.Errorf("invalid topic name")
	}
	if _, err := dkvsindexer.TopicStateKey(normalized); err != nil {
		return "", err
	}
	return normalized, nil
}

func (m *TopicCryptoManager) accountID() (string, error) {
	if m == nil || m.wallet == nil {
		return "", ErrMessageServiceUnavailable
	}
	return dkvsAccountID(m.wallet)
}

func topicKeyDBKey(accountID, topicName string, keySeq uint64) []byte {
	return []byte(GetDBKeyPrefix() + topicCryptoDBPrefix + accountID + "/" + topicName + "/key/" + strconv.FormatUint(keySeq, 10))
}

func topicCurrentDBKey(accountID, topicName string) []byte {
	return []byte(GetDBKeyPrefix() + topicCryptoDBPrefix + accountID + "/" + topicName + "/current")
}

func generateTopicKey() ([]byte, error) {
	key := make([]byte, topicKeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	return key, nil
}

func (m *TopicCryptoManager) StoreTopicKey(topicName string, keySeq uint64, topicKey []byte) error {
	if m == nil || m.manager == nil || m.manager.db == nil || keySeq == 0 || len(topicKey) != topicKeySize {
		return fmt.Errorf("invalid topic key")
	}
	topicName, err := normalizeTopicID(topicName)
	if err != nil {
		return err
	}
	accountID, err := m.accountID()
	if err != nil {
		return err
	}
	wrapped, err := m.wallet.EncryptToAccount(accountID, topicKey)
	if err != nil {
		return err
	}
	keyRecord := storedTopicKey{Version: topicCryptoVersion, TopicName: topicName, KeySeq: keySeq, Wrapped: wrapped}
	encoded, err := EncodeToBytes(&keyRecord)
	if err != nil {
		return err
	}
	if existingRaw, readErr := m.manager.db.Read(topicKeyDBKey(accountID, topicName, keySeq)); readErr == nil && len(existingRaw) != 0 {
		var existing storedTopicKey
		if DecodeFromBytes(existingRaw, &existing) != nil || existing.Version != topicCryptoVersion || existing.TopicName != topicName || existing.KeySeq != keySeq {
			return fmt.Errorf("invalid stored topic key")
		}
		plain, err := m.wallet.DecryptFromAccount(accountID, existing.Wrapped)
		if err != nil || !bytes.Equal(plain, topicKey) {
			return fmt.Errorf("topic key sequence already stores a different key")
		}
	} else if readErr != nil && !errors.Is(readErr, indexer.ErrKeyNotFound) {
		return readErr
	} else if err := m.manager.db.Write(topicKeyDBKey(accountID, topicName, keySeq), encoded); err != nil {
		return err
	}

	currentSeq := uint64(0)
	if raw, readErr := m.manager.db.Read(topicCurrentDBKey(accountID, topicName)); readErr == nil && len(raw) != 0 {
		var current storedTopicCurrent
		if DecodeFromBytes(raw, &current) != nil || current.Version != topicCryptoVersion || current.TopicName != topicName {
			return fmt.Errorf("invalid topic current-key state")
		}
		currentSeq = current.KeySeq
	} else if readErr != nil && !errors.Is(readErr, indexer.ErrKeyNotFound) {
		return readErr
	}
	if keySeq >= currentSeq {
		current := storedTopicCurrent{Version: topicCryptoVersion, TopicName: topicName, KeySeq: keySeq}
		currentEncoded, err := EncodeToBytes(&current)
		if err != nil {
			return err
		}
		if err := m.manager.db.Write(topicCurrentDBKey(accountID, topicName), currentEncoded); err != nil {
			return err
		}
	}
	return nil
}

func (m *TopicCryptoManager) LoadTopicKey(topicName string, keySeq uint64) ([]byte, error) {
	if m == nil || m.manager == nil || m.manager.db == nil || keySeq == 0 {
		return nil, fmt.Errorf("invalid topic key request")
	}
	topicName, err := normalizeTopicID(topicName)
	if err != nil {
		return nil, err
	}
	accountID, err := m.accountID()
	if err != nil {
		return nil, err
	}
	raw, err := m.manager.db.Read(topicKeyDBKey(accountID, topicName, keySeq))
	if err != nil {
		return nil, err
	}
	var stored storedTopicKey
	if DecodeFromBytes(raw, &stored) != nil || stored.Version != topicCryptoVersion || stored.TopicName != topicName || stored.KeySeq != keySeq {
		return nil, fmt.Errorf("invalid stored topic key")
	}
	key, err := m.wallet.DecryptFromAccount(accountID, stored.Wrapped)
	if err != nil || len(key) != topicKeySize {
		return nil, fmt.Errorf("invalid topic key payload")
	}
	return key, nil
}

func (m *TopicCryptoManager) CurrentTopicKeySeq(topicName string) (uint64, error) {
	topicName, err := normalizeTopicID(topicName)
	if err != nil {
		return 0, err
	}
	accountID, err := m.accountID()
	if err != nil {
		return 0, err
	}
	raw, err := m.manager.db.Read(topicCurrentDBKey(accountID, topicName))
	if err != nil {
		return 0, err
	}
	var current storedTopicCurrent
	if DecodeFromBytes(raw, &current) != nil || current.Version != topicCryptoVersion || current.TopicName != topicName || current.KeySeq == 0 {
		return 0, fmt.Errorf("invalid topic current-key state")
	}
	return current.KeySeq, nil
}

func topicMessageContext(topicName string, keySeq uint64, senderAccount string, senderMsgID uint64, messageID string) ([]byte, error) {
	topicName, err := normalizeTopicID(topicName)
	if err != nil || len(senderAccount) != 64 || keySeq == 0 || !swire.ValidMessageID(messageID) {
		return nil, fmt.Errorf("invalid topic message context")
	}
	buf := bytes.NewBuffer(nil)
	writeField := func(value []byte) {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(value)))
		buf.Write(size[:])
		buf.Write(value)
	}
	writeField([]byte(topicName))
	writeField([]byte(senderAccount))
	writeField([]byte(messageID))
	var scratch [16]byte
	binary.BigEndian.PutUint64(scratch[:8], keySeq)
	binary.BigEndian.PutUint64(scratch[8:], senderMsgID)
	buf.Write(scratch[:])
	return buf.Bytes(), nil
}

func deriveTopicMessageKey(topicKey, context []byte) [32]byte {
	hasher := sha256.New()
	_, _ = hasher.Write(topicMessageKeyDomain)
	_, _ = hasher.Write(topicKey)
	_, _ = hasher.Write(context)
	var key [32]byte
	copy(key[:], hasher.Sum(nil))
	return key
}

func encryptTopicPayload(topicKey, context, plaintext []byte) ([]byte, error) {
	key := deriveTopicMessageKey(topicKey, context)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	aad := append(append([]byte(nil), topicMessageAADDomain...), context...)
	ciphertext := aead.Seal(nil, nonce, plaintext, aad)
	return append(nonce, ciphertext...), nil
}

func decryptTopicPayload(topicKey, context, payload []byte) ([]byte, error) {
	key := deriveTopicMessageKey(topicKey, context)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(payload) < aead.NonceSize() {
		return nil, fmt.Errorf("topic ciphertext too short")
	}
	aad := append(append([]byte(nil), topicMessageAADDomain...), context...)
	return aead.Open(nil, payload[:aead.NonceSize()], payload[aead.NonceSize():], aad)
}

func verifyTopicAccountSignature(accountID string, signature []byte, hash [32]byte) error {
	pubBytes, err := dkvsindexer.AccountPubKey(accountID)
	if err != nil {
		return dkvsindexer.ErrInvalidSignature
	}
	pub, err := btcec.ParsePubKey(pubBytes)
	if err != nil {
		return dkvsindexer.ErrInvalidSignature
	}
	sig, err := schnorr.ParseSignature(signature)
	if err != nil || !sig.Verify(hash[:], pub) {
		return dkvsindexer.ErrInvalidSignature
	}
	return nil
}

func (m *TopicCryptoManager) CreateKeyPackages(topicName string, keySeq uint64, topicKey []byte, recipients []string) ([]swire.TopicKeyPackageRecipient, error) {
	if m == nil || len(topicKey) != topicKeySize || keySeq == 0 || len(recipients) == 0 {
		return nil, fmt.Errorf("invalid topic key package request")
	}
	topicName, err := normalizeTopicID(topicName)
	if err != nil {
		return nil, err
	}
	issuer, err := m.accountID()
	if err != nil {
		return nil, err
	}
	signer, ok := interface{}(m.wallet).(dkvsAccountSchnorrSigner)
	if !ok {
		return nil, fmt.Errorf("topic wallet cannot sign")
	}
	seen := make(map[string]struct{}, len(recipients))
	packages := make([]swire.TopicKeyPackageRecipient, 0, len(recipients))
	for _, recipient := range recipients {
		recipient = strings.ToLower(strings.TrimSpace(recipient))
		if _, exists := seen[recipient]; exists {
			return nil, fmt.Errorf("duplicate topic key recipient")
		}
		seen[recipient] = struct{}{}
		encrypted, err := m.wallet.EncryptToAccount(recipient, topicKey)
		if err != nil {
			return nil, err
		}
		item := swire.TopicKeyPackageRecipient{Recipient: recipient, EncryptedTopicKey: encrypted}
		hash, err := swire.TopicKeyPackageSigningHash(topicName, keySeq, issuer, item)
		if err != nil {
			return nil, err
		}
		item.IssuerSignature, err = signer.SignSchnorrMessage(hash[:])
		if err != nil {
			return nil, err
		}
		packages = append(packages, item)
	}
	return packages, nil
}

func (m *TopicCryptoManager) AcceptKeyPackage(fanout *swire.TopicKeyFanoutMessage) error {
	if m == nil || fanout == nil || len(fanout.Recipients) != 1 || fanout.KeySeq == 0 {
		return fmt.Errorf("invalid topic key package")
	}
	accountID, err := m.accountID()
	if err != nil {
		return err
	}
	item := fanout.Recipients[0]
	if item.Recipient != accountID {
		return dkvsindexer.ErrPermissionDenied
	}
	hash, err := swire.TopicKeyPackageSigningHash(fanout.TopicName, fanout.KeySeq, fanout.IssuerAccount, item)
	if err != nil {
		return err
	}
	if err := verifyTopicAccountSignature(fanout.IssuerAccount, item.IssuerSignature, hash); err != nil {
		return err
	}
	key, err := m.wallet.DecryptFromAccount(fanout.IssuerAccount, item.EncryptedTopicKey)
	if err != nil || len(key) != topicKeySize {
		return fmt.Errorf("invalid decrypted topic key")
	}
	return m.StoreTopicKey(fanout.TopicName, fanout.KeySeq, key)
}

func (m *TopicCryptoManager) EncryptPublish(topicName string, keySeq, senderMsgID uint64, messageID string, plaintext []byte) (*swire.TopicPublishMessage, error) {
	if m == nil || len(plaintext) == 0 || len(plaintext) > topicMessageMaxPlain {
		return nil, fmt.Errorf("invalid topic plaintext")
	}
	topicName, err := normalizeTopicID(topicName)
	if err != nil {
		return nil, err
	}
	sender, err := m.accountID()
	if err != nil {
		return nil, err
	}
	topicKey, err := m.LoadTopicKey(topicName, keySeq)
	if err != nil {
		return nil, err
	}
	context, err := topicMessageContext(topicName, keySeq, sender, senderMsgID, messageID)
	if err != nil {
		return nil, err
	}
	ciphertext, err := encryptTopicPayload(topicKey, context, plaintext)
	if err != nil {
		return nil, err
	}
	message := &swire.TopicPublishMessage{
		TopicName: topicName, SenderAccount: sender, SenderMsgID: senderMsgID,
		MessageID: messageID, KeySeq: keySeq, Ciphertext: ciphertext,
	}
	hash, err := swire.TopicPublishSigningHash(message)
	if err != nil {
		return nil, err
	}
	signer, ok := interface{}(m.wallet).(dkvsAccountSchnorrSigner)
	if !ok {
		return nil, fmt.Errorf("topic wallet cannot sign")
	}
	message.SenderSignature, err = signer.SignSchnorrMessage(hash[:])
	if err != nil {
		return nil, err
	}
	return message, nil
}

func (m *TopicCryptoManager) DecryptPublish(message *swire.TopicPublishMessage) ([]byte, error) {
	if m == nil || message == nil {
		return nil, fmt.Errorf("invalid topic publish")
	}
	hash, err := swire.TopicPublishSigningHash(message)
	if err != nil {
		return nil, err
	}
	if err := verifyTopicAccountSignature(message.SenderAccount, message.SenderSignature, hash); err != nil {
		return nil, err
	}
	topicKey, err := m.LoadTopicKey(message.TopicName, message.KeySeq)
	if err != nil {
		return nil, err
	}
	context, err := topicMessageContext(message.TopicName, message.KeySeq, message.SenderAccount, message.SenderMsgID, message.MessageID)
	if err != nil {
		return nil, err
	}
	return decryptTopicPayload(topicKey, context, message.Ciphertext)
}
