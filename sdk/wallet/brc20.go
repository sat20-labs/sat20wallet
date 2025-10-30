package wallet

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
)


const CONTENT_DEPLOY_BRC20_BODY_4 string = `{"p":"brc-20","op":"deploy","tick":"%s","max":"%d","lim":"%d","dec":"0"}`
const CONTENT_DEPLOY_BRC20_BODY_5 string = `{"p":"brc-20","op":"deploy","tick":"%s","max":"%d","lim":"%d","self_mint":"true","dec":"0"}`
const CONTENT_MINT_BRC20_BODY string = `{"p":"brc-20","op":"mint","tick":"%s","amt":"%d"}`
const CONTENT_MINT_BRC20_TRANSFER_BODY string = `{"p":"brc-20","op":"transfer","tick":"%s","amt":"%s"}`

func (p *Manager) inscribeV2(srcAddr, destAddr string, body string, feeRate int64, 
	defaultUtxos []string, onlyUsingDefaultUtxos bool, privateKey []byte, inChannel, broadcast bool) (*InscribeResv, error) {
	if srcAddr == "" {
		srcAddr = p.wallet.GetAddress()
	}
	if destAddr == "" {
		destAddr = srcAddr
	}

	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}

	changePkScript, err := AddrToPkScript(srcAddr, GetChainParam())
	if err != nil {
		return nil, err
	}

	p.utxoLockerL1.Reload(srcAddr)
	commitTxPrevOutputList := make([]*PrevOutput, 0)
	included := make(map[string]bool)
	total := int64(0)
	estimatedFee := EstimatedInscribeFee(1, len(body), feeRate, 330)

	if len(defaultUtxos) != 0 {
		for _, utxo := range defaultUtxos {
			txOut, err := p.GetTxOutFromRawTx(utxo)
			if err != nil {
				Log.Errorf("GetTxOutFromRawTx %s failed, %v", utxo, err)
				return nil, err
			}

			parts := strings.Split(utxo, ":")
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid utxo %s", utxo)
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
			included[utxo] = true

			estimatedFee = EstimatedInscribeFee(len(commitTxPrevOutputList), 
				len(body), feeRate, 330)
			if total >= estimatedFee {
				break
			}
		}
	}

	if total < estimatedFee {
		if onlyUsingDefaultUtxos {
			// 调整feeRate到最低，尽可能广播该铭刻交易
			feeRate = 1
		} else {
			// 补充
			utxos, _, err := p.l1IndexerClient.GetAllUtxosWithAddress(srcAddr)
			if err != nil {
				Log.Errorf("GetAllUtxosWithAddress %s failed. %v", srcAddr, err)
				return nil, err
			}
			if len(utxos) == 0 {
				return nil, fmt.Errorf("no utxos for fee")
			}

			for _, u := range utxos {
				utxo := u.Txid + ":" + strconv.Itoa(u.Vout)
				if p.utxoLockerL1.IsLocked(utxo) {
					continue
				}
				_, ok := included[utxo]
				if ok {
					continue
				}
				included[utxo] = true
				total += u.Value
				commitTxPrevOutputList = append(commitTxPrevOutputList, &PrevOutput{
					TxId:     u.Txid,
					VOut:     uint32(u.Vout),
					Amount:   u.Value,
					PkScript: changePkScript,
				})
				estimatedFee = EstimatedInscribeFee(len(commitTxPrevOutputList), 
					len(body), feeRate, 330)
				if total >= estimatedFee {
					break
				}
			}
			if total < estimatedFee {
				return nil, fmt.Errorf("no enough utxos for fee")
			}
		}
	}

	if srcAddr == "" {
		srcAddr = p.wallet.GetAddress()
	}
	if destAddr == "" {
		destAddr = srcAddr
	}
	
	var signer Signer
	if !inChannel {
		signer = p.SignTxV2
	}
	req := &InscriptionRequest{
		CommitTxPrevOutputList: commitTxPrevOutputList,
		CommitFeeRate:          feeRate,
		RevealFeeRate:          feeRate,
		RevealOutValue:         330,
		RevealPrivateKey:       privateKey,
		InscriptionData: InscriptionData{
			ContentType: CONTENT_TYPE,
			Body:        []byte(body),
		},
		DestAddress:   destAddr,
		ChangeAddress: srcAddr,
		Broadcast:     broadcast,
		InChannel:     inChannel,
		Signer:        signer,
	}
	return p.inscribe(req)
}

func (p *Manager) DeployTicker_brc20(ticker string, max, lim int64, feeRate int64) (*InscribeResv, error) {
	if max%lim != 0 {
		return nil, fmt.Errorf("invalid max %d", max)
	}

	var body string
	switch len(ticker) {
	case 4:
		body = fmt.Sprintf(CONTENT_DEPLOY_BRC20_BODY_4, ticker, max, lim)
	case 5:
		body = fmt.Sprintf(CONTENT_DEPLOY_BRC20_BODY_5, ticker, max, lim)
	default:
		return nil, fmt.Errorf("invalid ticker length %s", ticker)
	}
	
	return p.inscribeV2("", "", body, feeRate, nil, false, nil, false, true)
}

