package wallet

import (
	"fmt"
	"strings"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const accountRecoveryPath = "account/recovery"

type AccountDKVSRepository struct {
	store         *dkvsStore
	owner         common.Wallet
	autopay       DKVSAutopayOptions
	recordOptions dkvsindexer.RecordOptions
	accountID     string
}

func newAccountDKVSRepository(store *dkvsStore, owner common.Wallet, autopay DKVSAutopayOptions, options dkvsindexer.RecordOptions) (*AccountDKVSRepository, error) {
	if store == nil || owner == nil {
		return nil, fmt.Errorf("DKVS store and owner wallet are required")
	}
	accountID := dkvsindexer.AccountID(owner.GetPubKey().SerializeCompressed())
	if accountID == "" {
		return nil, fmt.Errorf("invalid account owner public key")
	}
	return &AccountDKVSRepository{store: store, owner: owner, autopay: autopay, recordOptions: options, accountID: accountID}, nil
}

func newReadOnlyAccountDKVSRepository(store *dkvsStore, accountID string) (*AccountDKVSRepository, error) {
	if store == nil {
		return nil, fmt.Errorf("DKVS store is required")
	}
	if _, err := dkvsindexer.AccountPubKey(accountID); err != nil {
		return nil, err
	}
	return &AccountDKVSRepository{store: store, accountID: accountID}, nil
}

func (r *AccountDKVSRepository) AccountID() string { return r.accountID }
func accountPackagePath(packageID string) string {
	return accountRecoveryPath + "/" + packageID
}

func (r *AccountDKVSRepository) assertLocator(locator account.Locator) error {
	if err := account.ValidateLocator(locator); err != nil {
		return err
	}
	if locator.AccountID != r.accountID {
		return fmt.Errorf("locator does not belong to repository account")
	}
	return nil
}

type AccountWalletMetadata struct {
	Name           string
	SubAccountDIDs map[uint32]string
}

func (p *Manager) ExportAccountBackup(password string, metadata map[int64]AccountWalletMetadata) (account.Backup, error) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	infos := p.canonicalWalletInfosLocked()
	backup := account.Backup{Version: account.BackupVersion, Wallets: make([]account.WalletBackup, 0, len(infos))}
	for _, info := range infos {
		if info == nil {
			return account.Backup{}, fmt.Errorf("wallet is unavailable")
		}
		id := info.Id
		localSecret, err := p.loadWalletSecretBytes(info, password)
		if err != nil {
			return account.Backup{}, err
		}
		typ, material, err := accountRecordMaterial(info.Type, localSecret)
		zeroBytes(localSecret)
		if err != nil {
			return account.Backup{}, err
		}
		meta := metadata[id]
		name := meta.Name
		if name == "" {
			name = strings.TrimSpace(info.Name)
		}
		if name == "" {
			name = fmt.Sprintf("Wallet %d", len(backup.Wallets)+1)
		}
		subAccounts := make([]account.SubAccount, info.Accounts)
		var spEnabled []uint32
		for index := 0; index < info.Accounts; index++ {
			if info.SPEnabled[uint32(index)] {
				spEnabled = append(spEnabled, uint32(index))
			}
			did := meta.SubAccountDIDs[uint32(index)]
			if did == "" {
				did = info.AccountDIDs[uint32(index)]
			}
			accountName := info.AccountNames[uint32(index)]
			if strings.TrimSpace(accountName) == "" {
				accountName = defaultAccountName(uint32(index))
			}
			subAccounts[index] = account.SubAccount{
				Index: uint32(index), Name: accountName, DID: did,
			}
		}
		backup.Wallets = append(backup.Wallets, account.WalletBackup{Name: name, Type: typ, Secret: material, AccountCount: uint32(info.Accounts), SubAccounts: subAccounts, SPEnabled: spEnabled})
	}
	return account.NormalizeBackup(backup)
}

func (p *Manager) RestoreAccountBackup(value account.Backup, password string) error {
	_, err := p.RestoreAccountBackupWithResult(value, password)
	return err
}
