package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
)

const (
	SPLICING_REASON_LOCAL    string = "local"
	SPLICING_REASON_REMOTE   string = "remote"
	SPLICING_REASON_WITHDRAW string = "withdraw"
	SPLICING_REASON_STAKE    string = "stake"
	SPLICING_REASON_UNSTAKE  string = "unstake"
	SPLICING_REASON_CLOSE    string = "close"
	SPLICING_REASON_CONTRACT string = "contract"
)

func (p *Manager) OpenChannel(feeRate int64, amt int64, utxos []string, memo string) (string, error) {
	start := time.Now()
	Log.Infof("OpenChannel %d", amt)
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}
	channelAddr, err := p.GetChannelAddress()
	if err != nil {
		return "", err
	}
	if err := p.rejectUnfinishedChannelLifecycle(channelAddr); err != nil {
		return "", err
	}
	if !p.IsReady() {
		return "", fmt.Errorf("not ready")
	}
	if !p.CheckSuperNodeStatus() {
		return "", fmt.Errorf("peer is offline")
	}
	if p.ServerIsBootstrapNode() {
		pubkey := p.wallet.GetPaymentPubKey().SerializeCompressed()
		if !p.HasStaked(pubkey) {
			addr, _ := indexer.GetCoreNodeChannelAddress(pubkey, GetChainParam())
			return "", fmt.Errorf("channel address %s has no stake assets", addr)
		}
	}

	l2DrainTxId, err := p.DrainChannelL2BeforeOpenIfNeeded(channelAddr)
	if err != nil {
		return "", err
	}

	result, err := p.FunderInitFundingProcess(feeRate, amt, utxos, memo, l2DrainTxId)
	Log.Infof("OpenChannel finished: %s, %v", result, time.Since(start))
	return result, err
}

func (p *Manager) CloseChannel(channelId string, feeRate int64, force bool) (string, string, error) {
	start := time.Now()
	Log.Infof("CloseChannel %s", channelId)
	if p.wallet == nil {
		return "", "", fmt.Errorf("wallet is not created/unlocked")
	}
	if !p.IsReady() {
		return "", "", fmt.Errorf("not ready")
	}
	if force {
		return p.CloserForcelyClose(channelId, feeRate)
	}
	if err := p.rejectUnfinishedChannelLifecycle(channelId); err != nil {
		return "", "", err
	}

	tx1, tx2, err := p.CloserInitCoopCloseProcess(channelId, feeRate)
	Log.Infof("CloseChannel finished: %v, %v", err, time.Since(start))
	return tx1, tx2, err
}

func (p *Manager) UnlockFromChannel(channelId string, destAddr string, assetName string, amt string,
	feeUtxos []string, memo []byte) (string, int64, error) {
	if destAddr == "" {
		destAddr = p.wallet.GetAddress()
	}
	return p.UnlockFromChannelV2(channelId, destAddr, assetName, amt, feeUtxos, memo, "", nil)
}

func (p *Manager) UnlockFromChannelV2(channelId string, destAddr string, assetName string, amt string,
	feeUtxos []string, memo []byte, reason string, more []byte) (string, int64, error) {
	return p.BatchUnlockFromChannelV2(channelId, []string{destAddr}, assetName, []string{amt}, feeUtxos, memo, reason, more)
}

func (p *Manager) BatchUnlockFromChannel(channelId string, destAddr []string, assetName string, amt string,
	feeUtxos []string, memo []byte, reason string, more []byte) (string, int64, error) {
	var destAmt []string
	for range destAddr {
		destAmt = append(destAmt, amt)
	}
	return p.BatchUnlockFromChannelV2(channelId, destAddr, assetName, destAmt, feeUtxos, memo, reason, more)
}

