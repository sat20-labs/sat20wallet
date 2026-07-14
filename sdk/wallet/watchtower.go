package wallet

import (
	"fmt"
	"sync"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/wire"
	db "github.com/sat20-labs/indexer/common"
)

// 可以作为一个公共的watchtower
// 委托人发送一个commitTxId和签名的punishTx，如果该CommitTxId被广播，punishTx会被自动广播
// TODO
// 1. 客户端模式下，因为数据存储空间有限，只能保存一部分数据，需要有一个循环保存的方式，只保存100条
// 2. 只保存commitTxId，会导致无法精确监控的情况，一些没用的punishTx没有被即时清理。更
//    好的方案，应该是保存commitTx的输入utxo，也就是每个utxo对应哪些commitTx，这样该utxo在
//    splicing-out操作中被花费后，这些commitTx和对应的punishTx都应该被删除

type WatchTower struct {
	mutex                sync.RWMutex
	commitMap            map[string]string          // commitTxId->channelId
	broadcastedCommitMap map[string]bool            // commitTxId
	utxoMap              map[string]map[string]bool // utxo -> map of commitTxID

	manager *Manager
}

func NewWatchTower(manager *Manager) *WatchTower {
	return newWatchTower(manager, true)
}

func NewWatchTowerWithDB(kvdb db.KVDB) *WatchTower {
	return newWatchTower(&Manager{db: kvdb}, false)
}

func newWatchTower(manager *Manager, autoBroadcast bool) *WatchTower {
	tower := &WatchTower{
		manager:              manager,
		commitMap:            loadAllCommitTxIdFromDB(manager.db),
		broadcastedCommitMap: loadAllBroadcastedCommitTxIdFromDB(manager.db),
		utxoMap:              loadAllUtxoCommitTxIdMap(manager.db),
	}

	if autoBroadcast {
		for k := range tower.broadcastedCommitMap {
			tower.broadcastPunishTx(k)
		}
	}

	return tower
}

type PunishTxInfo struct {
	ChannelId     string   `json:"channel_id"`
	CommitTxId    string   `json:"commit_txid"`
	PunishTxIds   []string `json:"punish_txids"`
	PunishTxHex   []string `json:"punish_tx_hex,omitempty"`
	Broadcastable bool     `json:"broadcastable"`
	Broadcasted   bool     `json:"broadcasted"`
	Verified      bool     `json:"verified"`
}

func (p *WatchTower) HasCommitTx(commitTxId string) bool {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	_, ok := p.commitMap[commitTxId]
	return ok
}

func (p *WatchTower) HasUtxoCommitTx(utxo, commitTxId string) bool {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	m, ok := p.utxoMap[utxo]
	return ok && m[commitTxId]
}

func (p *WatchTower) HasBroadcastedCommitTx(commitTxId string) bool {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.broadcastedCommitMap[commitTxId]
}

// 只有撤销的commitTx才需要发给watchTower
func (p *WatchTower) AddCommitTx(channel *Channel, commitTx *wire.MsgTx, signedPunishTx []*wire.MsgTx) error {
	if len(signedPunishTx) == 0 {
		return fmt.Errorf("at least one signed punish transaction is required")
	}

	txId := commitTx.TxID()
	err := savePunishTx(p.manager.db, channel, txId, signedPunishTx)
	if err != nil {
		Log.Errorf("savePunishTx %s-%s failed. %v", channel.ChannelId, txId, err)
		return err
	}
	Log.Infof("save punishTx %s for commitTx %s", signedPunishTx[len(signedPunishTx)-1].TxID(), txId)

	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.commitMap[txId] = channel.ChannelId

	// TODO 如果是客户端模式，为了降低存储空间，可能就不保存utxo相关数据

	for _, txIn := range commitTx.TxIn {
		utxo := txIn.PreviousOutPoint.String()
		m, ok := p.utxoMap[utxo]
		if !ok {
			m = make(map[string]bool)
		}
		m[txId] = true
		p.utxoMap[utxo] = m
		saveUtxoCommitTxIdMap(p.manager.db, channel.ChannelId, utxo, m)
	}

	return nil
}

