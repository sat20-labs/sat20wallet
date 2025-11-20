package wallet

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	swire "github.com/sat20-labs/satoshinet/wire"
	"lukechampine.com/uint128"

	db "github.com/sat20-labs/indexer/common"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/runes/runestone"
)

// //////
// only use in testing
var _enable_testing bool = false
var _not_send_tx = false
var _not_invoke_block = false

////////

type Node struct {
	client   NodeRPCClient
	Host     string // ip:port
	NodeType string
	NodeId   *secp256k1.PublicKey // same as Pubkey
	Pubkey   *secp256k1.PublicKey
}

type NotifyCB func(string, interface{})

// 密码只有一个，助记词可以有多组，对应不同的wallet
type Manager struct {
	mutex sync.RWMutex

	cfg           *common.Config
	bInited       bool
	bStop         bool
	status        *Status
	walletInfoMap map[int64]*WalletInDB
	wallet        *InternalWallet
	msgCallback   NotifyCB
	tickerInfoMap map[string]*indexer.TickerInfo // 缓存数据, key: AssetName.String()

	db                   db.KVDB
	http                 HttpClient
	l1IndexerClient      IndexerRPCClient
	slaveL1IndexerClient IndexerRPCClient
	l2IndexerClient      IndexerRPCClient
	slaveL2IndexerClient IndexerRPCClient

	bootstrapNode []*Node // 引导节点，全网目前唯一，以后由基金会提供至少3个，通过MPC管理密钥
	serverNode    *Node   // 服务节点，由引导节点更新维护，一般情况下，用户只跟一个服务节点打交道

	utxoLockerL1 *UtxoLocker
	utxoLockerL2 *UtxoLocker

	feeRateL1     int64 // sat/vkb
	refreshTimeL1 int64
	feeRateL2     int64 // sat/vkb
	refreshTimeL2 int64

	inscibeMap map[int64]*InscribeResv // key: timestamp
}

func (p *Manager) init() error {
	if p.bInited {
		return nil
	}

	p.initResvMap()
	err := p.initNode()
	if err != nil {
		Log.Errorf("initNode failed. %v", err)
		return err
	}

	err = p.initDB()
	if err != nil {
		Log.Errorf("initDB failed. %v", err)
		return err
	}

	p.bInited = true

	return nil
}

func (p *Manager) SetIndexerHttpClient(client IndexerRPCClient) {
	p.l1IndexerClient = client
	p.utxoLockerL1.rpcClient = client
}

func (p *Manager) SetIndexerHttpClient_SatsNet(client IndexerRPCClient) {
	p.l2IndexerClient = client
	p.utxoLockerL2.rpcClient = client
}

func (p *Manager) SetServerNodeHttpClient(client NodeRPCClient) {
	p.serverNode.client = client
}

func GetBootstrapPubKey() *secp256k1.PublicKey {
	keyBytes, err := hex.DecodeString(indexer.GetBootstrapPubKey())
	if err != nil {
		Log.Panic("GetBootstrapPubKey failed")
	}
	r, err := utils.BytesToPublicKey(keyBytes)
	if err != nil {
		Log.Panic("BytesToPublicKey failed")
	}
	return r
}

