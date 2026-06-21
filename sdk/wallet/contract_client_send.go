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
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func (p *Manager) createContractWithdrawDeAnchorTx(channelAddr string, sendInfo []*SendAssetInfo,
	splicingOutTxID string, assetName *AssetName) (*swire.MsgTx, *stxscript.MultiPrevOutFetcher, error) {
	var totalValue int64
	var totalAmt *Decimal
	for _, item := range sendInfo {
		totalAmt = totalAmt.Add(item.AssetAmt)
		totalValue += item.Value
	}

	return p.CreateDeAnchorTx(channelAddr, splicingOutTxID, assetName, totalAmt, totalValue, nil)
}

func (p *Manager) coSendOrdxWithStub(localWallet common.Wallet, dest string, assetNameStr string, amt int64,
	feeRate int64, stub string, reason, contractURL string, invokeCount int64, memo, static, runtime []byte,
	sendDeAnchorTx, excludeRecentBlock bool) (string, int64, error) {
	return p.coSendOrdxWithStubWithMaxConfirmedInputHeight(localWallet, dest, assetNameStr, amt, feeRate,
		stub, reason, contractURL, invokeCount, memo, static, runtime, sendDeAnchorTx, excludeRecentBlock, 0)
}

func (p *Manager) coSendOrdxWithStubHeight(localWallet common.Wallet, dest string, assetNameStr string, amt int64,
	feeRate int64, stub string, reason, contractURL string, invokeCount int64, memo, static, runtime []byte,
	sendDeAnchorTx, excludeRecentBlock bool, maxConfirmedInputHeight int) (string, int64, error) {
	return p.coSendOrdxWithStubWithMaxConfirmedInputHeight(localWallet, dest, assetNameStr, amt, feeRate,
		stub, reason, contractURL, invokeCount, memo, static, runtime, sendDeAnchorTx, excludeRecentBlock, maxConfirmedInputHeight)
}

