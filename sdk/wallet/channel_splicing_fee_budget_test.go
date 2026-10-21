package wallet

import (
	"fmt"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/runes/runestone"
	"github.com/stretchr/testify/require"
)

// Exercise the production preflight without signing, broadcasting, or writing
// a channel. Fee candidates are already selected by the caller.
func TestSplicingInFeeBudgetIncludesAllStubOutputsAndDustChange(t *testing.T) {
	for _, spec := range []struct {
		name  string
		stubs int
		fees  []int64
		short bool
	}{
		{"two_stubs_real_pwa_amounts", 2, []int64{1000, 1000}, false},
		{"dust_change_needs_second_selected_fee", 0, []int64{500, 10000}, false},
		{"zero_stubs_large_fee_control", 0, []int64{10000}, false},
		{"one_stub_large_fee_control", 1, []int64{10000}, false},
		{"two_stubs_large_fee_must_not_overcharge", 2, []int64{10000}, false},
		{"truly_insufficient_fee_rejected", 2, []int64{100}, true},
	} {
		t.Run(spec.name, func(t *testing.T) {
			name := &AssetName{AssetName: indexer.AssetName{Protocol: "ordx", Type: "f", Ticker: "pwapos"}, N: 1}
			var inputs, fees []*TxOutput
			for i := 0; i < 2; i++ {
				output := indexer.NewTxOutput(400)
				output.OutPointStr = fmt.Sprintf("%064x:0", i+1)
				output.OutValue.PkScript = []byte{0x51}
				output.Assets = indexer.TxAssets{{Name: name.AssetName, Amount: *indexer.NewDefaultDecimal(400), BindingSat: 1}}
				output.Offsets[name.AssetName] = indexer.AssetOffsets{{Start: 0, End: 400}}
				inputs = append(inputs, output)
			}
			for i, value := range spec.fees {
				output := indexer.NewTxOutput(value)
				output.OutPointStr = fmt.Sprintf("%064x:0", i+3)
				output.OutValue.PkScript = []byte{0x51}
				fees = append(fees, output)
			}
			resv := &SplicingReservation{SplicingDataInDB: SplicingDataInDB{
				AssetName: name,
				Amt:       indexer.NewDefaultDecimal(700),
			}, RevocationInfo: RevocationInfo{FeeRate: 1}}
			err := (&Manager{}).AllowSplicingInV2(resv, inputs, fees, spec.stubs, 330)
			if spec.short {
				require.ErrorContains(t, err, "no enough sats to pay fee")
				return
			}
			require.NoError(t, err)
			require.EqualValues(t, 700, resv.SplicingValue)
			require.EqualValues(t, 330, resv.SplicingChange)
			selectedValue := int64(800)
			for _, input := range resv.Fees {
				selectedValue += input.Value()
			}
			actualFee := selectedValue - resv.SplicingValue - resv.SplicingChange - int64(spec.stubs)*330 - resv.FeeChange
			require.GreaterOrEqual(t, actualFee, resv.RequiredFee, "all actual outputs must fit the selected input budget")
			if resv.FeeChange > 0 {
				require.Equal(t, resv.RequiredFee, actualFee, "stub cost must not be silently charged twice")
			}
		})
	}
}

