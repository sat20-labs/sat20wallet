package wallet

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	schainhash "github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	swire "github.com/sat20-labs/satoshinet/wire"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/runes/runestone"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
)

type network struct {
	mutex         sync.RWMutex
	name          string
	height        int // 当前写入高度（未确定高度）， bestheight = height - 1
	lastTime      int64
	blocks        map[int][]string  // height -> txId list
	txBroadcasted map[string]string // txId -> txHex
	utxos         []string
	utxoUsed      map[string]string // utxo -> spent in the txId
	utxoIndex     map[string]int
	utxoValue     []int64
	utxoAssets    []indexer.TxAssets
	offsets       []map[swire.AssetName]indexer.AssetOffsets
	utxoOwner     []int
	ascendMap     map[string]string  // utxo->anchorTxId
	descendMap    map[string]string  // deanchorTxId->utxo
}

func InitTickerInfo(txId string) {
	_tickerInfoRunning = make(map[string]*indexer.TickerInfo)
	for k, v := range _tickerInfo {
		v.DeployTx = txId
		_tickerInfoRunning[k] = v
	}
}

func CloneTickerInfo() map[string]*indexer.TickerInfo {
	tickerInfo := make(map[string]*indexer.TickerInfo)
	for k, v := range _tickerInfoRunning {
		tickerInfo[k] = v
	}
	return tickerInfo
}

func SetTickerInfo(another map[string]*indexer.TickerInfo) {
	_tickerInfoRunning = make(map[string]*indexer.TickerInfo)
	for k, v := range another {
		_tickerInfoRunning[k] = v
	}
}

func GetTickerInfoByRuneId(runeId string) (*indexer.TickerInfo, error) {
	for _, v := range _tickerInfoRunning {
		if v.DisplayName == runeId {
			return v, nil
		}
	}
	return nil, fmt.Errorf("not found %s", runeId)
}

