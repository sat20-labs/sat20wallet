// Package fakeindexer contains the shared in-memory indexer used by SDK and STP tests.
package fakeindexer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	schainhash "github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/runes/runestone"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	sindexerwire "github.com/sat20-labs/satoshinet/indexer/rpcserver/wire"
)

type Decimal = indexer.Decimal
type TxOutput = indexer.TxOutput

type Brc20Transfer struct {
	Utxo      string
	Address   string
	AssetName *swire.AssetName
	Amt       *Decimal
}

type Network struct {
	State         *State // metadata shared by clients on this fixture, never across independent fixtures.
	Mutex         sync.RWMutex
	Name          string
	Height        int // 当前写入高度（未确定高度）， bestheight = height - 1
	LastTime      int64
	Blocks        map[int][]string  // height -> txId list
	TxBroadcasted map[string]string // txId -> txHex
	Utxos         []string
	UtxoUsed      map[string]string // utxo -> spent in the txId
	UtxoIndex     map[string]int
	UtxoValue     []int64
	UtxoAssets    []indexer.TxAssets
	Offsets       []map[swire.AssetName]indexer.AssetOffsets
	UtxoOwner     []int
	AscendMap     map[string]string // utxo->anchorTxId
	DescendMap    map[string]string // deanchorTxId->utxo

	// brc20, 仅在L1有效
	AddrAssetMap    map[string]map[swire.AssetName]*Decimal        // brc20 持有的数量
	AddrTransferMap map[string]map[swire.AssetName]map[string]bool // brc20 持有的transfer铭文
	TransferInfo    map[string]*Brc20Transfer                      // brc20 持有的transfer铭文
	Invalids        map[string]map[swire.AssetName]bool            // utxo中哪些资产是无效的

	// DKVS is shared by every fake client connected to this simulated Network.
	KvCache         map[string]*indexerwire.KeyValue
	DkvsRecords     map[string]*swire.DKVSRecord
	DkvsDeleted     map[string]dkvsindexer.DKVSKeyState
	DkvsGenerations map[string]uint64
}

// 将预设的brc20资产都当作可以转移的
func (p *Network) InitBRC20AssetInfo() {
	for i, utxo := range p.Utxos {
		if p.UtxoUsed[utxo] != "" {
			continue
		}

		addr := p.State.PkScripts[p.UtxoOwner[i]]
		assets := p.UtxoAssets[i]

		for _, asset := range assets {
			if asset.Name.Protocol == indexer.PROTOCOL_NAME_BRC20 {
				assetmap, ok := p.AddrAssetMap[addr]
				if !ok {
					assetmap = make(map[swire.AssetName]*Decimal)
					p.AddrAssetMap[addr] = assetmap
				}
				total := assetmap[asset.Name]
				assetmap[asset.Name] = total.Add(&asset.Amount).Add(&asset.Amount) // 加倍

				transferMap, ok := p.AddrTransferMap[addr]
				if !ok {
					transferMap = make(map[swire.AssetName]map[string]bool)
					p.AddrTransferMap[addr] = transferMap
				}
				utxomap, ok := transferMap[asset.Name]
				if !ok {
					utxomap = make(map[string]bool)
					transferMap[asset.Name] = utxomap
				}
				utxomap[utxo] = true

				p.TransferInfo[utxo] = &Brc20Transfer{
					Utxo:      utxo,
					Address:   addr,
					AssetName: &asset.Name,
					Amt:       &asset.Amount,
				}

			}
		}

	}
}

func (p *State) InitTickerInfo(txId string) {
	p.Tickers = make(map[string]*indexer.TickerInfo)
	for k, v := range defaultTickers() {
		v.DeployTx = txId
		p.Tickers[k] = v
	}
}

func (p *State) CloneTickerInfo() map[string]*indexer.TickerInfo {
	tickerInfo := make(map[string]*indexer.TickerInfo)
	for k, v := range p.Tickers {
		n := *v
		n.Content = append([]byte(nil), v.Content...)
		tickerInfo[k] = &n
	}
	return tickerInfo
}

func (p *State) SetTickerInfo(another map[string]*indexer.TickerInfo) {
	p.Tickers = make(map[string]*indexer.TickerInfo)
	for k, v := range another {
		n := *v
		n.Content = append([]byte(nil), v.Content...)
		p.Tickers[k] = &n
	}
}

func (p *State) GetTickerInfoByRuneId(runeId string) (*indexer.TickerInfo, error) {
	for _, v := range p.Tickers {
		if v.DisplayName == runeId {
			return v, nil
		}
	}
	return nil, fmt.Errorf("not found %s", runeId)
}

func (p *Network) Clone() *Network {
	n := &Network{State: p.State.Clone()}

	n.Name = p.Name
	n.Height = p.Height
	n.LastTime = p.LastTime

	n.Blocks = make(map[int][]string)
	for k, v := range p.Blocks {
		txList := make([]string, len(v))
		copy(txList, v)
		n.Blocks[k] = txList
	}

	n.TxBroadcasted = make(map[string]string)
	for k, v := range p.TxBroadcasted {
		n.TxBroadcasted[k] = v
	}

	n.Utxos = make([]string, len(p.Utxos))
	copy(n.Utxos, p.Utxos)

	n.UtxoUsed = make(map[string]string)
	for k, v := range p.UtxoUsed {
		n.UtxoUsed[k] = v
	}

	n.UtxoIndex = make(map[string]int)
	for k, v := range p.UtxoIndex {
		n.UtxoIndex[k] = v
	}

	n.UtxoValue = make([]int64, len(p.UtxoValue))
	copy(n.UtxoValue, p.UtxoValue)

	n.UtxoAssets = make([]indexer.TxAssets, len(p.UtxoAssets))
	for i, assets := range p.UtxoAssets {
		n.UtxoAssets[i] = assets.Clone()
	}

	n.Offsets = make([]map[swire.AssetName]indexer.AssetOffsets, len(p.Offsets))
	for i, offsetmap := range p.Offsets {
		n.Offsets[i] = cloneOffsets(offsetmap)
	}

	n.UtxoOwner = make([]int, len(p.UtxoOwner))
	copy(n.UtxoOwner, p.UtxoOwner)

	n.AscendMap = make(map[string]string)
	for k, v := range p.AscendMap {
		n.AscendMap[k] = v
	}

	n.DescendMap = make(map[string]string)
	for k, v := range p.DescendMap {
		n.DescendMap[k] = v
	}

	n.AddrAssetMap = make(map[string]map[swire.AssetName]*Decimal)
	for addr, assetMap := range p.AddrAssetMap {
		newAssetMap := make(map[swire.AssetName]*Decimal)
		for k, v := range assetMap {
			newAssetMap[k] = v.Clone()
		}
		n.AddrAssetMap[addr] = newAssetMap
	}

	n.AddrTransferMap = make(map[string]map[swire.AssetName]map[string]bool)
	for addr, transferMap := range p.AddrTransferMap {
		newTransferMap := make(map[swire.AssetName]map[string]bool)
		for name, um := range transferMap {
			utxoMap := make(map[string]bool)
			for k, v := range um {
				utxoMap[k] = v
			}
			newTransferMap[name] = utxoMap
		}
		n.AddrTransferMap[addr] = newTransferMap
	}

	n.TransferInfo = make(map[string]*Brc20Transfer)
	for k, v := range p.TransferInfo {
		newInfo := &Brc20Transfer{
			Utxo:      v.Utxo,
			Address:   v.Address,
			AssetName: v.AssetName,
			Amt:       v.Amt.Clone(),
		}
		n.TransferInfo[k] = newInfo
	}

	n.Invalids = make(map[string]map[swire.AssetName]bool)
	for utxo, assetMap := range p.Invalids {
		newAssetMap := make(map[swire.AssetName]bool)
		for k, v := range assetMap {
			newAssetMap[k] = v
		}
		n.Invalids[utxo] = newAssetMap
	}

	n.KvCache = make(map[string]*indexerwire.KeyValue)
	for k, v := range p.KvCache {
		n.KvCache[k] = cloneFakeKeyValue(v)
	}
	n.DkvsRecords = make(map[string]*swire.DKVSRecord)
	for k, v := range p.DkvsRecords {
		n.DkvsRecords[k] = cloneFakeDKVSRecord(v)
	}
	n.DkvsDeleted = make(map[string]dkvsindexer.DKVSKeyState)
	for k, v := range p.DkvsDeleted {
		n.DkvsDeleted[k] = cloneFakeDKVSKeyState(v)
	}
	n.DkvsGenerations = make(map[string]uint64)
	for k, v := range p.DkvsGenerations {
		n.DkvsGenerations[k] = v
	}

	return n
}

func cloneOffsets(this map[swire.AssetName]indexer.AssetOffsets) map[swire.AssetName]indexer.AssetOffsets {
	that := make(map[swire.AssetName]indexer.AssetOffsets)
	for k, v := range this {
		that[k] = v.Clone()
	}
	return that
}

// Set restores ledger data into the existing fixture. STP restores its shared
// L1/L2 metadata separately with SetTickerInfo, retaining the same State pointer.
func (p *Network) Set(another *Network) {
	n := another.Clone()
	p.Name = n.Name
	p.Height = n.Height
	p.Blocks = n.Blocks
	p.LastTime = n.LastTime
	p.TxBroadcasted = n.TxBroadcasted
	p.Utxos = n.Utxos
	p.UtxoUsed = n.UtxoUsed
	p.UtxoIndex = n.UtxoIndex
	p.UtxoValue = n.UtxoValue
	p.UtxoAssets = n.UtxoAssets
	p.Offsets = n.Offsets
	p.UtxoOwner = n.UtxoOwner
	p.AscendMap = n.AscendMap
	p.DescendMap = n.DescendMap
	p.AddrAssetMap = n.AddrAssetMap
	p.AddrTransferMap = n.AddrTransferMap
	p.TransferInfo = n.TransferInfo
	p.Invalids = n.Invalids
	p.KvCache = n.KvCache
	p.DkvsRecords = n.DkvsRecords
	p.DkvsDeleted = n.DkvsDeleted
	p.DkvsGenerations = n.DkvsGenerations
}

func (p *Network) IsBitcoinNet() bool {
	return p.Name == "bitcoin"
}

