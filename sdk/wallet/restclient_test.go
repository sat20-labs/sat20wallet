package wallet

import (
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/btcsuite/btcd/wire"
	swire "github.com/sat20-labs/satsnet_btcd/wire"

	"github.com/sat20-labs/sat20wallet/sdk/wallet/indexer"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/sindexer"
)

var _mutex sync.RWMutex

var _utxos = []string{
	// open
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:0",
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:1",
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:2",
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:3",

	// ordx
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:4",
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:5",
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:6",
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:7",

	// runes
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:8",
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:9",
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:10",
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:11",

	// brc20
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:12",
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:13",
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:14",
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:15",

	// rare
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:16",
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:17",
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:18",
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:19",

	// fees
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:20",
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:21",
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:22",
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:23",

	// other
}

var _txmap = map[string]bool{
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7": true,
}

var _utxoIndex = map[string]int{
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:0":  0,
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:1":  1,
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:2":  2,
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:3":  3,
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:4":  4,
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:5":  5,
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:6":  6,
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:7":  7,
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:8":  8,
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:9":  9,
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:10": 10,
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:11": 11,
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:12": 12,
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:13": 13,
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:14": 14,
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:15": 15,
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:16": 16,
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:17": 17,
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:18": 18,
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:19": 19,
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:20": 20,
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:21": 21,
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:22": 22,
	"4dab4d1ad653f9da1f923ac65a12277e427c5d4b3e1a98ffbc510d87df46eef7:23": 23,
}

var _utxoValue = []int64{
	20000, 20000, 20000, 20000,
	10000, 10000, 10000, 10000,
	10000, 10000, 10000, 10000,
	330, 546, 600, 1000,
	330, 546, 10000, 10000,
	10000, 10000, 10000, 10000,
}

var _utxoAssets = []indexer.TxAssets{
	nil, nil, nil, nil,

	{
		{Name: swire.AssetName{Protocol: "ordx", Type: "f", Ticker: "pizza"}, Amount: 10000, BindingSat: 1},
	},
	{
		{Name: swire.AssetName{Protocol: "ordx", Type: "f", Ticker: "pizza"}, Amount: 9000, BindingSat: 1},
	},
	{
		{Name: swire.AssetName{Protocol: "ordx", Type: "f", Ticker: "pearl"}, Amount: 8000, BindingSat: 1},
	},
	{
		{Name: swire.AssetName{Protocol: "ordx", Type: "f", Ticker: "pearl"}, Amount: 6000, BindingSat: 1},
	},

	{
		{Name: swire.AssetName{Protocol: "runes", Type: "f", Ticker: "840000_1"}, Amount: 1000, BindingSat: 0},
	},
	{
		{Name: swire.AssetName{Protocol: "runes", Type: "f", Ticker: "840000_1"}, Amount: 1000, BindingSat: 0},
	},
	{
		{Name: swire.AssetName{Protocol: "runes", Type: "f", Ticker: "840000_2"}, Amount: 100, BindingSat: 0},
	},
	{
		{Name: swire.AssetName{Protocol: "runes", Type: "f", Ticker: "840000_2"}, Amount: 10, BindingSat: 0},
	},

	{
		{Name: swire.AssetName{Protocol: "brc20", Type: "f", Ticker: "ordi"}, Amount: 1000, BindingSat: 0},
	},
	{
		{Name: swire.AssetName{Protocol: "brc20", Type: "f", Ticker: "ordi"}, Amount: 100, BindingSat: 0},
	},
	{
		{Name: swire.AssetName{Protocol: "brc20", Type: "f", Ticker: "ordi"}, Amount: 10, BindingSat: 0},
	},
	{
		{Name: swire.AssetName{Protocol: "brc20", Type: "f", Ticker: "ordi"}, Amount: 1, BindingSat: 0},
	},

	{
		{Name: swire.AssetName{Protocol: "ordx", Type: "e", Ticker: "pizza"}, Amount: 10000, BindingSat: 1},
	},
	{
		{Name: swire.AssetName{Protocol: "ordx", Type: "e", Ticker: "pizza"}, Amount: 9000, BindingSat: 1},
	},
	{
		{Name: swire.AssetName{Protocol: "ordx", Type: "e", Ticker: "vintage"}, Amount: 8000, BindingSat: 1},
	},
	{
		{Name: swire.AssetName{Protocol: "ordx", Type: "e", Ticker: "vintage"}, Amount: 6000, BindingSat: 1},
	},

	nil, nil, nil, nil,
}

var _offsets = []indexer.AssetOffsets{
	nil, nil, nil, nil,

	{{Start: 0, End: 10000}},
	{{Start: 0, End: 9000}},
	{{Start: 1000, End: 9000}},
	{{Start: 0, End: 1000}, {Start: 3000, End: 4000}, {Start: 5000, End: 9000}},

	nil, nil, nil, nil,
	nil, nil, nil, nil,

	{{Start: 0, End: 10000}},
	{{Start: 0, End: 9000}},
	{{Start: 1000, End: 9000}},
	{{Start: 0, End: 1000}, {Start: 3000, End: 4000}, {Start: 5000, End: 9000}},

	nil, nil, nil, nil,
}

