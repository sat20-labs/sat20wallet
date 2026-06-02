package wallet

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type remoteSignData struct {
	tx           *wire.MsgTx
	prevFetcher  txscript.PrevOutputFetcher
	tx2          *swire.MsgTx
	prevFetcher2 stxscript.PrevOutputFetcher
	fee          int64
	notSign      bool
}

func (p *Manager) BatchSendAssetsV3FromAddress(dest []*SendAssetInfo,
	assetNameStr string, feeRate int64, memo []byte,
	reason, action string, channelId string, payFeeByLocalAddress bool) (string, int64, error) {

	return p.CoBatchSendAssetsV3FromAddress(p.wallet, dest, assetNameStr, feeRate, memo,
		reason, action, channelId, payFeeByLocalAddress)
}

func (p *Manager) CoBatchSendAssetsV3FromAddress(localWallet common.Wallet, dest []*SendAssetInfo,
	assetNameStr string, feeRate int64, memo []byte,
	reason, action string, channelId string, payFeeByLocalAddress bool) (string, int64, error) {

	if p.wallet == nil {
		return "", 0, fmt.Errorf("wallet is not created/unlocked")
	}
	if localWallet == nil {
		localWallet = p.wallet
	}
	if !IsValidNullData(memo) {
		return "", 0, fmt.Errorf("invalid length of null data %d", len(memo))
	}
	if channelId == "" {
		return p.BatchSendAssetsV3(dest, assetNameStr, feeRate, memo, reason, false)
	}

	name := ParseAssetString(assetNameStr)
	if name == nil {
		return "", 0, fmt.Errorf("invalid asset name %s", assetNameStr)
	}
	tickerInfo := p.getTickerInfo(name)
	if tickerInfo == nil {
		return "", 0, fmt.Errorf("can't get ticker %s info", assetNameStr)
	}
	assetName := GetAssetName(tickerInfo)
	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}

	excluded := make(map[string]bool)
	var tx *wire.MsgTx
	var prevFetcher *txscript.MultiPrevOutFetcher
	var fee int64
	var inscribes []*InscribeResv
	var err error
	switch name.Protocol {
	case "":
		tx, prevFetcher, fee, err = p.BuildBatchSendTxV3_btc(channelId,
			excluded, dest, feeRate, memo, false, true, true)
	case indexer.PROTOCOL_NAME_ORDX:
		tx, prevFetcher, fee, err = p.BuildBatchSendTxV3_ordx(channelId,
			excluded, dest, assetName, feeRate, memo, false, true, localWallet, payFeeByLocalAddress)
	case indexer.PROTOCOL_NAME_RUNES:
		if len(memo) != 0 {
			return "", 0, fmt.Errorf("do not attach memo when send runes asset")
		}
		tx, prevFetcher, fee, err = p.BuildBatchSendTxV3_runes(channelId,
			excluded, dest, assetName, feeRate, false, true, localWallet, payFeeByLocalAddress)
	case indexer.PROTOCOL_NAME_BRC20:
		tx, prevFetcher, fee, inscribes, err = p.BuildBatchSendTxV3_brc20(channelId,
			excluded, dest, assetName, feeRate, memo, false, true, localWallet, payFeeByLocalAddress)
	default:
		return "", 0, fmt.Errorf("BatchSendAssetsV3FromAddress unsupport protocol %s", name.Protocol)
	}
	if err != nil {
		return "", 0, err
	}
	defer p.unlockOpenInscribes(inscribes)

	witness, peerPubKey, err := p.channelWitness(localWallet, channelId)
	if err != nil {
		return "", 0, err
	}

	signData, txsSignInfo, txs, err := p.generateSignData(localWallet, inscribes,
		dest, witness, peerPubKey, payFeeByLocalAddress)
	if err != nil {
		return "", 0, err
	}
	signData, txsSignInfo, txs, err = p.addToSignData(localWallet, tx, prevFetcher, fee, "", nil,
		witness, peerPubKey, signData, txsSignInfo, txs, false)
	if err != nil {
		return "", 0, err
	}

	moredata := wwire.RemoteSignMoreData{
		Tx:       txsSignInfo,
		Witness:  witness,
		Action:   action,
		MoreData: memo,
	}
	md, err := json.Marshal(moredata)
	if err != nil {
		return "", 0, err
	}

	tx, totalFee, err := p.reqRemoteSignAndBroadcast(localWallet, witness, peerPubKey,
		reason, channelId, signData, txsSignInfo, txs, nil, md)
	if err != nil {
		return "", 0, err
	}
	p.closeInscribes(inscribes)

	Log.Infof("BatchSendAssetsV3FromAddress succeed. %s %d", tx.TxID(), totalFee)
	return tx.TxID(), totalFee, nil
}