func CreateGenesisTx(n *Network) (*wire.MsgTx, error) {
	tx := wire.NewMsgTx(wire.TxVersion)

	// 构造 coinbase 输入
	coinbaseInput := &wire.TxIn{
		PreviousOutPoint: wire.OutPoint{
			Hash:  chainhash.Hash{}, // 全 0
			Index: 0xffffffff,       // 特殊 index
		},
		SignatureScript: []byte("genesis"), // 任意 coinbase data（如 block height）
		Sequence:        0xffffffff,
	}
	tx.AddTxIn(coinbaseInput)

	for i, value := range n.UtxoValue {
		pkscript := n.State.PkScripts[n.UtxoOwner[i]]
		b, _ := hex.DecodeString(pkscript)
		// 构造输出
		tx.AddTxOut(&wire.TxOut{
			Value:    value,
			PkScript: b,
		})
	}

	fmt.Printf("%s genesis tx %s\n", n.Name, tx.TxID())
	return tx, nil
}

func CreateGenesisTx_SatsNet(n *Network) (*swire.MsgTx, error) {
	tx := swire.NewMsgTx(wire.TxVersion)

	// 构造 coinbase 输入
	coinbaseInput := &swire.TxIn{
		PreviousOutPoint: swire.OutPoint{
			Hash:  schainhash.Hash{}, // 全 0
			Index: 0xffffffff,        // 特殊 index
		},
		SignatureScript: []byte("genesis"), // 任意 coinbase data（如 block height）
		Sequence:        0xffffffff,
	}
	tx.AddTxIn(coinbaseInput)

	for i, value := range n.UtxoValue {
		pkscript := n.State.PkScripts[n.UtxoOwner[i]]
		b, _ := hex.DecodeString(pkscript)
		// 构造输出
		tx.AddTxOut(&swire.TxOut{
			Value:    value,
			Assets:   n.UtxoAssets[i],
			PkScript: b,
		})
	}

	fmt.Printf("%s genesis tx %s", n.Name, tx.TxID())
	return tx, nil
}

type Client struct {
	*wallet.RESTClient
	Network *Network
}

func NewClient(n *Network) *Client {
	if n.State == nil {
		n.State = NewState()
	}
	client := wallet.NewRESTClient("", "", "", nil)
	return &Client{
		RESTClient: client,
		Network:    n,
	}
}

func (p *Client) Host() string {
	return p.RESTClient.Host
}

func (p *Client) Ping() error {
	return nil
}

func (p *Client) GetTxOutput(utxo string) (*TxOutput, error) {
	utxoId, err := p.GetUtxoId(utxo)
	if err != nil {
		return nil, err
	}

	p.Network.Mutex.RLock()
	defer p.Network.Mutex.RUnlock()

	index, ok := p.Network.UtxoIndex[utxo]
	if !ok {
		return nil, fmt.Errorf("can't find utxo %s", utxo)
	}

	if p.Network.UtxoUsed[utxo] != "" {
		return nil, fmt.Errorf("utxo %s is spent", utxo)
	}

	offsets := p.Network.Offsets[index]
	txAssets := p.Network.UtxoAssets[index]

	pkScript, _ := hex.DecodeString(p.Network.State.PkScripts[p.Network.UtxoOwner[index]])

	output := TxOutput{
		UtxoId:        utxoId,
		OutPointStr:   utxo,
		OutValue:      wire.TxOut{Value: p.Network.UtxoValue[index], PkScript: pkScript},
		Assets:        txAssets.Clone(),
		Offsets:       cloneOffsets(offsets),
		SatBindingMap: make(map[int64]*indexer.AssetInfo),
		Invalids:      make(map[indexer.AssetName]bool),
	}
	invalidmap, existing := p.Network.Invalids[utxo]
	for _, asset := range output.Assets {
		if p.Network.IsBitcoinNet() && asset.Name.Protocol == indexer.PROTOCOL_NAME_BRC20 {
			offsets, ok := output.Offsets[asset.Name]
			if ok {
				if len(offsets) != 1 {
					continue
				}
				output.SatBindingMap[offsets[0].Start] = asset.Clone()
			}
		}

		if existing {
			invalid, ok := invalidmap[asset.Name]
			if ok {
				output.Invalids[asset.Name] = invalid
			}
		}
	}

	return &output, nil
}

func (p *Client) GetAscendData(utxo string) (*sindexer.AscendData, error) {
	txId, ok := p.Network.AscendMap[utxo]
	if ok {
		return &sindexer.AscendData{
			AnchorTxId: txId,
		}, nil
	}
	return nil, fmt.Errorf("not found")
}

func (p *Client) GetChannelLedger(channel string) ([]*sindexer.ChannelLedgerEntry, error) {
	return []*sindexer.ChannelLedgerEntry{}, nil
}

func (p *Client) GetChannelStateEvents(channel string) ([]*sindexer.ChannelStateEvent, error) {
	return []*sindexer.ChannelStateEvent{}, nil
}

func (p *Client) RecordChannelStateEvent(event *sindexer.ChannelStateEvent, pubkey, sig []byte) error {
	return nil
}

func (p *Client) IsCoreNode(pubkey []byte) (bool, error) {
	pkStr := hex.EncodeToString(pubkey)
	if pkStr == indexer.GetBootstrapPubKey() || pkStr == indexer.GetCoreNodePubKey() {
		return true, nil
	}

	_, ok := p.Network.State.CoreNodes[pkStr]
	return ok, nil
}

func (p *Client) GetCoreNodeInfo(pubkey []byte) (*sindexer.CoreNodeInfo, error) {
	pkStr := hex.EncodeToString(pubkey)
	_, ok := p.Network.State.CoreNodes[pkStr]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	info := sindexer.NewCoreNodeInfo(nil)
	if minerInfo, ok := p.Network.State.MinerInfo[pkStr]; ok {
		info.MinerInfo = *minerInfo
	}
	if childMap, ok := p.Network.State.CoreChildren[pkStr]; ok {
		info.ChildMiners = childMap
	}
	return info, nil
}

func (p *Client) IsMinerNode(pubkey []byte) (bool, error) {
	ok, _ := p.IsCoreNode(pubkey)
	if ok {
		return true, nil
	}
	pkStr := hex.EncodeToString(pubkey)
	if _, ok := p.Network.State.CoreNodes[pkStr]; ok {
		return true, nil
	}
	return false, nil
}

func (p *Client) GetMinerInfo(pubkey []byte) (*sindexerwire.MinerInfo, error) {
	pkStr := hex.EncodeToString(pubkey)

	c, err := p.GetCoreNodeInfo(pubkey)
	if err == nil {
		return &sindexerwire.MinerInfo{
			MinerInfo:  &c.MinerInfo,
			IsCoreNode: true,
			ChildCount: len(c.ChildMiners),
		}, nil
	}

	info, ok := p.Network.State.MinerInfo[pkStr]
	if ok {
		return &sindexerwire.MinerInfo{
			MinerInfo:  info,
			IsCoreNode: false,
		}, nil
	}
	return nil, fmt.Errorf("not a miner")
}

// 只有未花费的能拿到id
func (p *Client) GetUtxoId(utxo string) (uint64, error) {
	p.Network.Mutex.RLock()
	defer p.Network.Mutex.RUnlock()

	_, ok := p.Network.UtxoIndex[utxo]
	if !ok {
		return indexer.INVALID_ID, fmt.Errorf("can't find utxo %s", utxo)
	}

	if p.Network.UtxoUsed[utxo] != "" {
		return indexer.INVALID_ID, fmt.Errorf("utxo %s is spent", utxo)
	}

	txId, vout, err := indexer.ParseUtxo(utxo)
	if err != nil {
		return indexer.INVALID_ID, err
	}
	for height, txList := range p.Network.Blocks {
		for txIndex, tx := range txList {
			if tx == txId {
				utxoId := indexer.ToUtxoId(height, txIndex, vout)
				return utxoId, nil
			}
		}
	}

	return indexer.INVALID_ID, fmt.Errorf("utxo %s is invalid", utxo)
}

// btcutil.Tx
func (p *Client) GetRawTx(tx string) (string, error) {
	str, ok := p.Network.TxBroadcasted[tx]
	if !ok {
		return "", fmt.Errorf("not found")
	}
	return str, nil
}

// btcutil.Tx
func (p *Client) GetTxInfo(tx string) (*indexerwire.TxSimpleInfo, error) {
	p.Network.Mutex.RLock()
	defer p.Network.Mutex.RUnlock()
	_, ok := p.Network.TxBroadcasted[tx]
	if !ok {
		return nil, fmt.Errorf("not found")
	}

	var height int
	for k, idList := range p.Network.Blocks {
		for _, id := range idList {
			if id == tx {
				height = k
				break
			}
		}
	}

	return &indexerwire.TxSimpleInfo{
		TxID:          tx,
		Version:       1,
		Confirmations: uint64(p.Network.Height - height),
		BlockHeight:   int64(height),
	}, nil
}

func (p *Client) GetTxHeight(tx string) (int, error) {
	p.Network.Mutex.RLock()
	defer p.Network.Mutex.RUnlock()

	return p.getTxHeight(tx)
}

func (p *Client) getTxHeight(tx string) (int, error) {

	_, ok := p.Network.TxBroadcasted[tx]
	if !ok {
		return -1, fmt.Errorf("not found")
	}

	for k, idList := range p.Network.Blocks {
		for _, id := range idList {
			if id == tx {
				if k == p.Network.Height {
					return -1, nil
				}
				return k, nil
			}
		}
	}
	return -1, fmt.Errorf("not found")

}

func (p *Client) IsTxConfirmed(tx string) bool {
	height, _ := p.GetTxHeight(tx)
	return height >= 0
}

func (p *Client) generateNewBlock() {
	if len(p.Network.Blocks[p.Network.Height]) > 0 {
		wallet.Log.Infof("%s generate block %d, tx count %d", p.Network.Name, p.Network.Height, len(p.Network.Blocks[p.Network.Height]))
		p.Network.Height++
		p.Network.LastTime = time.Now().UnixMilli()
	}
}

// 由这个接口决定出块
func (p *Client) GetSyncHeight() int {
	p.Network.Mutex.Lock()
	defer p.Network.Mutex.Unlock()

	p.generateNewBlock() // 有tx就生成新的块，确定性
	return p.Network.Height - 1
}

// 通过indexer访问btc节点，效率比较低。最好改用上面的接口。
func (p *Client) GetBestHeight() int64 {
	p.Network.Mutex.Lock()
	defer p.Network.Mutex.Unlock()

	return int64(p.Network.Height - 1)
}

func (p *Client) GetBlockHash(height int) (string, error) {
	return fmt.Sprintf("%d", height), nil
}

