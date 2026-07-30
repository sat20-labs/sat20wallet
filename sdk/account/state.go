package account

import (
	"bytes"
	"compress/flate"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	ManagedStateVersion   = uint32(2)
	managedStateMaxItems  = 1024
	managedStateMaxString = 4096
)

var (
	managedStatePlainMagic    = []byte{'A', 'M', 'S', '2'}
	managedStateEnvelopeMagic = []byte{'A', 'M', 'E', '2'}
)

type ManagedWallet struct {
	Fingerprint  string
	Revision     uint64
	Deleted      bool
	Name         string
	Mnemonic     string
	AccountCount uint32
	SubAccounts  []SubAccount
}

type ManagedState struct {
	Version         uint32
	RootFingerprint string
	Revision        uint64
	Wallets         []ManagedWallet
}

func writeStateUvarint(buf *bytes.Buffer, value uint64) {
	var encoded [binary.MaxVarintLen64]byte
	size := binary.PutUvarint(encoded[:], value)
	buf.Write(encoded[:size])
}

func writeStateString(buf *bytes.Buffer, value string) error {
	if len(value) > managedStateMaxString {
		return ErrInvalidBackup
	}
	writeStateUvarint(buf, uint64(len(value)))
	buf.WriteString(value)
	return nil
}

func readStateUvarint(reader *bytes.Reader) (uint64, error) {
	value, err := binary.ReadUvarint(reader)
	if err != nil {
		return 0, ErrRecoveryFailed
	}
	return value, nil
}

func readStateString(reader *bytes.Reader) (string, error) {
	size, err := readStateUvarint(reader)
	if err != nil || size > managedStateMaxString || size > uint64(reader.Len()) {
		return "", ErrRecoveryFailed
	}
	value := make([]byte, int(size))
	if _, err := io.ReadFull(reader, value); err != nil {
		return "", ErrRecoveryFailed
	}
	return string(value), nil
}

func normalizedManagedState(value ManagedState) (ManagedState, error) {
	if value.Version != ManagedStateVersion || value.Revision == 0 {
		return ManagedState{}, ErrInvalidBackup
	}
	root, err := hex.DecodeString(strings.TrimSpace(value.RootFingerprint))
	if err != nil || len(root) != 32 {
		return ManagedState{}, ErrInvalidBackup
	}
	out := ManagedState{
		Version: ManagedStateVersion, RootFingerprint: hex.EncodeToString(root),
		Revision: value.Revision, Wallets: append([]ManagedWallet(nil), value.Wallets...),
	}
	if len(out.Wallets) == 0 || len(out.Wallets) > managedStateMaxItems {
		return ManagedState{}, ErrInvalidBackup
	}
	sort.Slice(out.Wallets, func(i, j int) bool {
		if out.Wallets[i].Fingerprint == out.RootFingerprint {
			return true
		}
		if out.Wallets[j].Fingerprint == out.RootFingerprint {
			return false
		}
		return out.Wallets[i].Fingerprint < out.Wallets[j].Fingerprint
	})
	seen := make(map[string]struct{}, len(out.Wallets))
	rootPresent := false
	for index := range out.Wallets {
		item := &out.Wallets[index]
		fingerprint, err := hex.DecodeString(strings.TrimSpace(item.Fingerprint))
		if err != nil || len(fingerprint) != 32 || item.Revision == 0 {
			return ManagedState{}, ErrInvalidBackup
		}
		item.Fingerprint = hex.EncodeToString(fingerprint)
		if _, ok := seen[item.Fingerprint]; ok {
			return ManagedState{}, ErrInvalidBackup
		}
		seen[item.Fingerprint] = struct{}{}
		if item.Fingerprint == out.RootFingerprint && !item.Deleted {
			rootPresent = true
		}
		if item.Deleted {
			item.Name = ""
			item.Mnemonic = ""
			item.AccountCount = 0
			item.SubAccounts = nil
			continue
		}
		backup, err := NormalizeBackup(Backup{Version: Version, Wallets: []WalletBackup{{
			Name: item.Name, Mnemonic: item.Mnemonic, AccountCount: item.AccountCount,
			SubAccounts: item.SubAccounts,
		}}})
		if err != nil {
			return ManagedState{}, err
		}
		item.Name = backup.Wallets[0].Name
		item.Mnemonic = backup.Wallets[0].Mnemonic
		item.AccountCount = backup.Wallets[0].AccountCount
		item.SubAccounts = backup.Wallets[0].SubAccounts
		sort.Slice(item.SubAccounts, func(i, j int) bool {
			return item.SubAccounts[i].Index < item.SubAccounts[j].Index
		})
	}
	if !rootPresent {
		return ManagedState{}, ErrInvalidBackup
	}
	return out, nil
}

