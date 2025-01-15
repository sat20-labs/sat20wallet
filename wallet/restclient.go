package wallet

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/btcsuite/btcd/wire"
	swire "github.com/sat20-labs/satsnet_btcd/wire"
	"github.com/sat20-labs/sat20wallet/common"

	"github.com/sat20-labs/sat20wallet/wallet/indexer"
	"github.com/sat20-labs/sat20wallet/wallet/sindexer"
)

type RESTClient struct {
	Scheme string
	Host   string
	Proxy  string
	Http   common.HttpClient
}

func NewRESTClient(scheme, host string, http common.HttpClient) *RESTClient {
	net := "mainnet"
	if IsTestNet() {
		net = "testnet"
	}

	if scheme == "" {
		scheme = "https"
	}

	return &RESTClient{
		Scheme: scheme,
		Host:   host,
		Proxy:  net,
		Http:   http,
	}
}

func (p *RESTClient) GetUrl(path string) *common.URL {
	return &common.URL{
		Scheme: p.Scheme,
		Host:   p.Host,
		Path:   p.Proxy + path,
	}
}

type IndexerRPCClient interface {
	GetTxOutput(utxo string) (*TxOutput, error)
	GetAscendData(utxo string) (*sindexer.AscendData, error)
	GetUtxoId(utxo string) (uint64, error)
	GetRawTx(tx string) (string, error) 
	GetTxInfo(tx string) (*indexer.TxSimpleInfo, error)
	GetTxHeight(tx string) (int, error)
	IsTxConfirmed(tx string) bool
	GetSyncHeight() int
	GetBestHeight() int64 
	GetBlockHash(height int) (string, error)
	GetBlock(blockHash string) (string, error)
	GetAssetSummaryWithAddress(address string) *indexer.AssetSummary
	GetUtxoListWithTicker(address string, ticker *swire.AssetName) []*indexer.TxOutputInfo 
	GetBlankUtxoList(address string) []*indexer.PlainUtxo
	GetUtxosWithAddress(address string) (map[string]*wire.TxOut, error)
	GetAllUtxosWithAddress(address string) ([]*indexer.PlainUtxo, []*indexer.PlainUtxo, error)
	GetFeeRate() int64
	GetExistingUtxos(utxos []string) ([]string, error)
	BroadCastTx(tx *wire.MsgTx) (string, error)
	BroadCastTx_SatsNet(tx *swire.MsgTx) (string, error)
	GetTickInfo(assetName *AssetName) *indexer.TickerInfo
}

type IndexerClient struct {
	*RESTClient
}

func NewIndexerClient(scheme, host string, http common.HttpClient) *IndexerClient {
	client := NewRESTClient(scheme, host, http)
	return &IndexerClient{client}
}

func (p *IndexerClient) GetTxOutput(utxo string) (*TxOutput, error) {
	url := p.GetUrl("/v2/utxo/info/" + utxo)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil, err
	}

	Log.Infof("%v response: %s", url, string(rsp))

	// Unmarshal the response.
	var result indexer.TxOutputResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v", err)
		return nil, err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return nil, fmt.Errorf("%s", result.Msg)
	}

	var Assets swire.TxAssets
	Offsets := make(map[swire.AssetName]indexer.AssetOffsets)
	for _, info := range result.Data.AssetInfo {
		Assets = append(Assets, info.Asset)
		Offsets[info.Asset.Name] = info.Offsets
	}

	output := TxOutput{
		OutPointStr: result.Data.OutPoint,
		OutValue:    result.Data.OutValue,
		Assets:      Assets,
		Offsets:     Offsets,
	}

	return &output, nil
}

func (p *IndexerClient) GetAscendData(utxo string) (*sindexer.AscendData, error) {
	url := p.GetUrl("/v2/ascend/" + utxo)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil, err
	}

	Log.Infof("%v response: %s", url, string(rsp))

	// Unmarshal the response.
	var result sindexer.AscendResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v", err)
		return nil, err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return nil, fmt.Errorf("%s", result.Msg)
	}

	return result.Data, nil
}


// 只有未花费的能拿到id
func (p *IndexerClient) GetUtxoId(utxo string) (uint64, error) {
	url := p.GetUrl("/utxo/range/" + utxo)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return INVALID_ID, err
	}

	//Log.Infof("%v response: %s", url, string(rsp))

	// Unmarshal the response.
	var result indexer.UtxoInfoResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v", err)
		return INVALID_ID, err
	}

	if result.Code != 0 {
		//Log.Errorf("%v response message %s", url, result.Msg)
		return INVALID_ID, fmt.Errorf("%s", result.Msg)
	}

	if result.Data.Id == INVALID_ID {
		return INVALID_ID, fmt.Errorf("can't find utxo %s", utxo)
	}

	return result.Data.Id, nil
}

