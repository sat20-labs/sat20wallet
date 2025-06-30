package wallet

import (
	"sync"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/wire"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	sbtcutil "github.com/sat20-labs/satoshinet/btcutil"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// 只需要在发起方对utxo做锁定，因为remote端不需要检查，所有的utxo由发起方选好

type LockedUtxo struct {
	LockedTime int64 	`json:"lockedTime"`
	Reason string		`json:"reason"`
	Value  int64        `json:"value"`
	Assets swire.TxAssets `json:"assets"`
}

// 前端钱包在background和worker两个不同线程存在两个不同的stp模块，需要考虑这种情况下的数据同步
type UtxoLocker struct {
	mutex sync.RWMutex
	network string
	address string  // 暂时不区分地址，降低复杂性
	lockmap map[string]*LockedUtxo  // utxo -> lock time
	refreshTime int64 

	db common.KVDB
	rpcClient IndexerRPCClient
}

func NewUtxoLocker(db common.KVDB, rpc IndexerRPCClient, network string) *UtxoLocker {
	locker := &UtxoLocker{
		lockmap: 	make(map[string]*LockedUtxo),
		db: 		db,
		rpcClient:  rpc,
		network:    network,
	}
	return locker
}

func (p *UtxoLocker) Init(address string) {
	p.Reload(address)
}

func (p *UtxoLocker) Reload(address string) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	
	p.reload()
}

func (p *UtxoLocker) reload() {
	lastTime, err := loadLastLockTime(p.db, p.network)
	if err == nil {
		if lastTime == p.refreshTime {
			return
		}
	}

	p.lockmap = loadAllLockedUtxoFromDB(p.db, p.network)
	p.refreshTime = lastTime
}

func (p *UtxoLocker) LockUtxosWithTx(tx *wire.MsgTx) error {
	if tx == nil {
		return nil
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for _, in := range tx.TxIn {
		err := p.lockUtxo(in.PreviousOutPoint.String(), "broadcasted")
		if err != nil {
			return err
		}
	}
	p.refreshTime = time.Now().UnixMilli()
	saveLastLockTime(p.db, p.network, p.refreshTime)
	return nil
}

func (p *UtxoLocker) UnlockUtxosWithTx(tx *wire.MsgTx) error {
	if tx == nil {
		return nil
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for _, in := range tx.TxIn {
		err := p.unlockUtxo(in.PreviousOutPoint.String())
		if err != nil {
			return err
		}
	}
	p.refreshTime = time.Now().UnixMilli()
	saveLastLockTime(p.db, p.network, p.refreshTime)
	return nil
}

func (p *UtxoLocker) LockUtxosWithTx_SatsNet(tx *swire.MsgTx) error {
	if tx == nil {
		return nil
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for _, in := range tx.TxIn {
		err := p.lockUtxo(in.PreviousOutPoint.String(), "broadcasted")
		if err != nil {
			return err
		}
	}
	p.refreshTime = time.Now().UnixMilli()
	saveLastLockTime(p.db, p.network, p.refreshTime)
	return nil
}

func (p *UtxoLocker) UnlockUtxosWithTx_SatsNet(tx *swire.MsgTx) error {
	if tx == nil {
		return nil
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for _, in := range tx.TxIn {
		err := p.unlockUtxo(in.PreviousOutPoint.String())
		if err != nil {
			return err
		}
	}
	p.refreshTime = time.Now().UnixMilli()
	saveLastLockTime(p.db, p.network, p.refreshTime)
	return nil
}

func (p *UtxoLocker) LockUtxos(utxos []string, reason string) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for _, utxo := range utxos {
		err := p.lockUtxo(utxo, reason)
		if err != nil {
			return err
		}
	}
	p.refreshTime = time.Now().UnixMilli()
	saveLastLockTime(p.db, p.network, p.refreshTime)
	return nil
}

func (p *UtxoLocker) LockUtxo(utxo, reason string) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	err := p.lockUtxo(utxo, reason)
	if err != nil {
		return err
	}
	p.refreshTime = time.Now().UnixMilli()
	saveLastLockTime(p.db, p.network, p.refreshTime)
	return nil
}

func (p *UtxoLocker) lockUtxo(utxo, reason string) error {
	_, ok := p.lockmap[utxo]
	if ok {
		return nil
	}

	lockedUtxo := LockedUtxo{
		LockedTime: time.Now().Unix(),
		Reason: reason,
	}
	err := saveLockedUtxo(p.db, p.network, utxo, &lockedUtxo)
	if err != nil {
		Log.Errorf("saveLockedUtxo %s failed, %v", utxo, err)
		return err
	}
	//go p.FillAsset(utxo, &lockedUtxo)

	p.lockmap[utxo] = &lockedUtxo
	return nil
}


func (p *UtxoLocker) FillAsset(utxo string, lock *LockedUtxo) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.fillAsset(utxo, lock)
}

func (p *UtxoLocker) fillAsset(utxo string, lock *LockedUtxo) error {
	txOutput, err := p.rpcClient.GetTxOutput(utxo)
	if err != nil {
		Log.Errorf("fillAsset->GetTxOutput %s failed, %v", utxo, err)
		return err
	}
	lock.Value = txOutput.Value()
	lock.Assets = txOutput.Assets
	err = saveLockedUtxo(p.db, p.network, utxo, lock)
	if err != nil {
		Log.Errorf("fillAsset->saveLockedUtxo %s failed, %v", utxo, err)
		return err
	}
	return nil
}

func (p *UtxoLocker) UnlockUtxo(utxo string) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.unlockUtxo(utxo)

	p.refreshTime = time.Now().UnixMilli()
	saveLastLockTime(p.db, p.network, p.refreshTime)

	return nil
}

