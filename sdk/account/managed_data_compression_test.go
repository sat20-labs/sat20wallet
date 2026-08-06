package account

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"math/rand"
	"reflect"
	"testing"
)

func managedDataCompressionFixture(payload []byte) ManagedDataBundle {
	return ManagedDataBundle{
		Version:  ManagedDataBundleVersion,
		Revision: 7,
		Items: []ManagedDataItem{{
			Provider: "rgb11",
			Scope:    "testnet/wallet/0",
			Payload:  payload,
		}},
	}
}

func managedDataCompressionSecrets() ([]byte, string) {
	secret := bytes.Repeat([]byte{0x21}, 32)
	accountID := hex.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	return secret, accountID
}

func TestManagedDataCompressesBeforeEncryptionWhenBeneficial(t *testing.T) {
	payload := bytes.Repeat([]byte("rgb11-proof-consignment-allocation|"), 4096)
	bundle := managedDataCompressionFixture(payload)
	canonical, err := EncodeManagedDataBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	prepared, compressed, err := prepareManagedDataPlaintext(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if !compressed || len(prepared)+managedDataCompressionMinSavings >= len(canonical) {
		t.Fatalf("compression decision compressed=%v canonical=%d prepared=%d", compressed, len(canonical), len(prepared))
	}

	secret, accountID := managedDataCompressionSecrets()
	sealed, err := SealManagedDataBundle(secret, accountID, bundle,
		bytes.NewReader(bytes.Repeat([]byte{0x55}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	if len(sealed) >= len(canonical) {
		t.Fatalf("compressed encrypted envelope=%d canonical=%d", len(sealed), len(canonical))
	}
	restored, err := OpenManagedDataBundle(secret, accountID, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored, bundle) {
		t.Fatalf("restored bundle mismatch")
	}
}

func TestManagedDataKeepsIncompressiblePlaintextUncompressed(t *testing.T) {
	payload := make([]byte, 16*1024)
	if _, err := rand.New(rand.NewSource(1)).Read(payload); err != nil {
		t.Fatal(err)
	}
	bundle := managedDataCompressionFixture(payload)
	canonical, err := EncodeManagedDataBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	prepared, compressed, err := prepareManagedDataPlaintext(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if compressed || !bytes.Equal(prepared, canonical) {
		t.Fatalf("incompressible data was unexpectedly compressed: canonical=%d prepared=%d", len(canonical), len(prepared))
	}

	secret, accountID := managedDataCompressionSecrets()
	sealed, err := SealManagedDataBundle(secret, accountID, bundle,
		bytes.NewReader(bytes.Repeat([]byte{0x33}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	restored, err := OpenManagedDataBundle(secret, accountID, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored, bundle) {
		t.Fatalf("restored uncompressed bundle mismatch")
	}
}

func TestManagedDataRejectsMalformedCompressedPlaintext(t *testing.T) {
	malformed := append(append([]byte(nil), managedDataCompressedMagic...), []byte("not-zlib")...)
	if _, _, err := restoreManagedDataPlaintext(malformed); err == nil {
		t.Fatal("malformed compressed managed data was accepted")
	}
}

func sealLegacyManagedDataEnvelopeForTest(t *testing.T, secret []byte, accountID string,
	bundle ManagedDataBundle) []byte {

	t.Helper()
	plaintext, err := EncodeManagedDataBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	key, err := managedDataKey(secret, accountID)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := bytes.Repeat([]byte{0x19}, gcm.NonceSize())
	ciphertext := gcm.Seal(nil, nonce, plaintext, managedDataAAD(accountID))
	result := append(append(append([]byte(nil), managedDataEnvelopeMagic...), nonce...), ciphertext...)
	zero(plaintext)
	zero(key)
	return result
}

func TestManagedDataLegacyEnvelopeReportsCompressionMigration(t *testing.T) {
	bundle := managedDataCompressionFixture(
		bytes.Repeat([]byte("legacy-rgb11-proof-allocation|"), 4096))
	secret, accountID := managedDataCompressionSecrets()
	legacy := sealLegacyManagedDataEnvelopeForTest(t, secret, accountID, bundle)

	restored, info, err := OpenManagedDataBundleWithInfo(secret, accountID, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if info.Compressed {
		t.Fatal("legacy uncompressed envelope reported compressed")
	}
	if !reflect.DeepEqual(restored, bundle) {
		t.Fatal("legacy envelope payload mismatch")
	}
	beneficial, err := ManagedDataBundleCompressionBeneficial(restored)
	if err != nil {
		t.Fatal(err)
	}
	if !beneficial {
		t.Fatal("legacy envelope was not identified as a beneficial compression candidate")
	}

	migrated, migratedInfo, err := SealManagedDataBundleWithInfo(secret, accountID, restored,
		bytes.NewReader(bytes.Repeat([]byte{0x27}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	if !migratedInfo.Compressed || len(migrated) >= len(legacy) {
		t.Fatalf("migration compressed=%v legacy=%d migrated=%d",
			migratedInfo.Compressed, len(legacy), len(migrated))
	}
}