func TestSplicingInFeeBudgetReviewEdgeCases(t *testing.T) {
	t.Run("dust_change_is_charged_once_without_stub", func(t *testing.T) {
		name := feeBudgetReviewAssetName()
		inputs := feeBudgetReviewAssetInputs(name, nil)
		fees := []*TxOutput{feeBudgetReviewPlainInput(3, 700, []byte{0x51})}
		resv := feeBudgetReviewReservation(name, 700, nil)

		err := (&Manager{}).AllowSplicingInV2(resv, inputs, fees, 0, 330)
		require.NoError(t, err)
		require.Empty(t, resv.StubUtxo)
		require.EqualValues(t, 700, resv.SplicingValue)
		require.EqualValues(t, 330, resv.SplicingChange)
		require.Zero(t, resv.FeeChange)
		require.Len(t, resv.Fees, 1)

		actualFee := int64(800+700) - resv.SplicingValue - resv.SplicingChange
		require.EqualValues(t, 470, actualFee, "the 230-sat dust shortfall is part of the actual fee")
		require.GreaterOrEqual(t, actualFee, resv.RequiredFee)
	})

	t.Run("all_selected_fee_inputs_are_conserved_and_change_returns_to_initiator", func(t *testing.T) {
		name := feeBudgetReviewAssetName()
		manager, channel := feeBudgetReviewChannel(t)
		localScript := channel.GetLocalPkScript()
		inputs := feeBudgetReviewAssetInputs(name, localScript)
		fees := []*TxOutput{
			feeBudgetReviewPlainInput(3, 10_000, localScript),
			feeBudgetReviewPlainInput(4, 1_000, localScript),
		}
		resv := feeBudgetReviewReservation(name, 700, channel)

		err := manager.AllowSplicingInV2(resv, inputs, fees, 2, 330)
		require.NoError(t, err)
		require.Len(t, resv.Fees, 2, "both already-selected fee inputs must be accounted for")
		require.Greater(t, resv.FeeChange, int64(330))
		for _, fee := range resv.Fees {
			require.Equal(t, localScript, fee.OutValue.PkScript, "fee inputs must belong to the initiator")
		}

		tx, _, err := manager.CreateSplicingInTx(resv)
		require.NoError(t, err)
		require.NotEmpty(t, tx.TxOut)
		feeChange := tx.TxOut[len(tx.TxOut)-1]
		require.EqualValues(t, resv.FeeChange, feeChange.Value)
		require.Equal(t, localScript, feeChange.PkScript, "fee change must return to the fee-input owner")

		inputValue := channel.GetChanPoint().Value()
		for _, input := range resv.SplicingInputs {
			inputValue += input.Value()
		}
		for _, input := range resv.Fees {
			inputValue += input.Value()
		}
		var outputValue int64
		for _, output := range tx.TxOut {
			outputValue += output.Value
		}
		require.EqualValues(t, resv.RequiredFee, inputValue-outputValue,
			"all selected values must be conserved with only the required fee removed")
	})

	t.Run("remote_payment_key_fee_input_is_rejected", func(t *testing.T) {
		name := feeBudgetReviewAssetName()
		manager, channel := feeBudgetReviewChannel(t)
		localScript := channel.GetLocalPkScript()
		remoteScript := channel.GetRemotePkScript()
		require.NotEmpty(t, localScript)
		require.NotEmpty(t, remoteScript)
		require.NotEqual(t, localScript, remoteScript)
		inputs := feeBudgetReviewAssetInputs(name, localScript)
		fees := []*TxOutput{feeBudgetReviewPlainInput(3, 10_000, remoteScript)}
		resv := feeBudgetReviewReservation(name, 700, channel)

		err := manager.AllowSplicingInV2(resv, inputs, fees, 2, 330)
		require.ErrorContains(t, err, "can't use this utxo")
		require.Empty(t, resv.Fees, "a peer-owned fee input must not be selected")
	})
}

