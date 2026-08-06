package account

import (
	"bytes"
	"compress/zlib"
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
	ManagedDataBundleVersion         = uint32(1)
	managedDataMaxItems              = 4096
	managedDataMaxProvider           = 128
	managedDataMaxScope              = 1024
	ManagedDataMaxPayload            = 1 << 20
	managedDataCompressionThreshold  = 1024
	managedDataCompressionMinSavings = 64
)

var (
	managedDataPlainMagic      = []byte{'A', 'M', 'D'}
	managedDataEnvelopeMagic   = []byte{'A', 'D', 'E'}
	managedDataCompressedMagic = []byte{'A', 'D', 'C', 1}
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

// ManagedDataEnvelopeInfo describes the authenticated inner encoding without
// exposing plaintext bytes to callers.
type ManagedDataEnvelopeInfo struct {
	Compressed bool
}

// prepareManagedDataPlaintext compresses the canonical bundle before
// encryption only when doing so produces a meaningful size reduction. The
// marker is encrypted and authenticated together with the compressed bytes.
func prepareManagedDataPlaintext(value []byte) ([]byte, bool, error) {
	if len(value) < managedDataCompressionThreshold {
		return value, false, nil
	}
	var buf bytes.Buffer
	buf.Grow(len(value) / 2)
	buf.Write(managedDataCompressedMagic)
	writer, err := zlib.NewWriterLevel(&buf, zlib.BestSpeed)
	if err != nil {
		return nil, false, err
	}
	if _, err := writer.Write(value); err != nil {
		_ = writer.Close()
		zero(buf.Bytes())
		return nil, false, err
	}
	if err := writer.Close(); err != nil {
		zero(buf.Bytes())
		return nil, false, err
	}
	compressed := buf.Bytes()
	if len(compressed)+managedDataCompressionMinSavings >= len(value) {
		zero(compressed)
		return value, false, nil
	}
	return compressed, true, nil
}

func restoreManagedDataPlaintext(value []byte) ([]byte, bool, error) {
	if bytes.HasPrefix(value, managedDataPlainMagic) {
		// Uncompressed envelopes, including records written before compression
		// support was added, remain canonical input.
		return value, false, nil
	}
	if !bytes.HasPrefix(value, managedDataCompressedMagic) {
		return nil, false, ErrRecoveryFailed
	}
	reader, err := zlib.NewReader(bytes.NewReader(value[len(managedDataCompressedMagic):]))
	if err != nil {
		return nil, false, ErrRecoveryFailed
	}
	decoded, readErr := io.ReadAll(io.LimitReader(reader, ManagedDataMaxPayload+1))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || len(decoded) == 0 ||
		len(decoded) > ManagedDataMaxPayload || !bytes.HasPrefix(decoded, managedDataPlainMagic) {
		zero(decoded)
		return nil, false, ErrRecoveryFailed
	}
	return decoded, true, nil
}

// ManagedDataBundleCompressionBeneficial reports whether the canonical bundle
// would use compressed inner encoding under the current codec policy.
func ManagedDataBundleCompressionBeneficial(bundle ManagedDataBundle) (bool, error) {
	plaintext, err := EncodeManagedDataBundle(bundle)
	if err != nil {
		return false, err
	}
	defer zero(plaintext)
	prepared, compressed, err := prepareManagedDataPlaintext(plaintext)
	if compressed {
		zero(prepared)
	}
	return compressed, err
}

func SealManagedDataBundle(secret []byte, accountID string, bundle ManagedDataBundle,
	randomSource io.Reader) ([]byte, error) {

	value, _, err := SealManagedDataBundleWithInfo(secret, accountID, bundle, randomSource)
	return value, err
}

func SealManagedDataBundleWithInfo(secret []byte, accountID string, bundle ManagedDataBundle,
	randomSource io.Reader) ([]byte, ManagedDataEnvelopeInfo, error) {

	plaintext, err := EncodeManagedDataBundle(bundle)
	if err != nil {
		return nil, ManagedDataEnvelopeInfo{}, err
	}
	defer zero(plaintext)
	payload, compressed, err := prepareManagedDataPlaintext(plaintext)
	if err != nil {
		return nil, ManagedDataEnvelopeInfo{}, err
	}
	if compressed {
		defer zero(payload)
	}
	key, err := managedDataKey(secret, accountID)
	if err != nil {
		return nil, ManagedDataEnvelopeInfo{}, err
	}
	defer zero(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ManagedDataEnvelopeInfo{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ManagedDataEnvelopeInfo{}, err
	}
	if randomSource == nil {
		randomSource = rand.Reader
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(randomSource, nonce); err != nil {
		return nil, ManagedDataEnvelopeInfo{}, err
	}
	ciphertext := gcm.Seal(nil, nonce, payload, managedDataAAD(accountID))
	result := make([]byte, 0, len(managedDataEnvelopeMagic)+len(nonce)+len(ciphertext))
	result = append(result, managedDataEnvelopeMagic...)
	result = append(result, nonce...)
	result = append(result, ciphertext...)
	if len(result) > ManagedDataMaxPayload {
		return nil, ManagedDataEnvelopeInfo{}, fmt.Errorf("encrypted account-managed data exceeds blob limit")
	}
	return result, ManagedDataEnvelopeInfo{Compressed: compressed}, nil
}

func OpenManagedDataBundle(secret []byte, accountID string, value []byte) (ManagedDataBundle, error) {
	bundle, _, err := OpenManagedDataBundleWithInfo(secret, accountID, value)
	return bundle, err
}

func OpenManagedDataBundleWithInfo(secret []byte, accountID string,
	value []byte) (ManagedDataBundle, ManagedDataEnvelopeInfo, error) {
	key, err := managedDataKey(secret, accountID)
	if err != nil {
		return ManagedDataBundle{}, ManagedDataEnvelopeInfo{}, err
	}
	defer zero(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return ManagedDataBundle{}, ManagedDataEnvelopeInfo{}, ErrRecoveryFailed
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(value) < len(managedDataEnvelopeMagic)+gcm.NonceSize()+gcm.Overhead() ||
		!bytes.Equal(value[:len(managedDataEnvelopeMagic)], managedDataEnvelopeMagic) {
		return ManagedDataBundle{}, ManagedDataEnvelopeInfo{}, ErrRecoveryFailed
	}
	offset := len(managedDataEnvelopeMagic)
	nonce := value[offset : offset+gcm.NonceSize()]
	ciphertext := value[offset+gcm.NonceSize():]
	payload, err := gcm.Open(nil, nonce, ciphertext, managedDataAAD(accountID))
	if err != nil {
		return ManagedDataBundle{}, ManagedDataEnvelopeInfo{}, ErrRecoveryFailed
	}
	defer zero(payload)
	plaintext, expanded, err := restoreManagedDataPlaintext(payload)
	if err != nil {
		return ManagedDataBundle{}, ManagedDataEnvelopeInfo{}, err
	}
	if expanded {
		defer zero(plaintext)
	}
	bundle, err := DecodeManagedDataBundle(plaintext)
	if err != nil {
		return ManagedDataBundle{}, ManagedDataEnvelopeInfo{}, err
	}
	return bundle, ManagedDataEnvelopeInfo{Compressed: expanded}, nil
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