func BackupFromManagedState(value ManagedState) (Backup, error) {
	state, err := normalizedManagedState(value)
	if err != nil {
		return Backup{}, err
	}
	result := Backup{Version: Version, Wallets: make([]WalletBackup, 0, len(state.Wallets))}
	for _, item := range state.Wallets {
		if item.Deleted {
			continue
		}
		result.Wallets = append(result.Wallets, WalletBackup{
			Name: item.Name, Mnemonic: item.Mnemonic, AccountCount: item.AccountCount,
			SubAccounts: append([]SubAccount(nil), item.SubAccounts...),
		})
	}
	return NormalizeBackup(result)
}

func encodeManagedState(value ManagedState) ([]byte, error) {
	state, err := normalizedManagedState(value)
	if err != nil {
		return nil, err
	}
	var raw bytes.Buffer
	raw.Write(managedStatePlainMagic)
	writeStateUvarint(&raw, uint64(state.Version))
	root, _ := hex.DecodeString(state.RootFingerprint)
	raw.Write(root)
	writeStateUvarint(&raw, state.Revision)
	writeStateUvarint(&raw, uint64(len(state.Wallets)))
	for _, wallet := range state.Wallets {
		fingerprint, _ := hex.DecodeString(wallet.Fingerprint)
		raw.Write(fingerprint)
		writeStateUvarint(&raw, wallet.Revision)
		if wallet.Deleted {
			raw.WriteByte(1)
			continue
		}
		raw.WriteByte(0)
		if err := writeStateString(&raw, wallet.Name); err != nil {
			return nil, err
		}
		if err := writeStateString(&raw, wallet.Mnemonic); err != nil {
			return nil, err
		}
		writeStateUvarint(&raw, uint64(wallet.AccountCount))
		for _, sub := range wallet.SubAccounts {
			writeStateUvarint(&raw, uint64(sub.Index))
			if err := writeStateString(&raw, sub.Name); err != nil {
				return nil, err
			}
			if err := writeStateString(&raw, sub.DID); err != nil {
				return nil, err
			}
		}
	}
	var compressed bytes.Buffer
	writer, err := flate.NewWriter(&compressed, flate.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := writer.Write(raw.Bytes()); err != nil {
		writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return compressed.Bytes(), nil
}

func decodeManagedState(value []byte) (ManagedState, error) {
	reader := flate.NewReader(bytes.NewReader(value))
	raw, err := io.ReadAll(io.LimitReader(reader, MaxRecoveryObjectSize+1))
	closeErr := reader.Close()
	if err != nil || closeErr != nil || len(raw) > MaxRecoveryObjectSize {
		return ManagedState{}, ErrRecoveryFailed
	}
	stream := bytes.NewReader(raw)
	magic := make([]byte, len(managedStatePlainMagic))
	if _, err := io.ReadFull(stream, magic); err != nil || !bytes.Equal(magic, managedStatePlainMagic) {
		return ManagedState{}, ErrRecoveryFailed
	}
	version, err := readStateUvarint(stream)
	if err != nil || uint32(version) != ManagedStateVersion {
		return ManagedState{}, ErrRecoveryFailed
	}
	root := make([]byte, 32)
	if _, err := io.ReadFull(stream, root); err != nil {
		return ManagedState{}, ErrRecoveryFailed
	}
	revision, err := readStateUvarint(stream)
	if err != nil {
		return ManagedState{}, err
	}
	count, err := readStateUvarint(stream)
	if err != nil || count == 0 || count > managedStateMaxItems {
		return ManagedState{}, ErrRecoveryFailed
	}
	state := ManagedState{
		Version: ManagedStateVersion, RootFingerprint: hex.EncodeToString(root),
		Revision: revision, Wallets: make([]ManagedWallet, 0, count),
	}
	for index := uint64(0); index < count; index++ {
		fingerprint := make([]byte, 32)
		if _, err := io.ReadFull(stream, fingerprint); err != nil {
			return ManagedState{}, ErrRecoveryFailed
		}
		itemRevision, err := readStateUvarint(stream)
		if err != nil {
			return ManagedState{}, err
		}
		flag, err := stream.ReadByte()
		if err != nil || flag > 1 {
			return ManagedState{}, ErrRecoveryFailed
		}
		item := ManagedWallet{
			Fingerprint: hex.EncodeToString(fingerprint), Revision: itemRevision, Deleted: flag == 1,
		}
		if !item.Deleted {
			item.Name, err = readStateString(stream)
			if err != nil {
				return ManagedState{}, err
			}
			item.Mnemonic, err = readStateString(stream)
			if err != nil {
				return ManagedState{}, err
			}
			accountCount, err := readStateUvarint(stream)
			if err != nil || accountCount == 0 || accountCount > managedStateMaxItems {
				return ManagedState{}, ErrRecoveryFailed
			}
			item.AccountCount = uint32(accountCount)
			item.SubAccounts = make([]SubAccount, 0, accountCount)
			for accountIndex := uint64(0); accountIndex < accountCount; accountIndex++ {
				subIndex, err := readStateUvarint(stream)
				if err != nil || subIndex > uint64(^uint32(0)) {
					return ManagedState{}, ErrRecoveryFailed
				}
				name, err := readStateString(stream)
				if err != nil {
					return ManagedState{}, err
				}
				did, err := readStateString(stream)
				if err != nil {
					return ManagedState{}, err
				}
				item.SubAccounts = append(item.SubAccounts, SubAccount{Index: uint32(subIndex), Name: name, DID: did})
			}
		}
		state.Wallets = append(state.Wallets, item)
	}
	if stream.Len() != 0 {
		return ManagedState{}, ErrRecoveryFailed
	}
	return normalizedManagedState(state)
}

func managedStateKey(secret []byte, accountID string) ([]byte, error) {
	accountIDBytes, err := hex.DecodeString(accountID)
	if err != nil || len(accountIDBytes) != 32 || len(secret) != accountSecretSize {
		return nil, ErrInvalidAccountID
	}
	return hkdfSHA256(secret, accountIDBytes, []byte("sat20-account-managed-state-v2"), 32), nil
}

func managedStateAAD(accountID string) []byte {
	return []byte("sat20-account-managed-state-aad-v2|" + accountID)
}

func SealManagedState(secret []byte, accountID string, state ManagedState, randomSource io.Reader) ([]byte, error) {
	plaintext, err := encodeManagedState(state)
	if err != nil {
		return nil, err
	}
	defer zero(plaintext)
	key, err := managedStateKey(secret, accountID)
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
	ciphertext := gcm.Seal(nil, nonce, plaintext, managedStateAAD(accountID))
	result := make([]byte, 0, len(managedStateEnvelopeMagic)+len(nonce)+len(ciphertext))
	result = append(result, managedStateEnvelopeMagic...)
	result = append(result, nonce...)
	result = append(result, ciphertext...)
	if len(result) > MaxRecoveryObjectSize {
		return nil, fmt.Errorf("managed account state exceeds DKVS value limit")
	}
	return result, nil
}

func OpenManagedState(secret []byte, accountID string, value []byte) (ManagedState, error) {
	key, err := managedStateKey(secret, accountID)
	if err != nil {
		return ManagedState{}, err
	}
	defer zero(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return ManagedState{}, ErrRecoveryFailed
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(value) < len(managedStateEnvelopeMagic)+gcm.NonceSize()+gcm.Overhead() ||
		!bytes.Equal(value[:len(managedStateEnvelopeMagic)], managedStateEnvelopeMagic) {
		return ManagedState{}, ErrRecoveryFailed
	}
	offset := len(managedStateEnvelopeMagic)
	nonce := value[offset : offset+gcm.NonceSize()]
	ciphertext := value[offset+gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, managedStateAAD(accountID))
	if err != nil {
		return ManagedState{}, ErrRecoveryFailed
	}
	defer zero(plaintext)
	return decodeManagedState(plaintext)
}