func BuildBlockFromHexTxs(txHexes []string) (*wire.MsgBlock, error) {
	block := wire.NewMsgBlock(&wire.BlockHeader{
		// 随便填一些字段（不检查）
		Version:    0x20000000,
		PrevBlock:  chainhash.Hash{}, // 可以根据需要替换为某个真实区块hash
		MerkleRoot: chainhash.Hash{}, // 不做检查，不需要真实Merkle根
		Timestamp:  time.Unix(0, 0),
		Bits:       0x1d00ffff, // 通常是难度目标
		Nonce:      0,
	})

	var buf []byte
	for _, txHex := range txHexes {
		tx, err := wallet.DecodeMsgTx(txHex)
		if err != nil {
			return nil, err
		}

		block.AddTransaction(tx)
		buf = append(buf, []byte(tx.TxID())...)
	}
	block.Header.MerkleRoot = chainhash.DoubleHashH(buf)

	return block, nil
}

func BuildBlockFromHexTxs_SatsNet(txHexes []string) (*swire.MsgBlock, error) {
	block := swire.NewMsgBlock(&swire.BlockHeader{
		// 随便填一些字段（不检查）
		Version:    0x20000000,
		PrevBlock:  schainhash.Hash{}, // 可以根据需要替换为某个真实区块hash
		MerkleRoot: schainhash.Hash{}, // 不做检查，不需要真实Merkle根
		Timestamp:  time.Unix(0, 0),
		Bits:       0x1d00ffff, // 通常是难度目标
		Nonce:      0,
	})

	var buf []byte
	for _, txHex := range txHexes {
		tx, err := wallet.DecodeMsgTx_SatsNet(txHex)
		if err != nil {
			return nil, err
		}

		block.AddTransaction(tx)
		buf = append(buf, []byte(tx.TxID())...)
	}
	block.Header.MerkleRoot = schainhash.DoubleHashH(buf)

	return block, nil
}

func (p *Client) GetBlock(blockHash string) (string, error) {
	p.Network.Mutex.RLock()
	defer p.Network.Mutex.RUnlock()

	height, err := strconv.Atoi(blockHash)
	if err != nil {
		return "", err
	}

	txIdList := p.Network.Blocks[height]
	if len(txIdList) == 0 {
		return "", fmt.Errorf("noblock %d", height)
	}
	txHexList := make([]string, 0)
	for _, txId := range txIdList {
		txHexList = append(txHexList, p.Network.TxBroadcasted[txId])
	}
	var buf bytes.Buffer
	if p.Network.IsBitcoinNet() {
		b, err := BuildBlockFromHexTxs(txHexList)
		if err != nil {
			return "", err
		}

		err = b.Serialize(&buf)
		if err != nil {
			return "", err
		}
	} else {
		b, err := BuildBlockFromHexTxs_SatsNet(txHexList)
		if err != nil {
			return "", err
		}

		err = b.Serialize(&buf)
		if err != nil {
			return "", err
		}
	}

	return hex.EncodeToString(buf.Bytes()), nil
}

func (p *Client) GetAssetSummaryWithAddress(address string) *indexerwire.AssetSummary {
	p.Network.Mutex.RLock()
	defer p.Network.Mutex.RUnlock()

	pkScript, err := wallet.AddrToPkScript(address, wallet.GetChainParam())
	if err != nil {
		wallet.Log.Errorf("invalid address %s, %v", address, err)
		return nil
	}

	assetmap := make(map[swire.AssetName]*swire.AssetInfo, 0)
	for i, utxo := range p.Network.Utxos {
		if p.Network.UtxoUsed[utxo] != "" {
			continue
		}

		pkScript2, _ := hex.DecodeString(p.Network.State.PkScripts[p.Network.UtxoOwner[i]])
		if bytes.Equal(pkScript, pkScript2) {
			assets := p.Network.UtxoAssets[i]
			if len(assets) != 0 {
				invalids := p.Network.Invalids[utxo]
				hasAsset := false
				for _, asset := range assets {
					if invalids != nil {
						invalid := invalids[asset.Name]
						if invalid {
							// 该资产无效
							continue
						}
					}
					hasAsset = true
					assets2, ok2 := assetmap[asset.Name]
					if ok2 {
						assets2.Add(&asset)
					} else {
						assetmap[asset.Name] = asset.Clone()
					}
				}
				if !hasAsset {
					assets2, ok2 := assetmap[indexer.ASSET_PLAIN_SAT]
					if ok2 {
						assets2.Amount = *assets2.Amount.Add(indexer.NewDefaultDecimal(p.Network.UtxoValue[i]))
					} else {
						assetmap[indexer.ASSET_PLAIN_SAT] = &swire.AssetInfo{
							Name:       indexer.ASSET_PLAIN_SAT,
							Amount:     *indexer.NewDefaultDecimal(p.Network.UtxoValue[i]),
							BindingSat: 1,
						}
					}
				}

				if p.Network.IsBitcoinNet() {

				} else {
					// 聪网上，看看聪数量是不是比绑定资产的聪多，如果多，就加入白聪
					bindingSatsNum := assets.GetBindingSatAmout()
					if p.Network.UtxoValue[i] > bindingSatsNum {
						plain := p.Network.UtxoValue[i] - bindingSatsNum
						assets2, ok := assetmap[indexer.ASSET_PLAIN_SAT]
						if ok {
							assets2.Amount = *assets2.Amount.Add(indexer.NewDefaultDecimal(plain))
						} else {
							assetmap[indexer.ASSET_PLAIN_SAT] = &swire.AssetInfo{
								Name:       indexer.ASSET_PLAIN_SAT,
								Amount:     *indexer.NewDefaultDecimal(plain),
								BindingSat: 1,
							}
						}
					}
				}
			} else {
				assets2, ok := assetmap[indexer.ASSET_PLAIN_SAT]
				if ok {
					assets2.Amount = *assets2.Amount.Add(indexer.NewDefaultDecimal(p.Network.UtxoValue[i]))
				} else {
					assetmap[indexer.ASSET_PLAIN_SAT] = &swire.AssetInfo{
						Name:       indexer.ASSET_PLAIN_SAT,
						Amount:     *indexer.NewDefaultDecimal(p.Network.UtxoValue[i]),
						BindingSat: 1,
					}
				}
			}
		}
	}

	// 增加brc20的资产数量
	addr := hex.EncodeToString(pkScript)
	brc20AssetMap := p.Network.AddrAssetMap[addr]
	for k, v := range brc20AssetMap {
		if v.IsZero() {
			continue
		}
		assetmap[k] = &indexer.AssetInfo{
			Name:       k,
			Amount:     *v.Clone(),
			BindingSat: 0,
		}
	}

	result := make([]*swire.AssetInfo, 0)
	for _, v := range assetmap {
		result = append(result, v)
	}

	return &indexerwire.AssetSummary{
		ListResp: indexerwire.ListResp{
			Start: 0,
			Total: uint64(len(result)),
		},
		Data: result,
	}
}

func (p *Client) GetIndexerPubKey() ([]byte, error) {
	return hex.DecodeString(indexer.GetBootstrapPubKey())
}

func (p *Client) GetUtxoListWithTicker(address string, ticker *swire.AssetName) []*indexerwire.TxOutputInfo {
	return p.getUtxoListWithTicker(address, ticker, false)
}

func (p *Client) GetAllUtxosWithAddress(address string) []*indexerwire.TxOutputInfo {
	return p.getUtxoListWithTicker(address, nil, false)
}

func (p *Client) GetUtxoListWithBRC20Ticker(address string, ticker *swire.AssetName, invalid bool) []*indexerwire.TxOutputInfo {
	return p.getUtxoListWithTicker(address, ticker, invalid)
}

func (p *Client) getUtxoListWithTicker(address string, ticker *swire.AssetName, includeInvalid bool) []*indexerwire.TxOutputInfo {
	p.Network.Mutex.RLock()
	defer p.Network.Mutex.RUnlock()

	pkScript, err := wallet.AddrToPkScript(address, wallet.GetChainParam())
	if err != nil {
		wallet.Log.Errorf("invalid address %s, %v", address, err)
		return nil
	}

	// 过滤无效资产

	outputs := make([]*indexerwire.TxOutputInfo, 0)
	for i, utxo := range p.Network.Utxos {

		if p.Network.UtxoUsed[utxo] != "" {
			continue
		}

		var output *indexerwire.TxOutputInfo
		assets := p.Network.UtxoAssets[i]
		invalids := p.Network.Invalids[utxo]
		validAssets := make(swire.TxAssets, 0, len(assets))
		invalidAssets := make(swire.TxAssets, 0, len(assets))
		for _, asset := range assets {
			if invalids != nil && invalids[asset.Name] {
				invalidAssets = append(invalidAssets, asset)
				continue
			}
			validAssets = append(validAssets, asset)
		}

		if ticker == nil {
			output = &indexerwire.TxOutputInfo{
				OutPoint: utxo,
			}
		} else if *ticker != indexer.ASSET_PLAIN_SAT {
			if includeInvalid {
				if invalidAssets != nil {
					_, err := invalidAssets.Find(ticker)
					if err == nil {
						output = &indexerwire.TxOutputInfo{
							OutPoint: utxo,
						}
					}
				}
			} else {
				if validAssets != nil {
					_, err := validAssets.Find(ticker)
					if err == nil {
						output = &indexerwire.TxOutputInfo{
							OutPoint: utxo,
						}
					}
				}
			}
		} else {
			if len(validAssets) == 0 {
				output = &indexerwire.TxOutputInfo{
					OutPoint: utxo,
				}
			} else {
				if !p.Network.IsBitcoinNet() && p.Network.UtxoValue[i] > validAssets.GetBindingSatAmout() {
					output = &indexerwire.TxOutputInfo{
						OutPoint: utxo,
					}
				}
			}
		}

		var selectedAssets swire.TxAssets
		if includeInvalid {
			selectedAssets = invalidAssets
		} else {
			selectedAssets = validAssets
		}

		if output != nil {
			pkScript2, _ := hex.DecodeString(p.Network.State.PkScripts[p.Network.UtxoOwner[i]])
			if bytes.Equal(pkScript, pkScript2) {

				offsets := cloneOffsets(p.Network.Offsets[i])
				var utxoAssets []*indexer.DisplayAsset
				for _, v := range selectedAssets {
					asset := indexer.DisplayAsset{
						AssetName:  v.Name,
						Amount:     v.Amount.String(),
						Precision:  v.Amount.Precision,
						BindingSat: int(v.BindingSat),
						Offsets:    offsets[v.Name],
					}
					if v.Name.Protocol == indexer.PROTOCOL_NAME_BRC20 && p.Network.IsBitcoinNet() {
						asset.Offsets = []*indexer.OffsetRange{{Start: 0, End: 1}}
						asset.OffsetToAmts = []*indexer.OffsetToAmount{{Offset: 0, Amount: v.Amount.String()}}
						asset.Invalid = includeInvalid
					}
					utxoAssets = append(utxoAssets, &asset)
				}

				parts := strings.Split(utxo, ":")
				h, _ := p.getTxHeight(parts[0])
				output.UtxoId = indexer.ToUtxoId(h, 0, 0)
				output.Value = p.Network.UtxoValue[i]
				output.Assets = utxoAssets
				output.PkScript = pkScript
				outputs = append(outputs, output)
			}
		}
	}
	sort.Slice(outputs, func(i, j int) bool {
		return outputs[i].Value > outputs[j].Value
	})
	return outputs
}