// 初始化服务节点
func (p *Manager) initNode() error {
	// 第一个node，是bootstrap，第二个是服务节点。如果有两个，nodeId不能相同
	nodes := p.cfg.Peers
	bootstrappubkey := GetBootstrapPubKey()
	for _, n := range nodes {
		parts := strings.Split(n, "@")
		if len(parts) != 3 {
			Log.Errorf("invalid peers config item: %s", n)
			continue
		}
		parsedPubkey, err := utils.ParsePubkey(parts[1])
		if err != nil {
			return fmt.Errorf("invalid AddPeers config item: %s", n)
		}

		var scheme, host, proxy string
		// http://host[:port]/stp/testnet
		if strings.HasPrefix(parts[2], "http://") {
			scheme = "http"
			host = strings.TrimPrefix(parts[2], "http://")
		} else if strings.HasPrefix(parts[2], "https://") {
			scheme = "https"
			host = strings.TrimPrefix(parts[2], "https://")
		} else {
			scheme = "http"
			host = parts[2]
		}
		if strings.HasSuffix(host, "/stp/testnet") {
			host = strings.TrimSuffix(host, "/stp/testnet")
			proxy = "stp/testnet"
		} else if strings.HasSuffix(host, "/stp/mainnet") {
			host = strings.TrimSuffix(host, "/stp/mainnet")
			proxy = "stp/mainnet"
		} else {
			h, p, bfound := strings.Cut(host, "/")
			host = h
			if bfound {
				proxy = p
			}
		}

		switch parts[0] {
		case "b":
			if bytes.Equal(parsedPubkey.SerializeCompressed(), bootstrappubkey.SerializeCompressed()) {
				node := &Node{
					client:   NewNodeClient(scheme, host, proxy, p.http),
					NodeId:   bootstrappubkey,
					Host:     host,
					NodeType: BOOTSTRAP_NODE,
					Pubkey:   bootstrappubkey,
				}
				p.bootstrapNode = append(p.bootstrapNode, node)
			} else {
				return fmt.Errorf("invalid bootstrap pubkey")
			}

		case "s":
			if p.serverNode != nil {
				return fmt.Errorf("too many server node setting")
			}
			p.serverNode = &Node{
				client:   NewNodeClient(scheme, host, proxy, p.http),
				NodeId:   parsedPubkey,
				Host:     host,
				NodeType: SERVER_NODE,
				Pubkey:   parsedPubkey,
			}
		default:
			Log.Errorf("not support type %s", n)
		}
	}

	if len(p.bootstrapNode) == 0 {
		Log.Warnf("no bootstrap node setting, use default setting")
		host := "apiprd.sat20.org"
		var proxy string
		if IsTestNet() {
			proxy = "/stp/testnet"
		} else {
			proxy = "/stp/mainnet"
		}

		p.bootstrapNode = []*Node{{
			client:   NewNodeClient("https", host, proxy, p.http),
			NodeId:   bootstrappubkey,
			Host:     host,
			NodeType: BOOTSTRAP_NODE,
			Pubkey:   bootstrappubkey,
		}}
	}

	if p.serverNode == nil {
		p.serverNode = p.bootstrapNode[0]
	}

	Log.Infof("server node id: %s", hex.EncodeToString(p.serverNode.NodeId.SerializeCompressed()))

	return nil
}
func (p *Manager) Close() {
	p.bInited = false
}

func (p *Manager) checkSelf() error {

	return nil
}

func (p *Manager) dbStatistic() bool {

	return false
}

func (p *Manager) GetWallet() common.Wallet {
	return p.wallet
}

func IsTestNet() bool {
	return _chain != "mainnet"
}

func (p *Manager) checkSuperNodeStatus() bool {
	err := p.serverNode.client.SendActionResultNfty(0, "", 0, "")
	return err == nil
}

func (p *Manager) initResvMap() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.inscibeMap = make(map[int64]*InscribeResv)
}

func (p *Manager) GenerateNewResvId() int64 {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	id := time.Now().UnixMicro()
	for {
		_, ok := p.inscibeMap[id]
		if ok {
			id++
			continue
		}
		break
	}
	return id
}

func (p *Manager) GetInscribeResv(id int64) *InscribeResv {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	r, ok := p.inscibeMap[id]
	if ok {
		return r
	}
	return nil
}

func (p *Manager) GetFeeRate() int64 {
	if IsTestNet() {
		return 1
	}
	now := time.Now().Unix()
	if now-p.refreshTimeL1 > 3*60 {
		fr := p.l1IndexerClient.GetFeeRate()
		if fr == 0 {
			fr = 10
		} else {
			p.refreshTimeL1 = now
		}
		p.feeRateL1 = fr
	}
	return p.feeRateL1
}

// L2 费率，计算出来的fee再除以10
func (p *Manager) GetFeeRate_SatsNet() int64 {
	now := time.Now().Unix()
	if now-p.refreshTimeL2 > 60 {
		fr := p.l2IndexerClient.GetFeeRate()
		if fr == 0 {
			fr = 1
		} else {
			p.refreshTimeL2 = now
		}
		p.feeRateL2 = fr
	}
	return p.feeRateL2
}

func (p *Manager) getRuneIdFromName(name *swire.AssetName) (*runestone.RuneId, error) {

	if name.Protocol != indexer.PROTOCOL_NAME_RUNES {
		return nil, fmt.Errorf("not runes")
	}
	tickerInfo := p.getTickerInfo(name)
	if tickerInfo == nil {
		return nil, fmt.Errorf("not found ticker %s", name.String())
	}

	if strings.Contains(tickerInfo.DisplayName, ":") {
		return runestone.RuneIdFromString(tickerInfo.DisplayName)
	}

	return runestone.RuneIdFromString(tickerInfo.AssetName.Ticker)
}