func TestSplicingInFeeBudgetProtocolConstruction(t *testing.T) {
	for _, spec := range []struct {
		name            string
		protocol        string
		ticker          string
		inputAmount     int64
		transferAmount  int64
		inputValue      int64
		stubCount       int
		stubValue       int64
		wantAssetChange int64
	}{
		{name: "plain_sats", inputAmount: 800, transferAmount: 800, inputValue: 800},
		{name: "brc20_full_transfer", protocol: indexer.PROTOCOL_NAME_BRC20, ticker: "ordi", inputAmount: 100, transferAmount: 100, inputValue: 330, stubCount: 2, stubValue: STUB_VALUE_BRC20},
		{name: "runes_partial_transfer", protocol: indexer.PROTOCOL_NAME_RUNES, ticker: "FEE•TEST", inputAmount: 1000, transferAmount: 400, inputValue: 330, stubCount: 1, stubValue: 330, wantAssetChange: 600},
	} {
		t.Run(spec.name, func(t *testing.T) {
			name := feeBudgetProtocolAssetName(spec.protocol, spec.ticker)
			manager, channel := feeBudgetReviewChannel(t)
			localScript := channel.GetLocalPkScript()
			channelScript := channel.GetChannelPkScript()
			remoteScript := channel.GetRemotePkScript()

			if spec.protocol == indexer.PROTOCOL_NAME_RUNES {
				manager.mutex.Lock()
				if manager.tickerInfoMap == nil {
					manager.tickerInfoMap = make(map[string]*indexer.TickerInfo)
				}
				manager.tickerInfoMap[name.String()] = &indexer.TickerInfo{
					AssetName:   name.AssetName,
					DisplayName: "1:1",
				}
				manager.mutex.Unlock()
			}

			assetInput := feeBudgetProtocolAssetInput(1, name, spec.inputAmount, spec.inputValue, localScript)
			fees := []*TxOutput{
				feeBudgetReviewPlainInput(2, 10_000, localScript),
				feeBudgetReviewPlainInput(3, 1_500, localScript),
			}
			stubCount, stubValue := channel.NeedStubUtxo(name)
			require.Equal(t, spec.stubCount, stubCount)
			require.Equal(t, spec.stubValue, stubValue)

			resv := feeBudgetReviewReservation(name, spec.transferAmount, channel)
			require.False(t, NeedStubUtxoForInputAsset(name, resv.Amt), "these protocol amounts do not require an input stub")
			require.NoError(t, manager.AllowSplicingInV2(resv, []*TxOutput{assetInput}, fees, stubCount, stubValue))
			require.EqualValues(t, spec.transferAmount, resv.SplicingAmt.Int64())
			require.Len(t, resv.Fees, len(fees), "every already-selected fee input must be included")
			require.EqualValues(t, spec.stubCount, stubCount)
			require.Greater(t, resv.FeeChange, int64(330), "the selected inputs leave spendable initiator change")
			for i := range fees {
				require.Equal(t, fees[i].OutPointStr, resv.Fees[i].OutPointStr)
				require.Equal(t, localScript, resv.Fees[i].OutValue.PkScript, "fee inputs must belong to the initiator")
			}

			tx, _, err := manager.CreateSplicingInTx(resv)
			require.NoError(t, err)
			require.False(t, resv.StubIsSet)
			require.NotEmpty(t, tx.TxOut)
			require.Equal(t, channelScript, tx.TxOut[0].PkScript, "the transferred asset output must go to the channel")
			require.EqualValues(t, spec.transferAmount, resv.SplicingOutput.GetAsset(&name.AssetName).Int64())

			assetChange := int64(0)
			if !IsPlainAsset(name) && resv.SplicingChangeOutput != nil {
				if amount := resv.SplicingChangeOutput.GetAsset(&name.AssetName); amount != nil {
					assetChange = amount.Int64()
				}
			}
			require.EqualValues(t, spec.wantAssetChange, assetChange)
			require.EqualValues(t, spec.inputAmount, resv.SplicingOutput.GetAsset(&name.AssetName).Int64()+assetChange,
				"transferred asset and change must conserve the selected asset amount")

			if spec.protocol == indexer.PROTOCOL_NAME_BRC20 {
				require.Len(t, resv.SplicingOutput.SatBindingMap, 1, "the full BRC20 transfer inscription must remain with the channel output")
				require.EqualValues(t, spec.transferAmount, resv.SplicingOutput.SatBindingMap[0].Amount.Int64())
				require.Zero(t, assetChange, "BRC20 inputs must be transferred in full")
			}
			if spec.protocol == indexer.PROTOCOL_NAME_RUNES {
				require.NotNil(t, resv.SplicingChangeOutput)
				require.Equal(t, localScript, resv.SplicingChangeOutput.OutValue.PkScript, "runes change must return to the initiator")
				require.GreaterOrEqual(t, len(tx.TxOut), 2)
				require.EqualValues(t, 330, tx.TxOut[1].Value)
				require.Equal(t, localScript, tx.TxOut[1].PkScript)

				stone := runestone.Runestone{}
				decoded, err := stone.DecipherFromPkScript(tx.TxOut[len(tx.TxOut)-1].PkScript)
				require.NoError(t, err)
				require.NotNil(t, decoded.Runestone)
				require.Len(t, decoded.Runestone.Edicts, 1)
				require.EqualValues(t, 1, decoded.Runestone.Edicts[0].ID.Block)
				require.EqualValues(t, 1, decoded.Runestone.Edicts[0].ID.Tx)
				require.EqualValues(t, 1, decoded.Runestone.Edicts[0].Output, "the rune change edict must target the initiator change output")
				require.EqualValues(t, spec.wantAssetChange, decoded.Runestone.Edicts[0].Amount.Lo)
				require.Zero(t, decoded.Runestone.Edicts[0].Amount.Hi)
			}

			changeOutputs := 0
			for _, output := range tx.TxOut {
				if output.Value == resv.FeeChange {
					changeOutputs++
					require.Equal(t, localScript, output.PkScript, "fee change must return to the fee-input owner")
				}
				require.NotEqual(t, remoteScript, output.PkScript, "splicing-in must not pay an output to the peer")
			}
			require.Equal(t, 1, changeOutputs)

			stubStart := 2
			if resv.SplicingChange >= 330 {
				stubStart++
			}
			require.GreaterOrEqual(t, len(tx.TxOut), stubStart+spec.stubCount)
			for i := 0; i < spec.stubCount; i++ {
				require.EqualValues(t, spec.stubValue, tx.TxOut[stubStart+i].Value)
				require.Equal(t, channelScript, tx.TxOut[stubStart+i].PkScript, "channel stubs must be funded to the channel output")
			}
			channelStubs := channel.GetStubUtxoForAsset(name)
			require.Len(t, channelStubs, spec.stubCount)

			require.Len(t, tx.TxIn, len(resv.SplicingInputs)+1+len(resv.Fees))
			feeInputStart := len(resv.SplicingInputs) + 1 // channel funding input follows the asset inputs
			for i, fee := range resv.Fees {
				require.Equal(t, *fee.OutPoint(), tx.TxIn[feeInputStart+i].PreviousOutPoint,
					"the constructed transaction must spend every selected fee input")
			}

			inputValue := channel.GetChanPoint().Value()
			for _, input := range resv.SplicingInputs {
				inputValue += input.Value()
			}
			for _, input := range resv.Fees {
				inputValue += input.Value()
			}
			var outputValue int64
			for _, output := range tx.TxOut {
				outputValue += output.Value
			}
			require.EqualValues(t, resv.RequiredFee, inputValue-outputValue,
				"selected values must be conserved with only the calculated transaction fee removed")
		})
	}
}