func (p *Manager) CoBatchSendV4(localWallet common.Wallet, dest []*SendAssetInfo, assetNameStr string, feeRate int64,
	reason, action, channelId string, memo []byte,
	sendDeAnchorTx bool, deAnchorAmt *Decimal, excludeRecentBlock, payFeeByCurrentAddress bool) (string, int64, error) {

	start := time.Now()
	Log.Infof("CoBatchSendV4 %s", assetNameStr)
	if localWallet == nil {
		localWallet = p.wallet
	}
	if localWallet == nil {
		return "", 0, fmt.Errorf("wallet is not created/unlocked")
	}
	if len(memo) > txscript.MaxDataCarrierSize {
		return "", 0, fmt.Errorf("too large data %d in op_return", len(memo))
	}

	asset := ParseAssetString(assetNameStr)
	if asset == nil {
		return "", 0, fmt.Errorf("invalid asset name %s", assetNameStr)
	}
	tickerInfo := p.getTickerInfo(asset)
	if tickerInfo == nil {
		return "", 0, fmt.Errorf("can't get ticker %s info", assetNameStr)
	}
	assetName := GetAssetName(tickerInfo)
	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}

	excluded := make(map[string]bool)
	var tx *wire.MsgTx
	var prevFetcher *txscript.MultiPrevOutFetcher
	var fee int64
	var inscribes []*InscribeResv
	var err error
	switch asset.Protocol {
	case "":
		tx, prevFetcher, fee, err = p.BuildBatchSendTxV3_btc(channelId,
			excluded, dest, feeRate, memo, false, true, true)
	case indexer.PROTOCOL_NAME_ORDX:
		tx, prevFetcher, fee, err = p.BuildBatchSendTxV3_ordx(channelId,
			excluded, dest, assetName, feeRate, memo, false, true, localWallet, payFeeByCurrentAddress)
	case indexer.PROTOCOL_NAME_RUNES:
		tx, prevFetcher, fee, err = p.BuildBatchSendTxV3_runes(channelId,
			excluded, dest, assetName, feeRate, false, true, localWallet, payFeeByCurrentAddress)
	case indexer.PROTOCOL_NAME_BRC20:
		tx, prevFetcher, fee, inscribes, err = p.BuildBatchSendTxV3_brc20(channelId,
			excluded, dest, assetName, feeRate, memo, false, true, localWallet, payFeeByCurrentAddress)
	default:
		return "", 0, fmt.Errorf("CoBatchSendV4 unsupport protocol %s", asset.Protocol)
	}
	if err != nil {
		return "", 0, err
	}
	defer p.unlockOpenInscribes(inscribes)

	witness, peerPubKey, err := p.channelWitness(localWallet, channelId)
	if err != nil {
		return "", 0, err
	}

	signData, txsSignInfo, txs, err := p.generateSignData(localWallet, inscribes,
		dest, witness, peerPubKey, payFeeByCurrentAddress)
	if err != nil {
		return "", 0, err
	}
	signData, txsSignInfo, txs, err = p.addToSignData(localWallet, tx, prevFetcher, fee, "", nil,
		witness, peerPubKey, signData, txsSignInfo, txs, false)
	if err != nil {
		return "", 0, err
	}

	txs2 := make([]*swire.MsgTx, 0)
	if sendDeAnchorTx {
		tx2, prevFetcher2, err := p.CreateDeAnchorTx(channelId, tx.TxID(), assetName, deAnchorAmt, 0, memo)
		if err != nil {
			return "", 0, err
		}
		signData, txsSignInfo, txs2, err = p.addToSignData_SatsNet(localWallet, tx2, prevFetcher2, 0,
			"descend", witness, peerPubKey, signData, txsSignInfo, txs2, false)
		if err != nil {
			return "", 0, err
		}
	}

	moredata := wwire.RemoteSignMoreData{
		Tx:       txsSignInfo,
		Witness:  witness,
		Action:   action,
		MoreData: memo,
	}
	md, err := json.Marshal(moredata)
	if err != nil {
		return "", 0, err
	}

	tx, totalFee, err := p.reqRemoteSignAndBroadcast(localWallet, witness, peerPubKey,
		reason, channelId, signData, txsSignInfo, txs, txs2, md)
	if err != nil {
		return "", 0, err
	}
	p.closeInscribes(inscribes)

	Log.Infof("CoBatchSendV4 %s finished, %v", tx.TxID(), time.Since(start))
	return tx.TxID(), totalFee, nil
}