func (p *Client) GetUtxosWithAddress(address string) (map[string]*wire.TxOut, error) {
	p.Network.Mutex.RLock()
	defer p.Network.Mutex.RUnlock()

	pkScript, err := wallet.AddrToPkScript(address, wallet.GetChainParam())
	if err != nil {
		wallet.Log.Errorf("invalid address %s, %v", address, err)
		return nil, nil
	}

	outputs := make(map[string]*wire.TxOut, 0)
	for i, utxo := range p.Network.Utxos {
		if p.Network.UtxoUsed[utxo] != "" {
			continue
		}

		assets := p.Network.UtxoAssets[i]
		if len(assets) == 0 {
			pkScript2, _ := hex.DecodeString(p.Network.State.PkScripts[p.Network.UtxoOwner[i]])

			if bytes.Equal(pkScript, pkScript2) {
				outputs[utxo] = &wire.TxOut{Value: p.Network.UtxoValue[i], PkScript: pkScript}
			}
		}
	}
	return outputs, nil
}

func (p *Client) GetUnusableUtxosWithAddress(address string) ([]*TxOutput, error) {
	return nil, fmt.Errorf("not implemented")
}

// sat/vb
func (p *Client) GetFeeRate() int64 {
	return 1
}

func (p *Client) GetExistingUtxos(utxos []string) ([]string, error) {
	p.Network.Mutex.RLock()
	defer p.Network.Mutex.RUnlock()

	result := make([]string, 0)
	for _, utxo := range utxos {
		if p.Network.UtxoUsed[utxo] != "" {
			continue
		}
		_, ok := p.Network.UtxoIndex[utxo]
		if ok {
			result = append(result, utxo)
		}
	}
	return result, nil
}

func (p *State) insertPkScript(pkScript []byte) int {
	str := hex.EncodeToString(pkScript)
	for i, b := range p.PkScripts {
		if b == str {
			return i
		}
	}
	p.PkScripts = append(p.PkScripts, str)
	return len(p.PkScripts) - 1
}

func (p *Client) TestRawTx_Bitcoin(signedTxs []string) error {
	return nil
}

func (p *Client) TestRawTx_SatsNet(signedTxs []string) error {
	return nil
}

