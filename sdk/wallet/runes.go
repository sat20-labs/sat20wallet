package wallet

import (
	"fmt"
	"math"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/runes/runestone"
	"lukechampine.com/uint128"
)

// 预估
func GetRunesEstimatePayload(_ *AssetName, vout int) ([]byte, error) {
	edict := runestone.Edict{
		ID: runestone.RuneId{
			Block: 840000,
			Tx:    100,
		},
		Amount: uint128.Uint128{Lo: math.MaxInt64, Hi: math.MaxInt64},
		Output: uint32(vout),
	}

	return EncipherRunePayload([]runestone.Edict{edict})
}

func EncipherRunePayload(edicts []runestone.Edict) ([]byte, error) {
	stone := runestone.Runestone{
		Edicts: edicts,
	}

	result, err := stone.Encipher()
	if err != nil {
		return nil, err
	}

	if _enable_testing {
		edicts2, err := DecipherRunePayload(result)
		if err != nil {
			Log.Errorf("DecipherRunePayload failed. %v", err)
			return nil, err
		}
		if len(edicts) != len(edicts2) {
			return nil, fmt.Errorf("different %d %d", len(edicts), len(edicts2))
		}
		for i := 0; i < len(edicts); i++ {
			if edicts[i] != edicts2[i] {
				return nil, fmt.Errorf("different %v %v", edicts[i], edicts2[i])
			}
		}
	}

	return result, nil
}

func DecipherRunePayload(pkScript []byte) ([]runestone.Edict, error) {
	stone := runestone.Runestone{}

	result, err := stone.DecipherFromPkScript(pkScript)
	if err != nil {
		return nil, err
	}
	return result.Runestone.Edicts, nil
}

// 定制的铸造
func GenEtching(displayName string, symbol int32, maxSupply int64) (*runestone.Etching, error) {
	spacerRune, err := runestone.SpacedRuneFromString(displayName)
	if err != nil {
		Log.Errorf("SpacedRuneFromString %s failed, %v", displayName, err)
		return nil, err
	}

	premine := uint128.New(uint64(maxSupply), 0)

	return &runestone.Etching{
		Divisibility: nil,
		Premine:      &premine,
		Rune:         &spacerRune.Rune,
		Spacers:      &spacerRune.Spacers,
		Symbol:       &symbol,
		Terms:        nil,
		Turbo:        false,
	}, nil
}

func EstimatedDeployRunesNameFee(inputLen int, feeRate int64) int64 {
	/*
		经验数据，跟etching的数据相关，这里只是一个估算
		estimatedInputValue1 := 340*feeRate + 330
		estimatedInputValue2 := 400*feeRate + 330
		estimatedInputValue3 := 460*feeRate + 330

			txIn   commit fee | reveal fee
			1       154        172          = 326
			2       212        172          = 384
			3       269        172          = 441
			4       327        172          = 499
	*/
	return (340+int64(inputLen-1)*60)*feeRate + 660
}

func (p *Manager) inscribeRunes(address string, runeName, nullData []byte,
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
		RevealOutValue:         660,
		InscriptionData: InscriptionData{
			ContentType:      "",
			Body:             nil,
			RuneName:         runeName,
			RevealTxNullData: nullData,
		},
		DestAddress:   address,
		ChangeAddress: changeAddr,
		Broadcast:     false,
		Signer:        p.SignTxV2,
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

	// 缓存
	txs.Status = RS_INSCRIBING_COMMIT_BROADCASTED
	SaveInscribeResv(p.db, txs)

	// 等待6个确认后才能发送etching指令
	return txs, nil

	// revealTxId, err := p.BroadcastTx(txs.RevealTx)
	// if err != nil {
	// 	// 缓存数据，确保可以取回资金
	// 	return txs, err
	// }
	// Log.Infof("reveal txid: %s", revealTxId)

	// txs.Status = RS_INSCRIBING_REVEAL_BROADCASTED
	// saveReservation(p.db, txs)

	// return txs, nil
}

func (p *Manager) DeployTicker_runes(destAddr string, ticker string, symbol int32, 
	max, feeRate int64) (*InscribeResv, error) {

	wallet := p.wallet
	address := wallet.GetAddress()

	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}
	etching, err := GenEtching(ticker, symbol, max)
	if err != nil {
		return nil, err
	}
	stone := runestone.Runestone{
		Etching: etching,
	}
	nullData, err := stone.Encipher()
	if err != nil {
		return nil, err
	}

	utxos := p.l1IndexerClient.GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)
	if len(utxos) == 0 {
		return nil, fmt.Errorf("no utxos for fee")
	}

	p.utxoLockerL1.Reload(address)
	commitTxPrevOutputList := make([]*PrevOutput, 0)
	total := int64(0)
	estimatedFee := int64(0)
	for _, u := range utxos {
		if p.utxoLockerL1.IsLocked(u.OutPoint) {
			continue
		}
		total += u.Value
		commitTxPrevOutputList = append(commitTxPrevOutputList, u.ToTxOutput())
		estimatedFee = EstimatedDeployRunesNameFee(len(commitTxPrevOutputList), feeRate)
		if total >= estimatedFee {
			break
		}
	}
	if total < estimatedFee {
		return nil, fmt.Errorf("no enough utxos for fee")
	}

	return p.inscribeRunes(destAddr, etching.Rune.Commitment(), nullData, feeRate, commitTxPrevOutputList)
}

// 需要调用方确保amt<=limit
func (p *Manager) MintAsset_runes(destAddr string, tickInfo *indexer.TickerInfo) (string, error) {

	//wallet := p.wallet

	// pkScript, _ := GetP2TRpkScript(wallet.GetPaymentPubKey())
	// address := wallet.GetAddress()

	return "", fmt.Errorf("not implemented")
}