func (p *Manager) getTickerInfo(name *swire.AssetName) *indexer.TickerInfo {

	if name.String() == ASSET_PLAIN_SAT.String() ||
		name.String() == db.ASSET_ALL_SAT.String() {
		return &indexer.TickerInfo{
			AssetName:    *name,
			MaxSupply:    "21000000000000000", //  sats
			Divisibility: 0,
			N:            1,
		}
	}

	p.mutex.RLock()
	info, ok := p.tickerInfoMap[name.String()]
	p.mutex.RUnlock()
	if ok {
		return info
	}

	// TODO 还在铸造中的ticker，需要每个区块更新一次数据
	//info, err := loadTickerInfo(p.db, name)
	//if err != nil {
	info = p.l1IndexerClient.GetTickInfo(name)
	if info == nil {
		Log.Errorf("GetTickInfo %s failed", name)
		return nil
	}
	saveTickerInfo(p.db, info)
	//}

	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.tickerInfoMap[info.AssetName.String()] = info
	if name.Protocol == indexer.PROTOCOL_NAME_RUNES {
		p.tickerInfoMap[info.DisplayName] = info
	}
	return info
}

func (p *Manager) GetTickerInfoFromRuneId(runeId string) *indexer.TickerInfo {
	p.mutex.RLock()
	info, ok := p.tickerInfoMap[runeId]
	p.mutex.RUnlock()
	if ok {
		return info
	}

	return p.getTickerInfo(&indexer.AssetName{
		Protocol: indexer.PROTOCOL_NAME_RUNES,
		Type:     indexer.ASSET_TYPE_FT,
		Ticker:   runeId,
	})
}

func (p *Manager) RemoveAllTickerInfo() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.tickerInfoMap = make(map[string]*indexer.TickerInfo)
	deleteAllTickerInfoFromDB(p.db)
}

// 一聪最多绑定的资产数量
func (p *Manager) getBindingSat(name *swire.AssetName) uint32 {
	info := p.getTickerInfo(name)
	if info == nil {
		return 0
	}
	return uint32(info.N)
}

// amt的资产需要多少聪
func (p *Manager) getBindingSatNum(name *swire.AssetName, amt *Decimal) int64 {
	return indexer.GetBindingSatNum(amt, (p.getBindingSat(name)))
}

func newAssetInfo(name *AssetName, amt *Decimal) *swire.AssetInfo {
	return &swire.AssetInfo{
		Name:       name.AssetName,
		Amount:     *amt.Clone(),
		BindingSat: uint32(name.N),
	}
}

func (p *Manager) GetMaxSupplyWithRune(name *swire.AssetName) (uint128.Uint128, int, error) {
	info := p.getTickerInfo(name)
	if info == nil {
		return uint128.Uint128{}, 0, fmt.Errorf("can't find ticker %s", name)
	}
	maxSupply, err := uint128.FromString(info.MaxSupply)
	if err != nil {
		return uint128.Uint128{}, 0, err
	}
	return maxSupply, info.Divisibility, nil
}

func (p *Manager) GetTxOutFromRawTx(utxo string) (*TxOutput, error) {
	parts := strings.Split(utxo, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid utxo %s", utxo)
	}
	vout, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, err
	}
	txHex, err := p.l1IndexerClient.GetRawTx(parts[0])
	if err != nil {
		Log.Errorf("GetRawTx %s failed, %v", parts[0], err)
		return nil, err
	}
	tx, err := DecodeMsgTx(txHex)
	if err != nil {
		return nil, err
	}
	if vout >= len(tx.TxOut) {
		return nil, fmt.Errorf("invalid index of utxo %s", utxo)
	}
	return &TxOutput{
		OutPointStr: utxo,
		OutValue: wire.TxOut{
			Value:    tx.TxOut[vout].Value,
			PkScript: tx.TxOut[vout].PkScript,
		},
	}, nil
}

