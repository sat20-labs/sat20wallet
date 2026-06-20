package wallet

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

func (p *Manager) GetBootstrapNodeId() *btcec.PublicKey {
	nodes := p.GetBootstrapNodes()
	if len(nodes) == 0 {
		return nil
	}
	return nodes[0].NodeId
}

func (p *Manager) GetBootstrapNodePaymentPubKey() *btcec.PublicKey {
	nodes := p.GetBootstrapNodes()
	if len(nodes) == 0 {
		return nil
	}
	return nodes[0].Pubkey
}

func (p *Manager) GetBootstrapWalletAddress() string {
	pubkey := p.GetBootstrapNodePaymentPubKey()
	if pubkey == nil {
		return ""
	}
	return PublicKeyToP2TRAddress(pubkey)
}

func (p *Manager) GetChannelWithBootstrapNode() *Channel {
	addr := p.GetBootstrapWalletAddress()
	if addr == "" {
		return nil
	}
	return p.getChannelByPeerWallet(addr)
}

func (p *Manager) GetDAOPkScript(channel *Channel) []byte {
	if channel == nil {
		return nil
	}
	var err error
	var toDAOPkScript []byte
	bootstrapPubKey := p.GetBootstrapNodePaymentPubKey()
	if bootstrapPubKey == nil {
		return nil
	}
	if channel.IsInitiator {
		if channel.RemoteChanCfg.PaymentKey.IsEqual(bootstrapPubKey) {
			toDAOPkScript, err = GetP2TRpkScript(bootstrapPubKey)
		} else {
			_, toDAOPkScript, err = GetP2WSHscript(channel.RemoteChanCfg.PaymentKey.SerializeCompressed(),
				bootstrapPubKey.SerializeCompressed())
		}
	} else {
		if channel.LocalChanCfg.PaymentKey.IsEqual(bootstrapPubKey) {
			toDAOPkScript, err = GetP2TRpkScript(bootstrapPubKey)
		} else {
			_, toDAOPkScript, err = GetP2WSHscript(channel.LocalChanCfg.PaymentKey.SerializeCompressed(),
				bootstrapPubKey.SerializeCompressed())
		}
	}
	if err != nil {
		Log.Errorf("GetP2WSHscript failed. %v", err)
		return nil
	}
	return toDAOPkScript
}

func (p *Manager) GetDAOPkScriptByCoreNode(coreNodePubKey []byte) ([]byte, error) {
	bootstrapPubKey := p.GetBootstrapNodePaymentPubKey()
	if bootstrapPubKey == nil {
		return nil, fmt.Errorf("bootstrap node is not configured")
	}
	_, toDAOPkScript, err := GetP2WSHscript(coreNodePubKey, bootstrapPubKey.SerializeCompressed())
	return toDAOPkScript, err
}

func expectedOpeningAnchorOutputValue(channel *Channel) int64 {
	if channel == nil || channel.FeeCfg == nil {
		return 0
	}
	return channel.LocalChanCfg.InitialBalance +
		channel.RemoteChanCfg.InitialBalance +
		channel.FeeCfg.MortgageFee
}

func isPlainSatsNetOutput(output *TxOutput_SatsNet) bool {
	return output != nil && output.OutValue.Value > 0 && len(output.OutValue.Assets) == 0
}