func (p *Manager) CoBatchSendV4_SatsNet(localWallet common.Wallet, dest []*SendAssetInfo, assetName string,
	reason, action, channelId string, memo []byte) (string, error) {

	start := time.Now()
	Log.Infof("CoBatchSendV4_SatsNet %s", assetName)
	if localWallet == nil {
		localWallet = p.wallet
	}
	if localWallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}

	asset := ParseAssetString(assetName)
	if asset == nil {
		return "", fmt.Errorf("invalid asset name %s", assetName)
	}
	if p.getTickerInfo(asset) == nil {
		return "", fmt.Errorf("can't get ticker %s info", assetName)
	}

	var totalValue int64
	var totalAmt *Decimal
	for _, item := range dest {
		totalAmt = totalAmt.Add(item.AssetAmt)
		totalValue += item.Value
	}
	totalValue += DEFAULT_FEE_SATSNET

	witness, peerPubKey, err := p.channelWitness(localWallet, channelId)
	if err != nil {
		return "", err
	}

	utxos, fees, err := p.GetUtxosWithAssetV2_SatsNet(channelId, totalValue, totalAmt, asset, nil)
	if err != nil {
		return "", err
	}

	tx, prevFetcher, err := p.BuildBatchSendTxV2_SatsNet(channelId, dest, asset, utxos, fees, memo)
	if err != nil {
		return "", err
	}

	sigs, err := PartialSignTxWithWallet_SatsNet(localWallet, tx, prevFetcher, witness, peerPubKey)
	if err != nil {
		return "", err
	}

	txHex, err := EncodeMsgTx_SatsNet(tx)
	if err != nil {
		return "", err
	}
	txs := []*wwire.TxSignInfo{{
		Tx:        txHex,
		L1Tx:      false,
		LocalSigs: sigs,
	}}
	moredata := wwire.RemoteSignMoreData{
		Tx:       txs,
		Witness:  witness,
		Action:   action,
		MoreData: memo,
	}
	md, err := json.Marshal(moredata)
	if err != nil {
		return "", err
	}

	req := wwire.SignRequest{
		MsgHeader:    wwire.NewMsgHeader(),
		ChannelId:    channelId,
		CommitHeight: -1,
		Reason:       reason,
		MoreData:     md,
		PubKey:       localWallet.GetPubKey().SerializeCompressed(),
		NodeId:       localWallet.GetNodePubKey().SerializeCompressed(),
	}
	msg, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	sig, err := localWallet.SignMessageWithIndex(msg, 0)
	if err != nil {
		return "", err
	}
	peerSig, err := p.serverNode.client.SendSigReq(&req, sig)
	if err != nil {
		return "", err
	}

	_, err = FinalSignTxWithWallet_SatsNet(localWallet, tx, prevFetcher, witness, peerPubKey, peerSig[0])
	if err != nil {
		return "", err
	}

	txid, err := p.BroadcastTx_SatsNet(tx)
	if err != nil {
		return "", err
	}
	PrintJsonTx_SatsNet(tx, "CoBatchSendV4_SatsNet TX")

	Log.Infof("CoBatchSendV4_SatsNet %s finished, %v", txid, time.Since(start))
	return txid, nil
}

