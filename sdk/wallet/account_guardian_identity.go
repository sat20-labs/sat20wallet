package wallet

import (
	"crypto/ecdh"
	"encoding/base64"
	"fmt"

	"github.com/sat20-labs/sat20wallet/sdk/account"
)

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

func (p *Manager) accountGuardianPrivateKeyLocked(password string) ([]byte, string, error) {
	var info *WalletInfo
	var err error
	if p.accountProfile == nil {
		info, err = p.accountManagementCandidateRootLocked()
	} else {
		info, err = p.accountManagementRootWalletLocked()
	}
	if err != nil {
		return nil, "", err
	}
	// Authenticate against the durable wallet credentials even while unlocked.
	secret, err := p.loadWalletSecretBytes(info, password)
	zeroBytes(secret)
	if err != nil {
		return nil, "", err
	}
	root := cloneWalletAtAccountZero(info.Wallet)
	accountID, err := dkvsAccountID(root)
	if err != nil {
		return nil, "", err
	}
	privateKey, err := deriveAccountGuardianPrivateKey(root, GetChainParam_SatsNet().Name, accountID)
	return privateKey, accountID, err
}

func (p *Manager) GetOrCreateAccountGuardianIdentity(password string) (*AccountGuardianIdentity, error) {
	if p == nil || p.db == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	privateKey, accountID, err := p.accountGuardianPrivateKeyLocked(password)
	if err != nil {
		return nil, err
	}
	defer zeroWalletBytes(privateKey)
	key, err := ecdh.X25519().NewPrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("invalid guardian recovery key")
	}
	return &AccountGuardianIdentity{Version: account.Version, Network: GetChainParam_SatsNet().Name, MailboxID: accountID,
		PublicKey: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())}, nil
}

func (p *Manager) LoadAccountGuardianPrivateKey(password string) ([]byte, error) {
	if p == nil || p.db == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	privateKey, _, err := p.accountGuardianPrivateKeyLocked(password)
	return privateKey, err
}
