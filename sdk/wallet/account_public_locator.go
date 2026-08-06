package wallet

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const (
	AccountPublicLocatorPrefix = "sat20account1:"
	accountLocatorDomain       = "sat20-account-public-locator-v1\x00"
)

// AccountPublicLocator is intentionally public but authenticated. It contains
// only the information needed to find encrypted account recovery records. The
// signature is made by the root account wallet whose public key is encoded in
// Locator.AccountID.
type AccountPublicLocator struct {
	Version          uint32                  `json:"version"`
	Network          string                  `json:"network"`
	StorageLocation  AccountIndexerLocation  `json:"storage_location"`
	StorageMode      string                  `json:"storage_mode"`
	RecordTTL        uint64                  `json:"record_ttl,omitempty"`
	AutopayContract  string                  `json:"autopay_contract,omitempty"`
	GuardianLocation *AccountIndexerLocation `json:"guardian_location,omitempty"`
	Locator          account.Locator         `json:"locator"`
	Signature        string                  `json:"signature"`
}

type unsignedAccountPublicLocator struct {
	Version          uint32                  `json:"version"`
	Network          string                  `json:"network"`
	StorageLocation  AccountIndexerLocation  `json:"storage_location"`
	StorageMode      string                  `json:"storage_mode"`
	RecordTTL        uint64                  `json:"record_ttl,omitempty"`
	AutopayContract  string                  `json:"autopay_contract,omitempty"`
	GuardianLocation *AccountIndexerLocation `json:"guardian_location,omitempty"`
	Locator          account.Locator         `json:"locator"`
}

func accountPublicLocatorMessage(value AccountPublicLocator) ([]byte, error) {
	unsigned := unsignedAccountPublicLocator{
		Version: value.Version, Network: value.Network,
		StorageLocation: value.StorageLocation, StorageMode: value.StorageMode,
		RecordTTL: value.RecordTTL, AutopayContract: value.AutopayContract,
		GuardianLocation: value.GuardianLocation, Locator: value.Locator,
	}
	encoded, err := json.Marshal(unsigned)
	if err != nil {
		return nil, err
	}
	message := make([]byte, 0, len(accountLocatorDomain)+len(encoded))
	message = append(message, accountLocatorDomain...)
	message = append(message, encoded...)
	return message, nil
}

func validateAccountPublicLocatorShape(value AccountPublicLocator, expectedNetwork string) error {
	if value.Version != account.Version || strings.TrimSpace(value.Network) == "" ||
		strings.TrimSpace(value.StorageLocation.Host) == "" || strings.TrimSpace(value.Signature) == "" {
		return account.ErrInvalidRecoveryPackage
	}
	if expectedNetwork != "" && value.Network != expectedNetwork {
		return fmt.Errorf("account recovery network mismatch: locator=%s wallet=%s", value.Network, expectedNetwork)
	}
	if value.GuardianLocation != nil && strings.TrimSpace(value.GuardianLocation.Host) == "" {
		return account.ErrInvalidRecoveryPackage
	}
	return account.ValidateLocator(value.Locator)
}

func VerifyAccountPublicLocator(value AccountPublicLocator, expectedNetwork string) error {
	if err := validateAccountPublicLocatorShape(value, expectedNetwork); err != nil {
		return err
	}
	pubKeyBytes, err := dkvsindexer.AccountPubKey(value.Locator.AccountID)
	if err != nil {
		return account.ErrInvalidRecoveryPackage
	}
	pubKey, err := btcec.ParsePubKey(pubKeyBytes)
	if err != nil {
		return account.ErrInvalidRecoveryPackage
	}
	signatureBytes, err := base64.RawURLEncoding.DecodeString(value.Signature)
	if err != nil {
		return account.ErrInvalidRecoveryPackage
	}
	signature, err := schnorr.ParseSignature(signatureBytes)
	if err != nil {
		return account.ErrInvalidRecoveryPackage
	}
	message, err := accountPublicLocatorMessage(value)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(message)
	if !signature.Verify(hash[:], pubKey) {
		return fmt.Errorf("account recovery locator signature is invalid")
	}
	return nil
}

func (p *Manager) SignAccountPublicLocator(value *AccountPublicLocator) error {
	if p == nil || value == nil {
		return account.ErrInvalidRecoveryPackage
	}
	root, err := p.accountManagementRootWallet()
	if err != nil {
		return err
	}
	accountID, err := dkvsAccountID(root)
	if err != nil {
		return err
	}
	if accountID != value.Locator.AccountID {
		return fmt.Errorf("account recovery locator root wallet mismatch")
	}
	message, err := accountPublicLocatorMessage(*value)
	if err != nil {
		return err
	}
	signer, ok := root.(dkvsAccountSchnorrSigner)
	if !ok {
		return fmt.Errorf("account root wallet does not support Schnorr signing")
	}
	hash := sha256.Sum256(message)
	signature, err := signer.SignSchnorrMessage(hash[:])
	if err != nil {
		return err
	}
	value.Signature = base64.RawURLEncoding.EncodeToString(signature)
	return VerifyAccountPublicLocator(*value, value.Network)
}

func EncodeAccountPublicLocator(value AccountPublicLocator, expectedNetwork string) (string, error) {
	if err := VerifyAccountPublicLocator(value, expectedNetwork); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if len(encoded) > account.MaxRecoveryObjectSize {
		return "", account.ErrInvalidRecoveryPackage
	}
	return AccountPublicLocatorPrefix + base64.RawURLEncoding.EncodeToString(encoded), nil
}

func DecodeAccountPublicLocator(value, expectedNetwork string) (AccountPublicLocator, error) {
	var locator AccountPublicLocator
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, AccountPublicLocatorPrefix) {
		return locator, account.ErrInvalidRecoveryPackage
	}
	encoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, AccountPublicLocatorPrefix))
	if err != nil || len(encoded) > account.MaxRecoveryObjectSize {
		return locator, account.ErrInvalidRecoveryPackage
	}
	if err := json.Unmarshal(encoded, &locator); err != nil {
		return locator, account.ErrInvalidRecoveryPackage
	}
	if err := VerifyAccountPublicLocator(locator, expectedNetwork); err != nil {
		return AccountPublicLocator{}, err
	}
	return locator, nil
}