func (p *network) Clone() *network {
	n := &network{}

	n.name = p.name
	n.height = p.height
	n.lastTime = p.lastTime

	n.blocks = make(map[int][]string)
	for k, v := range p.blocks {
		txList := make([]string, len(v))
		copy(txList, v)
		n.blocks[k] = v
	}

	n.txBroadcasted = make(map[string]string)
	for k, v := range p.txBroadcasted {
		n.txBroadcasted[k] = v
	}

	n.utxos = make([]string, len(p.utxos))
	copy(n.utxos, p.utxos)

	n.utxoUsed = make(map[string]string)
	for k, v := range p.utxoUsed {
		n.utxoUsed[k] = v
	}

	n.utxoIndex = make(map[string]int)
	for k, v := range p.utxoIndex {
		n.utxoIndex[k] = v
	}

	n.utxoValue = make([]int64, len(p.utxoValue))
	copy(n.utxoValue, p.utxoValue)

	n.utxoAssets = make([]indexer.TxAssets, len(p.utxoAssets))
	for i, assets := range p.utxoAssets {
		var newAssets indexer.TxAssets
		newAssets = append(newAssets, assets.Clone()...)
		n.utxoAssets[i] = newAssets
	}

	n.offsets = make([]map[swire.AssetName]indexer.AssetOffsets, len(p.offsets))
	for i, offsetmap := range p.offsets {
		n.offsets[i] = cloneOffsets(offsetmap)
	}

	n.utxoOwner = make([]int, len(p.utxoOwner))
	copy(n.utxoOwner, p.utxoOwner)

	n.ascendMap = make(map[string]string)
	for k, v := range p.ascendMap {
		n.ascendMap[k] = v
	}

	n.descendMap = make(map[string]string)
	for k, v := range p.descendMap {
		n.descendMap[k] = v
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

func (p *network) Set(another *network) {
	n := another.Clone()
	p.name = n.name
	p.height = n.height
	p.blocks = n.blocks
	p.lastTime = n.lastTime
	p.txBroadcasted = n.txBroadcasted
	p.utxos = n.utxos
	p.utxoUsed = n.utxoUsed
	p.utxoIndex = n.utxoIndex
	p.utxoValue = n.utxoValue
	p.utxoAssets = n.utxoAssets
	p.offsets = n.offsets
	p.utxoOwner = n.utxoOwner
	p.ascendMap = n.ascendMap
	p.descendMap = n.descendMap
}

func (p *network) IsBitcoinNet() bool {
	return p.name == "bitcoin"
}

var _network1 = &network{
	name:          "bitcoin",
	height:        0,
	blocks:        make(map[int][]string),
	txBroadcasted: make(map[string]string),
	utxos:         make([]string, 0),
	utxoUsed:      make(map[string]string),
	utxoIndex:     make(map[string]int),
	ascendMap:     make(map[string]string),
	descendMap:    make(map[string]string),

	utxoValue: []int64{
		20000, 2000000, 20000, 2000000,
		1000000, 100000, 10000, 10000, // 4-
		10000, 10000, 10000, 10000, // 8-
		330, 546, 600, 1000, // 12-
		10000, 90000, 10000, 10000, // 16-
		100000, 100000, 10000, 10000, // 20-
		1000, 1000, 1000, 1000, // 24-
		10000, 1000, 1010000, 1000, // 28,29,30,31
		10000, 10000, 10000, 10000, // 32-
	},

	utxoAssets: []indexer.TxAssets{
		nil, nil, nil, nil,

		// 4-
		{
			{Name: swire.AssetName{Protocol: "ordx", Type: "f", Ticker: "pizza"}, Amount: *indexer.NewDefaultDecimal(1000000), BindingSat: 1},
		},
		{
			{Name: swire.AssetName{Protocol: "ordx", Type: "f", Ticker: "pizza"}, Amount: *indexer.NewDefaultDecimal(90000), BindingSat: 1},
		},
		{
			{Name: swire.AssetName{Protocol: "ordx", Type: "f", Ticker: "pearl"}, Amount: *indexer.NewDefaultDecimal(8000), BindingSat: 1},
		},
		{
			{Name: swire.AssetName{Protocol: "ordx", Type: "f", Ticker: "pearl"}, Amount: *indexer.NewDefaultDecimal(6000), BindingSat: 1},
		},

		// 8-
		{
			{Name: swire.AssetName{Protocol: "runes", Type: "f", Ticker: "TEST•FIRST•TEST"}, Amount: *indexer.NewDefaultDecimal(1000), BindingSat: 0},
		},
		{
			{Name: swire.AssetName{Protocol: "runes", Type: "f", Ticker: "TEST•FIRST•TEST"}, Amount: *indexer.NewDefaultDecimal(100000000), BindingSat: 0},
		},
		{
			{Name: swire.AssetName{Protocol: "runes", Type: "f", Ticker: "TEST•SECOND•TEST"}, Amount: *indexer.NewDecimal(1000000, 2), BindingSat: 0},
		},
		{
			{Name: swire.AssetName{Protocol: "runes", Type: "f", Ticker: "TEST•SECOND•TEST"}, Amount: *indexer.NewDecimal(1000, 2), BindingSat: 0},
		},

		// 12-
		{
			{Name: swire.AssetName{Protocol: "brc20", Type: "f", Ticker: "ordi"}, Amount: *indexer.NewDecimal(10000000, 4), BindingSat: 0},
		},
		{
			{Name: swire.AssetName{Protocol: "brc20", Type: "f", Ticker: "ordi"}, Amount: *indexer.NewDecimal(1000000, 4), BindingSat: 0},
		},
		{
			{Name: swire.AssetName{Protocol: "brc20", Type: "f", Ticker: "ordi"}, Amount: *indexer.NewDecimal(100000, 4), BindingSat: 0},
		},
		{
			{Name: swire.AssetName{Protocol: "brc20", Type: "f", Ticker: "ordi"}, Amount: *indexer.NewDecimal(10000, 4), BindingSat: 0},
		},

		// 16-
		{
			{Name: swire.AssetName{Protocol: "ordx", Type: "f", Ticker: "pizza"}, Amount: *indexer.NewDefaultDecimal(10000), BindingSat: 1},
		},
		{
			{Name: swire.AssetName{Protocol: "ordx", Type: "f", Ticker: "dogcoin"}, Amount: *indexer.NewDefaultDecimal(90000), BindingSat: 1},
		},
		{
			{Name: swire.AssetName{Protocol: "ordx", Type: "e", Ticker: "vintage"}, Amount: *indexer.NewDefaultDecimal(8000), BindingSat: 1},
		},
		{
			{Name: swire.AssetName{Protocol: "ordx", Type: "e", Ticker: "vintage"}, Amount: *indexer.NewDefaultDecimal(6000), BindingSat: 1},
		},

		// 20-
		{
			{Name: swire.AssetName{Protocol: "ordx", Type: "f", Ticker: "satoshilpt"}, Amount: *indexer.NewDefaultDecimal(100000000), BindingSat: 1000},
		},
		nil, nil, nil,

		// 24-
		{
			{Name: swire.AssetName{Protocol: "ordx", Type: "f", Ticker: "pizza"}, Amount: *indexer.NewDefaultDecimal(1000), BindingSat: 1},
		},
		{
			{Name: swire.AssetName{Protocol: "runes", Type: "f", Ticker: "TEST•FIRST•TEST"}, Amount: *indexer.NewDefaultDecimal(10000), BindingSat: 0},
		},
		{
			{Name: swire.AssetName{Protocol: "brc20", Type: "f", Ticker: "ordi"}, Amount: *indexer.NewDecimal(100000000, 4), BindingSat: 0},
		},
		nil,

		// 28,29,30,31
		nil,
		{
			{Name: swire.AssetName{Protocol: "ordx", Type: "f", Ticker: "satoshilpt"}, Amount: *indexer.NewDefaultDecimal(10000000), BindingSat: 1000},
		},
		{
			{Name: swire.AssetName{Protocol: "ordx", Type: "f", Ticker: "pearl"}, Amount: *indexer.NewDefaultDecimal(1000000), BindingSat: 1},
		},
		{
			{Name: swire.AssetName{Protocol: "ordx", Type: "f", Ticker: "pearl.lpt"}, Amount: *indexer.NewDefaultDecimal(1000000), BindingSat: 1000},
		},

		// 32-
		nil, nil, nil, nil,
	},

	offsets: []map[swire.AssetName]indexer.AssetOffsets{
		nil, nil, nil, nil,

		{{Protocol: "ordx", Type: "f", Ticker: "pizza"}: {{Start: 0, End: 1000000}}},
		{{Protocol: "ordx", Type: "f", Ticker: "pizza"}: {{Start: 0, End: 90000}}},
		{{Protocol: "ordx", Type: "f", Ticker: "pearl"}: {{Start: 1000, End: 9000}}},
		{{Protocol: "ordx", Type: "f", Ticker: "pearl"}: {{Start: 0, End: 1000}, {Start: 3000, End: 4000}, {Start: 5000, End: 9000}}},

		nil, nil, nil, nil,
		nil, nil, nil, nil,

		{{Protocol: "ordx", Type: "f", Ticker: "pizza"}: {{Start: 0, End: 10000}}},
		{{Protocol: "ordx", Type: "f", Ticker: "dogcoin"}: {{Start: 0, End: 90000}}},
		{{Protocol: "ordx", Type: "e", Ticker: "vintage"}: {{Start: 1000, End: 9000}}},
		{{Protocol: "ordx", Type: "e", Ticker: "vintage"}: {{Start: 0, End: 1000}, {Start: 3000, End: 4000}, {Start: 5000, End: 9000}}},

		{{Protocol: "ordx", Type: "f", Ticker: "satoshilpt"}: {{Start: 0, End: 100000}}},
		nil, nil, nil,

		{{Protocol: "ordx", Type: "f", Ticker: "pizza"}: {{Start: 0, End: 1000}}},
		nil, nil, nil,

		// 28,29,30,31
		nil,
		{{Protocol: "ordx", Type: "f", Ticker: "satoshilpt"}: {{Start: 0, End: 10000}}},
		{{Protocol: "ordx", Type: "f", Ticker: "pearl"}: {{Start: 1000, End: 2000}, {Start: 3000, End: 4000}, {Start: 5000, End: 1003000}}},
		{{Protocol: "ordx", Type: "f", Ticker: "pearl.lpt"}: {{Start: 0, End: 1000}}},

		nil, nil, nil, nil,
	},

	utxoOwner: []int{
		1, 1, 0, 0, // 4
		0, 0, 0, 0, // 8
		0, 0, 0, 0, // 12
		0, 0, 0, 0, // 16
		0, 0, 0, 0, // 20
		0, 0, 0, 0, // 24
		3, 3, 3, 3, // 28
		1, 1, 1, 1, // 32
		1, 1, 1, 1,
	},
}

var _network2 = &network{
	name:          "satoshinet",
	height:        0,
	blocks:        make(map[int][]string),
	txBroadcasted: make(map[string]string),

	utxos:     make([]string, 0),
	utxoUsed:  make(map[string]string),
	utxoIndex: make(map[string]int),
	ascendMap:     make(map[string]string),
	descendMap:    make(map[string]string),

	utxoValue: []int64{
		20000, 20000, 20000, 2000000,
		10000000, 1000, 1000, 1000000,
		10000000, 1000, 1000, 1000000,
		20000, 20000, 20000, 2000000,
	},

	utxoAssets: []indexer.TxAssets{
		nil, nil, nil, nil,

		{
			{Name: swire.AssetName{Protocol: "ordx", Type: "f", Ticker: "pizza"}, Amount: *indexer.NewDefaultDecimal(10000000), BindingSat: 1},
		},
		{
			{Name: swire.AssetName{Protocol: "runes", Type: "f", Ticker: "TEST•FIRST•TEST"}, Amount: *indexer.NewDefaultDecimal(100000000), BindingSat: 0},
		},
		{
			{Name: swire.AssetName{Protocol: "brc20", Type: "f", Ticker: "ordi"}, Amount: *indexer.NewDecimal(100000000, 4), BindingSat: 0},
		},
		nil,

		{
			{Name: swire.AssetName{Protocol: "ordx", Type: "f", Ticker: "pizza"}, Amount: *indexer.NewDefaultDecimal(10000000), BindingSat: 1},
		},
		{
			{Name: swire.AssetName{Protocol: "runes", Type: "f", Ticker: "TEST•FIRST•TEST"}, Amount: *indexer.NewDefaultDecimal(100000000), BindingSat: 0},
		},
		{
			{Name: swire.AssetName{Protocol: "brc20", Type: "f", Ticker: "ordi"}, Amount: *indexer.NewDecimal(100000000, 4), BindingSat: 0},
		},
		{
			{Name: swire.AssetName{Protocol: "ordx", Type: "f", Ticker: "satoshilpt"}, Amount: *indexer.NewDefaultDecimal(100000000), BindingSat: 1000},
		},

		nil, nil, nil, nil,
	},

	offsets: []map[swire.AssetName]indexer.AssetOffsets{
		nil, nil, nil, nil,
		{{Protocol: "ordx", Type: "f", Ticker: "pizza"}: {{Start: 0, End: 10000000}}},
		nil, nil, nil,

		{{Protocol: "ordx", Type: "f", Ticker: "pizza"}: {{Start: 0, End: 10000000}}},
		nil, nil, nil,

		nil, nil, nil, nil,
	},

	utxoOwner: []int{
		0, 0, 0, 0,
		3, 3, 3, 3,
		0, 0, 0, 0,
		1, 1, 1, 1,
	},
}


var _coreNodeMap = map[string]bool { // pubkey
	"":true,
}

var _tickerInfoRunning map[string]*indexer.TickerInfo
var _tickerInfo = map[string]*indexer.TickerInfo{
	"runes:f:TEST•FIRST•TEST": {
		AssetName: swire.AssetName{
			Protocol: indexer.PROTOCOL_NAME_RUNES,
			Type:     indexer.ASSET_TYPE_FT,
			Ticker:   "TEST•FIRST•TEST",
		},
		DisplayName:  "111:1",
		Divisibility: 0,
		Limit:        "1000",
		TotalMinted:  "100000000",
		MaxSupply:    "100000000",
	},
	"runes:f:TEST•SECOND•TEST": {
		AssetName: swire.AssetName{
			Protocol: indexer.PROTOCOL_NAME_RUNES,
			Type:     indexer.ASSET_TYPE_FT,
			Ticker:   "TEST•SECOND•TEST",
		},
		DisplayName:  "222:2",
		Divisibility: 2,
		Limit:        "1000",
		TotalMinted:  "21000000",
		MaxSupply:    "100000000",
	},
	"runes:f:TEST•THIRD•TEST": {
		AssetName: swire.AssetName{
			Protocol: indexer.PROTOCOL_NAME_RUNES,
			Type:     indexer.ASSET_TYPE_FT,
			Ticker:   "TEST•THIRD•TEST",
		},
		DisplayName:  "333:3",
		Divisibility: 1,
		Limit:        "1000",
		TotalMinted:  "21000000",
		MaxSupply:    "100000000000000100000000000000",
	},
	"brc20:f:ordi": {
		AssetName: swire.AssetName{
			Protocol: indexer.PROTOCOL_NAME_BRC20,
			Type:     indexer.ASSET_TYPE_FT,
			Ticker:   "ordi",
		},
		Divisibility: 4,
		Limit:        "1000",
		TotalMinted:  "210000000", // 21,000,000
		MaxSupply:    "210000000",
	},
	"ordx:f:pizza": {
		AssetName: swire.AssetName{
			Protocol: indexer.PROTOCOL_NAME_ORDX,
			Type:     indexer.ASSET_TYPE_FT,
			Ticker:   "pizza",
		},
		N:            1,
		Divisibility: 0,
		Limit:        "1000",
		TotalMinted:  "100000000",
		MaxSupply:    "100000000",
	},
	"ordx:f:pearl": {
		AssetName: swire.AssetName{
			Protocol: indexer.PROTOCOL_NAME_ORDX,
			Type:     indexer.ASSET_TYPE_FT,
			Ticker:   "pearl",
		},
		N:            1,
		Divisibility: 0,
		Limit:        "1000",
		TotalMinted:  "200000000",
		MaxSupply:    "200000000",
	},
	"ordx:f:pearl.lpt": {
		AssetName: swire.AssetName{
			Protocol: indexer.PROTOCOL_NAME_ORDX,
			Type:     indexer.ASSET_TYPE_FT,
			Ticker:   "pearl.lpt",
		},
		N:            1000,
		Divisibility: 0,
		Limit:        "200000000",
		TotalMinted:  "200000000",
		MaxSupply:    "200000000",
		DeployTx:     "", // update with genesis tx
	},
	"ordx:f:satoshilpt": {
		AssetName: swire.AssetName{
			Protocol: indexer.PROTOCOL_NAME_ORDX,
			Type:     indexer.ASSET_TYPE_FT,
			Ticker:   "satoshilpt",
		},
		N:            1000,
		Divisibility: 0,
		Limit:        "2000000000",
		TotalMinted:  "2000000000",
		MaxSupply:    "2000000000",
		DeployTx:     "", // update with genesis tx
	},
	"ordx:e:vintage": {
		AssetName: swire.AssetName{
			Protocol: indexer.PROTOCOL_NAME_ORDX,
			Type:     indexer.ASSET_TYPE_EXOTIC,
			Ticker:   "vintage",
		},
		N:            1,
		Divisibility: 0,
		Limit:        "1000",
		TotalMinted:  "200000000",
		MaxSupply:    "200000000",
	},
	// testnet4 ticker
	"ordx:f:dogcoin": {
		AssetName: swire.AssetName{
			Protocol: indexer.PROTOCOL_NAME_ORDX,
			Type:     indexer.ASSET_TYPE_FT,
			Ticker:   "dogcoin",
		},
		N:            1,
		Divisibility: 0,
		Limit:        "1000",
		TotalMinted:  "200000000",
		MaxSupply:    "200000000",
	},
	"runes:f:BITCOIN•TESTNET": {
		AssetName: swire.AssetName{
			Protocol: indexer.PROTOCOL_NAME_RUNES,
			Type:     indexer.ASSET_TYPE_FT,
			Ticker:   "BITCOIN•TESTNET",
		},
		N:            0,
		Divisibility: 0,
		Limit:        "1000",
		TotalMinted:  "200000000",
		MaxSupply:    "200000000",
	},
	"ordx:f:justatest": {
		AssetName: swire.AssetName{
			Protocol: indexer.PROTOCOL_NAME_ORDX,
			Type:     indexer.ASSET_TYPE_FT,
			Ticker:   "justatest",
		},
		N:            1000,
		Divisibility: 0,
		Limit:        "1000",
		TotalMinted:  "200000000",
		MaxSupply:    "200000000",
	},
	"ordx:f:cook": {
		AssetName: swire.AssetName{
			Protocol: indexer.PROTOCOL_NAME_ORDX,
			Type:     indexer.ASSET_TYPE_FT,
			Ticker:   "cook",
		},
		N:            1000,
		Divisibility: 0,
		Limit:        "1000",
		TotalMinted:  "200000000",
		MaxSupply:    "200000000",
	},
}

var _pkScripts = []string{
	"51208c4a6b130077db156fb22e7946711377c06327298b4c7e6e19a6eaa808d19eba", // client
	"51206b8e69003724d73623f173d9b1584df34cad6a4ec046272de0ccf4a09041682f", // server-2
	"5120d2912b91d0802aa584f4c8ff364f9bb2d5af103368fef4c61584b34f1f081f8b", // bootstrap
	"0020d7d42c2c26031ccb27d14ccc0cbb22b45664fc4e8c325b9d5c317a3b4336c0e1", // a-s2 channel
	"002071f5786fd95a6b2c0008a53462d61a85ba513d512f6ff7ae1f87183a2e966be6", // s2-dao channel
	"00205b7208d774f8958d776869e090950c4ce5d55b656d52f0f7fa98ee37ae541948", // a-s1 channel
	"0020c98fce9212d1f0c286fed2e9f8355ac507bfcb5eb50d285df21645e1032765ab", // s1-dao channel
	"512017abefbc099ae2053a210b6b4e69fe18a197a3a7a7cac6497891c17c7653c821", // server-1
}

var _nameMap = map[string]*indexerwire.OrdinalsName {
	"bigdaddy": {
		NftItem: indexerwire.NftItem{
			Id: 0,
			Name: "bigdaddy",
			Address: "tb1p339xkycqwld32maj9eu5vugnwlqxxfef3dx8umse5m42szx3n6aq6qv65g",
		},
	},
}

// CreateCoinbaseTx 创建一个 coinbase 交易
func CreateGenesisTx(n *network) (*wire.MsgTx, error) {
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

	for i, value := range n.utxoValue {
		pkscript := _pkScripts[n.utxoOwner[i]]
		b, _ := hex.DecodeString(pkscript)
		// 构造输出
		tx.AddTxOut(&wire.TxOut{
			Value:    value,
			PkScript: b,
		})
	}

	fmt.Printf("%s genesis tx %s\n", n.name, tx.TxID())
	return tx, nil
}

func CreateGenesisTx_SatsNet(n *network) (*swire.MsgTx, error) {
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

	for i, value := range n.utxoValue {
		pkscript := _pkScripts[n.utxoOwner[i]]
		b, _ := hex.DecodeString(pkscript)
		// 构造输出
		tx.AddTxOut(&swire.TxOut{
			Value:    value,
			Assets:   n.utxoAssets[i],
			PkScript: b,
		})
	}

	fmt.Printf("%s genesis tx %s", n.name, tx.TxID())
	return tx, nil
}

type TestIndexerClient struct {
	*RESTClient
	network *network
}

func NewTestIndexerClient(network *network) *TestIndexerClient {
	client := NewRESTClient("", "", "", nil)
	return &TestIndexerClient{
		RESTClient: client,
		network:    network,
	}
}

func (p *TestIndexerClient) GetTxOutput(utxo string) (*TxOutput, error) {
	p.network.mutex.RLock()
	defer p.network.mutex.RUnlock()

	index, ok := p.network.utxoIndex[utxo]
	if !ok {
		return nil, fmt.Errorf("can't find utxo %s", utxo)
	}

	if p.network.utxoUsed[utxo] != "" {
		return nil, fmt.Errorf("utxo %s is spent", utxo)
	}

	offsets := p.network.offsets[index]
	txAssets := p.network.utxoAssets[index]

	pkScript, _ := hex.DecodeString(_pkScripts[p.network.utxoOwner[index]])

	output := TxOutput{
		OutPointStr: utxo,
		OutValue:    wire.TxOut{Value: p.network.utxoValue[index], PkScript: pkScript},
		Assets:      txAssets.Clone(),
		Offsets:     cloneOffsets(offsets),
	}

	return &output, nil
}

func (p *TestIndexerClient) GetAscendData(utxo string) (*sindexer.AscendData, error) {
	txId, ok := p.network.ascendMap[utxo]
	if ok {
		return &sindexer.AscendData{
			AnchorTxId: txId,
		}, nil
	}
	return nil, fmt.Errorf("not found")
}

func (p *TestIndexerClient) IsCoreNode(pubkey []byte) (bool, error) {
	pkStr := hex.EncodeToString(pubkey)
	if pkStr == indexer.GetBootstrapPubKey() || pkStr == indexer.GetCoreNodePubKey() {
		return true, nil
	}

	_, ok := _coreNodeMap[pkStr]
	return ok, nil
}

// 只有未花费的能拿到id
func (p *TestIndexerClient) GetUtxoId(utxo string) (uint64, error) {
	p.network.mutex.RLock()
	defer p.network.mutex.RUnlock()

	index, ok := p.network.utxoIndex[utxo]
	if !ok {
		return INVALID_ID, fmt.Errorf("can't find utxo %s", utxo)
	}

	if p.network.utxoUsed[utxo] != "" {
		return INVALID_ID, fmt.Errorf("utxo %s is spent", utxo)
	}

	return uint64(index), nil
}

// btcutil.Tx
func (p *TestIndexerClient) GetRawTx(tx string) (string, error) {
	str, ok := p.network.txBroadcasted[tx]
	if !ok {
		return "", fmt.Errorf("not found")
	}
	return str, nil
}

// btcutil.Tx
func (p *TestIndexerClient) GetTxInfo(tx string) (*indexerwire.TxSimpleInfo, error) {
	_, ok := p.network.txBroadcasted[tx]
	if !ok {
		return nil, fmt.Errorf("not found")
	}

	var height int
	for k, idList := range p.network.blocks {
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
		Confirmations: uint64(p.network.height - height),
		BlockHeight:   int64(height),
	}, nil
}

func (p *TestIndexerClient) GetTxHeight(tx string) (int, error) {
	p.network.mutex.RLock()
	defer p.network.mutex.RUnlock()

	return p.getTxHeight(tx)
}

func (p *TestIndexerClient) getTxHeight(tx string) (int, error) {

	_, ok := p.network.txBroadcasted[tx]
	if !ok {
		return -1, fmt.Errorf("not found")
	}

	for k, idList := range p.network.blocks {
		for _, id := range idList {
			if id == tx {
				return k, nil
			}
		}
	}
	return -1, fmt.Errorf("not found")

}

func (p *TestIndexerClient) IsTxConfirmed(tx string) bool {
	height, _ := p.GetTxHeight(tx)
	return height >= 0
}

func (p *TestIndexerClient) GetSyncHeight() int {
	p.network.mutex.Lock()
	defer p.network.mutex.Unlock()

	if p.network.height < 0 {
		p.network.height = 0
		p.network.lastTime = time.Now().UnixMilli()
	} else {
		now := time.Now().UnixMilli()
		if now-p.network.lastTime >= 1000 {
			if len(p.network.blocks[p.network.height]) > 0 {
				p.network.height++
				p.network.lastTime = now
			}
		}
	}
	return p.network.height - 1
}

// 通过indexer访问btc节点，效率比较低。最好改用上面的接口。
func (p *TestIndexerClient) GetBestHeight() int64 {
	return int64(p.GetSyncHeight())
}

func (p *TestIndexerClient) GetBlockHash(height int) (string, error) {
	return fmt.Sprintf("%d", height), nil
}

func BuildBlockFromHexTxs(txHexes []string) (*wire.MsgBlock, error) {
	block := wire.NewMsgBlock(&wire.BlockHeader{
		// 随便填一些字段（不检查）
		Version:    0x20000000,
		PrevBlock:  chainhash.Hash{}, // 可以根据需要替换为某个真实区块hash
		MerkleRoot: chainhash.Hash{}, // 不做检查，不需要真实Merkle根
		Timestamp:  time.Now(),
		Bits:       0x1d00ffff, // 通常是难度目标
		Nonce:      0,
	})

	for _, txHex := range txHexes {
		tx, err := DecodeMsgTx(txHex)
		if err != nil {
			return nil, err
		}

		block.AddTransaction(tx)
	}

	return block, nil
}

func BuildBlockFromHexTxs_SatsNet(txHexes []string) (*swire.MsgBlock, error) {
	block := swire.NewMsgBlock(&swire.BlockHeader{
		// 随便填一些字段（不检查）
		Version:    0x20000000,
		PrevBlock:  schainhash.Hash{}, // 可以根据需要替换为某个真实区块hash
		MerkleRoot: schainhash.Hash{}, // 不做检查，不需要真实Merkle根
		Timestamp:  time.Now(),
		Bits:       0x1d00ffff, // 通常是难度目标
		Nonce:      0,
	})

	for _, txHex := range txHexes {
		tx, err := DecodeMsgTx_SatsNet(txHex)
		if err != nil {
			return nil, err
		}
		block.AddTransaction(tx)
	}

	return block, nil
}

func (p *TestIndexerClient) GetBlock(blockHash string) (string, error) {
	p.network.mutex.RLock()
	defer p.network.mutex.RUnlock()

	height, err := strconv.Atoi(blockHash)
	if err != nil {
		return "", err
	}

	txIdList := p.network.blocks[height]
	if len(txIdList) == 0 {
		return "", fmt.Errorf("noblock %d", height)
	}
	txHexList := make([]string, 0)
	for _, txId := range txIdList {
		txHexList = append(txHexList, p.network.txBroadcasted[txId])
	}
	var buf bytes.Buffer
	if p.network.IsBitcoinNet() {
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

func (p *TestIndexerClient) GetAssetSummaryWithAddress(address string) *indexerwire.AssetSummary {
	p.network.mutex.RLock()
	defer p.network.mutex.RUnlock()

	pkScript, err := AddrToPkScript(address, GetChainParam())
	if err != nil {
		Log.Errorf("invalid address %s, %v", address, err)
		return nil
	}

	assetmap := make(map[swire.AssetName]*swire.AssetInfo, 0)
	for i, utxo := range p.network.utxos {
		if p.network.utxoUsed[utxo] != "" {
			continue
		}

		pkScript2, _ := hex.DecodeString(_pkScripts[p.network.utxoOwner[i]])
		if bytes.Equal(pkScript, pkScript2) {
			assets := p.network.utxoAssets[i]
			if assets != nil {
				for _, asset := range assets {
					assets, ok := assetmap[asset.Name]
					if ok {
						assets.Add(&asset)
					} else {
						assetmap[asset.Name] = &asset
					}
				}

				if p.network.name == "satoshinet" {
					// 加入白聪
					bindingSatsNum := assets.GetBindingSatAmout()
					if p.network.utxoValue[i] > bindingSatsNum {
						plain := p.network.utxoValue[i] - bindingSatsNum
						assets, ok := assetmap[ASSET_PLAIN_SAT]
						if ok {
							assets.Amount = *assets.Amount.Add(indexer.NewDefaultDecimal(plain))
						} else {
							assetmap[ASSET_PLAIN_SAT] = &swire.AssetInfo{
								Name:       ASSET_PLAIN_SAT,
								Amount:     *indexer.NewDefaultDecimal(plain),
								BindingSat: 1,
							}
						}
					}
				}
			} else {
				assets, ok := assetmap[ASSET_PLAIN_SAT]
				if ok {
					assets.Amount = *assets.Amount.Add(indexer.NewDefaultDecimal(p.network.utxoValue[i]))
				} else {
					assetmap[ASSET_PLAIN_SAT] = &swire.AssetInfo{
						Name:       ASSET_PLAIN_SAT,
						Amount:     *indexer.NewDefaultDecimal(p.network.utxoValue[i]),
						BindingSat: 1,
					}
				}
			}
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

func (p *TestIndexerClient) GetUtxoListWithTicker(address string, ticker *swire.AssetName) []*indexerwire.TxOutputInfo {
	p.network.mutex.RLock()
	defer p.network.mutex.RUnlock()

	pkScript, err := AddrToPkScript(address, GetChainParam())
	if err != nil {
		Log.Errorf("invalid address %s, %v", address, err)
		return nil
	}

	outputs := make([]*indexerwire.TxOutputInfo, 0)
	for i, utxo := range p.network.utxos {

		if p.network.utxoUsed[utxo] != "" {
			continue
		}

		var output *indexerwire.TxOutputInfo
		assets := p.network.utxoAssets[i]
		if *ticker != ASSET_PLAIN_SAT {
			if assets != nil {
				_, err := assets.Find(ticker)
				if err == nil {
					output = &indexerwire.TxOutputInfo{
						OutPoint: utxo,
					}
				}
			}
		} else {
			if assets == nil {
				output = &indexerwire.TxOutputInfo{
					OutPoint: utxo,
				}
			} else {
				if p.network.name == "satoshinet" && p.network.utxoValue[i] > assets.GetBindingSatAmout() {
					output = &indexerwire.TxOutputInfo{
						OutPoint: utxo,
					}
				}
			}
		}

		if output != nil {
			pkScript2, _ := hex.DecodeString(_pkScripts[p.network.utxoOwner[i]])
			if bytes.Equal(pkScript, pkScript2) {

				offsets := cloneOffsets(p.network.offsets[i])
				var utxoAssets []*indexer.DisplayAsset
				for _, asset := range assets {
					asset := indexer.DisplayAsset{
						AssetName:  asset.Name,
						Amount:     asset.Amount.String(),
						Precision:  asset.Amount.Precision,
						BindingSat: int(asset.BindingSat),
						Offsets:    offsets[asset.Name],
					}
					utxoAssets = append(utxoAssets, &asset)
				}

				parts := strings.Split(utxo, ":")
				h, _ := p.getTxHeight(parts[0])
				output.UtxoId = indexer.ToUtxoId(h, 0, 0)
				output.Value = p.network.utxoValue[i]
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

func (p *TestIndexerClient) GetPlainUtxoList(address string) []*indexerwire.PlainUtxo {
	p.network.mutex.RLock()
	defer p.network.mutex.RUnlock()

	pkScript, err := AddrToPkScript(address, GetChainParam())
	if err != nil {
		Log.Errorf("invalid address %s, %v", address, err)
		return nil
	}

	outputs := make([]*indexerwire.PlainUtxo, 0)
	for i, utxo := range p.network.utxos {
		if p.network.utxoUsed[utxo] != "" {
			continue
		}

		assets := p.network.utxoAssets[i]
		if assets == nil {
			pkScript2, _ := hex.DecodeString(_pkScripts[p.network.utxoOwner[i]])

			if bytes.Equal(pkScript, pkScript2) {
				parts := strings.Split(utxo, ":")
				vout, _ := strconv.Atoi(parts[1])
				outputs = append(outputs, &indexerwire.PlainUtxo{
					Txid:  parts[0],
					Vout:  vout,
					Value: p.network.utxoValue[i],
				})
			}
		}
	}

	sort.Slice(outputs, func(i, j int) bool {
		return outputs[i].Value > outputs[j].Value
	})
	return outputs
}

func (p *TestIndexerClient) GetUtxosWithAddress(address string) (map[string]*wire.TxOut, error) {
	p.network.mutex.RLock()
	defer p.network.mutex.RUnlock()

	pkScript, err := AddrToPkScript(address, GetChainParam())
	if err != nil {
		Log.Errorf("invalid address %s, %v", address, err)
		return nil, nil
	}

	outputs := make(map[string]*wire.TxOut, 0)
	for i, utxo := range p.network.utxos {
		if p.network.utxoUsed[utxo] != "" {
			continue
		}

		assets := p.network.utxoAssets[i]
		if assets == nil {
			pkScript2, _ := hex.DecodeString(_pkScripts[p.network.utxoOwner[i]])

			if bytes.Equal(pkScript, pkScript2) {
				outputs[utxo] = &wire.TxOut{Value: p.network.utxoValue[i], PkScript: pkScript}
			}
		}
	}
	return outputs, nil
}

func (p *TestIndexerClient) GetAllUtxosWithAddress(address string) ([]*indexerwire.PlainUtxo, []*indexerwire.PlainUtxo, error) {
	p.network.mutex.RLock()
	defer p.network.mutex.RUnlock()

	pkScript, err := AddrToPkScript(address, GetChainParam())
	if err != nil {
		Log.Errorf("invalid address %s, %v", address, err)
		return nil, nil, err
	}

	plain := make([]*indexerwire.PlainUtxo, 0)
	others := make([]*indexerwire.PlainUtxo, 0)
	for i, utxo := range p.network.utxos {
		if p.network.utxoUsed[utxo] != "" {
			continue
		}

		pkScript2, _ := hex.DecodeString(_pkScripts[p.network.utxoOwner[i]])

		if bytes.Equal(pkScript, pkScript2) {
			assets := p.network.utxoAssets[i]

			parts := strings.Split(utxo, ":")
			vout, _ := strconv.Atoi(parts[1])
			u := indexerwire.PlainUtxo{
				Txid:  parts[0],
				Vout:  vout,
				Value: p.network.utxoValue[i],
			}
			if assets == nil {
				plain = append(plain, &u)

			} else {
				others = append(others, &u)
			}
		}
	}

	sort.Slice(plain, func(i, j int) bool {
		return plain[i].Value > plain[j].Value
	})

	sort.Slice(others, func(i, j int) bool {
		return others[i].Value > others[j].Value
	})

	return plain, others, nil
}

// sat/vb
func (p *TestIndexerClient) GetFeeRate() int64 {
	return 1
}

func (p *TestIndexerClient) GetExistingUtxos(utxos []string) ([]string, error) {
	p.network.mutex.RLock()
	defer p.network.mutex.RUnlock()

	result := make([]string, 0)
	for _, utxo := range utxos {
		if p.network.utxoUsed[utxo] != "" {
			continue
		}
		_, ok := p.network.utxoIndex[utxo]
		if ok {
			result = append(result, utxo)
		}
	}
	return result, nil
}

func insertPkScript(pkScript []byte) int {
	str := hex.EncodeToString(pkScript)
	for i, b := range _pkScripts {
		if b == str {
			return i
		}
	}
	_pkScripts = append(_pkScripts, str)
	return len(_pkScripts) - 1
}

func (p *TestIndexerClient) TestRawTx(signedTxs []string) error {
	return nil
}

func (p *TestIndexerClient) BroadCastTx(tx *wire.MsgTx) (string, error) {

	txHex, err := EncodeMsgTx(tx)
	if err != nil {
		return "", err
	}
	fmt.Printf("BroadCastTx TX: %s\n%s\n", tx.TxID(), txHex)

	p.network.mutex.Lock()
	defer p.network.mutex.Unlock()

	_, ok := p.network.txBroadcasted[tx.TxID()]
	if ok {
		return tx.TxID(), nil
	}
	p.network.txBroadcasted[tx.TxID()] = txHex

	if p.network.height == 0 {
		p.network.lastTime = time.Now().UnixMilli()
	} else {
		now := time.Now().UnixMilli()
		if now-p.network.lastTime >= 1000 {
			if len(p.network.blocks[p.network.height]) > 0 {
				p.network.height++
				p.network.lastTime = now
			}
		}
	}

	txs := p.network.blocks[p.network.height]
	txs = append(txs, tx.TxID())
	p.network.blocks[p.network.height] = txs

	if string(tx.TxIn[0].SignatureScript) == "genesis" {
		for i, _ := range tx.TxOut {
			utxo := fmt.Sprintf("%s:%d", tx.TxID(), i)
			p.network.utxos = append(p.network.utxos, utxo)
			index := len(p.network.utxos) - 1
			p.network.utxoIndex[utxo] = index
			// p.network.utxoValue = append(p.network.utxoValue, txOut.Value)
			// p.network.utxoAssets = append(p.network.utxoAssets, nil)
			// p.network.offsets = append(p.network.offsets, nil)
			// j := insertPkScript(txOut.PkScript)
			// p.network.utxoOwner = append(p.network.utxoOwner, j)
		}
		p.network.height++
		fmt.Printf("bitcoin genesis txId %s\n", tx.TxID())
		return tx.TxID(), nil
	}

	// 尝试为一层数据分配资产
	// 按ordx协议的规则
	// 按runes协议的规则
	// var assetName *swire.AssetName
	// var offsets indexer.AssetOffsets
	var input *TxOutput
	//var assetAmt *Decimal
	for _, txIn := range tx.TxIn {
		utxo := txIn.PreviousOutPoint.String()
		if txIn.PreviousOutPoint.Index < swire.AnchorTxOutIndex {
			spendTx, ok := p.network.utxoUsed[utxo]
			if ok {
				return "", fmt.Errorf("utxo %s spent in %s", utxo, spendTx)
			}
		}

		p.network.utxoUsed[utxo] = tx.TxID()
		index, ok := p.network.utxoIndex[utxo]
		if !ok {
			return "", fmt.Errorf("can't find utxo %s", utxo)
		}
		value := p.network.utxoValue[index]
		txAssets := p.network.utxoAssets[index]
		txOffsets := p.network.offsets[index]

		in := indexer.TxOutput{
			OutValue: wire.TxOut{
				Value: value,
			},
			Assets:  txAssets,
			Offsets: cloneOffsets(txOffsets),
		}

		if input == nil {
			input = &in
		} else {
			input.Append(&in)
		}

		// 增加对ordx部署和铸造的支持
		inscriptions, err := indexer.ParseInscription(txIn.Witness)
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
					_, ok := _tickerInfoRunning[assetName.String()]
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
					_tickerInfoRunning[assetName.String()] = &tickerInfo

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
					ticker, ok := _tickerInfoRunning[assetName.String()]
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
					input.Assets = append(input.Assets, asset)
					satsNum := indexer.GetBindingSatNum(dAmt, uint32(ticker.N))
					input.Offsets[assetName] = indexer.AssetOffsets{&indexer.OffsetRange{Start: 0, End: satsNum}}
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
							nameInfo, ok := _nameMap[updateInfo.Name]
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
											Key: k,
											Value: v,
											InscriptionId: fmt.Sprintf("%si%d", tx.TxID(), i),
										})
									}
								}
							}
						}
					}
				}
			
				
			}
		}
	}

	var runesOutputUtxo, premineOutput string
	var edicts []runestone.Edict
	var premineAssetInfo *indexer.AssetInfo
	for i, txOut := range tx.TxOut {
		utxo := fmt.Sprintf("%s:%d", tx.TxID(), i)
		p.network.utxos = append(p.network.utxos, utxo)
		index := len(p.network.utxos) - 1
		p.network.utxoIndex[utxo] = index
		p.network.utxoValue = append(p.network.utxoValue, txOut.Value)

		var curr *indexer.TxOutput
		curr, input, err = input.Cut(txOut.Value)
		if err != nil {
			Log.Panicf("Cut failed, %v", err)
		}

		var utxoAssets swire.TxAssets
		var utxoOffsets map[swire.AssetName]indexer.AssetOffsets
		utxoAssets = curr.Assets
		utxoOffsets = curr.Offsets

		p.network.utxoAssets = append(p.network.utxoAssets, utxoAssets)
		p.network.offsets = append(p.network.offsets, utxoOffsets)
		j := insertPkScript(txOut.PkScript)
		p.network.utxoOwner = append(p.network.utxoOwner, j)

		if IsOpReturn(txOut.PkScript) {
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
							DisplayName:  fmt.Sprintf("%d:%d", (p.network.height), len(p.network.blocks[p.network.height])),
							Divisibility: int(divisibility),
							MaxSupply:    supply.String(),
						}
						_tickerInfoRunning[assetName.String()] = &tickInfo

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
				}
			}
		} else {
			if runesOutputUtxo == "" {
				runesOutputUtxo = utxo
			}
		}
	}

	if premineOutput != "" {
		index, ok := p.network.utxoIndex[premineOutput]
		if !ok {
			return "", fmt.Errorf("can't find utxo %s", premineOutput)
		}
		err = p.network.utxoAssets[index].Add(premineAssetInfo)
		if err != nil {
			return "", err
		}
	}

	// 执行runes的转移规则
	index1, ok := p.network.utxoIndex[runesOutputUtxo]
	if !ok {
		return "", fmt.Errorf("can't find utxo %s", runesOutputUtxo)
	}
	for _, edict := range edicts {
		if int(edict.Output) >= len(tx.TxOut) {
			return "", fmt.Errorf("invalid edict %v", edict)
		}
		index2, ok := p.network.utxoIndex[fmt.Sprintf("%s:%d", tx.TxID(), edict.Output)]
		if !ok {
			return "", fmt.Errorf("invalid edict %v", edict)
		}
		tickerInfo, err := GetTickerInfoByRuneId(edict.ID.String())
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

		err = p.network.utxoAssets[index1].Subtract(&asset)
		if err != nil {
			return "", err
		}
		err = p.network.utxoAssets[index2].Add(&asset)
		if err != nil {
			return "", err
		}
	}

	fmt.Printf("BroadCastTx succeeded. %s\n", tx.TxID())
	return tx.TxID(), nil
}