func (p *Client) BroadCastTx(tx *wire.MsgTx) (string, error) {

	txHex, err := wallet.EncodeMsgTx(tx)
	if err != nil {
		return "", err
	}
	fmt.Printf("BroadCastTx TX: %s\n%s\n", tx.TxID(), txHex)

	p.Network.Mutex.Lock()
	defer p.Network.Mutex.Unlock()

	_, ok := p.Network.TxBroadcasted[tx.TxID()]
	if ok {
		return tx.TxID(), nil
	}
	p.Network.TxBroadcasted[tx.TxID()] = txHex

	txs := p.Network.Blocks[p.Network.Height]
	txs = append(txs, tx.TxID())
	p.Network.Blocks[p.Network.Height] = txs

	if string(tx.TxIn[0].SignatureScript) == "genesis" {
		for i, txOut := range tx.TxOut {
			utxo := fmt.Sprintf("%s:%d", tx.TxID(), i)
			p.Network.Utxos = append(p.Network.Utxos, utxo)
			index := len(p.Network.Utxos) - 1
			p.Network.UtxoIndex[utxo] = index
			if index >= len(p.Network.UtxoValue) {
				p.Network.UtxoValue = append(p.Network.UtxoValue, txOut.Value)
			}
			if index >= len(p.Network.UtxoAssets) {
				p.Network.UtxoAssets = append(p.Network.UtxoAssets, nil)
			}
			if index >= len(p.Network.Offsets) {
				p.Network.Offsets = append(p.Network.Offsets, nil)
			}
			if index >= len(p.Network.UtxoOwner) {
				j := p.Network.State.insertPkScript(txOut.PkScript)
				p.Network.UtxoOwner = append(p.Network.UtxoOwner, j)
			}

			if p.Network.IsBitcoinNet() {
				utxoAssets := p.Network.UtxoAssets[index]
				addr := hex.EncodeToString(txOut.PkScript)
				assetmap, ok := p.Network.AddrAssetMap[addr]
				if !ok {
					assetmap = make(map[swire.AssetName]*Decimal)
					p.Network.AddrAssetMap[addr] = assetmap
				}
				for _, asset := range utxoAssets {
					if asset.Name.Protocol == indexer.PROTOCOL_NAME_BRC20 {
						total := assetmap[asset.Name]
						assetmap[asset.Name] = total.Add(&asset.Amount)

						// 设置invalid
						// utxoAssets = nil
						// utxoOffsets = nil
						invalidmap, ok := p.Network.Invalids[utxo]
						if !ok {
							invalidmap = make(map[swire.AssetName]bool)
							p.Network.Invalids[utxo] = invalidmap
						}
						invalidmap[asset.Name] = true
					}
				}
			}

		}
		p.generateNewBlock()
		fmt.Printf("bitcoin genesis txId %s\n", tx.TxID())
		return tx.TxID(), nil
	}

	// 尝试为一层数据分配资产
	// 按ordx协议的规则
	// 按runes协议的规则
	// var assetName *swire.AssetName
	// var offsets indexer.AssetOffsets
	var input *TxOutput
	status := 0 //
	//var transferFrom *Brc20Transfer
	//var assetAmt *Decimal
	for _, txIn := range tx.TxIn {
		utxo := txIn.PreviousOutPoint.String()
		if txIn.PreviousOutPoint.Index < swire.AnchorTxOutIndex {
			spendTx, ok := p.Network.UtxoUsed[utxo]
			if ok {
				return "", fmt.Errorf("utxo %s spent in %s", utxo, spendTx)
			}
		}
		transferFrom, ok := p.Network.TransferInfo[utxo]
		if ok {
			from := transferFrom.Address
			transferMap, ok := p.Network.AddrTransferMap[from]
			if !ok {
				return "", fmt.Errorf("can't find transfer map")
			}
			utxomap, ok := transferMap[*transferFrom.AssetName]
			if !ok {
				return "", fmt.Errorf("can't find utxo map")
			}

			assetmap, ok := p.Network.AddrAssetMap[from]
			if !ok {
				return "", fmt.Errorf("no asset to transfer")
			}
			total := assetmap[*transferFrom.AssetName]
			if total.Cmp(transferFrom.Amt) < 0 {
				return "", fmt.Errorf("no enough asset to transfer")
			}
			assetmap[*transferFrom.AssetName] = total.Sub(transferFrom.Amt)

			delete(utxomap, transferFrom.Utxo)
			delete(p.Network.TransferInfo, transferFrom.Utxo)
			status = 3
		}

		p.Network.UtxoUsed[utxo] = tx.TxID()
		index, ok := p.Network.UtxoIndex[utxo]
		if !ok {
			return "", fmt.Errorf("can't find utxo %s", utxo)
		}
		value := p.Network.UtxoValue[index]
		txAssets := p.Network.UtxoAssets[index]
		txOffsets := p.Network.Offsets[index]
		satBindingMap := make(map[int64]*indexer.AssetInfo)
		if p.Network.IsBitcoinNet() && len(txAssets) == 1 && txAssets[0].Name.Protocol == indexer.PROTOCOL_NAME_BRC20 {
			if len(txOffsets) != 1 {
				wallet.Log.Panic("")
			}
			for k, v := range txOffsets {
				if len(v) != 1 && k != txAssets[0].Name {
					wallet.Log.Panic("")
				}
				satBindingMap[v[0].Start] = txAssets[0].Clone()
			}
		}

		in := indexer.TxOutput{
			OutValue: wire.TxOut{
				Value: value,
			},
			Assets:        txAssets,
			Offsets:       cloneOffsets(txOffsets),
			SatBindingMap: satBindingMap,
			Invalids:      make(map[indexer.AssetName]bool),
		}
		invalidmap, existing := p.Network.Invalids[utxo]
		if existing {
			for assetName, invalid := range invalidmap {
				in.Invalids[assetName] = invalid
			}
		}

		if input == nil {
			input = &in
		} else {
			input.Append(&in)
		}

		// 增加对ordx部署和铸造的支持
		inscriptions, _, err := indexer.ParseInscription(txIn.Witness)
		if err != nil {
			continue
		}

		for i, insc := range inscriptions {

			protocol, content := indexer.GetProtocol(insc)
			switch protocol {
			case "ordx":
				ordxInfo, bOrdx := indexer.IsOrdXProtocol(insc)
				if !bOrdx {
					continue
				}
				ordxType := indexer.GetBasicContent(ordxInfo)
				switch ordxType.Op {
				case "deploy":
					deployInfo := indexer.ParseDeployContent(ordxInfo)
					if deployInfo == nil {
						fmt.Printf("ParseDeployContent failed, %v", err)
						continue
					}
					assetName := indexer.AssetName{
						Protocol: "ordx",
						Type:     "f",
						Ticker:   deployInfo.Ticker,
					}
					_, ok := p.Network.State.Tickers[assetName.String()]
					if ok {
						fmt.Printf("ticker %s exists", deployInfo.Ticker)
						continue
					}
					n := 1
					if deployInfo.N != "" {
						n, err = strconv.Atoi(deployInfo.N)
						if err != nil {
							fmt.Printf("Atoi %s failed, %v", deployInfo.N, err)
							continue
						}
					}
					tickerInfo := indexer.TickerInfo{
						AssetName:    assetName,
						Divisibility: 0,
						N:            n,
						Limit:        deployInfo.Lim,
						TotalMinted:  "0",
						MaxSupply:    deployInfo.Max,
					}
					p.Network.State.Tickers[assetName.String()] = &tickerInfo

				case "mint":
					mintInfo := indexer.ParseMintContent(ordxInfo)
					if mintInfo == nil {
						fmt.Printf("ParseMintContent failed, %v", err)
						continue
					}
					assetName := indexer.AssetName{
						Protocol: "ordx",
						Type:     "f",
						Ticker:   mintInfo.Ticker,
					}
					ticker, ok := p.Network.State.Tickers[assetName.String()]
					if !ok {
						fmt.Printf("ticker %s not exists", mintInfo.Ticker)
						continue
					}

					amt := ticker.Limit
					if mintInfo.Amt != "" {
						amt = mintInfo.Amt
					}
					dAmt, err := indexer.NewDecimalFromString(amt, 0)
					if err != nil {
						fmt.Printf("NewDecimalFromString %s failed, %v", amt, err)
						continue
					}

					asset := indexer.AssetInfo{
						Name:       assetName,
						Amount:     *dAmt,
						BindingSat: uint32(ticker.N),
					}
					input.Assets.Add(&asset)
					satsNum := indexer.GetBindingSatNum(dAmt, uint32(ticker.N))
					input.Offsets[assetName] = indexer.AssetOffsets{&indexer.OffsetRange{Start: 0, End: satsNum}}
				}

			case "brc-20":
				brc20Content := indexer.ParseBrc20BaseContent(string(content))
				if brc20Content == nil {
					continue
				}
				switch brc20Content.Op {
				case "deploy":
					deployInfo := indexer.ParseBrc20DeployContent(string(content))
					if deployInfo == nil {
						continue
					}
					if len(deployInfo.Ticker) == 5 {
						if deployInfo.SelfMint != "true" {
							wallet.Log.Errorf("deploy, tick length 5, but not self_mint")
							continue
						}
					}
					assetName := indexer.AssetName{
						Protocol: "brc20",
						Type:     "f",
						Ticker:   deployInfo.Ticker,
					}

					_, ok := p.Network.State.Tickers[assetName.String()]
					if ok {
						wallet.Log.Warnf("ticker %s exists", deployInfo.Ticker)
						continue
					}

					dec, err := strconv.Atoi(deployInfo.Decimal)
					if err != nil {
						wallet.Log.Warnf("invalid dec %s", deployInfo.Decimal)
					}
					tickerInfo := indexer.TickerInfo{
						AssetName:    assetName,
						Divisibility: dec,
						Limit:        deployInfo.Lim,
						TotalMinted:  "0",
						MaxSupply:    deployInfo.Max,
					}
					p.Network.State.Tickers[assetName.String()] = &tickerInfo

				case "mint":
					mintInfo := indexer.ParseBrc20MintContent(string(content))
					if mintInfo == nil {
						continue
					}

					assetName := indexer.AssetName{
						Protocol: "brc20",
						Type:     "f",
						Ticker:   mintInfo.Ticker,
					}
					ticker, ok := p.Network.State.Tickers[assetName.String()]
					if !ok {
						fmt.Printf("ticker %s not exists", mintInfo.Ticker)
						continue
					}

					amt := ticker.Limit
					if mintInfo.Amt != "" {
						amt = mintInfo.Amt
					}
					dAmt, err := indexer.NewDecimalFromString(amt, 0)
					if err != nil {
						fmt.Printf("NewDecimalFromString %s failed, %v", amt, err)
						continue
					}

					asset := indexer.AssetInfo{
						Name:       assetName,
						Amount:     *dAmt,
						BindingSat: 0,
					}
					// 假装是从这个输入转移到输出
					input.Assets.Add(&asset)
					input.Offsets[assetName] = indexer.AssetOffsets{&indexer.OffsetRange{Start: 0, End: 1}}
					input.SatBindingMap[0] = asset.Clone()
					status = 1

				case "transfer":
					transferInfo := indexer.ParseBrc20TransferContent(string(content))
					if transferInfo == nil {
						continue
					}

					assetName := indexer.AssetName{
						Protocol: "brc20",
						Type:     "f",
						Ticker:   transferInfo.Ticker,
					}
					ticker, ok := p.Network.State.Tickers[assetName.String()]
					if !ok {
						fmt.Printf("ticker %s not exists", transferInfo.Ticker)
						continue
					}

					amt := transferInfo.Amt
					dAmt, err := indexer.NewDecimalFromString(amt, ticker.Divisibility)
					if err != nil {
						fmt.Printf("NewDecimalFromString %s failed, %v", amt, err)
						continue
					}

					asset := indexer.AssetInfo{
						Name:       assetName,
						Amount:     *dAmt.Clone(),
						BindingSat: 0,
					}
					// 假装是从这个输入转移到输出，在输出的地方，检查是否有足够的资产可以转移
					input.Assets.Add(&asset)
					input.Offsets[assetName] = indexer.AssetOffsets{&indexer.OffsetRange{Start: 0, End: 1}}
					input.SatBindingMap[0] = asset.Clone()
					status = 2
				}

			case "sns":
				domain := indexer.ParseDomainContent(string(insc[indexer.FIELD_CONTENT]))
				if domain == nil {
					domain = indexer.ParseDomainContent(string(content))
				}
				if domain != nil {
					switch domain.Op {
					case "reg":

					case "update":
						var updateInfo *indexer.OrdxUpdateContentV2
						// 如果有metadata，那么不处理FIELD_CONTENT的内容
						if string(insc[indexer.FIELD_META_PROTOCOL]) == "sns" && insc[indexer.FIELD_META_DATA] != nil {
							updateInfo = indexer.ParseUpdateContent(string(content))
							updateInfo.P = "sns"
							value, ok := updateInfo.KVs["key"]
							if ok {
								// 这个有什么用？
								delete(updateInfo.KVs, "key")
								updateInfo.KVs[value] = fmt.Sprintf("%si%d", tx.TxID(), i)
							}
						} else {
							updateInfo = indexer.ParseUpdateContent(string(insc[indexer.FIELD_CONTENT]))
						}

						if updateInfo != nil {
							nameInfo, ok := p.Network.State.Names[updateInfo.Name]
							if ok {
								for k, v := range updateInfo.KVs {
									found := false
									for _, item := range nameInfo.KVItemList {
										if item.Key == k {
											item.Value = v
											item.InscriptionId = fmt.Sprintf("%si%d", tx.TxID(), i)
											found = true
											break
										}
									}
									if !found {
										nameInfo.KVItemList = append(nameInfo.KVItemList, &indexerwire.KVItem{
											Key:           k,
											Value:         v,
											InscriptionId: fmt.Sprintf("%si%d", tx.TxID(), i),
										})
									}
								}
							}
						}
					}
				}

			default:
				// 可能是名字
				if protocol == "" {
					content := insc[indexer.FIELD_CONTENT]
					if len(content) <= indexer.MAX_NAME_LEN {
						name := string(content)
						tickerInfo := indexer.TickerInfo{
							AssetName: indexer.AssetName{
								Protocol: indexer.PROTOCOL_NAME_ORDX,
								Type:     indexer.ASSET_TYPE_NS,
								Ticker:   name,
							},
							Divisibility: 0,
							Limit:        "1",
							TotalMinted:  "1",
							MaxSupply:    "1",
							N:            1,
						}
						p.Network.State.Tickers[tickerInfo.AssetName.String()] = &tickerInfo

						asset := indexer.AssetInfo{
							Name:       tickerInfo.AssetName,
							Amount:     *indexer.NewDecimal(1, 0),
							BindingSat: 1,
						}
						input.Assets.Add(&asset)
						input.Offsets[tickerInfo.AssetName] = indexer.AssetOffsets{&indexer.OffsetRange{Start: 0, End: 1}}
					}
				}
			}
		}
	}

	var runesOutputUtxo, firstRuneOutputUtxo, premineOutput string
	var runePointer *uint32
	var edicts []runestone.Edict
	var premineAssetInfo *indexer.AssetInfo
	for i, txOut := range tx.TxOut {
		utxo := fmt.Sprintf("%s:%d", tx.TxID(), i)
		p.Network.Utxos = append(p.Network.Utxos, utxo)
		index := len(p.Network.Utxos) - 1
		p.Network.UtxoIndex[utxo] = index
		p.Network.UtxoValue = append(p.Network.UtxoValue, txOut.Value)
		j := p.Network.State.insertPkScript(txOut.PkScript)
		p.Network.UtxoOwner = append(p.Network.UtxoOwner, j)

		var curr *indexer.TxOutput
		if input == nil {
			input = indexer.NewTxOutput(0)
		}
		curr, input, err = input.Cut(txOut.Value)
		if err != nil {
			return "", fmt.Errorf("allocate output %d: %w", i, err)
		}

		var utxoAssets swire.TxAssets
		var utxoOffsets map[swire.AssetName]indexer.AssetOffsets
		utxoAssets = curr.Assets
		utxoOffsets = curr.Offsets
		if p.Network.IsBitcoinNet() && len(utxoAssets) == 1 && utxoAssets[0].Name.Protocol == indexer.PROTOCOL_NAME_BRC20 {
			// status = 1 或者2，一个tx中只有一个，但3可能有多个output
			switch status {
			case 1: // brc20 mint
				addr := hex.EncodeToString(txOut.PkScript)
				assetmap, ok := p.Network.AddrAssetMap[addr]
				if !ok {
					assetmap = make(map[swire.AssetName]*Decimal)
					p.Network.AddrAssetMap[addr] = assetmap
				}
				asset := utxoAssets[0]
				total := assetmap[asset.Name]
				assetmap[asset.Name] = total.Add(&asset.Amount)

				// 设置invalid
				// utxoAssets = nil
				// utxoOffsets = nil
				invalidmap, ok := p.Network.Invalids[utxo]
				if !ok {
					invalidmap = make(map[swire.AssetName]bool)
					p.Network.Invalids[utxo] = invalidmap
				}
				invalidmap[asset.Name] = true
			case 2: // brc20 transfer
				addr := hex.EncodeToString(txOut.PkScript)
				assetmap, ok := p.Network.AddrAssetMap[addr]
				if !ok {
					return "", fmt.Errorf("no asset to transfer")
				}
				asset := utxoAssets[0]
				total := assetmap[asset.Name]
				if total.Cmp(&asset.Amount) < 0 {
					return "", fmt.Errorf("no enough asset to transfer")
				}
				transferMap, ok := p.Network.AddrTransferMap[addr]
				if !ok {
					transferMap = make(map[swire.AssetName]map[string]bool)
					p.Network.AddrTransferMap[addr] = transferMap
				}
				utxomap, ok := transferMap[asset.Name]
				if !ok {
					utxomap = make(map[string]bool)
					transferMap[asset.Name] = utxomap
				}
				utxomap[utxo] = true

				p.Network.TransferInfo[utxo] = &Brc20Transfer{
					Utxo:      utxo,
					Address:   addr,
					AssetName: &asset.Name,
					Amt:       asset.Amount.Clone(),
				}

			case 3:
				// brc20 的转移
				to := hex.EncodeToString(txOut.PkScript)
				assetInfo := utxoAssets[0]
				assetmap, ok := p.Network.AddrAssetMap[to]
				if !ok {
					assetmap = make(map[swire.AssetName]*Decimal)
					p.Network.AddrAssetMap[to] = assetmap
				}
				total := assetmap[assetInfo.Name]
				assetmap[assetInfo.Name] = total.Add(&assetInfo.Amount)

				// 暂时保留，但是设置为invalid
				// utxoAssets = nil
				// utxoOffsets = nil
				invalidmap, ok := p.Network.Invalids[utxo]
				if !ok {
					invalidmap = make(map[swire.AssetName]bool)
					p.Network.Invalids[utxo] = invalidmap
				}
				invalidmap[assetInfo.Name] = true

			default:

			}
		}
		p.Network.UtxoAssets = append(p.Network.UtxoAssets, utxoAssets)
		p.Network.Offsets = append(p.Network.Offsets, utxoOffsets)

		if wallet.IsOpReturn(txOut.PkScript) {
			stone := runestone.Runestone{}
			result, err := stone.DecipherFromPkScript(txOut.PkScript)
			if err == nil {
				if result.Runestone != nil {
					etching := result.Runestone.Etching
					if etching != nil {
						spacerRune := runestone.NewSpacedRune(*etching.Rune, *etching.Spacers)
						assetName := indexer.AssetName{
							Protocol: indexer.PROTOCOL_NAME_RUNES,
							Type:     indexer.ASSET_TYPE_FT,
							Ticker:   spacerRune.String(),
						}
						divisibility := uint8(0)
						if etching.Divisibility != nil {
							divisibility = *etching.Divisibility
						}
						supply := indexer.NewDecimalFromUint128(*etching.Supply(), int(divisibility))
						tickInfo := indexer.TickerInfo{
							AssetName:    assetName,
							DisplayName:  fmt.Sprintf("%d:%d", (p.Network.Height), len(p.Network.Blocks[p.Network.Height])),
							Divisibility: int(divisibility),
							MaxSupply:    supply.String(),
						}
						p.Network.State.Tickers[assetName.String()] = &tickInfo

						if etching.Premine != nil {
							vout := uint32(0)
							if result.Runestone.Pointer != nil {
								vout = *result.Runestone.Pointer
							} else {
								if i == 0 {
									vout = 1
								}
							}
							amount := indexer.NewDecimalFromUint128(*etching.Premine, tickInfo.Divisibility)
							premineAssetInfo = &indexer.AssetInfo{
								Name:       assetName,
								Amount:     *amount,
								BindingSat: 0,
							}
							premineOutput = fmt.Sprintf("%s:%d", tx.TxID(), vout)
						}
					}

					edicts = result.Runestone.Edicts
					runePointer = result.Runestone.Pointer
				}
			}
		} else {
			if runesOutputUtxo == "" {
				runesOutputUtxo = utxo
			}
			if firstRuneOutputUtxo == "" {
				for _, asset := range utxoAssets {
					if asset.Name.Protocol == indexer.PROTOCOL_NAME_RUNES {
						firstRuneOutputUtxo = utxo
						break
					}
				}
			}
		}
	}

	if premineOutput != "" {
		index, ok := p.Network.UtxoIndex[premineOutput]
		if !ok {
			return "", fmt.Errorf("can't find utxo %s", premineOutput)
		}
		err = p.Network.UtxoAssets[index].Add(premineAssetInfo)
		if err != nil {
			return "", err
		}
	}

	if runePointer != nil {
		runesOutputUtxo = fmt.Sprintf("%s:%d", tx.TxID(), *runePointer)
		if firstRuneOutputUtxo != "" && firstRuneOutputUtxo != runesOutputUtxo {
			index1, ok := p.Network.UtxoIndex[firstRuneOutputUtxo]
			if !ok {
				return "", fmt.Errorf("can't find utxo %s", firstRuneOutputUtxo)
			}
			index2, ok := p.Network.UtxoIndex[runesOutputUtxo]
			if !ok {
				return "", fmt.Errorf("can't find utxo %s", runesOutputUtxo)
			}
			for i := 0; i < len(p.Network.UtxoAssets[index1]); {
				asset := p.Network.UtxoAssets[index1][i]
				if asset.Name.Protocol != indexer.PROTOCOL_NAME_RUNES {
					i++
					continue
				}
				err = p.Network.UtxoAssets[index2].Add(&asset)
				if err != nil {
					return "", err
				}
				err = p.Network.UtxoAssets[index1].Subtract(&asset)
				if err != nil {
					return "", err
				}
			}
		}
	}

	// 执行runes的转移规则
	index1, ok := p.Network.UtxoIndex[runesOutputUtxo]
	if len(edicts) != 0 && !ok {
		return "", fmt.Errorf("can't find utxo %s", runesOutputUtxo)
	}
	for _, edict := range edicts {
		if int(edict.Output) >= len(tx.TxOut) {
			return "", fmt.Errorf("invalid edict %v", edict)
		}
		index2, ok := p.Network.UtxoIndex[fmt.Sprintf("%s:%d", tx.TxID(), edict.Output)]
		if !ok {
			return "", fmt.Errorf("invalid edict %v", edict)
		}
		tickerInfo, err := p.Network.State.GetTickerInfoByRuneId(edict.ID.String())
		if err != nil {
			return "", err
		}
		assetName := tickerInfo.AssetName

		amount := indexer.NewDecimalFromUint128(edict.Amount, tickerInfo.Divisibility)

		asset := indexer.AssetInfo{
			Name:       assetName,
			Amount:     *amount,
			BindingSat: 0,
		}

		err = p.Network.UtxoAssets[index1].Subtract(&asset)
		if err != nil {
			return "", err
		}
		err = p.Network.UtxoAssets[index2].Add(&asset)
		if err != nil {
			return "", err
		}
	}

	fmt.Printf("BroadCastTx succeeded. %s\n", tx.TxID())
	return tx.TxID(), nil
}

