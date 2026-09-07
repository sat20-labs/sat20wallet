package wallet

import (
	"strings"
	"testing"
)

func TestValidateMnemonicStrictNormalization(t *testing.T) {
	const validMnemonic = "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire"
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	result, err := ValidateMnemonicForCurrentNetwork("  inflict\tresource march liquid pigeon salad ankle miracle badge twelve smart wire\n", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Normalized != validMnemonic || result.Language != "english" || result.WordCount != "12" {
		t.Fatalf("unexpected normalized mnemonic: %+v", result)
	}
	if len(result.Fingerprint) != 64 || len(result.AccountID) != 64 || !strings.HasPrefix(result.Address, "tb1p") {
		t.Fatalf("unexpected derived identity: %+v", result)
	}

	for name, mnemonic := range map[string]string{
		"punctuation is not deleted": validMnemonic + ".",
		"uppercase is not rewritten": strings.Replace(validMnemonic, "inflict", "Inflict", 1),
		"unknown word":               strings.Replace(validMnemonic, "inflict", "notaword", 1),
		"bad checksum":               strings.TrimSuffix(validMnemonic, "wire") + "witness",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateMnemonicForCurrentNetwork(mnemonic, ""); err == nil {
				t.Fatal("invalid mnemonic was accepted")
			}
		})
	}
}
