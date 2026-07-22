package wallet

import (
	"crypto/ecdh"
	"encoding/base64"
	"fmt"

	"github.com/sat20-labs/sat20wallet/sdk/account"
)

const accountGuardianIdentityPrefix = "account-guardian-identity-"

type accountGuardianIdentityRecord struct {
	Version    uint32
	Ciphertext []byte
	Salt       []byte
}

type AccountGuardianIdentity struct {
	Version   uint32 `json:"version"`
	Network   string `json:"network"`
	MailboxID string `json:"mailbox_id"`
	PublicKey string `json:"recovery_public_key"`
}

func zeroWalletBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func accountGuardianIdentityKey(accountID string) []byte {
	return []byte(accountGuardianIdentityPrefix + accountID)
}

func (p *Manager) GetOrCreateAccountGuardianIdentity(password string) (*AccountGuardianIdentity, error) {
	if p == nil || p.wallet == nil || p.db == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	accountID, err := dkvsAccountID(p.wallet)
	if err != nil {
		return nil, err
	}
	privateKey, err := p.loadAccountGuardianPrivateKey(password, accountID)
	if err != nil {
		if _, readErr := p.db.Read(accountGuardianIdentityKey(accountID)); readErr == nil {
			return nil, err
		}
		privateKey, _, err = account.GenerateGuardianKey(nil)
		if err != nil {
			return nil, err
		}
		defer zeroWalletBytes(privateKey)
		key, err := p.newSnaclKey(password)
		if err != nil {
			return nil, err
		}
		ciphertext, err := key.Encrypt(privateKey)
		if err != nil {
			return nil, err
		}
		record := accountGuardianIdentityRecord{Version: account.Version, Ciphertext: ciphertext, Salt: key.Marshal()}
		encoded, err := EncodeToBytes(record)
		if err != nil {
			return nil, err
		}
		if err := p.db.Write(accountGuardianIdentityKey(accountID), encoded); err != nil {
			return nil, err
		}
	} else {
		defer zeroWalletBytes(privateKey)
	}
	curve := ecdh.X25519()
	key, err := curve.NewPrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("invalid guardian recovery key")
	}
	return &AccountGuardianIdentity{
		Version: account.Version, Network: GetChainParam_SatsNet().Name, MailboxID: accountID,
		PublicKey: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
	}, nil
}

func (p *Manager) loadAccountGuardianPrivateKey(password, accountID string) ([]byte, error) {
	encoded, err := p.db.Read(accountGuardianIdentityKey(accountID))
	if err != nil {
		return nil, err
	}
	var record accountGuardianIdentityRecord
	if err := DecodeFromBytes(encoded, &record); err != nil {
		return nil, err
	}
	if record.Version != account.Version || len(record.Ciphertext) == 0 || len(record.Salt) == 0 {
		return nil, fmt.Errorf("invalid guardian identity record")
	}
	key, err := p.restoreSnaclKey(record.Salt, password)
	if err != nil {
		return nil, err
	}
	privateKey, err := key.Decrypt(record.Ciphertext)
	if err != nil {
		return nil, err
	}
	if len(privateKey) != 32 {
		zeroWalletBytes(privateKey)
		return nil, fmt.Errorf("invalid guardian recovery key")
	}
	return privateKey, nil
}

func (p *Manager) LoadAccountGuardianPrivateKey(password string) ([]byte, error) {
	if p == nil || p.wallet == nil || p.db == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	accountID, err := dkvsAccountID(p.wallet)
	if err != nil {
		return nil, err
	}
	return p.loadAccountGuardianPrivateKey(password, accountID)
}