// btcutil.Tx
func (p *IndexerClient) GetRawTx(tx string) (string, error) {
	url := p.GetUrl("/btc/rawtx/" + tx)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return "", err
	}

	//Log.Infof("%v response: %s", url, string(rsp))

	// Unmarshal the response.
	var result indexer.TxResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v", err)
		return "", err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return "", fmt.Errorf("%s", result.Msg)
	}

	return result.Data.(string), nil
}

// btcutil.Tx
func (p *IndexerClient) GetTxInfo(tx string) (*indexer.TxSimpleInfo, error) {
	url := p.GetUrl("/btc/tx/simpleinfo/" + tx)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil, err
	}

	//Log.Infof("%v response: %s", url, string(rsp))

	// Unmarshal the response.
	var result indexer.TxSimpleInfoResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v", err)
		return nil, err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return nil, fmt.Errorf("%s", result.Msg)
	}

	return result.Data, nil
}

func (p *IndexerClient) GetTxHeight(tx string) (int, error) {
	info, err := p.GetTxInfo(tx)
	if err != nil {
		return -1, err
	}

	return int(info.BlockHeight), nil
}

func (p *IndexerClient) IsTxConfirmed(tx string) bool {
	info, err := p.GetTxInfo(tx)
	if err != nil {
		return false
	}

	return info.Confirmations > 0
}

func (p *IndexerClient) GetSyncHeight() int {
	url := p.GetUrl("/bestheight")
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return -1
	}

	// Log.Infof("%v response: %s", url, string(rsp))

	// Unmarshal the response.
	var result indexer.BestHeightResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v", err)
		return -1
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return -1
	}

	return result.Data["height"]
}

// 通过indexer访问btc节点，效率比较低。最好改用上面的接口。
func (p *IndexerClient) GetBestHeight() int64 {
	url := p.GetUrl("/btc/block/bestblockheight")
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return -1
	}

	// Log.Infof("%v response: %s", url, string(rsp))

	// Unmarshal the response.
	var result indexer.BestBlockHeightResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v", err)
		return -1
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return -1
	}

	return result.Data
}

func (p *IndexerClient) GetBlockHash(height int) (string, error) {
	url := p.GetUrl("/btc/block/blockhash/" + strconv.Itoa(height))
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return "", err
	}

	// Log.Infof("%v response: %s", url, string(rsp))

	// Unmarshal the response.
	var result indexer.BlockHashResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v", err)
		return "", err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return "", fmt.Errorf("%s", result.Msg)
	}

	return result.Data, nil
}

func (p *IndexerClient) GetBlock(blockHash string) (string, error) {
	url := p.GetUrl("/btc/block/" + blockHash)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return "", err
	}

	// Log.Infof("%v response: %s", url, string(rsp))

	// Unmarshal the response.
	var result indexer.RawBlockResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v", err)
		return "", err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return "", fmt.Errorf("%s", result.Msg)
	}

	return result.Data, nil
}

func (p *IndexerClient) GetAssetSummaryWithAddress(address string) *indexer.AssetSummary {
	url := p.GetUrl("/v2/address/summary/" + address)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil
	}

	Log.Infof("%v response: %s", url, string(rsp))

	// Unmarshal the response.
	var result indexer.AssetSummaryResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v", err)
		return nil
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return nil
	}

	return result.Data
}

func (p *IndexerClient) GetUtxoListWithTicker(address string, ticker *swire.AssetName) []*indexer.TxOutputInfo {
	url := p.GetUrl("/v2/address/asset/" + address + "/" + ticker.String())
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil
	}

	Log.Infof("%v response: %s", url, string(rsp))

	// Unmarshal the response.
	var result indexer.UtxosWithAssetResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v", err)
		return nil
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return nil
	}

	return result.Data
}

func (p *IndexerClient) GetBlankUtxoList(address string) []*indexer.PlainUtxo {
	url := p.GetUrl("/utxo/address/" + address + "/0")
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil
	}

	Log.Infof("%v response: %s", url, string(rsp))

	// Unmarshal the response.
	var result indexer.PlainUtxosResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v", err)
		return nil
	}

	if result.Code != 0 {
		Log.Errorf("GetBlankUtxoList response message %s", result.Msg)
		return nil
	}

	return result.Data
}

func (p *IndexerClient) GetUtxosWithAddress(address string) (map[string]*wire.TxOut, error) {
	pkScript, err := GetPkScriptFromAddress(address)
	if err != nil {
		return nil, err
	}

	result := make(map[string]*wire.TxOut)
	utxos1, utxos2, err := p.GetAllUtxosWithAddress(address)
	if err != nil {
		return nil, err
	}

	for _, utxo := range utxos1 {
		result[utxo.Txid+":"+strconv.Itoa(utxo.Vout)] = wire.NewTxOut(utxo.Value, pkScript)
	}
	for _, utxo := range utxos2 {
		result[utxo.Txid+":"+strconv.Itoa(utxo.Vout)] = wire.NewTxOut(utxo.Value, pkScript)
	}
	return result, nil
}

