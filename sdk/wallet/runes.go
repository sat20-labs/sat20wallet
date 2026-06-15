package wallet

import (
	"fmt"
	"math"
	"strings"

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
	return EncipherRunePayloadWithPointer(edicts, nil)
}

func EncipherRunePayloadWithPointer(edicts []runestone.Edict, pointer *uint32) ([]byte, error) {
	stone := runestone.Runestone{
		Edicts:  edicts,
		Pointer: pointer,
	}

	result, err := stone.Encipher()
	if err != nil {
		return nil, err
	}

	if ENABLE_TESTING {
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

func EncipherRuneMintPayload(runeId *runestone.RuneId, pointer *uint32) ([]byte, error) {
	if runeId == nil {
		return nil, fmt.Errorf("rune id is nil")
	}

	stone := runestone.Runestone{
		Mint:    runeId,
		Pointer: pointer,
	}

	return stone.Encipher()
}

func DecipherRunePayload(pkScript []byte) ([]runestone.Edict, error) {
	stone := runestone.Runestone{}

	result, err := stone.DecipherFromPkScript(pkScript)
	if err != nil {
		return nil, err
	}
	return result.Runestone.Edicts, nil
}

func GenEtching(displayName string, symbol int32, maxSupply int64) (*runestone.Etching, error) {
	return GenEtchingWithTerms(displayName, symbol, maxSupply, 0, true, 0)
}

// 定制的铸造
func GenEtchingWithTerms(displayName string, symbol int32, maxSupply, limit int64, selfMint bool, divisibility int64) (*runestone.Etching, error) {
	if maxSupply <= 0 {
		return nil, fmt.Errorf("invalid max supply %d", maxSupply)
	}
	if divisibility < 0 || divisibility > int64(runestone.MaxDivisibility) {
		return nil, fmt.Errorf("invalid divisibility %d", divisibility)
	}
	if !selfMint {
		if limit <= 0 {
			return nil, fmt.Errorf("invalid mint limit %d", limit)
		}
		if maxSupply%limit != 0 {
			return nil, fmt.Errorf("max supply %d must be divisible by mint limit %d", maxSupply, limit)
		}
	}

	spacerRune, err := runestone.SpacedRuneFromString(displayName)
	if err != nil {
		Log.Errorf("SpacedRuneFromString %s failed, %v", displayName, err)
		return nil, err
	}

	var premine *uint128.Uint128
	var terms *runestone.Terms
	if selfMint {
		v := uint128.New(uint64(maxSupply), 0)
		premine = &v
	} else {
		amount := uint128.New(uint64(limit), 0)
		cap := uint128.New(uint64(maxSupply/limit), 0)
		terms = &runestone.Terms{
			Amount: &amount,
			Cap:    &cap,
		}
	}

	d := uint8(divisibility)
	return &runestone.Etching{
		Divisibility: &d,
		Premine:      premine,
		Rune:         &spacerRune.Rune,
		Spacers:      &spacerRune.Spacers,
		Symbol:       &symbol,
		Terms:        terms,
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
	txs, err := p.PrepareInscribeRunes(address, runeName, nullData, feeRate, commitTxPrevOutputList)
	if err != nil {
		return nil, err
	}

	_, err = p.BroadcastRunesDeployCommit(txs)
	if err != nil {
		return nil, err
	}

	// 等待6个确认后才能发送etching指令
	return txs, nil
}

func (p *Manager) saveInscribeResv(resv *InscribeResv) error {
	p.mutex.Lock()
	p.inscibeMap[resv.Id] = resv
	p.resvMap[resv.Id] = resv
	p.mutex.Unlock()

	return SaveInscribeResv(p.db, resv)
}

func (p *Manager) PrepareInscribeRunes(address string, runeName, nullData []byte,
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

	txs.Status = RS_INSCRIBING_START
	err = p.saveInscribeResv(txs)
	if err != nil {
		return nil, err
	}
	p.utxoLockerL1.LockUtxosWithTx(txs.CommitTx)

	return txs, nil
}

func RunesDeployRequiredFee(resv *InscribeResv) int64 {
	if resv == nil {
		return 0
	}

	revealOutValue := int64(0)
	if resv.RevealTx != nil {
		for _, out := range resv.RevealTx.TxOut {
			if !IsOpReturn(out.PkScript) {
				revealOutValue += out.Value
			}
		}
	}

	return resv.CommitTxFee + resv.RevealTxFee + revealOutValue
}

func (p *Manager) BroadcastRunesDeployCommit(resv *InscribeResv) (string, error) {
	if resv == nil || resv.CommitTx == nil {
		return "", fmt.Errorf("invalid runes deploy reservation")
	}
	if resv.Status >= RS_INSCRIBING_COMMIT_BROADCASTED {
		return resv.CommitTx.TxID(), nil
	}

	commitTxId, err := p.BroadcastTx(resv.CommitTx)
	if err != nil {
		return "", err
	}
	Log.Infof("commit txid: %s", commitTxId)

	resv.Status = RS_INSCRIBING_COMMIT_BROADCASTED
	err = p.saveInscribeResv(resv)
	if err != nil {
		return "", err
	}

	return commitTxId, nil
}

func (p *Manager) BroadcastRunesDeployReveal(resv *InscribeResv) (string, error) {
	if resv == nil || resv.RevealTx == nil {
		return "", fmt.Errorf("invalid runes deploy reservation")
	}
	if resv.Status >= RS_INSCRIBING_REVEAL_BROADCASTED {
		return resv.RevealTx.TxID(), nil
	}

	revealTxId, err := p.BroadcastTx(resv.RevealTx)
	if err != nil {
		return "", err
	}
	Log.Infof("reveal txid: %s", revealTxId)

	resv.Status = RS_INSCRIBING_REVEAL_BROADCASTED
	err = p.saveInscribeResv(resv)
	if err != nil {
		return "", err
	}

	return revealTxId, nil
}

func (p *Manager) DeployTicker_runes(destAddr string, ticker string, symbol int32,
	max, feeRate int64) (*InscribeResv, error) {
	return p.DeployTicker_runesWithTerms(destAddr, ticker, symbol, max, 0, true, 0, feeRate)
}

func (p *Manager) DeployTicker_runesWithTerms(destAddr string, ticker string, symbol int32,
	max, limit int64, selfMint bool, divisibility int64, feeRate int64) (*InscribeResv, error) {

	wallet := p.wallet
	address := wallet.GetAddress()

	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}
	etching, err := GenEtchingWithTerms(ticker, symbol, max, limit, selfMint, divisibility)
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

func (p *Manager) PrepareDeployTicker_runes(destAddr string, ticker string, symbol int32,
	max, feeRate int64) (*InscribeResv, error) {
	return p.PrepareDeployTicker_runesWithTerms(destAddr, ticker, symbol, max, 0, true, 0, feeRate)
}

func (p *Manager) PrepareDeployTicker_runesWithTerms(destAddr string, ticker string, symbol int32,
	max, limit int64, selfMint bool, divisibility int64, feeRate int64) (*InscribeResv, error) {

	wallet := p.wallet
	address := wallet.GetAddress()

	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}
	etching, err := GenEtchingWithTerms(ticker, symbol, max, limit, selfMint, divisibility)
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

	return p.PrepareInscribeRunes(destAddr, etching.Rune.Commitment(), nullData, feeRate, commitTxPrevOutputList)
}

func runeIdFromTickerInfo(tickInfo *indexer.TickerInfo) (*runestone.RuneId, error) {
	if tickInfo == nil {
		return nil, fmt.Errorf("ticker info is nil")
	}
	if tickInfo.AssetName.Protocol != indexer.PROTOCOL_NAME_RUNES {
		return nil, fmt.Errorf("not runes")
	}
	if strings.Contains(tickInfo.DisplayName, ":") {
		return runestone.RuneIdFromString(tickInfo.DisplayName)
	}
	if strings.Contains(tickInfo.AssetName.Ticker, ":") {
		return runestone.RuneIdFromString(tickInfo.AssetName.Ticker)
	}
	return nil, fmt.Errorf("can't resolve rune id for %s", tickInfo.AssetName.String())
}

func (p *Manager) MintAsset_runes(destAddr string, tickInfo *indexer.TickerInfo, feeRate int64) (string, error) {
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}
	if destAddr == "" {
		destAddr = p.wallet.GetAddress()
	}

	runeId, err := runeIdFromTickerInfo(tickInfo)
	if err != nil {
		return "", err
	}

	nullData, err := EncipherRuneMintPayload(runeId, nil)
	if err != nil {
		return "", err
	}

	tx, err := p.SendAssets(destAddr, ASSET_PLAIN_SAT.String(), "330", feeRate, nullData)
	if err != nil {
		return "", err
	}
	return tx.TxID(), nil
}