func (p *Client) BroadCastTxs(txs []*wire.MsgTx) error {
	for i, tx := range txs {
		txId, err := p.BroadCastTx(tx)
		if err != nil {
			return fmt.Errorf("BroadCastTx %d failed, %v", i, err)
		}
		fmt.Printf("%d %s broadcasted", i, txId)
	}

	return nil
}

func (p *Client) BroadCastTx_SatsNet(tx *swire.MsgTx) (string, error) {

	txHex, err := wallet.EncodeMsgTx_SatsNet(tx)
	if err != nil {
		return "", err
	}

	fmt.Printf("BroadCastTx_SatsNet TX: %s\n%s\n", tx.TxID(), txHex)

	p.Network.Mutex.Lock()
	defer p.Network.Mutex.Unlock()

	_, ok := p.Network.TxBroadcasted[tx.TxID()]
	if ok {
		return tx.TxID(), nil
	}
	p.Network.TxBroadcasted[tx.TxID()] = txHex

	txs := p.Network.Blocks[p.Network.Height]
	txs = append(txs, tx.TxID())
	p.Network.Blocks[p.Network.Height] = txs

	if string(tx.TxIn[0].SignatureScript) == "genesis" {
		for i, txOut := range tx.TxOut {
			utxo := fmt.Sprintf("%s:%d", tx.TxID(), i)
			p.Network.Utxos = append(p.Network.Utxos, utxo)
			index := len(p.Network.Utxos) - 1
			p.Network.UtxoIndex[utxo] = index
			if index >= len(p.Network.UtxoValue) {
				p.Network.UtxoValue = append(p.Network.UtxoValue, txOut.Value)
			}
			if index >= len(p.Network.UtxoAssets) {
				p.Network.UtxoAssets = append(p.Network.UtxoAssets, txOut.Assets)
			}
			if index >= len(p.Network.Offsets) {
				p.Network.Offsets = append(p.Network.Offsets, nil)
			}
			if index >= len(p.Network.UtxoOwner) {
				j := p.Network.State.insertPkScript(txOut.PkScript)
				p.Network.UtxoOwner = append(p.Network.UtxoOwner, j)
			}
		}
		p.generateNewBlock()
		fmt.Printf("satsnet genesis txId %s\n", tx.TxID())
		return tx.TxID(), nil
	}

	var inputAddress string
	var anchorData *wallet.AnchorData
	for _, txIn := range tx.TxIn {
		utxo := txIn.PreviousOutPoint.String()
		if txIn.PreviousOutPoint.Index == swire.AnchorTxOutIndex {
			anchorData, _, err = wallet.CheckAnchorPkScript(tx.TxIn[0].SignatureScript, tx.TxOut, false)
			if err == nil {
				p.Network.AscendMap[anchorData.Utxo] = tx.TxID()
			}
		} else if txIn.PreviousOutPoint.Index < swire.AnchorTxOutIndex {
			spendTx, ok := p.Network.UtxoUsed[utxo]
			if ok {
				return "", fmt.Errorf("utxo %s spent in %s", utxo, spendTx)
			}
			if inputAddress == "" {
				index, ok := p.Network.UtxoIndex[utxo]
				if ok {
					pkScript, err := hex.DecodeString(p.Network.State.PkScripts[p.Network.UtxoOwner[index]])
					if err == nil {
						inputAddress, _ = getSatsNetAddressFromPkScript(pkScript)
					}
				}
			}
		}
		p.Network.UtxoUsed[utxo] = tx.TxID()
	}

	var descendTxOut *swire.TxOut
	var descendTxId string
	for i, txOut := range tx.TxOut {
		utxo := fmt.Sprintf("%s:%d", tx.TxID(), i)
		p.Network.Utxos = append(p.Network.Utxos, utxo)
		index := len(p.Network.Utxos) - 1
		p.Network.UtxoIndex[utxo] = index
		p.Network.UtxoValue = append(p.Network.UtxoValue, txOut.Value)
		p.Network.UtxoAssets = append(p.Network.UtxoAssets, txOut.Assets)
		p.Network.Offsets = append(p.Network.Offsets, nil)
		j := p.Network.State.insertPkScript(txOut.PkScript)
		p.Network.UtxoOwner = append(p.Network.UtxoOwner, j)

		ctype, data, err := sindexer.ReadDataFromNullDataScript(txOut.PkScript)
		if err == nil {
			switch ctype {
			case sindexer.CONTENT_TYPE_DESCENDING:
				descendTxOut = txOut
				descendTxId = string(data)
				p.Network.DescendMap[string(data)] = utxo

			case sindexer.CONTENT_TYPE_STAKE:
				if anchorData != nil {
					p.addTestMinerNodeFromAnchor(anchorData, tx.TxID())
				}

			case sindexer.CONTENT_TYPE_UNSTAKE:
				if descendTxOut != nil {
					p.removeTestMinerNodeFromDescend(inputAddress, p.Network.Height, descendTxOut, data, descendTxId)
				}
			}
		}
	}

	fmt.Printf("BroadCastTx_SatsNet succeeded. %s\n", tx.TxID())
	return tx.TxID(), nil
}