func (p *Manager) coSendOrdxWithStubWithMaxConfirmedInputHeight(localWallet common.Wallet, dest string, assetNameStr string, amt int64,
	feeRate int64, stub string, reason, contractURL string, invokeCount int64, memo, static, runtime []byte,
	sendDeAnchorTx, excludeRecentBlock bool, maxConfirmedInputHeight int) (string, int64, error) {

	start := time.Now()
	Log.Infof("CoSendOrdxWithStub %s", assetNameStr)
	if localWallet == nil {
		localWallet = p.wallet
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

	channelID, err := p.GetChannelAddress()
	if err != nil {
		return "", 0, err
	}
	witness, peerPubKey, err := p.ChannelWitness(localWallet, channelID)
	if err != nil {
		return "", 0, err
	}

	tx, prevFetcher, fee, err := p.buildSendOrdxTxWithStubFromAddressWithHeight(localWallet, channelID, dest, assetName, amt, stub, feeRate, memo, true, excludeRecentBlock, maxConfirmedInputHeight)
	if err != nil {
		return "", 0, err
	}

	signData := make([]*RemoteSignData, 0)
	txs := make([]*wire.MsgTx, 0)
	txsSignInfo := make([]*wwire.TxSignInfo, 0)
	signData, txsSignInfo, txs, err = p.AddToSignData(localWallet, tx, prevFetcher, fee, "", nil, witness, peerPubKey, signData, txsSignInfo, txs, false)
	if err != nil {
		return "", 0, err
	}

	txs2 := make([]*swire.MsgTx, 0)
	if sendDeAnchorTx {
		sendInfo := &SendAssetInfo{
			Address:   dest,
			Value:     0,
			AssetName: asset,
			AssetAmt:  indexer.NewDefaultDecimal(amt),
		}
		tx2, prevFetcher2, err := p.createContractWithdrawDeAnchorTx(channelID, []*SendAssetInfo{sendInfo}, tx.TxID(), assetName)
		if err != nil {
			return "", 0, err
		}
		signData, txsSignInfo, txs2, err = p.AddToSignDataSatsNet(localWallet, tx2, prevFetcher2, 0, "descend", witness, peerPubKey, signData, txsSignInfo, txs2, false)
		if err != nil {
			return "", 0, err
		}
	}

	moredata := wwire.RemoteSignMoreData_Contract{
		Tx:                txsSignInfo,
		Witness:           witness,
		ContractURL:       contractURL,
		InvokeCount:       invokeCount,
		StaticMerkleRoot:  static,
		RuntimeMerkleRoot: runtime,
		MoreData:          memo,
	}
	md, err := json.Marshal(moredata)
	if err != nil {
		return "", 0, err
	}

	tx, totalFee, err := p.ReqRemoteSignAndBroadcast(localWallet, witness, peerPubKey, reason, channelID, signData, txsSignInfo, txs, txs2, md)
	if err != nil {
		return "", 0, err
	}

	Log.Infof("CoSendOrdxWithStub %s finished, %v", tx.TxID(), time.Since(start))
	return tx.TxID(), totalFee, nil
}

func (p *Manager) coBatchSendV3(localWallet common.Wallet, dest []*SendAssetInfo, assetNameStr string, feeRate int64,
	reason, contractURL string, invokeCount int64, memo, static, runtime []byte,
	sendDeAnchorTx, excludeRecentBlock, payFeeByCurrentAddress bool) (string, int64, error) {
	return p.coBatchSendV3WithMaxConfirmedInputHeight(localWallet, dest, assetNameStr, feeRate,
		reason, contractURL, invokeCount, memo, static, runtime,
		sendDeAnchorTx, excludeRecentBlock, payFeeByCurrentAddress, 0)
}

func (p *Manager) coBatchSendV3Height(localWallet common.Wallet, dest []*SendAssetInfo, assetNameStr string, feeRate int64,
	reason, contractURL string, invokeCount int64, memo, static, runtime []byte,
	sendDeAnchorTx, excludeRecentBlock, payFeeByCurrentAddress bool, maxConfirmedInputHeight int) (string, int64, error) {
	return p.coBatchSendV3WithMaxConfirmedInputHeight(localWallet, dest, assetNameStr, feeRate,
		reason, contractURL, invokeCount, memo, static, runtime,
		sendDeAnchorTx, excludeRecentBlock, payFeeByCurrentAddress, maxConfirmedInputHeight)
}

func (p *Manager) coBatchSendV3WithMaxConfirmedInputHeight(localWallet common.Wallet, dest []*SendAssetInfo, assetNameStr string, feeRate int64,
	reason, contractURL string, invokeCount int64, memo, static, runtime []byte,
	sendDeAnchorTx, excludeRecentBlock, payFeeByCurrentAddress bool, maxConfirmedInputHeight int) (string, int64, error) {

	start := time.Now()
	Log.Infof("CoBatchSendV3 %s", assetNameStr)
	if localWallet == nil {
		localWallet = p.wallet
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
	channelID := ExtractChannelId(contractURL)
	if channelID == "" {
		return "", 0, fmt.Errorf("invalid channel address")
	}

	var (
		tx          *wire.MsgTx
		prevFetcher *txscript.MultiPrevOutFetcher
		fee         int64
		inscribes   []*InscribeResv
		err         error
	)
	switch asset.Protocol {
	case "":
		tx, prevFetcher, fee, err = p.buildBatchSendTxWithAddressHeight_btc(channelID, dest, feeRate, memo, true, excludeRecentBlock, maxConfirmedInputHeight)
	case indexer.PROTOCOL_NAME_ORDX:
		tx, prevFetcher, fee, err = p.buildBatchSendTxWithAddressHeight_ordx(localWallet, channelID, dest, assetName, feeRate, memo, true, excludeRecentBlock, payFeeByCurrentAddress, maxConfirmedInputHeight)
	case indexer.PROTOCOL_NAME_RUNES:
		tx, prevFetcher, fee, err = p.buildBatchSendTxWithAddressHeight_runes(localWallet, channelID, dest, assetName, feeRate, true, excludeRecentBlock, payFeeByCurrentAddress, maxConfirmedInputHeight)
	case indexer.PROTOCOL_NAME_BRC20:
		tx, prevFetcher, fee, inscribes, err = p.buildBatchSendTxWithAddressHeight_brc20(localWallet, channelID, dest, assetName, feeRate, memo, true, excludeRecentBlock, payFeeByCurrentAddress, maxConfirmedInputHeight)
	default:
		return "", 0, fmt.Errorf("CoBatchSendV3 unsupport protocol %s", asset.Protocol)
	}
	if err != nil {
		return "", 0, err
	}
	defer p.unlockOpenInscribes(inscribes)

	witness, peerPubKey, err := p.ChannelWitness(localWallet, channelID)
	if err != nil {
		return "", 0, err
	}

	signData, txsSignInfo, txs, err := p.GenerateSignData(localWallet, inscribes, dest, witness, peerPubKey, payFeeByCurrentAddress)
	if err != nil {
		return "", 0, err
	}
	signData, txsSignInfo, txs, err = p.AddToSignData(localWallet, tx, prevFetcher, fee, "", nil, witness, peerPubKey, signData, txsSignInfo, txs, false)
	if err != nil {
		return "", 0, err
	}

	txs2 := make([]*swire.MsgTx, 0)
	if sendDeAnchorTx {
		tx2, prevFetcher2, err := p.createContractWithdrawDeAnchorTx(channelID, dest, tx.TxID(), assetName)
		if err != nil {
			return "", 0, err
		}
		signData, txsSignInfo, txs2, err = p.AddToSignDataSatsNet(localWallet, tx2, prevFetcher2, 0, "descend", witness, peerPubKey, signData, txsSignInfo, txs2, payFeeByCurrentAddress)
		if err != nil {
			return "", 0, err
		}
	}

	moredata := wwire.RemoteSignMoreData_Contract{
		Tx:                txsSignInfo,
		Witness:           witness,
		ContractURL:       contractURL,
		InvokeCount:       invokeCount,
		StaticMerkleRoot:  static,
		RuntimeMerkleRoot: runtime,
		MoreData:          memo,
	}
	md, err := json.Marshal(moredata)
	if err != nil {
		return "", 0, err
	}

	tx, totalFee, err := p.ReqRemoteSignAndBroadcast(localWallet, witness, peerPubKey, reason, channelID, signData, txsSignInfo, txs, txs2, md)
	if err != nil {
		return "", 0, err
	}
	p.closeInscribes(inscribes)

	Log.Infof("CoBatchSendV3 %s finished, %v", tx.TxID(), time.Since(start))
	return tx.TxID(), totalFee, nil
}

func (p *Manager) coGenerateStubUtxos(localWallet common.Wallet, n int, feeRate int64, contractURL string, invokeCount int64,
	excludeRecentBlock bool) (string, int64, error) {

	start := time.Now()
	Log.Infof("CoGenerateStubUtxos %d", n)
	if localWallet == nil {
		localWallet = p.wallet
	}
	channelID, err := p.GetChannelAddress()
	if err != nil {
		return "", 0, err
	}
	witness, peerPubKey, err := p.ChannelWitness(localWallet, channelID)
	if err != nil {
		return "", 0, err
	}
	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}

	dest := &SendAssetInfo{
		Address:   channelID,
		Value:     330,
		AssetName: &indexer.ASSET_PLAIN_SAT,
	}
	dests := make([]*SendAssetInfo, 0, n)
	for range n {
		dests = append(dests, dest)
	}

	tx, prevFetcher, fee, err := p.BuildBatchSendTxV3BTCFromAddress(channelID, dests, feeRate, nil, true, excludeRecentBlock)
	if err != nil {
		return "", 0, err
	}

	signData := make([]*RemoteSignData, 0)
	txs := make([]*wire.MsgTx, 0)
	txsSignInfo := make([]*wwire.TxSignInfo, 0)
	signData, txsSignInfo, txs, err = p.AddToSignData(localWallet, tx, prevFetcher, fee, "", nil, witness, peerPubKey, signData, txsSignInfo, txs, false)
	if err != nil {
		return "", 0, err
	}

	moredata := wwire.RemoteSignMoreData_Contract{
		Tx:          txsSignInfo,
		Witness:     witness,
		ContractURL: contractURL,
		InvokeCount: invokeCount,
		MoreData:    []byte(fmt.Sprintf("%d", fee)),
	}
	md, err := json.Marshal(moredata)
	if err != nil {
		return "", 0, err
	}

	tx, totalFee, err := p.ReqRemoteSignAndBroadcast(localWallet, witness, peerPubKey, "stub", channelID, signData, txsSignInfo, txs, nil, md)
	if err != nil {
		return "", 0, err
	}

	PrintJsonTx(tx, "CoGenerateStubUtxos TX")
	Log.Infof("CoGenerateStubUtxos %s finished, %v", tx.TxID(), time.Since(start))
	return tx.TxID(), totalFee, nil
}