func (p *Manager) addExistingOpeningAnchorOutput(resv *FundingReservation) error {
	if resv == nil || resv.Channel == nil {
		return fmt.Errorf("invalid funding reservation")
	}

	target := expectedOpeningAnchorOutputValue(resv.Channel)
	if target <= 0 {
		return fmt.Errorf("invalid opening anchor output value %d", target)
	}

	channelId := resv.Channel.ChannelId
	if channelId == "" {
		channelId = resv.Channel.Address
	}
	ledger, err := p.l2IndexerClient.GetChannelLedger(channelId)
	if err != nil {
		return fmt.Errorf("GetChannelLedger %s failed: %v", channelId, err)
	}
	sort.Slice(ledger, func(i, j int) bool {
		if ledger[i] == nil || ledger[j] == nil {
			return ledger[j] != nil
		}
		if ledger[i].L2Height != ledger[j].L2Height {
			return ledger[i].L2Height < ledger[j].L2Height
		}
		return ledger[i].L2TxId < ledger[j].L2TxId
	})

	channelPkScript := resv.Channel.GetChannelPkScript()
	for _, entry := range ledger {
		if entry == nil ||
			entry.Direction != "ascending" ||
			entry.L2TxId == "" ||
			entry.Value != target ||
			len(entry.Assets) != 0 {
			continue
		}

		outpoint := fmt.Sprintf("%s:0", entry.L2TxId)
		output, err := p.l2IndexerClient.GetTxOutput(outpoint)
		if err != nil {
			Log.Warnf("GetTxOutput %s failed when rebuilding channel: %v", outpoint, err)
			continue
		}
		outputSatsNet := OutputToSatsNet(output)
		if !isPlainSatsNetOutput(outputSatsNet) ||
			outputSatsNet.Value() != target ||
			!bytes.Equal(outputSatsNet.OutValue.PkScript, channelPkScript) {
			Log.Warnf("skip invalid opening anchor candidate %s for channel %s", outpoint, channelId)
			continue
		}
		resv.Channel.AddUtxo_SatsNet(outputSatsNet)
		Log.Infof("reuse L2 opening anchor output %s for channel %s, value %d", outpoint, channelId, target)
		return nil
	}

	return fmt.Errorf("can't find existing L2 opening anchor output for channel %s value %d", channelId, target)
}

func (p *Manager) AddExistingOpeningAnchorOutput(resv *FundingReservation) error {
	return p.addExistingOpeningAnchorOutput(resv)
}

func (p *Manager) SendFundingBroadcastedReq(resv *FundingReservation) {
	if resv == nil || resv.FundingBroadcasted == nil || p.serverNode == nil || p.serverNode.client == nil {
		return
	}
	client, ok := p.serverNode.client.(NodeRPCClient)
	if !ok {
		Log.Warnf("SendFundingBroadcastedReq skipped: server node client does not support reservation helper")
		return
	}
	if err := client.SendFundingBroadcastedReq(resv); err != nil {
		Log.Warnf("SendFundingBroadcastedReq %d failed after funding tx was broadcasted. Keep reservation pending for retry. %v", resv.Id, err)
		return
	}
	resv.FundingBroadcasted = nil
}

func (p *Manager) SendClosingBroadcastedReq(resv *ClosingReservation) {
	if resv == nil || resv.Channel == nil || resv.ClosingBroadcasted == nil {
		return
	}
	client, ok := p.GetPeerNodeClient(&resv.Channel.ChannelInDB).(NodeRPCClient)
	if !ok {
		Log.Warnf("SendClosingBroadcastedReq skipped: peer node client does not support reservation helper")
		return
	}
	if err := client.SendClosingBroadcastedReq(resv); err != nil {
		Log.Warnf("SendClosingBroadcastedReq %d failed after deanchor tx was broadcasted. Keep reservation pending for retry. %v", resv.Id, err)
		return
	}
	resv.ClosingBroadcasted = nil
}

func CreateFundingTx2(utxos []*TxOutput, pubKeyA, pubKeyB []byte,
	amount int64, feeRate int64, changePkScript, DAOPkScript []byte, feeCfg *ChannelFeeConfig) (
	*wire.MsgTx, []byte, txscript.PrevOutputFetcher, error) {

	fundingTx := wire.NewMsgTx(wire.TxVersion)
	var weightEstimate utils.TxWeightEstimator
	if feeCfg == nil {
		feeCfg = NewFeeConfig()
	}
	feeToDAO := feeCfg.FeeToDAO()
	if feeToDAO < 0 {
		return nil, nil, nil, fmt.Errorf("invalid fee config: fee to dao is %d", feeToDAO)
	}

	redeemScript, pkScript, err := GetP2WSHscript(pubKeyA, pubKeyB)
	if err != nil {
		Log.Errorf("GetP2WSHScript failed. %v", err)
		return nil, nil, nil, err
	}

	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)

	inputValue := int64(0)
	for _, info := range utxos {
		inputValue += info.Value()
		txIn := info.TxIn()
		fundingTx.AddTxIn(txIn)
		weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
		prevFetcher.AddPrevOut(*info.OutPoint(), &info.OutValue)
	}

	weightEstimate.AddP2WSHOutput()
	if feeToDAO > 0 {
		weightEstimate.AddP2WSHOutput()
	}
	requiredFee := weightEstimate.Fee(feeRate)

	weightEstimate.AddP2TROutput()
	requiredFee1 := weightEstimate.Fee(feeRate)

	if inputValue < amount+requiredFee {
		return nil, nil, nil, fmt.Errorf("no enough sats. required %d but only %d", amount+requiredFee, inputValue)
	}
	value1 := amount - feeToDAO
	if value1 < 0 {
		return nil, nil, nil, fmt.Errorf("funding amount %d is smaller than fee to dao %d", amount, feeToDAO)
	}
	value2 := feeToDAO

	feechange := inputValue - (value1 + value2) - requiredFee1
	if feechange < 330 {
		feechange = 0
	}

	fundingTx.AddTxOut(wire.NewTxOut(value1, pkScript))
	if value2 > 0 {
		fundingTx.AddTxOut(wire.NewTxOut(value2, DAOPkScript))
	}
	if feechange >= 330 {
		fundingTx.AddTxOut(wire.NewTxOut(feechange, changePkScript))
	}

	PrintJsonTx(fundingTx, "funding")

	return fundingTx, redeemScript, prevFetcher, nil
}

