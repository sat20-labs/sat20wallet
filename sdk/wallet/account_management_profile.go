package wallet

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"

	indexercommon "github.com/sat20-labs/indexer/common"

	"github.com/sat20-labs/sat20wallet/sdk/account"
)

const (
	accountManagementProfileVersion = uint32(2)
	accountManagementProfileDBKey   = "account-management-profile-v2"
	accountManagementDeviceIDSize   = 16
)

type accountManagementProfile struct {
	Version               uint32
	RootFingerprint       string
	AccountID             string
	PackageID             string
	RecoveryMode          account.RecoveryMode
	StorageMode           string
	Location              AccountIndexerLocation
	RecordTTL             uint64
	AutopayContract       string
	PublicLocator         string
	LastRehearsalAt       int64
	SecretCipher          []byte
	SecretSalt            []byte
	DeviceID              []byte
	StateSeq              uint64
	StateHash             string
	StateEnvelope         []byte
	ManagedDataRevision   uint64
	ManagedDataHash       string
	ManagedDataEnvelope   []byte
	ManagedDataDirty      bool
	ManagedDataGeneration uint64
	RecoveryConfigured    bool
	Pending               []accountManagementMutation
}

type accountManagementMutation struct {
	ID          string
	Type        string
	Fingerprint string
	WalletID    int64
	Account     uint32
	Name        string
	DID         string
}

func accountManagementProfileKey() []byte {
	return []byte(GetDBKeyPrefix() + accountManagementProfileDBKey)
}

func (p *Manager) loadAccountManagementProfileLocked() error {
	encoded, err := p.db.Read(accountManagementProfileKey())
	if errors.Is(err, indexercommon.ErrKeyNotFound) {
		p.accountProfile = nil
		return nil
	}
	if err != nil {
		return err
	}
	if len(encoded) == 0 {
		return fmt.Errorf("empty account management profile")
	}
	var profile accountManagementProfile
	if err := DecodeFromBytes(encoded, &profile); err != nil {
		return err
	}
	if profile.Version != accountManagementProfileVersion ||
		len(profile.DeviceID) != accountManagementDeviceIDSize ||
		strings.TrimSpace(profile.RootFingerprint) == "" ||
		strings.TrimSpace(profile.AccountID) == "" {
		return fmt.Errorf("invalid account management profile")
	}
	p.accountProfile = &profile
	p.bumpAccountGenerationLocked()
	return nil
}

func (p *Manager) saveAccountManagementProfileLocked() error {
	if p.accountProfile == nil {
		return fmt.Errorf("account management profile is unavailable")
	}
	encoded, err := EncodeToBytes(p.accountProfile)
	if err != nil {
		return err
	}
	return p.db.Write(accountManagementProfileKey(), encoded)
}

func (p *Manager) encryptAccountManagementSecret(password string, secret []byte) ([]byte, []byte, error) {
	if len(secret) != 32 {
		return nil, nil, fmt.Errorf("invalid account management secret")
	}
	key, err := p.newSnaclKey(password)
	if err != nil {
		return nil, nil, err
	}
	encrypted, err := key.Encrypt(secret)
	if err != nil {
		return nil, nil, err
	}
	return encrypted, key.Marshal(), nil
}

func (p *Manager) unlockAccountManagementLocked(password string) error {
	secret, err := p.decryptAccountManagementSecretLocked(password)
	if err != nil {
		return err
	}
	// PWA screen unlock authenticates a running session. Its unchanged keys
	// must not invalidate account sync already waiting on the network.
	if p.accountPassword == password && bytes.Equal(p.accountSecret, secret) {
		zeroBytes(secret)
		return nil
	}
	zeroBytes(p.accountSecret)
	p.accountSecret = secret
	p.accountPassword = password
	p.bumpAccountGenerationLocked()
	return nil
}

func (p *Manager) decryptAccountManagementSecretLocked(password string) ([]byte, error) {
	if p.accountProfile == nil {
		return nil, nil
	}
	encoded, err := p.db.Read(accountManagementProfileKey())
	if err != nil {
		return nil, err
	}
	var profile accountManagementProfile
	if err := DecodeFromBytes(encoded, &profile); err != nil {
		return nil, err
	}
	if profile.Version != accountManagementProfileVersion ||
		profile.AccountID != p.accountProfile.AccountID || profile.RootFingerprint != p.accountProfile.RootFingerprint {
		return nil, fmt.Errorf("persisted account management identity changed")
	}
	key, err := p.restoreSnaclKey(profile.SecretSalt, password)
	if err != nil {
		return nil, err
	}
	secret, err := key.Decrypt(profile.SecretCipher)
	if err != nil {
		return nil, err
	}
	if len(secret) != 32 {
		zeroBytes(secret)
		return nil, fmt.Errorf("invalid account management secret")
	}
	// Keep only credential fields current; a password check must not replace
	// this Manager's pending catalog/sync state with another Manager's metadata.
	p.accountProfile.SecretCipher = append([]byte(nil), profile.SecretCipher...)
	p.accountProfile.SecretSalt = append([]byte(nil), profile.SecretSalt...)
	return secret, nil
}

func (p *Manager) clearAccountManagementSession() {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.clearAccountManagementSessionLocked()
}

func (p *Manager) clearAccountManagementSessionLocked() {
	zeroBytes(p.accountSecret)
	p.accountSecret = nil
	p.accountPassword = ""
	p.bumpAccountGenerationLocked()
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func (p *Manager) isAccountManagementRootLocked(info *WalletInfo) bool {
	return p.accountProfile != nil && info != nil && info.Wallet != nil &&
		walletFingerprint(info.Wallet) == p.accountProfile.RootFingerprint
}

func (p *Manager) accountManagementRootWalletLocked() (*WalletInfo, error) {
	if p.accountProfile == nil {
		return nil, fmt.Errorf("account management is not active")
	}
	for _, info := range p.canonicalWalletInfosLocked() {
		if info != nil && info.Wallet != nil &&
			walletFingerprint(info.Wallet) == p.accountProfile.RootFingerprint {
			return info, nil
		}
	}
	return nil, ErrAccountManagementWalletUnavailable
}

func (p *Manager) newAccountManagementDeviceID() ([]byte, error) {
	value := make([]byte, accountManagementDeviceIDSize)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return nil, err
	}
	return value, nil
}
