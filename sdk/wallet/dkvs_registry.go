package wallet

import (
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// refreshDKVSRegistrations collects logical keys from wallet features. It does
// not synchronize them; dkvsManager remains the only owner of that lifecycle.
func (p *Manager) refreshDKVSRegistrations() error {
	if p == nil || p.dkvs == nil {
		return nil
	}
	keys := make([]string, 0)
	directories := make([]string, 0)
	for _, item := range p.localRGB11Accounts() {
		if item.Wallet == nil || item.Wallet.GetPubKey() == nil {
			continue
		}
		manager, err := p.newScopedRGB11Manager(item)
		if err != nil {
			return err
		}
		walletID, err := manager.RGB11WalletID()
		if err != nil {
			return err
		}
		pubkey := item.Wallet.GetPubKey().SerializeCompressed()
		headKey, err := dkvsindexer.PersonalKey(pubkey, RGB11WalletHeadPath(walletID))
		if err != nil {
			return err
		}
		snapshotKey, err := dkvsindexer.BlobKey(
			dkvsindexer.AccountID(pubkey), RGB11WalletSnapshotBlobKey(walletID),
		)
		if err != nil {
			return err
		}
		keys = append(keys, headKey, snapshotKey)
		directories = append(directories,
			"/mail/"+dkvsindexer.AccountID(pubkey)+"/msg",
		)
	}

	p.mutex.RLock()
	accountActive := p.accountProfile != nil && len(p.accountSecret) == 32
	p.mutex.RUnlock()
	if accountActive {
		root, err := p.accountManagementRootWallet()
		if err != nil {
			return err
		}
		key, err := p.accountManagedStateKey(root)
		if err != nil {
			return err
		}
		keys = append(keys, key)
	}
	p.dkvs.rememberPaths(keys)
	p.dkvs.rememberDirectories(directories)
	p.dkvs.wakeSync()
	return nil
}