func (p *TestIndexerClient) BroadCastTx_SatsNet(tx *swire.MsgTx) (string, error) {

	txHex, err := EncodeMsgTx_SatsNet(tx)
	if err != nil {
		return "", err
	}

	fmt.Printf("BroadCastTx_SatsNet TX: %s\n%s\n", tx.TxID(), txHex)

	p.network.mutex.Lock()
	defer p.network.mutex.Unlock()

	_, ok := p.network.txBroadcasted[tx.TxID()]
	if ok {
		return tx.TxID(), nil
	}
	p.network.txBroadcasted[tx.TxID()] = txHex

	if p.network.height == 0 {
		p.network.lastTime = time.Now().UnixMilli()
	} else {
		now := time.Now().UnixMilli()
		if now-p.network.lastTime >= 1000 {
			if len(p.network.blocks[p.network.height]) > 0 {
				p.network.height++
				p.network.lastTime = now
			}
		}
	}

	txs := p.network.blocks[p.network.height]
	txs = append(txs, tx.TxID())
	p.network.blocks[p.network.height] = txs

	if string(tx.TxIn[0].SignatureScript) == "genesis" {
		for i, _ := range tx.TxOut {
			utxo := fmt.Sprintf("%s:%d", tx.TxID(), i)
			p.network.utxos = append(p.network.utxos, utxo)
			index := len(p.network.utxos) - 1
			p.network.utxoIndex[utxo] = index
			// p.network.utxoValue = append(p.network.utxoValue, txOut.Value)
			// p.network.utxoAssets = append(p.network.utxoAssets, nil)
			// p.network.offsets = append(p.network.offsets, nil)
			// j := insertPkScript(txOut.PkScript)
			// p.network.utxoOwner = append(p.network.utxoOwner, j)
		}
		p.network.height++
		fmt.Printf("satsnet genesis txId %s\n", tx.TxID())
		return tx.TxID(), nil
	}

	for _, txIn := range tx.TxIn {
		utxo := txIn.PreviousOutPoint.String()
		if txIn.PreviousOutPoint.Index == swire.AnchorTxOutIndex {
			data, _, err := CheckAnchorPkScript(tx.TxIn[0].SignatureScript)
			if err == nil {
				p.network.ascendMap[data.Utxo] = tx.TxID()
				// pubkeyStr := hex.EncodeToString(pubkey)
				// if pubkeyStr == indexer.GetBootstrapPubKey() {
				// 	_coreNodeMap[pubkeyStr] = true
				// }
			}
		} else if txIn.PreviousOutPoint.Index < swire.AnchorTxOutIndex {
			spendTx, ok := p.network.utxoUsed[utxo]
			if ok {
				return "", fmt.Errorf("utxo %s spent in %s", utxo, spendTx)
			}
		}
		p.network.utxoUsed[utxo] = tx.TxID()
	}

	for i, txOut := range tx.TxOut {
		utxo := fmt.Sprintf("%s:%d", tx.TxID(), i)
		p.network.utxos = append(p.network.utxos, utxo)
		index := len(p.network.utxos) - 1
		p.network.utxoIndex[utxo] = index
		p.network.utxoValue = append(p.network.utxoValue, txOut.Value)
		p.network.utxoAssets = append(p.network.utxoAssets, txOut.Assets)
		p.network.offsets = append(p.network.offsets, nil)
		j := insertPkScript(txOut.PkScript)
		p.network.utxoOwner = append(p.network.utxoOwner, j)
	}

	fmt.Printf("BroadCastTx_SatsNet succeeded. %s\n", tx.TxID())
	return tx.TxID(), nil
}