var _tickerInfo = map[string]*indexer.TickerInfo{
	"runes:f:840000_1": {
		AssetName: swire.AssetName{
			Protocol: indexer.PROTOCOL_NAME_RUNES,
			Ticker:   "840000:1",
		},
		Divisibility: 0,
		TotalMinted:  "100000000",
		MaxSupply:    "100000000",
	},
	"runes:f:840000_2": {
		AssetName: swire.AssetName{
			Protocol: indexer.PROTOCOL_NAME_RUNES,
			Ticker:   "840000_2",
		},
		Divisibility: 2,
		TotalMinted:  "21000000",
		MaxSupply:    "100000000",
	},
	"runes:f:39241_1": {
		AssetName: swire.AssetName{
			Protocol: indexer.PROTOCOL_NAME_RUNES,
			Ticker:   "39241_1",
		},
		Divisibility: 1,
		TotalMinted:  "21000000",
		MaxSupply:    "100000000000000100000000000000",
	},
	"brc20:f:ordi": {
		AssetName: swire.AssetName{
			Protocol: indexer.PROTOCOL_NAME_BRC20,
			Ticker:   "ordi",
		},
		Divisibility: 18,
		TotalMinted:  "21000000000000000000000000", // 21,000,000
		MaxSupply:    "21000000000000000000000000",
	},
	"ordx:f:pizza": {
		AssetName: swire.AssetName{
			Protocol: indexer.PROTOCOL_NAME_ORDX,
			Ticker:   "pizza",
		},
		Divisibility: 0,
		TotalMinted:  "100000000",
		MaxSupply:    "100000000",
	},
	"ordx:f:pearl": {
		AssetName: swire.AssetName{
			Protocol: indexer.PROTOCOL_NAME_ORDX,
			Ticker:   "pearl",
		},
		Divisibility: 0,
		TotalMinted:  "200000000",
		MaxSupply:    "200000000",
	},
}

var _pkScripts = []string{
	"51208c4a6b130077db156fb22e7946711377c06327298b4c7e6e19a6eaa808d19eba", // client
	"512017abefbc099ae2053a210b6b4e69fe18a197a3a7a7cac6497891c17c7653c821", // server
	"5120d2912b91d0802aa584f4c8ff364f9bb2d5af103368fef4c61584b34f1f081f8b", // bootstrap
	"00205b7208d774f8958d776869e090950c4ce5d55b656d52f0f7fa98ee37ae541948", // a-s channel

	"0020c98fce9212d1f0c286fed2e9f8355ac507bfcb5eb50d285df21645e1032765ab", // s-dao channel
}

var _utxoOwner = []int{
	1, 1, 0, 0,
	0, 0, 0, 0,
	0, 0, 0, 0,
	0, 0, 0, 0,
	0, 0, 0, 0,
	0, 0, 0, 0,
}

type TestIndexerClient struct {
	*RESTClient
}

func NewTestIndexerClient() *TestIndexerClient {
	client := NewRESTClient("", "", nil)
	return &TestIndexerClient{
		RESTClient: client,
	}
}

func (p *TestIndexerClient) GetTxOutput(utxo string) (*TxOutput, error) {
	_mutex.RLock()
	defer _mutex.RUnlock()

	index, ok := _utxoIndex[utxo]
	if !ok {
		return nil, fmt.Errorf("can't find utxo %s", utxo)
	}

	offsets := make(map[swire.AssetName]indexer.AssetOffsets)
	if _offsets[index] != nil {
		txAssets := _utxoAssets[index]
		offsets[txAssets[0].Name] = _offsets[index]
	}

	pkScript, _ := hex.DecodeString(_pkScripts[_utxoOwner[index]])

	output := TxOutput{
		OutPointStr: utxo,
		OutValue:    wire.TxOut{Value: _utxoValue[index], PkScript: pkScript},
		Assets:      _utxoAssets[index],
		Offsets:     offsets,
	}

	return &output, nil
}

func (p *TestIndexerClient) GetAscendData(utxo string) (*sindexer.AscendData, error) {
	return nil, fmt.Errorf("not implemented")
}

// 只有未花费的能拿到id
func (p *TestIndexerClient) GetUtxoId(utxo string) (uint64, error) {
	_mutex.RLock()
	defer _mutex.RUnlock()

	index, ok := _utxoIndex[utxo]
	if !ok {
		return INVALID_ID, fmt.Errorf("can't find utxo %s", utxo)
	}
	return uint64(index), nil
}