func feeBudgetProtocolAssetName(protocol, ticker string) *AssetName {
	if protocol == "" {
		name := PLAIN_ASSET
		return &name
	}
	return &AssetName{AssetName: indexer.AssetName{Protocol: protocol, Type: indexer.ASSET_TYPE_FT, Ticker: ticker}}
}

func feeBudgetProtocolAssetInput(index int, name *AssetName, amount, value int64, pkScript []byte) *TxOutput {
	output := indexer.NewTxOutput(value)
	output.OutPointStr = fmt.Sprintf("%064x:0", index)
	output.OutValue.PkScript = append([]byte(nil), pkScript...)
	if IsPlainAsset(name) {
		return output
	}
	output.Assets = indexer.TxAssets{{Name: name.AssetName, Amount: *indexer.NewDefaultDecimal(amount), BindingSat: 0}}
	if name.Protocol == indexer.PROTOCOL_NAME_BRC20 {
		output.Offsets[name.AssetName] = indexer.AssetOffsets{{Start: 0, End: 330}}
		if output.SatBindingMap == nil {
			output.SatBindingMap = make(map[int64]*indexer.AssetInfo)
		}
		output.SatBindingMap[0] = &indexer.AssetInfo{
			Name:       name.AssetName,
			Amount:     *indexer.NewDefaultDecimal(amount),
			BindingSat: 0,
		}
	}
	return output
}

