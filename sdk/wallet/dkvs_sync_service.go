package wallet

import (
	"context"
	"fmt"
	"strings"
	"time"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const (
	dkvsWatchTimeoutSeconds = 20
	dkvsSyncRetryDelay      = 5 * time.Second
)

func (p *Manager) dkvsReplicaNamespace() string {
	if p == nil || p.cfg == nil || p.cfg.IndexerL2 == nil {
		return ""
	}
	indexer := p.cfg.IndexerL2
	return strings.Join([]string{
		p.cfg.Env, p.cfg.Chain, indexer.Scheme, indexer.Host, indexer.Proxy,
	}, ":")
}

func (p *Manager) rgb11HeadFilters() ([]dkvsindexer.Subscription, error) {
	filters := make([]dkvsindexer.Subscription, 0)
	seen := make(map[string]struct{})
	for _, account := range p.localRGB11Accounts() {
		manager, err := p.newScopedRGB11Manager(account)
		if err != nil {
			return nil, err
		}
		walletID, err := manager.RGB11WalletID()
		if err != nil {
			return nil, err
		}
		key, err := dkvsindexer.PersonalKey(
			account.Wallet.GetPubKey().SerializeCompressed(),
			RGB11WalletHeadPath(walletID),
		)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		filters = append(filters, dkvsindexer.Subscription{
			Type: dkvsindexer.SubscriptionKey, Target: key,
		})
	}
	return filters, nil
}

func (p *Manager) flushDKVSOutbox(client *SatsNetDKVSClient, store *dkvsReplicaStore,
	scope string) (bool, error) {

	pending, err := store.loadOutbox(scope)
	if err != nil {
		return false, err
	}
	confirmed, err := store.loadConfirmed(scope)
	if err != nil {
		return false, err
	}
	confirmedByKey := make(map[string]*swire.DKVSRecord, len(confirmed))
	for _, record := range confirmed {
		confirmedByKey[record.Key] = record
	}
	submitted := false
	for _, record := range pending {
		active := confirmedByKey[record.Key]
		if active != nil && dkvsindexer.CompareRecords(active, record) >= 0 {
			if err := store.acknowledgeOutbox(scope, record); err != nil {
				return submitted, err
			}
			continue
		}
		if dkvsindexer.IsTombstone(record.Flags) {
			_, err = client.Tombstone(record)
		} else {
			_, err = client.PutRecord(record)
		}
		if err != nil {
			return submitted, err
		}
		submitted = true
	}
	return submitted, nil
}

func (p *Manager) syncDKVSOnce(ctx context.Context) ([]dkvsindexer.Subscription, string, error) {
	if p == nil || p.rgbManager == nil {
		return nil, "", fmt.Errorf("wallet manager is unavailable")
	}
	p.dkvsSyncRun.Lock()
	defer p.dkvsSyncRun.Unlock()
	filters, err := p.rgb11HeadFilters()
	if err != nil {
		return nil, "", err
	}
	if len(filters) == 0 {
		return nil, "", nil
	}
	client, err := p.rgbManager.rgb11DKVSClient()
	if err != nil {
		return nil, "", err
	}
	store := newDKVSReplicaStore(p.db)
	scope := dkvsReplicaScope(p.dkvsReplicaNamespace(), filters)
	pull := func() (string, error) {
		records, root, err := client.SyncFilteredAll(filters,
			dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())})
		if err != nil {
			return "", err
		}
		if err := store.applyConfirmed(scope, filters, records, root); err != nil {
			return "", err
		}
		return root, nil
	}
	root, err := pull()
	if err != nil {
		return filters, "", err
	}
	submitted, err := p.flushDKVSOutbox(client, store, scope)
	if err != nil {
		return filters, root, err
	}
	if submitted {
		root, err = pull()
		if err != nil {
			return filters, "", err
		}
	}
	result := p.SyncLocalRGB11State(ctx)
	for _, account := range result.Accounts {
		if account.Error != "" {
			Log.Warningf("RGB11 DKVS sync wallet=%d account=%d: %s",
				account.WalletID, account.AccountIndex, account.Error)
		}
	}
	p.dkvsSyncMu.Lock()
	callback := p.dkvsSyncCB
	p.dkvsSyncMu.Unlock()
	if callback != nil {
		callback()
	}
	return filters, root, nil
}

func (p *Manager) runDKVSBackgroundSync(ctx context.Context, done chan struct{}) {
	defer close(done)
	for {
		filters, root, err := p.syncDKVSOnce(ctx)
		if err != nil {
			Log.Warningf("DKVS background sync failed: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(dkvsSyncRetryDelay):
				continue
			}
		}
		if len(filters) == 0 || root == "" {
			select {
			case <-ctx.Done():
				return
			case <-time.After(dkvsSyncRetryDelay):
				continue
			}
		}
		client, err := p.rgbManager.rgb11DKVSClient()
		if err != nil {
			continue
		}
		watchFailed := false
		for {
			watch, watchErr := client.WatchFiltered(DKVSWatchRequest{
				Filters: filters, Root: root, TimeoutSeconds: dkvsWatchTimeoutSeconds,
			})
			if watchErr == nil && watch != nil && !watch.Changed && watch.Root == root {
				select {
				case <-ctx.Done():
					return
				default:
					continue
				}
			}
			if watchErr != nil {
				Log.Warningf("DKVS watch failed: %v", watchErr)
				watchFailed = true
			}
			break
		}
		if watchFailed {
			select {
			case <-ctx.Done():
				return
			case <-time.After(dkvsSyncRetryDelay):
			}
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

// StartDKVSBackgroundSync returns immediately. Local wallet state remains
// available while the worker reconciles the signed DKVS replica in background.
func (p *Manager) StartDKVSBackgroundSync() {
	if p == nil {
		return
	}
	p.dkvsSyncMu.Lock()
	if p.dkvsSyncStop != nil {
		p.dkvsSyncMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	p.dkvsSyncStop = cancel
	p.dkvsSyncDone = done
	p.dkvsSyncMu.Unlock()
	go p.runDKVSBackgroundSync(ctx, done)
}

func (p *Manager) SetDKVSBackgroundSyncCallback(callback func()) {
	if p == nil {
		return
	}
	p.dkvsSyncMu.Lock()
	p.dkvsSyncCB = callback
	p.dkvsSyncMu.Unlock()
}

func (p *Manager) RestartDKVSBackgroundSync() {
	p.StopDKVSBackgroundSync()
	p.StartDKVSBackgroundSync()
}

func (p *Manager) StopDKVSBackgroundSync() {
	if p == nil {
		return
	}
	p.dkvsSyncMu.Lock()
	cancel := p.dkvsSyncStop
	p.dkvsSyncStop = nil
	p.dkvsSyncDone = nil
	p.dkvsSyncMu.Unlock()
	if cancel != nil {
		cancel()
	}
}
