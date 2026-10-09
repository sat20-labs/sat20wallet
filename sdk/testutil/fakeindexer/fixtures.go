package fakeindexer

import (
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func NewTestNetworks() (*Network, *Network) {
	state := NewState()
	bitcoin := &Network{
		Name:          "bitcoin",
		Height:        0,
		Blocks:        make(map[int][]string),
		TxBroadcasted: make(map[string]string),
		Utxos:         make([]string, 0),
		UtxoUsed:      make(map[string]string),
		UtxoIndex:     make(map[string]int),
		AscendMap:     make(map[string]string),
		DescendMap:    make(map[string]string),

		UtxoValue: []int64{
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

		UtxoAssets: []indexer.TxAssets{
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

		Offsets: []map[swire.AssetName]indexer.AssetOffsets{
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

		UtxoOwner: []int{
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

		AddrAssetMap:    map[string]map[swire.AssetName]*Decimal{},
		AddrTransferMap: map[string]map[swire.AssetName]map[string]bool{},
		TransferInfo:    map[string]*Brc20Transfer{},
		Invalids:        map[string]map[swire.AssetName]bool{},
		KvCache:         map[string]*indexerwire.KeyValue{},
		DkvsRecords:     map[string]*swire.DKVSRecord{},
		DkvsDeleted:     map[string]dkvsindexer.DKVSKeyState{},
		DkvsGenerations: map[string]uint64{},
	}

	satoshinet := &Network{
		Name:          "satoshinet",
		Height:        0,
		Blocks:        make(map[int][]string),
		TxBroadcasted: make(map[string]string),

		Utxos:           make([]string, 0),
		UtxoUsed:        make(map[string]string),
		UtxoIndex:       make(map[string]int),
		AscendMap:       make(map[string]string),
		DescendMap:      make(map[string]string),
		KvCache:         make(map[string]*indexerwire.KeyValue),
		DkvsRecords:     make(map[string]*swire.DKVSRecord),
		DkvsDeleted:     make(map[string]dkvsindexer.DKVSKeyState),
		DkvsGenerations: make(map[string]uint64),

		UtxoValue: []int64{
			20000, 20000, 20000, 2000000,
			10000000, 1000, 1000, 1000000,
			10000000, 1000, 1000, 1000000,
			20000, 20000, 20000, 2000000,
		},

		UtxoAssets: []indexer.TxAssets{
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

		Offsets: []map[swire.AssetName]indexer.AssetOffsets{
			nil, nil, nil, nil,
			nil, nil, nil, nil,
			nil, nil, nil, nil,
			nil, nil, nil, nil,
		},

		UtxoOwner: []int{
			0, 0, 0, 0,
			3, 3, 3, 3,
			0, 0, 0, 0,
			1, 1, 1, 1,
		},
	}

	bitcoin.State, satoshinet.State = state, state
	return bitcoin, satoshinet
}
func defaultTickers() map[string]*indexer.TickerInfo {
	return map[string]*indexer.TickerInfo{
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
}
func NewState() *State {
	_coreNodeMap := map[string]bool{ // pubkey
		"025fb789035bc2f0c74384503401222e53f72eefdebf0886517ff26ac7985f52ad": true,
		"0367f26af23dc40fdad06752c38264fe621b7bbafb1d41ab436b87ded192f1336e": true,
	}
	_minerInfoMap := map[string]*sindexer.MinerInfo{}                      // pubkey
	_coreNodeChildMap := map[string]map[string]*sindexer.MinerAscendInfo{} // pubkey->child pubkey

	_pkScripts := []string{
		"51208c4a6b130077db156fb22e7946711377c06327298b4c7e6e19a6eaa808d19eba", // client
		"51206b8e69003724d73623f173d9b1584df34cad6a4ec046272de0ccf4a09041682f", // server-2
		"5120d2912b91d0802aa584f4c8ff364f9bb2d5af103368fef4c61584b34f1f081f8b", // bootstrap
		"0020d7d42c2c26031ccb27d14ccc0cbb22b45664fc4e8c325b9d5c317a3b4336c0e1", // a-s2 channel
		"002071f5786fd95a6b2c0008a53462d61a85ba513d512f6ff7ae1f87183a2e966be6", // s2-dao channel
		"00205b7208d774f8958d776869e090950c4ce5d55b656d52f0f7fa98ee37ae541948", // a-s1 channel
		"0020c98fce9212d1f0c286fed2e9f8355ac507bfcb5eb50d285df21645e1032765ab", // s1-dao channel
		"512017abefbc099ae2053a210b6b4e69fe18a197a3a7a7cac6497891c17c7653c821", // server-1
	}

	_nameMap := map[string]*indexerwire.OrdinalsName{
		"bigdaddy": {
			NftItem: indexerwire.NftItem{
				Id:      0,
				Name:    "bigdaddy",
				Address: "tb1p339xkycqwld32maj9eu5vugnwlqxxfef3dx8umse5m42szx3n6aq6qv65g",
			},
		},
	}

	// CreateCoinbaseTx 创建一个 coinbase 交易

	return &State{PkScripts: _pkScripts, Tickers: defaultTickers(), CoreNodes: _coreNodeMap, MinerInfo: _minerInfoMap, CoreChildren: _coreNodeChildMap, Names: _nameMap}
}