func (p *Manager) GetUtxosForFee(address string, value int64,
	excludedUtxoMap map[string]bool, excludeRecentBlock bool) ([]string, error) {

	if address == "" {
		address = p.wallet.GetAddress()
	}

	outputs, err := p.SelectUtxosForFeeV2(address, excludedUtxoMap, value, excludeRecentBlock, false)
	if err != nil {
		return nil, err
	}

	result := make([]string, len(outputs))
	for i, output := range outputs {
		result[i] = output.OutPointStr
	}
	return result, nil
}

func (p *Manager) GetUtxosForStubs(address string, n int, excludedUtxoMap map[string]bool) ([]string, error) {
	if address == "" {
		address = p.wallet.GetAddress()
	}

	utxos := p.l1IndexerClient.GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)
	p.utxoLockerL1.Reload(address)

	// 有序的utxo列表，直接放最后一个
	result := make([]string, 0)
	i := len(utxos) - 1

	for i >= 0 {
		u := utxos[i]
		i--
		utxo := u.OutPoint
		if _, ok := excludedUtxoMap[utxo]; ok {
			continue
		}
		if p.utxoLockerL1.IsLocked(utxo) {
			continue
		}
		if u.Value > 330 {
			continue
		}
		result = append(result, utxo)
		if len(result) == n {
			break
		}
	}

	if len(result) < n {
		return result, fmt.Errorf("need %d utxos for fee, but only %d found", n, len(result))
	}

	return result, nil
}

func (p *Manager) GetUtxosForFee_SatsNet(address string, value int64,
	excludedUtxoMap map[string]bool) ([]string, error) {

	return p.SelectUtxosForFeeV2_SatsNet(address, excludedUtxoMap, value)
}

func (p *Manager) GetUtxosWithAsset(address string, amt *Decimal, assetName *swire.AssetName,
	excludedUtxoMap map[string]bool) ([]string, error) {
	return p.SelectUtxosForAssetV2(address, excludedUtxoMap, assetName, amt, false)
}

func (p *Manager) GetUtxosWithAsset_SatsNet(address string, amt *Decimal,
	assetName *swire.AssetName, excludedUtxoMap map[string]bool) ([]string, error) {
	return p.SelectUtxosForAssetV2_SatsNet(address, excludedUtxoMap, assetName, amt)
}

func (p *Manager) GetUtxosWithAssetV2(address string, plainSats int64,
	amt *Decimal, assetName *swire.AssetName,
	excludedUtxoMap map[string]bool, excludeRecentBlock bool) ([]string, []string, error) {

	resultAssets, err := p.SelectUtxosForAssetV2(address, excludedUtxoMap, assetName, amt, excludeRecentBlock)
	if err != nil {
		return nil, nil, err
	}

	outputs, err := p.SelectUtxosForFeeV2(address, excludedUtxoMap, plainSats, excludeRecentBlock, false)
	if err != nil {
		return nil, nil, err
	}
	resultPlains := make([]string, len(outputs))
	for i, output := range outputs {
		resultPlains[i] = output.OutPointStr
	}

	return resultAssets, resultPlains, nil
}

// 这里白聪，是指额外的白聪，不包含绑定了指定资产的聪
func (p *Manager) GetUtxosWithAssetV2_SatsNet(address string, plainSats int64,
	amt *Decimal, assetName *swire.AssetName, excludedUtxoMap map[string]bool) ([]string, []string, error) {

	if address == "" {
		address = p.wallet.GetAddress()
	}
	utxos := p.l2IndexerClient.GetUtxoListWithTicker(address, assetName)

	expectedAssetAmt := amt.Clone()
	if indexer.IsPlainAsset(assetName) {
		expectedAssetAmt = expectedAssetAmt.Add(indexer.NewDefaultDecimal(plainSats))
	}

	resultAssets := make([]string, 0)
	resultPlains := make([]string, 0)
	var totalAssets *Decimal
	var totalPlainSats int64
	p.utxoLockerL2.Reload(address)
	for _, u := range utxos {
		if _, ok := excludedUtxoMap[u.OutPoint]; ok {
			continue
		}
		if p.utxoLockerL2.IsLocked(u.OutPoint) {
			continue
		}
		output := OutputInfoToOutput_SatsNet(u)
		totalPlainSats += output.GetPlainSat()
		num := output.GetAsset(assetName)
		totalAssets = totalAssets.Add(num)

		resultAssets = append(resultAssets, output.OutPointStr)
		if totalAssets.Cmp(expectedAssetAmt) >= 0 && totalPlainSats >= plainSats {
			break
		}
	}
	if totalAssets.Cmp(expectedAssetAmt) < 0 {
		return nil, nil, fmt.Errorf("no enough utxo for %s, require %s but only %s", 
			assetName.String(), expectedAssetAmt.String(), totalAssets.String())
	}

	if totalPlainSats < plainSats && !indexer.IsPlainAsset(assetName) {
		// 所有包含了白聪的utxo都在这里，即使utxo中有其他资产
		utxos := p.l2IndexerClient.GetUtxoListWithTicker(address, &ASSET_PLAIN_SAT)
		p.utxoLockerL2.Reload(address)
		for _, u := range utxos {
			if _, ok := excludedUtxoMap[u.OutPoint]; ok {
				continue
			}
			if p.utxoLockerL2.IsLocked(u.OutPoint) {
				continue
			}
			output := OutputInfoToOutput_SatsNet(u)
			num := output.GetAsset(assetName)
			if num.Sign() > 0 {
				// 在上面已经包含
				continue
			}
			totalPlainSats += output.GetPlainSat()
			resultPlains = append(resultPlains, output.OutPointStr)
			if totalPlainSats >= plainSats {
				break
			}
		}
	}

	if totalPlainSats < plainSats {
		return nil, nil, fmt.Errorf("no enough utxo for plain sats, require %d but only %d, ", plainSats, totalPlainSats)
	}

	return resultAssets, resultPlains, nil
}

