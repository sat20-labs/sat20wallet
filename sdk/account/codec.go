package account

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	locatorEncodingPrefix = "sat20account1:"
	shareEncodingPrefix   = "sat20share1:"
)

func EncodeLocator(locator Locator) (string, error) {
	if err := ValidateLocator(locator); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(locator)
	if err != nil {
		return "", err
	}
	return locatorEncodingPrefix + base64.RawURLEncoding.EncodeToString(encoded), nil
}

func DecodeLocator(value string) (Locator, error) {
	var locator Locator
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, locatorEncodingPrefix) {
		return locator, ErrInvalidRecoveryPackage
	}
	encoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, locatorEncodingPrefix))
	if err != nil {
		return locator, ErrInvalidRecoveryPackage
	}
	if err := json.Unmarshal(encoded, &locator); err != nil {
		return locator, ErrInvalidRecoveryPackage
	}
	if err := ValidateLocator(locator); err != nil {
		return locator, err
	}
	return locator, nil
}

func EncodeRecoveryShare(share RecoveryShare) (string, error) {
	if _, err := validateShare(share); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(share)
	if err != nil {
		return "", err
	}
	return shareEncodingPrefix + base64.RawURLEncoding.EncodeToString(encoded), nil
}

func DecodeRecoveryShare(value string) (RecoveryShare, error) {
	var share RecoveryShare
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, shareEncodingPrefix) {
		return share, ErrInvalidShare
	}
	encoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, shareEncodingPrefix))
	if err != nil {
		return share, ErrInvalidShare
	}
	if len(encoded) > MaxRecoveryObjectSize {
		return share, fmt.Errorf("encoded recovery share exceeds maximum size")
	}
	if err := json.Unmarshal(encoded, &share); err != nil {
		return share, ErrInvalidShare
	}
	if _, err := validateShare(share); err != nil {
		return share, err
	}
	return share, nil
}
