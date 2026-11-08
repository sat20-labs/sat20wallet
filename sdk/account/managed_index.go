package account

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"sort"
	"strings"

	strict "github.com/sat20-labs/rgb11/strict_encoding"
)

const (
	ManagedIndexVersion   = uint32(1)
	ManagedIndexMaxItems  = 4096
	ManagedObjectMaxBytes = 1 << 20
	managedProviderMax    = 128
	managedScopeMax       = 1024
	managedCodecMax       = 128
	managedKeyMax         = 4096
	managedIndexMagic     = "AMIX"
	managedIndexSeal      = "AMIE\x01"
)

type ManagedEntry struct {
	Provider string
	Scope    string
	Key      string
	Codec    string
	Deleted  bool
}

type ManagedIndex struct {
	Version uint32
	Entries []ManagedEntry
}

func ManagedIndexKey(accountID string) (string, error) {
	if !validHex(accountID, 64) {
		return "", ErrInvalidAccountID
	}
	return "/personal/" + accountID + "/account/index", nil
}

func managedNames(provider, scope string) (string, string, error) {
	provider, scope = strings.TrimSpace(provider), strings.TrimSpace(scope)
	if !validManagedName(provider, managedProviderMax) || strings.ContainsAny(provider, "/:") ||
		!validManagedName(scope, managedScopeMax) {
		return "", "", ErrInvalidBackup
	}
	return provider, scope, nil
}

func validManagedName(value string, limit int) bool {
	if value == "" || len(value) > limit {
		return false
	}
	for _, ch := range value {
		if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || strings.ContainsRune("-_.:/", ch) {
			continue
		}
		return false
	}
	return true
}

// Entry identity has explicit field boundaries and never includes payload,
// codec, account, or a local revision. A content update retains the same key.
func ManagedEntryID(provider, scope string) (string, error) {
	provider, scope, err := managedNames(provider, scope)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	e := strict.NewEncoder(&buf)
	_ = e.Raw([]byte("sat20-account-entry-v1\x00"))
	_ = e.String(provider, 1, managedProviderMax)
	_ = e.String(scope, 1, managedScopeMax)
	hash := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(hash[:]), nil
}

func ManagedModuleKey(accountID, provider, scope string) (string, error) {
	if !validHex(accountID, 64) {
		return "", ErrInvalidAccountID
	}
	id, err := ManagedEntryID(provider, scope)
	if err != nil {
		return "", err
	}
	return "/blob/" + accountID + "/" + id, nil
}

func NormalizeManagedIndex(accountID string, value ManagedIndex) (ManagedIndex, error) {
	if !validHex(accountID, 64) {
		return ManagedIndex{}, ErrInvalidAccountID
	}
	if value.Version != ManagedIndexVersion || len(value.Entries) > ManagedIndexMaxItems {
		return ManagedIndex{}, ErrInvalidBackup
	}
	result := ManagedIndex{Version: ManagedIndexVersion, Entries: make([]ManagedEntry, 0, len(value.Entries))}
	seen, keys := make(map[string]bool), make(map[string]bool)
	for _, entry := range value.Entries {
		var err error
		entry.Provider, entry.Scope, err = managedNames(entry.Provider, entry.Scope)
		entry.Codec = strings.TrimSpace(entry.Codec)
		if err != nil || !validManagedName(entry.Codec, managedCodecMax) {
			return ManagedIndex{}, ErrInvalidBackup
		}
		key, err := ManagedModuleKey(accountID, entry.Provider, entry.Scope)
		coreKey := entry.Provider == "wallets" && entry.Scope == "global" &&
			entry.Key == "/personal/"+accountID+"/account/state"
		if err != nil || (entry.Key != key && !coreKey) {
			return ManagedIndex{}, ErrInvalidBackup
		}
		id := entry.Provider + "\x00" + entry.Scope
		if seen[id] || keys[entry.Key] {
			return ManagedIndex{}, ErrInvalidBackup
		}
		seen[id], keys[entry.Key] = true, true
		result.Entries = append(result.Entries, entry)
	}
	sort.Slice(result.Entries, func(i, j int) bool {
		if result.Entries[i].Provider != result.Entries[j].Provider {
			return result.Entries[i].Provider < result.Entries[j].Provider
		}
		return result.Entries[i].Scope < result.Entries[j].Scope
	})
	return result, nil
}

func encodeManagedIndex(accountID string, value ManagedIndex) ([]byte, error) {
	value, err := NormalizeManagedIndex(accountID, value)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	e := strict.NewEncoder(&buf)
	_ = e.Raw([]byte(managedIndexMagic))
	_ = e.U8(uint8(value.Version))
	_ = e.Length(uint64(len(value.Entries)), ManagedIndexMaxItems)
	for _, entry := range value.Entries {
		for _, field := range []struct {
			text string
			max  uint64
		}{{entry.Provider, managedProviderMax}, {entry.Scope, managedScopeMax}, {entry.Key, managedKeyMax}, {entry.Codec, managedCodecMax}} {
			if err := e.String(field.text, 1, field.max); err != nil {
				return nil, err
			}
		}
		_ = e.Bool(entry.Deleted)
	}
	if buf.Len() > ManagedObjectMaxBytes {
		return nil, ErrInvalidBackup
	}
	return buf.Bytes(), nil
}