// available, locked
func (p *Manager) GetAssetAmount(address string, name *swire.AssetName,
	excludedUtxoMap map[string]bool) (*Decimal, *Decimal) {
	if address == "" {
		address = p.wallet.GetAddress()
	}
	bPlainAsset := indexer.IsPlainAsset(name)

	var availableSats, lockedSats int64
	var available, locked *Decimal
	outputs := p.l1IndexerClient.GetUtxoListWithTicker(address, name)

	p.utxoLockerL1.Reload(address)
	for _, u := range outputs {
		_, ok := excludedUtxoMap[u.OutPoint]
		if ok || p.utxoLockerL1.IsLocked(u.OutPoint) {
			if bPlainAsset {
				lockedSats += u.Value
			} else {
				assets := u.ToTxAssets()
				asset, _ := assets.Find(name)
				if asset != nil {
					if locked == nil {
						locked = &asset.Amount
					} else {
						locked = locked.Add(&asset.Amount)
					}
				}
			}
		} else {
			if bPlainAsset {
				availableSats += u.Value
			} else {
				assets := u.ToTxAssets()
				asset, _ := assets.Find(name)
				if asset != nil {
					if available == nil {
						available = &asset.Amount
					} else {
						available = available.Add(&asset.Amount)
					}
				}
			}
		}
	}

	if bPlainAsset {
		return indexer.NewDefaultDecimal(availableSats), indexer.NewDefaultDecimal(lockedSats)
	}

	return available, locked
}

func (p *Manager) GetAssetAmount_SatsNet(address string, name *swire.AssetName,
	excludedUtxoMap map[string]bool) (*Decimal, *Decimal) {
	if address == "" {
		address = p.wallet.GetAddress()
	}
	bPlainAsset := indexer.IsPlainAsset(name)

	var availableSats, lockedSats int64
	var available, locked *Decimal
	outputs := p.l2IndexerClient.GetUtxoListWithTicker(address, name)

	p.utxoLockerL2.Reload(address)
	for _, u := range outputs {
		output := OutputInfoToOutput_SatsNet(u)
		_, ok := excludedUtxoMap[u.OutPoint]
		if ok || p.utxoLockerL2.IsLocked(u.OutPoint) {
			if bPlainAsset {
				lockedSats += output.GetPlainSat()
			} else {
				assets := u.ToTxAssets()
				asset, _ := assets.Find(name)
				if asset != nil {
					locked = locked.Add(&asset.Amount)
				}
			}
		} else {
			if bPlainAsset {
				availableSats += output.GetPlainSat()
			} else {
				assets := u.ToTxAssets()
				asset, _ := assets.Find(name)
				if asset != nil {
					available = available.Add(&asset.Amount)
				}
			}
		}
	}

	if bPlainAsset {
		return indexer.NewDefaultDecimal(availableSats), indexer.NewDefaultDecimal(lockedSats)
	}

	return available, locked
}

