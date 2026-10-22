package wallet

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

func (p *Manager) GetCommitTxAssetInfo(channelId string) (*TxAssetInfo, error) {
	channel := p.GetChannel(channelId)
	if channel == nil {
		return nil, fmt.Errorf("can't find channel %s", channelId)
	}

	channel.Mutex.RLock()
	defer channel.Mutex.RUnlock()

	tx := channel.LocalCommitment.CommitTx
	txHex, err := EncodeMsgTx(tx)
	if err != nil {
		return nil, err
	}
	result := &TxAssetInfo{
		TxId:         tx.TxID(),
		TxHex:        txHex,
		InputAssets:  make([]*indexer.AssetsInUtxo, 0),
		OutputAssets: make([]*indexer.AssetsInUtxo, 0),
	}

	materializedInputs := make([]*indexer.AssetInfo, 0)
	for _, protocol := range stpAssetProtocols.ordered {
		if !protocol.Policy(nil).AccountBased {
			continue
		}
		for _, assetToOutput := range channel.GetFundingOutputWithProtocol(protocol.Protocol()) {
			name := *assetToOutput.AssetName

			local := channel.LocalCommitment.LocalBalance[name].Clone()
			remote := channel.LocalCommitment.RemoteBalance[name].Clone()

			if local.Sign() != 0 {
				materializedInputs = append(materializedInputs, &indexer.AssetInfo{
					Name:       assetToOutput.AssetName.AssetName,
					Amount:     *local,
					BindingSat: 0,
				})
			}
			if remote.Sign() != 0 {
				materializedInputs = append(materializedInputs, &indexer.AssetInfo{
					Name:       assetToOutput.AssetName.AssetName,
					Amount:     *remote,
					BindingSat: 0,
				})
			}
		}
	}

	sort.Slice(materializedInputs, func(i, j int) bool {
		return materializedInputs[i].Name.String() < materializedInputs[j].Name.String()
	})

	prevFetcher := make(map[string]*TxOutput)
	for _, tx := range channel.LocalCommitment.PrevTxs {
		for n := range tx.TxOut {
			output := indexer.GenerateTxOutput(tx, n)
			prevFetcher[output.OutPointStr] = output
		}
	}
	plainStubs := channel.GetStubUtxoList()

	var input *TxOutput
	var completeInputs []*TxOutput
	for _, txIn := range tx.TxIn {
		utxo := txIn.PreviousOutPoint.String()
		fundingOutput := channel.GetFundingOutput(utxo)
		if fundingOutput == nil {
			for _, stub := range plainStubs {
				if stub.OutPointStr == utxo {
					fundingOutput = stub.Clone()
					break
				}
			}
		}
		if fundingOutput == nil {
			var ok bool
			fundingOutput, ok = prevFetcher[utxo]
			if !ok {
				return nil, fmt.Errorf("can't find previous output %s", utxo)
			}

			if fundingOutput.OutValue.Value == 330 &&
				bytes.Equal(fundingOutput.OutValue.PkScript, channel.GetChannelPkScript()) {
				if len(materializedInputs) == 0 {
					return nil, fmt.Errorf("no materialized asset for previous output %s", utxo)
				}
				in := materializedInputs[0]
				materializedInputs = utils.RemoveIndex(materializedInputs, 0)

				fundingOutput.Assets = indexer.TxAssets{*in}
				fundingOutput.Offsets = map[indexer.AssetName]indexer.AssetOffsets{
					in.Name: {
						{
							Start: 0,
							End:   1,
						},
					},
				}
				fundingOutput.SatBindingMap = map[int64]*indexer.AssetInfo{0: in.Clone()}
			}
		}
		completeInputs = append(completeInputs, fundingOutput.Clone())
		ordinal := fundingOutput.Clone()
		for _, asset := range fundingOutput.Assets {
			protocol := stpAssetProtocols.byName[asset.Name.Protocol]
			_, classified := protocol.(STPAssetOutputClassifier)
			_, native := protocol.(STPAssetChannelProtocol)
			if classified || native {
				filtered := ordinal.Assets[:0]
				for _, candidate := range ordinal.Assets {
					if candidate.Name != asset.Name {
						filtered = append(filtered, candidate)
					}
				}
				ordinal.Assets = filtered
				delete(ordinal.Offsets, asset.Name)
			}
		}
		if input == nil {
			input = ordinal
		} else if err := input.Append(ordinal); err != nil {
			return nil, err
		}
		result.InputAssets = append(result.InputAssets, fundingOutput.ToAssetsInUtxo())
	}

	var classifiedOutputs []*TxOutput
	for i, txOut := range tx.TxOut {
		curr, nextInput, err := input.Cut(txOut.Value)
		if err != nil {
			return nil, err
		}
		input = nextInput
		if curr == nil {
			return nil, fmt.Errorf("inputs have no enough asset for output %d", i)
		}
		curr.OutPointStr = tx.TxID() + ":" + strconv.Itoa(i)
		curr.OutValue.PkScript = append([]byte(nil), txOut.PkScript...)
		classifiedOutputs = append(classifiedOutputs, curr)
	}

	for _, protocol := range stpAssetProtocols.ordered {
		if classifier, ok := protocol.(STPAssetOutputClassifier); ok {
			if err := classifier.ClassifyOutputs(p, tx, completeInputs, classifiedOutputs); err != nil {
				return nil, err
			}
		}
		if native, ok := protocol.(STPAssetChannelProtocol); ok {
			proof := channel.LocalCommitment.AssetProofs[protocol.Protocol()]
			if len(proof) == 0 {
				if channel.HasProtocolAsset(protocol.Protocol()) {
					return nil, fmt.Errorf("missing native output proof")
				}
				continue
			}
			verified, err := native.VerifyChannelProof(p, channel, tx, proof, STPOpCommit)
			if err != nil {
				return nil, err
			}
			for _, view := range verified {
				index := int(view.OutPoint().Index)
				if index >= len(classifiedOutputs) || view.OutPoint().Hash != tx.TxHash() {
					return nil, fmt.Errorf("native output identity mismatch")
				}
				for _, asset := range view.Assets {
					if asset.Name.Protocol != protocol.Protocol() {
						return nil, fmt.Errorf("native output contains another protocol")
					}
					classifiedOutputs[index].Assets = append(classifiedOutputs[index].Assets, asset)
				}
			}
		}
	}
	for _, output := range classifiedOutputs {
		result.OutputAssets = append(result.OutputAssets, output.ToAssetsInUtxo())
	}
	return result, nil
}