func (p *Manager) channelLedgerHasHistory(channel string) (bool, error) {
	ledger, err := p.l2IndexerClient.GetChannelLedger(channel)
	if err != nil {
		return false, err
	}
	return channelLedgerHasHistory(channel, ledger), nil
}

func (p *Manager) ChannelLedgerHasHistory(channel string) (bool, error) {
	return p.channelLedgerHasHistory(channel)
}

func (p *Manager) AllowOpen(feeRate, amt int64, outpoints []string, feeCfg *ChannelFeeConfig) ([]*TxOutput, error) {
	if feeCfg == nil {
		feeCfg = NewFeeConfig()
	}
	feeToDAO := feeCfg.FeeToDAO()
	if feeToDAO < 0 {
		return nil, fmt.Errorf("invalid fee config: fee to dao is %d", feeToDAO)
	}

	minCap := feeCfg.MinCapacity()
	if amt < minCap {
		return nil, fmt.Errorf("channel capacity must larger than %d", minCap)
	}

	err := p.checkUtxos(outpoints, nil)
	if err != nil {
		return nil, err
	}

	var weightEstimate utils.TxWeightEstimator

	infos := make([]*TxOutput, 0, len(outpoints))
	value := int64(0)
	for _, utxo := range outpoints {
		info, err := p.l1IndexerClient.GetTxOutput(utxo)
		if err != nil {
			return nil, err
		}
		if info.HasAsset() {
			return nil, fmt.Errorf("has assets in utxo %s", utxo)
		}
		AlignAsset(info, &indexer.ASSET_PLAIN_SAT)

		value += info.OutValue.Value
		infos = append(infos, info)
		weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
	}

	weightEstimate.AddP2WSHOutput()
	if feeToDAO > 0 {
		weightEstimate.AddP2WSHOutput()
	}
	weightEstimate.AddP2TROutput()
	requiredFee := weightEstimate.Fee(feeRate)
	if value < amt+requiredFee {
		return nil, fmt.Errorf("value of input utxos too small")
	}

	return infos, nil
}

func hasSameUtxo(utxos []string) bool {
	utxoMap := make(map[string]bool)
	for _, u := range utxos {
		if utxoMap[u] {
			return true
		}
		utxoMap[u] = true
	}
	return false
}

func (p *Manager) checkUtxos(utxos []string, channel *Channel) error {
	if hasSameUtxo(utxos) {
		return fmt.Errorf("same utxo exists")
	}

	if channel != nil {
		incontrol := channel.InControl(utxos)
		if len(incontrol) > 0 {
			return fmt.Errorf("utxo has been in channel")
		}
	}

	if u := p.GetUtxoLocker().CheckUtxos(utxos); u != "" {
		return fmt.Errorf("utxo %s has been locked", u)
	}

	result, err := p.l1IndexerClient.GetExistingUtxos(utxos)
	if err != nil {
		return err
	}
	if len(utxos) != len(result) {
		return fmt.Errorf("some utxo has spent")
	}
	return nil
}
