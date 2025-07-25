package wallet

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"

	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	sindexerwire "github.com/sat20-labs/satoshinet/indexer/rpcserver/wire"
)


type RESTClient struct {
	Scheme string
	Host   string
	Proxy  string
	Http   HttpClient
}

func NewRESTClient(scheme, host, proxy string, http HttpClient) *RESTClient {
	net := "mainnet"
	if IsTestNet() {
		net = "testnet"
	}
	if proxy == "" {
		proxy = net
	}

	if scheme == "" {
		scheme = "https"
	}

	return &RESTClient{
		Scheme: scheme,
		Host:   host,
		Proxy:  proxy,
		Http:   http,
	}
}

func (p *RESTClient) GetUrl(path string) *URL {
	return &URL{
		Scheme: p.Scheme,
		Host:   p.Host,
		Path:   p.Proxy + path,
	}
}

type IndexerRPCClient interface {
	GetTxOutput(utxo string) (*TxOutput, error)  // 索引器接口，被花费后就找不到数据
	GetAscendData(utxo string) (*sindexer.AscendData, error)
	IsCoreNode(pubkey []byte) (bool, error)
	GetUtxoId(utxo string) (uint64, error)
	GetRawTx(tx string) (string, error)
	GetTxInfo(tx string) (*indexerwire.TxSimpleInfo, error)
	GetTxHeight(tx string) (int, error)
	IsTxConfirmed(tx string) bool
	GetSyncHeight() int
	GetBestHeight() int64
	GetBlockHash(height int) (string, error)
	GetBlock(blockHash string) (string, error)
	GetAssetSummaryWithAddress(address string) *indexerwire.AssetSummary
	GetUtxoListWithTicker(address string, ticker *swire.AssetName) []*indexerwire.TxOutputInfo
	GetPlainUtxoList(address string) []*indexerwire.PlainUtxo
	GetUtxosWithAddress(address string) (map[string]*wire.TxOut, error)
	GetAllUtxosWithAddress(address string) ([]*indexerwire.PlainUtxo, []*indexerwire.PlainUtxo, error)
	GetFeeRate() int64
	GetExistingUtxos(utxos []string) ([]string, error)
	TestRawTx(signedTxs []string) error
	BroadCastTx(tx *wire.MsgTx) (string, error)
	BroadCastTx_SatsNet(tx *swire.MsgTx) (string, error)
	GetTickInfo(assetName *swire.AssetName) *indexer.TickerInfo
	AllowDeployTick(assetName *swire.AssetName) error
	GetUtxoSpentTx(utxo string) (string, error) // TODO L1索引器需要支持这个api
	GetServiceIncoming(addr string) (int, int64, error)

	// for dkvs
	GetNonce(pubKey []byte) ([]byte, error)
	PutKVs(req *indexerwire.PutKValueReq) (error)
	DelKVs(req *indexerwire.DelKValueReq) (error)
	GetKV(pubkey []byte, key string) (*indexerwire.KeyValue, error)

	// for names
	GetNameInfo(string) (*indexerwire.OrdinalsName, error)
	GetNamesWithKey(string, string) ([]*indexerwire.OrdinalsName, error)
}


type IndexerClient struct {
	*RESTClient
}

func NewIndexerClient(scheme, host, proxy string, http HttpClient) *IndexerClient {
	client := NewRESTClient(scheme, host, proxy, http)
	return &IndexerClient{client}
}

func (p *IndexerClient) GetTxOutput(utxo string) (*TxOutput, error) {
	url := p.GetUrl("/v3/utxo/info/" + utxo)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil, err
	}

	//

	// Unmarshal the response.
	var result indexerwire.TxOutputRespV3
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return nil, err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return nil, fmt.Errorf("%s", result.Msg)
	}

	if result.Data == nil {
		return nil, fmt.Errorf("can't find utxo %s", utxo)
	}

	var Assets swire.TxAssets
	Offsets := make(map[swire.AssetName]indexer.AssetOffsets)
	for _, info := range result.Data.Assets {
		Assets.Add(info.ToAssetInfo())
		Offsets[info.AssetName] = info.Offsets
	}

	output := TxOutput{
		UtxoId:      result.Data.UtxoId,
		OutPointStr: result.Data.OutPoint,
		OutValue:    wire.TxOut{
			Value: result.Data.Value,
			PkScript: result.Data.PkScript,
		},
		Assets:      Assets,
		Offsets:     Offsets,
	}

	return &output, nil
}

