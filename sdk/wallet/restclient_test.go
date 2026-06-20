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
	schainhash "github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/runes/runestone"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"

	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	sindexerwire "github.com/sat20-labs/satoshinet/indexer/rpcserver/wire"
)

type Brc20Transfer struct {
	Utxo      string
	Address   string
	AssetName *swire.AssetName
	Amt       *Decimal
}

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
	ascendMap     map[string]string // utxo->anchorTxId
	descendMap    map[string]string // deanchorTxId->utxo

	// brc20, 仅在L1有效
	addrAssetMap    map[string]map[swire.AssetName]*Decimal        // brc20 持有的数量
	addrTransferMap map[string]map[swire.AssetName]map[string]bool // brc20 持有的transfer铭文
	transferInfo    map[string]*Brc20Transfer                      // brc20 持有的transfer铭文
	invalids        map[string]map[swire.AssetName]bool            // utxo中哪些资产是无效的
}

// 将预设的brc20资产都当作可以转移的
func (p *network) InitBRC20AssetInfo() {
	for i, utxo := range p.utxos {
		if p.utxoUsed[utxo] != "" {
			continue
		}

		addr := _pkScripts[p.utxoOwner[i]]
		assets := p.utxoAssets[i]

		for _, asset := range assets {
			if asset.Name.Protocol == indexer.PROTOCOL_NAME_BRC20 {
				assetmap, ok := p.addrAssetMap[addr]
				if !ok {
					assetmap = make(map[swire.AssetName]*Decimal)
					p.addrAssetMap[addr] = assetmap
				}
				total := assetmap[asset.Name]
				assetmap[asset.Name] = total.Add(&asset.Amount).Add(&asset.Amount) // 加倍

				transferMap, ok := p.addrTransferMap[addr]
				if !ok {
					transferMap = make(map[swire.AssetName]map[string]bool)
					p.addrTransferMap[addr] = transferMap
				}
				utxomap, ok := transferMap[asset.Name]
				if !ok {
					utxomap = make(map[string]bool)
					transferMap[asset.Name] = utxomap
				}
				utxomap[utxo] = true

				p.transferInfo[utxo] = &Brc20Transfer{
					Utxo:      utxo,
					Address:   addr,
					AssetName: &asset.Name,
					Amt:       &asset.Amount,
				}

			}
		}

	}
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
		n := *v
		tickerInfo[k] = &n
	}
	return tickerInfo
}

func SetTickerInfo(another map[string]*indexer.TickerInfo) {
	_tickerInfoRunning = make(map[string]*indexer.TickerInfo)
	for k, v := range another {
		n := *v
		_tickerInfoRunning[k] = &n
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
		n.blocks[k] = txList
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
		n.utxoAssets[i] = assets.Clone()
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

	n.addrAssetMap = make(map[string]map[swire.AssetName]*Decimal)
	for addr, assetMap := range p.addrAssetMap {
		newAssetMap := make(map[swire.AssetName]*Decimal)
		for k, v := range assetMap {
			newAssetMap[k] = v.Clone()
		}
		n.addrAssetMap[addr] = newAssetMap
	}

	n.addrTransferMap = make(map[string]map[swire.AssetName]map[string]bool)
	for addr, transferMap := range p.addrTransferMap {
		newTransferMap := make(map[swire.AssetName]map[string]bool)
		for name, um := range transferMap {
			utxoMap := make(map[string]bool)
			for k, v := range um {
				utxoMap[k] = v
			}
			newTransferMap[name] = utxoMap
		}
		n.addrTransferMap[addr] = newTransferMap
	}

	n.transferInfo = make(map[string]*Brc20Transfer)
	for k, v := range p.transferInfo {
		newInfo := &Brc20Transfer{
			Utxo:      v.Utxo,
			Address:   v.Address,
			AssetName: v.AssetName,
			Amt:       v.Amt.Clone(),
		}
		n.transferInfo[k] = newInfo
	}

	n.invalids = make(map[string]map[swire.AssetName]bool)
	for utxo, assetMap := range p.invalids {
		newAssetMap := make(map[swire.AssetName]bool)
		for k, v := range assetMap {
			newAssetMap[k] = v
		}
		n.invalids[utxo] = newAssetMap
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
	p.addrAssetMap = n.addrAssetMap
	p.addrTransferMap = n.addrTransferMap
	p.transferInfo = n.transferInfo
	p.invalids = n.invalids
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
		20000, 2000000, 20000, 200000000,
		1000000, 100000, 10000, 10000, // 4-
		10000, 10000, 10000, 10000, // 8-
		330, 330, 330, 330, // 12-
		10000, 90000, 10000, 10000, // 16-
		100000, 100000, 10000, 10000, // 20-
		1000, 1000, 330, 1000, // 24-
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
			{Name: swire.AssetName{Protocol: "brc20", Type: "f", Ticker: "ordi"}, Amount: *indexer.NewDecimal(10000000, 1), BindingSat: 0},
		},
		{
			{Name: swire.AssetName{Protocol: "brc20", Type: "f", Ticker: "ordi"}, Amount: *indexer.NewDecimal(1000000, 1), BindingSat: 0},
		},
		{
			{Name: swire.AssetName{Protocol: "brc20", Type: "f", Ticker: "ordi"}, Amount: *indexer.NewDecimal(100000, 1), BindingSat: 0},
		},
		{
			{Name: swire.AssetName{Protocol: "brc20", Type: "f", Ticker: "pizza"}, Amount: *indexer.NewDecimal(10000000, 1), BindingSat: 0},
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
			{Name: swire.AssetName{Protocol: "brc20", Type: "f", Ticker: "ordi"}, Amount: *indexer.NewDecimal(100000000, 1), BindingSat: 0},
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

		// 12
		{{Protocol: "brc20", Type: "f", Ticker: "ordi"}: {{Start: 0, End: 1}}},
		{{Protocol: "brc20", Type: "f", Ticker: "ordi"}: {{Start: 0, End: 1}}},
		{{Protocol: "brc20", Type: "f", Ticker: "ordi"}: {{Start: 0, End: 1}}},
		{{Protocol: "brc20", Type: "f", Ticker: "pizza"}: {{Start: 0, End: 1}}},

		{{Protocol: "ordx", Type: "f", Ticker: "pizza"}: {{Start: 0, End: 10000}}},
		{{Protocol: "ordx", Type: "f", Ticker: "dogcoin"}: {{Start: 0, End: 90000}}},
		{{Protocol: "ordx", Type: "e", Ticker: "vintage"}: {{Start: 1000, End: 9000}}},
		{{Protocol: "ordx", Type: "e", Ticker: "vintage"}: {{Start: 0, End: 1000}, {Start: 3000, End: 4000}, {Start: 5000, End: 9000}}},

		{{Protocol: "ordx", Type: "f", Ticker: "satoshilpt"}: {{Start: 0, End: 100000}}},
		nil, nil, nil,

		{{Protocol: "ordx", Type: "f", Ticker: "pizza"}: {{Start: 0, End: 1000}}},
		nil,
		{{Protocol: "brc20", Type: "f", Ticker: "ordi"}: {{Start: 0, End: 1}}},
		nil,

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

	addrAssetMap:    map[string]map[swire.AssetName]*Decimal{},
	addrTransferMap: map[string]map[swire.AssetName]map[string]bool{},
	transferInfo:    map[string]*Brc20Transfer{},
	invalids:        map[string]map[swire.AssetName]bool{},
}

var _network2 = &network{
	name:          "satoshinet",
	height:        0,
	blocks:        make(map[int][]string),
	txBroadcasted: make(map[string]string),

	utxos:      make([]string, 0),
	utxoUsed:   make(map[string]string),
	utxoIndex:  make(map[string]int),
	ascendMap:  make(map[string]string),
	descendMap: make(map[string]string),

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
			{Name: swire.AssetName{Protocol: "brc20", Type: "f", Ticker: "ordi"}, Amount: *indexer.NewDecimal(100000000, 1), BindingSat: 0},
		},
		nil,

		{
			{Name: swire.AssetName{Protocol: "ordx", Type: "f", Ticker: "pizza"}, Amount: *indexer.NewDefaultDecimal(10000000), BindingSat: 1},
		},
		{
			{Name: swire.AssetName{Protocol: "runes", Type: "f", Ticker: "TEST•FIRST•TEST"}, Amount: *indexer.NewDefaultDecimal(100000000), BindingSat: 0},
		},
		{
			{Name: swire.AssetName{Protocol: "brc20", Type: "f", Ticker: "ordi"}, Amount: *indexer.NewDecimal(100000000, 1), BindingSat: 0},
		},
		{
			{Name: swire.AssetName{Protocol: "ordx", Type: "f", Ticker: "satoshilpt"}, Amount: *indexer.NewDefaultDecimal(100000000), BindingSat: 1000},
		},

		nil, nil, nil, nil,
	},

	offsets: []map[swire.AssetName]indexer.AssetOffsets{
		nil, nil, nil, nil,
		nil, nil, nil, nil,
		nil, nil, nil, nil,
		nil, nil, nil, nil,
	},

	utxoOwner: []int{
		0, 0, 0, 0,
		3, 3, 3, 3,
		0, 0, 0, 0,
		1, 1, 1, 1,
	},
}

