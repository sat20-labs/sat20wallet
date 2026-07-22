package account

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

const locatorEncodingPrefix = "sat20locator1:"

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
