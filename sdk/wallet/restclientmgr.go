package wallet

import (
	"strings"
	"sync"
	"time"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type IndexerRPCClientMgr struct {
	indexers []IndexerRPCClient // 默认第一个是master，第二个是slave
	active IndexerRPCClient
	ticker *time.Ticker

	mutex sync.RWMutex
}

func NewIndexerRPCClientMgr() *IndexerRPCClientMgr{
	return &IndexerRPCClientMgr{}
}

func (p *IndexerRPCClientMgr) Start() {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	if p.ticker != nil {
		return
	}
	go p.pingThread()
}

func (p *IndexerRPCClientMgr) Stop() {
	p.ticker.Stop()
	p.ticker = nil
}

func (p *IndexerRPCClientMgr) Set(rpc IndexerRPCClient) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.indexers = append(p.indexers, rpc)
}

func (p *IndexerRPCClientMgr) SetMaster(rpc IndexerRPCClient) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if len(p.indexers) == 0 {
		p.indexers = append(p.indexers, rpc)
	} else {
		p.indexers[0] = rpc
	}
}

func (p *IndexerRPCClientMgr) ping() {
	active := p.getActiveIndexer()
	if active != nil {
		if err := active.Ping(); err == nil {
			return
		} else {
			Log.Infof("indexer %s become inactive", active.Host())
		}
	}

	p.selector()
}

func (p *IndexerRPCClientMgr) selector() IndexerRPCClient {
	p.mutex.RLock()
	indexers := utils.CloneSlice(p.indexers)
	p.mutex.RUnlock()

	for _, client := range indexers {
		if client.Ping() == nil {
			p.mutex.Lock()
			p.active = client
			p.mutex.Unlock()
			Log.Infof("switch to indexer %s", p.active.Host())
			return client
		}
	}
	return nil
}

func (p *IndexerRPCClientMgr) pingThread() {
	Log.Infof("pingThread start")

	duration := 60
	if _enable_testing {
		duration = 3
	}
	p.ticker = time.NewTicker(time.Duration(duration) * time.Second)

	p.ping()
	select {
	case <-p.ticker.C:
		p.ping()
	}

	Log.Infof("pingThread exit.")
}

func (p *IndexerRPCClientMgr) getActiveIndexer() IndexerRPCClient {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	if p.active != nil {
		return p.active
	}
	return p.indexers[0]
}

func shouldSwitchIndexer(err error) bool {
	if err == nil {
		return false
	}
	e := err.Error()
	return strings.Contains(e, "connection refused") ||
	strings.Contains(e, "panic")
}


func (p *IndexerRPCClientMgr) GetSlaveIndexer() IndexerRPCClient {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	if len(p.indexers) < 2 {
		return nil
	}
	return p.indexers[1]
}


// indexer interface

func (p *IndexerRPCClientMgr) Host() string {
	active := p.getActiveIndexer()
	if active == nil {
		return ""
	}
	return active.Host()
}

func (p *IndexerRPCClientMgr) Ping() (error) {
	err := p.getActiveIndexer().Ping()
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			err = indexer.Ping()
		}
	}
	return err
}

func (p *IndexerRPCClientMgr) GetTxOutput(utxo string) (*TxOutput, error) {
	result, err := p.getActiveIndexer().GetTxOutput(utxo)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			result, err = indexer.GetTxOutput(utxo)
		}
	}
	return result, err
}