func (p *UtxoLocker) unlockUtxo(utxo string) error {
	_, ok := p.lockmap[utxo]
	if !ok {
		return nil
	}
	deleteLockedUtxo(p.db, p.network, utxo)
	delete(p.lockmap, utxo)

	return nil
}

// 调用之前，先reload确保数据最新
func (p *UtxoLocker) IsLocked(utxo string) bool {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	_, ok := p.lockmap[utxo]
	return ok
}

func (p *UtxoLocker) GetLockedUtxoList() map[string]*LockedUtxo {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	
	p.reload()
	result := make(map[string]*LockedUtxo)
	for k, v := range p.lockmap {
		result[k] = v
	}

	return result
}


// func (p *UtxoLocker) GetLockedAssetAmount(asset *swire.AssetName) *Decimal {
// 	p.mutex.RLock()
// 	defer p.mutex.RUnlock()

// 	p.reload()
	
// 	var assetAmt *Decimal
// 	var totalValue int64
// 	for k, v := range p.lockmap {

// 		if v.Value == 0 && len(v.Assets) == 0 {
// 			err := p.fillAsset(k, v)
// 			if err != nil {
// 				continue
// 			}
// 		}

// 		totalValue += v.Value
// 		if len(v.Assets) > 0 {
// 			info, err := v.Assets.Find(asset)
// 			if err == nil {
// 				if assetAmt == nil {
// 					assetAmt = &info.Amount
// 				} else {
// 					assetAmt = assetAmt.Add(&info.Amount)
// 				}
// 			}
// 		}
// 	}

// 	if indexer.IsPlainAsset(asset) {
// 		return indexer.NewDefaultDecimal(totalValue)
// 	}

// 	return assetAmt
// }


func (p *UtxoLocker) GetLockedUtxoListV2() map[string]bool {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	
	p.reload()
	result := make(map[string]bool)
	for k := range p.lockmap {
		result[k] = true
	}

	return result
}


func (p *UtxoLocker) CheckBlock(transactions []*btcutil.Tx)  {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.reload()
	bUpdated := false
	for _, tx := range transactions {
		msgTx := tx.MsgTx()
		for _, txIn := range msgTx.TxIn {
			utxo := txIn.PreviousOutPoint.String()
			_, ok := p.lockmap[utxo]
			if ok {
				deleteLockedUtxo(p.db, p.network, utxo)
				delete(p.lockmap, utxo)
				bUpdated = true
			}
		}
	}

	if bUpdated {
		p.refreshTime = time.Now().UnixMilli()
		saveLastLockTime(p.db, p.network, p.refreshTime)
	}
}

func (p *UtxoLocker) CheckBlock_SatsNet(transactions []*sbtcutil.Tx)  {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.reload()
	bUpdated := false
	for _, tx := range transactions {
		msgTx := tx.MsgTx()
		for _, txIn := range msgTx.TxIn {
			utxo := txIn.PreviousOutPoint.String()
			_, ok := p.lockmap[utxo]
			if ok {
				deleteLockedUtxo(p.db, p.network, utxo)
				delete(p.lockmap, utxo)
				bUpdated = true
			}
		}
	}

	if bUpdated {
		p.refreshTime = time.Now().UnixMilli()
		saveLastLockTime(p.db, p.network, p.refreshTime)
	}
}


func (p *UtxoLocker) CheckExisting()  {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.reload()

	utxos := make([]string, 0)
	for k := range p.lockmap {
		utxos = append(utxos, k)
	}

	existingUtxos, err := p.rpcClient.GetExistingUtxos(utxos)
	if err != nil {
		Log.Errorf("GetExistingUtxos failed, %v", err)
		return
	}
	if len(existingUtxos) != len(utxos) {
		//Log.Infof("some utxo spent! should check channel status...")
		existingUtxoMap := make(map[string]bool)
		for _, u := range existingUtxos {
			existingUtxoMap[u] = true
		}
		for k := range p.lockmap{
			_, ok := existingUtxoMap[k]
			if ok {
				continue
			}
			
			p.unlockUtxo(k)
		}
		p.refreshTime = time.Now().UnixMilli()
		saveLastLockTime(p.db, p.network, p.refreshTime)
	}
}


func (p *UtxoLocker) CheckUtxos(utxos []string) string  {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	p.reload()
	for _, utxo := range utxos {
		_, ok := p.lockmap[utxo]
		if ok {
			return utxo
		}
		
	}
	return ""
}

