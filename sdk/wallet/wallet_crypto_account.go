package wallet

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// normalizedAccountPrivate returns the BIP340 even-y form of a private key.
// Account IDs are x-only public keys, so ECDH must normalize both sides to the
// same representative or an odd-y wallet key would derive a different secret.
func normalizedAccountPrivate(priv *secp256k1.PrivateKey) *secp256k1.PrivateKey {
	if priv == nil {
		return nil
	}
	copyPriv := *priv
	if serialized := priv.PubKey().SerializeCompressed(); len(serialized) == 33 && serialized[0] == 0x03 {
		copyPriv.Key.Negate()
	}
	return &copyPriv
}

func deriveAccountSharedSecret(priv *secp256k1.PrivateKey, accountID string) ([]byte, error) {
	priv = normalizedAccountPrivate(priv)
	if priv == nil {
		return nil, fmt.Errorf("nil private key")
	}
	remote, err := dkvsindexer.AccountPubKeyV2(accountID)
	if err != nil {
		return nil, err
	}
	return deriveSharedSecret(priv, remote)
}

func encryptAccountPayload(shared, plaintext []byte) ([]byte, error) {
	key := sha256.Sum256(shared)
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
	ciphertext := aead.Seal(nil, nonce, plaintext, nil)
	return append(nonce, ciphertext...), nil
}

func decryptAccountPayload(shared, payload []byte) ([]byte, error) {
	key := sha256.Sum256(shared)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(payload) < aead.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	return aead.Open(nil, payload[:aead.NonceSize()], payload[aead.NonceSize():], nil)
}

func (p *InternalWallet) EncryptToAccount(accountID string, plaintext []byte) ([]byte, error) {
	if p == nil {
		return nil, fmt.Errorf("nil wallet")
	}
	p.mutex.Lock()
	priv := p.getPaymentPrivKey()
	p.mutex.Unlock()
	shared, err := deriveAccountSharedSecret(priv, accountID)
	if err != nil {
		return nil, err
	}
	return encryptAccountPayload(shared, plaintext)
}

func (p *InternalWallet) DecryptFromAccount(accountID string, payload []byte) ([]byte, error) {
	if p == nil {
		return nil, fmt.Errorf("nil wallet")
	}
	p.mutex.Lock()
	priv := p.getPaymentPrivKey()
	p.mutex.Unlock()
	shared, err := deriveAccountSharedSecret(priv, accountID)
	if err != nil {
		return nil, err
	}
	return decryptAccountPayload(shared, payload)
}
