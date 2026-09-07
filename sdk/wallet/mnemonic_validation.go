package wallet

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"

	"github.com/tyler-smith/go-bip39"
	"golang.org/x/text/unicode/norm"
)

type MnemonicValidation struct {
	Normalized  string `json:"normalized"`
	Language    string `json:"language"`
	WordCount   string `json:"wordCount"`
	Fingerprint string `json:"fingerprint"`
	AccountID   string `json:"accountId"`
	Address     string `json:"address"`
}

func ValidateMnemonicForCurrentNetwork(raw, password string) (*MnemonicValidation, error) {
	normalizedUnicode := norm.NFKD.String(raw)
	for _, value := range normalizedUnicode {
		if unicode.IsSpace(value) || (value >= 'a' && value <= 'z') {
			continue
		}
		return nil, fmt.Errorf("mnemonic contains an unsupported character")
	}
	words := strings.Fields(normalizedUnicode)
	if len(words) != 12 {
		return nil, fmt.Errorf("only 12-word English mnemonics are supported")
	}
	normalized := strings.Join(words, " ")
	if !bip39.IsMnemonicValid(normalized) {
		return nil, fmt.Errorf("mnemonic has an unknown word or invalid checksum")
	}
	temporary := NewInternalWalletWithMnemonic(normalized, password, GetChainParam())
	if temporary == nil || temporary.GetPaymentPubKey() == nil {
		return nil, fmt.Errorf("mnemonic identity derivation failed")
	}
	fingerprint := sha256.Sum256(temporary.GetPaymentPubKey().SerializeCompressed())
	address := temporary.GetAddressByIndex(0)
	if address == "" {
		return nil, fmt.Errorf("mnemonic address derivation failed")
	}
	accountID, err := dkvsAccountID(temporary)
	if err != nil {
		return nil, fmt.Errorf("mnemonic account identity derivation failed: %w", err)
	}
	return &MnemonicValidation{
		Normalized: normalized, Language: "english", WordCount: "12",
		Fingerprint: hex.EncodeToString(fingerprint[:]), AccountID: accountID, Address: address,
	}, nil
}

func (p *Manager) ValidateMnemonic(raw, password string) (*MnemonicValidation, error) {
	return ValidateMnemonicForCurrentNetwork(raw, password)
}
