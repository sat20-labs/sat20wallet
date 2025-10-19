package wallet

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

/* 各种协议作为lpt资产发行协议的问题：
1. 用ordx协议作为lpt资产发行协议，唯一的问题就是绑定聪会让通道的状态变迁受utxo最小值所限制，
导致在充值lpt时，需要让远端持有330聪绑定的资产，才可以方便做stake和unstake
2. 用runes协议，通道所支持的资产种类受op_return能表达的转账的个数所限制，而且名字太长不方便
3. 用brc20协议，需要两步才能转移，这让承诺交易需要依赖一大堆前置交易，用于正确转账

综合来看，用ordx协议更方便。提供流动性的项目方，先提供330聪的lpt作为质押给基金会，也是合适的做法。
*/

// TODO 需要让indexer接受.LPT为后缀的资产
// 每个通道都可以发行自己的LPT，以最后序号为区分（只能由核心节点发行，核心节点编号，并且永不能更改）
// 核心节点通过持有一个从0-999编号的NFT来区分
const CONTENT_TYPE string = "text/plain;charset=utf-8"
const CONTENT_DEPLOY_BODY string = `{"p":"ordx","op":"deploy","tick":"%s","max":"%d","lim":"%d","n":"%d","self":"100","des":"%s"}`
const CONTENT_MINT_BODY string = `{"p":"ordx","op":"mint","tick":"%s","amt":"%d"}`
const CONTENT_MINT_ABBR_BODY string = `{"p":"ordx","op":"mint","tick":"%s"}`
const CONTENT_SETKV1_BODY string = `{"p":"sns","op":"update","name":"%s","%s":"%s"}`
const CONTENT_SETKV_N_BODY string = `{"p":"sns","op":"update","name":"%s""%s"}`

// 不精确，因为 60 这个数稍微大一些，但足够用
func EstimatedInscribeFee(inputLen, bodyLen int, feeRate int64, revealOutValue int64) int64 {
	commitFee := int64(154 + (inputLen - 1) * 60)
	revealFee := int64(bodyLen / 4 + 138)
	return (commitFee + revealFee) * feeRate + revealOutValue
}


// 只适合 CONTENT_DEPLOY_BODY （可以从EstimatedInscribeFee计算得出）
func EstimatedDeployFee(inputLen int, feeRate int64) int64 {
	/*
		经验数据，调整 CONTENT_DEPLOY_BODY 后需要调整
		estimatedInputValue1 := 340*feeRate + 330
		estimatedInputValue2 := 400*feeRate + 330
		estimatedInputValue3 := 460*feeRate + 330
	*/
	return (340+int64(inputLen-1)*60)*feeRate + 330
}


func (p *Manager) inscribe(address string, body string, revealOutValue int64,
	feeRate int64, commitTxPrevOutputList []*PrevOutput) (*InscribeResv, error) {
	wallet := p.wallet
	changeAddr := wallet.GetAddress()
	if address == "" {
		address = changeAddr
	}

	request := &InscriptionRequest{
		CommitTxPrevOutputList: commitTxPrevOutputList,
		CommitFeeRate:          feeRate,
		RevealFeeRate:          feeRate,
		RevealOutValue:         revealOutValue,
		InscriptionData: InscriptionData{
			ContentType: CONTENT_TYPE,
			Body:        []byte(body),
		},
		DestAddress:   address,
		ChangeAddress: changeAddr,
		SignAndSend:   true,
		Signer:        p.SignTxV2,
		PublicKey:     wallet.GetPaymentPubKey(),
	}

	txs, err := Inscribe(GetChainParam(), request, p.GenerateNewResvId())
	if err != nil {
		return nil, err
	}
	Log.Infof("commit fee %d, reveal fee %d", txs.CommitTxFee, txs.RevealTxFee)

	err = p.TestAcceptance([]*wire.MsgTx{txs.CommitTx, txs.RevealTx})
	if err != nil {
		return nil, err
	}

	commitTxId, err := p.BroadcastTx(txs.CommitTx)
	if err != nil {
		return nil, err
	}
	Log.Infof("commit txid: %s", commitTxId)

	revealTxId, err := p.BroadcastTx(txs.RevealTx)
	if err != nil {
		// 缓存数据，确保可以取回资金
		txs.Status = RS_INSCRIBING_COMMIT_BROADCASTED
		SaveInscribeResv(p.db, txs)
		return txs, err
	}
	Log.Infof("reveal txid: %s", revealTxId)

	txs.Status = RS_INSCRIBING_REVEAL_BROADCASTED
	SaveInscribeResv(p.db, txs)

	return txs, nil
}