func (p *TestIndexerClient) GetTickInfo(assetName *swire.AssetName) *indexer.TickerInfo {
	p.network.mutex.RLock()
	defer p.network.mutex.RUnlock()

	ticker, ok := _tickerInfoRunning[assetName.String()]
	if !ok {
		return nil
	}

	return ticker
}

func (p *TestIndexerClient) AllowDeployTick(assetName *swire.AssetName) error {
	return nil
}

func (p *TestIndexerClient) GetUtxoSpentTx(utxo string) (string, error) {
	txId := p.network.utxoUsed[utxo]
	if txId != "" {
		return txId, nil
	}
	return "", fmt.Errorf("not spent")
}

func (p *TestIndexerClient) GetServiceIncoming(addr string) (int, int64, error) {
	return 0, 0, fmt.Errorf("not implemented")
}

func (p *TestIndexerClient) GetNonce(pubKey []byte) ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestIndexerClient) PutKVs(req *indexerwire.PutKValueReq) error {

	return fmt.Errorf("not implemented")
}

func (p *TestIndexerClient) DelKVs(req *indexerwire.DelKValueReq) error {
	return fmt.Errorf("not implemented")
}

func (p *TestIndexerClient) GetKV(pubkey []byte, key string) (*indexerwire.KeyValue, error) {

	return nil, fmt.Errorf("not implemented")
}


