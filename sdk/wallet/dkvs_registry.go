package wallet

import (
	"errors"
	"strings"

	indexercommon "github.com/sat20-labs/indexer/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// Registering the same root-owned prefix is a local no-op. In particular a
// wallet/account selection refresh must not queue behind an unrelated remote
// PUT holding the DKVS transport coordinator. Genuine registration changes
// still go through the normal serialized subscription operation.
func (p *Manager) ensureOwnedDKVSPrefix(prefix string) error {
	if p == nil || p.db == nil {
		return ErrDKVSPathNotSynced
	}
	registered, err := newDKVSReplicaStore(p.db).LoadRegisteredPrefixes(p.dkvsReplicaNamespace())
	if err != nil && !errors.Is(err, indexercommon.ErrKeyNotFound) {
		return err
	}
	for _, current := range registered {
		if current == prefix {
			return nil
		}
	}
	return p.SubscribeDKVSPrefix(prefix)
}

// refreshDKVSRegistrations explicitly registers account-management-owned
// prefixes in the durable Wallet prefix registry. Ordinary Get/Put operations
// never remember a prefix implicitly.
func (p *Manager) refreshDKVSRegistrations() error {
	if p == nil || p.ensureDKVSManager() == nil {
		return nil
	}
	if p.cfg == nil || p.cfg.IndexerL2 == nil || strings.TrimSpace(p.cfg.IndexerL2.Host) == "" {
		return nil
	}
	keys := make([]string, 0, 4)
	p.mutex.RLock()
	accountActive := p.accountProfile != nil && len(p.accountSecret) == 32
	p.mutex.RUnlock()
	root, rootErr := p.accountManagementRootWallet()
	if rootErr != nil {
		if accountActive {
			return rootErr
		}
		return nil
	}
	accountID, err := dkvsAccountID(root)
	if err != nil {
		return err
	}
	address := root.GetAddress()
	if accountActive {
		stateKey, err := p.accountManagedStateKey(root)
		if err != nil {
			return err
		}
		dataKey, err := p.accountManagedDataBlobKey(root)
		if err != nil {
			return err
		}
		wrapperKey, err := accountRootWrapperKey(root)
		if err != nil {
			return err
		}
		keys = append(keys, stateKey, dataKey, wrapperKey)
	}
	seen := make(map[string]struct{}, len(keys)+1)
	for _, key := range keys {
		prefix, _, err := dkvsManagedPathForKey(key)
		if err != nil {
			return err
		}
		if _, exists := seen[prefix]; exists {
			continue
		}
		seen[prefix] = struct{}{}
		if err := p.ensureOwnedDKVSPrefix(prefix); err != nil {
			return err
		}
	}
	if accountID != "" {
		mailboxPrefix, err := mailboxSubscriptionTarget(accountID)
		if err != nil {
			return err
		}
		if err := p.ensureOwnedDKVSPrefix(mailboxPrefix); err != nil {
			return err
		}
		// The one root-owned account service record carries both the public
		// address mapping and small service capabilities.
		if address != "" {
			mappingKey, err := dkvsindexer.AccountMappingKey(GetChainParam().Name, address)
			if err != nil {
				return err
			}
			prefix, _, err := dkvsManagedPathForKey(mappingKey)
			if err != nil {
				return err
			}
			if _, exists := seen[prefix]; !exists {
				seen[prefix] = struct{}{}
				if err := p.ensureOwnedDKVSPrefix(prefix); err != nil {
					return err
				}
			}
		}
		p.scheduleRootAccountDKVSServices(accountID)
	}
	return nil
}
