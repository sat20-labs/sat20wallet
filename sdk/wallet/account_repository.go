package wallet

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const accountRecoveryPath = "account/recovery"

type AccountDKVSRepository struct {
	client        *SatsNetDKVSClient
	owner         common.Wallet
	autopay       DKVSAutopayOptions
	recordOptions dkvsindexer.RecordOptions
	accountID     string
}

func NewAccountDKVSRepository(client *SatsNetDKVSClient, owner common.Wallet, autopay DKVSAutopayOptions, options dkvsindexer.RecordOptions) (*AccountDKVSRepository, error) {
	if client == nil || owner == nil {
		return nil, fmt.Errorf("DKVS client and owner wallet are required")
	}
	accountID := dkvsindexer.AccountID(owner.GetPubKey().SerializeCompressed())
	if accountID == "" {
		return nil, fmt.Errorf("invalid account owner public key")
	}
	return &AccountDKVSRepository{client: client, owner: owner, autopay: autopay, recordOptions: options, accountID: accountID}, nil
}

func NewReadOnlyAccountDKVSRepository(client *SatsNetDKVSClient, accountID string) (*AccountDKVSRepository, error) {
	if client == nil {
		return nil, fmt.Errorf("DKVS client is required")
	}
	if _, err := dkvsindexer.AccountPubKey(accountID); err != nil {
		return nil, err
	}
	return &AccountDKVSRepository{client: client, accountID: accountID}, nil
}

func (r *AccountDKVSRepository) AccountID() string { return r.accountID }
func accountPath(packageID, name string) string {
	return accountRecoveryPath + "/" + packageID + "/" + name
}

