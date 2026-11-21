package account

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
)

func zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func hkdfSHA256(secret, salt, info []byte, size int) []byte {
	extract := hmac.New(sha256.New, salt)
	_, _ = extract.Write(secret)
	prk := extract.Sum(nil)
	defer zero(prk)
	out := make([]byte, 0, size)
	var previous []byte
	for counter := byte(1); len(out) < size; counter++ {
		mac := hmac.New(sha256.New, prk)
		_, _ = mac.Write(previous)
		_, _ = mac.Write(info)
		_, _ = mac.Write([]byte{counter})
		previous = mac.Sum(nil)
		out = append(out, previous...)
	}
	zero(previous)
	return out[:size]
}

func locatorAAD(locator Locator, domain string) []byte {
	return []byte(fmt.Sprintf("%s|%d|%s|%s|%s", domain, locator.Version, locator.AccountID, locator.PackageID, locator.RecoveryMode))
}

func deriveBackupKey(secret []byte, locator Locator) []byte {
	return hkdfSHA256(secret, []byte(locator.PackageID), []byte("sat20-account-backup-v1|"+locator.AccountID), 32)
}

func sealBytes(key, plaintext, aad []byte, random io.Reader) (nonce, ciphertext string, err error) {
	if len(key) != 32 {
		return "", "", ErrInvalidRecoveryPackage
	}
	if random == nil {
		random = rand.Reader
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", err
	}
	nonceBytes := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(random, nonceBytes); err != nil {
		return "", "", err
	}
	sealed := gcm.Seal(nil, nonceBytes, plaintext, aad)
	return base64.RawURLEncoding.EncodeToString(nonceBytes), base64.RawURLEncoding.EncodeToString(sealed), nil
}

func openSealed(key []byte, nonceText, ciphertextText string, aad []byte) ([]byte, error) {
	nonce, err := base64.RawURLEncoding.DecodeString(nonceText)
	if err != nil {
		return nil, ErrRecoveryFailed
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(ciphertextText)
	if err != nil {
		return nil, ErrRecoveryFailed
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrRecoveryFailed
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(nonce) != gcm.NonceSize() {
		return nil, ErrRecoveryFailed
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrRecoveryFailed
	}
	return plaintext, nil
}

func recoveryKnowledgeKey(locator Locator, share RecoveryShare) ([]byte, error) {
	if err := ValidateLocator(locator); err != nil {
		return nil, err
	}
	raw, err := validateShare(share)
	if err != nil {
		return nil, err
	}
	defer zero(raw)
	total := uint8(2)
	if locator.RecoveryMode == RecoveryMode2Of3 {
		total = 3
	}
	if share.Role != ShareRoleUser || share.PackageID != locator.PackageID || share.Total != total {
		return nil, ErrInvalidShare
	}
	return hkdfSHA256(raw[:accountSecretSize], []byte(locator.PackageID), []byte("sat20-account-recovery-knowledge-v1"), 32), nil
}

func sealRecoveryKnowledge(locator Locator, share RecoveryShare, knowledge KnowledgeRecoveryBundle, random io.Reader) (EncryptedBlob, error) {
	key, err := recoveryKnowledgeKey(locator, share)
	if err != nil {
		return EncryptedBlob{}, err
	}
	defer zero(key)
	plain, err := encodeRecoveryKnowledge(knowledge)
	if err != nil {
		return EncryptedBlob{}, err
	}
	defer zero(plain)
	nonce, ciphertext, err := sealBytes(key, plain, locatorAAD(locator, "sat20-account-recovery-knowledge-aad-v1"), random)
	if err != nil {
		return EncryptedBlob{}, err
	}
	return EncryptedBlob{Algorithm: "aes-256-gcm", Nonce: nonce, Ciphertext: ciphertext}, nil
}

// OpenRecoveryKnowledge authenticates the complete question bundle before use.
func OpenRecoveryKnowledge(pkg RecoveryPackage, share RecoveryShare) (KnowledgeRecoveryBundle, error) {
	locator := pkg.Envelope.Locator
	if err := validateEncryptedBlob(pkg.SealedKnowledge); err != nil {
		return KnowledgeRecoveryBundle{}, err
	}
	key, err := recoveryKnowledgeKey(locator, share)
	if err != nil {
		return KnowledgeRecoveryBundle{}, err
	}
	defer zero(key)
	plain, err := openSealed(key, pkg.SealedKnowledge.Nonce, pkg.SealedKnowledge.Ciphertext,
		locatorAAD(locator, "sat20-account-recovery-knowledge-aad-v1"))
	if err != nil {
		return KnowledgeRecoveryBundle{}, err
	}
	defer zero(plain)
	return decodeRecoveryKnowledge(locator.PackageID, plain)
}

func encryptBackup(secret []byte, locator Locator, backup Backup, random io.Reader) (EncryptedBlob, error) {
	plaintext, err := json.Marshal(backup)
	if err != nil {
		return EncryptedBlob{}, err
	}
	defer zero(plaintext)
	key := deriveBackupKey(secret, locator)
	defer zero(key)
	nonce, ciphertext, err := sealBytes(key, plaintext, locatorAAD(locator, "sat20-account-backup-aad-v1"), random)
	if err != nil {
		return EncryptedBlob{}, err
	}
	return EncryptedBlob{Algorithm: "aes-256-gcm", Nonce: nonce, Ciphertext: ciphertext}, nil
}

func decryptBackup(secret []byte, locator Locator, encrypted EncryptedBlob) (Backup, error) {
	if err := validateEncryptedBlob(encrypted); err != nil {
		return Backup{}, err
	}
	key := deriveBackupKey(secret, locator)
	defer zero(key)
	plaintext, err := openSealed(key, encrypted.Nonce, encrypted.Ciphertext, locatorAAD(locator, "sat20-account-backup-aad-v1"))
	if err != nil {
		return Backup{}, err
	}
	defer zero(plaintext)
	var backup Backup
	if err := json.Unmarshal(plaintext, &backup); err != nil {
		return Backup{}, ErrRecoveryFailed
	}
	return NormalizeBackup(backup)
}