func (p *Manager) BatchUnlockFromChannelV2(channelId string, destAddr []string, assetName string, amtVect []string,
	feeUtxos []string, memo []byte, reason string, moreData []byte) (string, int64, error) {
	start := time.Now()
	Log.Infof("BatchUnlockFromChannelV2 %s", assetName)
	if p.wallet == nil {
		return "", 0, fmt.Errorf("wallet is not created/unlocked")
	}
	if !p.IsReady() {
		return "", 0, fmt.Errorf("not ready")
	}
	if len(destAddr) != len(amtVect) {
		return "", 0, fmt.Errorf("the length of address and amount should be equal")
	}

	channel := p.GetChannel(channelId)
	if channel == nil {
		return "", 0, fmt.Errorf("can't find channel %s", channelId)
	}
	if channel.Status != CS_READY {
		return "", 0, fmt.Errorf("channel is not ready")
	}
	if !channel.IsInitiator {
		return "", 0, fmt.Errorf("can't perform this action from remote peer")
	}
	if !p.IsPeerOnline(channel.PeerNodeId) {
		return "", 0, fmt.Errorf("peer is offline")
	}

	// Own the request material before querying. The original synchronous
	// lock/RPC scope below retains one evidence window for the whole unlock.
	destAddr = append([]string(nil), destAddr...)
	amtVect = append([]string(nil), amtVect...)
	feeUtxos = append([]string(nil), feeUtxos...)
	memo = append([]byte(nil), memo...)
	moreData = append([]byte(nil), moreData...)
	rgbRequest, err := json.Marshal(struct {
		Channel, Asset, Reason      string
		Destinations, Amounts, Fees []string
		Memo, MoreData              []byte
	}{channelId, assetName, reason, destAddr, amtVect, feeUtxos, memo, moreData})
	if err != nil {
		return "", 0, err
	}
	rgbEvidence, err := p.PrepareSTPRGBSplicingEvidence(channel, ParseAssetString(assetName), nil, nil, rgbRequest, nil)
	if err != nil {
		return "", 0, err
	}

	channel.Mutex.Lock()
	defer channel.Mutex.Unlock()
	if err := rgbEvidence.ValidateForChannel(channel, rgbRequest); err != nil {
		return "", 0, err
	}
	if channel.Status != CS_READY {
		return "", 0, fmt.Errorf("channel %s is not ready", channelId)
	}

	asset := ParseAssetString(assetName)
	if asset == nil {
		return "", 0, fmt.Errorf("invalid asset name %s", assetName)
	}
	if err := ValidateSTPAsset(asset, STPOpUnlock); err != nil {
		return "", 0, err
	}
	tickerInfo := p.getTickerInfo(asset)
	if tickerInfo == nil {
		return "", 0, fmt.Errorf("can't get ticker %s info", assetName)
	}
	var destAmt []*Decimal
	var totalAmt *Decimal
	for _, amt := range amtVect {
		dAmt, err := indexer.NewDecimalFromString(amt, tickerInfo.Divisibility)
		if err != nil {
			return "", 0, err
		}
		if dAmt.Sign() <= 0 {
			return "", 0, fmt.Errorf("invalid amount %s", amt)
		}
		destAmt = append(destAmt, dAmt)
		totalAmt = totalAmt.Add(dAmt)
	}

	oldChannel, err := p.LoadChannel(channelId)
	if err != nil {
		return "", 0, err
	}

	if len(feeUtxos) == 0 {
		feeUtxos, err = p.GetUtxosForFee_SatsNet("", 0, nil)
		if err != nil {
			if !indexer.IsPlainAsset(asset) {
				return "", -1, err
			}
			localSats := channel.GetAvalaiblePlainSats()
			if localSats == totalAmt.Int64() {
				if len(destAmt) != 1 {
					return "", -1, err
				}
				destAmt[0] = destAmt[0].Sub(indexer.NewDefaultDecimal(DEFAULT_FEE_SATSNET))
				if destAmt[0].Sign() <= 0 {
					return "", -1, err
				}
			}
		}
	}

	resv := &PaymentReservation{
		PaymentDataInDB: PaymentDataInDB{
			ReservationBase: NewReservationBase(0, true, RS_INIT, channel.LocalWallet()),
			ChannelId:       channel.ChannelId,
			IsUnlock:        true,
			NeedSendLockTx:  true,
			AssetName:       GetAssetName(tickerInfo),
			Memo:            memo,
			DestAmt:         destAmt,
			DestAddr:        destAddr,
			Reason:          reason,
			MoreData:        moreData,
		},
		RevocationInfo: RevocationInfo{
			RGBEvidence: rgbEvidence,
			OldChannel:  oldChannel,
			Channel:     channel,
			FeeRate:     p.GetFeeRate(),
		},
		TickerInfo: tickerInfo,
	}
	resv.InitRuntime()

	txid, err := p.InitUnlockProcess(resv, feeUtxos)
	Log.Infof("BatchUnlockFromChannelV2 %s finished, %v", txid, time.Since(start))
	return txid, resv.Id, err
}

func (p *Manager) LockToChannel(channelId string, assetName string, amt string,
	utxos, fees []string, memo []byte) (string, int64, error) {
	start := time.Now()
	Log.Infof("LockToChannel %s %s", assetName, amt)
	if p.wallet == nil {
		return "", 0, fmt.Errorf("wallet is not created/unlocked")
	}
	if !p.IsReady() {
		return "", 0, fmt.Errorf("not ready")
	}

	channel := p.GetChannel(channelId)
	if channel == nil {
		return "", 0, fmt.Errorf("can't find channel %s", channelId)
	}
	if channel.Status != CS_READY {
		return "", 0, fmt.Errorf("channel is not ready")
	}
	if !channel.IsInitiator {
		return "", 0, fmt.Errorf("can't perform this action from remote peer")
	}
	if !p.IsPeerOnline(channel.PeerNodeId) {
		return "", 0, fmt.Errorf("peer is offline")
	}
	// Bind the synchronous lock operation to its request-owned RGB facts.
	// Existing L2 input selection and peer exchange stay in the original lock.
	utxos = append([]string(nil), utxos...)
	fees = append([]string(nil), fees...)
	memo = append([]byte(nil), memo...)
	rgbRequest, err := json.Marshal(struct {
		Channel, Asset, Amount string
		Inputs, Fees           []string
		Memo                   []byte
	}{channelId, assetName, amt, utxos, fees, memo})
	if err != nil {
		return "", 0, err
	}
	rgbEvidence, err := p.PrepareSTPRGBSplicingEvidence(channel, ParseAssetString(assetName), nil, nil, rgbRequest, nil)
	if err != nil {
		return "", 0, err
	}

	channel.Mutex.Lock()
	defer channel.Mutex.Unlock()
	if err := rgbEvidence.ValidateForChannel(channel, rgbRequest); err != nil {
		return "", 0, err
	}
	if channel.Status != CS_READY {
		return "", 0, fmt.Errorf("channel %s is not ready", channelId)
	}

	asset := ParseAssetString(assetName)
	if asset == nil {
		return "", 0, fmt.Errorf("invalid asset name %s", assetName)
	}
	if err := ValidateSTPAsset(asset, STPOpLock); err != nil {
		return "", 0, err
	}
	tickerInfo := p.getTickerInfo(asset)
	if tickerInfo == nil {
		return "", 0, fmt.Errorf("can't get ticker %s info", assetName)
	}
	dAmt, err := indexer.NewDecimalFromString(amt, tickerInfo.Divisibility)
	if err != nil {
		return "", 0, err
	}
	if dAmt.Sign() < 0 {
		return "", 0, fmt.Errorf("invalid amt")
	}

	oldChannel, err := p.LoadChannel(channelId)
	if err != nil {
		return "", 0, err
	}
	if len(utxos) == 0 && len(fees) == 0 {
		utxos, fees, err = p.GetUtxosWithAssetV2_SatsNet("", DEFAULT_FEE_SATSNET, dAmt, asset, nil)
		if err != nil {
			return "", -1, err
		}
	} else if len(utxos) == 0 {
		utxos, err = p.GetUtxosWithAsset_SatsNet("", dAmt, asset, nil)
		if err != nil {
			return "", -1, err
		}
	} else if len(fees) == 0 {
		fees, err = p.GetUtxosForFee_SatsNet("", DEFAULT_FEE_SATSNET, nil)
		if err != nil {
			return "", -1, err
		}
	}

	var revealPrivKey []byte
	if channel.NeedsSTPRevealKey() {
		priv, err := btcec.NewPrivateKey()
		if err != nil {
			return "", -1, err
		}
		revealPrivKey = priv.Serialize()
	}

	resv := &PaymentReservation{
		PaymentDataInDB: PaymentDataInDB{
			ReservationBase: NewReservationBase(0, true, RS_INIT, channel.LocalWallet()),
			ChannelId:       channel.ChannelId,
			IsUnlock:        false,
			NeedSendLockTx:  true,
			AssetName:       GetAssetName(tickerInfo),
			Memo:            memo,
			Amt:             dAmt,
		},
		RevocationInfo: RevocationInfo{
			RGBEvidence:   rgbEvidence,
			OldChannel:    oldChannel,
			Channel:       channel,
			FeeRate:       p.GetFeeRate(),
			RevealPrivKey: revealPrivKey,
		},
		TickerInfo: tickerInfo,
	}
	resv.InitRuntime()

	txId, err := p.InitLockProcess(resv, utxos, fees)
	Log.Infof("LockToChannel %s finished, %v", txId, time.Since(start))
	return txId, resv.Id, err
}