func decodeManagedIndex(accountID string, value []byte) (ManagedIndex, error) {
	r := bytes.NewReader(value)
	d := strict.NewDecoder(r)
	magic, err := d.Raw(uint64(len(managedIndexMagic)))
	if err != nil || string(magic) != managedIndexMagic {
		return ManagedIndex{}, ErrRecoveryFailed
	}
	version, err := d.U8()
	if err != nil || uint32(version) != ManagedIndexVersion {
		return ManagedIndex{}, ErrRecoveryFailed
	}
	count, err := d.Length(ManagedIndexMaxItems)
	if err != nil {
		return ManagedIndex{}, ErrRecoveryFailed
	}
	result := ManagedIndex{Version: uint32(version), Entries: make([]ManagedEntry, 0, count)}
	for i := uint64(0); i < count; i++ {
		entry := ManagedEntry{}
		for _, field := range []struct {
			text *string
			max  uint64
		}{{&entry.Provider, managedProviderMax}, {&entry.Scope, managedScopeMax}, {&entry.Key, managedKeyMax}, {&entry.Codec, managedCodecMax}} {
			*field.text, err = d.String(1, field.max)
			if err != nil {
				return ManagedIndex{}, ErrRecoveryFailed
			}
		}
		entry.Deleted, err = d.Bool()
		if err != nil {
			return ManagedIndex{}, ErrRecoveryFailed
		}
		result.Entries = append(result.Entries, entry)
	}
	canonical, err := encodeManagedIndex(accountID, result)
	if err != nil || r.Len() != 0 || !bytes.Equal(canonical, value) {
		return ManagedIndex{}, ErrRecoveryFailed
	}
	return result, nil
}

func managedContext(accountID, network, key, domain string) ([]byte, []byte, error) {
	if !validHex(accountID, 64) {
		return nil, nil, ErrInvalidAccountID
	}
	if network != strings.TrimSpace(network) || !validManagedName(network, 64) || len(key) == 0 || len(key) > managedKeyMax {
		return nil, nil, ErrInvalidBackup
	}
	accountBytes, _ := hex.DecodeString(accountID)
	var buf bytes.Buffer
	e := strict.NewEncoder(&buf)
	_ = e.String("sat20-account-"+domain+"-aad", 1, 128)
	_ = e.Raw(accountBytes)
	_ = e.String(network, 1, 64)
	_ = e.String(key, 1, managedKeyMax)
	return accountBytes, buf.Bytes(), nil
}

func sealManagedObject(secret []byte, accountID, network, key, domain, magic string, plain []byte, randomSource io.Reader) ([]byte, error) {
	salt, aad, err := managedContext(accountID, network, key, domain)
	if err != nil {
		return nil, err
	}
	if len(secret) != accountSecretSize || len(plain)+len(magic)+12+16 > ManagedObjectMaxBytes {
		return nil, ErrInvalidBackup
	}
	derived := hkdfSHA256(secret, salt, []byte("sat20-account-"+domain), 32)
	defer zero(derived)
	nonceText, cipherText, err := sealBytes(derived, plain, aad, randomSource)
	if err != nil {
		return nil, err
	}
	nonce, _ := base64.RawURLEncoding.DecodeString(nonceText)
	sealed, _ := base64.RawURLEncoding.DecodeString(cipherText)
	result := append([]byte(magic), nonce...)
	return append(result, sealed...), nil
}

func openManagedObject(secret []byte, accountID, network, key, domain, magic string, value []byte) ([]byte, error) {
	salt, aad, err := managedContext(accountID, network, key, domain)
	if err != nil {
		return nil, err
	}
	if len(secret) != accountSecretSize || len(value) < len(magic)+12+16 || len(value) > ManagedObjectMaxBytes || !bytes.HasPrefix(value, []byte(magic)) {
		return nil, ErrRecoveryFailed
	}
	derived := hkdfSHA256(secret, salt, []byte("sat20-account-"+domain), 32)
	defer zero(derived)
	nonce := value[len(magic) : len(magic)+12]
	ciphertext := value[len(magic)+12:]
	return openSealed(derived, base64.RawURLEncoding.EncodeToString(nonce), base64.RawURLEncoding.EncodeToString(ciphertext), aad)
}

func SealManagedIndex(secret []byte, accountID, network string, value ManagedIndex, randomSource io.Reader) ([]byte, error) {
	key, err := ManagedIndexKey(accountID)
	if err != nil {
		return nil, err
	}
	plain, err := encodeManagedIndex(accountID, value)
	if err != nil {
		return nil, err
	}
	defer zero(plain)
	return sealManagedObject(secret, accountID, network, key, "managed-index-v1", managedIndexSeal, plain, randomSource)
}

func OpenManagedIndex(secret []byte, accountID, network string, value []byte) (ManagedIndex, error) {
	key, err := ManagedIndexKey(accountID)
	if err != nil {
		return ManagedIndex{}, err
	}
	plain, err := openManagedObject(secret, accountID, network, key, "managed-index-v1", managedIndexSeal, value)
	if err != nil {
		return ManagedIndex{}, err
	}
	defer zero(plain)
	return decodeManagedIndex(accountID, plain)
}