// 某个通道内的utxo被正常花费掉，比如splicing-out
func (p *WatchTower) UtxoSpent(channel *Channel, utxos []string) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	commitTxIds := make(map[string]bool, 0)
	for _, utxo := range utxos {
		commitTxMap, ok := p.utxoMap[utxo]
		if ok {
			for k := range commitTxMap {
				commitTxIds[k] = true
			}
		}
		delete(p.utxoMap, utxo)
		deleteUtxoCommitTxIdMap(p.manager.db, channel.ChannelId, utxo)
	}
	if len(commitTxIds) == 0 {
		return
	}
	if err := deletePunishTx(p.manager.db, channel, commitTxIds); err != nil {
		Log.Errorf("deletePunishTx %s failed. %v", channel.ChannelId, err)
	}
	for commitTxId := range commitTxIds {
		delete(p.commitMap, commitTxId)
	}
	for utxo, commitTxMap := range p.utxoMap {
		updated := false
		for commitTxId := range commitTxIds {
			if commitTxMap[commitTxId] {
				delete(commitTxMap, commitTxId)
				updated = true
			}
		}
		if !updated {
			continue
		}
		if len(commitTxMap) == 0 {
			delete(p.utxoMap, utxo)
			deleteUtxoCommitTxIdMap(p.manager.db, channel.ChannelId, utxo)
		} else {
			saveUtxoCommitTxIdMap(p.manager.db, channel.ChannelId, utxo, commitTxMap)
		}
	}
}

func (p *WatchTower) FindConfirmedCommitTxBySpentUtxos(channelId string, utxos []string) string {
	candidates := make([]string, 0)
	seen := make(map[string]bool)

	p.mutex.RLock()
	for _, utxo := range utxos {
		commitTxMap := p.utxoMap[utxo]
		for commitTxId := range commitTxMap {
			if seen[commitTxId] {
				continue
			}
			if p.commitMap[commitTxId] != channelId {
				continue
			}
			seen[commitTxId] = true
			candidates = append(candidates, commitTxId)
		}
	}
	p.mutex.RUnlock()

	for _, commitTxId := range candidates {
		if p.manager.l1IndexerClient.IsTxConfirmed(commitTxId) {
			return commitTxId
		}
	}
	return ""
}

func (p *WatchTower) RemoveCommitTx(channel *Channel, commitTxId string) {
	if channel == nil || commitTxId == "" {
		return
	}

	commitTxIds := map[string]bool{commitTxId: true}
	if err := deletePunishTx(p.manager.db, channel, commitTxIds); err != nil {
		Log.Errorf("deletePunishTx %s failed. %v", channel.ChannelId, err)
	}
	deleteBroadcastedCommitTx(p.manager.db, commitTxId)

	p.mutex.Lock()
	defer p.mutex.Unlock()
	delete(p.commitMap, commitTxId)
	delete(p.broadcastedCommitMap, commitTxId)
	for utxo, commitTxMap := range p.utxoMap {
		if !commitTxMap[commitTxId] {
			continue
		}
		delete(commitTxMap, commitTxId)
		if len(commitTxMap) == 0 {
			delete(p.utxoMap, utxo)
			deleteUtxoCommitTxIdMap(p.manager.db, channel.ChannelId, utxo)
		} else {
			saveUtxoCommitTxIdMap(p.manager.db, channel.ChannelId, utxo, commitTxMap)
		}
	}
}

func (p *WatchTower) CleanCurrentRemoteCommitTx(channel *Channel) {
	if channel == nil || channel.RemoteCommitment == nil || channel.RemoteCommitment.CommitTx == nil {
		return
	}
	commitTxId := channel.RemoteCommitment.CommitTx.TxID()

	p.mutex.RLock()
	_, ok := p.commitMap[commitTxId]
	p.mutex.RUnlock()
	if !ok {
		return
	}

	Log.Warningf("remove current remote commitment %s from watchtower revoked set for channel %s",
		commitTxId, channel.ChannelId)
	p.RemoveCommitTx(channel, commitTxId)
}

// 只在punishTx广播失败后才进入这里，这里是补救措施
func (p *WatchTower) SetBroadcastedFlag(commitTxId string) error {

	err := saveBroadcastedCommitTx(p.manager.db, commitTxId)
	if err != nil {
		Log.Errorf("saveBroadcastedCommitTx %s failed. %v", commitTxId, err)
		return err
	}

	p.mutex.Lock()
	p.broadcastedCommitMap[commitTxId] = true
	p.mutex.Unlock()

	p.broadcastPunishTx(commitTxId)

	return nil
}

