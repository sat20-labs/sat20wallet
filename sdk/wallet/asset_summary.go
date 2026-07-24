package wallet

import (
	"context"
	"fmt"
	"sort"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	walletcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

type localRGB11Account struct {
	WalletID     int64
	AccountIndex uint32
	Address      string
	Wallet       walletcommon.Wallet
}

type LocalRGB11SyncAccountResult struct {
	WalletID     int64  `json:"wallet_id"`
	AccountIndex uint32 `json:"account_index"`
	Address      string `json:"address"`
	Activated    bool   `json:"activated"`
	Mailbox      bool   `json:"mailbox"`
	Refreshed    bool   `json:"refreshed"`
	Error        string `json:"error,omitempty"`
}

type LocalRGB11SyncResult struct {
	Accounts []*LocalRGB11SyncAccountResult `json:"accounts"`
}

func rgb11StorageScope(walletID int64, accountIndex uint32) string {
	return fmt.Sprintf("wallet-%d-account-%d-rgb11v2", walletID, accountIndex)
}

func (p *Manager) localRGB11Accounts() []localRGB11Account {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	ids := make([]int64, 0, len(p.walletInfoMap))
	for id := range p.walletInfoMap {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	result := make([]localRGB11Account, 0)
	for _, id := range ids {
		info := p.walletInfoMap[id]
		if info == nil || info.Wallet == nil {
			continue
		}
		accountCount := info.Accounts
		if accountCount < 1 {
			accountCount = 1
		}
		for index := 0; index < accountCount; index++ {
			wallet := info.Wallet.Clone()
			wallet.SetSubAccount(uint32(index))
			address := wallet.GetAddress()
			if address == "" {
				continue
			}
			result = append(result, localRGB11Account{
				WalletID:     id,
				AccountIndex: uint32(index),
				Address:      address,
				Wallet:       wallet,
			})
		}
	}
	return result
}

func (p *Manager) localRGB11AccountForAddress(address string) (localRGB11Account, bool) {
	if address == "" && p.wallet != nil {
		address = p.wallet.GetAddress()
	}
	for _, account := range p.localRGB11Accounts() {
		if account.Address == address {
			return account, true
		}
	}
	return localRGB11Account{}, false
}

func (p *Manager) scopedRGB11ProjectionStore(account localRGB11Account) (*rgb11wallet.ProjectionStore, error) {
	store := rgb11wallet.NewProjectionStore(p.db, nil)
	if err := store.SetScope(rgb11StorageScope(account.WalletID, account.AccountIndex)); err != nil {
		return nil, err
	}
	return store, nil
}

func (p *Manager) localRGB11Assets(address string) (indexer.TxAssets, int64, error) {
	account, ok := p.localRGB11AccountForAddress(address)
	if !ok {
		return nil, 0, nil
	}
	store, err := p.scopedRGB11ProjectionStore(account)
	if err != nil {
		return nil, 0, err
	}
	outputs, err := store.ListOutputs()
	if err != nil {
		return nil, 0, err
	}
	for _, output := range outputs {
		if output == nil {
			continue
		}
		for index := range output.Assets {
			if output.Assets[index].Name.Protocol == rgb11wallet.Protocol {
				if err := p.utxoLockerL1.LockUtxo(output.OutPointStr, rgb11wallet.LockReasonRGB); err != nil {
					return nil, 0, err
				}
				break
			}
		}
	}
	proofs, err := store.ListProofs()
	if err != nil {
		return nil, 0, err
	}
	proofIndex := make(map[string]struct{}, len(proofs))
	for _, proof := range proofs {
		if proof != nil {
			proofIndex[proof.OutPoint+"|"+proof.AssetName.String()] = struct{}{}
		}
	}

	var assets indexer.TxAssets
	var carrierSats int64
	for _, output := range outputs {
		if output == nil {
			continue
		}
		hasRGB11 := false
		for index := range output.Assets {
			asset := &output.Assets[index]
			if asset.Name.Protocol != rgb11wallet.Protocol {
				continue
			}
			if _, ok := proofIndex[output.OutPointStr+"|"+asset.Name.String()]; !ok {
				return nil, 0, fmt.Errorf("%w: proof missing for %s %s",
					ErrRGB11Inconsistent, output.OutPointStr, asset.Name.String())
			}
			if err := store.AssertConsistent(output.OutPointStr, asset.Name); err != nil {
				return nil, 0, fmt.Errorf("%w: %v", ErrRGB11Inconsistent, err)
			}
			if err := assets.Add(asset); err != nil {
				return nil, 0, err
			}
			hasRGB11 = true
		}
		if hasRGB11 {
			carrierSats += output.OutValue.Value
		}
	}
	return assets, carrierSats, nil
}

func cloneAssetSummary(summary *indexerwire.AssetSummary) *indexerwire.AssetSummary {
	if summary == nil {
		return nil
	}
	result := &indexerwire.AssetSummary{ListResp: summary.ListResp}
	result.Data = make([]*indexer.AssetInfo, 0, len(summary.Data))
	for _, asset := range summary.Data {
		if asset != nil {
			result.Data = append(result.Data, asset.Clone())
		}
	}
	return result
}

// GetAssetSummary returns the normal L1 Indexer summary plus validated RGB11
// allocations for local wallet addresses. RGB11 carrier sats remain part of
// the physical BTC total but are removed from spendable plain sats.
func (p *Manager) GetAssetSummary(address string) (*indexerwire.AssetSummary, error) {
	if address == "" {
		if p.wallet == nil {
			return nil, ErrRGB11WalletLocked
		}
		address = p.wallet.GetAddress()
	}
	result := cloneAssetSummary(p.l1IndexerClient.GetAssetSummaryWithAddress(address))
	if result == nil {
		return nil, fmt.Errorf("get L1 asset summary for %s failed", address)
	}

	rgbAssets, carrierSats, err := p.localRGB11Assets(address)
	if err != nil {
		Log.Errorf("merge local RGB11 summary for %s failed: %v", address, err)
		return result, nil
	}
	if carrierSats > 0 {
		for _, asset := range result.Data {
			if asset == nil || asset.Name != indexer.ASSET_PLAIN_SAT {
				continue
			}
			carrier := indexer.NewDefaultDecimal(carrierSats)
			if asset.Amount.Cmp(carrier) <= 0 {
				asset.Amount = *indexer.NewDefaultDecimal(0)
			} else {
				asset.Amount = *asset.Amount.Sub(carrier)
			}
			break
		}
	}
	for index := range rgbAssets {
		asset := rgbAssets[index].Clone()
		merged := false
		for _, current := range result.Data {
			if current != nil && current.Name == asset.Name {
				if err := current.Add(asset); err != nil {
					return nil, err
				}
				merged = true
				break
			}
		}
		if !merged {
			result.Data = append(result.Data, asset)
		}
	}
	sort.Slice(result.Data, func(i, j int) bool {
		return result.Data[i].Name.String() < result.Data[j].Name.String()
	})
	result.Total = uint64(len(result.Data))
	return result, nil
}

func (p *Manager) getLocalRGB11AssetBalance(address string, name *indexer.AssetName) (*Decimal, bool, error) {
	if name == nil || name.Protocol != rgb11wallet.Protocol {
		return nil, false, nil
	}
	account, ok := p.localRGB11AccountForAddress(address)
	if !ok {
		return nil, false, nil
	}
	store, err := p.scopedRGB11ProjectionStore(account)
	if err != nil {
		return nil, true, err
	}
	balance, err := store.Balance(*name)
	return balance, true, err
}

func (p *Manager) newScopedRGB11Manager(account localRGB11Account) (*rgb11Manager, error) {
	if account.Wallet == nil || account.Wallet.GetAddress() == "" {
		return nil, ErrRGB11WalletLocked
	}
	owner := &Manager{
		cfg:             p.cfg,
		status:          &Status{CurrentWallet: account.WalletID, CurrentAccount: account.AccountIndex},
		wallet:          account.Wallet,
		tickerInfoMap:   make(map[string]*indexer.TickerInfo),
		db:              p.db,
		http:            p.http,
		l1IndexerClient: p.l1IndexerClient,
		l2IndexerClient: p.l2IndexerClient,
		utxoLockerL1:    p.utxoLockerL1,
		utxoLockerL2:    p.utxoLockerL2,
	}
	scoped, err := newRGB11Manager(owner, p.db, p.utxoLockerL1, newIndexerBitcoinEvidenceProvider(p.l1IndexerClient))
	if err != nil {
		return nil, err
	}
	owner.rgbManager = scoped
	if err := scoped.selectRGB11Scope(); err != nil {
		return nil, err
	}
	outputs, err := scoped.projectionStore.ListOutputs()
	if err != nil {
		return nil, err
	}
	for _, output := range outputs {
		if output == nil {
			continue
		}
		for index := range output.Assets {
			name := &output.Assets[index].Name
			if name.Protocol != rgb11wallet.Protocol {
				continue
			}
			info, loadErr := loadTickerInfo(p.db, name)
			if loadErr == nil && info != nil {
				owner.tickerInfoMap[name.String()] = info
			}
		}
	}
	return scoped, nil
}

// SyncLocalRGB11State restores, receives and refreshes RGB11 data for every
// unlocked local wallet/account without changing the active wallet selection.
func (p *Manager) SyncLocalRGB11State(ctx context.Context) *LocalRGB11SyncResult {
	result := &LocalRGB11SyncResult{Accounts: make([]*LocalRGB11SyncAccountResult, 0)}
	currentWalletID := p.GetCurrentWalletId()
	currentAccount := uint32(0)
	p.mutex.RLock()
	if p.status != nil {
		currentAccount = p.status.CurrentAccount
	}
	p.mutex.RUnlock()

	for _, account := range p.localRGB11Accounts() {
		item := &LocalRGB11SyncAccountResult{
			WalletID: account.WalletID, AccountIndex: account.AccountIndex, Address: account.Address,
		}
		manager, err := p.newScopedRGB11Manager(account)
		if err != nil {
			item.Error = err.Error()
			result.Accounts = append(result.Accounts, item)
			continue
		}
		if _, err := manager.ActivateRGB11WalletState(dkvsindexer.RecordVerificationOptions{
			Now: uint64(time.Now().UnixMilli()),
		}); err != nil {
			item.Error = err.Error()
		} else {
			item.Activated = true
		}
		if _, err := manager.SyncConfiguredRGB11AddressMailbox(
			ctx,
			dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())},
			RGB11AddressDeliveryOptions{},
		); err != nil {
			if item.Error == "" {
				item.Error = err.Error()
			}
		} else {
			item.Mailbox = true
		}
		if _, err := manager.RefreshRGB11State(ctx); err != nil {
			if item.Error == "" {
				item.Error = err.Error()
			}
		} else {
			item.Refreshed = true
		}
		manager.waitForRGB11AutoBackup()
		if account.WalletID == currentWalletID && account.AccountIndex == currentAccount {
			p.mutex.Lock()
			if p.status == nil ||
				p.status.CurrentWallet != account.WalletID ||
				p.status.CurrentAccount != account.AccountIndex ||
				p.rgbManager == nil {
				p.mutex.Unlock()
				result.Accounts = append(result.Accounts, item)
				continue
			}
			p.rgbManager.head = manager.rgbManager.head
			p.rgbManager.autoBackup = manager.rgbManager.autoBackup
			p.rgbManager.dkvsStatus = manager.rgbManager.dkvsStatus
			p.rgbManager.dkvsBackupMode = manager.rgbManager.dkvsBackupMode
			p.rgbManager.dkvsBackupTTL = manager.rgbManager.dkvsBackupTTL
			p.rgbManager.consistencyStatus = manager.rgbManager.consistencyStatus
			p.mutex.Unlock()
		}
		result.Accounts = append(result.Accounts, item)
	}
	return result
}
