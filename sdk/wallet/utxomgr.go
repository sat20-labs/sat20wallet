package wallet

import (
	"sync"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// 一个临时的事务性的utxo管理器
// 在一个事务中，将上一个tx的输出保存下来，可用于在下一个tx中作为输入

type utxoInfo struct {
	assetName  *indexer.AssetName
}

type UtxoMgr struct {
	mutex       sync.RWMutex   
	address     string     
	assetToUtxosMap map[indexer.AssetName][]*indexer.AssetsInUtxo 
	utxoInfoMap 	map[string]*utxoInfo // utxo
	rpcClient 		IndexerRPCClient
}

func NewUtxoMgr(address string, rpc IndexerRPCClient) *UtxoMgr {
	mgr := &UtxoMgr{
		address: address,
		assetToUtxosMap: make(map[indexer.AssetName][]*indexer.AssetsInUtxo),
		utxoInfoMap: make(map[string]*utxoInfo),
		rpcClient: rpc,
	}
	return mgr
}

func (p *UtxoMgr) GetAddress() string {
	return p.address
}

func (p *UtxoMgr) GetUtxoListWithTicker(name *indexer.AssetName) []*indexer.AssetsInUtxo {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	
	outputs, ok := p.assetToUtxosMap[*name]
	if ok {
		return outputs
	}
	
	outputs = p.rpcClient.GetUtxoListWithTicker(p.address, name)
	p.assetToUtxosMap[*name] = outputs
	for _, v := range outputs {
		p.utxoInfoMap[v.OutPoint] = &utxoInfo{
			assetName: name,
		}
	}
	
	return outputs
}

func (p *UtxoMgr) AddOutput(name *indexer.AssetName, output *indexer.AssetsInUtxo) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.utxoInfoMap[output.OutPoint] = &utxoInfo{
			assetName: name,
		}

	outputs, ok := p.assetToUtxosMap[*name]
	if !ok {
		outputs = append(outputs, output)
		p.assetToUtxosMap[*name] = outputs
	} else {
		// 从大到小排序，插入合适的位置
		isPlainAsset := indexer.IsPlainAsset(name)
		var amt *Decimal
		if !isPlainAsset {
			var err error
			for _, v := range output.Assets {
				if v.AssetName.String() == name.String() {
					amt, err = indexer.NewDecimalFromString(v.Amount, v.Precision)
					if err != nil {
						Log.Errorf("AddOutput invalid amt %s, %v", v.Amount, err)
						return 
					}
				}
			}
		}
		insertIndex := len(outputs)
		out:
		for i, v := range outputs {
			if isPlainAsset {
				if v.Value <= output.Value {
					insertIndex = i
					break
				}
			} else {
				for _, asset := range v.Assets {
					if asset.AssetName.String() == name.String() {
						amt2, err := indexer.NewDecimalFromString(asset.Amount, asset.Precision)
						if err != nil {
							Log.Errorf("AddOutput invalid amt %s, %v", asset.Amount, err)
							continue 
						}
						if amt2.Cmp(amt) <= 0 {
							insertIndex = i
							break out
						}
					}
				}
			}
		}
		outputs = append(outputs, nil)
		copy(outputs[insertIndex+1:], outputs[insertIndex:])
		outputs[insertIndex] = output
		p.assetToUtxosMap[*name] = outputs
	}
}

func (p *UtxoMgr) RemoveUtxo(utxo string) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.removeUtxo(utxo)
}

func (p *UtxoMgr) removeUtxo(utxo string) {
	
	info, ok := p.utxoInfoMap[utxo]
	if !ok {
		return
	}
	
	delete(p.utxoInfoMap, utxo)

	outputs, ok := p.assetToUtxosMap[*info.assetName]
	if !ok {
		outputs = make([]*indexer.AssetsInUtxo, 0)
		p.assetToUtxosMap[*info.assetName] = outputs
	}

	for i, v := range outputs {
		if v.OutPoint == utxo {
			copy(outputs[i:], outputs[i+1:])
			outputs = outputs[:len(outputs)-1]
			p.assetToUtxosMap[*info.assetName] = outputs
			return
		}
	}
}

func (p *UtxoMgr) RemoveOutputs(outputs []*TxOutput) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for _, output := range outputs {
		utxo := output.OutPointStr
		p.removeUtxo(utxo)
	}
}

func (p *UtxoMgr) RemoveUtxosWithTx(tx *wire.MsgTx) {
	if tx == nil {
		return
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for _, in := range tx.TxIn {
		utxo := in.PreviousOutPoint.String()
		p.removeUtxo(utxo)
	}
}

func (p *UtxoMgr) RemoveUtxosWithTx_SatsNet(tx *swire.MsgTx) {
	if tx == nil {
		return
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for _, in := range tx.TxIn {
		utxo := in.PreviousOutPoint.String()
		p.removeUtxo(utxo)
	}
}

