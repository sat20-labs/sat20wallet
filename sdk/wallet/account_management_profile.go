package wallet

import (
	"crypto/rand"
	"fmt"
	"io"
	"strings"

	"github.com/sat20-labs/sat20wallet/sdk/account"
)

const (
	accountManagementProfileVersion = uint32(2)
	accountManagementProfileDBKey   = "account-management-profile-v2"
	accountManagementDeviceIDSize   = 16
)

type accountManagementProfile struct {
	Version         uint32
	RootFingerprint string
	AccountID       string
	PackageID       string
	RecoveryMode    account.RecoveryMode
	StorageMode     string
	Location        AccountIndexerLocation
	RecordTTL       uint64
	AutopayContract string
	PublicLocator   string
	LastRehearsalAt int64
	SecretCipher    []byte
	SecretSalt      []byte
	DeviceID        []byte
	StateSeq        uint64
	StateHash       string
	StateEnvelope   []byte
	Pending         []accountManagementMutation
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
	p.accountProfile = nil
	encoded, err := p.db.Read(accountManagementProfileKey())
	if err != nil || len(encoded) == 0 {
		return nil
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
	if p.accountProfile == nil {
		return nil
	}
	key, err := p.restoreSnaclKey(p.accountProfile.SecretSalt, password)
	if err != nil {
		return err
	}
	secret, err := key.Decrypt(p.accountProfile.SecretCipher)
	if err != nil {
		return err
	}
	if len(secret) != 32 {
		zeroBytes(secret)
		return fmt.Errorf("invalid account management secret")
	}
	zeroBytes(p.accountSecret)
	p.accountSecret = secret
	p.accountPassword = password
	return nil
}

func (p *Manager) clearAccountManagementSession() {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	zeroBytes(p.accountSecret)
	p.accountSecret = nil
	p.accountPassword = ""
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
	return nil, fmt.Errorf("account management wallet is unavailable")
}

func (p *Manager) newAccountManagementDeviceID() ([]byte, error) {
	value := make([]byte, accountManagementDeviceIDSize)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return nil, err
	}
	return value, nil
}