// 传回该name绑定的所有kv
func (p *TestIndexerClient) GetNameInfo(name string) (*indexerwire.OrdinalsName, error) {
	info, ok := _nameMap[name]
	if ok {
		return info, nil
	}
	return nil, fmt.Errorf("not found")
}

func (p *TestIndexerClient) GetNamesWithKey(address, key string) (
	[]*indexerwire.OrdinalsName, error) {
	return nil, fmt.Errorf("not implemented")
}


type TestNodeClient struct {
	*RESTClient
	server *Manager
}

func NewTestNodeClient(server *Manager) *TestNodeClient {
	client := NewRESTClient("", "", "", nil)
	return &TestNodeClient{
		RESTClient: client,
		server:     server,
	}
}

func (p *TestNodeClient) GetNodeInfoReq(nodeId string) (*Node, error) {

	node := &Node{}

	return node, nil
}

func (p *TestNodeClient) GetSupportedContractsReq() ([]string, error) {
	//return GetSupportedContracts(), nil
	return nil, fmt.Errorf("not implemented")
}

func (p *TestNodeClient) GetDeployedContractsReq() ([]string, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestNodeClient) GetContractStatusReq(url string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (p *TestNodeClient) GetContractAnalyticsReq(url string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (p *TestNodeClient) GetContractInvokeHistoryReq(contractUrl string, start, limit int) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (p *TestNodeClient) GetContractInvokeHistoryByAddressReq(contractUrl, address string, start, limit int) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (p *TestNodeClient) GetContractAllAddressesReq(contractUrl string, start, limit int) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (p *TestNodeClient) GetContractStatusByAddressReq(url, address string) (string, error) {
	return "", fmt.Errorf("not implemented")
}


func (p *TestNodeClient) SendSigReq(req *wwire.SignRequest,
	sig []byte) ([][][]byte, error) {

	// signedReq := wwire.SignReq{
	// 	SignRequest: *req,
	// 	Sig:         sig,
	// }

	// return sig2, nil
	return nil, fmt.Errorf("not implemented")
}


func (p *TestNodeClient) SendActionResultNfty(msgId int64, action string, ret int, reason string) error {

	// req := wwire.ActionResultNotify{
	// 	Id:     msgId,
	// 	Action: action,
	// 	Result: ret,
	// 	Reason: reason,
	// }

	// if p.server != nil {
	// 	err := p.server.HandleActionResultNfty(&req)
	// 	if err != nil {
	// 		Log.Errorf("HandleActionResultNfty failed, %v", err)
	// 		return err
	// 	}
	// }

	return nil
}

func (p *TestNodeClient) SendPingReq(req *wwire.PingReq) (*wwire.PingResp, error) {

	// resp := &wwire.PingResp{
	// 	BaseResp: wwire.BaseResp{
	// 		Code: 0,
	// 		Msg:  "ok",
	// 	},
	// 	PingResponse: &wwire.PingResponse{
	// 		NextAction: "pong",
	// 	},
	// }

	// if p.server != nil {
	// 	resp.PingResponse, _ = p.server.HandlePingReq(req)
	// 	return resp, nil
	// }

	//return resp, fmt.Errorf("no server")

	return nil, fmt.Errorf("not implemented")
}

func (p *TestNodeClient) SendActionSyncReq(req *wwire.ActionSyncReq) (*wwire.ActionSyncResp, error) {
	// resp := &wwire.ActionSyncResp{
	// 	BaseResp: wwire.BaseResp{
	// 		Code: 0,
	// 		Msg:  "ok",
	// 	},
	// }

	// if p.server != nil {
	// 	buf, err := p.server.HandleActionSyncReq(req)
	// 	if err != nil {
	// 		Log.Errorf("HandleActionSyncReq failed, %v", err)
	// 		return nil, err
	// 	}
	// 	resp.ChannelData = buf
	// 	return resp, nil
	// }

	// return nil, fmt.Errorf("no server")
	return nil, fmt.Errorf("not implemented")
}