func (p *Manager) DeployOrdxTicker(ticker string, max, lim int64, n int) (*InscribeResv, error) {
	if n <= 0 || n > 65535 {
		return nil, fmt.Errorf("n too big (>65535)")
	}
	if lim%int64(n) != 0 {
		return nil, fmt.Errorf("invalid lim %d", lim)
	}
	if max%int64(n) != 0 {
		return nil, fmt.Errorf("invalid max %d", max)
	}

	wallet := p.wallet

	pkScript, _ := GetP2TRpkScript(wallet.GetPaymentPubKey())
	address := wallet.GetAddress()

	feeRate := p.GetFeeRate()
	// 经验数据，调整 CONTENT_DEPLOY_BODY 后需要调整
	// estimatedInputValue1 := 340*feeRate + 330
	// estimatedInputValue2 := 400*feeRate + 330
	// estimatedInputValue3 := 460*feeRate + 330

	utxos, _, err := p.l1IndexerClient.GetAllUtxosWithAddress(address)
	if err != nil {
		Log.Errorf("GetAllUtxosWithAddress %s failed. %v", address, err)
		return nil, err
	}
	if len(utxos) == 0 {
		return nil, fmt.Errorf("no utxos for fee")
	}
	sort.Slice(utxos, func(i, j int) bool {
		return utxos[i].Value > utxos[j].Value
	})

	p.utxoLockerL1.Reload(address)
	commitTxPrevOutputList := make([]*PrevOutput, 0)
	total := int64(0)
	estimatedFee := int64(0)
	for _, u := range utxos {
		utxo := u.Txid + ":" + strconv.Itoa(u.Vout)
		if p.utxoLockerL1.IsLocked(utxo) {
			continue
		}
		total += u.Value
		commitTxPrevOutputList = append(commitTxPrevOutputList, &PrevOutput{
			TxId:     u.Txid,
			VOut:     uint32(u.Vout),
			Amount:   u.Value,
			PkScript: pkScript,
		})
		estimatedFee = EstimatedDeployFee(len(commitTxPrevOutputList), feeRate)
		if total >= estimatedFee {
			break
		}
		// if len(commitTxPrevOutputList) == 1 && total >= estimatedInputValue1 {
		// 	ok = true
		// 	break
		// }
		// if len(commitTxPrevOutputList) == 2 && total >= estimatedInputValue2 {
		// 	ok = true
		// 	break
		// }
		// if len(commitTxPrevOutputList) == 3 && total >= estimatedInputValue3 {
		// 	ok = true
		// 	break
		// }
	}
	if total < estimatedFee {
		return nil, fmt.Errorf("no enough utxos for fee")
	}

	pubkey := hex.EncodeToString(p.wallet.GetPaymentPubKey().SerializeCompressed())
	body := fmt.Sprintf(CONTENT_DEPLOY_BODY, ticker, max, lim, n, pubkey)
	return p.inscribe("", body, 330, feeRate, commitTxPrevOutputList)
}

// 只适合 CONTENT_MINT_BODY ，可以估算 CONTENT_MINT_ABBR_BODY
func EstimatedMintFee(inputLen int, feeRate, revealOutValue int64) int64 {
	/*
		// 经验数据，调整 CONTENT_MINT_BODY 后需要调整
		estimatedInputValue1 := 310*feeRate + revealOutValue
		estimatedInputValue2 := 370*feeRate + revealOutValue
		estimatedInputValue3 := 430*feeRate + revealOutValue
	*/
	return (310+int64(inputLen-1)*60)*feeRate + revealOutValue
}