func (p *Manager) CreateDeAnchorTx(channelAddr string, splicingOutTxId string,
	assetName *AssetName, totalAmt *Decimal, totalValue int64, memo []byte) (
	*swire.MsgTx, *stxscript.MultiPrevOutFetcher, error) {

	pkScript, err := GetPkScriptFromAddress_SatsNet(channelAddr)
	if err != nil {
		return nil, nil, err
	}
	utxos, fees, err := p.GetUtxosWithAssetV2_SatsNet(channelAddr, totalValue, totalAmt, &assetName.AssetName, nil)
	if err != nil {
		return nil, nil, err
	}

	tx := swire.NewMsgTx(swire.TxVersion)
	prevFetcher := stxscript.NewMultiPrevOutFetcher(nil)
	var input TxOutput_SatsNet
	allUtxos := append(utxos, fees...)
	for _, utxo := range allUtxos {
		if p.utxoLockerL2.IsLocked(utxo) {
			return nil, nil, fmt.Errorf("utxo %s is locked", utxo)
		}
		txOut, err := p.l2IndexerClient.GetTxOutput(utxo)
		if err != nil {
			return nil, nil, fmt.Errorf("GetTxOutput %s failed, %v", utxo, err)
		}
		output := OutputToSatsNet(txOut)
		outpoint := output.OutPoint()
		tx.AddTxIn(swire.NewTxIn(outpoint, nil, nil))
		prevFetcher.AddPrevOut(*outpoint, &output.OutValue)
		input.Merge(output)
	}

	outValue := totalValue + indexer.GetBindingSatNum(totalAmt, uint32(assetName.N))
	out1, out2, err := input.Split(&assetName.AssetName, outValue, totalAmt)
	if err != nil {
		return nil, nil, err
	}
	if !out2.Zero() {
		tx.AddTxOut(swire.NewTxOut(out2.Value(), GenTxAssetsFromAssets(out2.OutValue.Assets), pkScript))
	}

	nullDataScript, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_DESCENDING, []byte(splicingOutTxId))
	if err != nil {
		return nil, nil, fmt.Errorf("sindexer.NullDataScript failed. %v", err)
	}
	tx.AddTxOut(swire.NewTxOut(out1.Value(), GenTxAssetsFromAssets(out1.OutValue.Assets), nullDataScript))

	channelScript, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_CHANNELID, []byte(channelAddr))
	if err != nil {
		return nil, nil, fmt.Errorf("sindexer.NullDataScript failed. %v", err)
	}
	tx.AddTxOut(swire.NewTxOut(0, nil, channelScript))
	if len(memo) > 0 {
		tx.AddTxOut(swire.NewTxOut(0, nil, memo))
	}

	PrintJsonTx_SatsNet(tx, "deAnchor")
	return tx, prevFetcher, nil
}

func (p *Manager) generateSignData(localWallet common.Wallet, inscribes []*InscribeResv, dest []*SendAssetInfo,
	witness, peerPubKey []byte, notSign bool) ([]*remoteSignData, []*wwire.TxSignInfo, []*wire.MsgTx, error) {

	signData := make([]*remoteSignData, 0)
	txs := make([]*wire.MsgTx, 0)
	txsSignInfo := make([]*wwire.TxSignInfo, 0)
	for i, insc := range inscribes {
		more, err := GenerateInscribeMoreData(
			dest[i].Address,
			dest[i].AssetName,
			dest[i].AssetAmt,
			insc.FeeRate,
			insc.RevealPrivateKey)
		if err != nil {
			return nil, nil, nil, err
		}

		signData, txsSignInfo, txs, err = p.addToSignData(localWallet, insc.CommitTx,
			insc.GetCommitPrevOutputFetcher(),
			insc.CommitTxFee+insc.RevealTxFee+330, "commit", more,
			witness, peerPubKey, signData, txsSignInfo, txs, notSign)
		if err != nil {
			return nil, nil, nil, err
		}

		signData, txsSignInfo, txs, err = p.addToSignData(localWallet, insc.RevealTx, nil,
			0, "reveal", nil, nil, nil, signData, txsSignInfo, txs, true)
		if err != nil {
			return nil, nil, nil, err
		}
		txs = append(txs, insc.RevealTx)
	}
	return signData, txsSignInfo, txs, nil
}

