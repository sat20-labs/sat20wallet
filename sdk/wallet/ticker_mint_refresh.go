package wallet

import (
	"fmt"
	"strings"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

const tickerMintRefreshInterval = 30 * time.Second

type tickerMintRefresh struct {
	checkedAt time.Time
	info      *indexer.TickerInfo
}

// These L1 indexers return lower-case tickers even when queried with
// different casing. Use the same cache key for both forms.
func tickerLookupName(name *indexer.AssetName) *indexer.AssetName {
	if name == nil {
		return nil
	}
	switch name.Protocol {
	case indexer.PROTOCOL_NAME_ORDX, indexer.PROTOCOL_NAME_BRC20, indexer.PROTOCOL_NAME_ATOM:
		lower := strings.ToLower(name.Ticker)
		if lower != name.Ticker {
			canonical := *name
			canonical.Ticker = lower
			return &canonical
		}
	}
	return name
}

// RefreshTickerMintInfo is only for an explicit request for fresh mint totals.
// A failed refresh returns nil, not a stale value. Ordinary GetTickerInfo
// calls use the cache and never trigger this refresh policy.
func (p *Manager) RefreshTickerMintInfo(name *indexer.AssetName) *indexer.TickerInfo {
	if p == nil || name == nil {
		return nil
	}
	if name.Protocol == rgb11wallet.Protocol || *name == ASSET_PLAIN_SAT || *name == indexer.ASSET_ALL_SAT {
		return p.GetTickerInfo(name)
	}

	name = tickerLookupName(name)
	key := name.String()
	p.mutex.RLock()
	epoch := p.tickerCacheEpoch
	p.mutex.RUnlock()
	value, _, _ := p.tickerMintRefreshGroup.Do(fmt.Sprintf("%d:%s", epoch, key), func() (any, error) {
		p.mutex.RLock()
		info, recent := p.tickerInfoMap[key], p.tickerMintRefresh[key]
		p.mutex.RUnlock()
		if info == nil && p.db != nil {
			info, _ = loadTickerInfo(p.db, name)
		}
		if info == nil {
			info = recent.info
		}
		if info != nil && info.Status == indexer.TICKER_STATUS_MINT_COMPLETED {
			return info, nil
		}
		if time.Since(recent.checkedAt) < tickerMintRefreshInterval {
			return recent.info, nil
		}

		if p.l1IndexerClient == nil {
			return (*indexer.TickerInfo)(nil), nil
		}
		fresh := p.l1IndexerClient.GetTickInfo(name)
		p.mutex.Lock()
		defer p.mutex.Unlock()
		if p.tickerCacheEpoch != epoch {
			return (*indexer.TickerInfo)(nil), nil
		}
		if p.tickerMintRefresh == nil {
			p.tickerMintRefresh = make(map[string]tickerMintRefresh)
		}
		p.tickerMintRefresh[key] = tickerMintRefresh{checkedAt: time.Now(), info: fresh}
		if fresh != nil {
			if p.db != nil {
				if err := saveTickerInfo(p.db, fresh); err != nil {
					Log.Warningf("save refreshed ticker %s: %v", key, err)
				}
			}
			p.cacheTickerInfoLocked(fresh)
		}
		return fresh, nil
	})
	info, _ := value.(*indexer.TickerInfo)
	return info
}
