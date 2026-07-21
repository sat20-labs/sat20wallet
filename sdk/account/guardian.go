package account

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
)

func GenerateGuardianKey(random io.Reader) (privateKey, publicKey []byte, err error) {
	if random == nil {
		random = rand.Reader
	}
	key, err := ecdh.X25519().GenerateKey(random)
	if err != nil {
		return nil, nil, err
	}
	return append([]byte(nil), key.Bytes()...), append([]byte(nil), key.PublicKey().Bytes()...), nil
}

func guardianAAD(packageID, shareID string) []byte {
	return []byte("sat20-guardian-share-v1|" + packageID + "|" + shareID)
}
func guardianKey(shared []byte, packageID, shareID string) []byte {
	return hkdfSHA256(shared, []byte(packageID), []byte("sat20-guardian-wrap-v1|"+shareID), 32)
}
func validateGuardianCapsule(value GuardianShareCapsule) error {
	if value.Version != Version || !validHex(value.PackageID, 32) || !validHex(value.ShareID, 16) || value.Algorithm != "x25519-aes-256-gcm" || value.EphemeralPublicKey == "" || value.Nonce == "" || value.Ciphertext == "" {
		return ErrInvalidRecoveryPackage
	}
	return nil
}

func EncryptGuardianShare(share RecoveryShare, guardianPublicKey []byte, random io.Reader) (GuardianShareCapsule, error) {
	if _, err := validateShare(share); err != nil || share.Role != ShareRoleGuardian {
		return GuardianShareCapsule{}, ErrInvalidShare
	}
	if random == nil {
		random = rand.Reader
	}
	curve := ecdh.X25519()
	recipient, err := curve.NewPublicKey(guardianPublicKey)
	if err != nil {
		return GuardianShareCapsule{}, fmt.Errorf("invalid guardian public key")
	}
	ephemeral, err := curve.GenerateKey(random)
	if err != nil {
		return GuardianShareCapsule{}, err
	}
	shared, err := ephemeral.ECDH(recipient)
	if err != nil {
		return GuardianShareCapsule{}, err
	}
	defer zero(shared)
	key := guardianKey(shared, share.PackageID, share.Checksum)
	defer zero(key)
	plaintext, err := json.Marshal(share)
	if err != nil {
		return GuardianShareCapsule{}, err
	}
	defer zero(plaintext)
	nonce, ciphertext, err := sealBytes(key, plaintext, guardianAAD(share.PackageID, share.Checksum), random)
	if err != nil {
		return GuardianShareCapsule{}, err
	}
	capsule := GuardianShareCapsule{Version: Version, PackageID: share.PackageID, ShareID: share.Checksum, Algorithm: "x25519-aes-256-gcm", EphemeralPublicKey: base64.RawURLEncoding.EncodeToString(ephemeral.PublicKey().Bytes()), Nonce: nonce, Ciphertext: ciphertext}
	if err := validateGuardianCapsule(capsule); err != nil {
		return GuardianShareCapsule{}, err
	}
	return capsule, nil
}

func DecryptGuardianShare(capsule GuardianShareCapsule, guardianPrivateKey []byte) (RecoveryShare, error) {
	if err := validateGuardianCapsule(capsule); err != nil {
		return RecoveryShare{}, err
	}
	curve := ecdh.X25519()
	privateKey, err := curve.NewPrivateKey(guardianPrivateKey)
	if err != nil {
		return RecoveryShare{}, ErrRecoveryFailed
	}
	ephemeralBytes, err := base64.RawURLEncoding.DecodeString(capsule.EphemeralPublicKey)
	if err != nil {
		return RecoveryShare{}, ErrRecoveryFailed
	}
	ephemeral, err := curve.NewPublicKey(ephemeralBytes)
	if err != nil {
		return RecoveryShare{}, ErrRecoveryFailed
	}
	shared, err := privateKey.ECDH(ephemeral)
	if err != nil {
		return RecoveryShare{}, ErrRecoveryFailed
	}
	defer zero(shared)
	key := guardianKey(shared, capsule.PackageID, capsule.ShareID)
	defer zero(key)
	plaintext, err := openSealed(key, capsule.Nonce, capsule.Ciphertext, guardianAAD(capsule.PackageID, capsule.ShareID))
	if err != nil {
		return RecoveryShare{}, ErrRecoveryFailed
	}
	defer zero(plaintext)
	var share RecoveryShare
	if err := json.Unmarshal(plaintext, &share); err != nil {
		return RecoveryShare{}, ErrRecoveryFailed
	}
	if _, err := validateShare(share); err != nil || share.PackageID != capsule.PackageID || share.Checksum != capsule.ShareID || share.Role != ShareRoleGuardian {
		return RecoveryShare{}, ErrRecoveryFailed
	}
	return share, nil
}