func (p *Manager) GetAssetBalance(address string, name *swire.AssetName) *Decimal {
	if address == "" {
		address = p.wallet.GetAddress()
	}

	assets := p.l1IndexerClient.GetAssetSummaryWithAddress(address)
	if assets != nil {
		for _, u := range assets.Data {
			if u.Name == *name {
				return &u.Amount
			}
		}
	}

	return nil
}

func (p *Manager) GetAssetBalance_SatsNet(address string, name *swire.AssetName) *Decimal {
	if address == "" {
		address = p.wallet.GetAddress()
	}

	assets := p.l2IndexerClient.GetAssetSummaryWithAddress(address)
	if assets != nil {
		for _, u := range assets.Data {
			if u.Name == *name {
				return &u.Amount
			}
		}
	}

	return nil
}

func (p *Manager) BroadcastTx(tx *wire.MsgTx) (string, error) {
	if _enable_testing && _not_send_tx {
		return tx.TxID(), nil
	}

	txId, err := p.l1IndexerClient.BroadCastTx(tx)
	if err != nil {
		Log.Errorf("BroadCastTx %s failed. %v", tx.TxID(), err)
		return "", err
	}
	p.utxoLockerL1.LockUtxosWithTx(tx)
	// tx确认后自动解锁
	return txId, nil
}

func (p *Manager) BroadcastTxs(txs []*wire.MsgTx) (error) {
	if _enable_testing && _not_send_tx {
		return nil
	}

	err := p.l1IndexerClient.BroadCastTxs(txs)
	if err != nil {
		Log.Errorf("BroadCastTxs failed. %v", err)
		return err
	}
	for _, tx := range txs {
		p.utxoLockerL1.LockUtxosWithTx(tx)
		// tx确认后自动解锁
	}
	return nil
}

func (p *Manager) BroadcastTx_SatsNet(tx *swire.MsgTx) (string, error) {
	if _enable_testing && _not_send_tx {
		return tx.TxID(), nil
	}

	txId, err := p.l2IndexerClient.BroadCastTx_SatsNet(tx)
	if err != nil {
		Log.Errorf("BroadCastTx_SatsNet %s failed. %v", tx.TxID(), err)
		return "", err
	}
	p.utxoLockerL2.LockUtxosWithTx_SatsNet(tx)
	return txId, nil
}

func (p *Manager) GetUtxoLocker() *UtxoLocker {
	return p.utxoLockerL1
}

func (p *Manager) GetUtxoLocker_SatsNet() *UtxoLocker {
	return p.utxoLockerL2
}

func (p *Manager) IsRecentBlockUtxo(utxoId uint64) bool {
	h, _, _ := indexer.FromUtxoId(utxoId)
	return p.status.SyncHeight == h
}

func (p *Manager) IsBootstrapMode() bool {
	// TODO 需要检查钱包公钥和资产 ?
	return p.cfg.Mode == BOOTSTRAP_NODE
}

func (p *Manager) IsServerMode() bool {
	// TODO 需要检查钱包公钥和资产 ?
	return p.cfg.Mode == SERVER_NODE || p.cfg.Mode == BOOTSTRAP_NODE
}

func (p *Manager) IsBootstrapNode() bool {
	pubkey := p.wallet.GetPaymentPubKey()
	return hex.EncodeToString(pubkey.SerializeCompressed()) == indexer.GetBootstrapPubKey()
}

func (p *Manager) IsCoreNode() bool {
	pubkey := p.wallet.GetPaymentPubKey().SerializeCompressed()
	pkStr := hex.EncodeToString(pubkey)
	if pkStr == indexer.GetBootstrapPubKey() || pkStr == indexer.GetCoreNodePubKey() {
		return true
	}

	b, _ := p.l2IndexerClient.IsCoreNode(pubkey)
	return b
}

func (p *Manager) GetCoreChannelAddr() string {
	var coreChannelId string
	var err error
	if p.IsServerMode() {
		coreChannelId, err = GetP2WSHaddress(p.serverNode.Pubkey.SerializeCompressed(),
			p.wallet.GetPaymentPubKey().SerializeCompressed())
	} else {
		bootstrapPubkey, _ := hex.DecodeString(indexer.GetBootstrapPubKey())
		coreChannelId, err = GetP2WSHaddress(p.serverNode.Pubkey.SerializeCompressed(),
			bootstrapPubkey)
	}
	if err != nil {
		return ""
	}
	return coreChannelId
}
