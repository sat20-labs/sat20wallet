package account

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"github.com/sat20-labs/sat20wallet/sdk/account/internal/shamir"
)

func expectedRole(total, index uint8) ShareRole {
	if total == 2 {
		if index == 1 {
			return ShareRoleUser
		}
		if index == 2 {
			return ShareRoleDKVS
		}
	}
	if total == 3 {
		if index == 1 {
			return ShareRoleUser
		}
		if index == 2 {
			return ShareRoleDKVS
		}
		if index == 3 {
			return ShareRoleGuardian
		}
	}
	return ""
}

func shareChecksum(value RecoveryShare) string {
	copyValue := value
	copyValue.Checksum = ""
	encoded, _ := json.Marshal(copyValue)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:8])
}

func newRecoveryShare(packageID string, total, index uint8, data []byte) (RecoveryShare, error) {
	value := RecoveryShare{Version: Version, PackageID: packageID, Threshold: 2, Total: total, Index: index, Role: expectedRole(total, index), Data: base64.RawURLEncoding.EncodeToString(data)}
	value.Checksum = shareChecksum(value)
	_, err := validateShare(value)
	return value, err
}

func validateShare(value RecoveryShare) ([]byte, error) {
	if value.Version != Version || !validHex(value.PackageID, 32) || value.Threshold != 2 ||
		(value.Total != 2 && value.Total != 3) || value.Index < 1 || value.Index > value.Total ||
		value.Role != expectedRole(value.Total, value.Index) || !validHex(value.Checksum, 16) || shareChecksum(value) != value.Checksum {
		return nil, ErrInvalidShare
	}
	raw, err := base64.RawURLEncoding.DecodeString(value.Data)
	if err != nil || len(raw) != accountSecretSize+shamir.ShareOverhead || raw[len(raw)-1] != value.Index {
		return nil, ErrInvalidShare
	}
	return raw, nil
}

func splitSecret(secret []byte, packageID string, mode RecoveryMode, random io.Reader) ([]RecoveryShare, error) {
	total := 2
	if mode == RecoveryMode2Of3 {
		total = 3
	} else if mode != RecoveryMode2Of2 {
		return nil, ErrInvalidRecoveryPackage
	}
	rawShares, err := shamir.Split(secret, total, 2, random)
	if err != nil {
		return nil, err
	}
	shares := make([]RecoveryShare, total)
	for i := range rawShares {
		shares[i], err = newRecoveryShare(packageID, uint8(total), uint8(i+1), rawShares[i])
		if err != nil {
			return nil, err
		}
	}
	return shares, nil
}

func CombineShares(shares ...RecoveryShare) ([]byte, error) {
	if len(shares) < 2 {
		return nil, ErrInsufficientShares
	}
	reference := shares[0]
	seen := map[uint8]struct{}{}
	raw := make([][]byte, 0, len(shares))
	for _, share := range shares {
		if share.PackageID != reference.PackageID || share.Total != reference.Total || share.Threshold != reference.Threshold {
			return nil, ErrInvalidShare
		}
		if _, ok := seen[share.Index]; ok {
			return nil, ErrInvalidShare
		}
		seen[share.Index] = struct{}{}
		decoded, err := validateShare(share)
		if err != nil {
			return nil, err
		}
		raw = append(raw, decoded)
	}
	secret, err := shamir.Combine(raw[:2])
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRecoveryFailed, err)
	}
	if len(secret) != accountSecretSize {
		return nil, ErrRecoveryFailed
	}
	return secret, nil
}

func EncodeRecoveryShare(value RecoveryShare) (string, error) {
	if _, err := validateShare(value); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return "sat20-share-v1:" + base64.RawURLEncoding.EncodeToString(encoded), nil
}

func DecodeRecoveryShare(value string) (RecoveryShare, error) {
	const prefix = "sat20-share-v1:"
	if len(value) <= len(prefix) || value[:len(prefix)] != prefix {
		return RecoveryShare{}, ErrInvalidShare
	}
	raw, err := base64.RawURLEncoding.DecodeString(value[len(prefix):])
	if err != nil {
		return RecoveryShare{}, ErrInvalidShare
	}
	var share RecoveryShare
	if err := json.Unmarshal(raw, &share); err != nil {
		return RecoveryShare{}, ErrInvalidShare
	}
	if _, err := validateShare(share); err != nil {
		return RecoveryShare{}, err
	}
	return share, nil
}