// btcutil.Tx
func (p *TestIndexerClient) GetRawTx(tx string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

// btcutil.Tx
func (p *TestIndexerClient) GetTxInfo(tx string) (*indexer.TxSimpleInfo, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestIndexerClient) GetTxHeight(tx string) (int, error) {
	return -1, fmt.Errorf("not implemented")
}

func (p *TestIndexerClient) IsTxConfirmed(tx string) bool {
	return true
}

func (p *TestIndexerClient) GetSyncHeight() int {
	return 10
}

// 通过indexer访问btc节点，效率比较低。最好改用上面的接口。
func (p *TestIndexerClient) GetBestHeight() int64 {
	return 10
}

func (p *TestIndexerClient) GetBlockHash(height int) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (p *TestIndexerClient) GetBlock(blockHash string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (p *TestIndexerClient) GetAssetSummaryWithAddress(address string) *indexer.AssetSummary {
	return nil
}

func (p *TestIndexerClient) GetUtxoListWithTicker(address string, ticker *swire.AssetName) []*indexer.TxOutputInfo {
	_mutex.RLock()
	defer _mutex.RUnlock()

	outputs := make([]*indexer.TxOutputInfo, 0)
	for i, utxo := range _utxos {
		assets := _utxoAssets[i]
		var assetInfo *swire.AssetInfo
		for _, asset := range assets {
			if asset.Name == *ticker {
				assetInfo = &asset
				break
			}
		}
		if assetInfo != nil {
			pkScript, _ := hex.DecodeString(_pkScripts[_utxoOwner[i]])
			outputs = append(outputs, &indexer.TxOutputInfo{
				OutPoint:  utxo,
				OutValue:  wire.TxOut{Value: _utxoValue[i], PkScript: pkScript},
				AssetInfo: []*indexer.AssetInfo{{Asset: *assetInfo}},
			})
		}
	}
	return outputs
}

func (p *TestIndexerClient) GetBlankUtxoList(address string) []*indexer.PlainUtxo {
	return nil
}

func (p *TestIndexerClient) GetUtxosWithAddress(address string) (map[string]*wire.TxOut, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *TestIndexerClient) GetAllUtxosWithAddress(address string) ([]*indexer.PlainUtxo, []*indexer.PlainUtxo, error) {
	return nil, nil, fmt.Errorf("not implemented")
}

// sat/vb
func (p *TestIndexerClient) GetFeeRate() int64 {
	return 1
}

func (p *TestIndexerClient) GetExistingUtxos(utxos []string) ([]string, error) {
	return utxos, nil
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

func (p *TestIndexerClient) BroadCastTx(tx *wire.MsgTx) (string, error) {
	str, err := EncodeMsgTx(tx)
	if err != nil {
		return "", err
	}
	Log.Infof("TX: %s\n%s", tx.TxID(), str)

	_mutex.Lock()
	defer _mutex.Unlock()

	_, ok := _txmap[tx.TxID()]
	if ok {
		return tx.TxID(), nil
	}
	_txmap[tx.TxID()] = true

	for i, txOut := range tx.TxOut {
		utxo := fmt.Sprintf("%s:%d", tx.TxID(), i)
		_utxos = append(_utxos, utxo)
		index := len(_utxos) - 1
		_utxoIndex[utxo] = index
		_utxoValue = append(_utxoValue, txOut.Value)
		_utxoAssets = append(_utxoAssets, nil) // 先占位
		_offsets = append(_offsets, nil)
		j := insertPkScript(txOut.PkScript)
		_utxoOwner = append(_utxoOwner, j)
	}

	return tx.TxID(), nil
}

func (p *TestIndexerClient) BroadCastTx_SatsNet(tx *swire.MsgTx) (string, error) {
	str, err := EncodeMsgTx_SatsNet(tx)
	if err != nil {
		return "", err
	}

	Log.Infof("TX: %s\n%s", tx.TxID(), str)

	_mutex.Lock()
	defer _mutex.Unlock()

	_, ok := _txmap[tx.TxID()]
	if ok {
		return tx.TxID(), nil
	}
	_txmap[tx.TxID()] = true

	for i, txOut := range tx.TxOut {
		utxo := fmt.Sprintf("%s:%d", tx.TxID(), i)
		_utxos = append(_utxos, utxo)
		index := len(_utxos) - 1
		_utxoIndex[utxo] = index
		_utxoValue = append(_utxoValue, txOut.Value)
		_utxoAssets = append(_utxoAssets, txOut.Assets)
		_offsets = append(_offsets, nil)
		j := insertPkScript(txOut.PkScript)
		_utxoOwner = append(_utxoOwner, j)
	}

	return tx.TxID(), nil
}

func (p *TestIndexerClient) GetTickInfo(assetName *AssetName) *indexer.TickerInfo {
	ticker, ok := _tickerInfo[assetName.String()]
	if !ok {
		return nil
	}

	return ticker
}
