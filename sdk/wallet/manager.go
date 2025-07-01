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

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/runes/runestone"
)


type Node struct {
	client   NodeRPCClient
	Host     string // ip:port
	NodeType string
	NodeId   *secp256k1.PublicKey // same as Pubkey
	Pubkey   *secp256k1.PublicKey
}


type NotifyCB func (string, interface{})

// 密码只有一个，助记词可以有多组，对应不同的wallet
type Manager struct {
	mutex sync.RWMutex

	cfg           *Config
	bInited       bool
	bStop         bool
	password      string
	status        *Status
	walletInfoMap map[int64]*WalletInDB
	wallet        *InternalWallet
	msgCallback   NotifyCB
	tickerInfoMap map[string]*indexer.TickerInfo // 缓存数据, key: AssetName.String()

	db              common.KVDB
	http                 HttpClient
	l1IndexerClient      IndexerRPCClient
	slaveL1IndexerClient IndexerRPCClient
	l2IndexerClient      IndexerRPCClient

	bootstrapNode        []*Node // 引导节点，全网目前唯一，以后由基金会提供至少3个，通过MPC管理密钥
	serverNode           *Node   // 服务节点，由引导节点更新维护，一般情况下，用户只跟一个服务节点打交道

	utxoLockerL1 *UtxoLocker
	utxoLockerL2 *UtxoLocker

	feeRateL1     int64 // sat/vkb
	refreshTimeL1 int64
	feeRateL2     int64 // sat/vkb
	refreshTimeL2 int64

	inscibeMap             map[int64]*InscribeResv                   // key: timestamp
	resvMap                map[int64]Reservation                     // key: timestamp
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
		parsedPubkey, err := ParsePubkey(parts[1])
		if err != nil {
			return fmt.Errorf("invalid AddPeers config item: %s", n)
		}

		var scheme, host string
		// http://host:port
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

		switch parts[0] {
		case "b":
			if bytes.Equal(parsedPubkey.SerializeCompressed(), bootstrappubkey.SerializeCompressed()) {
				node := &Node{
					client:   NewNodeClient(scheme, host, "", p.http),
					NodeId:   bootstrappubkey,
					Host:     parts[2],
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
				client:   NewNodeClient(scheme, host, "", p.http),
				NodeId:   parsedPubkey,
				Host:     parts[2],
				NodeType: SERVER_NODE,
				Pubkey:   parsedPubkey,
			}
		default:
			Log.Errorf("not support type %s", n)
		}
	}

	if len(p.bootstrapNode) == 0 {
		Log.Warnf("no bootstrap node setting, use default setting")
		host := "seed.sat20.org:" + REST_SERVER_PORT
		p.bootstrapNode = []*Node{{
			client:   NewNodeClient("http", host, "", p.http),
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


func (p *Manager) initResvMap() {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	
	p.inscibeMap = make(map[int64]*InscribeResv)
	p.resvMap = make(map[int64]Reservation)
}


func (p *Manager) GenerateNewResvId() int64 {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	id := time.Now().UnixMicro()
	for {
		_, ok := p.resvMap[id]
		if ok {
			id++
			continue
		}
		break
	}
	return id
}

func (p *Manager) GetFeeRate() int64 {
	if IsTestNet() {
		return 2
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

	if *name == ASSET_PLAIN_SAT {
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
	info, err := loadTickerInfo(p.db, name)
	if err != nil {
		info = p.l1IndexerClient.GetTickInfo(name)
		if info == nil {
			Log.Errorf("GetTickInfo %s failed", name)
			return nil
		}
		saveTickerInfo(p.db, info)
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.tickerInfoMap[info.AssetName.String()] = info
	if name.Protocol == indexer.PROTOCOL_NAME_RUNES {
		p.tickerInfoMap[info.DisplayName] = info
	}
	return info
}

func (p *Manager) getTickerInfoFromRuneId(runeId string) *indexer.TickerInfo {
	p.mutex.RLock()
	info, ok := p.tickerInfoMap[runeId]
	p.mutex.RUnlock()
	if ok {
		return info
	}

	return p.getTickerInfo(&indexer.AssetName{
		Protocol: indexer.PROTOCOL_NAME_RUNES,
		Type: indexer.ASSET_TYPE_FT,
		Ticker: runeId,
	})
}

func (p *Manager) removeAllTickerInfo() {
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
		OutValue:    wire.TxOut{
			Value: tx.TxOut[vout].Value,
			PkScript: tx.TxOut[vout].PkScript,
		},
	}, nil
}

func (p *Manager) GetUtxosForFee(address string, value int64) ([]string, error) {
	if address == "" {
		address = p.wallet.GetAddress()
	}

	utxos, _, err := p.l1IndexerClient.GetAllUtxosWithAddress(address)
	if err != nil {
		Log.Errorf("GetAllUtxosWithAddress %s failed. %v", address, err)
		return nil, err
	}
	p.utxoLockerL1.Reload(address)
	if value == 0 {
		value = MAX_FEE
	}

	result := make([]string, 0)
	total := int64(0)
	for _, u := range utxos {
		utxo := u.Txid + ":" + strconv.Itoa(u.Vout)
		if p.utxoLockerL1.IsLocked(utxo) {
			continue
		}
		total += u.Value
		result = append(result, utxo)
		if total >= value {
			break
		}
	}

	if total < value {
		return nil, fmt.Errorf("no enough utxo for fee, require %d but only %d", value, total)
	}

	return result, nil
}


func (p *Manager) GetUtxosForFeeV2(address string, value int64, needStub bool) ([]string, error) {
	if address == "" {
		address = p.wallet.GetAddress()
	}

	utxos, _, err := p.l1IndexerClient.GetAllUtxosWithAddress(address)
	if err != nil {
		Log.Errorf("GetAllUtxosWithAddress %s failed. %v", address, err)
		return nil, err
	}
	p.utxoLockerL1.Reload(address)
	if value == 0 {
		value = MAX_FEE
	}

	// 有序的utxo列表，直接放最后一个
	result := make([]string, 0)
	i := len(utxos) - 1
	if needStub {	
		for i >= 0 {
			u := utxos[i]
			i--
			utxo := u.Txid + ":" + strconv.Itoa(u.Vout)
			if p.utxoLockerL1.IsLocked(utxo) {
				continue
			}
			result = append(result, utxo)
			break
		}
	}

	total := int64(0)
	for j := 0; j < i; j++ {
		u := utxos[j]
		utxo := u.Txid + ":" + strconv.Itoa(u.Vout)
		if p.utxoLockerL1.IsLocked(utxo) {
			continue
		}
		total += u.Value
		result = append(result, utxo)
		if total >= value {
			break
		}
	}

	if total < value {
		return nil, fmt.Errorf("no enough utxo for fee, require %d but only %d", value, total)
	}

	if needStub && len(result) < 2 {
		return nil, fmt.Errorf("need at least 2 utxos for fee, but only %d found", len(result))
	}

	return result, nil
}


func (p *Manager) GetUtxosForStubs(address string, n int) ([]string, error) {
	if address == "" {
		address = p.wallet.GetAddress()
	}

	utxos, _, err := p.l1IndexerClient.GetAllUtxosWithAddress(address)
	if err != nil {
		Log.Errorf("GetAllUtxosWithAddress %s failed. %v", address, err)
		return nil, err
	}
	p.utxoLockerL1.Reload(address)

	// 有序的utxo列表，直接放最后一个
	result := make([]string, 0)
	i := len(utxos) - 1
	
	for i >= 0 {
		u := utxos[i]
		i--
		utxo := u.Txid + ":" + strconv.Itoa(u.Vout)
		if p.utxoLockerL1.IsLocked(utxo) {
			continue
		}
		if u.Value > 600 {
			break
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

func (p *Manager) GetUtxosForFee_SatsNet(address string, value int64) ([]string, error) {
	if address == "" {
		address = p.wallet.GetAddress()
	}

	utxos, _, err := p.l2IndexerClient.GetAllUtxosWithAddress(address)
	if err != nil {
		Log.Errorf("GetAllUtxosWithAddress %s failed. %v", address, err)
		return nil, err
	}
	p.utxoLockerL2.Reload(address)
	if value == 0 {
		value = DEFAULT_FEE_SATSNET
	}

	result := make([]string, 0)
	total := int64(0)
	for _, u := range utxos {
		utxo := u.Txid + ":" + strconv.Itoa(u.Vout)
		if p.utxoLockerL2.IsLocked(utxo) {
			continue
		}
		total += u.Value
		result = append(result, utxo)
		if total >= value {
			break
		}
	}

	if total < value {
		return nil, fmt.Errorf("no enough utxo for fee, require %d but only %d", value, total)
	}

	return result, nil
}

func (p *Manager) getUtxosWithAsset(address string, amt *Decimal, assetName *swire.AssetName) ([]string, error) {
	if address == "" {
		address = p.wallet.GetAddress()
	}
	utxos := p.l1IndexerClient.GetUtxoListWithTicker(address, assetName)
	p.utxoLockerL1.Reload(address)

	result := make([]string, 0)
	var total *Decimal
	for _, u := range utxos {
		if p.utxoLockerL1.IsLocked(u.OutPoint) {
			continue
		}
		output := OutputInfoToOutput(u)
		num := output.GetAsset(assetName)
		if num.Sign() != 0 {
			total = total.Add(num)
			result = append(result, output.OutPointStr)
			if total.Cmp(amt) >= 0 {
				break
			}
		}
	}

	if total.Cmp(amt) < 0 {
		return nil, fmt.Errorf("no enough utxo for %s, require %s but only %d", assetName.String(), amt.String(), total)
	}

	return result, nil
}

func (p *Manager) getUtxosWithAsset_SatsNet(address string, amt *Decimal, assetName *swire.AssetName) ([]string, error) {
	if address == "" {
		address = p.wallet.GetAddress()
	}
	utxos := p.l2IndexerClient.GetUtxoListWithTicker(address, assetName)
	p.utxoLockerL2.Reload(address)

	result := make([]string, 0)
	var total *Decimal
	for _, u := range utxos {
		if p.utxoLockerL2.IsLocked(u.OutPoint) {
			continue
		}
		output := OutputInfoToOutput_SatsNet(u)
		num := output.GetAsset(assetName)
		if num.Sign() != 0 {
			total = total.Add(num)
			result = append(result, output.OutPointStr)
			if total.Cmp(amt) >= 0 {
				break
			}
		}
	}

	if total.Cmp(amt) < 0 {
		return nil, fmt.Errorf("no enough utxo for %s, require %s but only %d", assetName.String(), amt.String(), total)
	}

	return result, nil
}

func (p *Manager) getUtxosWithAssetV2(address string, plainSats int64,
	amt *Decimal, assetName *swire.AssetName) ([]string, []string, error) {
	// TODO 最好由索引器提供接口，不要这样多次获取数据

	if address == "" {
		address = p.wallet.GetAddress()
	}
	utxos := p.l1IndexerClient.GetUtxoListWithTicker(address, assetName)
	p.utxoLockerL1.Reload(address)

	resultAssets := make([]string, 0)
	resultPlains := make([]string, 0)
	var totalAssets *Decimal
	var totalPlainSats int64
	for _, u := range utxos {
		if p.utxoLockerL1.IsLocked(u.OutPoint) {
			continue
		}
		output := OutputInfoToOutput(u)
		num := output.GetAsset(assetName)
		if num.Sign() > 0 {
			totalAssets = totalAssets.Add(num)
			resultAssets = append(resultAssets, output.OutPointStr)
			if totalAssets.Cmp(amt) >= 0 {
				break
			}
		}
	}
	if totalAssets.Cmp(amt) < 0 {
		return nil, nil, fmt.Errorf("no enough utxo for %s, require %s but only %d", assetName.String(), amt.String(), totalAssets)
	}

	if !indexer.IsPlainAsset(assetName) {
		// 这里是纯粹的白聪，跟L2不一样
		utxos := p.l1IndexerClient.GetUtxoListWithTicker(address, &ASSET_PLAIN_SAT)
		p.utxoLockerL1.Reload(address)
		for _, u := range utxos {
			if p.utxoLockerL1.IsLocked(u.OutPoint) {
				continue
			}
			output := OutputInfoToOutput(u)
			totalPlainSats += output.GetPlainSat()
			resultPlains = append(resultPlains, output.OutPointStr)
			if totalPlainSats >= plainSats {
				break
			}
		}
	}
	if totalPlainSats < plainSats {
		// 如果还不够，需要让用户手动操作，先分离出足够的白聪出来
		return nil, nil, fmt.Errorf("no enough utxo for plain sats, require %d but only %d, ", plainSats, totalPlainSats)
	}

	return resultAssets, resultPlains, nil
}

// 这里白聪，是指额外的白聪，不包含绑定了指定资产的聪
func (p *Manager) getUtxosWithAssetV2_SatsNet(address string, plainSats int64,
	amt *Decimal, assetName *swire.AssetName) ([]string, []string, error) {
	// TODO 最好由索引器提供接口，不要这样多次获取数据

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
		return nil, nil, fmt.Errorf("no enough utxo for %s, require %s but only %d", assetName.String(), expectedAssetAmt.String(), totalAssets)
	}

	if totalPlainSats < plainSats && !indexer.IsPlainAsset(assetName) {
		// 所有包含了白聪的utxo都在这里，即使utxo中有其他资产
		utxos := p.l2IndexerClient.GetUtxoListWithTicker(address, &ASSET_PLAIN_SAT)
		p.utxoLockerL2.Reload(address)
		for _, u := range utxos {
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
func (p *Manager) getAssetAmount(address string, name *swire.AssetName) (*Decimal, *Decimal) {
	if address == "" {
		address = p.wallet.GetAddress()
	}
	bPlainAsset := indexer.IsPlainAsset(name)

	var availableSats, lockedSats int64
	var available, locked *Decimal
	outputs := p.l1IndexerClient.GetUtxoListWithTicker(address, name)

	p.utxoLockerL1.Reload(address)
	for _, u := range outputs {
		if p.utxoLockerL1.IsLocked(u.OutPoint) {
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

func (p *Manager) getAssetAmount_SatsNet(address string, name *swire.AssetName) (*Decimal, *Decimal) {
	if address == "" {
		address = p.wallet.GetAddress()
	}
	bPlainAsset := indexer.IsPlainAsset(name)

	var availableSats, lockedSats int64
	var available, locked *Decimal
	outputs := p.l2IndexerClient.GetUtxoListWithTicker(address, name)

	p.utxoLockerL2.Reload(address)
	for _, u := range outputs {
		if p.utxoLockerL2.IsLocked(u.OutPoint) {
			if bPlainAsset {
				lockedSats += u.GetPlainSat()
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
				availableSats += u.GetPlainSat()
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
	txId, err := p.l1IndexerClient.BroadCastTx(tx)
	if err != nil {
		Log.Errorf("BroadCastTx %s failed. %v", tx.TxID(), err)
		return "", err
	}
	p.utxoLockerL1.LockUtxosWithTx(tx)
	// tx确认后自动解锁
	return txId, nil
}

func (p *Manager) BroadcastTx_SatsNet(tx *swire.MsgTx) (string, error) {
	txId, err := p.l2IndexerClient.BroadCastTx_SatsNet(tx)
	if err != nil {
		Log.Errorf("BroadCastTx_SatsNet %s failed. %v", tx.TxID(), err)
		return "", err
	}
	p.utxoLockerL2.LockUtxosWithTx_SatsNet(tx)
	return txId, nil
}