func (p *Manager) addToSignData(localWallet common.Wallet, tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher,
	fee int64, reason string, more, witness, peerPubKey []byte,
	signData []*remoteSignData, txsSignInfo []*wwire.TxSignInfo,
	txs []*wire.MsgTx, notSign bool) ([]*remoteSignData, []*wwire.TxSignInfo, []*wire.MsgTx, error) {

	var sigs [][]byte
	if prevFetcher != nil {
		var err error
		sigs, err = PartialSignTxWithWallet(localWallet, tx, prevFetcher, witness, false, peerPubKey)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	txHex, err := EncodeMsgTx(tx)
	if err != nil {
		return nil, nil, nil, err
	}
	txsSignInfo = append(txsSignInfo, &wwire.TxSignInfo{
		Tx:        txHex,
		L1Tx:      true,
		LocalSigs: sigs,
		Reason:    reason,
		NotSign:   notSign,
		MoreData:  more,
	})
	signData = append(signData, &remoteSignData{
		tx:          tx,
		prevFetcher: prevFetcher,
		fee:         fee,
		notSign:     notSign,
	})
	txs = append(txs, tx)

	return signData, txsSignInfo, txs, nil
}

func (p *Manager) addToSignData_SatsNet(localWallet common.Wallet, tx *swire.MsgTx, prevFetcher stxscript.PrevOutputFetcher,
	fee int64, reason string, witness, peerPubKey []byte,
	signData []*remoteSignData, txsSignInfo []*wwire.TxSignInfo,
	txs []*swire.MsgTx, notSign bool) ([]*remoteSignData, []*wwire.TxSignInfo, []*swire.MsgTx, error) {

	sigs, err := PartialSignTxWithWallet_SatsNet(localWallet, tx, prevFetcher, witness, peerPubKey)
	if err != nil {
		return nil, nil, nil, err
	}
	txHex, err := EncodeMsgTx_SatsNet(tx)
	if err != nil {
		return nil, nil, nil, err
	}
	txsSignInfo = append(txsSignInfo, &wwire.TxSignInfo{
		Tx:        txHex,
		L1Tx:      false,
		LocalSigs: sigs,
		Reason:    reason,
		NotSign:   notSign,
	})
	signData = append(signData, &remoteSignData{
		tx2:          tx,
		prevFetcher2: prevFetcher,
		fee:          fee,
		notSign:      notSign,
	})
	txs = append(txs, tx)

	return signData, txsSignInfo, txs, nil
}

func (p *Manager) reqRemoteSignAndBroadcast(localWallet common.Wallet, witness, peerPubKey []byte,
	reason, channelId string, signData []*remoteSignData, txsSignInfo []*wwire.TxSignInfo,
	txs []*wire.MsgTx, txs2 []*swire.MsgTx, md []byte) (*wire.MsgTx, int64, error) {

	localKey := localWallet.GetPaymentPubKey().SerializeCompressed()
	req := wwire.SignRequest{
		MsgHeader:    wwire.NewMsgHeader(),
		ChannelId:    channelId,
		CommitHeight: -1,
		Reason:       reason,
		MoreData:     md,
		PubKey:       localKey,
		NodeId:       localWallet.GetNodePubKey().SerializeCompressed(),
	}
	msg, err := json.Marshal(req)
	if err != nil {
		return nil, 0, err
	}
	sig, err := localWallet.SignMessageWithIndex(msg, 0)
	if err != nil {
		return nil, 0, err
	}
	peerSig, err := p.serverNode.client.SendSigReq(&req, sig)
	if err != nil {
		return nil, 0, err
	}

	var mainTx *wire.MsgTx
	totalFee := int64(0)
	for i, data := range signData {
		if data.notSign {
			continue
		}
		if data.tx != nil {
			_, err = FinalSignTxWithWallet(localWallet, data.tx, data.prevFetcher, witness, false, peerPubKey, peerSig[i])
			if err != nil {
				return nil, 0, err
			}
			totalFee += data.fee
			if txsSignInfo[i].Reason == "" {
				mainTx = data.tx
			}
		} else {
			_, err = FinalSignTxWithWallet_SatsNet(localWallet, data.tx2, data.prevFetcher2, witness, peerPubKey, peerSig[i])
			if err != nil {
				return nil, 0, err
			}
			totalFee += data.fee
		}
	}
	if mainTx == nil {
		return nil, 0, fmt.Errorf("can't find main tx")
	}

	if err := p.BroadcastTxs_SatsNet(txs2); err != nil {
		return nil, 0, err
	}
	if err := p.BroadcastTxs(txs); err != nil {
		return nil, 0, err
	}

	return mainTx, totalFee, nil
}

func (p *Manager) channelWitness(localWallet common.Wallet, channelId string) ([]byte, []byte, error) {
	if p.serverNode == nil || p.serverNode.Pubkey == nil || p.serverNode.client == nil {
		return nil, nil, fmt.Errorf("server node is not ready")
	}
	localKey := localWallet.GetPaymentPubKey().SerializeCompressed()
	peerPubKey := p.serverNode.Pubkey.SerializeCompressed()
	witness, pkScript, err := GetP2WSHscript(localKey, peerPubKey)
	if err != nil {
		return nil, nil, err
	}
	channelId2, err := GetP2WSHaddressFromScript(pkScript)
	if err != nil {
		return nil, nil, err
	}
	if channelId2 != channelId {
		return nil, nil, fmt.Errorf("invalid channel %s", channelId)
	}
	return witness, peerPubKey, nil
}

func (p *Manager) unlockOpenInscribes(inscribes []*InscribeResv) {
	for _, insc := range inscribes {
		if insc.Status != RS_CLOSED {
			p.utxoLockerL1.UnlockUtxosWithTx(insc.CommitTx)
		}
	}
}

func (p *Manager) closeInscribes(inscribes []*InscribeResv) {
	for _, insc := range inscribes {
		insc.Status = RS_CLOSED
	}
}