func (p *Client) addTestMinerNodeFromAnchor(data *wallet.AnchorData, anchorTxId string) {
	pubA, pubB, err := getAnchorPubKeysForTest(data)
	if err != nil {
		fmt.Printf("addTestMinerNodeFromAnchor get pubkeys failed: %v\n", err)
		return
	}
	if !hasTestStakeEligibility(p.Network.Height, data.Assets) {
		return
	}

	channelAddr, err := wallet.GetP2WSHaddress(pubA, pubB)
	if err != nil {
		fmt.Printf("addTestMinerNodeFromAnchor GetP2WSHaddress failed: %v\n", err)
		return
	}
	parentKey := hex.EncodeToString(pubA)
	nodeKey := hex.EncodeToString(pubB)
	info := (&sindexer.AscendData{
		Height:      p.Network.Height,
		FundingUtxo: data.Utxo,
		AnchorTxId:  anchorTxId,
		Value:       data.Value,
		Assets:      data.Assets,
		Sig:         data.Sig,
		Address:     channelAddr,
		PubA:        pubA,
		PubB:        pubB,
	}).ToMinerInfo()

	if parentKey == indexer.GetBootstrapPubKey() {
		p.Network.State.CoreNodes[nodeKey] = true
		p.Network.State.MinerInfo[nodeKey] = info
		p.Network.State.addTestCoreNodeChild(parentKey, nodeKey, p.Network.Height, data.Utxo)
		if _, ok := p.Network.State.CoreChildren[nodeKey]; !ok {
			p.Network.State.CoreChildren[nodeKey] = make(map[string]*sindexer.MinerAscendInfo)
		}
		fmt.Printf("test indexer add core node %s at height %d\n", nodeKey, p.Network.Height)
		return
	}

	if _, ok := p.Network.State.CoreNodes[parentKey]; !ok {
		return
	}
	p.Network.State.MinerInfo[nodeKey] = info
	p.Network.State.addTestCoreNodeChild(parentKey, nodeKey, p.Network.Height, data.Utxo)
	fmt.Printf("test indexer add miner node %s under %s at height %d\n", nodeKey, parentKey, p.Network.Height)
}

func (p *Client) removeTestMinerNodeFromDescend(channelAddr string, height int,
	descendTxOut *swire.TxOut, data []byte, descendTxId string) {

	assetName, amt, err := sindexer.ParseStakeInvoice(data)
	if err != nil {
		fmt.Printf("removeTestMinerNodeFromDescend invalid unstake data: %v\n", err)
		return
	}
	if assetName != indexer.GetStakeAssetName(height) {
		fmt.Printf("removeTestMinerNodeFromDescend invalid stake asset %s\n", assetName)
		return
	}
	info, err := descendTxOut.Assets.Find(indexer.NewAssetNameFromString(assetName))
	if err != nil || info.Amount.Cmp(amt) != 0 {
		fmt.Printf("removeTestMinerNodeFromDescend invalid descend asset %s %s\n", assetName, amt.String())
		return
	}

	nodeKey, minerInfo := p.Network.State.findTestMinerByChannel(channelAddr)
	if nodeKey == "" || minerInfo == nil {
		fmt.Printf("removeTestMinerNodeFromDescend can't find miner from channel %s\n", channelAddr)
		return
	}

	if _, ok := p.Network.State.CoreNodes[nodeKey]; ok {
		if len(p.Network.State.CoreChildren[nodeKey]) != 0 {
			fmt.Printf("removeTestMinerNodeFromDescend core node %s still has child miners\n", nodeKey)
			return
		}
		p.Network.State.deleteTestCoreNodeChild(minerInfo.ServerNode, nodeKey)
		delete(p.Network.State.CoreNodes, nodeKey)
		delete(p.Network.State.CoreChildren, nodeKey)
		delete(p.Network.State.MinerInfo, nodeKey)
		fmt.Printf("test indexer remove core node %s at tx %s\n", nodeKey, descendTxId)
		return
	}

	p.Network.State.deleteTestCoreNodeChild(minerInfo.ServerNode, nodeKey)
	delete(p.Network.State.MinerInfo, nodeKey)
	fmt.Printf("test indexer remove miner node %s at tx %s\n", nodeKey, descendTxId)
}

func getAnchorPubKeysForTest(data *wallet.AnchorData) ([]byte, []byte, error) {
	addrType, addresses, _, err := stxscript.ExtractPkScriptAddrs(data.WitnessScript, wallet.GetChainParam_SatsNet())
	if err != nil {
		return nil, nil, err
	}
	if addrType != stxscript.MultiSigTy || len(addresses) != 2 {
		return nil, nil, fmt.Errorf("invalid anchor witness script")
	}
	pubA := addresses[0].ScriptAddress()
	pubB := addresses[1].ScriptAddress()

	invoice, err := sindexer.StandardAnchorScript(data.Utxo, data.WitnessScript, data.Value, data.Assets)
	if err != nil {
		return nil, nil, err
	}
	pubKeyA, err := utils.BytesToPublicKey(pubA)
	if err != nil {
		return nil, nil, fmt.Errorf("BytesToPublicKey failed. %v", err)
	}
	if wallet.VerifyMessage(pubKeyA, invoice, data.Sig) {
		return pubA, pubB, nil
	}
	pubKeyB, err := utils.BytesToPublicKey(pubB)
	if err != nil {
		return nil, nil, fmt.Errorf("BytesToPublicKey failed. %v", err)
	}
	if wallet.VerifyMessage(pubKeyB, invoice, data.Sig) {
		return pubB, pubA, nil
	}
	return nil, nil, fmt.Errorf("anchor signature is not signed by either multisig pubkey")
}

func hasTestStakeEligibility(height int, assets swire.TxAssets) bool {
	assetName := indexer.GetStakeAssetNameWithHeightL2(height)
	assetAmt := indexer.GetStakeAssetAmtWithHeightL2(height)
	for _, asset := range assets {
		if asset.Name.String() == assetName {
			return asset.Amount.Int64() >= assetAmt
		}
	}
	return false
}

func (p *State) addTestCoreNodeChild(parentKey, childKey string, height int, ascendUtxo string) {
	childMap, ok := p.CoreChildren[parentKey]
	if !ok {
		childMap = make(map[string]*sindexer.MinerAscendInfo)
		p.CoreChildren[parentKey] = childMap
	}
	childMap[childKey] = &sindexer.MinerAscendInfo{
		AscendHeight: height,
		AscendUtxo:   ascendUtxo,
	}
}

func (p *State) deleteTestCoreNodeChild(parentKey, childKey string) {
	if childMap, ok := p.CoreChildren[parentKey]; ok {
		delete(childMap, childKey)
	}
}

func (p *State) findTestMinerByChannel(channelAddr string) (string, *sindexer.MinerInfo) {
	for nodeKey, info := range p.MinerInfo {
		if info.ChannelAddr == channelAddr {
			return nodeKey, info
		}
	}
	return "", nil
}

func getSatsNetAddressFromPkScript(pkScript []byte) (string, error) {
	_, addresses, _, err := stxscript.ExtractPkScriptAddrs(pkScript, wallet.GetChainParam_SatsNet())
	if err != nil {
		return "", err
	}
	if len(addresses) == 0 {
		return "", fmt.Errorf("can't extract satsnet address")
	}
	return addresses[0].EncodeAddress(), nil
}

func (p *Client) BroadCastTxs_SatsNet(txs []*swire.MsgTx) error {
	for _, tx := range txs {
		_, err := p.BroadCastTx_SatsNet(tx)
		if err != nil {
			return err
		}
	}

	return nil
}

func (p *Client) GetTickInfo(assetName *swire.AssetName) *indexer.TickerInfo {
	p.Network.Mutex.RLock()
	defer p.Network.Mutex.RUnlock()

	ticker, ok := p.Network.State.Tickers[assetName.String()]
	if !ok {
		return nil
	}

	return ticker
}

func (p *Client) AllowDeployTick(assetName *swire.AssetName) error {
	return nil
}

func (p *Client) GetUtxoSpentTx(utxo string) (string, error) {
	txId := p.Network.UtxoUsed[utxo]
	if txId != "" {
		return txId, nil
	}
	return "", fmt.Errorf("not spent")
}

func (p *Client) GetServiceIncoming(addr string) (int, int64, error) {
	return 0, 0, fmt.Errorf("not implemented")
}

func (p *Client) GetNonce(pubKey []byte) ([]byte, error) {
	if p.Network.Name != "satoshinet" {
		return nil, fmt.Errorf("DKVS is only supported by the fake L2 indexer")
	}
	nonce := sha256.Sum256(pubKey)
	return nonce[:], nil
}

func (p *Client) PutKVs(req *indexerwire.PutKValueReq) error {
	if p.Network.Name != "satoshinet" {
		return fmt.Errorf("DKVS is only supported by the fake L2 indexer")
	}
	p.Network.Mutex.Lock()
	defer p.Network.Mutex.Unlock()
	if p.Network.KvCache == nil {
		p.Network.KvCache = make(map[string]*indexerwire.KeyValue)
	}
	for _, value := range req.Values {
		if value == nil {
			continue
		}
		p.Network.KvCache[KVCacheKey(req.PubKey, value.Key)] = cloneFakeKeyValue(value)
	}
	return nil
}

func (p *Client) DelKVs(req *indexerwire.DelKValueReq) error {
	if p.Network.Name != "satoshinet" {
		return fmt.Errorf("DKVS is only supported by the fake L2 indexer")
	}
	p.Network.Mutex.Lock()
	defer p.Network.Mutex.Unlock()
	for _, key := range req.Keys {
		delete(p.Network.KvCache, KVCacheKey(req.PubKey, key))
	}
	return nil
}

func (p *Client) GetKV(pubkey []byte, key string) (*indexerwire.KeyValue, error) {
	if p.Network.Name != "satoshinet" {
		return nil, fmt.Errorf("DKVS is only supported by the fake L2 indexer")
	}
	p.Network.Mutex.RLock()
	defer p.Network.Mutex.RUnlock()
	value := p.Network.KvCache[KVCacheKey(pubkey, key)]
	if value == nil {
		return nil, fmt.Errorf("DKVS key not found: %s", key)
	}
	return cloneFakeKeyValue(value), nil
}

func KVCacheKey(pubkey []byte, key string) string {
	return hex.EncodeToString(pubkey) + "\x00" + key
}