// 需要调用方确保amt<=limit
func (p *Manager) MintOrdxAsset(destAddr string, tickInfo *indexer.TickerInfo,
	amt int64, preUtxo string) (*InscribeResv, error) {

	limit, err := strconv.ParseInt(tickInfo.Limit, 10, 64)
	if err != nil {
		return nil, err
	}
	if limit < amt {
		return nil, fmt.Errorf("amt %d biger than limit %d", amt, limit)
	}
	if amt == 0 {
		amt = limit
	}

	wallet := p.wallet

	pkScript, _ := GetP2TRpkScript(wallet.GetPaymentPubKey())
	address := wallet.GetAddress()

	revealOutValue := GetBindingSatNum(indexer.NewDefaultDecimal(amt), tickInfo.N)
	if revealOutValue < 330 {
		revealOutValue = 330
	}
	feeRate := p.GetFeeRate()
	// 经验数据，调整 CONTENT_MINT_BODY 后需要调整
	// estimatedInputValue1 := 310*feeRate + revealOutValue
	// estimatedInputValue2 := 370*feeRate + revealOutValue
	// estimatedInputValue3 := 430*feeRate + revealOutValue

	utxos, _, err := p.l1IndexerClient.GetAllUtxosWithAddress(address)
	if err != nil {
		Log.Errorf("GetAllUtxosWithAddress %s failed. %v", address, err)
		return nil, err
	}
	if len(utxos) == 0 {
		return nil, fmt.Errorf("no utxos for fee")
	}
	sort.Slice(utxos, func(i, j int) bool {
		return utxos[i].Value > utxos[j].Value
	})

	commitTxPrevOutputList := make([]*PrevOutput, 0)
	total := int64(0)
	included := make(map[string]bool)
	// preUtxo，可能还没有确认，但可以加进来使用
	if preUtxo != "" {
		txOut, err := p.GetTxOutFromRawTx(preUtxo)
		if err != nil {
			Log.Errorf("GetTxOutFromRawTx %s failed, %v", preUtxo, err)
			return nil, err
		}

		parts := strings.Split(preUtxo, ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid utxo %s", preUtxo)
		}
		vout, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, err
		}

		total += txOut.OutValue.Value
		commitTxPrevOutputList = append(commitTxPrevOutputList, &PrevOutput{
			TxId:     parts[0],
			VOut:     uint32(vout),
			Amount:   txOut.OutValue.Value,
			PkScript: txOut.OutValue.PkScript,
		})
		included[preUtxo] = true
	}

	p.utxoLockerL1.Reload(address)

	estimatedFee := int64(0)
	for _, u := range utxos {
		utxo := u.Txid + ":" + strconv.Itoa(u.Vout)
		if p.utxoLockerL1.IsLocked(utxo) {
			continue
		}
		_, ok := included[utxo]
		if ok {
			continue
		}
		included[preUtxo] = true

		total += u.Value
		commitTxPrevOutputList = append(commitTxPrevOutputList, &PrevOutput{
			TxId:     u.Txid,
			VOut:     uint32(u.Vout),
			Amount:   u.Value,
			PkScript: pkScript,
		})
		estimatedFee = EstimatedMintFee(len(commitTxPrevOutputList), feeRate, revealOutValue)
		if total >= estimatedFee {
			break
		}
		// if len(commitTxPrevOutputList) == 1 && total >= estimatedInputValue1 {
		// 	ok = true
		// 	break
		// }
		// if len(commitTxPrevOutputList) == 2 && total >= estimatedInputValue2 {
		// 	ok = true
		// 	break
		// }
		// if len(commitTxPrevOutputList) == 3 && total >= estimatedInputValue3 {
		// 	ok = true
		// 	break
		// }
	}
	if total < estimatedFee {
		return nil, fmt.Errorf("no enough utxos for fee")
	}

	var body string
	if amt == limit {
		body = fmt.Sprintf(CONTENT_MINT_ABBR_BODY, tickInfo.AssetName.Ticker)
	} else {
		body = fmt.Sprintf(CONTENT_MINT_BODY, tickInfo.AssetName.Ticker, amt)
	}

	return p.inscribe(destAddr, body, revealOutValue, feeRate, commitTxPrevOutputList)
}

// 暂时不支持coreid后缀
func NewLPTAssetName(name *AssetName, _ int) *swire.AssetName {
	var newTicker string
	if name == nil || *name == PLAIN_ASSET {
		newTicker = "satoshi.lpt" // fmt.Sprintf("satoshi.lpt%d", id)
	} else if name.Protocol == indexer.PROTOCOL_NAME_ORDX {
		newTicker = fmt.Sprintf("%s.lpt", name.Ticker) //fmt.Sprintf("%s.lpt%d", name.Ticker, id)
	} else {
		newTicker = fmt.Sprintf("%s.%s.lpt", name.Ticker, name.Protocol) // fmt.Sprintf("%s.%s.lpt%d", name.Ticker, name.Protocol, id)
	}

	return &swire.AssetName{
		Protocol: indexer.PROTOCOL_NAME_ORDX,
		Type:     indexer.ASSET_TYPE_FT,
		Ticker:   newTicker,
	}
}

func NewOrgAssetName(lpt *AssetName) *swire.AssetName {
	ticker := lpt.Ticker
	parts := strings.Split(lpt.Ticker, ".")
	if len(parts) >= 2 {
		ticker = parts[0]
	}
	protocol := indexer.PROTOCOL_NAME_ORDX
	if len(parts) == 3 {
		protocol = parts[1]
	}

	if ticker == "satoshi" {
		return &ASSET_PLAIN_SAT
	} else {
		return &swire.AssetName{
			Protocol: protocol,
			Type:     lpt.Type,
			Ticker:   ticker,
		}
	}
}