var _coreNodeMap = map[string]bool{ // pubkey
	"025fb789035bc2f0c74384503401222e53f72eefdebf0886517ff26ac7985f52ad": true,
	"0367f26af23dc40fdad06752c38264fe621b7bbafb1d41ab436b87ded192f1336e": true,
}
var _minerInfoMap = map[string]*sindexer.MinerInfo{}                      // pubkey
var _coreNodeChildMap = map[string]map[string]*sindexer.MinerAscendInfo{} // pubkey->child pubkey

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
		Divisibility: 1,
		Limit:        "1000",
		TotalMinted:  "210000000", // 21,000,000
		MaxSupply:    "210000000",
	},
	"brc20:f:pizza": {
		AssetName: swire.AssetName{
			Protocol: indexer.PROTOCOL_NAME_BRC20,
			Type:     indexer.ASSET_TYPE_FT,
			Ticker:   "pizza",
		},
		Divisibility: 1,
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

var _nameMap = map[string]*indexerwire.OrdinalsName{
	"bigdaddy": {
		NftItem: indexerwire.NftItem{
			Id:      0,
			Name:    "bigdaddy",
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

func (p *TestIndexerClient) Host() string {
	return p.RESTClient.Host
}

func (p *TestIndexerClient) Ping() error {
	return nil
}

func (p *TestIndexerClient) GetTxOutput(utxo string) (*TxOutput, error) {
	utxoId, err := p.GetUtxoId(utxo)
	if err != nil {
		return nil, err
	}

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
		UtxoId:        utxoId,
		OutPointStr:   utxo,
		OutValue:      wire.TxOut{Value: p.network.utxoValue[index], PkScript: pkScript},
		Assets:        txAssets.Clone(),
		Offsets:       cloneOffsets(offsets),
		SatBindingMap: make(map[int64]*indexer.AssetInfo),
		Invalids:      make(map[indexer.AssetName]bool),
	}
	invalidmap, existing := p.network.invalids[utxo]
	for _, asset := range output.Assets {
		if p.network.IsBitcoinNet() && asset.Name.Protocol == indexer.PROTOCOL_NAME_BRC20 {
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

func (p *TestIndexerClient) GetAscendData(utxo string) (*sindexer.AscendData, error) {
	txId, ok := p.network.ascendMap[utxo]
	if ok {
		return &sindexer.AscendData{
			AnchorTxId: txId,
		}, nil
	}
	return nil, fmt.Errorf("not found")
}

func (p *TestIndexerClient) GetChannelLedger(channel string) ([]*sindexer.ChannelLedgerEntry, error) {
	return []*sindexer.ChannelLedgerEntry{}, nil
}

func (p *TestIndexerClient) GetChannelStateEvents(channel string) ([]*sindexer.ChannelStateEvent, error) {
	return []*sindexer.ChannelStateEvent{}, nil
}

func (p *TestIndexerClient) RecordChannelStateEvent(event *sindexer.ChannelStateEvent, pubkey, sig []byte) error {
	return nil
}

func (p *TestIndexerClient) IsCoreNode(pubkey []byte) (bool, error) {
	pkStr := hex.EncodeToString(pubkey)
	if pkStr == indexer.GetBootstrapPubKey() || pkStr == indexer.GetCoreNodePubKey() {
		return true, nil
	}

	_, ok := _coreNodeMap[pkStr]
	return ok, nil
}

func (p *TestIndexerClient) GetCoreNodeInfo(pubkey []byte) (*sindexer.CoreNodeInfo, error) {
	pkStr := hex.EncodeToString(pubkey)
	_, ok := _coreNodeMap[pkStr]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	info := sindexer.NewCoreNodeInfo(nil)
	if minerInfo, ok := _minerInfoMap[pkStr]; ok {
		info.MinerInfo = *minerInfo
	}
	if childMap, ok := _coreNodeChildMap[pkStr]; ok {
		info.ChildMiners = childMap
	}
	return info, nil
}

func (p *TestIndexerClient) IsMinerNode(pubkey []byte) (bool, error) {
	ok, _ := p.IsCoreNode(pubkey)
	if ok {
		return true, nil
	}
	pkStr := hex.EncodeToString(pubkey)
	if _, ok := _coreNodeMap[pkStr]; ok {
		return true, nil
	}
	return false, nil
}

func (p *TestIndexerClient) GetMinerInfo(pubkey []byte) (*sindexerwire.MinerInfo, error) {
	pkStr := hex.EncodeToString(pubkey)

	c, err := p.GetCoreNodeInfo(pubkey)
	if err == nil {
		return &sindexerwire.MinerInfo{
			MinerInfo:  &c.MinerInfo,
			IsCoreNode: true,
			ChildCount: len(c.ChildMiners),
		}, nil
	}

	info, ok := _minerInfoMap[pkStr]
	if ok {
		return &sindexerwire.MinerInfo{
			MinerInfo:  info,
			IsCoreNode: false,
		}, nil
	}
	return nil, fmt.Errorf("not a miner")
}

// 只有未花费的能拿到id
func (p *TestIndexerClient) GetUtxoId(utxo string) (uint64, error) {
	p.network.mutex.RLock()
	defer p.network.mutex.RUnlock()

	_, ok := p.network.utxoIndex[utxo]
	if !ok {
		return INVALID_ID, fmt.Errorf("can't find utxo %s", utxo)
	}

	if p.network.utxoUsed[utxo] != "" {
		return INVALID_ID, fmt.Errorf("utxo %s is spent", utxo)
	}

	txId, vout, err := indexer.ParseUtxo(utxo)
	if err != nil {
		return INVALID_ID, err
	}
	for height, txList := range p.network.blocks {
		for txIndex, tx := range txList {
			if tx == txId {
				utxoId := indexer.ToUtxoId(height, txIndex, vout)
				return utxoId, nil
			}
		}
	}

	return INVALID_ID, fmt.Errorf("utxo %s is invalid", utxo)
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
	p.network.mutex.RLock()
	defer p.network.mutex.RUnlock()
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
				if k == p.network.height {
					return -1, nil
				}
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

func (p *TestIndexerClient) generateNewBlock() {
	if len(p.network.blocks[p.network.height]) > 0 {
		Log.Infof("%s generate block %d, tx count %d", p.network.name, p.network.height, len(p.network.blocks[p.network.height]))
		p.network.height++
		p.network.lastTime = time.Now().UnixMilli()
	}
}

// 由这个接口决定出块
func (p *TestIndexerClient) GetSyncHeight() int {
	p.network.mutex.Lock()
	defer p.network.mutex.Unlock()

	p.generateNewBlock() // 有tx就生成新的块，确定性
	return p.network.height - 1
}

// 通过indexer访问btc节点，效率比较低。最好改用上面的接口。
func (p *TestIndexerClient) GetBestHeight() int64 {
	p.network.mutex.Lock()
	defer p.network.mutex.Unlock()

	return int64(p.network.height - 1)
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
		Timestamp:  time.Unix(0, 0),
		Bits:       0x1d00ffff, // 通常是难度目标
		Nonce:      0,
	})

	var buf []byte
	for _, txHex := range txHexes {
		tx, err := DecodeMsgTx(txHex)
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
		tx, err := DecodeMsgTx_SatsNet(txHex)
		if err != nil {
			return nil, err
		}

		block.AddTransaction(tx)
		buf = append(buf, []byte(tx.TxID())...)
	}
	block.Header.MerkleRoot = schainhash.DoubleHashH(buf)

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
			if len(assets) != 0 {
				invalids := p.network.invalids[utxo]
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
					assets2, ok2 := assetmap[ASSET_PLAIN_SAT]
					if ok2 {
						assets2.Amount = *assets2.Amount.Add(indexer.NewDefaultDecimal(p.network.utxoValue[i]))
					} else {
						assetmap[ASSET_PLAIN_SAT] = &swire.AssetInfo{
							Name:       ASSET_PLAIN_SAT,
							Amount:     *indexer.NewDefaultDecimal(p.network.utxoValue[i]),
							BindingSat: 1,
						}
					}
				}

				if p.network.IsBitcoinNet() {

				} else {
					// 聪网上，看看聪数量是不是比绑定资产的聪多，如果多，就加入白聪
					bindingSatsNum := assets.GetBindingSatAmout()
					if p.network.utxoValue[i] > bindingSatsNum {
						plain := p.network.utxoValue[i] - bindingSatsNum
						assets2, ok := assetmap[ASSET_PLAIN_SAT]
						if ok {
							assets2.Amount = *assets2.Amount.Add(indexer.NewDefaultDecimal(plain))
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
				assets2, ok := assetmap[ASSET_PLAIN_SAT]
				if ok {
					assets2.Amount = *assets2.Amount.Add(indexer.NewDefaultDecimal(p.network.utxoValue[i]))
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

	// 增加brc20的资产数量
	addr := hex.EncodeToString(pkScript)
	brc20AssetMap := p.network.addrAssetMap[addr]
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

func (p *TestIndexerClient) GetIndexerPubKey() ([]byte, error) {
	return hex.DecodeString(indexer.GetBootstrapPubKey())
}

func (p *TestIndexerClient) GetUtxoListWithTicker(address string, ticker *swire.AssetName) []*indexerwire.TxOutputInfo {
	return p.getUtxoListWithTicker(address, ticker, false)
}

func (p *TestIndexerClient) GetAllUtxosWithAddress(address string) []*indexerwire.TxOutputInfo {
	return p.getUtxoListWithTicker(address, nil, false)
}

func (p *TestIndexerClient) GetUtxoListWithBRC20Ticker(address string, ticker *swire.AssetName, invalid bool) []*indexerwire.TxOutputInfo {
	return p.getUtxoListWithTicker(address, ticker, invalid)
}

func (p *TestIndexerClient) getUtxoListWithTicker(address string, ticker *swire.AssetName, includeInvalid bool) []*indexerwire.TxOutputInfo {
	p.network.mutex.RLock()
	defer p.network.mutex.RUnlock()

	pkScript, err := AddrToPkScript(address, GetChainParam())
	if err != nil {
		Log.Errorf("invalid address %s, %v", address, err)
		return nil
	}

	// 过滤无效资产

	outputs := make([]*indexerwire.TxOutputInfo, 0)
	for i, utxo := range p.network.utxos {

		if p.network.utxoUsed[utxo] != "" {
			continue
		}

		var output *indexerwire.TxOutputInfo
		assets := p.network.utxoAssets[i]
		invalids := p.network.invalids[utxo]
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
		} else if *ticker != ASSET_PLAIN_SAT {
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
				if !p.network.IsBitcoinNet() && p.network.utxoValue[i] > validAssets.GetBindingSatAmout() {
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
			pkScript2, _ := hex.DecodeString(_pkScripts[p.network.utxoOwner[i]])
			if bytes.Equal(pkScript, pkScript2) {

				offsets := cloneOffsets(p.network.offsets[i])
				var utxoAssets []*indexer.DisplayAsset
				for _, v := range selectedAssets {
					asset := indexer.DisplayAsset{
						AssetName:  v.Name,
						Amount:     v.Amount.String(),
						Precision:  v.Amount.Precision,
						BindingSat: int(v.BindingSat),
						Offsets:    offsets[v.Name],
					}
					if v.Name.Protocol == indexer.PROTOCOL_NAME_BRC20 && p.network.IsBitcoinNet() {
						asset.Offsets = []*indexer.OffsetRange{{Start: 0, End: 1}}
						asset.OffsetToAmts = []*indexer.OffsetToAmount{{Offset: 0, Amount: v.Amount.String()}}
						asset.Invalid = includeInvalid
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
		if len(assets) == 0 {
			pkScript2, _ := hex.DecodeString(_pkScripts[p.network.utxoOwner[i]])

			if bytes.Equal(pkScript, pkScript2) {
				outputs[utxo] = &wire.TxOut{Value: p.network.utxoValue[i], PkScript: pkScript}
			}
		}
	}
	return outputs, nil
}

func (p *TestIndexerClient) GetUnusableUtxosWithAddress(address string) ([]*TxOutput, error) {
	return nil, fmt.Errorf("not implemented")
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

	txs := p.network.blocks[p.network.height]
	txs = append(txs, tx.TxID())
	p.network.blocks[p.network.height] = txs

	if string(tx.TxIn[0].SignatureScript) == "genesis" {
		for i, txOut := range tx.TxOut {
			utxo := fmt.Sprintf("%s:%d", tx.TxID(), i)
			p.network.utxos = append(p.network.utxos, utxo)
			index := len(p.network.utxos) - 1
			p.network.utxoIndex[utxo] = index
			// p.network.utxoValue = append(p.network.utxoValue, txOut.Value)
			// p.network.utxoAssets = append(p.network.utxoAssets, nil)
			// p.network.offsets = append(p.network.offsets, nil)
			// j := insertPkScript(txOut.PkScript)
			// p.network.utxoOwner = append(p.network.utxoOwner, j)

			if p.network.IsBitcoinNet() {
				utxoAssets := p.network.utxoAssets[index]
				addr := hex.EncodeToString(txOut.PkScript)
				assetmap, ok := p.network.addrAssetMap[addr]
				if !ok {
					assetmap = make(map[swire.AssetName]*Decimal)
					p.network.addrAssetMap[addr] = assetmap
				}
				for _, asset := range utxoAssets {
					if asset.Name.Protocol == indexer.PROTOCOL_NAME_BRC20 {
						total := assetmap[asset.Name]
						assetmap[asset.Name] = total.Add(&asset.Amount)

						// 设置invalid
						// utxoAssets = nil
						// utxoOffsets = nil
						invalidmap, ok := p.network.invalids[utxo]
						if !ok {
							invalidmap = make(map[swire.AssetName]bool)
							p.network.invalids[utxo] = invalidmap
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
			spendTx, ok := p.network.utxoUsed[utxo]
			if ok {
				return "", fmt.Errorf("utxo %s spent in %s", utxo, spendTx)
			}
		}
		transferFrom, ok := p.network.transferInfo[utxo]
		if ok {
			from := transferFrom.Address
			transferMap, ok := p.network.addrTransferMap[from]
			if !ok {
				return "", fmt.Errorf("can't find transfer map")
			}
			utxomap, ok := transferMap[*transferFrom.AssetName]
			if !ok {
				return "", fmt.Errorf("can't find utxo map")
			}

			assetmap, ok := p.network.addrAssetMap[from]
			if !ok {
				return "", fmt.Errorf("no asset to transfer")
			}
			total := assetmap[*transferFrom.AssetName]
			if total.Cmp(transferFrom.Amt) < 0 {
				return "", fmt.Errorf("no enough asset to transfer")
			}
			assetmap[*transferFrom.AssetName] = total.Sub(transferFrom.Amt)

			delete(utxomap, transferFrom.Utxo)
			delete(p.network.transferInfo, transferFrom.Utxo)
			status = 3
		}

		p.network.utxoUsed[utxo] = tx.TxID()
		index, ok := p.network.utxoIndex[utxo]
		if !ok {
			return "", fmt.Errorf("can't find utxo %s", utxo)
		}
		value := p.network.utxoValue[index]
		txAssets := p.network.utxoAssets[index]
		txOffsets := p.network.offsets[index]
		satBindingMap := make(map[int64]*indexer.AssetInfo)
		if p.network.IsBitcoinNet() && len(txAssets) == 1 && txAssets[0].Name.Protocol == indexer.PROTOCOL_NAME_BRC20 {
			if len(txOffsets) != 1 {
				Log.Panic("")
			}
			for k, v := range txOffsets {
				if len(v) != 1 && k != txAssets[0].Name {
					Log.Panic("")
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
		invalidmap, existing := p.network.invalids[utxo]
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
							Log.Errorf("deploy, tick length 5, but not self_mint")
							continue
						}
					}
					assetName := indexer.AssetName{
						Protocol: "brc20",
						Type:     "f",
						Ticker:   deployInfo.Ticker,
					}

					_, ok := _tickerInfoRunning[assetName.String()]
					if ok {
						Log.Warnf("ticker %s exists", deployInfo.Ticker)
						continue
					}

					dec, err := strconv.Atoi(deployInfo.Decimal)
					if err != nil {
						Log.Warnf("invalid dec %s", deployInfo.Decimal)
					}
					tickerInfo := indexer.TickerInfo{
						AssetName:    assetName,
						Divisibility: dec,
						Limit:        deployInfo.Lim,
						TotalMinted:  "0",
						MaxSupply:    deployInfo.Max,
					}
					_tickerInfoRunning[assetName.String()] = &tickerInfo

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
					ticker, ok := _tickerInfoRunning[assetName.String()]
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
						_tickerInfoRunning[tickerInfo.AssetName.String()] = &tickerInfo

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
		p.network.utxos = append(p.network.utxos, utxo)
		index := len(p.network.utxos) - 1
		p.network.utxoIndex[utxo] = index
		p.network.utxoValue = append(p.network.utxoValue, txOut.Value)
		j := insertPkScript(txOut.PkScript)
		p.network.utxoOwner = append(p.network.utxoOwner, j)

		var curr *indexer.TxOutput
		Log.Infof("before cut: %v", *input)
		curr, input, err = input.Cut(txOut.Value)
		Log.Infof("after cut: %v\n%v", curr, input)
		if err != nil {
			Log.Panicf("Cut failed, %v", err)
		}

		var utxoAssets swire.TxAssets
		var utxoOffsets map[swire.AssetName]indexer.AssetOffsets
		utxoAssets = curr.Assets
		utxoOffsets = curr.Offsets
		if p.network.IsBitcoinNet() && len(utxoAssets) == 1 && utxoAssets[0].Name.Protocol == indexer.PROTOCOL_NAME_BRC20 {
			// status = 1 或者2，一个tx中只有一个，但3可能有多个output
			switch status {
			case 1: // brc20 mint
				addr := hex.EncodeToString(txOut.PkScript)
				assetmap, ok := p.network.addrAssetMap[addr]
				if !ok {
					assetmap = make(map[swire.AssetName]*Decimal)
					p.network.addrAssetMap[addr] = assetmap
				}
				asset := utxoAssets[0]
				total := assetmap[asset.Name]
				assetmap[asset.Name] = total.Add(&asset.Amount)

				// 设置invalid
				// utxoAssets = nil
				// utxoOffsets = nil
				invalidmap, ok := p.network.invalids[utxo]
				if !ok {
					invalidmap = make(map[swire.AssetName]bool)
					p.network.invalids[utxo] = invalidmap
				}
				invalidmap[asset.Name] = true
			case 2: // brc20 transfer
				addr := hex.EncodeToString(txOut.PkScript)
				assetmap, ok := p.network.addrAssetMap[addr]
				if !ok {
					return "", fmt.Errorf("no asset to transfer")
				}
				asset := utxoAssets[0]
				total := assetmap[asset.Name]
				if total.Cmp(&asset.Amount) < 0 {
					return "", fmt.Errorf("no enough asset to transfer")
				}
				transferMap, ok := p.network.addrTransferMap[addr]
				if !ok {
					transferMap = make(map[swire.AssetName]map[string]bool)
					p.network.addrTransferMap[addr] = transferMap
				}
				utxomap, ok := transferMap[asset.Name]
				if !ok {
					utxomap = make(map[string]bool)
					transferMap[asset.Name] = utxomap
				}
				utxomap[utxo] = true

				p.network.transferInfo[utxo] = &Brc20Transfer{
					Utxo:      utxo,
					Address:   addr,
					AssetName: &asset.Name,
					Amt:       asset.Amount.Clone(),
				}

			case 3:
				// brc20 的转移
				to := hex.EncodeToString(txOut.PkScript)
				assetInfo := utxoAssets[0]
				assetmap, ok := p.network.addrAssetMap[to]
				if !ok {
					assetmap = make(map[swire.AssetName]*Decimal)
					p.network.addrAssetMap[to] = assetmap
				}
				total := assetmap[assetInfo.Name]
				assetmap[assetInfo.Name] = total.Add(&assetInfo.Amount)

				// 暂时保留，但是设置为invalid
				// utxoAssets = nil
				// utxoOffsets = nil
				invalidmap, ok := p.network.invalids[utxo]
				if !ok {
					invalidmap = make(map[swire.AssetName]bool)
					p.network.invalids[utxo] = invalidmap
				}
				invalidmap[assetInfo.Name] = true

			default:

			}
		}
		p.network.utxoAssets = append(p.network.utxoAssets, utxoAssets)
		p.network.offsets = append(p.network.offsets, utxoOffsets)

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
		index, ok := p.network.utxoIndex[premineOutput]
		if !ok {
			return "", fmt.Errorf("can't find utxo %s", premineOutput)
		}
		err = p.network.utxoAssets[index].Add(premineAssetInfo)
		if err != nil {
			return "", err
		}
	}

	if runePointer != nil {
		runesOutputUtxo = fmt.Sprintf("%s:%d", tx.TxID(), *runePointer)
		if firstRuneOutputUtxo != "" && firstRuneOutputUtxo != runesOutputUtxo {
			index1, ok := p.network.utxoIndex[firstRuneOutputUtxo]
			if !ok {
				return "", fmt.Errorf("can't find utxo %s", firstRuneOutputUtxo)
			}
			index2, ok := p.network.utxoIndex[runesOutputUtxo]
			if !ok {
				return "", fmt.Errorf("can't find utxo %s", runesOutputUtxo)
			}
			for i := 0; i < len(p.network.utxoAssets[index1]); {
				asset := p.network.utxoAssets[index1][i]
				if asset.Name.Protocol != indexer.PROTOCOL_NAME_RUNES {
					i++
					continue
				}
				err = p.network.utxoAssets[index2].Add(&asset)
				if err != nil {
					return "", err
				}
				err = p.network.utxoAssets[index1].Subtract(&asset)
				if err != nil {
					return "", err
				}
			}
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

func (p *TestIndexerClient) BroadCastTxs(txs []*wire.MsgTx) error {
	for i, tx := range txs {
		txId, err := p.BroadCastTx(tx)
		if err != nil {
			return fmt.Errorf("BroadCastTx %d failed, %v", i, err)
		}
		fmt.Printf("%d %s broadcasted", i, txId)
	}

	return nil
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
		p.generateNewBlock()
		fmt.Printf("satsnet genesis txId %s\n", tx.TxID())
		return tx.TxID(), nil
	}

	var inputAddress string
	var anchorData *AnchorData
	for _, txIn := range tx.TxIn {
		utxo := txIn.PreviousOutPoint.String()
		if txIn.PreviousOutPoint.Index == swire.AnchorTxOutIndex {
			anchorData, _, err = CheckAnchorPkScript(tx.TxIn[0].SignatureScript)
			if err == nil {
				p.network.ascendMap[anchorData.Utxo] = tx.TxID()
			}
		} else if txIn.PreviousOutPoint.Index < swire.AnchorTxOutIndex {
			spendTx, ok := p.network.utxoUsed[utxo]
			if ok {
				return "", fmt.Errorf("utxo %s spent in %s", utxo, spendTx)
			}
			if inputAddress == "" {
				index, ok := p.network.utxoIndex[utxo]
				if ok {
					pkScript, err := hex.DecodeString(_pkScripts[p.network.utxoOwner[index]])
					if err == nil {
						inputAddress, _ = getSatsNetAddressFromPkScript(pkScript)
					}
				}
			}
		}
		p.network.utxoUsed[utxo] = tx.TxID()
	}

	var descendTxOut *swire.TxOut
	var descendTxId string
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

		ctype, data, err := sindexer.ReadDataFromNullDataScript(txOut.PkScript)
		if err == nil {
			switch ctype {
			case sindexer.CONTENT_TYPE_DESCENDING:
				descendTxOut = txOut
				descendTxId = string(data)
				p.network.descendMap[string(data)] = utxo

			case sindexer.CONTENT_TYPE_STAKE:
				if anchorData != nil {
					p.addTestMinerNodeFromAnchor(anchorData, tx.TxID())
				}

			case sindexer.CONTENT_TYPE_UNSTAKE:
				if descendTxOut != nil {
					p.removeTestMinerNodeFromDescend(inputAddress, p.network.height, descendTxOut, data, descendTxId)
				}
			}
		}
	}

	fmt.Printf("BroadCastTx_SatsNet succeeded. %s\n", tx.TxID())
	return tx.TxID(), nil
}

func (p *TestIndexerClient) addTestMinerNodeFromAnchor(data *AnchorData, anchorTxId string) {
	pubA, pubB, err := getAnchorPubKeysForTest(data)
	if err != nil {
		fmt.Printf("addTestMinerNodeFromAnchor get pubkeys failed: %v\n", err)
		return
	}
	if !hasTestStakeEligibility(p.network.height, data.Assets) {
		return
	}

	channelAddr, err := GetP2WSHaddress(pubA, pubB)
	if err != nil {
		fmt.Printf("addTestMinerNodeFromAnchor GetP2WSHaddress failed: %v\n", err)
		return
	}
	parentKey := hex.EncodeToString(pubA)
	nodeKey := hex.EncodeToString(pubB)
	info := (&sindexer.AscendData{
		Height:      p.network.height,
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
		_coreNodeMap[nodeKey] = true
		_minerInfoMap[nodeKey] = info
		addTestCoreNodeChild(parentKey, nodeKey, p.network.height, data.Utxo)
		if _, ok := _coreNodeChildMap[nodeKey]; !ok {
			_coreNodeChildMap[nodeKey] = make(map[string]*sindexer.MinerAscendInfo)
		}
		fmt.Printf("test indexer add core node %s at height %d\n", nodeKey, p.network.height)
		return
	}

	if _, ok := _coreNodeMap[parentKey]; !ok {
		return
	}
	_minerInfoMap[nodeKey] = info
	addTestCoreNodeChild(parentKey, nodeKey, p.network.height, data.Utxo)
	fmt.Printf("test indexer add miner node %s under %s at height %d\n", nodeKey, parentKey, p.network.height)
}

func (p *TestIndexerClient) removeTestMinerNodeFromDescend(channelAddr string, height int,
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

	nodeKey, minerInfo := findTestMinerByChannel(channelAddr)
	if nodeKey == "" || minerInfo == nil {
		fmt.Printf("removeTestMinerNodeFromDescend can't find miner from channel %s\n", channelAddr)
		return
	}

	if _, ok := _coreNodeMap[nodeKey]; ok {
		if len(_coreNodeChildMap[nodeKey]) != 0 {
			fmt.Printf("removeTestMinerNodeFromDescend core node %s still has child miners\n", nodeKey)
			return
		}
		deleteTestCoreNodeChild(minerInfo.ServerNode, nodeKey)
		delete(_coreNodeMap, nodeKey)
		delete(_coreNodeChildMap, nodeKey)
		delete(_minerInfoMap, nodeKey)
		fmt.Printf("test indexer remove core node %s at tx %s\n", nodeKey, descendTxId)
		return
	}

	deleteTestCoreNodeChild(minerInfo.ServerNode, nodeKey)
	delete(_minerInfoMap, nodeKey)
	fmt.Printf("test indexer remove miner node %s at tx %s\n", nodeKey, descendTxId)
}

func getAnchorPubKeysForTest(data *AnchorData) ([]byte, []byte, error) {
	addrType, addresses, _, err := stxscript.ExtractPkScriptAddrs(data.WitnessScript, GetChainParam_SatsNet())
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
	if VerifyMessage(pubKeyA, invoice, data.Sig) {
		return pubA, pubB, nil
	}
	pubKeyB, err := utils.BytesToPublicKey(pubB)
	if err != nil {
		return nil, nil, fmt.Errorf("BytesToPublicKey failed. %v", err)
	}
	if VerifyMessage(pubKeyB, invoice, data.Sig) {
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

func addTestCoreNodeChild(parentKey, childKey string, height int, ascendUtxo string) {
	childMap, ok := _coreNodeChildMap[parentKey]
	if !ok {
		childMap = make(map[string]*sindexer.MinerAscendInfo)
		_coreNodeChildMap[parentKey] = childMap
	}
	childMap[childKey] = &sindexer.MinerAscendInfo{
		AscendHeight: height,
		AscendUtxo:   ascendUtxo,
	}
}

func deleteTestCoreNodeChild(parentKey, childKey string) {
	if childMap, ok := _coreNodeChildMap[parentKey]; ok {
		delete(childMap, childKey)
	}
}

func findTestMinerByChannel(channelAddr string) (string, *sindexer.MinerInfo) {
	for nodeKey, info := range _minerInfoMap {
		if info.ChannelAddr == channelAddr {
			return nodeKey, info
		}
	}
	return "", nil
}

func getSatsNetAddressFromPkScript(pkScript []byte) (string, error) {
	_, addresses, _, err := stxscript.ExtractPkScriptAddrs(pkScript, GetChainParam_SatsNet())
	if err != nil {
		return "", err
	}
	if len(addresses) == 0 {
		return "", fmt.Errorf("can't extract satsnet address")
	}
	return addresses[0].EncodeAddress(), nil
}

func (p *TestIndexerClient) BroadCastTxs_SatsNet(txs []*swire.MsgTx) error {
	for _, tx := range txs {
		_, err := p.BroadCastTx_SatsNet(tx)
		if err != nil {
			return err
		}
	}

	return nil
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

func (p *TestNodeClient) GetContractInvokeItemByInUtxoReq(contractUrl, inUtxo string) (string, error) {
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

func (p *TestNodeClient) SendPerformRemoteActionReq(info *RemoteActionPerformReservation) error {
	return fmt.Errorf("not implemented")
}

func (p *TestNodeClient) SendPerformRemoteActionAckReq(info *RemoteActionPerformReservation) error {
	return fmt.Errorf("not implemented")
}

func (p *TestNodeClient) SendOpenChannelReq(info *FundingReservation) error {
	_, err := p.SendChannelOpenReq(&wwire.ChannelOpenReq{
		OpenChannelRequest: *info.Req,
		Sig:                info.ReqSig,
	})
	return err
}

func (p *TestNodeClient) SendFundingCreatedReq(info *FundingReservation) error {
	_, err := p.SendChannelFundingCreatedReq(&wwire.FundingCreatedReq{
		FundingCreated: *info.FundingCreated,
	})
	return err
}

func (p *TestNodeClient) SendFundingBroadcastedReq(info *FundingReservation) error {
	_, err := p.SendChannelFundingBroadcastedReq(&wwire.FundingBroadcastedReq{
		FundingBroadcasted: *info.FundingBroadcasted,
	})
	return err
}

func (p *TestNodeClient) SendCloseChannelReq(info *ClosingReservation) error {
	_, err := p.SendChannelCloseReq(&wwire.ChannelCloseReq{
		CloseChannelRequest: *info.Req,
		Sig:                 info.ReqSig,
	})
	return err
}

func (p *TestNodeClient) SendClosingSignedReq(info *ClosingReservation) error {
	_, err := p.SendChannelClosingSignedReq(&wwire.ClosingSignedReq{
		ChannelId:     info.ChannelId,
		ClosingSigned: *info.LocalSigned,
	})
	return err
}

func (p *TestNodeClient) SendClosingBroadcastedReq(info *ClosingReservation) error {
	_, err := p.SendChannelClosingBroadcastedReq(&wwire.ClosingBroadcastedReq{
		ChannelId:          info.ChannelId,
		ClosingBroadcasted: *info.ClosingBroadcasted,
	})
	return err
}

func (p *TestNodeClient) SendUnlockReq(info *PaymentReservation) error {
	_, err := p.SendChannelUnlockReq(&wwire.UnlockReq{
		UnlockRequest: *info.UnlockReq,
		Sig:           info.ReqSig,
	})
	return err
}

func (p *TestNodeClient) SendUnlockCommitSigReq(info *PaymentReservation) error {
	_, err := p.SendChannelUnlockCommitSigReq(&wwire.UnlockCommitSigReq{})
	return err
}

func (p *TestNodeClient) SendUnlockRevokeAndAckReq(info *PaymentReservation) error {
	_, err := p.SendChannelUnlockRevokeAndAckReq(&wwire.UnlockRevokeAndAckReq{})
	return err
}

func (p *TestNodeClient) SendRecoverPaymentReq(info *PaymentReservation) error {
	_, err := p.SendChannelRecoverPaymentReq(&wwire.RecoverPaymentRequireReq{})
	return err
}

func (p *TestNodeClient) SendRecoverPaymentCommitSigReq(info *PaymentReservation) error {
	_, err := p.SendChannelRecoverPaymentCommitSigReq(&wwire.RecoverPaymentCommitSigReq{})
	return err
}

func (p *TestNodeClient) SendRecoverPaymentRevokeAndAckReq(info *PaymentReservation) error {
	_, err := p.SendChannelRecoverPaymentRevokeAndAckReq(&wwire.RecoverPaymentRevokeAndAckReq{})
	return err
}

func (p *TestNodeClient) SendLockReq(info *PaymentReservation) error {
	_, err := p.SendChannelLockReq(&wwire.LockReq{
		LockRequest: *info.LockReq,
		Sig:         info.ReqSig,
	})
	return err
}

func (p *TestNodeClient) SendLockCommitSigAndRevokeReq(info *PaymentReservation) error {
	_, err := p.SendChannelLockCommitSigAndRevokeReq(&wwire.LockCommitSigAndRevokeReq{})
	return err
}

func (p *TestNodeClient) SendLockAckReq(info *PaymentReservation) error {
	_, err := p.SendChannelLockAckReq(&wwire.LockAckReq{})
	return err
}

func (p *TestNodeClient) SendSplicingInReq(info *SplicingReservation) error {
	_, err := p.SendChannelSplicingInReq(&wwire.SplicingInReq{
		SplicingInRequest: *info.InReq,
		Sig:               info.ReqSig,
	})
	return err
}

func (p *TestNodeClient) SendSplicingInCommitSigReq(info *SplicingReservation) error {
	_, err := p.SendChannelSplicingInCommitSigReq(&wwire.SplicingInCommitSigReq{})
	return err
}

func (p *TestNodeClient) SendSplicingInRevokeAndAckReq(info *SplicingReservation) error {
	_, err := p.SendChannelSplicingInRevokeAndAckReq(&wwire.SplicingInRevokeAndAckReq{})
	return err
}

func (p *TestNodeClient) SendSplicingOutReq(info *SplicingReservation) error {
	_, err := p.SendChannelSplicingOutReq(&wwire.SplicingOutReq{
		SplicingOutRequest: *info.OutReq,
		Sig:                info.ReqSig,
	})
	return err
}

func (p *TestNodeClient) SendSplicingOutCommitSigReq(info *SplicingReservation) error {
	_, err := p.SendChannelSplicingOutCommitSigReq(&wwire.SplicingOutCommitSigReq{})
	return err
}

func (p *TestNodeClient) SendSplicingOutRevokeAndAckReq(info *SplicingReservation) error {
	_, err := p.SendChannelSplicingOutRevokeAndAckReq(&wwire.SplicingOutRevokeAndAckReq{})
	return err
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

func (p *TestNodeClient) SendChannelOpenReq(req *wwire.ChannelOpenReq) (*wwire.ChannelOpenResp, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestNodeClient) SendChannelFundingCreatedReq(req *wwire.FundingCreatedReq) (*wwire.FundingCreatedResp, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestNodeClient) SendChannelFundingBroadcastedReq(req *wwire.FundingBroadcastedReq) (*wwire.FundingBroadcastedResp, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestNodeClient) SendChannelCloseReq(req *wwire.ChannelCloseReq) (*wwire.ChannelCloseResp, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestNodeClient) SendChannelClosingSignedReq(req *wwire.ClosingSignedReq) (*wwire.ClosingSignedResp, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestNodeClient) SendChannelClosingBroadcastedReq(req *wwire.ClosingBroadcastedReq) (*wwire.ClosingBroadcastedResp, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestNodeClient) SendChannelLockReq(req *wwire.LockReq) (*wwire.LockResp, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestNodeClient) SendChannelLockCommitSigAndRevokeReq(req *wwire.LockCommitSigAndRevokeReq) (*wwire.LockCommitSigAndRevokeResp, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestNodeClient) SendChannelLockAckReq(req *wwire.LockAckReq) (*wwire.LockAckResp, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestNodeClient) SendChannelUnlockReq(req *wwire.UnlockReq) (*wwire.UnlockResp, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestNodeClient) SendChannelUnlockCommitSigReq(req *wwire.UnlockCommitSigReq) (*wwire.UnlockCommitSigResp, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestNodeClient) SendChannelUnlockRevokeAndAckReq(req *wwire.UnlockRevokeAndAckReq) (*wwire.UnlockRevokeAndAckResp, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestNodeClient) SendChannelRecoverPaymentReq(req *wwire.RecoverPaymentRequireReq) (*wwire.RecoverPaymentRequireResp, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestNodeClient) SendChannelRecoverPaymentCommitSigReq(req *wwire.RecoverPaymentCommitSigReq) (*wwire.RecoverPaymentCommitSigResp, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestNodeClient) SendChannelRecoverPaymentRevokeAndAckReq(req *wwire.RecoverPaymentRevokeAndAckReq) (*wwire.RecoverPaymentRevokeAndAckResp, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestNodeClient) SendChannelSplicingInReq(req *wwire.SplicingInReq) (*wwire.SplicingInResp, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestNodeClient) SendChannelSplicingInCommitSigReq(req *wwire.SplicingInCommitSigReq) (*wwire.SplicingInCommitSigResp, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestNodeClient) SendChannelSplicingInRevokeAndAckReq(req *wwire.SplicingInRevokeAndAckReq) (*wwire.SplicingInRevokeAndAckResp, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestNodeClient) SendChannelSplicingOutReq(req *wwire.SplicingOutReq) (*wwire.SplicingOutResp, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestNodeClient) SendChannelSplicingOutCommitSigReq(req *wwire.SplicingOutCommitSigReq) (*wwire.SplicingOutCommitSigResp, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestNodeClient) SendChannelSplicingOutRevokeAndAckReq(req *wwire.SplicingOutRevokeAndAckReq) (*wwire.SplicingOutRevokeAndAckResp, error) {
	return nil, fmt.Errorf("not implemented")
}
