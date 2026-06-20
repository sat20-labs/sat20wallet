package wallet

import (
	"bytes"
	"fmt"
	"sort"

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

	brc20Inputs := make([]*indexer.AssetInfo, 0)
	brc20Outputs := channel.GetFundingOutputWithProtocol(indexer.PROTOCOL_NAME_BRC20)
	for _, assetToOutput := range brc20Outputs {
		name := *assetToOutput.AssetName

		local := channel.LocalCommitment.LocalBalance[name].Clone()
		remote := channel.LocalCommitment.RemoteBalance[name].Clone()

		if local.Sign() != 0 {
			brc20Inputs = append(brc20Inputs, &indexer.AssetInfo{
				Name:       assetToOutput.AssetName.AssetName,
				Amount:     *local,
				BindingSat: 0,
			})
		}
		if remote.Sign() != 0 {
			brc20Inputs = append(brc20Inputs, &indexer.AssetInfo{
				Name:       assetToOutput.AssetName.AssetName,
				Amount:     *remote,
				BindingSat: 0,
			})
		}
	}
	sort.Slice(brc20Inputs, func(i, j int) bool {
		return brc20Inputs[i].Name.String() < brc20Inputs[j].Name.String()
	})

	prevFetcher := make(map[string]*TxOutput)
	for _, tx := range channel.LocalCommitment.PrevTxs {
		output := indexer.GenerateTxOutput(tx, 0)
		prevFetcher[output.OutPointStr] = output
		if len(tx.TxOut) == 2 {
			output2 := indexer.GenerateTxOutput(tx, 1)
			prevFetcher[output2.OutPointStr] = output2
		}
	}

	var input *TxOutput
	for _, txIn := range tx.TxIn {
		utxo := txIn.PreviousOutPoint.String()
		fundingOutput := channel.GetFundingOutput(utxo)
		if fundingOutput == nil {
			var ok bool
			fundingOutput, ok = prevFetcher[utxo]
			if !ok {
				return nil, fmt.Errorf("can't find previous output %s", utxo)
			}

			if fundingOutput.OutValue.Value == 330 &&
				bytes.Equal(fundingOutput.OutValue.PkScript, channel.GetChannelPkScript()) {
				if len(brc20Inputs) == 0 {
					return nil, fmt.Errorf("no brc20 asset for previous output %s", utxo)
				}
				in := brc20Inputs[0]
				brc20Inputs = utils.RemoveIndex(brc20Inputs, 0)

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
		if input == nil {
			input = fundingOutput
		} else {
			input.Append(fundingOutput)
		}
		result.InputAssets = append(result.InputAssets, fundingOutput.ToAssetsInUtxo())
	}

	for i, txOut := range tx.TxOut {
		curr, nextInput, err := input.Cut(txOut.Value)
		if err != nil {
			return nil, err
		}
		input = nextInput
		if curr == nil {
			return nil, fmt.Errorf("inputs have no enough asset for output %d", i)
		}
		result.OutputAssets = append(result.OutputAssets, curr.ToAssetsInUtxo())
	}

	return result, nil
}
