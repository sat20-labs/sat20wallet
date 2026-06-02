package wallet

import (
	"encoding/hex"
	"fmt"

	indexer "github.com/sat20-labs/indexer/common"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	sindexerwire "github.com/sat20-labs/satoshinet/indexer/rpcserver/wire"
)

func (p *Manager) MinerUnstake(feeRate int64) (string, int64, error) {
	return p.MinnerUnstake(feeRate)
}

func (p *Manager) MinnerUnstake(feeRate int64) (string, int64, error) {
	Log.Infof("MinnerUnstake")
	if p.wallet == nil {
		return "", -1, fmt.Errorf("wallet is not created/unlocked")
	}
	if p.serverNode == nil || p.serverNode.client == nil {
		return "", -1, fmt.Errorf("server node is not ready")
	}
	if !p.checkSuperNodeStatus() {
		return "", -1, fmt.Errorf("peer is offline")
	}
	if p.hasLocalAction(LOCAL_ACTION_UNSTAKE_MINER) {
		return "", -1, fmt.Errorf("same type of resv exists")
	}

	param, err := p.BuildUnstakeMinerLocalActionParam()
	if err != nil {
		return "", 0, err
	}

	return p.PerformLocalAction(LOCAL_ACTION_UNSTAKE_MINER, param, feeRate)
}

func (p *Manager) BuildUnstakeMinerLocalActionParam() (*LocalActionParam_UnstakeMiner, error) {
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	localPubkey := p.wallet.GetPubKey().SerializeCompressed()
	minerInfo, err := p.getStakeMinerInfo(localPubkey)
	if err != nil {
		return nil, err
	}
	if minerInfo.ChildCount > 0 {
		return nil, fmt.Errorf("core node still has child miners")
	}

	asset := ParseAssetString(minerInfo.AssetName)
	if asset == nil {
		return nil, fmt.Errorf("invalid asset name %s", minerInfo.AssetName)
	}
	tickerInfo := p.getTickerInfo(asset)
	if tickerInfo == nil {
		return nil, fmt.Errorf("can't get ticker %s info", asset.String())
	}
	name := GetAssetName(tickerInfo)
	dAmt, err := indexer.NewDecimalFromString(minerInfo.AssetAmt, tickerInfo.Divisibility)
	if err != nil {
		return nil, err
	}

	value, err := p.checkUnstakeChannelAssets(minerInfo.ChannelAddr, name, dAmt)
	if err != nil {
		return nil, err
	}

	return &LocalActionParam_UnstakeMiner{
		MinerInfo:   minerInfo,
		AssetName:   name,
		Amt:         dAmt,
		Value:       value,
		ContractURL: "",
	}, nil
}

func (p *Manager) localActionUnstakeMinerStart(resv *LocalActionPerformData) error {
	param, ok := resv.ActionParam.(*LocalActionParam_UnstakeMiner)
	if !ok {
		return fmt.Errorf("invalid parameter LocalActionParam_UnstakeMiner")
	}
	if param.MinerInfo == nil {
		return fmt.Errorf("invalid parameter LocalActionParam_UnstakeMiner")
	}
	if param.Value > DEFAULT_FEE_SATSNET {
		pubkey, err := hex.DecodeString(param.MinerInfo.ServerNode)
		if err != nil {
			return err
		}
		serverAddr, err := indexer.GetP2TRAddressFromPubkey(pubkey, GetChainParam())
		if err != nil {
			return err
		}
		foundationAddr, err := indexer.GetBootstrapAddress(GetChainParam())
		if err != nil {
			return err
		}

		value := param.Value - DEFAULT_FEE_SATSNET
		lpValue, serverValue, foundationValue := GetLpProfitValue(value, serverAddr == foundationAddr)
		dest := []*SendAssetInfo{{Address: p.wallet.GetAddress(), Value: lpValue}}
		if serverValue > 0 {
			dest = append(dest, &SendAssetInfo{Address: serverAddr, Value: serverValue})
		}
		if foundationValue > 0 {
			dest = append(dest, &SendAssetInfo{Address: foundationAddr, Value: foundationValue})
		}

		txId, err := p.CoBatchSendV4_SatsNet(p.wallet, dest, indexer.ASSET_PLAIN_SAT.String(),
			"unstake", "profit", param.MinerInfo.ChannelAddr, nil)
		if err != nil {
			return err
		}
		resv.ActionResvs = append(resv.ActionResvs, &SubActionInfo{
			ActionType: "profit",
			TxId:       txId,
			MoreData:   param.AssetName.String(),
		})
		resv.TxId = txId
		resv.IsL1Tx = false
		resv.Status = RS_PERFORM_ACTION_TX_BROADCASTED
		return nil
	}
	return p.localActionUnstakeMinerStartNextStep(resv)
}