func cloneFakeKeyValue(value *indexerwire.KeyValue) *indexerwire.KeyValue {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.Value = append([]byte(nil), value.Value...)
	cloned.PubKey = append([]byte(nil), value.PubKey...)
	cloned.Signature = append([]byte(nil), value.Signature...)
	return &cloned
}

const DKVSEndpointID = "transcend-fake-l2"

func cloneFakeDKVSRecord(record *swire.DKVSRecord) *swire.DKVSRecord {
	if record == nil {
		return nil
	}
	cloned := *record
	cloned.Value = append([]byte(nil), record.Value...)
	cloned.PubKey = append([]byte(nil), record.PubKey...)
	cloned.Signature = append([]byte(nil), record.Signature...)
	cloned.FeeProof = append([]byte(nil), record.FeeProof...)
	return &cloned
}

func cloneFakeDKVSKeyState(state dkvsindexer.DKVSKeyState) dkvsindexer.DKVSKeyState {
	state.Record = cloneFakeDKVSRecord(state.Record)
	return state
}

func fakeDKVSResponse(code int, msg string, data interface{}) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"code": code,
		"msg":  msg,
		"data": data,
	})
}

func fakeDKVSErrorResponse(err error) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"code":       -1,
		"msg":        err.Error(),
		"error_code": dkvsindexer.ErrorCodeOf(err),
		"data":       nil,
	})
}

func (p *Client) DKVSClientConfig() (*dkvsindexer.ClientConfig, error) {
	if p.Network.Name != "satoshinet" {
		return nil, fmt.Errorf("DKVS is only supported by the fake L2 indexer")
	}
	return &dkvsindexer.ClientConfig{
		FreeLocal:              dkvsindexer.DefaultFreeLocalCachePolicy(),
		Blob:                   dkvsindexer.DefaultBlobPolicy(),
		MaxBatchMutations:      dkvsindexer.MaxBatchCASMutations,
		MaxBatchBytes:          dkvsindexer.MaxBatchCASTotalSize,
		MaxPrefixesPerTerminal: dkvsindexer.MaxPrefixesPerTerminal,
		EndpointID:             DKVSEndpointID,
	}, nil
}

func (p *Client) SendGetRequest(url *wallet.URL) ([]byte, error) {
	return p.SendDKVSGet(url.Path, url.Query)
}

func (p *Client) SendPostRequest(url *wallet.URL, body []byte) ([]byte, error) {
	return p.SendDKVSPost(url.Path, body)
}

func (p *Client) SendDKVSGet(path string, query map[string]string) ([]byte, error) {
	if p.Network.Name != "satoshinet" {
		return nil, fmt.Errorf("DKVS is only supported by the fake L2 indexer")
	}
	switch path {
	case "/v3/dkvs/config":
		config, err := p.DKVSClientConfig()
		if err != nil {
			return nil, err
		}
		return fakeDKVSResponse(0, "ok", config)
	case "/v3/dkvs/record":
		key := query["key"]
		p.Network.Mutex.RLock()
		record := cloneFakeDKVSRecord(p.Network.DkvsRecords[key])
		p.Network.Mutex.RUnlock()
		if record == nil {
			return fakeDKVSErrorResponse(dkvsindexer.ErrRecordNotFound)
		}
		return json.Marshal(map[string]interface{}{
			"code": 0, "msg": "ok", "data": record,
			"etag": dkvsindexer.RecordHash(record).String(),
		})
	case "/v3/dkvs/key-state":
		key := query["key"]
		p.Network.Mutex.RLock()
		state := p.Network.fakeDKVSKeyStateLocked(key)
		p.Network.Mutex.RUnlock()
		return fakeDKVSResponse(0, "ok", &state)
	default:
		return nil, fmt.Errorf("unsupported fake L2 DKVS GET path %s", path)
	}
}

func (p *Client) SendDKVSPost(path string, body []byte) ([]byte, error) {
	if p.Network.Name != "satoshinet" {
		return nil, fmt.Errorf("DKVS is only supported by the fake L2 indexer")
	}
	switch path {
	case "/v3/dkvs/records/batch-cas":
		var req wallet.DKVSBatchCASRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return p.fakeDKVSBatchCAS(req)
	case "/v3/dkvs/prefixes/read":
		var req struct {
			Prefix string `json:"prefix"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return p.fakeDKVSPrefixRead(req.Prefix)
	default:
		return nil, fmt.Errorf("unsupported fake L2 DKVS POST path %s", path)
	}
}

func (p *Client) fakeDKVSBatchCAS(req wallet.DKVSBatchCASRequest) ([]byte, error) {
	if len(req.Mutations) == 0 {
		return fakeDKVSErrorResponse(dkvsindexer.ErrInvalidRecord)
	}

	p.Network.Mutex.Lock()
	defer p.Network.Mutex.Unlock()
	if p.Network.DkvsRecords == nil {
		p.Network.DkvsRecords = make(map[string]*swire.DKVSRecord)
	}
	if p.Network.DkvsDeleted == nil {
		p.Network.DkvsDeleted = make(map[string]dkvsindexer.DKVSKeyState)
	}
	if p.Network.DkvsGenerations == nil {
		p.Network.DkvsGenerations = make(map[string]uint64)
	}

	result := &dkvsindexer.WriteResult{
		Applied:      len(req.Mutations),
		Records:      make([]*swire.DKVSRecord, 0, len(req.Mutations)),
		Hashes:       make([]string, 0, len(req.Mutations)),
		ViewHeight:   p.Network.fakeDKVSViewHeightLocked(),
		ServerTimeMS: uint64(time.Now().UnixMilli()),
		EndpointID:   DKVSEndpointID,
		RequestID:    req.RequestID,
	}
	for _, mutation := range req.Mutations {
		if mutation.Record == nil {
			return fakeDKVSErrorResponse(dkvsindexer.ErrInvalidRecord)
		}
		record := cloneFakeDKVSRecord(mutation.Record)
		path, err := dkvsindexer.CollectionPathForKey(record.Key)
		if err != nil {
			return fakeDKVSErrorResponse(err)
		}
		hash := dkvsindexer.RecordHash(record).String()
		if dkvsindexer.IsTombstone(record.Flags) {
			delete(p.Network.DkvsRecords, record.Key)
			p.Network.DkvsDeleted[record.Key] = dkvsindexer.DKVSKeyState{
				Key: record.Key, Status: dkvsindexer.KeyStateDeleted, Seq: record.Seq, ETag: hash,
			}
		} else {
			p.Network.DkvsRecords[record.Key] = record
			delete(p.Network.DkvsDeleted, record.Key)
		}
		// This mirrors PathMeta.EndpointGeneration: every mutation visible on
		// this endpoint advances the collection, including FREE_LOCAL writes.
		p.Network.DkvsGenerations[path]++
		result.Records = append(result.Records, cloneFakeDKVSRecord(record))
		result.Hashes = append(result.Hashes, hash)
	}
	result.ViewHeight = p.Network.fakeDKVSViewHeightLocked()
	for _, mutation := range req.Mutations {
		if fakeDKVSRecordIsFreeLocal(mutation.Record) {
			result.LocalOnly = true
			break
		}
	}
	return fakeDKVSResponse(0, "ok", result)
}

func (p *Network) fakeDKVSPrefixRecordsLocked(prefix string) ([]*swire.DKVSRecord, []dkvsindexer.DKVSKeyState) {
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
	records := make([]*swire.DKVSRecord, 0)
	states := make([]dkvsindexer.DKVSKeyState, 0)
	for key, record := range p.DkvsRecords {
		if fakeDKVSMatchesPrefix(key, prefix) {
			records = append(records, cloneFakeDKVSRecord(record))
			states = append(states, p.fakeDKVSKeyStateLocked(key))
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Key < records[j].Key })
	sort.Slice(states, func(i, j int) bool { return states[i].Key < states[j].Key })
	return records, states
}

func (p *Client) fakeDKVSPrefixRead(prefix string) ([]byte, error) {
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
	p.Network.Mutex.RLock()
	defer p.Network.Mutex.RUnlock()
	records, states := p.Network.fakeDKVSPrefixRecordsLocked(prefix)
	return fakeDKVSResponse(0, "ok", &dkvsindexer.PrefixReadResult{
		EndpointID: DKVSEndpointID, Prefix: prefix, ViewHeight: p.Network.fakeDKVSViewHeightLocked(),
		Records: records, KeyStates: states,
	})
}

func (p *Network) fakeDKVSKeyStateLocked(key string) dkvsindexer.DKVSKeyState {
	if record := p.DkvsRecords[key]; record != nil {
		return dkvsindexer.DKVSKeyState{
			Key: key, Status: dkvsindexer.KeyStateActive, Seq: record.Seq,
			ETag: dkvsindexer.RecordHash(record).String(), ExpiryHeight: dkvsindexer.RecordExpiryHeight(record),
			StorageMode: fakeDKVSStorageMode(record), Record: cloneFakeDKVSRecord(record),
		}
	}
	if state, ok := p.DkvsDeleted[key]; ok {
		return cloneFakeDKVSKeyState(state)
	}
	return dkvsindexer.DKVSKeyState{Key: key, Status: dkvsindexer.KeyStateNeverSeen}
}

func (p *Network) fakeDKVSViewHeightLocked() uint64 {
	height := uint64(1)
	for _, record := range p.DkvsRecords {
		if record != nil && record.IssueHeight > height {
			height = record.IssueHeight
		}
	}
	return height
}

func fakeDKVSMatchesPrefix(key, prefix string) bool {
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
	return key == prefix || strings.HasPrefix(key, prefix+"/")
}

func fakeDKVSStorageMode(record *swire.DKVSRecord) dkvsindexer.StorageMode {
	if record != nil {
		if proof, err := dkvsindexer.ParseFeeProof(record.FeeProof); err == nil {
			switch proof.Mode {
			case dkvsindexer.FeeModeFreeLocal:
				return dkvsindexer.StorageModeFreeLocal
			case dkvsindexer.FeeModeAutopay:
				return dkvsindexer.StorageModeAutopay
			default:
				return dkvsindexer.StorageModePaid
			}
		}
	}
	return dkvsindexer.StorageModeFreeLocal
}

func fakeDKVSRecordIsFreeLocal(record *swire.DKVSRecord) bool {
	return fakeDKVSStorageMode(record) == dkvsindexer.StorageModeFreeLocal
}

// 传回该name绑定的所有kv
func (p *Client) GetNameInfo(name string) (*indexerwire.OrdinalsName, error) {
	info, ok := p.Network.State.Names[name]
	if ok {
		return info, nil
	}
	return nil, fmt.Errorf("not found")
}

func (p *Client) GetNamesWithKey(address, key string) (
	[]*indexerwire.OrdinalsName, error) {
	return nil, fmt.Errorf("not implemented")
}