func (p *WatchTower) broadcastPunishTx(commitTxId string) {
	chanId, punishTxs, err := p.GetPunishTx(commitTxId)
	if err != nil {
		// 不应该出现这种情况
		Log.Errorf("Panic: can't find punishTx for the unexpectedly commitTx %s!!!!", commitTxId)
		return
	}
	go func() {
		err := p.manager.BroadcastTxs(punishTxs)
		for err != nil {
			time.Sleep(10 * time.Second)
			err = p.manager.BroadcastTxs(punishTxs)
			if err != nil {
				Log.Errorf("BroadCastTx punishTx for commitTx %s failed. %v", commitTxId, err)
			}
		}
		Log.Infof("punishTx broadcasted. %s", punishTxs[len(punishTxs)-1].TxID())

		p.CleanAllCommitTx(chanId)
		p.mutex.Lock()
		delete(p.broadcastedCommitMap, commitTxId)
		p.mutex.Unlock()
		deleteBroadcastedCommitTx(p.manager.db, commitTxId)
	}()
}

func (p *WatchTower) CleanAllCommitTx(chanId string) error {

	commits, err := deleteAllPunishTxWithChannel(p.manager.db, chanId)
	if err != nil {
		Log.Errorf("deleteAllPunishTxWithChannel %s failed. %v", chanId, err)
	}

	utxos, err := deleteAllUtxoDataWithChannel(p.manager.db, chanId)
	if err != nil {
		Log.Errorf("deleteAllUtxoDataWithChannel %s failed. %v", chanId, err)
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()
	for _, txId := range commits {
		delete(p.commitMap, txId)
	}
	for _, utxo := range utxos {
		delete(p.utxoMap, utxo)
	}

	return nil
}

func (p *WatchTower) GetPunishTx(commitTxId string) (string, []*wire.MsgTx, error) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	chanId, ok := p.commitMap[commitTxId]
	if !ok {
		return "", nil, fmt.Errorf("can't find punish tx by %s", commitTxId)
	}

	txs, err := loadPunishTx(p.manager.db, chanId, commitTxId)
	if err != nil {
		return "", nil, err
	}
	if len(txs) == 0 {
		return "", nil, fmt.Errorf("no punish transactions for %s", commitTxId)
	}

	PrintJsonTx(txs[len(txs)-1], "punishTx")

	return chanId, txs, nil
}

func (p *WatchTower) ListPunishTx(channelId string, includeHex bool) ([]*PunishTxInfo, error) {
	p.mutex.RLock()
	commitIds := make([]string, 0)
	for commitTxId, chanId := range p.commitMap {
		if channelId == "" || channelId == chanId {
			commitIds = append(commitIds, commitTxId)
		}
	}
	p.mutex.RUnlock()

	result := make([]*PunishTxInfo, 0, len(commitIds))
	for _, commitTxId := range commitIds {
		info, err := p.GetPunishTxInfo(channelId, commitTxId, includeHex)
		if err != nil {
			return nil, err
		}
		result = append(result, info)
	}
	return result, nil
}

func (p *WatchTower) GetPunishTxInfo(channelId, commitTxId string, includeHex bool) (*PunishTxInfo, error) {
	chanId, txs, err := p.GetPunishTx(commitTxId)
	if err != nil {
		return nil, err
	}
	if channelId != "" && channelId != chanId {
		return nil, fmt.Errorf("commit tx %s does not belong to channel %s", commitTxId, channelId)
	}

	info := &PunishTxInfo{
		ChannelId:     chanId,
		CommitTxId:    commitTxId,
		PunishTxIds:   make([]string, 0, len(txs)),
		Broadcastable: len(txs) != 0,
		Verified:      len(txs) != 0,
	}
	p.mutex.RLock()
	info.Broadcasted = p.broadcastedCommitMap[commitTxId]
	p.mutex.RUnlock()

	if includeHex {
		info.PunishTxHex = make([]string, 0, len(txs))
	}
	for _, tx := range txs {
		info.PunishTxIds = append(info.PunishTxIds, tx.TxID())
		if includeHex {
			txHex, err := EncodeMsgTx(tx)
			if err != nil {
				return nil, err
			}
			info.PunishTxHex = append(info.PunishTxHex, txHex)
		}
	}
	return info, nil
}

// return: chanId->punishTx
func (p *WatchTower) CheckBlock(transactions []*btcutil.Tx) map[string][]*wire.MsgTx {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	result := make(map[string][]*wire.MsgTx, 0)
	for _, tx := range transactions {
		txId := tx.MsgTx().TxID()
		chanId, ok := p.commitMap[txId]
		if !ok {
			continue
		}

		punishTx, err := loadPunishTx(p.manager.db, chanId, txId)
		if err != nil {
			Log.Errorf("loadPunishTx %s failed. %v", txId, err)
			continue
		}
		result[chanId] = punishTx
	}
	return result
}
