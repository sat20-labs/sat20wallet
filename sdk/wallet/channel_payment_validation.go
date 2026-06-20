package wallet

import (
	"bytes"
	"fmt"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

func (p *Manager) GetCurrRevocationPrivKey(channel *Channel) (*secp256k1.PrivateKey, error) {
	commitSecret := channel.LocalWallet().GetCommitSecret(channel.PeerNodeId, uint32(channel.CommitHeight))
	if commitSecret == nil {
		return nil, fmt.Errorf("GetRevocationSecret failed")
	}

	return commitSecret, nil
}

func (p *Manager) GetCurrRevocationKey(channel *Channel) ([]byte, error) {
	commitSecret := channel.LocalWallet().GetCommitSecret(channel.PeerNodeId, uint32(channel.CommitHeight))
	if commitSecret == nil {
		return nil, fmt.Errorf("GetRevocationSecret failed")
	}

	return commitSecret.PubKey().SerializeCompressed(), nil
}

func (p *Manager) GetNextRevocationKey(channel *Channel) ([]byte, error) {
	nextCommitSecret := channel.LocalWallet().GetCommitSecret(channel.PeerNodeId, uint32(channel.CommitHeight+1))
	if nextCommitSecret == nil {
		return nil, fmt.Errorf("GetRevocationSecret failed")
	}

	return nextCommitSecret.PubKey().SerializeCompressed(), nil
}

func (p *Manager) CheckUtxosSatsNet(utxos []string) error {
	utxoMap := make(map[string]bool, len(utxos))
	for _, u := range utxos {
		if utxoMap[u] {
			return fmt.Errorf("same utxo exists")
		}
		utxoMap[u] = true
	}
	if u := p.GetUtxoLocker_SatsNet().CheckUtxos(utxos); u != "" {
		return fmt.Errorf("utxo %s has been locked", u)
	}
	result, err := p.GetIndexerClient_SatsNet().GetExistingUtxos(utxos)
	if err != nil {
		return err
	}
	if len(utxos) != len(result) {
		return fmt.Errorf("some utxo has spent")
	}
	return nil
}

func (p *Manager) AllowUnlock(resv *PaymentReservation, feeutxos []string) error {
	var requiredAmt *Decimal
	for _, amt := range resv.DestAmt {
		if requiredAmt == nil {
			requiredAmt = amt.Clone()
		} else {
			requiredAmt = requiredAmt.Add(amt)
		}
	}

	err := resv.Channel.HasEnoughAssetForUnlock(resv.AssetName, requiredAmt,
		len(feeutxos) != 0, resv.IsInitiator == resv.Channel.IsInitiator)
	if err != nil {
		return err
	}

	utxos, err := resv.Channel.GetChannelUtxosWithAsset_SatsNet(requiredAmt, resv.AssetName)
	if err != nil {
		return err
	}

	var feeUtxosInfo []*TxOutput_SatsNet
	if len(feeutxos) != 0 {
		err := p.CheckUtxosSatsNet(feeutxos)
		if err != nil {
			return err
		}

		plainAmt := int64(0)
		for _, utxo := range feeutxos {
			txOut, err := p.GetIndexerClient_SatsNet().GetTxOutput(utxo)
			if err != nil {
				return fmt.Errorf("GetTxOutput %s failed, %v", utxo, err)
			}
			txOutSatsNet := OutputToSatsNet(txOut)
			if err := ValidateSatsNetInputFromInitiator(resv.Channel, resv.IsInitiator, txOutSatsNet, "unlock fee"); err != nil {
				return err
			}

			value := txOut.GetPlainSat()
			if value > 0 {
				feeUtxosInfo = append(feeUtxosInfo, txOutSatsNet)
				plainAmt += value
				if plainAmt >= DEFAULT_FEE_SATSNET {
					break
				}
			}
		}
		if plainAmt < DEFAULT_FEE_SATSNET {
			return fmt.Errorf("no enough fee")
		}
	}

	resv.Utxos = utxos
	resv.Fees = feeUtxosInfo

	return nil
}

func (p *Manager) AllowLock(resv *PaymentReservation, utxos, fees []string, feeValue int64) error {
	err := p.CheckUtxosSatsNet(append(utxos, fees...))
	if err != nil {
		return err
	}
	fromInitiator := resv.Channel.IsInitiator == resv.IsInitiator

	assetName := resv.AssetName
	if resv.Amt.Sign() > 0 {
		if resv.Channel != nil {
			err = resv.Channel.HasEnoughCapacityForLock(assetName, resv.Amt, true, fromInitiator)
			if err != nil {
				return err
			}
		}
	}

	fee := indexer.NewDefaultDecimal(feeValue)
	expectedValue := resv.Amt.Clone()
	if len(fees) == 0 && indexer.IsPlainAsset(&assetName.AssetName) {
		expectedValue = expectedValue.Add(fee)
	}

	lockUtxosInfo := make([]*TxOutput_SatsNet, 0)
	plainAmt := int64(0)
	var assetAmt *indexer.Decimal
	for i := 0; i < len(utxos); i++ {
		utxo := utxos[i]
		txOut, err := p.GetIndexerClient_SatsNet().GetTxOutput(utxo)
		if err != nil {
			return fmt.Errorf("GetTxOutput %s failed, %v", utxo, err)
		}
		txOutSatsNet := OutputToSatsNet(txOut)
		if err := ValidateSatsNetInputFromInitiator(resv.Channel, resv.IsInitiator, txOutSatsNet, "lock input"); err != nil {
			return err
		}

		plainAmt += txOut.GetPlainSat()
		amt := txOut.GetAsset(&assetName.AssetName)
		assetAmt = indexer.DecimalAdd(assetAmt, amt)
		lockUtxosInfo = append(lockUtxosInfo, txOutSatsNet)

		if resv.Amt.Sign() > 0 && assetAmt.Cmp(expectedValue) >= 0 {
			break
		}
	}

	if resv.Amt.Sign() == 0 {
		if len(fees) == 0 && indexer.IsPlainAsset(&assetName.AssetName) {
			resv.Amt = indexer.DecimalSub(assetAmt, fee)
		} else {
			resv.Amt = assetAmt
		}

		if resv.Channel != nil {
			err = resv.Channel.HasEnoughCapacityForLock(assetName, resv.Amt, true, fromInitiator)
			if err != nil {
				return err
			}
		}
	} else {
		if assetAmt.Cmp(expectedValue) < 0 {
			return fmt.Errorf("not enough sats in input utxos, required %d but only %d", expectedValue, assetAmt)
		}
	}
	if indexer.IsPlainAsset(&assetName.AssetName) {
		plainAmt -= resv.Amt.Int64()
	}

	var feeUtxosInfo []*TxOutput_SatsNet
	if len(fees) > 0 {
		for _, utxo := range fees {
			txOut, err := p.GetIndexerClient_SatsNet().GetTxOutput(utxo)
			if err != nil {
				return fmt.Errorf("GetTxOutput %s failed, %v", utxo, err)
			}
			txOutSatsNet := OutputToSatsNet(txOut)
			if err := ValidateSatsNetInputFromInitiator(resv.Channel, resv.IsInitiator, txOutSatsNet, "lock fee"); err != nil {
				return err
			}

			value := txOut.GetPlainSat()
			if value > 0 {
				feeUtxosInfo = append(feeUtxosInfo, txOutSatsNet)
				plainAmt += value
				if plainAmt >= feeValue {
					break
				}
			}
		}
		if plainAmt < feeValue {
			return fmt.Errorf("no enough fee")
		}
	}

	resv.Utxos = lockUtxosInfo
	resv.Fees = feeUtxosInfo

	return nil
}

func ValidateSatsNetInputFromInitiator(channel *Channel, localInitiated bool, output *TxOutput_SatsNet, role string) error {
	if channel == nil || output == nil {
		return nil
	}
	initiatorPkScript := channel.GetRemotePkScript()
	if localInitiated {
		initiatorPkScript = channel.GetLocalPkScript()
	}
	channelPkScript := channel.GetChannelPkScript()
	if bytes.Equal(output.OutValue.PkScript, channelPkScript) {
		return fmt.Errorf("%s utxo %s belongs to channel address", role, output.OutPointStr)
	}
	if !bytes.Equal(output.OutValue.PkScript, initiatorPkScript) {
		return fmt.Errorf("%s utxo %s does not belong to action initiator", role, output.OutPointStr)
	}
	return nil
}
