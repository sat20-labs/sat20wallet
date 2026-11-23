package wallet

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const dkvsAccountServiceJobPrefix = "account-services:"

func (p *Manager) rootDKVSAccountIdentity() (string, string) {
	if p == nil {
		return "", ""
	}
	root, err := p.accountManagementRootWallet()
	if err != nil || root == nil || root.GetPubKey() == nil {
		return "", ""
	}
	return dkvsindexer.AccountID(root.GetPubKey().SerializeCompressed()), root.GetAddress()
}

func (p *Manager) rootDKVSAccountID() string {
	accountID, _ := p.rootDKVSAccountIdentity()
	return accountID
}

func (p *Manager) registerRootMailbox(root common.Wallet) error {
	key, err := dkvsindexer.AccountMappingKey(GetChainParam().Name, root.GetAddress())
	if err != nil {
		return err
	}
	prefix, _, err := dkvsManagedPathForKey(key)
	if err != nil {
		return err
	}
	if err := p.ensureOwnedDKVSPrefix(prefix); err != nil {
		return err
	}
	return p.watchAccountMailbox(root)
}

func (p *Manager) scheduleRootAccountDKVSServices(accountID string) {
	accountID = strings.TrimSpace(accountID)
	if p == nil || p.dkvs == nil || accountID == "" {
		return
	}
	p.dkvs.schedule(dkvsAccountServiceJobPrefix+accountID, func(store *dkvsStore) error {
		if p.rootDKVSAccountID() != accountID {
			return nil
		}
		ctx := context.Background()
		if p.dkvs != nil {
			ctx = p.dkvs.requestContext()
		}
		_, err := p.syncRootRGB11AddressMailbox(ctx,
			dkvsindexer.RecordVerificationOptions{})
		if mailboxWaitingForAccount(err) {
			return nil
		}
		if err == nil {
			p.dkvs.markMailboxPolled(accountID)
		}
		return err
	})
}

// A module waiting for discovery, import or conflict resolution leaves its
// messages unread. DKVS receiving and independent backup jobs can continue.
func mailboxWaitingForAccount(err error) bool {
	return errors.Is(err, ErrRootAccountDiscoveryPending) ||
		errors.Is(err, ErrAccountManagedDataImportIncomplete) ||
		errors.Is(err, dkvsindexer.ErrWriteConflict)
}

func (m *dkvsManager) mailboxPollDue(accountID string) bool {
	if m == nil || accountID == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mailboxPollAccount != accountID || m.mailboxPollAt.IsZero() ||
		time.Since(m.mailboxPollAt) >= dkvsIdleSyncInterval
}

func (m *dkvsManager) markMailboxPolled(accountID string) {
	if m == nil || accountID == "" {
		return
	}
	m.mu.Lock()
	m.mailboxPollAccount = accountID
	m.mailboxPollAt = time.Now()
	m.mu.Unlock()
}

func (p *Manager) pollRootAccountMailboxIfDue(client *SatsNetDKVSClient) (bool, error) {
	if p == nil || p.dkvs == nil || client == nil {
		return false, nil
	}
	accountID := p.rootDKVSAccountID()
	mailboxPrefix, err := mailboxSubscriptionTarget(accountID)
	if err != nil || !p.dkvs.desiredPrefixContainsKey(client, mailboxPrefix) {
		return false, nil
	}
	if !p.dkvs.mailboxPollDue(accountID) {
		return false, nil
	}
	ctx := p.dkvs.requestContext()
	_, err = p.syncRootRGB11AddressMailbox(ctx,
		dkvsindexer.RecordVerificationOptions{})
	if mailboxWaitingForAccount(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	p.dkvs.markMailboxPolled(accountID)
	return true, nil
}

func (p *Manager) scheduleMailboxRefreshForChanges(paths []string) {
	accountID, address := p.rootDKVSAccountIdentity()
	if accountID == "" {
		return
	}
	mailboxPrefix, err := mailboxSubscriptionTarget(accountID)
	if err != nil {
		return
	}
	targets := []string{mailboxPrefix}
	if address != "" {
		if mappingKey, keyErr := dkvsindexer.AccountMappingKey(GetChainParam().Name, address); keyErr == nil {
			if prefix, _, pathErr := dkvsManagedPathForKey(mappingKey); pathErr == nil {
				targets = append(targets, prefix)
			}
		}
	}
	for _, path := range paths {
		for _, target := range targets {
			if path == target || strings.HasPrefix(path, target+"/") {
				p.scheduleRootAccountDKVSServices(accountID)
				return
			}
		}
	}
}
