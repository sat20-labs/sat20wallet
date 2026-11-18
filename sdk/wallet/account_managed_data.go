package wallet

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/sat20-labs/sat20wallet/sdk/account"
)

const (
	AccountManagedDataGlobalScope = "global"
)

// AccountManagedDataScope is defined by account management and handed to
// providers. Modules do not discover or mutate the wallet/account catalog.
type AccountManagedDataScope struct {
	Type              account.WalletType
	WalletID          int64
	WalletFingerprint string
	AccountIndex      uint32
	Network           string
}

func (s AccountManagedDataScope) ID() string {
	return strings.ToLower(strings.TrimSpace(s.Network)) + "/" +
		strings.ToLower(strings.TrimSpace(s.WalletFingerprint)) + "/" +
		strconv.FormatUint(uint64(s.AccountIndex), 10)
}

type AccountManagedDataCatalog struct {
	AccountID string
	Network   string
	Scopes    []AccountManagedDataScope
}

type AccountManagedDataPayload struct {
	Scope   string
	Payload []byte
}

// AccountManagedDataProvider lets any wallet module contribute only the data
// that is necessary for cross-device recovery. Account management owns
// encryption, DKVS storage, retention, AUTOPAY, synchronization and deletion.
type AccountManagedDataProvider interface {
	ID() string
	Export(AccountManagedDataCatalog) ([]AccountManagedDataPayload, error)
	Validate(AccountManagedDataCatalog, []AccountManagedDataPayload) error
	Import(AccountManagedDataCatalog, []AccountManagedDataPayload) error
}

func validAccountManagedProviderID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return false
	}
	for _, ch := range value {
		if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' ||
			ch == '-' || ch == '_' || ch == '.' {
			continue
		}
		return false
	}
	return true
}

func (p *Manager) RegisterAccountManagedDataProvider(provider AccountManagedDataProvider) error {
	if p == nil || provider == nil {
		return fmt.Errorf("account-managed data provider is required")
	}
	id := strings.TrimSpace(provider.ID())
	if !validAccountManagedProviderID(id) {
		return fmt.Errorf("invalid account-managed data provider %q", id)
	}
	p.managedDataMu.Lock()
	defer p.managedDataMu.Unlock()
	if p.managedDataProviders == nil {
		p.managedDataProviders = make(map[string]AccountManagedDataProvider)
	}
	if _, exists := p.managedDataProviders[id]; exists {
		return fmt.Errorf("account-managed data provider %q is already registered", id)
	}
	p.managedDataProviders[id] = provider
	return nil
}

func (p *Manager) accountManagedDataProviders() []AccountManagedDataProvider {
	if p == nil {
		return nil
	}
	p.managedDataMu.RLock()
	ids := make([]string, 0, len(p.managedDataProviders))
	for id := range p.managedDataProviders {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	providers := make([]AccountManagedDataProvider, 0, len(ids))
	for _, id := range ids {
		providers = append(providers, p.managedDataProviders[id])
	}
	p.managedDataMu.RUnlock()
	return providers
}

func (p *Manager) accountManagedDataCatalog() (AccountManagedDataCatalog, error) {
	if p == nil {
		return AccountManagedDataCatalog{}, fmt.Errorf("wallet manager is unavailable")
	}
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.accountManagedCatalogLocked()
}

// Caller owns the Manager read/write gate.
func (p *Manager) accountManagedCatalogLocked() (AccountManagedDataCatalog, error) {
	accountID := ""
	if p.accountProfile != nil {
		accountID = p.accountProfile.AccountID
	} else {
		root, err := p.accountManagementCandidateRootLocked()
		if err != nil {
			return AccountManagedDataCatalog{}, err
		}
		accountID, err = dkvsAccountID(cloneWalletAtAccountZero(root.Wallet))
		if err != nil {
			return AccountManagedDataCatalog{}, err
		}
	}
	catalog := AccountManagedDataCatalog{AccountID: accountID, Network: _chain}
	for _, info := range p.canonicalWalletInfosLocked() {
		if info == nil || info.Wallet == nil || (info.Type != WALLET_TYPE_MNEMONIC && info.Type != WALLET_TYPE_PRIVKEY) {
			continue
		}
		var typ account.WalletType
		switch info.Type {
		case WALLET_TYPE_MNEMONIC:
			typ = account.WalletMnemonic
		case WALLET_TYPE_PRIVKEY:
			typ = account.WalletPrivateKey
		default:
			return AccountManagedDataCatalog{}, fmt.Errorf("unsupported managed wallet type")
		}
		fingerprint := walletFingerprint(info.Wallet)
		for index := uint32(0); index < uint32(info.Accounts); index++ {
			catalog.Scopes = append(catalog.Scopes, AccountManagedDataScope{
				Type: typ, WalletID: info.Id, WalletFingerprint: fingerprint,
				AccountIndex: index, Network: _chain,
			})
		}
	}
	sort.Slice(catalog.Scopes, func(i, j int) bool { return catalog.Scopes[i].ID() < catalog.Scopes[j].ID() })
	return catalog, nil
}

func accountManagedScopeSet(catalog AccountManagedDataCatalog) map[string]struct{} {
	result := map[string]struct{}{AccountManagedDataGlobalScope: {}}
	for _, scope := range catalog.Scopes {
		result[scope.ID()] = struct{}{}
	}
	return result
}
