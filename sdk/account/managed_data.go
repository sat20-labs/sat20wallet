package account

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	ManagedDataBundleVersion = uint32(1)
	managedDataMaxItems      = 4096
	managedDataMaxProvider   = 128
	managedDataMaxScope      = 1024
	ManagedDataMaxPayload    = 1 << 20
)

var (
	managedDataPlainMagic    = []byte{'A', 'M', 'D'}
	managedDataEnvelopeMagic = []byte{'A', 'D', 'E'}
)

// ManagedDataItem is one module-owned, account-managed recovery payload.
// Provider names are stable module identifiers. Scope is opaque to account
// management, but must be stable across devices.
type ManagedDataItem struct {
	Provider string
	Scope    string
	Payload  []byte
}

// ManagedDataBundle is the complete set of module recovery data referenced by
// one ManagedState revision. Absence from a newer bundle removes an older item.
type ManagedDataBundle struct {
	Version  uint32
	Revision uint64
	Items    []ManagedDataItem
}

func validManagedDataName(value string, limit int) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > limit {
		return false
	}
	for _, ch := range value {
		if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' ||
			ch == '-' || ch == '_' || ch == '.' || ch == '/' || ch == ':' {
			continue
		}
		return false
	}
	return true
}

func NormalizeManagedDataBundle(value ManagedDataBundle) (ManagedDataBundle, error) {
	if value.Version != ManagedDataBundleVersion || value.Revision == 0 ||
		len(value.Items) > managedDataMaxItems {
		return ManagedDataBundle{}, ErrInvalidBackup
	}
	result := ManagedDataBundle{
		Version: ManagedDataBundleVersion, Revision: value.Revision,
		Items: make([]ManagedDataItem, 0, len(value.Items)),
	}
	seen := make(map[string]struct{}, len(value.Items))
	for _, raw := range value.Items {
		provider := strings.TrimSpace(raw.Provider)
		scope := strings.TrimSpace(raw.Scope)
		if !validManagedDataName(provider, managedDataMaxProvider) ||
			!validManagedDataName(scope, managedDataMaxScope) ||
			len(raw.Payload) == 0 || len(raw.Payload) > ManagedDataMaxPayload {
			return ManagedDataBundle{}, ErrInvalidBackup
		}
		key := provider + "\x00" + scope
		if _, exists := seen[key]; exists {
			return ManagedDataBundle{}, ErrInvalidBackup
		}
		seen[key] = struct{}{}
		result.Items = append(result.Items, ManagedDataItem{
			Provider: provider, Scope: scope, Payload: append([]byte(nil), raw.Payload...),
		})
	}
	sort.Slice(result.Items, func(i, j int) bool {
		if result.Items[i].Provider != result.Items[j].Provider {
			return result.Items[i].Provider < result.Items[j].Provider
		}
		return result.Items[i].Scope < result.Items[j].Scope
	})
	return result, nil
}

func writeManagedDataBytes(buf *bytes.Buffer, value []byte) {
	writeStateUvarint(buf, uint64(len(value)))
	buf.Write(value)
}

func readManagedDataBytes(reader *bytes.Reader, limit uint64) ([]byte, error) {
	size, err := readStateUvarint(reader)
	if err != nil || size == 0 || size > limit || size > uint64(reader.Len()) {
		return nil, ErrRecoveryFailed
	}
	value := make([]byte, int(size))
	if _, err := io.ReadFull(reader, value); err != nil {
		return nil, ErrRecoveryFailed
	}
	return value, nil
}

func EncodeManagedDataBundle(value ManagedDataBundle) ([]byte, error) {
	bundle, err := NormalizeManagedDataBundle(value)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.Write(managedDataPlainMagic)
	writeStateUvarint(&buf, uint64(bundle.Version))
	writeStateUvarint(&buf, bundle.Revision)
	writeStateUvarint(&buf, uint64(len(bundle.Items)))
	for _, item := range bundle.Items {
		if err := writeStateString(&buf, item.Provider); err != nil {
			return nil, err
		}
		if err := writeStateString(&buf, item.Scope); err != nil {
			return nil, err
		}
		writeManagedDataBytes(&buf, item.Payload)
	}
	if buf.Len() > ManagedDataMaxPayload {
		return nil, fmt.Errorf("account-managed data exceeds blob limit")
	}
	return buf.Bytes(), nil
}