func (p *IndexerClient) GetAscendData(utxo string) (*sindexer.AscendData, error) {
	url := p.GetUrl("/v3/ascend/" + utxo)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil, err
	}

	// Unmarshal the response.
	var result sindexerwire.AscendResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return nil, err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return nil, fmt.Errorf("%s", result.Msg)
	}

	return result.Data, nil
}

func (p *IndexerClient) IsCoreNode(pubkey []byte) (bool, error) {
	url := p.GetUrl("/v3/corenode/check/" + hex.EncodeToString(pubkey))
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return false, err
	}

	// Unmarshal the response.
	var result sindexerwire.CheckCoreNodeResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return false, err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return false, fmt.Errorf("%s", result.Msg)
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

	

	// Unmarshal the response.
	var result indexerwire.UtxoInfoResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
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

	

	// Unmarshal the response.
	var result indexerwire.TxResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return "", err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return "", fmt.Errorf("%s", result.Msg)
	}

	return result.Data.(string), nil
}

// btcutil.Tx
func (p *IndexerClient) GetTxInfo(tx string) (*indexerwire.TxSimpleInfo, error) {
	url := p.GetUrl("/btc/tx/simpleinfo/" + tx)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil, err
	}

	

	// Unmarshal the response.
	var result indexerwire.TxSimpleInfoResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
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

// 这个接口返回tx已经确认时，索引器可能还没有更新，所以最好是继续查询utxo是否已经生成
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

	

	// Unmarshal the response.
	var result indexerwire.BestHeightResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
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

	

	// Unmarshal the response.
	var result indexerwire.BestBlockHeightResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
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

	

	// Unmarshal the response.
	var result indexerwire.BlockHashResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
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

	

	// Unmarshal the response.
	var result indexerwire.RawBlockResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return "", err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return "", fmt.Errorf("%s", result.Msg)
	}

	return result.Data, nil
}

func (p *IndexerClient) GetAssetSummaryWithAddress(address string) *indexerwire.AssetSummary {
	url := p.GetUrl("/v3/address/summary/" + address)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil
	}

	

	// Unmarshal the response.
	var result indexerwire.AssetSummaryRespV3
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return nil
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return nil
	}

	assets := make([]*indexer.AssetInfo, 0)
	for _, info := range result.Data {
		assets = append(assets, info.ToAssetInfo())
	}

	return &indexerwire.AssetSummary{
		ListResp: indexerwire.ListResp{
			Start: 0,
			Total: uint64(len(result.Data)),
		},
		Data: assets,
	}
}

func (p *IndexerClient) GetUtxoListWithTicker(address string, ticker *swire.AssetName) []*indexerwire.TxOutputInfo {
	url := p.GetUrl("/v3/address/asset/" + address + "/" + ticker.String())
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil
	}

	

	// Unmarshal the response.
	var result indexerwire.UtxosWithAssetRespV3
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return nil
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return nil
	}

	return result.Data
}

func (p *IndexerClient) GetPlainUtxoList(address string) []*indexerwire.PlainUtxo {
	url := p.GetUrl("/utxo/address/" + address + "/0")
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil
	}

	

	// Unmarshal the response.
	var result indexerwire.PlainUtxosResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return nil
	}

	if result.Code != 0 {
		Log.Errorf("GetPlainUtxoList response message %s", result.Msg)
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

func (p *IndexerClient) GetAllUtxosWithAddress(address string) ([]*indexerwire.PlainUtxo, []*indexerwire.PlainUtxo, error) {
	url := p.GetUrl("/allutxos/address/" + address)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil, nil, err
	}

	

	// Unmarshal the response.
	var result indexerwire.AllUtxosResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return nil, nil, err
	}

	if result.Code != 0 {
		Log.Errorf("GetAllUtxosWithAddress response message %s", result.Msg)
		return nil, nil, fmt.Errorf("%s", result.Msg)
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

	

	// Unmarshal the response.
	var result indexerwire.FeeSummaryResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
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
	req := indexerwire.UtxosReq{
		Utxos: utxos,
	}

	buff, err := json.Marshal(&req)
	if err != nil {
		return nil, err
	}

	url := p.GetUrl("/v3/utxos/existing")
	rsp, err := p.Http.SendPostRequest(url, buff)
	if err != nil {
		Log.Errorf("SendPostRequest %v failed. %v", url, err)
		return nil, err
	}
	

	var result indexerwire.ExistingUtxoResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return nil, err
	}

	if result.Code != 0 {
		Log.Errorf("GetExistingUtxos error message %s", result.Msg)
		return nil, fmt.Errorf("%s", result.Msg)
	}

	return result.ExistingUtxos, nil
}

