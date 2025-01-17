package wallet

import (
	"sync"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/common"
)

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
	msgCallback   interface{}

	db              common.KVDB
	http            common.HttpClient
	l1IndexerClient IndexerRPCClient
	l2IndexerClient IndexerRPCClient

	feeRateL1     int64 // sat/vkb
	refreshTimeL1 int64
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

	p.http = NewHTTPClient()
	p.l1IndexerClient = NewIndexerClient(p.cfg.IndexerL1.Scheme, p.cfg.IndexerL1.Host, p.http)
	p.l2IndexerClient = NewIndexerClient(p.cfg.IndexerL2.Scheme, p.cfg.IndexerL2.Host, p.http)

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

func (p *Manager) GetFeeRate() int64 {
	now := time.Now().Unix()
	if now-p.refreshTimeL1 > 3*60 {
		fr := p.l1IndexerClient.GetFeeRate()
		if fr == 0 {
			fr = 10
		} else {
			p.refreshTimeL1 = now
		}
		p.feeRateL1 = fr
	}
	return p.feeRateL1
}