func (p *Manager) LockToChannelWithExpand(channelId string, assetName string, amt string, feeRate int64) (string, int64, error) {
	asset := ParseAssetString(assetName)
	if asset == nil {
		return "", 0, fmt.Errorf("invalid asset name %s", assetName)
	}
	if err := ValidateSTPAsset(asset, STPOpLock); err != nil {
		return "", 0, err
	}
	tickerInfo := p.getTickerInfo(asset)
	if tickerInfo == nil {
		return "", 0, fmt.Errorf("can't get ticker %s info", assetName)
	}
	name := GetAssetName(tickerInfo)
	dAmt, err := indexer.NewDecimalFromString(amt, tickerInfo.Divisibility)
	if err != nil {
		return "", 0, err
	}
	if dAmt.Sign() < 0 {
		return "", 0, fmt.Errorf("invalid amt")
	}

	return p.PerformLocalAction(LOCAL_ACTION_LOCK_WITH_EXPAND, &LocalActionParam_Expand{
		ChannelId: channelId,
		AssetName: name,
		Amt:       dAmt,
	}, feeRate)
}

func (p *Manager) SplicingOut(channelId string, destAddr string,
	assetName string, amt string,
	fees []string, preTxInputs []string, revealKey []byte,
	feeRate int64, reason string, moreData []byte) (string, int64, error) {
	start := time.Now()
	Log.Infof("SplicingOut %s %s", assetName, amt)
	if p.wallet == nil {
		return "", 0, fmt.Errorf("wallet is not created/unlocked")
	}
	if !p.IsReady() {
		return "", 0, fmt.Errorf("not ready")
	}

	channel := p.GetChannel(channelId)
	if channel == nil {
		return "", 0, fmt.Errorf("can't find channel %s", channelId)
	}
	if channel.Status != CS_READY {
		return "", 0, fmt.Errorf("channel is not ready")
	}
	if !p.IsPeerOnline(channel.PeerNodeId) {
		return "", 0, fmt.Errorf("peer is offline")
	}

	// Observe RGB dependencies before the unchanged synchronous business lock.
	rgbRequest, err := json.Marshal(struct {
		Channel, Destination, Asset, Amount, Reason string
		Fees, Previous                              []string
		Reveal, More                                []byte
		FeeRate                                     int64
	}{channelId, destAddr, assetName, amt, reason, fees, preTxInputs, revealKey, moreData, feeRate})
	if err != nil {
		return "", 0, err
	}
	rgbEvidence, err := p.PrepareSTPRGBSplicingEvidence(channel, ParseAssetString(assetName), nil, nil, rgbRequest, nil)
	if err != nil {
		return "", 0, err
	}

	channel.Mutex.Lock()
	defer channel.Mutex.Unlock()
	if err := rgbEvidence.ValidateForChannel(channel, rgbRequest); err != nil {
		return "", 0, err
	}

	asset := ParseAssetString(assetName)
	if asset == nil {
		return "", 0, fmt.Errorf("invalid asset name %s", assetName)
	}
	if err := ValidateSTPAsset(asset, STPOpSpliceOut); err != nil {
		return "", 0, err
	}
	tickerInfo := p.getTickerInfo(asset)
	if tickerInfo == nil {
		return "", 0, fmt.Errorf("can't get ticker %s info", assetName)
	}
	name := GetAssetName(tickerInfo)
	dAmt, err := indexer.NewDecimalFromString(amt, tickerInfo.Divisibility)
	if err != nil {
		return "", 0, err
	}
	if dAmt.Sign() < 0 {
		return "", 0, fmt.Errorf("invalid amt")
	}
	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}

	var address string
	if reason == SPLICING_REASON_REMOTE {
		address = channel.GetRemoteAddress()
	} else {
		address = channel.GetLocalAddress()
	}

	excludedUtxoMap := make(map[string]bool)
	fundingUtxoMgr := NewUtxoMgr(address, p.l1IndexerClient)
	prepared, err := p.PrepareSTPAssetInputs(STPAssetInputRequest{
		Asset: asset, Amount: dAmt, FundingAddress: address, AssetAddress: channel.Address,
		FundingUtxos: fundingUtxoMgr, ExcludedUtxos: excludedUtxoMap,
		PreviousInputs: preTxInputs, RevealKey: revealKey, FeeRate: feeRate,
		LockInputs: true, Operation: STPOpSpliceOut,
	})
	if err != nil {
		return "", 0, err
	}
	utxos, inscribe := prepared.Inputs, prepared.Inscribe
	if inscribe != nil {
		defer p.unlockOpenInscribes([]*InscribeResv{inscribe})
	}

	var feeInputs []*TxOutput
	if len(fees) == 0 {
		estimatedFee := CalcFee_SplicingOut(3, 4, name, dAmt, feeRate, channel.IsInitiator, channel.FeeCfg)
		feeInputs, err = p.GetUtxosForFeeV3WithMgr(fundingUtxoMgr, estimatedFee, excludedUtxoMap, false)
		if err != nil {
			Log.Errorf("GetUtxosForFeeV3WithMgr failed, %v", err)
			return "", -1, err
		}
	} else {
		for _, utxo := range fees {
			info, err := p.getL1TxOutput(utxo)
			if err != nil {
				Log.Errorf("SplicingOut: GetTxOutput %s failed, %v", utxo, err)
				return "", 0, err
			}
			feeInputs = append(feeInputs, info)
		}
	}

	txid, id, err := p.funderInitSplicingOutProcessWithRGBEvidence(channel, destAddr, name, feeRate, dAmt,
		utxos, feeInputs, inscribe, revealKey, reason, moreData, rgbEvidence)
	Log.Infof("SplicingOut finished, %v", time.Since(start))
	return txid, id, err
}

