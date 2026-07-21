package account

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	bip39 "github.com/tyler-smith/go-bip39"
)

func validHex(value string, size int) bool {
	if len(value) != size {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func ValidateLocator(locator Locator) error {
	if locator.Version != Version || !validHex(locator.AccountID, 64) {
		return ErrInvalidAccountID
	}
	if !validHex(locator.PackageID, 32) {
		return ErrInvalidPackageID
	}
	if locator.RecoveryMode != RecoveryMode2Of2 && locator.RecoveryMode != RecoveryMode2Of3 {
		return ErrInvalidRecoveryPackage
	}
	return nil
}

func normalizeSpace(value string) string { return strings.Join(strings.Fields(strings.TrimSpace(value)), " ") }

func NormalizeBackup(value Backup) (Backup, error) {
	if value.Version != Version || len(value.Wallets) == 0 {
		return Backup{}, ErrInvalidBackup
	}
	out := Backup{Version: Version, Wallets: make([]WalletBackup, len(value.Wallets))}
	walletNames := map[string]struct{}{}
	for walletIndex, wallet := range value.Wallets {
		name := normalizeSpace(wallet.Name)
		mnemonic := normalizeSpace(wallet.Mnemonic)
		if name == "" || !bip39.IsMnemonicValid(mnemonic) || wallet.AccountCount == 0 || int(wallet.AccountCount) != len(wallet.SubAccounts) {
			return Backup{}, ErrInvalidBackup
		}
		if _, ok := walletNames[name]; ok {
			return Backup{}, fmt.Errorf("%w: duplicate wallet name", ErrInvalidBackup)
		}
		walletNames[name] = struct{}{}
		subAccounts := append([]SubAccount(nil), wallet.SubAccounts...)
		seenIndex := map[uint32]struct{}{}
		seenDID := map[string]struct{}{}
		for i := range subAccounts {
			subAccounts[i].DID = normalizeSpace(subAccounts[i].DID)
			if subAccounts[i].Index >= wallet.AccountCount || subAccounts[i].DID == "" {
				return Backup{}, ErrInvalidBackup
			}
			if _, ok := seenIndex[subAccounts[i].Index]; ok {
				return Backup{}, ErrInvalidBackup
			}
			if _, ok := seenDID[subAccounts[i].DID]; ok {
				return Backup{}, ErrInvalidBackup
			}
			seenIndex[subAccounts[i].Index] = struct{}{}
			seenDID[subAccounts[i].DID] = struct{}{}
		}
		for i := uint32(0); i < wallet.AccountCount; i++ {
			if _, ok := seenIndex[i]; !ok {
				return Backup{}, ErrInvalidBackup
			}
		}
		out.Wallets[walletIndex] = WalletBackup{Name: name, Mnemonic: mnemonic, AccountCount: wallet.AccountCount, SubAccounts: subAccounts}
	}
	return out, nil
}

func validateEncryptedBlob(value EncryptedBlob) error {
	if value.Algorithm != "aes-256-gcm" || value.Nonce == "" || value.Ciphertext == "" {
		return ErrInvalidRecoveryPackage
	}
	return nil
}

func validateEnvelope(value Envelope) error {
	if value.Version != Version {
		return ErrInvalidRecoveryPackage
	}
	if err := ValidateLocator(value.Locator); err != nil {
		return err
	}
	return validateEncryptedBlob(value.EncryptedBackup)
}

func validateManifest(value Manifest, locator Locator) error {
	if value.Version != Version || value.Locator != locator || value.Threshold != 2 || !validHex(value.EnvelopeHash, 64) {
		return ErrInvalidRecoveryPackage
	}
	expected := uint8(2)
	if locator.RecoveryMode == RecoveryMode2Of3 {
		expected = 3
	}
	if value.Total != expected {
		return ErrInvalidRecoveryPackage
	}
	return nil
}

func canonicalHash(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func HashEnvelope(value Envelope) (string, error) {
	if err := validateEnvelope(value); err != nil {
		return "", err
	}
	return canonicalHash(value)
}

func HashGuardianCapsule(value GuardianShareCapsule) (string, error) {
	if err := validateGuardianCapsule(value); err != nil {
		return "", err
	}
	return canonicalHash(value)
}

func SummarizeBackup(locator Locator, backup Backup) RecoverySummary {
	summary := RecoverySummary{AccountID: locator.AccountID, PackageID: locator.PackageID, RecoveryMode: locator.RecoveryMode, Wallets: make([]WalletSummary, len(backup.Wallets))}
	for i, wallet := range backup.Wallets {
		dids := make([]string, len(wallet.SubAccounts))
		for j, subAccount := range wallet.SubAccounts {
			dids[j] = subAccount.DID
		}
		summary.Wallets[i] = WalletSummary{Name: wallet.Name, AccountCount: wallet.AccountCount, DIDs: dids}
	}
	return summary
}