func (p *IndexerClient) GetAllUtxosWithAddress(address string) ([]*indexer.PlainUtxo, []*indexer.PlainUtxo, error) {
	url := p.GetUrl("/allutxos/address/" + address)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil, nil, err
	}

	Log.Infof("%v response: %s", url, string(rsp))

	// Unmarshal the response.
	var result indexer.AllUtxosResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v", err)
		return nil, nil, err
	}

	if result.Code != 0 {
		Log.Errorf("GetAllUtxosWithAddress response message %s", result.Msg)
		return nil, nil, fmt.Errorf("server failed")
	}

	return result.PlainUtxos, result.OtherUtxos, nil
}

// sat/vb
func (p *IndexerClient) GetFeeRate() int64 {
	url := p.GetUrl("/extension/default/fee-summary")
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return 0
	}

	Log.Infof("%v response: %s", url, string(rsp))

	// Unmarshal the response.
	var result indexer.FeeSummaryResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v", err)
		return 0
	}

	if result.Code != 0 {
		Log.Errorf("GetFeeRate response message %s", result.Msg)
		return 0
	}

	fr, err := strconv.ParseFloat(result.Data.List[0].FeeRate, 64)
	if err != nil {
		Log.Errorf("ParseFloat %s failed. %v", result.Data.List[0].FeeRate, err)
		return 0
	}
	ir := int64(fr)
	if ir == 0 {
		ir = 1
	}
	return ir
}

func (p *IndexerClient) GetExistingUtxos(utxos []string) ([]string, error) {
	req := indexer.UtxosReq{
		Utxos: utxos,
	}

	buff, err := json.Marshal(&req)
	if err != nil {
		return nil, err
	}

	url := p.GetUrl("/v2/utxos/existing")
	rsp, err := p.Http.SendPostRequest(url, buff)
	if err != nil {
		Log.Errorf("SendPostRequest %v failed. %v", url, err)
		return nil, err
	}

	var result indexer.ExistingUtxoResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v", err)
		return nil, err
	}

	if result.Code != 0 {
		Log.Errorf("GetExistingUtxos error message %s", result.Msg)
		return nil, fmt.Errorf("%s", result.Msg)
	}

	return result.ExistingUtxos, nil
}

func (p *IndexerClient) BroadCastTx(tx *wire.MsgTx) (string, error) {
	str, err := EncodeMsgTx(tx)
	if err != nil {
		return "", err
	}

	err = p.broadCastHexTx(str)
	if err != nil {
		Log.Errorf("BroadCastTxHex failed. %v", err)
		return "", err
	}

	return tx.TxID(), nil
}

func (p *IndexerClient) BroadCastTx_SatsNet(tx *swire.MsgTx) (string, error) {
	str, err := EncodeMsgTx_SatsNet(tx)
	if err != nil {
		return "", err
	}

	err = p.broadCastHexTx(str)
	if err != nil {
		Log.Errorf("BroadCastTxHex failed. %v", err)
		return "", err
	}

	return tx.TxID(), nil
}

func (p *IndexerClient) broadCastHexTx(hexTx string) error {
	req := indexer.SendRawTxReq{
		SignedTxHex: hexTx,
		Maxfeerate:  0,
	}

	buff, err := json.Marshal(&req)
	if err != nil {
		return err
	}

	url := p.GetUrl("/btc/tx")
	rsp, err := p.Http.SendPostRequest(url, buff)
	if err != nil {
		Log.Errorf("SendPostRequest %v failed. %v", url, err)
		return err
	}

	var result indexer.SendRawTxResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v", err)
		return err
	}

	if result.Code != 0 {
		if strings.Contains(result.Msg, "transaction already exists in blockchain") ||
			strings.Contains(result.Msg, "database contains entry for spent tx output") ||
			strings.Contains(result.Msg, "already have transaction in mempool") {
			Log.Infof("BroadCastTxHex TX has broadcasted. %s", result.Msg)
			return nil
		} else if strings.Contains(result.Msg, "the locked tx is anchored already in sats net") {
			// 聪网的特殊处理，只检查包含该utxo的anchorTx是否已经被广播，但不确定两个anchorTx是否一致。理论上应该一致。
			Log.Infof("BroadCastTxHex TX failed. %s", result.Msg)
			return nil
		}
		Log.Errorf("BroadCastTxHex error message %s", result.Msg)
		return fmt.Errorf("%s", result.Msg)
	}

	Log.Infof("BroadCastTxHex return %s", result.Data)
	return nil
}

func (p *IndexerClient) GetTickInfo(assetName *AssetName) *indexer.TickerInfo {
	url := p.GetUrl("/v2/tick/info/"+assetName.String())
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil
	}

	// Log.Infof("%v response: %s", url, string(rsp))

	// Unmarshal the response.
	var result indexer.TickerInfoResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v", err)
		return nil
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return nil
	}

	return result.Data
}
