package wallet

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	satbtcec "github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func signedPublicLocatorForTest(t *testing.T) AccountPublicLocator {
	t.Helper()
	privateKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	value := AccountPublicLocator{
		Version: account.Version, Network: "testnet",
		StorageLocation: AccountIndexerLocation{Scheme: "https", Host: "dkvs.example"},
		StorageMode:     "paid", RecordTTL: 100,
		Locator: account.Locator{
			Version:   account.Version,
			AccountID: dkvsindexer.AccountID(privateKey.PubKey().SerializeCompressed()),
			PackageID: strings.Repeat("a", 32), RecoveryMode: account.RecoveryMode2Of3,
		},
	}
	message, err := accountPublicLocatorMessage(value)
	if err != nil {
		t.Fatal(err)
	}
	signingKey, _ := satbtcec.PrivKeyFromBytes(privateKey.Serialize())
	hash := sha256.Sum256(message)
	signature, err := schnorr.Sign(signingKey, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	value.Signature = base64.RawURLEncoding.EncodeToString(signature.Serialize())
	return value
}

func TestAccountPublicLocatorRejectsTamperingAndWrongNetwork(t *testing.T) {
	value := signedPublicLocatorForTest(t)
	encoded, err := EncodeAccountPublicLocator(value, "testnet")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeAccountPublicLocator(encoded, "testnet")
	if err != nil || decoded.Locator.AccountID != value.Locator.AccountID {
		t.Fatalf("valid locator rejected: %+v %v", decoded, err)
	}
	tampered := value
	tampered.StorageLocation.Host = "attacker.internal"
	if err := VerifyAccountPublicLocator(tampered, "testnet"); err == nil {
		t.Fatal("tampered endpoint was accepted")
	}
	tampered = value
	tampered.GuardianLocation = &AccountIndexerLocation{Scheme: "https", Host: "guardian.attacker"}
	if err := VerifyAccountPublicLocator(tampered, "testnet"); err == nil {
		t.Fatal("tampered guardian endpoint was accepted")
	}
	if err := VerifyAccountPublicLocator(value, "mainnet"); err == nil {
		t.Fatal("wrong network was accepted")
	}
}

func TestAccountPublicLocatorRejectsSubstitutedSigner(t *testing.T) {
	value := signedPublicLocatorForTest(t)
	attacker, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	message, err := accountPublicLocatorMessage(value)
	if err != nil {
		t.Fatal(err)
	}
	signingKey, _ := satbtcec.PrivKeyFromBytes(attacker.Serialize())
	hash := sha256.Sum256(message)
	signature, err := schnorr.Sign(signingKey, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	value.Signature = base64.RawURLEncoding.EncodeToString(signature.Serialize())
	if err := VerifyAccountPublicLocator(value, "testnet"); err == nil {
		t.Fatal("signature from a key unrelated to AccountID was accepted")
	}
}
