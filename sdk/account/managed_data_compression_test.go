package account

import (
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
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

func sealManagedDataInnerForTest(t *testing.T, secret []byte, accountID string,
	inner []byte) []byte {

	t.Helper()
	key, err := managedDataKey(secret, accountID)
	if err != nil {
		t.Fatal(err)
	}
	defer zero(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := bytes.Repeat([]byte{0x19}, gcm.NonceSize())
	ciphertext := gcm.Seal(nil, nonce, inner, managedDataAAD(accountID))
	result := append(append(append([]byte(nil), managedDataEnvelopeMagic...), nonce...), ciphertext...)
	return result
}

func TestManagedDataEnvelopeRequiresCurrentBundleVersion(t *testing.T) {
	bundle := managedDataCompressionFixture(bytes.Repeat([]byte("rgb11-proof-allocation|"), 4096))
	secret, accountID := managedDataCompressionSecrets()
	canonical, err := EncodeManagedDataBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	versionOffset := len(managedDataPlainMagic)
	_, versionSize := binary.Uvarint(canonical[versionOffset:])
	if versionSize <= 0 {
		t.Fatal("current bundle has no explicit version")
	}
	for _, test := range []struct {
		name    string
		version uint64
		valid   bool
	}{
		{name: "current", version: uint64(ManagedDataBundleVersion), valid: true},
		{name: "invalid-v0", version: 0},
		{name: "unknown", version: uint64(ManagedDataBundleVersion) + 1},
		{name: "wrapped-version", version: uint64(ManagedDataBundleVersion) + 1<<32},
	} {
		for _, compressed := range []bool{false, true} {
			encodingName := "plain"
			if compressed {
				encodingName = "compressed"
			}
			t.Run(test.name+"/"+encodingName, func(t *testing.T) {
				// Replace only the version varint in an otherwise valid bundle,
				// then authenticate it with a valid envelope. A version rejection
				// must not be explained by a broken ciphertext or payload shape.
				var version [binary.MaxVarintLen64]byte
				size := binary.PutUvarint(version[:], test.version)
				inner := append([]byte(nil), canonical[:versionOffset]...)
				inner = append(inner, version[:size]...)
				inner = append(inner, canonical[versionOffset+versionSize:]...)
				if compressed {
					var encoded bytes.Buffer
					encoded.Write(managedDataCompressedMagic)
					writer := zlib.NewWriter(&encoded)
					if _, err := writer.Write(inner); err != nil {
						t.Fatal(err)
					}
					if err := writer.Close(); err != nil {
						t.Fatal(err)
					}
					inner = encoded.Bytes()
				}
				sealed := sealManagedDataInnerForTest(t, secret, accountID, inner)
				restored, info, err := OpenManagedDataBundleWithInfo(secret, accountID, sealed)
				if !test.valid {
					if err != ErrRecoveryFailed {
						t.Fatalf("unsupported bundle version %d was not rejected: %v", test.version, err)
					}
					return
				}
				// The current large, compressible bundle is valid in either inner
				// representation. Compression selection belongs to the writer.
				if err != nil || info.Compressed != compressed || !reflect.DeepEqual(restored, bundle) {
					t.Fatalf("current bundle encoding rejected or changed: compressed=%v info=%+v err=%v", compressed, info, err)
				}
			})
		}
	}
}