func DecodeManagedDataBundle(value []byte) (ManagedDataBundle, error) {
	if len(value) == 0 || len(value) > ManagedDataMaxPayload {
		return ManagedDataBundle{}, ErrRecoveryFailed
	}
	reader := bytes.NewReader(value)
	magic := make([]byte, len(managedDataPlainMagic))
	if _, err := io.ReadFull(reader, magic); err != nil || !bytes.Equal(magic, managedDataPlainMagic) {
		return ManagedDataBundle{}, ErrRecoveryFailed
	}
	version, err := readStateUvarint(reader)
	if err != nil || uint32(version) != ManagedDataBundleVersion {
		return ManagedDataBundle{}, ErrRecoveryFailed
	}
	revision, err := readStateUvarint(reader)
	if err != nil || revision == 0 {
		return ManagedDataBundle{}, ErrRecoveryFailed
	}
	count, err := readStateUvarint(reader)
	if err != nil || count > managedDataMaxItems {
		return ManagedDataBundle{}, ErrRecoveryFailed
	}
	bundle := ManagedDataBundle{Version: ManagedDataBundleVersion, Revision: revision,
		Items: make([]ManagedDataItem, 0, count)}
	for index := uint64(0); index < count; index++ {
		provider, err := readStateString(reader)
		if err != nil {
			return ManagedDataBundle{}, err
		}
		scope, err := readStateString(reader)
		if err != nil {
			return ManagedDataBundle{}, err
		}
		payload, err := readManagedDataBytes(reader, ManagedDataMaxPayload)
		if err != nil {
			return ManagedDataBundle{}, err
		}
		bundle.Items = append(bundle.Items, ManagedDataItem{Provider: provider, Scope: scope, Payload: payload})
	}
	if reader.Len() != 0 {
		return ManagedDataBundle{}, ErrRecoveryFailed
	}
	return NormalizeManagedDataBundle(bundle)
}

func ManagedDataBundleHash(value ManagedDataBundle) (string, error) {
	encoded, err := EncodeManagedDataBundle(value)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

func managedDataKey(secret []byte, accountID string) ([]byte, error) {
	accountIDBytes, err := hex.DecodeString(accountID)
	if err != nil || len(accountIDBytes) != 32 || len(secret) != accountSecretSize {
		return nil, ErrInvalidAccountID
	}
	return hkdfSHA256(secret, accountIDBytes, []byte("sat20-account-managed-data"), 32), nil
}

func managedDataAAD(accountID string) []byte {
	return []byte("sat20-account-managed-data-aad|" + accountID)
}

func SealManagedDataBundle(secret []byte, accountID string, bundle ManagedDataBundle,
	randomSource io.Reader) ([]byte, error) {

	plaintext, err := EncodeManagedDataBundle(bundle)
	if err != nil {
		return nil, err
	}
	defer zero(plaintext)
	key, err := managedDataKey(secret, accountID)
	if err != nil {
		return nil, err
	}
	defer zero(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if randomSource == nil {
		randomSource = rand.Reader
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(randomSource, nonce); err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, managedDataAAD(accountID))
	result := make([]byte, 0, len(managedDataEnvelopeMagic)+len(nonce)+len(ciphertext))
	result = append(result, managedDataEnvelopeMagic...)
	result = append(result, nonce...)
	result = append(result, ciphertext...)
	if len(result) > ManagedDataMaxPayload {
		return nil, fmt.Errorf("encrypted account-managed data exceeds blob limit")
	}
	return result, nil
}

func OpenManagedDataBundle(secret []byte, accountID string, value []byte) (ManagedDataBundle, error) {
	key, err := managedDataKey(secret, accountID)
	if err != nil {
		return ManagedDataBundle{}, err
	}
	defer zero(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return ManagedDataBundle{}, ErrRecoveryFailed
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(value) < len(managedDataEnvelopeMagic)+gcm.NonceSize()+gcm.Overhead() ||
		!bytes.Equal(value[:len(managedDataEnvelopeMagic)], managedDataEnvelopeMagic) {
		return ManagedDataBundle{}, ErrRecoveryFailed
	}
	offset := len(managedDataEnvelopeMagic)
	nonce := value[offset : offset+gcm.NonceSize()]
	ciphertext := value[offset+gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, managedDataAAD(accountID))
	if err != nil {
		return ManagedDataBundle{}, ErrRecoveryFailed
	}
	defer zero(plaintext)
	return DecodeManagedDataBundle(plaintext)
}

// ManagedDataBundleDigest verifies the exact plaintext content referenced by a
// ManagedState without exposing it outside the account-management envelope.
func ManagedDataBundleDigest(value []byte) string {
	hash := sha256.Sum256(value)
	return hex.EncodeToString(hash[:])
}

func EncodeManagedDataRevision(value uint64) []byte {
	var result [8]byte
	binary.BigEndian.PutUint64(result[:], value)
	return result[:]
}
