package wallet

import (
	"fmt"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	indexer "github.com/sat20-labs/indexer/common"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
)


const CONTENT_DEPLOY_BRC20_BODY_4 string = `{"p":"brc-20","op":"deploy","tick":"%s","max":"%d","lim":"%d","dec":"0"}`
const CONTENT_DEPLOY_BRC20_BODY_5 string = `{"p":"brc-20","op":"deploy","tick":"%s","max":"%d","lim":"%d","self_mint":"true","dec":"0"}`
const CONTENT_MINT_BRC20_BODY string = `{"p":"brc-20","op":"mint","tick":"%s","amt":"%s"}`
const CONTENT_MINT_BRC20_TRANSFER_BODY string = `{"p":"brc-20","op":"transfer","tick":"%s","amt":"%s"}`

func (p *Manager) inscribeV2(srcUtxoMgr *UtxoMgr, destAddr string, body string, feeRate int64, 
	defaultUtxos []*TxOutput, onlyUsingDefaultUtxos bool, realPrivateKey []byte, 
	signer Signer, scriptType int, witnessScript []byte, broadcast bool) (*InscribeResv, error) {
	srcAddr := srcUtxoMgr.GetAddress()
	if srcAddr == "" {
		srcAddr = p.wallet.GetAddress()
	}
	if destAddr == "" {
		destAddr = srcAddr
	}

	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}

	p.utxoLockerL1.Reload(srcAddr)
	commitTxPrevOutputList := make([]*PrevOutput, 0)
	included := make(map[string]bool)
	total := int64(0)
	estimatedFee := EstimatedInscribeFee(1, len(body), feeRate, 330)

	if len(defaultUtxos) != 0 {
		for _, utxo := range defaultUtxos {
			total += utxo.OutValue.Value
			commitTxPrevOutputList = append(commitTxPrevOutputList, utxo)
			included[utxo.OutPointStr] = true
		}
		estimatedFee = EstimatedInscribeFee(len(commitTxPrevOutputList), 
				len(body), feeRate, 330)
	}

	if total < estimatedFee {
		if onlyUsingDefaultUtxos {
			// 估算费用
		} else {
			// 补充
			utxos := srcUtxoMgr.GetUtxoListWithTicker(&indexer.ASSET_PLAIN_SAT)
			if len(utxos) == 0 {
				return nil, fmt.Errorf("no utxos for fee")
			}

			for _, u := range utxos {
				if p.utxoLockerL1.IsLocked(u.OutPoint) {
					continue
				}
				_, ok := included[u.OutPoint]
				if ok {
					continue
				}
				included[u.OutPoint] = true
				total += u.Value
				commitTxPrevOutputList = append(commitTxPrevOutputList, u.ToTxOutput())
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

	req := &InscriptionRequest{
		CommitTxPrevOutputList: commitTxPrevOutputList,
		CommitFeeRate:          feeRate,
		RevealFeeRate:          feeRate,
		RevealOutValue:         330,
		RevealPrivateKey:       realPrivateKey,
		InscriptionData: InscriptionData{
			ContentType: CONTENT_TYPE,
			Body:        []byte(body),
		},
		DestAddress:   destAddr,
		ChangeAddress: srcAddr,
		Broadcast:     broadcast,
		ScriptType:    scriptType,
		WitnessScript: witnessScript,
		Signer:        signer,
	}
	inscribe, err := p.inscribe(req)
	if err != nil {
		return inscribe, err
	}
	srcUtxoMgr.RemoveUtxosWithTx(inscribe.CommitTx)
	return inscribe, err
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
	
	return p.inscribeV2(NewUtxoMgr(p.wallet.GetAddress(), p.l1IndexerClient), "", body, feeRate, nil, false, nil, p.SignTxV2, 0, nil, true)
}

// 需要调用方确保amt<=limit
func (p *Manager) MintAsset_brc20(destAddr string, assetName *indexer.AssetName,
	amt *Decimal, defaultUtxos []string, feeRate int64) (*InscribeResv, error) {

	if assetName.Protocol != indexer.PROTOCOL_NAME_BRC20 {
		return nil, fmt.Errorf("not brc20")
	}
	tickInfo := p.GetTickerInfo(assetName)
	if tickInfo == nil {
		return nil, fmt.Errorf("can't find ticker info %s", assetName.String())
	}

	limit, err := indexer.NewDecimalFromString(tickInfo.Limit, tickInfo.Divisibility)
	if err != nil {
		return nil, err
	}
	if limit.Cmp(amt) < 0 {
		return nil, fmt.Errorf("amt %s biger than limit %s", amt.String(), limit.String())
	}
	if amt.Sign() == 0 {
		amt = limit
	}

	var outputs []*TxOutput
	for _, utxo := range defaultUtxos {
		txOut, err := p.l1IndexerClient.GetTxOutput(utxo)
		if err != nil {
			Log.Errorf("GetTxOutFromRawTx %s failed, %v", utxo, err)
			return nil, err
		}
		outputs = append(outputs, txOut)
	}

	body := fmt.Sprintf(CONTENT_MINT_BRC20_BODY, tickInfo.AssetName.Ticker, amt.String())
	return p.inscribeV2(NewUtxoMgr(p.wallet.GetAddress(), p.l1IndexerClient), destAddr, body, feeRate, outputs, false, nil, p.SignTxV2, 0, nil, true)
}

// 需要调用方确保amt<=用户持有量, 注意如果是lockInputs，而且最后不广播，需要对输入的utxo解锁
// 注意输入的defaultUtxos必须确保indexer能返回数据
func (p *Manager) MintTransfer_brc20(srcAddr, destAddr string, assetName *indexer.AssetName,
	amt *Decimal, feeRate int64, defaultUtxos []string, onlyUsingDefaultUtxos bool,  
	revealPrivKey []byte, inChannel, broadcast, lockInputs bool) (*InscribeResv, error) {

	if assetName.Protocol != indexer.PROTOCOL_NAME_BRC20 {
		return nil, fmt.Errorf("not brc20")
	}
	tickInfo := p.GetTickerInfo(assetName)
	if tickInfo == nil {
		return nil, fmt.Errorf("can't find ticker info %s", assetName.String())
	}

	if srcAddr == "" {
		srcAddr = p.wallet.GetAddress()
	}
	if destAddr == "" {
		destAddr = srcAddr
	}

	if amt.Sign() <= 0 {
		return nil, fmt.Errorf("amt %s biger than zero", amt.String())
	}

	var outputs []*TxOutput
	for _, utxo := range defaultUtxos {
		txOut, err := p.l1IndexerClient.GetTxOutput(utxo)
		if err != nil {
			Log.Errorf("GetTxOutFromRawTx %s failed, %v", utxo, err)
			return nil, err
		}
		outputs = append(outputs, txOut)
	}

	var signer Signer
	if !inChannel {
		signer = p.SignTxV2
	}
	
	body := fmt.Sprintf(CONTENT_MINT_BRC20_TRANSFER_BODY, tickInfo.AssetName.Ticker, amt.String())
	insc, err := p.inscribeV2(NewUtxoMgr(srcAddr, p.l1IndexerClient), destAddr, body, feeRate, outputs, onlyUsingDefaultUtxos, 
		revealPrivKey, signer, 0, nil, broadcast)
	if err != nil {
		return nil, err
	}
	if lockInputs {
		// 全局锁定输入
		p.utxoLockerL1.LockUtxosWithTx(insc.CommitTx)
	}
	return insc, nil
}

// defaultUtxos 可以是前置的tx的输出
func (p *Manager) MintTransferV2_brc20(srcAddr, destAddr string, assetName *indexer.AssetName,
	amt *Decimal, feeRate int64, defaultUtxos []*TxOutput, onlyUsingDefaultUtxos bool,  
	revealPrivKey []byte, inChannel, broadcast, lockInputs bool) (*InscribeResv, error) {

	if assetName.Protocol != indexer.PROTOCOL_NAME_BRC20 {
		return nil, fmt.Errorf("not brc20")
	}
	tickInfo := p.GetTickerInfo(assetName)
	if tickInfo == nil {
		return nil, fmt.Errorf("can't find ticker info %s", assetName.String())
	}
	if srcAddr == "" {
		srcAddr = p.wallet.GetAddress()
	}
	if destAddr == "" {
		destAddr = srcAddr
	}

	if amt.Sign() <= 0 {
		return nil, fmt.Errorf("amt %s biger than zero", amt.String())
	}

	var signer Signer
	if !inChannel {
		signer = p.SignTxV2
	}

	body := fmt.Sprintf(CONTENT_MINT_BRC20_TRANSFER_BODY, tickInfo.AssetName.Ticker, amt.String())
	insc, err := p.inscribeV2(NewUtxoMgr(srcAddr, p.l1IndexerClient), destAddr, body, feeRate, defaultUtxos, onlyUsingDefaultUtxos, 
		revealPrivKey, signer, 0, nil, broadcast)
	if err != nil {
		return nil, err
	}
	if lockInputs {
		// 全局锁定输入
		p.utxoLockerL1.LockUtxosWithTx(insc.CommitTx)
	}
	return insc, nil
}

// defaultUtxos 可以是前置的tx的输出
func (p *Manager) MintTransferV3_brc20(srcUtxoMgr *UtxoMgr, destAddr string, assetName *indexer.AssetName,
	amt *Decimal, feeRate int64, defaultUtxos []*TxOutput, onlyUsingDefaultUtxos bool,  
	revealPrivKey []byte, inChannel, broadcast, lockInputs bool) (*InscribeResv, error) {

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

	var signer Signer
	if !inChannel {
		signer = p.SignTxV2
	}

	body := fmt.Sprintf(CONTENT_MINT_BRC20_TRANSFER_BODY, tickInfo.AssetName.Ticker, amt.String())
	insc, err := p.inscribeV2(srcUtxoMgr, destAddr, body, feeRate, defaultUtxos, onlyUsingDefaultUtxos, 
		revealPrivKey, signer, 0, nil, broadcast)
	if err != nil {
		return nil, err
	}
	if lockInputs {
		// 全局锁定输入
		p.utxoLockerL1.LockUtxosWithTx(insc.CommitTx)
	}
	return insc, nil
}

// 对commit tx的输出进行punish
func (p *Manager) MintTransferWithCommitPriKey(srcAddr, destAddr string, assetName *indexer.AssetName,
	amt *Decimal, feeRate int64, defaultUtxos []*TxOutput, 
	scriptType int, redeemScript []byte, revPrivKey *secp256k1.PrivateKey) (*InscribeResv, error) {

	if assetName.Protocol != indexer.PROTOCOL_NAME_BRC20 {
		return nil, fmt.Errorf("not brc20")
	}
	tickInfo := p.GetTickerInfo(assetName)
	if tickInfo == nil {
		return nil, fmt.Errorf("can't find ticker info %s", assetName.String())
	}
	if srcAddr == "" {
		srcAddr = p.wallet.GetAddress()
	}
	if destAddr == "" {
		destAddr = srcAddr
	}

	if amt.Sign() <= 0 {
		return nil, fmt.Errorf("amt %s biger than zero", amt.String())
	}

	signer := func (tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher) error {
		sigHashes := txscript.NewTxSigHashes(tx, prevFetcher)
		for i, txIn := range tx.TxIn {
			preOut := prevFetcher.FetchPrevOutput(txIn.PreviousOutPoint)
			scriptType := GetPkScriptType(preOut.PkScript)
			switch scriptType {
				
			case txscript.WitnessV0ScriptHashTy: //"P2WSH": 
				sigScript, err := txscript.RawTxInWitnessSignature(tx, sigHashes, i,
					preOut.Value, redeemScript, txscript.SigHashAll, revPrivKey)
				if err != nil {
					return fmt.Errorf("failed to sign transaction: %v", err)
				}

				// 构造见证数据
				txIn.Witness = wire.TxWitness{sigScript,
					[]byte{1}, // OP_TRUE to choose the revocation path
					redeemScript}
			}
		}
		return nil
	}

	body := fmt.Sprintf(CONTENT_MINT_BRC20_TRANSFER_BODY, tickInfo.AssetName.Ticker, amt.String())
	insc, err := p.inscribeV2(NewUtxoMgr(srcAddr, p.l1IndexerClient), destAddr, body, feeRate, defaultUtxos, true, nil, 
		signer, scriptType, redeemScript, false)
	if err != nil {
		return nil, err
	}
	return insc, nil
}

func CalcFeeForMintTransfer(inputLen int, srcAddr, destAddr string, scriptType int,
	assetName *indexer.AssetName, amt *Decimal, feeRate int64) (int64, error) {
	
	srcPkScript, err := AddrToPkScript(srcAddr, GetChainParam())
	if err != nil {
		return 0, err
	}
	
	commitTxPrevOutputList := make([]*PrevOutput, 0)
	for i := 0; i < inputLen; i++ {
		commitTxPrevOutputList = append(commitTxPrevOutputList, &PrevOutput{
			OutPointStr:   fmt.Sprintf("aa09fa48dda0e2b7de1843c3db8d3f2d7f2cbe0f83331a125b06516a348abd26:%d", i),
			OutValue: wire.TxOut{
				Value:     10000,
				PkScript:  srcPkScript,
			},
		})
	}

	body := fmt.Sprintf(CONTENT_MINT_BRC20_TRANSFER_BODY, assetName.Ticker, amt.String())
	request := &InscriptionRequest{
		CommitTxPrevOutputList: commitTxPrevOutputList,
		CommitFeeRate:          feeRate,
		RevealFeeRate:          feeRate,
		RevealOutValue:         330,
		InscriptionData:    InscriptionData{
			ContentType: CONTENT_TYPE,
			Body:        []byte(body),
		},
		DestAddress:            destAddr,
		ChangeAddress:          srcAddr,
		ScriptType:             scriptType,
		Signer:                 nil,
	}

	insc, err := Inscribe(GetChainParam(), request, 0)
	if insc == nil {
		return 0, err
	}
	return insc.CommitTxFee+insc.RevealTxFee+330, nil
}

func CalcFeeForDeployTicker_brc20(inputLen int, srcAddr, destAddr string, 
	ticker string, max, lim int64, feeRate int64) (int64, error) {
	
	srcPkScript, err := AddrToPkScript(srcAddr, GetChainParam())
	if err != nil {
		return 0, err
	}
	
	commitTxPrevOutputList := make([]*PrevOutput, 0)
	for i := 0; i < inputLen; i++ {
		commitTxPrevOutputList = append(commitTxPrevOutputList, &PrevOutput{
			OutPointStr:   fmt.Sprintf("aa09fa48dda0e2b7de1843c3db8d3f2d7f2cbe0f83331a125b06516a348abd26:%d", i),
			OutValue: wire.TxOut{
				Value:     10000,
				PkScript:  srcPkScript,
			},
		})
	}

	var body string
	switch len(ticker) {
	case 4:
		body = fmt.Sprintf(CONTENT_DEPLOY_BRC20_BODY_4, ticker, max, lim)
	case 5:
		body = fmt.Sprintf(CONTENT_DEPLOY_BRC20_BODY_5, ticker, max, lim)
	default:
		return 0, fmt.Errorf("invalid ticker length %s", ticker)
	}
	request := &InscriptionRequest{
		CommitTxPrevOutputList: commitTxPrevOutputList,
		CommitFeeRate:          feeRate,
		RevealFeeRate:          feeRate,
		RevealOutValue:         330,
		InscriptionData:    InscriptionData{
			ContentType: CONTENT_TYPE,
			Body:        []byte(body),
		},
		DestAddress:            destAddr,
		ChangeAddress:          srcAddr,
		ScriptType:             SCRIPT_TYPE_TAPROOTKEYSPEND,
		Signer:                 nil,
	}

	insc, err := Inscribe(GetChainParam(), request, 0)
	if insc == nil {
		return 0, err
	}
	return insc.CommitTxFee+insc.RevealTxFee+330, nil
}

func CalcFeeForMintAsset_brc20(inputLen int, srcAddr, destAddr string, 
	assetName *indexer.AssetName, amt *Decimal, feeRate int64) (int64, error) {
	
	srcPkScript, err := AddrToPkScript(srcAddr, GetChainParam())
	if err != nil {
		return 0, err
	}
	
	commitTxPrevOutputList := make([]*PrevOutput, 0)
	for i := 0; i < inputLen; i++ {
		commitTxPrevOutputList = append(commitTxPrevOutputList, &PrevOutput{
			OutPointStr:   fmt.Sprintf("aa09fa48dda0e2b7de1843c3db8d3f2d7f2cbe0f83331a125b06516a348abd26:%d", i),
			OutValue: wire.TxOut{
				Value:     10000,
				PkScript:  srcPkScript,
			},
		})
	}

	body := fmt.Sprintf(CONTENT_MINT_BRC20_BODY, assetName.Ticker, amt.String())
	request := &InscriptionRequest{
		CommitTxPrevOutputList: commitTxPrevOutputList,
		CommitFeeRate:          feeRate,
		RevealFeeRate:          feeRate,
		RevealOutValue:         330,
		InscriptionData:    InscriptionData{
			ContentType: CONTENT_TYPE,
			Body:        []byte(body),
		},
		DestAddress:            destAddr,
		ChangeAddress:          srcAddr,
		ScriptType:             SCRIPT_TYPE_TAPROOTKEYSPEND,
		Signer:                 nil,
	}

	insc, err := Inscribe(GetChainParam(), request, 0)
	if insc == nil {
		return 0, err
	}
	return insc.CommitTxFee+insc.RevealTxFee+330, nil
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