func (p *IndexerRPCClientMgr) GetAscendData(utxo string) (*sindexer.AscendData, error) {
	result, err := p.getActiveIndexer().GetAscendData(utxo)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			result, err = indexer.GetAscendData(utxo)
		}
	}
	return result, err
}
func (p *IndexerRPCClientMgr) IsCoreNode(pubkey []byte) (bool, error) {
	result, err := p.getActiveIndexer().IsCoreNode(pubkey)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			result, err = indexer.IsCoreNode(pubkey)
		}
	}
	return result, err
}
func (p *IndexerRPCClientMgr) GetUtxoId(utxo string) (uint64, error) {
	result, err := p.getActiveIndexer().GetUtxoId(utxo)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			result, err = indexer.GetUtxoId(utxo)
		}
	}
	return result, err
}
func (p *IndexerRPCClientMgr) GetRawTx(tx string) (string, error) {
	result, err := p.getActiveIndexer().GetRawTx(tx)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			result, err = indexer.GetRawTx(tx)
		}
	}
	return result, err
}
func (p *IndexerRPCClientMgr) GetTxInfo(tx string) (*indexerwire.TxSimpleInfo, error) {
	result, err := p.getActiveIndexer().GetTxInfo(tx)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			result, err = indexer.GetTxInfo(tx)
		}
	}
	return result, err
}
func (p *IndexerRPCClientMgr) GetTxHeight(tx string) (int, error) {
	result, err := p.getActiveIndexer().GetTxHeight(tx)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			result, err = indexer.GetTxHeight(tx)
		}
	}
	return result, err
}
func (p *IndexerRPCClientMgr) IsTxConfirmed(tx string) bool {
	return p.getActiveIndexer().IsTxConfirmed(tx)
}
func (p *IndexerRPCClientMgr) GetSyncHeight() int {
	return p.getActiveIndexer().GetSyncHeight()
}
func (p *IndexerRPCClientMgr) GetBestHeight() int64 {
	return p.getActiveIndexer().GetBestHeight()
}
func (p *IndexerRPCClientMgr) GetBlockHash(height int) (string, error) {
	result, err := p.getActiveIndexer().GetBlockHash(height)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			result, err = indexer.GetBlockHash(height)
		}
	}
	return result, err
}
func (p *IndexerRPCClientMgr) GetBlock(blockHash string) (string, error) {
	result, err := p.getActiveIndexer().GetBlock(blockHash)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			result, err = indexer.GetBlock(blockHash)
		}
	}
	return result, err
}
func (p *IndexerRPCClientMgr) GetAssetSummaryWithAddress(address string) *indexerwire.AssetSummary {
	return p.getActiveIndexer().GetAssetSummaryWithAddress(address)
}
func (p *IndexerRPCClientMgr) GetUtxoListWithTicker(address string, ticker *swire.AssetName) []*indexerwire.TxOutputInfo {
	return p.getActiveIndexer().GetUtxoListWithTicker(address, ticker)
}
func (p *IndexerRPCClientMgr) GetUtxosWithAddress(address string) (map[string]*wire.TxOut, error) {
	result, err := p.getActiveIndexer().GetUtxosWithAddress(address)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			result, err = indexer.GetUtxosWithAddress(address)
		}
	}
	return result, err
}
func (p *IndexerRPCClientMgr) GetUnusableUtxosWithAddress(address string) ([]*TxOutput, error) {
	return p.getActiveIndexer().GetUnusableUtxosWithAddress(address)
}
func (p *IndexerRPCClientMgr) GetFeeRate() int64 {
	return p.getActiveIndexer().GetFeeRate()
}
func (p *IndexerRPCClientMgr) GetExistingUtxos(utxos []string) ([]string, error) {
	result, err := p.getActiveIndexer().GetExistingUtxos(utxos)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			result, err = indexer.GetExistingUtxos(utxos)
		}
	}
	return result, err
}
func (p *IndexerRPCClientMgr) TestRawTx(signedTxs []string) error {
	err := p.getActiveIndexer().TestRawTx(signedTxs)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			err = indexer.TestRawTx(signedTxs)
		}
	}
	return err
}
func (p *IndexerRPCClientMgr) BroadCastTx(tx *wire.MsgTx) (string, error) {
	result, err := p.getActiveIndexer().BroadCastTx(tx)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			result, err = indexer.BroadCastTx(tx)
		}
	}
	return result, err
}
func (p *IndexerRPCClientMgr) BroadCastTxs(tx []*wire.MsgTx) (error) {
	err := p.getActiveIndexer().BroadCastTxs(tx)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			err = indexer.BroadCastTxs(tx)
		}
	}
	return err
}
func (p *IndexerRPCClientMgr) BroadCastTx_SatsNet(tx *swire.MsgTx) (string, error) {
	result, err := p.getActiveIndexer().BroadCastTx_SatsNet(tx)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			result, err = indexer.BroadCastTx_SatsNet(tx)
		}
	}
	return result, err
}
func (p *IndexerRPCClientMgr) BroadCastTxs_SatsNet(tx []*swire.MsgTx) (error) {
	err := p.getActiveIndexer().BroadCastTxs_SatsNet(tx)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			err = indexer.BroadCastTxs_SatsNet(tx)
		}
	}
	return err
}
func (p *IndexerRPCClientMgr) GetTickInfo(assetName *swire.AssetName) *indexer.TickerInfo {
	return p.getActiveIndexer().GetTickInfo(assetName)
}
func (p *IndexerRPCClientMgr) AllowDeployTick(assetName *swire.AssetName) error {
	err := p.getActiveIndexer().AllowDeployTick(assetName)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			err = indexer.AllowDeployTick(assetName)
		}
	}
	return err
}
func (p *IndexerRPCClientMgr) GetUtxoSpentTx(utxo string) (string, error) {
	result, err := p.getActiveIndexer().GetUtxoSpentTx(utxo)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			result, err = indexer.GetUtxoSpentTx(utxo)
		}
	}
	return result, err
}
func (p *IndexerRPCClientMgr) GetServiceIncoming(addr string) (int, int64, error) {
	r1, r2, err := p.getActiveIndexer().GetServiceIncoming(addr)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			r1, r2, err = indexer.GetServiceIncoming(addr)
		}
	}
	return r1, r2, err
}

	// for dkvs
func (p *IndexerRPCClientMgr) GetNonce(pubKey []byte) ([]byte, error) {
	result, err := p.getActiveIndexer().GetNonce(pubKey)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			result, err = indexer.GetNonce(pubKey)
		}
	}
	return result, err
}
func (p *IndexerRPCClientMgr) PutKVs(req *indexerwire.PutKValueReq) (error) {
	err := p.getActiveIndexer().PutKVs(req)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			err = indexer.PutKVs(req)
		}
	}
	return err
}
func (p *IndexerRPCClientMgr) DelKVs(req *indexerwire.DelKValueReq) (error) {
	err := p.getActiveIndexer().DelKVs(req)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			err = indexer.DelKVs(req)
		}
	}
	return err
}
func (p *IndexerRPCClientMgr) GetKV(pubkey []byte, key string) (*indexerwire.KeyValue, error) {
	result, err := p.getActiveIndexer().GetKV(pubkey, key)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			result, err = indexer.GetKV(pubkey, key)
		}
	}
	return result, err
}

	// for names
func (p *IndexerRPCClientMgr) GetNameInfo(in string) (*indexerwire.OrdinalsName, error) {
	result, err := p.getActiveIndexer().GetNameInfo(in)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			result, err = indexer.GetNameInfo(in)
		}
	}
	return result, err
}
func (p *IndexerRPCClientMgr) GetNamesWithKey(in1 string, in2 string) ([]*indexerwire.OrdinalsName, error) {
	result, err := p.getActiveIndexer().GetNamesWithKey(in1, in2)
	if shouldSwitchIndexer(err) {
		indexer := p.selector()
		if indexer != nil {
			result, err = indexer.GetNamesWithKey(in2, in2)
		}
	}
	return result, err
}