func (p *Manager) localActionInnerStatusUnstakeMiner(resv *LocalActionPerformData) error {
	if len(resv.ActionResvs) == 0 {
		return fmt.Errorf("no sub actions")
	}
	currResv := resv.ActionResvs[len(resv.ActionResvs)-1]
	switch currResv.ActionType {
	case "profit", "unstake":
		return p.localActionUnstakeMinerStartNextStep(resv)
	default:
		return fmt.Errorf("invalid action %s", currResv.ActionType)
	}
}

func (p *Manager) localActionUnstakeMinerStartNextStep(resv *LocalActionPerformData) error {
	started, err := p.localActionUnstakeMinerStartUnstake(resv)
	if err != nil || started {
		return err
	}
	p.completeUnstakeMinerLocalAction(resv)
	return nil
}

func (p *Manager) localActionUnstakeMinerStartUnstake(resv *LocalActionPerformData) (bool, error) {
	param, ok := resv.ActionParam.(*LocalActionParam_UnstakeMiner)
	if !ok {
		return false, fmt.Errorf("invalid parameter LocalActionParam_UnstakeMiner")
	}
	if param.MinerInfo == nil {
		return false, fmt.Errorf("invalid parameter LocalActionParam_UnstakeMiner")
	}
	summary := p.l1IndexerClient.GetAssetSummaryWithAddress(param.MinerInfo.ChannelAddr)
	if summary == nil {
		return false, fmt.Errorf("can't get channel asset summary %s", param.MinerInfo.ChannelAddr)
	}
	var amt *Decimal
	for _, asset := range summary.Data {
		if asset.Amount.Sign() == 0 {
			continue
		}
		if asset.Name == param.AssetName.AssetName {
			amt = &asset.Amount
			continue
		}
		if asset.Name == indexer.ASSET_ALL_SAT {
			continue
		}

		dest := []*SendAssetInfo{{
			Address:   p.wallet.GetAddress(),
			Value:     0,
			AssetName: &asset.Name,
			AssetAmt:  asset.Amount.Clone(),
		}}
		txId, _, err := p.CoBatchSendAssetsV3FromAddress(p.wallet, dest, asset.Name.String(), resv.FeeRate, nil,
			"unstake", "unstake", param.MinerInfo.ChannelAddr, true)
		if err != nil {
			return false, err
		}
		resv.ActionResvs = append(resv.ActionResvs, &SubActionInfo{
			ActionType: "unstake",
			TxId:       txId,
			MoreData:   asset.Name.String(),
		})
		resv.TxId = txId
		resv.IsL1Tx = true
		resv.Status = RS_PERFORM_ACTION_TX_BROADCASTED
		return true, nil
	}

	if amt != nil && amt.Sign() > 0 {
		dest := []*SendAssetInfo{{
			Address:   p.wallet.GetAddress(),
			Value:     0,
			AssetName: &param.AssetName.AssetName,
			AssetAmt:  amt.Clone(),
		}}

		amtL2 := p.GetAssetBalance_SatsNet(param.MinerInfo.ChannelAddr, &param.AssetName.AssetName)
		if amtL2 == nil || amtL2.Sign() == 0 {
			return false, fmt.Errorf("no staking asset")
		}

		invoice, err := sindexer.CreateStakeInvoice(&param.AssetName.AssetName, amtL2)
		if err != nil {
			return false, err
		}
		nullDataScript, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_UNSTAKE, invoice)
		if err != nil {
			return false, err
		}

		txId, _, err := p.CoBatchSendV4(p.wallet, dest, param.AssetName.String(), resv.FeeRate,
			"unstake", "unstake", param.MinerInfo.ChannelAddr, nullDataScript, true, amtL2, false, true)
		if err != nil {
			return false, err
		}
		resv.ActionResvs = append(resv.ActionResvs, &SubActionInfo{
			ActionType: "unstake",
			TxId:       txId,
			MoreData:   param.AssetName.String(),
		})
		resv.TxId = txId
		resv.IsL1Tx = true
		resv.Status = RS_PERFORM_ACTION_TX_BROADCASTED
		return true, nil
	}

	return false, nil
}

