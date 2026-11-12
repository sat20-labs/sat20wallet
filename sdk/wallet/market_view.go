package wallet

import (
	indexer "github.com/sat20-labs/indexer/common"
	"time"
)

// Read-only market projection. The rolling window is shared by visitors and
// never serialized into contract state. Volume uses the existing hourly units.
type marketDay struct {
	at     int64
	trades int
	volume int64
	change *float64
}

func (p *SwapContractRuntime) readMarketDay(now int64) marketDay {
	d := p.marketDay
	if d.at/60 == now/60 && d.trades == p.TotalDealCount {
		return d
	}
	d = marketDay{at: now, trades: p.TotalDealCount}
	if d.trades != 0 {
		var hours [24]int64
		var newest, before float64
		for i := p.InvokeCount; i >= 0; i-- {
			it := p.getItemFromBuck(i)
			if it == nil {
				continue
			}
			age := now - it.OrderTime
			old := age >= 86400
			if old && newest == 0 {
				if p.LastDealPrice.Sign() > 0 {
					zero := 0.
					d.change = &zero
				}
				break
			}
			if it.Done != ITEM_STATUS_DEALT || (it.OrderType != ORDERTYPE_BUY && it.OrderType != ORDERTYPE_SELL) {
				continue
			}
			price := it.UnitPrice.Float64()
			if old {
				if price > 0 {
					before = price
					break
				}
				continue
			}
			if age < 0 {
				continue
			}
			if newest == 0 && price > 0 {
				newest = price
			}
			if it.OrderType == ORDERTYPE_BUY {
				hours[age/3600] += it.InValue - it.ServiceFee
			} else {
				hours[age/3600] += it.OutValue + it.ServiceFee
			}
		}
		for _, v := range hours {
			d.volume += v / 2
		}
		if before > 0 && newest > 0 {
			change := newest/before - 1
			d.change = &change
		}
	}
	p.marketDay = d
	return d
}

// Optional metadata must never turn a market read into an L1 RPC.
func (p *Manager) marketTicker(name *indexer.AssetName) *indexer.TickerInfo {
	if p == nil {
		return nil
	}
	key := name.String()
	p.mutex.RLock()
	info := p.tickerInfoMap[key]
	p.mutex.RUnlock()
	if info != nil && info.AssetName == *name {
		return info
	}
	if p.db == nil {
		return nil
	}
	buf, err := p.db.Read([]byte(GetTickerInfoKey(key)))
	if err != nil {
		return nil
	}
	var stored indexer.TickerInfo
	if DecodeFromBytes(buf, &stored) != nil || stored.AssetName != *name {
		return nil
	}
	return &stored
}

func (p *SwapContractRuntime) MarketView(detail bool) map[string]any {
	// Ticker lookup can enter the wallet; do not hold the contract lock for it.
	ti := p.stp.GetWalletMgr().marketTicker(p.GetAssetName())
	p.mutex.Lock()
	defer p.mutex.Unlock()
	d := p.readMarketDay(time.Now().Unix())
	asset := *p.GetAssetName()
	row := map[string]any{
		"contractURL": p.URL(), "assetName": asset, "status": p.Status, "enableBlock": p.EnableBlock,
		"deployTime": p.DeployTime, "AssetAmtInPool": p.AssetAmtInPool.Clone(), "SatsValueInPool": p.SatsValueInPool,
		"TotalDealSats": p.TotalDealSats, "TotalDealCount": p.TotalDealCount, "volume24h": d.volume, "pct24h": d.change,
		"lastDealPrice": p.LastDealPrice.String(),
	}
	if len(p.sellPool) > 0 {
		row["dealPrice"] = p.sellPool[0].UnitPrice.String()
	}
	if ti != nil {
		row["displayName"] = ti.DisplayName
		row["maxSupply"] = ti.MaxSupply
	}
	if detail {
		ticker := make(map[string]any)
		if ti != nil {
			ticker["displayname"] = ti.DisplayName
			ticker["maxSupply"] = ti.MaxSupply
			ticker["totalMinted"] = ti.TotalMinted
			ticker["divisibility"] = ti.Divisibility
		}
		row["tickerInfo"] = ticker
		row["Contract"] = p.Contract
		row["deployer"] = p.Deployer
		row["24hour"] = map[string]int64{"volume": d.volume}
		if p.GetTemplateName() == TEMPLATE_CONTRACT_AMM {
			row["BaseLptAmt"] = p.BaseLptAmt.Clone()
			row["TotalLptAmt"] = p.TotalLptAmt.Clone()
			row["TotalProfitAssets"] = p.TotalProfitAssets.Clone()
			row["TotalProfitSats"] = p.TotalProfitSats
			row["TotalProfitTx"] = p.TotalProfitTx
		} else {
			_, row["buyDepth"], row["sellDepth"] = p.marketDepth()
		}
	}
	return row
}
