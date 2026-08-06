package wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const (
	AccountManagedDataGlobalScope = "global"
	accountManagedDataBlobName    = "account-managed-data"
)

// AccountManagedDataScope is defined by account management and handed to
// providers. Modules do not discover or mutate the wallet/account catalog.
type AccountManagedDataScope struct {
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
		if info == nil || info.Wallet == nil || info.Type != WALLET_TYPE_MNEMONIC {
			continue
		}
		fingerprint := walletFingerprint(info.Wallet)
		for index := uint32(0); index < uint32(info.Accounts); index++ {
			catalog.Scopes = append(catalog.Scopes, AccountManagedDataScope{
				WalletID: info.Id, WalletFingerprint: fingerprint,
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

func accountManagedDataContentHash(items []account.ManagedDataItem) (string, error) {
	bundle := account.ManagedDataBundle{Version: account.ManagedDataBundleVersion, Revision: 1, Items: items}
	encoded, err := account.EncodeManagedDataBundle(bundle)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

func (p *Manager) exportAccountManagedData(catalog AccountManagedDataCatalog,
	revision uint64) (account.ManagedDataBundle, string, error) {

	if revision == 0 {
		revision = 1
	}
	allowed := accountManagedScopeSet(catalog)
	items := make([]account.ManagedDataItem, 0)
	for _, provider := range p.accountManagedDataProviders() {
		id := strings.TrimSpace(provider.ID())
		payloads, err := provider.Export(catalog)
		if err != nil {
			return account.ManagedDataBundle{}, "", fmt.Errorf("export %s account-managed data: %w", id, err)
		}
		if err := provider.Validate(catalog, payloads); err != nil {
			return account.ManagedDataBundle{}, "", fmt.Errorf("validate exported %s account-managed data: %w", id, err)
		}
		seen := make(map[string]struct{}, len(payloads))
		for _, payload := range payloads {
			scope := strings.TrimSpace(payload.Scope)
			if _, ok := allowed[scope]; !ok || len(payload.Payload) == 0 {
				return account.ManagedDataBundle{}, "", fmt.Errorf("provider %s exported invalid scope %q", id, scope)
			}
			if _, duplicate := seen[scope]; duplicate {
				return account.ManagedDataBundle{}, "", fmt.Errorf("provider %s exported duplicate scope %q", id, scope)
			}
			seen[scope] = struct{}{}
			items = append(items, account.ManagedDataItem{
				Provider: id, Scope: scope, Payload: append([]byte(nil), payload.Payload...),
			})
		}
	}
	bundle, err := account.NormalizeManagedDataBundle(account.ManagedDataBundle{
		Version: account.ManagedDataBundleVersion, Revision: revision, Items: items,
	})
	if err != nil {
		return account.ManagedDataBundle{}, "", err
	}
	hash, err := accountManagedDataContentHash(bundle.Items)
	return bundle, hash, err
}

func (p *Manager) importAccountManagedData(catalog AccountManagedDataCatalog,
	bundle account.ManagedDataBundle) error {

	bundle, err := account.NormalizeManagedDataBundle(bundle)
	if err != nil {
		return err
	}
	allowed := accountManagedScopeSet(catalog)
	byProvider := make(map[string][]AccountManagedDataPayload)
	for _, item := range bundle.Items {
		if _, ok := allowed[item.Scope]; !ok {
			return fmt.Errorf("account-managed data references unknown scope %q", item.Scope)
		}
		byProvider[item.Provider] = append(byProvider[item.Provider], AccountManagedDataPayload{
			Scope: item.Scope, Payload: append([]byte(nil), item.Payload...),
		})
	}
	providers := p.accountManagedDataProviders()
	known := make(map[string]AccountManagedDataProvider, len(providers))
	for _, provider := range providers {
		id := strings.TrimSpace(provider.ID())
		known[id] = provider
	}
	for id := range byProvider {
		if _, ok := known[id]; !ok {
			return fmt.Errorf("account-managed data provider %q is unavailable", id)
		}
	}
	// Validate every provider before the first import. This prevents an invalid
	// or unavailable later provider from leaving earlier modules partially
	// updated.
	for _, provider := range providers {
		id := strings.TrimSpace(provider.ID())
		if err := provider.Validate(catalog, byProvider[id]); err != nil {
			return fmt.Errorf("validate imported %s account-managed data: %w", id, err)
		}
	}
	for _, provider := range providers {
		id := strings.TrimSpace(provider.ID())
		if err := provider.Import(catalog, byProvider[id]); err != nil {
			return fmt.Errorf("import %s account-managed data: %w", id, err)
		}
	}
	return nil
}

func (p *Manager) markAccountManagedDataDirty(_ string) {
	if p == nil {
		return
	}
	p.mutex.Lock()
	if p.accountProfile != nil {
		p.accountProfile.ManagedDataDirty = true
		p.accountProfile.ManagedDataGeneration++
		if p.accountProfile.ManagedDataGeneration == 0 {
			p.accountProfile.ManagedDataGeneration = 1
		}
		if err := p.saveAccountManagementProfileLocked(); err != nil {
			Log.Warningf("save account-managed data dirty state failed: %v", err)
		}
	}
	p.mutex.Unlock()
	p.markDKVSStateDirty()
}

func (p *Manager) accountManagedDataBlobKey(root common.Wallet) (string, error) {
	accountID, err := dkvsAccountID(root)
	if err != nil {
		return "", err
	}
	return dkvsindexer.BlobKey(accountID, accountManagedDataBlobName)
}

func accountManagedDataMutation(profile *accountManagementProfile, root common.Wallet,
	key string, envelope []byte) (dkvsValueMutation, error) {

	value, err := EncodeDKVSBlobValue(envelope, nil)
	if err != nil {
		return dkvsValueMutation{}, err
	}
	return accountStateMutation(profile, root, key, value)
}

func accountManagedDataCatalogFromState(accountID, network string,
	state account.ManagedState) AccountManagedDataCatalog {

	catalog := AccountManagedDataCatalog{AccountID: accountID, Network: network}
	for _, wallet := range state.Wallets {
		if wallet.Deleted {
			continue
		}
		for index := uint32(0); index < wallet.AccountCount; index++ {
			catalog.Scopes = append(catalog.Scopes, AccountManagedDataScope{
				WalletFingerprint: wallet.Fingerprint,
				AccountIndex:      index,
				Network:           network,
			})
		}
	}
	sort.Slice(catalog.Scopes, func(i, j int) bool {
		return catalog.Scopes[i].ID() < catalog.Scopes[j].ID()
	})
	return catalog
}

func accountManagedDataItemMap(bundle account.ManagedDataBundle) map[string]account.ManagedDataItem {
	result := make(map[string]account.ManagedDataItem, len(bundle.Items))
	for _, item := range bundle.Items {
		key := item.Provider + "\x00" + item.Scope
		result[key] = account.ManagedDataItem{
			Provider: item.Provider,
			Scope:    item.Scope,
			Payload:  append([]byte(nil), item.Payload...),
		}
	}
	return result
}

func managedDataItemEqual(left account.ManagedDataItem, leftOK bool,
	right account.ManagedDataItem, rightOK bool) bool {

	return leftOK == rightOK && (!leftOK || (left.Provider == right.Provider &&
		left.Scope == right.Scope && bytes.Equal(left.Payload, right.Payload)))
}

// mergeAccountManagedDataBundles performs an opaque three-way merge per
// provider/scope. Independent module scopes merge automatically. Concurrent
// edits to the same provider/scope fail closed and must be retried after the
// owning module refreshes its local recovery state.
func mergeAccountManagedDataBundles(base, remote, local account.ManagedDataBundle,
	catalog AccountManagedDataCatalog, revision uint64) (account.ManagedDataBundle, error) {

	if revision == 0 {
		return account.ManagedDataBundle{}, fmt.Errorf("account-managed data revision is required")
	}
	allowed := accountManagedScopeSet(catalog)
	baseMap := accountManagedDataItemMap(base)
	remoteMap := accountManagedDataItemMap(remote)
	localMap := accountManagedDataItemMap(local)
	keys := make(map[string]struct{}, len(baseMap)+len(remoteMap)+len(localMap))
	for key := range baseMap {
		keys[key] = struct{}{}
	}
	for key := range remoteMap {
		keys[key] = struct{}{}
	}
	for key := range localMap {
		keys[key] = struct{}{}
	}
	items := make([]account.ManagedDataItem, 0, len(keys))
	for key := range keys {
		baseItem, baseOK := baseMap[key]
		remoteItem, remoteOK := remoteMap[key]
		localItem, localOK := localMap[key]
		candidate := account.ManagedDataItem{}
		candidateOK := false
		switch {
		case managedDataItemEqual(localItem, localOK, baseItem, baseOK):
			candidate, candidateOK = remoteItem, remoteOK
		case managedDataItemEqual(remoteItem, remoteOK, baseItem, baseOK):
			candidate, candidateOK = localItem, localOK
		case managedDataItemEqual(localItem, localOK, remoteItem, remoteOK):
			candidate, candidateOK = localItem, localOK
		default:
			return account.ManagedDataBundle{}, dkvsindexer.ErrWriteConflict
		}
		if !candidateOK {
			continue
		}
		if _, ok := allowed[candidate.Scope]; !ok {
			// Removing a wallet or subaccount from the catalog removes all module
			// payloads under that scope in the next authoritative bundle.
			continue
		}
		items = append(items, account.ManagedDataItem{
			Provider: candidate.Provider,
			Scope:    candidate.Scope,
			Payload:  append([]byte(nil), candidate.Payload...),
		})
	}
	return account.NormalizeManagedDataBundle(account.ManagedDataBundle{
		Version:  account.ManagedDataBundleVersion,
		Revision: revision,
		Items:    items,
	})
}

func emptyAccountManagedDataBundle(revision uint64) account.ManagedDataBundle {
	if revision == 0 {
		revision = 1
	}
	return account.ManagedDataBundle{
		Version:  account.ManagedDataBundleVersion,
		Revision: revision,
	}
}

func openProfileManagedDataBundle(profile accountManagementProfile,
	secret []byte) (account.ManagedDataBundle, error) {

	if profile.ManagedDataRevision == 0 {
		return emptyAccountManagedDataBundle(1), nil
	}
	if len(profile.ManagedDataEnvelope) == 0 {
		return account.ManagedDataBundle{}, fmt.Errorf("local account-managed data envelope is unavailable")
	}
	bundle, err := account.OpenManagedDataBundle(secret, profile.AccountID,
		profile.ManagedDataEnvelope)
	if err != nil {
		return account.ManagedDataBundle{}, err
	}
	hash, err := accountManagedDataContentHash(bundle.Items)
	if err != nil || bundle.Revision != profile.ManagedDataRevision ||
		hash != profile.ManagedDataHash {
		return account.ManagedDataBundle{}, fmt.Errorf("local account-managed data reference is inconsistent")
	}
	return bundle, nil
}
