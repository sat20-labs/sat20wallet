package wallet

import (
	"fmt"
	"strings"
	"sync"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type Channel struct {
	ChannelInDB
	IsDirty     bool          // 内存数据跟数据库数据不一致
	ResvId      int64         // channel处于某个修改过程中
	PeerRPC     NodeRPCClient // 除非peer是公网ip，不然保存这个没有意义，所以直接设置为服务端的rpcclient
	manager     *Manager
	localWallet common.Wallet
	Mutex       sync.RWMutex
}

func NewChannel(c *ChannelInDB, mgr *Manager) *Channel {
	newC := c
	if newC == nil {
		newC = NewChannelInDB()
	}
	ch := &Channel{
		ChannelInDB: *newC,
		PeerRPC:     mgr.GetPeerNodeClient(newC),
		manager:     mgr,
	}
	if ch.ChannelInDB.LocalWalletId == 0 {
		if c != nil {
			w := mgr.findWalletByChannelLocalKey(ch)
			if w == nil {
				Log.Panicf("wallet %d not found for channel %s", ch.ChannelInDB.LocalWalletId, ch.ChannelId)
			}
			ch.localWallet = w.Clone()
			ch.ChannelInDB.LocalWalletId = ch.localWallet.GetId()
			ch.ChannelInDB.LocalChanCfg.WalletId = ch.localWallet.GetSubAccount()
			_ = mgr.saveChannelToDB(ch)
		} else {
			ch.localWallet = mgr.GetWallet()
			ch.ChannelInDB.LocalWalletId = ch.localWallet.GetId()
			ch.ChannelInDB.LocalChanCfg.WalletId = ch.localWallet.GetSubAccount()
		}
	} else {
		w := mgr.FindWalletById(ch.ChannelInDB.LocalWalletId)
		if w == nil {
			oldWalletId := ch.ChannelInDB.LocalWalletId
			w = mgr.findWalletByChannelLocalKey(ch)
			if w == nil {
				Log.Panicf("wallet %d not found for channel %s", oldWalletId, ch.ChannelId)
			}
			Log.Warnf("channel %s local wallet id %d not found, recovered by local payment key wallet %d",
				ch.ChannelId, oldWalletId, w.GetId())
			ch.localWallet = w.Clone()
			ch.ChannelInDB.LocalWalletId = ch.localWallet.GetId()
			ch.ChannelInDB.LocalChanCfg.WalletId = ch.localWallet.GetSubAccount()
			_ = mgr.saveChannelToDB(ch)
			return ch
		}
		ch.localWallet = w.Clone()
		ch.localWallet.SetSubAccount(ch.ChannelInDB.LocalChanCfg.WalletId)
	}

	return ch
}

func (p *Manager) findWalletByChannelLocalKey(ch *Channel) common.Wallet {
	if ch == nil || ch.ChannelInDB.LocalChanCfg.PaymentKey == nil {
		return nil
	}
	pubKey := ch.ChannelInDB.LocalChanCfg.PaymentKey.SerializeCompressed()
	w := p.FindWalletByPubKey(pubKey)
	if w == nil {
		w = p.FindWalletByPubKeyWithDepth(pubKey, 100)
	}
	return w
}

func (p *Channel) Clone() *Channel {
	return &Channel{
		ChannelInDB: *p.ChannelInDB.Clone(),
		PeerRPC:     p.PeerRPC,
		manager:     p.manager,
		localWallet: p.localWallet,
	}
}

func (p *Channel) LocalWallet() common.Wallet {
	if p == nil {
		return nil
	}
	return p.localWallet
}

func (p *Channel) SetLocalWallet(w common.Wallet) {
	if p != nil {
		p.localWallet = w
	}
}

func (p *Channel) Manager() *Manager {
	if p == nil {
		return nil
	}
	return p.manager
}

func (p *Channel) IsAssetManaged(assetName *swire.AssetName) bool {
	p.Mutex.RLock()
	defer p.Mutex.RUnlock()

	tickInfo := p.manager.getTickerInfo(assetName)
	if tickInfo == nil {
		return false
	}
	outputs, ok := p.FundingUtxos[*GetAssetName(tickInfo)]
	return ok && len(outputs) > 0
}

func (p *Channel) RemoveAsset(assetName *AssetName) bool {
	p.Mutex.Lock()
	defer p.Mutex.Unlock()
	Log.Infof("remove %v from channel %s", assetName, p.ChannelId)
	delete(p.FundingUtxos, *assetName)
	delete(p.StubUtxos, *assetName)
	delete(p.LocalCommitment.LocalBalance, *assetName)
	delete(p.LocalCommitment.RemoteBalance, *assetName)
	delete(p.RemoteCommitment.LocalBalance, *assetName)
	delete(p.RemoteCommitment.RemoteBalance, *assetName)

	remove := make([]string, 0)
	for k, output := range p.UtxosL2 {
		amt := output.GetAsset(&assetName.AssetName)
		if amt.Sign() != 0 {
			remove = append(remove, k)
		}
	}
	for _, k := range remove {
		delete(p.UtxosL2, k)
	}
	remove = make([]string, 0)
	for k, output := range p.PendingUtxosL2 {
		amt := output.GetAsset(&assetName.AssetName)
		if amt.Sign() != 0 {
			remove = append(remove, k)
		}
	}
	for _, k := range remove {
		delete(p.PendingUtxosL2, k)
	}

	return true
}

func (p *Channel) RemoveUtxos_SatsNet(utxos map[string]bool) {
	for k := range utxos {
		delete(p.UtxosL2, k)
		parts := strings.Split(k, ":")
		delete(p.PendingUtxosL2, parts[0])
	}
}

func (p *Channel) IsBusy() bool {
	return p.ResvId != 0
}

func (p *Channel) GetChannelUtxosWithAsset_SatsNet(amt *Decimal, assetName *AssetName) ([]*TxOutput_SatsNet, error) {
	result := make([]*TxOutput_SatsNet, 0)
	var total *Decimal
	for _, u := range p.GetValidOutput_SatsNet() {
		if p.manager.utxoLockerL2.IsLocked(u.OutPointStr) {
			continue
		}

		num := u.GetAsset(&assetName.AssetName)
		if num.Sign() != 0 {
			total = total.Add(num)
			result = append(result, u)
			if total.Cmp(amt) >= 0 {
				break
			}
		}
	}

	if total.Cmp(amt) < 0 {
		return nil, fmt.Errorf("no enough utxo for %s, require %s but only %d", assetName.String(), amt.String(), total)
	}

	return result, nil
}

func (p *Channel) GetUtxosForStubs(n int) ([]string, error) {
	address := p.Address
	utxoInContral := p.UtxosInControl()
	return p.manager.GetUtxosForStubs(address, n, utxoInContral)
}

func (p *Channel) GetUtxosWithAssetV2_SatsNet(plainSats int64,
	amt *Decimal, assetName *swire.AssetName) ([]string, []string, error) {

	address := p.Address
	utxoInContral := p.UtxosInControl_SatsNet()
	return p.manager.GetUtxosWithAssetV2_SatsNet(address, plainSats, amt, assetName, utxoInContral)
}
