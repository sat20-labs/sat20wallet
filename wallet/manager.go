package wallet

import (
	"sync"
	"time"

	"github.com/sat20-labs/sat20wallet/common"
)

type Manager struct {
	mutex sync.RWMutex

	bInited bool
	bStop   bool
	cfg     *Config
	status  *Status
	quit    chan struct{}
	wallet  common.Wallet
	msgCallback        interface{}

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

	p.http = NewHTTPClient()
	p.l1IndexerClient = NewIndexerClient(p.cfg.IndexerL1.Scheme, p.cfg.IndexerL1.Host, p.http)
	p.l2IndexerClient = NewIndexerClient(p.cfg.IndexerL2.Scheme, p.cfg.IndexerL2.Host, p.http)


	p.saveStatus()
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