func (p *IndexerClient) TestRawTx(signedTxs []string) error {
	req := indexerwire.TestRawTxReq{
		SignedTxs: signedTxs,
	}

	buff, err := json.Marshal(&req)
	if err != nil {
		return err
	}

	url := p.GetUrl("/btc/tx/test")
	rsp, err := p.Http.SendPostRequest(url, buff)
	if err != nil {
		Log.Errorf("SendPostRequest %v failed. %v", url, err)
		return err
	}
	

	var result indexerwire.TestRawTxResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return err
	}
	//Log.Infof("TestRawTx return %d", len(result.Data))

	for i, r := range result.Data {
		Log.Infof("TestRawTx %d: %v", i, r)
		if !r.Allowed {
			if strings.Contains(r.RejectReason, "transaction already exists in blockchain") ||
				strings.Contains(r.RejectReason, "database contains entry for spent tx output") ||
				strings.Contains(r.RejectReason, "already have transaction in mempool") {
				Log.Infof("BroadCastTxHex TX has broadcasted. %s", r.RejectReason)
				continue
			} else if strings.Contains(r.RejectReason, "the locked tx is anchored already in sats net") {
				// 只有聪网交易才会走到这里
				tx, _ := DecodeMsgTx_SatsNet(signedTxs[i])
				if strings.Contains(r.RejectReason, tx.TxID()) {
					// 聪网的特殊处理，只检查包含该utxo的anchorTx是否已经被广播
					Log.Infof("BroadCastTxHex TX has anchored. %s", r.RejectReason)
					continue
				} else {
					Log.Errorf("%v", r.RejectReason)
					return fmt.Errorf("%d:%s", i, r.RejectReason)
				}
				
			}
			Log.Errorf("this raw tx %s is not accepted by mempool, %s", signedTxs[i], r.RejectReason)
			return fmt.Errorf("%d:%s", i, r.RejectReason)
		} else {
			//Log.Debugf("this raw tx %s is accepted by mempool", signedTxs[i])
		}
	}
	
	return nil
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
	req := indexerwire.SendRawTxReq{
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
	

	var result indexerwire.SendRawTxResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return err
	}

	if result.Code != 0 {
		if strings.Contains(result.Msg, "transaction already exists in blockchain") ||
			strings.Contains(result.Msg, "database contains entry for spent tx output") ||
			strings.Contains(result.Msg, "already have transaction in mempool") ||
			strings.Contains(result.Msg, "Transaction outputs already in utxo set") {
			Log.Infof("BroadCastTxHex TX has broadcasted. %s", result.Msg)
			return nil
		} else if strings.Contains(result.Msg, "the locked tx is anchored already in sats net") {
			// 聪网的特殊处理，只检查包含该utxo的anchorTx是否已经被广播，
			tx, _ := DecodeMsgTx_SatsNet(hexTx)
			if strings.Contains(result.Msg, tx.TxID()) {
				// 聪网的特殊处理，只检查包含该utxo的anchorTx是否已经被广播
				Log.Infof("BroadCastTxHex TX has anchored. %s", result.Msg)
				return nil
			} else {
				Log.Errorf("BroadCastTxHex failed, %v", result.Msg)
				return fmt.Errorf("%s", result.Msg)
			}
		}
		Log.Errorf("BroadCastTxHex error message %s", result.Msg)
		return fmt.Errorf("%s", result.Msg)
	}

	Log.Infof("BroadCastTxHex return %s", result.Data)
	return nil
}

func (p *IndexerClient) GetTickInfo(assetName *swire.AssetName) *indexer.TickerInfo {
	url := p.GetUrl("/v3/tick/info/" + assetName.String())
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil
	}

	

	// Unmarshal the response.
	var result indexerwire.TickerInfoResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return nil
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return nil
	}

	return result.Data
}

