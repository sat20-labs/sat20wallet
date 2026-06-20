package wallet

import (
	"bytes"
	"fmt"
	"sort"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	indexer "github.com/sat20-labs/indexer/common"
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

	channelAddr, err := p.GetChannelAddress()
	if err != nil {
		return "", err
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

	channel.Mutex.Lock()
	defer channel.Mutex.Unlock()
	if channel.Status != CS_READY {
		return "", 0, fmt.Errorf("channel %s is not ready", channelId)
	}

	asset := ParseAssetString(assetName)
	if asset == nil {
		return "", 0, fmt.Errorf("invalid asset name %s", assetName)
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
			OldChannel: oldChannel,
			Channel:    channel,
			FeeRate:    p.GetFeeRate(),
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
	channel.Mutex.Lock()
	defer channel.Mutex.Unlock()
	if channel.Status != CS_READY {
		return "", 0, fmt.Errorf("channel %s is not ready", channelId)
	}

	asset := ParseAssetString(assetName)
	if asset == nil {
		return "", 0, fmt.Errorf("invalid asset name %s", assetName)
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
	if channel.HasProtocolAsset(indexer.PROTOCOL_NAME_BRC20) {
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

	url, err := p.GetTranscendContractWithAssetNameInServer(assetName)
	if err != nil {
		Log.Errorf("can't find transcend contract by asset %s", assetName)
		return "", 0, err
	}

	return p.PerformLocalAction(LOCAL_ACTION_LOCK_WITH_EXPAND, &LocalActionParam_Expand{
		AssetName:   name,
		Amt:         dAmt,
		ContractURL: url,
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

	channel.Mutex.Lock()
	defer channel.Mutex.Unlock()

	asset := ParseAssetString(assetName)
	if asset == nil {
		return "", 0, fmt.Errorf("invalid asset name %s", assetName)
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

	var utxos []*TxOutput
	var inscribe *InscribeResv
	excludedUtxoMap := make(map[string]bool)
	fundingUtxoMgr := NewUtxoMgr(address, p.l1IndexerClient)
	if asset.Protocol == indexer.PROTOCOL_NAME_BRC20 {
		var output *TxOutput
		output, inscribe, err = p.getUtxosForBRC20(address, channel.Address, fundingUtxoMgr, excludedUtxoMap, asset, dAmt,
			preTxInputs, revealKey, feeRate, true)
		if err != nil {
			return "", 0, err
		}
		defer func() {
			if inscribe != nil && inscribe.Status != RS_CLOSED {
				p.utxoLockerL1.UnlockUtxosWithTx(inscribe.CommitTx)
			}
		}()
		utxos = []*TxOutput{output}
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
			info, err := p.l1IndexerClient.GetTxOutput(utxo)
			if err != nil {
				Log.Errorf("SplicingOut: GetTxOutput %s failed, %v", utxo, err)
				return "", 0, err
			}
			feeInputs = append(feeInputs, info)
		}
	}

	txid, id, err := p.FunderInitSplicingOutProcess(channel, destAddr, name, feeRate, dAmt,
		utxos, feeInputs, inscribe, revealKey, reason, moreData)
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

	channel.Mutex.Lock()
	defer channel.Mutex.Unlock()

	asset := ParseAssetString(assetName)
	if asset == nil {
		return "", 0, fmt.Errorf("invalid asset name %s", assetName)
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
				info, err := p.l1IndexerClient.GetTxOutput(utxo)
				if err != nil {
					Log.Errorf("SplicingIn: GetTxOutput %s failed, %v", utxo, err)
					return "", 0, err
				}
				assetInputs = append(assetInputs, info)
			}
		}
	} else {
		if len(utxos) == 0 {
			if asset.Protocol == indexer.PROTOCOL_NAME_BRC20 {
				var output *TxOutput
				output, inscribe, err = p.getUtxosForBRC20(fundingAddress, fundingAddress, fundingUtxoMgr,
					excludedUtxoMap, asset, dAmt, preTxInputs, revealKey, feeRate, true)
				if err == nil {
					assetInputs = []*TxOutput{output}
					defer func() {
						if inscribe != nil && inscribe.Status != RS_CLOSED {
							p.utxoLockerL1.UnlockUtxosWithTx(inscribe.CommitTx)
						}
					}()
				}
			} else {
				assetInputs, err = p.getUtxosWithAssetV3(fundingAddress, dAmt, asset, nil)
			}
			if err != nil {
				return "", -1, err
			}
		} else {
			for _, utxo := range utxos {
				info, err := p.l1IndexerClient.GetTxOutput(utxo)
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
				info, err := p.l1IndexerClient.GetTxOutput(utxo)
				if err != nil {
					Log.Errorf("SplicingIn: GetTxOutput %s failed, %v", utxo, err)
					return "", 0, err
				}
				feeInputs = append(feeInputs, info)
			}
		}
	}

	txid, id, err := p.FunderInitSplicingInProcess(channel, asset, dAmt, feeRate,
		assetInputs, feeInputs, inscribe, revealKey, reason)
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
	channel.Mutex.Lock()
	defer channel.Mutex.Unlock()

	if !channel.IsInitiator {
		return "", "", 0, fmt.Errorf("only initiator can perform this interface")
	}

	asset := ParseAssetString(assetName)
	if asset == nil {
		return "", "", 0, fmt.Errorf("invalid asset name %s", assetName)
	}

	txid, amt, id, err := p.FunderInitExpandingProcess(channel, asset, utxo, reason, memo)
	if err != nil {
		Log.Errorf("funderInitExpandingProcess %s failed. %v", utxo, err)
		return "", "", 0, err
	}
	Log.Infof("ExpandChannel finished, %v", time.Since(start))
	return txid, amt.String(), id, err
}

func (p *Manager) ExpandChannel_SatsNet(channelId string, assetName string, utxos []string) (string, error) {
	start := time.Now()
	Log.Infof("ExpandChannel_SatsNet")
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
	channel.Mutex.Lock()
	defer channel.Mutex.Unlock()

	if !channel.IsInitiator {
		return "", fmt.Errorf("only initiator can perform this interface")
	}

	asset := ParseAssetString(assetName)
	if asset == nil {
		return "", fmt.Errorf("invalid asset name %s", assetName)
	}

	amt, err := p.FunderInitExpandingProcessSatsNet(channel, asset, utxos)
	if err != nil {
		Log.Errorf("funderInitExpandingProcess_SatsNet failed. %v", err)
		return "", err
	}
	Log.Infof("ExpandChannel_SatsNet finished, %v", time.Since(start))
	return amt.String(), err
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
	channel.Mutex.Lock()
	defer channel.Mutex.Unlock()

	if !channel.IsInitiator {
		return "", fmt.Errorf("only initiator can perform this interface")
	}

	var value *Decimal
	var err error
	asset := ParseAssetString(assetName)
	if asset == nil {
		address := channel.Address
		assets := p.l2IndexerClient.GetAssetSummaryWithAddress(address)
		if assets != nil {
			for _, u := range assets.Data {
				assetName := &u.Name
				amt, err := p.ExpandAssetInChannelSatsNet(channel, assetName)
				if err != nil {
					Log.Errorf("expandAsset %s failed, %v", assetName, err)
					continue
				}
				Log.Infof("expandAsset %s %s", assetName.String(), amt.String())
			}
		}
	} else {
		value, err = p.ExpandAssetInChannelSatsNet(channel, asset)
	}

	Log.Infof("ExpandAll_SatsNet finished, %v", time.Since(start))
	return value.String(), err
}

func (p *Manager) ExpandAsset(channelId string, assetName string) ([]string, string, error) {
	start := time.Now()
	Log.Infof("ExpandAsset")
	if p.wallet == nil {
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
	if !p.IsPeerOnline(channel.PeerNodeId) {
		return nil, "", fmt.Errorf("peer is offline")
	}
	channel.Mutex.Lock()
	defer channel.Mutex.Unlock()

	if !channel.IsInitiator {
		return nil, "", fmt.Errorf("only initiator can perform this interface")
	}

	var value *Decimal
	anchorIds := make([]string, 0)
	var err error
	asset := ParseAssetString(assetName)
	if assetName == "" {
		address := channel.Address
		assets := p.l1IndexerClient.GetAssetSummaryWithAddress(address)
		if assets != nil {
			unhandled := make([]*indexer.AssetInfo, 0)
			for _, u := range assets.Data {
				if u.Name.String() == indexer.GetStakeAssetName(p.GetSyncHeightL1()) {
					continue
				}
				if u.Name.Protocol != indexer.PROTOCOL_NAME_BRC20 {
					unhandled = append(unhandled, u)
					continue
				}
				ids, amt, err := p.ExpandAssetInChannel(channel, &u.Name)
				if err != nil {
					Log.Errorf("expandAsset %s failed, %v", &u.Name, err)
					continue
				}
				Log.Infof("expandAsset %s %s", u.Name.String(), amt.String())
				anchorIds = append(anchorIds, ids...)
				value = value.Add(amt)
			}

			for _, u := range unhandled {
				ids, amt, err := p.ExpandAssetInChannel(channel, &u.Name)
				if err != nil {
					Log.Errorf("expandAsset %s failed, %v", &u.Name, err)
					continue
				}
				Log.Infof("expandAsset %s %s", u.Name.String(), amt.String())
				anchorIds = append(anchorIds, ids...)
				value = value.Add(amt)
			}
		}
	} else {
		anchorIds, value, err = p.ExpandAssetInChannel(channel, asset)
		if value != nil {
			Log.Infof("expandAsset %s %s", asset.String(), value.String())
		}
	}

	Log.Infof("ExpandAsset finished, %v", time.Since(start))
	return anchorIds, value.String(), err
}

func (p *Manager) ReopenChannel(expandAll bool) (string, error) {
	start := time.Now()
	Log.Infof("ReopenChannel")
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}
	if !p.IsReady() {
		return "", fmt.Errorf("not ready")
	}
	if !p.CheckSuperNodeStatus() {
		return "", fmt.Errorf("peer is offline")
	}
	if channel := p.GetActiveChannel(); channel != nil {
		return "", fmt.Errorf("channel %s exists", channel.ChannelId)
	}

	address, err := p.GetChannelAddress()
	if err != nil {
		Log.Errorf("GetChannelAddress failed. %v", err)
		return "", err
	}
	if p.HasContractInChannel(address) {
		return "", fmt.Errorf("contract exists, not allow to reopen channel")
	}
	l2DrainTxId, err := p.DrainChannelL2BeforeOpenIfNeeded(address)
	if err != nil {
		return "", err
	}

	oldChannel, err := p.LoadChannel(address)
	if err != nil {
		return "", err
	}
	feeCfg := reopenFeeConfig(oldChannel)
	ledger, err := p.l2IndexerClient.GetChannelLedger(address)
	if err != nil {
		return "", fmt.Errorf("GetChannelLedger %s failed: %v", address, err)
	}
	hasLedgerHistory := channelLedgerHasHistory(address, ledger)
	if hasLedgerHistory {
		feeCfg = reopenFeeConfigWithoutOpeningFee(feeCfg)
		Log.Infof("reopen channel %s has L2 ledger history; skip opening management fee", address)
	}
	fundingFeeCfg := feeCfg
	if !hasLedgerHistory {
		fundingFeeCfg = NewFeeConfig()
	}
	minFundingValue := feeCfg.MinCapacity()

	utxos := p.l1IndexerClient.GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)
	sort.Slice(utxos, func(i, j int) bool {
		return utxos[i].Value > utxos[j].Value
	})

	output := selectReopenFundingOutput(utxos, minFundingValue, func(outpoint string) bool {
		_, err := p.l2IndexerClient.GetAscendData(outpoint)
		return err == nil
	})
	needSendFundingTx := false
	fundingAmount := int64(0)
	var fundingOutput *TxOutput
	if output == nil {
		needSendFundingTx = true
		fundingAmount = fundingFeeCfg.MinCapacity()
		Log.Warnf("channel address %s has no unascended plain sats funding utxo >= %d sats; reopen with a new funding tx", address, minFundingValue)
	} else {
		fundingAmount = output.Value
		fundingOutput = OutputInfoToOutput(output)
	}

	channelId, err := p.FunderInitReOpenProcess(fundingAmount, fundingOutput, "reopen", needSendFundingTx, false, fundingFeeCfg, l2DrainTxId)
	if err != nil {
		Log.Errorf("funderInitReOpenProcess failed, %v", err)
		return "", err
	}
	Log.Infof("funderInitReOpenProcess %s", channelId)

	if expandAll {
		bReady := false
		for i := 0; i < 20; i++ {
			time.Sleep(3 * time.Second)
			channel := p.GetChannel(channelId)
			if channel == nil {
				continue
			}
			bReady = true
			Log.Infof("channel:%s is ready\n", channelId)
			break
		}
		if !bReady {
			Log.Errorf("channel %s is not ready in time", channelId)
			return channelId, nil
		}
		anchorTxIds, amt, err := p.ExpandAsset(address, "")
		if err != nil {
			Log.Errorf("ExpandAsset all assets failed, %v", err)
		} else {
			Log.Infof("amt %s, anchors:  \n%v", amt, anchorTxIds)
		}
	}

	Log.Infof("ReopenChannel finished, %v", time.Since(start))
	return channelId, err
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
	if p.HasContractInChannel(address) {
		return "", fmt.Errorf("contract exists, not allow to rebuild channel")
	}

	ledger, err := p.l2IndexerClient.GetChannelLedger(address)
	if err != nil {
		return "", fmt.Errorf("GetChannelLedger %s failed: %v", address, err)
	}

	var output *TxOutput
	if fundingUtxo != "" {
		output, err = p.l1IndexerClient.GetTxOutput(fundingUtxo)
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
	if err := p.CleanChannelData(channelId); err != nil {
		return nil, err
	}

	c, err := p.GetChannelFromDKVS(channelId)
	if err != nil {
		Log.Errorf("getChannelFromDKVS %s failed, %v", channelId, err)
		return nil, err
	}

	p.SaveChannelToDB(c)
	p.EnableChannel(c)
	c.PeerRPC = p.GetPeerNodeClient(&c.ChannelInDB)
	Log.Infof("channel %s %d is restored from dkvs", channelId, c.CommitHeight)
	return c, nil
}