func (p *Manager) GetLPTAssetName(name *AssetName, id int) *AssetName {
	lpt := &AssetName{
		AssetName: *NewLPTAssetName(name, id),
		N:         name.N,
	}

	tickerInfo := p.getTickerInfo(&lpt.AssetName)
	if tickerInfo == nil {
		return nil
	}

	lpt.N = tickerInfo.N
	return lpt
}

func (p *Manager) GetOrgAssetName(lpt *AssetName) *AssetName {

	org := NewOrgAssetName(lpt)

	tickerInfo := p.getTickerInfo(org)
	if tickerInfo == nil {
		return nil
	}

	return &AssetName{
		AssetName: *org,
		N:         tickerInfo.N,
	}
}


func (p *Manager) InscribeKeyValueInName(name string, key string, value string, feeRate int64) (*InscribeResv, error) {

	wallet := p.wallet

	pkScript, _ := GetP2TRpkScript(wallet.GetPaymentPubKey())
	address := wallet.GetAddress()


	utxos, _, err := p.l1IndexerClient.GetAllUtxosWithAddress(address)
	if err != nil {
		Log.Errorf("GetAllUtxosWithAddress %s failed. %v", address, err)
		return nil, err
	}
	if len(utxos) == 0 {
		return nil, fmt.Errorf("no utxos for fee")
	}
	sort.Slice(utxos, func(i, j int) bool {
		return utxos[i].Value > utxos[j].Value
	})

	name = strings.ToLower(name)
	name = strings.TrimSpace(name)
	body := fmt.Sprintf(CONTENT_SETKV1_BODY, name, key, value)
	lenBody := len(body)
	p.utxoLockerL1.Reload(address)
	commitTxPrevOutputList := make([]*PrevOutput, 0)
	total := int64(0)
	estimatedFee := int64(0)
	for _, u := range utxos {
		utxo := u.Txid + ":" + strconv.Itoa(u.Vout)
		if p.utxoLockerL1.IsLocked(utxo) {
			continue
		}
		total += u.Value
		commitTxPrevOutputList = append(commitTxPrevOutputList, &PrevOutput{
			TxId:     u.Txid,
			VOut:     uint32(u.Vout),
			Amount:   u.Value,
			PkScript: pkScript,
		})
		estimatedFee = EstimatedInscribeFee(len(commitTxPrevOutputList), lenBody, feeRate, 330)
		if total >= estimatedFee {
			break
		}
	}
	if total < estimatedFee {
		return nil, fmt.Errorf("no enough utxos for fee")
	}

	return p.inscribe("", body, 330, feeRate, commitTxPrevOutputList)
}


func (p *Manager) InscribeMultiKeyValueInName(name string, kv map[string]string) (*InscribeResv, error) {

	wallet := p.wallet

	pkScript, _ := GetP2TRpkScript(wallet.GetPaymentPubKey())
	address := wallet.GetAddress()

	feeRate := p.GetFeeRate()
	utxos, _, err := p.l1IndexerClient.GetAllUtxosWithAddress(address)
	if err != nil {
		Log.Errorf("GetAllUtxosWithAddress %s failed. %v", address, err)
		return nil, err
	}
	if len(utxos) == 0 {
		return nil, fmt.Errorf("no utxos for fee")
	}
	sort.Slice(utxos, func(i, j int) bool {
		return utxos[i].Value > utxos[j].Value
	})

	var kvs string
	for k, v := range kv {
		kvs += fmt.Sprintf(",\"%s\":\"%s\"", k, v)
	}

	name = strings.ToLower(name)
	name = strings.TrimSpace(name)
	body := fmt.Sprintf(CONTENT_SETKV_N_BODY, name, kvs)
	lenBody := len(body)

	p.utxoLockerL1.Reload(address)
	commitTxPrevOutputList := make([]*PrevOutput, 0)
	total := int64(0)
	estimatedFee := int64(0)
	for _, u := range utxos {
		utxo := u.Txid + ":" + strconv.Itoa(u.Vout)
		if p.utxoLockerL1.IsLocked(utxo) {
			continue
		}
		total += u.Value
		commitTxPrevOutputList = append(commitTxPrevOutputList, &PrevOutput{
			TxId:     u.Txid,
			VOut:     uint32(u.Vout),
			Amount:   u.Value,
			PkScript: pkScript,
		})
		estimatedFee = EstimatedInscribeFee(len(commitTxPrevOutputList), lenBody, feeRate, 330)
		if total >= estimatedFee {
			break
		}
	}
	if total < estimatedFee {
		return nil, fmt.Errorf("no enough utxos for fee")
	}

	return p.inscribe("", body, 330, feeRate, commitTxPrevOutputList)
}