func (r *AccountDKVSRepository) putJSON(path string, value any) error {
	if r.owner == nil {
		return fmt.Errorf("read-only account repository")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(encoded) > account.MaxRecoveryObjectSize {
		return fmt.Errorf("account recovery object exceeds DKVS value limit")
	}
	_, err = r.client.PutPersonalRecordWithAutopay(r.owner, path, encoded, r.recordOptions, r.autopay)
	return err
}

func (r *AccountDKVSRepository) getJSON(path string, target any) error {
	pubKey, err := dkvsindexer.AccountPubKey(r.accountID)
	if err != nil {
		return err
	}
	key, err := dkvsindexer.PersonalKey(pubKey, path)
	if err != nil {
		return err
	}
	record, err := r.client.GetVerifiedRecord(key, dkvsindexer.RecordVerificationOptions{ExpectedKey: key})
	if err != nil {
		return err
	}
	if record == nil || len(record.Value) == 0 || len(record.Value) > account.MaxRecoveryObjectSize {
		return fmt.Errorf("invalid account recovery object")
	}
	return json.Unmarshal(record.Value, target)
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

func (r *AccountDKVSRepository) SaveEnvelope(_ context.Context, value account.Envelope) error {
	if err := r.assertLocator(value.Locator); err != nil {
		return err
	}
	return r.putJSON(accountPath(value.Locator.PackageID, "envelope"), value)
}
func (r *AccountDKVSRepository) SaveDKVSShareCapsule(_ context.Context, locator account.Locator, value account.DKVSShareCapsule) error {
	if err := r.assertLocator(locator); err != nil {
		return err
	}
	return r.putJSON(accountPath(locator.PackageID, "share/dkvs"), value)
}
func (r *AccountDKVSRepository) SaveKnowledgeBundle(_ context.Context, locator account.Locator, value account.KnowledgeRecoveryBundle) error {
	if err := r.assertLocator(locator); err != nil {
		return err
	}
	return r.putJSON(accountPath(locator.PackageID, "questions"), value)
}
func (r *AccountDKVSRepository) SaveManifest(_ context.Context, value account.Manifest) error {
	if err := r.assertLocator(value.Locator); err != nil {
		return err
	}
	return r.putJSON(accountPath(value.Locator.PackageID, "manifest"), value)
}
func (r *AccountDKVSRepository) LoadEnvelope(_ context.Context, locator account.Locator) (*account.Envelope, error) {
	if err := r.assertLocator(locator); err != nil {
		return nil, err
	}
	var value account.Envelope
	if err := r.getJSON(accountPath(locator.PackageID, "envelope"), &value); err != nil {
		return nil, err
	}
	return &value, nil
}
func (r *AccountDKVSRepository) LoadDKVSShareCapsule(_ context.Context, locator account.Locator) (*account.DKVSShareCapsule, error) {
	if err := r.assertLocator(locator); err != nil {
		return nil, err
	}
	var value account.DKVSShareCapsule
	if err := r.getJSON(accountPath(locator.PackageID, "share/dkvs"), &value); err != nil {
		return nil, err
	}
	return &value, nil
}
func (r *AccountDKVSRepository) LoadKnowledgeBundle(_ context.Context, locator account.Locator) (*account.KnowledgeRecoveryBundle, error) {
	if err := r.assertLocator(locator); err != nil {
		return nil, err
	}
	var value account.KnowledgeRecoveryBundle
	if err := r.getJSON(accountPath(locator.PackageID, "questions"), &value); err != nil {
		return nil, err
	}
	return &value, nil
}
func (r *AccountDKVSRepository) LoadManifest(_ context.Context, locator account.Locator) (*account.Manifest, error) {
	if err := r.assertLocator(locator); err != nil {
		return nil, err
	}
	var value account.Manifest
	if err := r.getJSON(accountPath(locator.PackageID, "manifest"), &value); err != nil {
		return nil, err
	}
	return &value, nil
}

type AccountWalletMetadata struct {
	Name           string
	SubAccountDIDs map[uint32]string
}

func (p *Manager) ExportAccountBackup(password string, metadata map[int64]AccountWalletMetadata) (account.Backup, error) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	ids := make([]int64, 0, len(p.walletInfoMap))
	for id := range p.walletInfoMap {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	backup := account.Backup{Version: account.Version, Wallets: make([]account.WalletBackup, 0, len(ids))}
	for _, id := range ids {
		info := p.walletInfoMap[id]
		if info == nil || info.Type != WALLET_TYPE_MNEMONIC {
			return account.Backup{}, fmt.Errorf("wallet %d is not a mnemonic wallet", id)
		}
		mnemonic, err := p.loadWalletSecret(info, password)
		if err != nil {
			return account.Backup{}, err
		}
		meta := metadata[id]
		name := meta.Name
		if name == "" {
			name = fmt.Sprintf("Wallet %d", len(backup.Wallets)+1)
		}
		subAccounts := make([]account.SubAccount, info.Accounts)
		for index := 0; index < info.Accounts; index++ {
			did := meta.SubAccountDIDs[uint32(index)]
			if did == "" {
				return account.Backup{}, fmt.Errorf("wallet %d sub-account %d is missing an Ordinals DID name", id, index)
			}
			subAccounts[index] = account.SubAccount{Index: uint32(index), DID: did}
		}
		backup.Wallets = append(backup.Wallets, account.WalletBackup{Name: name, Mnemonic: mnemonic, AccountCount: uint32(info.Accounts), SubAccounts: subAccounts})
	}
	return account.NormalizeBackup(backup)
}

func (p *Manager) RestoreAccountBackup(value account.Backup, password string) error {
	backup, err := account.NormalizeBackup(value)
	if err != nil {
		return err
	}
	if p.IsWalletExist() {
		return fmt.Errorf("account restore requires an empty wallet database")
	}
	for _, item := range backup.Wallets {
		id, err := p.ImportWallet(item.Mnemonic, password)
		if err != nil {
			return err
		}
		p.mutex.Lock()
		info := p.walletInfoMap[id]
		if info == nil {
			p.mutex.Unlock()
			return fmt.Errorf("restored wallet %d was not persisted", id)
		}
		info.Accounts = int(item.AccountCount)
		err = saveWallet(p.db, &info.WalletInDB)
		p.mutex.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}
