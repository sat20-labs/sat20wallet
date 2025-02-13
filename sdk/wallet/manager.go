package wallet

import (
	"sync"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/indexer"
)

type NotifyCB func (string, interface{})

// 密码只有一个，助记词可以有多组，对应不同的wallet
type Manager struct {
	mutex sync.RWMutex

	bInited       bool
	bStop         bool
	cfg           *Config
	password      string
	status        *Status
	quit          chan struct{}
	walletInfoMap map[int64]*WalletInDB
	wallet        *InternalWallet
	msgCallback   NotifyCB
	tickerInfoMap map[string]*indexer.TickerInfo // 缓存数据, key: AssetName.String()

	db              common.KVDB
}

var _chain string

func (p *Manager) init() error {
	if p.bInited {
		return nil
	}

	err := p.initDB()
	if err != nil {
		Log.Errorf("initDB failed. %v", err)
		return err
	}

	p.bInited = true

	return nil
}

func (p *Manager) Close() {
	p.bInited = false
}

func (p *Manager) checkSelf() error {

	return nil
}

func (p *Manager) dbStatistic() bool {

	return false
}

func (p *Manager) GetWallet() common.Wallet {
	return p.wallet
}

func (p *Manager) GetConfig() *Config {
	return p.cfg
}

func IsTestNet() bool {
	return _chain != "mainnet"
}