func feeBudgetReviewAssetName() *AssetName {
	return &AssetName{AssetName: indexer.AssetName{Protocol: "ordx", Type: "f", Ticker: "pwapos"}, N: 1}
}

func feeBudgetReviewAssetInputs(name *AssetName, pkScript []byte) []*TxOutput {
	inputs := make([]*TxOutput, 0, 2)
	for i := 0; i < 2; i++ {
		output := indexer.NewTxOutput(400)
		output.OutPointStr = fmt.Sprintf("%064x:0", i+1)
		output.OutValue.PkScript = append([]byte(nil), pkScript...)
		output.Assets = indexer.TxAssets{{Name: name.AssetName, Amount: *indexer.NewDefaultDecimal(400), BindingSat: 1}}
		output.Offsets[name.AssetName] = indexer.AssetOffsets{{Start: 0, End: 400}}
		inputs = append(inputs, output)
	}
	return inputs
}

func feeBudgetReviewPlainInput(index int, value int64, pkScript []byte) *TxOutput {
	output := indexer.NewTxOutput(value)
	output.OutPointStr = fmt.Sprintf("%064x:0", index)
	output.OutValue.PkScript = append([]byte(nil), pkScript...)
	return output
}

func feeBudgetReviewReservation(name *AssetName, amount int64, channel *Channel) *SplicingReservation {
	base := NewReservationBase(9001, true, RS_SPLICINGIN_STARTED, nil)
	if channel != nil {
		base = NewReservationBase(9001, true, RS_SPLICINGIN_STARTED, channel.LocalWallet())
	}
	return &SplicingReservation{
		SplicingDataInDB: SplicingDataInDB{
			ReservationBase: base,
			ChannelId:       channelIDForFeeBudget(channel),
			AssetName:       name,
			Amt:             indexer.NewDefaultDecimal(amount),
		},
		RevocationInfo: RevocationInfo{FeeRate: 1, Channel: channel},
	}
}

func channelIDForFeeBudget(channel *Channel) string {
	if channel == nil {
		return ""
	}
	return channel.ChannelId
}

func feeBudgetReviewChannel(t *testing.T) (*Manager, *Channel) {
	t.Helper()
	manager, stored := localReadyTestFixture(t, 1)
	remoteWallet := NewInternalWalletWithMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", "", GetChainParam())
	stored.RemoteChanCfg.PaymentKey = remoteWallet.GetPaymentPubKey()
	stored.RemoteChanCfg.RevocationBasePoint = remoteWallet.GetRevocationBaseKey()
	redeemScript, _, err := GetP2WSHscript(stored.LocalChanCfg.PaymentKey.SerializeCompressed(), stored.RemoteChanCfg.PaymentKey.SerializeCompressed())
	require.NoError(t, err)
	stored.RedeemScript = redeemScript
	channelPkScript, err := GetP2WSHpkScript(redeemScript)
	require.NoError(t, err)
	stored.ChanPoint.OutValue.PkScript = channelPkScript
	channel := &Channel{ChannelInDB: *stored, manager: manager, localWallet: manager.wallet.Clone()}
	localScript := channel.GetLocalPkScript()
	remoteScript := channel.GetRemotePkScript()
	require.NotEmpty(t, localScript)
	require.NotEmpty(t, remoteScript)
	require.NotEqual(t, localScript, remoteScript)
	return manager, channel
}
