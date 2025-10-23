package wallet

import (
	"fmt"
	"strconv"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
)


const CONTENT_DEPLOY_BRC20_BODY_4 string = `{"p":"brc-20","op":"deploy","tick":"%s","max":"%d","lim":"%d","dec":"0"}`
const CONTENT_DEPLOY_BRC20_BODY_5 string = `{"p":"brc-20","op":"deploy","tick":"%s","max":"%d","lim":"%d","self_mint":"true","dec":"0"}`
const CONTENT_MINT_BRC20_BODY string = `{"p":"brc-20","op":"mint","tick":"%s","amt":"%d"}`
const CONTENT_MINT_BRC20_TRANSFER_BODY string = `{"p":"brc-20","op":"transfer","tick":"%s","amt":"%s"}`


func (p *Manager) inscribeV2(destAddr string, body string, feeRate int64, defaultUtxos []string, broadcast bool) (*InscribeResv, error) {
	wallet := p.wallet
	pkScript, _ := GetP2TRpkScript(wallet.GetPaymentPubKey())
	address := wallet.GetAddress()
	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}

	p.utxoLockerL1.Reload(address)
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
		utxos, _, err := p.l1IndexerClient.GetAllUtxosWithAddress(address)
		if err != nil {
			Log.Errorf("GetAllUtxosWithAddress %s failed. %v", address, err)
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
				PkScript: pkScript,
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

	return p.inscribe(destAddr, body, 330, feeRate, commitTxPrevOutputList, broadcast)
}

func (p *Manager) DeployTicker_brc20(ticker string, max, lim int64, feeRate int64) (*InscribeResv, error) {
	if max%lim != 0 {
		return nil, fmt.Errorf("invalid max %d", max)
	}

	body := fmt.Sprintf(CONTENT_DEPLOY_BRC20_BODY_4, ticker, max, lim)
	return p.inscribeV2(p.wallet.GetAddress(), body, feeRate, nil, true)
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
	return p.inscribeV2(destAddr, body, feeRate, defaultUtxos, true)
}

// 需要调用方确保amt<=用户持有量
func (p *Manager) MintTransfer_brc20(destAddr string, assetName *indexer.AssetName,
	amt *Decimal, defaultUtxos []string, feeRate int64, broadcast bool) (*InscribeResv, error) {

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
	return p.inscribeV2(destAddr, body, feeRate, defaultUtxos, broadcast)
}