func (p *Manager) SplicingIn(channelId string, assetName string, amt string,
	utxos, fees, preTxInputs []string, revealKey []byte, feeRate int64, reason string) (string, int64, error) {
	start := time.Now()
	Log.Infof("SplicingIn %s %s", assetName, amt)
	if p.wallet == nil {
		return "", 0, fmt.Errorf("wallet is not created/unlocked")
	}
	if !p.IsReady() {
		return "", 0, fmt.Errorf("not ready")
	}

	channel := p.GetChannel(channelId)
	if channel == nil {
		return "", 0, fmt.Errorf("can't find channel %s", channelId)
	}
	if channel.Status != CS_READY {
		return "", 0, fmt.Errorf("channel is not ready")
	}
	if !p.IsPeerOnline(channel.PeerNodeId) {
		return "", 0, fmt.Errorf("peer is offline")
	}

	// RGB native validation must observe the chain before the original
	// synchronous operation takes its channel lock. Existing lock/RPC order
	// below is unchanged, including both signature exchanges.
	var rgbEvidence *STPRGBReservationEvidence
	var rgbRequest []byte
	preAsset := ParseAssetString(assetName)
	if preAsset != nil && preAsset.Protocol == "rgb11" && len(utxos) == 0 {
		info := p.getTickerInfo(preAsset)
		if info == nil {
			return "", 0, fmt.Errorf("can't get ticker %s info", assetName)
		}
		amount, parseErr := indexer.NewDecimalFromString(amt, info.Divisibility)
		if parseErr != nil {
			return "", 0, parseErr
		}
		channel.Mutex.RLock()
		address := channel.GetLocalAddress()
		if reason == SPLICING_REASON_REMOTE {
			address = channel.GetRemoteAddress()
		}
		channel.Mutex.RUnlock()
		prepared, prepareErr := p.PrepareSTPAssetInputs(STPAssetInputRequest{
			Asset: preAsset, Amount: amount, FundingAddress: address, AssetAddress: address,
			ExcludedUtxos: map[string]bool{}, PreviousInputs: append([]string(nil), preTxInputs...),
			RevealKey: append([]byte(nil), revealKey...), FeeRate: feeRate, LockInputs: true, Operation: STPOpSpliceIn,
		})
		if prepareErr != nil {
			return "", -1, prepareErr
		}
		for _, input := range prepared.Inputs {
			if input != nil {
				utxos = append(utxos, input.OutPointStr)
			}
		}
	}
	rgbRequest, err := json.Marshal(struct {
		Channel, Asset, Amount, Reason string
		Inputs, Fees, Previous         []string
		Reveal                         []byte
		FeeRate                        int64
	}{channelId, assetName, amt, reason, utxos, fees, preTxInputs, revealKey, feeRate})
	if err != nil {
		return "", 0, err
	}
	rgbEvidence, err = p.PrepareSTPRGBSplicingEvidence(channel, preAsset, utxos, nil, rgbRequest, nil)
	if err != nil {
		return "", 0, err
	}

	channel.Mutex.Lock()
	defer channel.Mutex.Unlock()
	if err := rgbEvidence.ValidateForChannel(channel, rgbRequest); err != nil {
		return "", 0, err
	}

	asset := ParseAssetString(assetName)
	if asset == nil {
		return "", 0, fmt.Errorf("invalid asset name %s", assetName)
	}
	if err := ValidateSTPAsset(asset, STPOpSpliceIn); err != nil {
		return "", 0, err
	}
	tickerInfo := p.getTickerInfo(asset)
	if tickerInfo == nil {
		return "", 0, fmt.Errorf("can't get ticker %s info", assetName)
	}
	dAmt, err := indexer.NewDecimalFromString(amt, tickerInfo.Divisibility)
	if err != nil {
		return "", 0, err
	}
	if dAmt.Sign() <= 0 {
		return "", 0, fmt.Errorf("invalid amt")
	}
	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}

	var fundingAddress string
	if reason == SPLICING_REASON_REMOTE {
		fundingAddress = channel.GetRemoteAddress()
	} else {
		fundingAddress = channel.GetLocalAddress()
	}

	var assetInputs, feeInputs []*TxOutput
	var inscribe *InscribeResv
	n, v := channel.NeedStubUtxo(GetAssetName(tickerInfo))
	estimatedFee := CalcFee_SplicingIn(3, 3, asset, feeRate, n, v)
	Log.Infof("CalcFee_SplicingIn %d", estimatedFee)
	excludedUtxoMap := make(map[string]bool)
	fundingUtxoMgr := NewUtxoMgr(fundingAddress, p.l1IndexerClient)
	if indexer.IsPlainAsset(asset) {
		if len(utxos) == 0 {
			value := dAmt.Int64() + estimatedFee
			assetInputs, err = p.getUtxosWithAssetV3(fundingAddress, indexer.NewDefaultDecimal(value), asset, nil)
			if err != nil {
				return "", -1, err
			}
			fees = nil
		} else {
			all := append(utxos, fees...)
			for _, utxo := range all {
				info, err := p.getL1TxOutput(utxo)
				if err != nil {
					Log.Errorf("SplicingIn: GetTxOutput %s failed, %v", utxo, err)
					return "", 0, err
				}
				assetInputs = append(assetInputs, info)
			}
		}
	} else {
		if len(utxos) == 0 {
			prepared, prepareErr := p.PrepareSTPAssetInputs(STPAssetInputRequest{
				Asset: asset, Amount: dAmt, FundingAddress: fundingAddress, AssetAddress: fundingAddress,
				FundingUtxos: fundingUtxoMgr, ExcludedUtxos: excludedUtxoMap,
				PreviousInputs: preTxInputs, RevealKey: revealKey, FeeRate: feeRate,
				LockInputs: true, Operation: STPOpSpliceIn,
			})
			err = prepareErr
			if err == nil {
				assetInputs, inscribe = prepared.Inputs, prepared.Inscribe
				if inscribe != nil {
					defer p.unlockOpenInscribes([]*InscribeResv{inscribe})
				}
			}

			if err != nil {
				return "", -1, err
			}
		} else {
			for _, utxo := range utxos {
				info, err := p.getL1TxOutput(utxo)
				if err != nil {
					Log.Errorf("SplicingIn: GetTxOutput %s failed, %v", utxo, err)
					return "", 0, err
				}
				assetInputs = append(assetInputs, info)
			}
		}

		if len(fees) == 0 {
			feeInputs, err = p.GetUtxosForFeeV3WithMgr(fundingUtxoMgr, estimatedFee, excludedUtxoMap, false)
			if err != nil {
				Log.Errorf("GetUtxosForFeeV3WithMgr failed, %v", err)
				return "", -1, err
			}
		} else {
			for _, utxo := range fees {
				info, err := p.getL1TxOutput(utxo)
				if err != nil {
					Log.Errorf("SplicingIn: GetTxOutput %s failed, %v", utxo, err)
					return "", 0, err
				}
				feeInputs = append(feeInputs, info)
			}
		}
	}

	txid, id, err := p.funderInitSplicingInProcessWithRGBEvidence(channel, asset, dAmt, feeRate,
		assetInputs, feeInputs, inscribe, revealKey, reason, rgbEvidence)
	Log.Infof("SplicingIn finished, %v", time.Since(start))
	return txid, id, err
}

