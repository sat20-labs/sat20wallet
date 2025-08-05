package wallet

import (
	"sync"

	"github.com/sat20-labs/sat20wallet/sdk/common"
)

type UtxoMgr struct {
	mutex       sync.RWMutex
	network     string
	address     string                 
	utxomap     map[string]map[string]*TxOutput_SatsNet // addr->utxo -> data
	refreshTime int64

	db        common.KVDB
	rpcClient IndexerRPCClient
}

func NewUtxoMgr(db common.KVDB, rpc IndexerRPCClient, network string) *UtxoMgr {
	locker := &UtxoMgr{
		utxomap:   make(map[string]map[string]*TxOutput_SatsNet),
		db:        db,
		rpcClient: rpc,
		network:   network,
	}
	return locker
}

func (p *UtxoMgr) Init() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.reload()
}

func (p *UtxoMgr) Reload(address string) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.reload()
}

func (p *UtxoMgr) reload() {
	lastTime, err := loadLastLockTime(p.db, p.network)
	if err == nil {
		if lastTime == p.refreshTime {
			return
		}
	}

	p.utxomap = loadAllUtxoFromDB(p.db, p.network)
	p.refreshTime = lastTime
}