func (p *Manager) completeUnstakeMinerLocalAction(resv *LocalActionPerformData) {
	_, err := p.l2IndexerClient.GetMinerInfo(resv.ReqPubKey)
	if err == nil {
		Log.Errorf("still a miner after unstake")
		return
	}
	resv.Status = RS_PERFORM_ACTION_COMPLETED
	Log.Infof("localActionInnerStatus_UnstakeMiner %d finished", resv.Id)
}

func (p *Manager) getStakeMinerInfo(pubkey []byte) (*sindexerwire.MinerInfo, error) {
	minerInfo, err := p.l2IndexerClient.GetMinerInfo(pubkey)
	if err != nil {
		return nil, err
	}
	if minerInfo == nil {
		return nil, fmt.Errorf("miner info not found")
	}
	return minerInfo, nil
}

func (p *Manager) checkUnstakeChannelAssets(channelId string, stakeAsset *AssetName, stakeAmt *Decimal) (int64, error) {
	l1Stake := p.GetAssetBalance(channelId, &stakeAsset.AssetName)
	if l1Stake == nil || l1Stake.Cmp(stakeAmt) < 0 {
		have := "0"
		if l1Stake != nil {
			have = l1Stake.String()
		}
		return 0, fmt.Errorf("channel stake asset %s insufficient on L1, require %s but only %s",
			stakeAsset.String(), stakeAmt.String(), have)
	}

	assets := p.l2IndexerClient.GetAssetSummaryWithAddress(channelId)
	if assets == nil {
		return 0, fmt.Errorf("can't get channel asset summary %s on L2", channelId)
	}
	var l2Stake *Decimal
	var value int64
	for _, u := range assets.Data {
		if u.Name.String() == stakeAsset.String() {
			l2Stake = &u.Amount
		}
		if u.Name == indexer.ASSET_PLAIN_SAT {
			value = u.Amount.Int64()
		}
	}
	if l2Stake == nil || l2Stake.Cmp(stakeAmt) < 0 {
		have := "0"
		if l2Stake != nil {
			have = l2Stake.String()
		}
		return 0, fmt.Errorf("channel stake asset %s insufficient on L2, require %s but only %s",
			stakeAsset.String(), stakeAmt.String(), have)
	}

	return value, nil
}

func GetLpProfitValue(value int64, coreNode bool) (int64, int64, int64) {
	var lpValue, serverValue, foundationValue int64
	if coreNode {
		foundationValue = value * int64(PROFIT_SHARE_FOUNDATION) / 100
		lpValue = value - foundationValue
	} else {
		lpValue = value * int64(PROFIT_SHARE_LP) / 100
		foundationValue = value * int64(PROFIT_SHARE_FOUNDATION) / 100
		serverValue = value - lpValue - foundationValue
	}
	return lpValue, serverValue, foundationValue
}