func (p *Manager) ExpandChannel(channelId string, assetName string, utxo string, reason string, memo []byte) (string, string, int64, error) {
	start := time.Now()
	Log.Infof("ExpandChannel")
	if p.wallet == nil {
		return "", "", 0, fmt.Errorf("wallet is not created/unlocked")
	}
	if !p.IsReady() {
		return "", "", 0, fmt.Errorf("not ready")
	}
	if p.isControlByContract(channelId, assetName) {
		return "", "", 0, fmt.Errorf("asset %s is controlled by contract", assetName)
	}
	if indexer.GetStakeAssetName(p.GetSyncHeightL1()) == assetName {
		return "", "", 0, fmt.Errorf("asset %s is controlled by contract", assetName)
	}

	channel := p.GetChannel(channelId)
	if channel == nil {
		return "", "", 0, fmt.Errorf("can't find channel %s", channelId)
	}
	if !p.IsPeerOnline(channel.PeerNodeId) {
		return "", "", 0, fmt.Errorf("peer is offline")
	}
	// Collect RGB observations before the original business lock. Expansion
	// sources are private channel inventory, not ordinary wallet projections.
	rgbRequest, err := json.Marshal(struct {
		Channel, Asset, Input, Reason string
		Memo                          []byte
	}{channelId, assetName, utxo, reason, memo})
	if err != nil {
		return "", "", 0, err
	}
	rgbEvidence, err := p.prepareSTPRGBExpansionEvidence(channel, ParseAssetString(assetName), utxo, rgbRequest)
	if err != nil {
		return "", "", 0, err
	}
	channel.Mutex.Lock()
	defer channel.Mutex.Unlock()
	if err := rgbEvidence.ValidateForChannel(channel, rgbRequest); err != nil {
		return "", "", 0, err
	}

	if !channel.IsInitiator {
		return "", "", 0, fmt.Errorf("only initiator can perform this interface")
	}

	asset := ParseAssetString(assetName)
	if asset == nil {
		return "", "", 0, fmt.Errorf("invalid asset name %s", assetName)
	}
	if err := ValidateSTPAsset(asset, STPOpExpand); err != nil {
		return "", "", 0, err
	}

	txid, amt, id, err := p.funderInitExpandingProcessWithRGBEvidence(channel, asset, utxo, reason, memo, rgbEvidence)
	if err != nil {
		Log.Errorf("funderInitExpandingProcess %s failed. %v", utxo, err)
		return "", "", 0, err
	}
	Log.Infof("ExpandChannel finished, %v", time.Since(start))
	return txid, amt.String(), id, err
}

func (p *Manager) ExpandChannel_SatsNet(channelId string, assetName string, utxos []string) (string, error) {
	amount, err := p.expandChannelSatsNet(channelId, assetName, utxos)
	if err != nil {
		return "", err
	}
	return amount.String(), nil
}

func (p *Manager) expandChannelSatsNet(channelId string, assetName string, utxos []string) (*Decimal, error) {
	start := time.Now()
	Log.Infof("ExpandChannel_SatsNet")
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	if !p.IsReady() {
		return nil, fmt.Errorf("not ready")
	}
	if p.isControlByContract(channelId, assetName) {
		return nil, fmt.Errorf("asset %s is controlled by contract", assetName)
	}

	channel := p.GetChannel(channelId)
	if channel == nil {
		return nil, fmt.Errorf("can't find channel %s", channelId)
	}
	if !p.IsPeerOnline(channel.PeerNodeId) {
		return nil, fmt.Errorf("peer is offline")
	}
	// Own the exact inputs before querying RGB outside the original write lock.
	utxos = append([]string(nil), utxos...)
	rgbRequest, err := json.Marshal(struct {
		Channel, Asset string
		Inputs         []string
	}{channelId, assetName, utxos})
	if err != nil {
		return nil, err
	}
	rgbEvidence, err := p.PrepareSTPRGBSplicingEvidence(channel, ParseAssetString(assetName), nil, nil, rgbRequest, nil)
	if err != nil {
		return nil, err
	}
	channel.Mutex.Lock()
	defer channel.Mutex.Unlock()
	if err := rgbEvidence.ValidateForChannel(channel, rgbRequest); err != nil {
		return nil, err
	}

	if !channel.IsInitiator {
		return nil, fmt.Errorf("only initiator can perform this interface")
	}

	asset := ParseAssetString(assetName)
	if asset == nil {
		return nil, fmt.Errorf("invalid asset name %s", assetName)
	}

	amt, err := p.funderInitExpandingProcessSatsNetWithRGBEvidence(channel, asset, utxos, rgbEvidence)
	if err != nil {
		Log.Errorf("funderInitExpandingProcess_SatsNet failed. %v", err)
		return nil, err
	}
	Log.Infof("ExpandChannel_SatsNet finished, %v", time.Since(start))
	return amt, err
}