// 需要调用方确保amt<=limit
func (p *Manager) MintAsset_brc20(destAddr string, assetName *indexer.AssetName,
	amt int64, defaultUtxos []string, feeRate int64) (*InscribeResv, error) {

	if assetName.Protocol != indexer.PROTOCOL_NAME_BRC20 {
		return nil, fmt.Errorf("not brc20")
	}
	tickInfo := p.GetTickerInfo(assetName)
	if tickInfo == nil {
		return nil, fmt.Errorf("can't find ticker info %s", assetName.String())
	}

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

	body := fmt.Sprintf(CONTENT_MINT_BRC20_BODY, tickInfo.AssetName.Ticker, amt)
	return p.inscribeV2("", destAddr, body, feeRate, defaultUtxos, false, nil, false, true)
}

// 需要调用方确保amt<=用户持有量, 注意如果是lockInputs，而且最后不广播，需要对输入的utxo解锁
func (p *Manager) MintTransfer_brc20(srcAddr, destAddr string, assetName *indexer.AssetName,
	amt *Decimal, feeRate int64, defaultUtxos []string, onlyUsingDefaultUtxos bool,  
	privateKey []byte, inChannel, broadcast, lockInputs bool) (*InscribeResv, error) {

	if assetName.Protocol != indexer.PROTOCOL_NAME_BRC20 {
		return nil, fmt.Errorf("not brc20")
	}
	tickInfo := p.GetTickerInfo(assetName)
	if tickInfo == nil {
		return nil, fmt.Errorf("can't find ticker info %s", assetName.String())
	}

	if amt.Sign() <= 0 {
		return nil, fmt.Errorf("amt %s biger than zero", amt.String())
	}

	body := fmt.Sprintf(CONTENT_MINT_BRC20_TRANSFER_BODY, tickInfo.AssetName.Ticker, amt.String())
	insc, err := p.inscribeV2(srcAddr, destAddr, body, feeRate, defaultUtxos, onlyUsingDefaultUtxos, privateKey, inChannel, broadcast)
	if err != nil {
		return nil, err
	}
	if lockInputs {
		// 全局锁定输入
		p.utxoLockerL1.LockUtxosWithTx(insc.CommitTx)
	}
	return insc, nil
}

func GenerateBRC20TransferOutput(revealTx *wire.MsgTx, assetName *indexer.AssetName, amt *Decimal) *indexer.TxOutput {
	output := indexer.GenerateTxOutput(revealTx, 0)
	assetInfo := indexer.AssetInfo{
		Name: *assetName,
		Amount: *amt,
		BindingSat: 0,
	}
	output.Assets = indexer.TxAssets{assetInfo}
	output.Offsets = map[indexer.AssetName]indexer.AssetOffsets{
						*assetName: {{Start:0, End:1}},
					}
	output.SatBindingMap = map[int64]*indexer.AssetInfo{
						0: &assetInfo,
					}
	return output
}

func GenerateInscribeMoreData(destAddr string, assetName *indexer.AssetName, 
	amt *Decimal, feeRate int64, revealKey []byte) ([]byte, error) {
	more, err := stxscript.NewScriptBuilder().
	AddData([]byte(destAddr)).
	AddData([]byte(assetName.String())).
	AddData([]byte(amt.ToFormatString())).
	AddInt64(feeRate).
	AddData((revealKey)).Script()
	if err != nil {
		return nil, err
	}
	return more, nil
}

func ParseInscribeMoreData(more []byte) (destAddr string, assetName *indexer.AssetName, 
	amt *Decimal, feeRate int64, revealKey []byte, err error) {
	tokenizer := stxscript.MakeScriptTokenizer(0, more)
	if !tokenizer.Next() || tokenizer.Err() != nil {
		err = fmt.Errorf("missing address")
		return
	}
	destAddr = string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		err = fmt.Errorf("missing asset name")
		return
	}
	name := string(tokenizer.Data())
	assetName = indexer.NewAssetNameFromString(name)
	

	if !tokenizer.Next() || tokenizer.Err() != nil {
		err = fmt.Errorf("missing asset amt")
		return
	}
	amt, err = indexer.NewDecimalFromFormatString(string(tokenizer.Data()))
	if err != nil {
		return
	}

	if !tokenizer.Next() || tokenizer.Err() != nil {
		err = fmt.Errorf("missing fee rate")
		return
	}
	feeRate = tokenizer.ExtractInt64()

	if !tokenizer.Next() || tokenizer.Err() != nil {
		err = fmt.Errorf("missing reveal private key")
		return
	}
	revealKey = (tokenizer.Data())
	return
}