func (p *IndexerClient) AllowDeployTick(assetName *swire.AssetName) error {
	url := p.GetUrl("/deploy/" + assetName.String())
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil
	}

	// Unmarshal the response.
	var result indexerwire.BaseResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return nil
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return fmt.Errorf(result.Msg)
	}

	return nil
}

func (p *IndexerClient) GetUtxoSpentTx(utxo string) (string, error) {
	// for test
	return "", fmt.Errorf("not implemented")
}

func (p *IndexerClient) GetServiceIncoming(addr string) (int, int64, error) {
	url := p.GetUrl(fmt.Sprintf("/incoming/%s", addr))
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return 0, 0, err
	}

	// Unmarshal the response.
	var result indexerwire.BaseResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return 0, 0, err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return 0, 0, fmt.Errorf(result.Msg)
	}

	return 0, 0, nil
}

func (p *IndexerClient) GetNonce(pubKey []byte) ([]byte, error) {
	req := indexerwire.GetNonceReq{
		PubKey: pubKey,
	}

	buff, err := json.Marshal(&req)
	if err != nil {
		return nil, err
	}

	url := p.GetUrl("/kv/nonce")
	rsp, err := p.Http.SendPostRequest(url, buff)
	if err != nil {
		Log.Warningf("SendPostRequest %v failed. %v", url, err)
		return nil, err
	}
	

	var result indexerwire.GetNonceResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return nil, err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return nil, fmt.Errorf("%s", result.Msg)
	}

	return result.Nonce, nil
}

func (p *IndexerClient) PutKVs(req *indexerwire.PutKValueReq) (error) {
	
	buff, err := json.Marshal(&req)
	if err != nil {
		return err
	}

	url := p.GetUrl("/kv/put")
	rsp, err := p.Http.SendPostRequest(url, buff)
	if err != nil {
		Log.Warningf("SendPostRequest %v failed. %v", url, err)
		return err
	}
	

	var result indexerwire.PutKValueResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return fmt.Errorf("%s", result.Msg)
	}

	return nil
}

func (p *IndexerClient) DelKVs(req *indexerwire.DelKValueReq) (error) {
	buff, err := json.Marshal(&req)
	if err != nil {
		return err
	}

	url := p.GetUrl("/kv/del")
	rsp, err := p.Http.SendPostRequest(url, buff)
	if err != nil {
		Log.Warningf("SendPostRequest %v failed. %v", url, err)
		return err
	}
	

	var result indexerwire.DelKValueResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return fmt.Errorf("%s", result.Msg)
	}

	return nil
}

// 绑定在公钥的kv，只有一些支付费用的节点才能上传kv到indexer
func (p *IndexerClient) GetKV(pubkey []byte, key string) (*indexerwire.KeyValue, error) {

	path := fmt.Sprintf("/kv/get/%s/%s", hex.EncodeToString(pubkey), key)
	url := p.GetUrl(path)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil, err
	}

	
	var result indexerwire.GetValueResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return nil, err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return nil, fmt.Errorf("%s", result.Msg)
	}

	return result.Value, nil
}

// 绑定在name的kv，在主网无许可自主绑定任何kv
func (p *IndexerClient) GetKVInName(name, key string) (*indexerwire.OrdinalsName, error) {

	path := fmt.Sprintf("/ns/name/%s?key=%s", name, key)
	url := p.GetUrl(path)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil, err
	}

	// 只传回该keyvalue的值，其他过滤掉
	var result indexerwire.NamePropertiesResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return nil, err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return nil, fmt.Errorf("%s", result.Msg)
	}

	return result.Data, nil
}

// 传回该name绑定的所有kv
func (p *IndexerClient) GetNameInfo(name string) (*indexerwire.OrdinalsName, error) {

	path := fmt.Sprintf("/ns/name/%s", name)
	url := p.GetUrl(path)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil, err
	}

	
	var result indexerwire.NamePropertiesResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return nil, err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return nil, fmt.Errorf("%s", result.Msg)
	}

	return result.Data, nil
}

func (p *IndexerClient) GetNamesWithKey(address, key string) ([]*indexerwire.OrdinalsName, error) {

	path := fmt.Sprintf("/ns/address/%s", address)
	url := p.GetUrl(path)
	url.Query = make(map[string]string)  
	url.Query["key"] = key
	
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil, err
	}

	var result indexerwire.NamesWithAddressResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return nil, err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return nil, fmt.Errorf("%s", result.Msg)
	}

	return result.Data.Names, nil
}