func (p *Manager) ExpandAll_SatsNet(channelId string, assetName string) (string, error) {
	start := time.Now()
	Log.Infof("ExpandAll_SatsNet")
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}
	if !p.IsReady() {
		return "", fmt.Errorf("not ready")
	}
	if p.isControlByContract(channelId, assetName) {
		return "", fmt.Errorf("asset %s is controlled by contract", assetName)
	}

	channel := p.GetChannel(channelId)
	if channel == nil {
		return "", fmt.Errorf("can't find channel %s", channelId)
	}
	if !p.IsPeerOnline(channel.PeerNodeId) {
		return "", fmt.Errorf("peer is offline")
	}
	channel.Mutex.RLock()
	initiator, address := channel.IsInitiator, channel.Address
	channel.Mutex.RUnlock()

	if !initiator {
		return "", fmt.Errorf("only initiator can perform this interface")
	}

	var value *Decimal
	var itemErrors []error
	asset := ParseAssetString(assetName)
	if asset == nil {
		assets := p.l2IndexerClient.GetAssetSummaryWithAddress(address)
		if assets != nil {
			for _, u := range assets.Data {
				assetName := &u.Name
				amt, err := p.ExpandAssetInChannelSatsNet(channel, assetName)
				value = value.Add(amt)
				if err != nil {
					itemErrors = append(itemErrors, fmt.Errorf("expand asset %s: %w", assetName.String(), err))
					continue
				}
				Log.Infof("expandAsset %s %s", assetName.String(), amt.String())
			}
		}
	} else {
		var err error
		value, err = p.ExpandAssetInChannelSatsNet(channel, asset)
		if err != nil {
			itemErrors = append(itemErrors, fmt.Errorf("expand asset %s: %w", asset.String(), err))
		}
	}

	Log.Infof("ExpandAll_SatsNet finished, %v", time.Since(start))
	return value.String(), errors.Join(itemErrors...)
}

func (p *Manager) ExpandAsset(channelId string, assetName string) ([]string, string, error) {
	if p.wallet == nil {
		return nil, "", fmt.Errorf("wallet is not created/unlocked")
	}
	return p.expandAssetForWallet(p.wallet.Clone(), channelId, assetName)
}

func (p *Manager) expandAssetForWallet(w common.Wallet, channelId string, assetName string) ([]string, string, error) {
	start := time.Now()
	Log.Infof("ExpandAsset")
	if w == nil {
		return nil, "", fmt.Errorf("wallet is not created/unlocked")
	}
	if !p.IsReady() {
		return nil, "", fmt.Errorf("not ready")
	}
	if indexer.GetStakeAssetName(p.GetSyncHeightL1()) == assetName {
		return nil, "", fmt.Errorf("asset %s is controlled by contract", assetName)
	}

	channel := p.GetChannel(channelId)
	if channel == nil {
		return nil, "", fmt.Errorf("can't find channel %s", channelId)
	}
	channel.Mutex.RLock()
	matchesWallet := channel.LocalWallet() != nil && channel.LocalWallet().GetWalletId() == w.GetWalletId() && channel.LocalWallet().GetPaymentPubKey().IsEqual(w.GetPaymentPubKey())
	initiator, address := channel.IsInitiator, channel.Address
	channel.Mutex.RUnlock()
	if !matchesWallet {
		return nil, "", fmt.Errorf("channel signer does not match captured recovery account")
	}
	if !p.IsPeerOnline(channel.PeerNodeId) {
		return nil, "", fmt.Errorf("peer is offline")
	}
	if !initiator {
		return nil, "", fmt.Errorf("only initiator can perform this interface")
	}

	var value *Decimal
	anchorIds := make([]string, 0)
	var itemErrors []error
	asset := ParseAssetString(assetName)
	if assetName == "" {
		assets := p.l1IndexerClient.GetAssetSummaryWithAddress(address)
		if assets != nil {
			unhandled := make([]*indexer.AssetInfo, 0)
			for _, u := range assets.Data {
				if u.Name.String() == indexer.GetStakeAssetName(p.GetSyncHeightL1()) {
					continue
				}
				if !STPPolicyFor(&AssetName{AssetName: u.Name}).AccountBased {
					unhandled = append(unhandled, u)
					continue
				}
				ids, amt, err := p.expandAssetInChannelForWallet(channel, &u.Name, w)
				anchorIds = append(anchorIds, ids...)
				value = value.Add(amt)
				if err != nil {
					itemErrors = append(itemErrors, fmt.Errorf("expand asset %s: %w", u.Name.String(), err))
					continue
				}
				Log.Infof("expandAsset %s %s", u.Name.String(), amt.String())
			}

			for _, u := range unhandled {
				ids, amt, err := p.expandAssetInChannelForWallet(channel, &u.Name, w)
				anchorIds = append(anchorIds, ids...)
				value = value.Add(amt)
				if err != nil {
					itemErrors = append(itemErrors, fmt.Errorf("expand asset %s: %w", u.Name.String(), err))
					continue
				}
				Log.Infof("expandAsset %s %s", u.Name.String(), amt.String())
			}
		}
	} else {
		if asset == nil {
			return nil, "", fmt.Errorf("invalid asset name %s", assetName)
		}
		if err := ValidateSTPAsset(asset, STPOpExpand); err != nil {
			return nil, "", err
		}
		var err error
		anchorIds, value, err = p.expandAssetInChannelForWallet(channel, asset, w)
		if err != nil {
			itemErrors = append(itemErrors, fmt.Errorf("expand asset %s: %w", asset.String(), err))
		}
		if value != nil {
			Log.Infof("expandAsset %s %s", asset.String(), value.String())
		}
	}

	Log.Infof("ExpandAsset finished, %v", time.Since(start))
	return anchorIds, value.String(), errors.Join(itemErrors...)
}

