package wallet

import (
	"bytes"
	"fmt"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func (p *Manager) GetUtxosForFeeV3WithMgr(utxoMgr *UtxoMgr, value int64,
	excludedUtxoMap map[string]bool, excludeRecentBlock bool) ([]*TxOutput, error) {
	if excludedUtxoMap == nil {
		excludedUtxoMap = make(map[string]bool)
	}
	return p.SelectUtxosForFeeV2WithMgr(utxoMgr, excludedUtxoMap, value, excludeRecentBlock)
}

func (p *Manager) getUtxosWithAssetV3(address string, amt *Decimal, assetName *swire.AssetName,
	excludedUtxoMap map[string]bool) ([]*TxOutput, error) {
	var weightEstimate utils.TxWeightEstimator
	selected, _, _, err := p.SelectUtxosForAsset(address, excludedUtxoMap, assetName,
		amt, &weightEstimate, false, false)
	if err != nil {
		return nil, err
	}
	return selected, nil
}

func (p *Manager) addInscribeChangeToUtxoMgr(utxoMgr *UtxoMgr, inscribe *InscribeResv) (*TxOutput, error) {
	if utxoMgr == nil || inscribe == nil {
		return nil, nil
	}
	change := inscribe.GetChangeOutput()
	if change == nil {
		return nil, nil
	}
	if change.HasAsset() {
		return nil, fmt.Errorf("inscribe change output %s has assets", change.OutPointStr)
	}
	pkScript, err := AddrToPkScript(utxoMgr.GetAddress(), GetChainParam())
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(change.OutValue.PkScript, pkScript) {
		return nil, fmt.Errorf("inscribe change output %s does not belong to %s",
			change.OutPointStr, utxoMgr.GetAddress())
	}
	AlignAsset(change, &indexer.ASSET_PLAIN_SAT)
	utxoMgr.AddOutput(&indexer.ASSET_PLAIN_SAT, change.ToAssetsInUtxo())
	return change, nil
}

func (p *Manager) GetUtxosForBRC20(fundingAddr, assetAddr string, fundingUtxoMgr *UtxoMgr,
	excludedUtxoMap map[string]bool, assetName *indexer.AssetName, amt *Decimal,
	preTxInputs []string, revealKey []byte, feeRate int64, lookInputs bool) (*TxOutput, *InscribeResv, error) {
	return p.getUtxosForBRC20(fundingAddr, assetAddr, fundingUtxoMgr, excludedUtxoMap,
		assetName, amt, preTxInputs, revealKey, feeRate, lookInputs)
}

func (p *Manager) getUtxosForBRC20(fundingAddr, assetAddr string, fundingUtxoMgr *UtxoMgr,
	excludedUtxoMap map[string]bool, assetName *indexer.AssetName, amt *Decimal,
	preTxInputs []string, revealKey []byte, feeRate int64, lookInputs bool) (*TxOutput, *InscribeResv, error) {
	totalAmt := p.GetAssetBalance(assetAddr, assetName)
	if totalAmt.Cmp(amt) < 0 {
		return nil, nil, fmt.Errorf("no enough asset, required %s but only %s", amt.String(), totalAmt.String())
	}

	utxos := p.l1IndexerClient.GetUtxoListWithTicker(assetAddr, assetName)
	var totalTransfer *Decimal
	for _, o := range utxos {
		output := o.ToTxOutput()
		d := output.GetAsset(assetName)
		if d.Cmp(amt) == 0 && len(preTxInputs) == 0 {
			return output, nil, nil
		}
		totalTransfer = totalTransfer.Add(d)
	}

	mintable := indexer.DecimalSub(totalAmt, totalTransfer)
	if mintable.Cmp(amt) < 0 {
		return nil, nil, fmt.Errorf("can't mint %s for asset %s", amt.String(), assetName.String())
	}

	if fundingUtxoMgr == nil {
		fundingUtxoMgr = NewUtxoMgr(fundingAddr, p.l1IndexerClient)
	}
	var defaultUtxos []*TxOutput
	for _, utxo := range preTxInputs {
		txOut, err := p.l1IndexerClient.GetTxOutput(utxo)
		if err != nil {
			Log.Errorf("GetTxOutFromRawTx %s failed, %v", utxo, err)
			return nil, nil, err
		}
		defaultUtxos = append(defaultUtxos, txOut)
	}

	inscribe, err := p.MintTransferV3_brc20(fundingUtxoMgr, assetAddr,
		excludedUtxoMap, assetName, amt, feeRate, defaultUtxos, len(defaultUtxos) != 0,
		revealKey, SCRIPT_TYPE_TAPROOTKEYSPEND, nil, false, lookInputs)
	if err != nil {
		Log.Errorf("MintTransfer_brc20 failed, %v", err)
		return nil, nil, err
	}
	PrintJsonTx(inscribe.CommitTx, "prev transfer commit")
	PrintJsonTx(inscribe.RevealTx, "prev transfer reveal")
	if _, err := p.addInscribeChangeToUtxoMgr(fundingUtxoMgr, inscribe); err != nil {
		return nil, nil, err
	}
	output := GenerateBRC20TransferOutput(inscribe.RevealTx, assetName, amt)
	return output, inscribe, nil
}
