package wallet

import (
	"errors"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	"strings"

	indexercommon "github.com/sat20-labs/indexer/common"
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
	if p == nil || p.IsServerMode() || p.ensureDKVSManager() == nil {
		return nil
	}
	if p.cfg == nil || p.cfg.IndexerL2 == nil || strings.TrimSpace(p.cfg.IndexerL2.Host) == "" {
		return nil
	}
	keys := make([]string, 0, 4)
	p.mutex.RLock()
	accountActive := p.accountProfile != nil && len(p.accountSecret) == 32
	configured := p.accountProfile != nil && p.accountProfile.RecoveryConfigured
	root, rootErr := p.accountDiscoveryRootLocked()
	p.mutex.RUnlock()
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
	stateKey, err := p.accountManagedStateKey(root)
	if err != nil {
		return err
	}
	dataKey, err := account.ManagedIndexKey(accountID)
	if err != nil {
		return err
	}
	wrapperKey, err := accountRootWrapperKey(root)
	if err != nil {
		return err
	}
	keys = append(keys, stateKey, dataKey, wrapperKey)
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
	if configured {
		return p.registerRootMailbox(root)
	}
	return nil
}