func (p *Manager) ReopenChannel(expandAll bool) (string, error) {
	w, client, err := p.closedRecoveryWalletSnapshot()
	if err != nil {
		return "", err
	}
	return p.reopenClosedChannelForWallet(w, client, expandAll)
}

func (p *Manager) reopenClosedChannelForWallet(w common.Wallet, client NodeRPCClient, expandAll bool) (string, error) {
	return p.reopenClosedChannelForWalletWithFundingPolicy(context.Background(), w, client, expandAll, closedReopenExplicitFunding, "")
}

func (p *Manager) reopenClosedChannelForWalletWithFundingPolicy(ctx context.Context, w common.Wallet, client NodeRPCClient, expandAll bool, policy closedReopenFundingPolicy, expectedChannelID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !p.IsReady() {
		return "", fmt.Errorf("not ready")
	}
	if !p.CheckSuperNodeStatus() {
		return "", fmt.Errorf("peer is offline")
	}
	address, err := p.channelIDForWallet(w)
	if err != nil {
		return "", err
	}
	if expectedChannelID != "" && address != expectedChannelID {
		return "", fmt.Errorf("captured recovery channel payment keys changed")
	}
	var opts FundingInitOptions
	var fundingFeeCfg *ChannelFeeConfig
	fundingPrepared := false
	if policy == closedReopenExistingFundingOnly {
		stored, err := p.LoadChannelInDB(address)
		if err != nil {
			return "", err
		}
		if stored.Status <= CS_CLOSED {
			// Read-only preflight precedes creating a new reopen owner. A lack
			// of old funding must not silently choose reopen over withdrawal.
			opts, fundingFeeCfg, err = p.prepareClosedReopenFunding(w, address, stored, policy)
			if err != nil {
				return "", err
			}
			fundingPrepared = true
		}
	}
	owner, intent, err := p.closedReopenIntentForWallet(w, address)
	if err != nil {
		return "", err
	}
	if err := p.rejectClosedChannelIntentConflicts(owner, w); err != nil {
		return "", err
	}
	if policy == closedReopenExistingFundingOnly && intent.Request != nil && intent.Request.NeedSendFundingTx {
		return "", closedChannelFundingApprovalRequired(address)
	}
	if p.HasContractInChannel(address) {
		return "", fmt.Errorf("contract exists, not allow to reopen channel")
	}
	if intent.Request == nil || intent.Step == "open_rejected" {
		var approvedTopup *TxOutput
		if intent.TopupJSON != "" {
			approvedTopup, err = p.closedChannelSingleUTXOTopupFunding(w, owner, intent)
			if err != nil {
				return "", err
			}
			if approvedTopup == nil {
				return "", closedChannelRecoveryPending(owner, "topup_confirmation", nil)
			}
		}
		if intent.Step == "drain_inflight" {
			return "", closedChannelRecoveryPending(owner, intent.Step, nil)
		}
		if !fundingPrepared {
			stored, err := p.LoadChannelInDB(address)
			if err != nil {
				return "", err
			}
			opts, fundingFeeCfg, err = p.prepareClosedReopenFunding(w, address, stored, policy)
			if err != nil {
				return "", err
			}
		}
		if approvedTopup != nil {
			approvedRate, err := closedChannelSingleUTXOTopupFeeRate(intent)
			if err != nil {
				return "", err
			}
			opts.FundingUtxo, opts.Amount, opts.InitialCapacity = approvedTopup, approvedTopup.Value(), approvedTopup.Value()
			opts.NeedSendFundingTx, opts.FeeRate = false, approvedRate
		}
		if policy == closedReopenExistingFundingOnly {
			actual, err := p.validateClosedReopenExistingFunding(address, opts.FundingUtxo, opts.Amount)
			if err != nil {
				return "", err
			}
			opts.FundingUtxo = actual
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// The intent is already durable before the existing drain signing RPC.
		opts.L2DrainTxId, err = p.drainClosedChannelBeforeReopen(w, client, owner, intent)
		if err != nil {
			return "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	channelID, err := p.funderClosedReopenForWallet(w, client, owner, intent, opts, fundingFeeCfg)
	if err != nil || !expandAll && policy != closedReopenExistingFundingOnly {
		return channelID, err
	}
	var ready *Channel
	for i := 0; i < 20; i++ {
		channel := p.GetChannel(channelID)
		if channel != nil {
			channel.Mutex.RLock()
			matched := channel.Status == CS_READY && channel.LocalWallet() != nil && channel.LocalWallet().GetWalletId() == w.GetWalletId() &&
				channel.LocalWallet().GetPaymentPubKey().IsEqual(w.GetPaymentPubKey())
			channel.Mutex.RUnlock()
			if matched {
				ready = channel
				break
			}
		}
		select {
		case <-ctx.Done():
			return channelID, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	if ready == nil {
		return channelID, closedChannelRecoveryPending(owner, "waiting_ready", nil)
	}
	if !expandAll {
		// Automatic funding recovery only establishes this fresh channel.
		// Legacy all-asset expansion may top up stubs using the live wallet
		// and wait without a bound; it is not safe automatic authorization.
		return channelID, nil
	}
	_, _, err = p.expandAssetForWallet(w, channelID, "")
	return channelID, err
}

func (p *Manager) prepareClosedReopenFunding(w common.Wallet, address string, stored *ChannelInDB, policy closedReopenFundingPolicy) (FundingInitOptions, *ChannelFeeConfig, error) {
	oldChannel := &Channel{ChannelInDB: *stored, manager: p, localWallet: w.Clone()}
	feeCfg := reopenFeeConfig(oldChannel)
	ledger, err := p.l2IndexerClient.GetChannelLedger(address)
	if err != nil {
		return FundingInitOptions{}, nil, fmt.Errorf("GetChannelLedger %s failed: %v", address, err)
	}
	hasHistory := channelLedgerHasHistory(address, ledger)
	if hasHistory {
		feeCfg = reopenFeeConfigWithoutOpeningFee(feeCfg)
	}
	fundingFeeCfg := feeCfg
	if !hasHistory {
		fundingFeeCfg = NewFeeConfig()
	}
	utxos := p.l1IndexerClient.GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)
	sort.Slice(utxos, func(i, j int) bool { return utxos[i].Value > utxos[j].Value })
	var readErr error
	output := selectReopenFundingOutput(utxos, feeCfg.MinCapacity(), func(outpoint string) bool {
		lucky, err := p.isBTCLuckyL1Output(outpoint, nil)
		if err != nil {
			readErr = err
			return true
		}
		if lucky {
			return true
		}
		_, err = p.l2IndexerClient.GetAscendData(outpoint)
		if policy == closedReopenExistingFundingOnly && err != nil && !closedReopenAscendMissing(outpoint, err) {
			readErr = err
			return true
		}
		return err == nil
	})
	if readErr != nil {
		return FundingInitOptions{}, nil, fmt.Errorf("cannot verify existing channel funding: %w", readErr)
	}
	if policy == closedReopenExistingFundingOnly && output == nil {
		return FundingInitOptions{}, nil, closedChannelFundingApprovalRequired(address)
	}
	opts := FundingInitOptions{FeeRate: p.GetFeeRate(), Memo: "reopen", NeedSendFundingTx: output == nil}
	if output == nil {
		opts.Amount = fundingFeeCfg.MinCapacity()
	} else {
		opts.Amount, opts.FundingUtxo = output.Value, OutputInfoToOutput(output)
	}
	opts.InitialCapacity = opts.Amount
	if policy == closedReopenExistingFundingOnly {
		actual, err := p.validateClosedReopenExistingFunding(address, opts.FundingUtxo, opts.Amount)
		if err != nil {
			return FundingInitOptions{}, nil, err
		}
		opts.FundingUtxo = actual
	}
	return opts, fundingFeeCfg, nil
}

func (p *Manager) RebuildChannel() (string, error) {
	return p.RebuildChannelWithUtxo("")
}

func (p *Manager) RebuildChannelWithUtxo(fundingUtxo string) (string, error) {
	start := time.Now()
	Log.Infof("RebuildChannel")
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}
	if !p.IsReady() {
		return "", fmt.Errorf("not ready")
	}
	if !p.CheckSuperNodeStatus() {
		return "", fmt.Errorf("peer is offline")
	}
	if channel := p.GetActiveChannel(); channel != nil && channel.Status > CS_CLOSED {
		return "", fmt.Errorf("channel %s exists", channel.ChannelId)
	}

	address, err := p.GetChannelAddress()
	if err != nil {
		Log.Errorf("GetChannelAddress failed. %v", err)
		return "", err
	}
	if err := p.rejectUnfinishedChannelLifecycle(address); err != nil {
		return "", err
	}
	if p.HasContractInChannel(address) {
		return "", fmt.Errorf("contract exists, not allow to rebuild channel")
	}

	ledger, err := p.l2IndexerClient.GetChannelLedger(address)
	if err != nil {
		return "", fmt.Errorf("GetChannelLedger %s failed: %v", address, err)
	}

	var output *TxOutput
	if fundingUtxo != "" {
		output, err = p.getL1TxOutput(fundingUtxo)
		if err != nil {
			return "", err
		}
		if output.HasAsset() {
			return "", fmt.Errorf("funding utxo %s has assets", fundingUtxo)
		}
		pkScript, err := GetPkScriptFromAddress(address)
		if err != nil {
			return "", err
		}
		if !bytes.Equal(output.OutValue.PkScript, pkScript) {
			return "", fmt.Errorf("funding utxo %s is not locked to channel address %s", fundingUtxo, address)
		}
		reason, ok := classifyRebuildFundingUtxo(ledger, fundingUtxo)
		if !ok {
			return "", fmt.Errorf("funding utxo %s cannot rebuild without opening anchor: %s", fundingUtxo, reason)
		}
		Log.Infof("rebuild funding utxo %s accepted: %s", fundingUtxo, reason)
	} else {
		utxos := p.l1IndexerClient.GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)
		if len(utxos) == 0 {
			return "", fmt.Errorf("ReopenChannel failed. address %s have no enough plain sats", address)
		}

		sort.Slice(utxos, func(i, j int) bool {
			return utxos[i].Value > utxos[j].Value
		})

		for _, utxo := range utxos {
			lucky, err := p.isBTCLuckyL1Output(utxo.OutPoint, nil)
			if err != nil {
				return "", err
			}
			if lucky {
				continue
			}

			reason, ok := classifyRebuildFundingUtxo(ledger, utxo.OutPoint)
			if !ok {
				Log.Warnf("skip rebuild candidate %s: %s", utxo.OutPoint, reason)
				continue
			}
			Log.Infof("rebuild funding utxo %s accepted: %s", utxo.OutPoint, reason)
			output = OutputInfoToOutput(utxo)
			break
		}
		if output == nil {
			return "", fmt.Errorf("no L1 channel utxo can rebuild without opening anchor; use reopen/open for unanchored assets")
		}
	}
	channelId, err := p.FunderInitReOpenProcess(output.Value(), output, "rebuild", false, true, nil, "")
	Log.Infof("RebuildChannel finished, %v", time.Since(start))
	return channelId, err
}

func (p *Manager) RestoreChannel(channelId string) (*Channel, error) {
	Log.Infof("RestoreChannel")
	if err := p.rejectUnfinishedChannelLifecycle(channelId); err != nil {
		return nil, err
	}
	if err := p.CleanChannelData(channelId); err != nil {
		return nil, err
	}

	c, err := p.GetChannelFromDKVS(channelId)
	if err != nil {
		Log.Errorf("getChannelFromDKVS %s failed, %v", channelId, err)
		return nil, err
	}

	if err := p.SaveChannelToDB(c); err != nil {
		return nil, err
	}
	if err := p.EnableChannel(c); err != nil {
		return nil, err
	}
	c.PeerRPC = p.GetPeerNodeClient(&c.ChannelInDB)
	Log.Infof("channel %s %d is restored from dkvs", channelId, c.CommitHeight)
	return c, nil
}
