package wallet

import (
	"bytes"
	"fmt"

	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/runes/runestone"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

type CommitAssetOutput struct {
	Index     int
	TxOut     *wire.TxOut
	AssetName AssetName
	Amt       *Decimal
}

func (p *Manager) isControlByContract(channelId string, assetName string) bool {
	return p.IsControlByContract(channelId, assetName)
}

func (p *Manager) CreateCommitTx(whoseCommit int, channel *Channel, keyRing *CommitmentKeyRing,
	bootstrapKey *secp256k1.PublicKey, feeRate int64) (*wire.MsgTx, []*InscribeResv, []*InscribeResv, error) {
	commitTx, inscribes, next, _, err := p.CreateCommitTxWithOthers(whoseCommit, channel, keyRing, bootstrapKey, feeRate)
	return commitTx, inscribes, next, err
}

func (p *Manager) CreateCommitTxWithOthers(whoseCommit int, channel *Channel, keyRing *CommitmentKeyRing,
	bootstrapKey *secp256k1.PublicKey, feeRate int64) (*wire.MsgTx, []*InscribeResv, []*InscribeResv, []*CommitmentTx, error) {
	return p.CreateCommitTx3(whoseCommit, channel, keyRing, bootstrapKey, feeRate)
}

func (p *Manager) CreateCommitTx3(whoseCommit int, channel *Channel, keyRing *CommitmentKeyRing,
	bootstrapKey *secp256k1.PublicKey, feeRate int64) (*wire.MsgTx, []*InscribeResv, []*InscribeResv, []*CommitmentTx, error) {
	serverCommit := IsServerCommitment(whoseCommit, channel)

	// serverCommit determines script shape, while SelectCommitBalances maps
	// balances to the delay/direct side. The owner of a commitment always gets
	// the delayed output; the counterparty gets the direct output.
	delayScript, directScript, err := GenerateChannelScript3(serverCommit, channel, keyRing, bootstrapKey)
	if err != nil {
		Log.Errorf("GenerateChannelScript3 failed. %v", err)
		return nil, nil, nil, nil, err
	}

	localBalance, remoteBalance := SelectCommitBalances(channel, serverCommit)

	var weightEstimate utils.TxWeightEstimator
	feeRate = ClampCommitmentFeeRate(channel, feeRate)

	commitTx := wire.NewMsgTx(2)
	delayPkScript := delayScript.PkScript()
	directPkScript := directScript.PkScript()

	assetReq := STPAssetCommitmentRequest{
		Operation: STPOpCommit, Channel: channel, Tx: commitTx,
		LocalBalance: localBalance, RemoteBalance: remoteBalance,
		LocalScript: delayPkScript, RemoteScript: directPkScript,
		WitnessScript: delayScript.WitnessScriptToSign(), RevealKey: keyRing.GetRevealKey(),
		KeyRing: keyRing, FeeRate: feeRate, Weight: &weightEstimate, ServerCommit: serverCommit,
	}
	assetPlans, err := p.planSTPCommitmentAssets(assetReq)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	var inscribes []*InscribeResv
	var extraStubs []*TxOutput
	var plainSats int64
	var initiatorPlainSats int64
	for _, item := range assetPlans {
		inscribes = append(inscribes, item.plan.PrevTxs...)
		extraStubs = append(extraStubs, item.plan.PlainInputs...)
		plainSats += item.plan.ExtraPlainSats
		initiatorPlainSats += item.plan.InitiatorPlainSats
	}
	plainSats += AddCommitmentPlainAndStubInputs(commitTx, channel, extraStubs, &weightEstimate)

	// Plain sats are handled last. They pay the commitment transaction fee and
	// also reserve enough value on the delayed side for future punish/sweep
	// transactions.
	localPlainValue, remotePlainValue := InitCommitmentPlainValues(localBalance, remoteBalance, plainSats)
	if serverCommit {
		remotePlainValue += initiatorPlainSats
	} else {
		localPlainValue += initiatorPlainSats
	}
	localPkScript := delayPkScript
	remotePkScript := directPkScript

	for _, item := range assetPlans {
		reserve, err := item.protocol.ReserveRecovery(p, assetReq, item.plan)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		if reserve == nil {
			continue
		}
		if reserve.Value < 330 || localPlainValue < reserve.Value {
			return nil, nil, nil, nil, fmt.Errorf("CreateCommitTx3 insufficient recovery reserve: require %d but %d", reserve.Value, localPlainValue)
		}
		localPlainValue -= reserve.Value
		item.plan.RecoveryOutput = reserve
		weightEstimate.AddTxOutput(reserve)
		commitTx.AddTxOut(reserve)
		item.plan.Layout = append(item.plan.Layout, STPAssetOutputConstraint{
			Output: reserve, Value: reserve.Value, PkScript: append([]byte(nil), reserve.PkScript...),
			Index: len(commitTx.TxOut) - 1,
		})
	}

	// Rebalance may switch plain-sats ownership for reopen/splicing scenarios,
	// but it must preserve the invariant that the delayed side has enough white
	// sats for fee-sensitive follow-up transactions.
	localPlainValue, remotePlainValue, localPkScript, remotePkScript, err = RebalanceCommitmentPlainOutputs(
		channel, serverCommit, localPlainValue, remotePlainValue, localPkScript, remotePkScript)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("CreateCommitTx3 %w", err)
	}

	var recoveryFeeIndexes []uint32
	if remotePlainValue >= 330 {
		txOut2 := &wire.TxOut{PkScript: remotePkScript, Value: remotePlainValue}
		recoveryFeeIndexes = append(recoveryFeeIndexes, uint32(len(commitTx.TxOut)))
		commitTx.AddTxOut(txOut2)
		weightEstimate.AddTxOutput(txOut2)
	}

	txOut1 := &wire.TxOut{PkScript: localPkScript, Value: localPlainValue}
	weightEstimate.AddTxOutput(txOut1)
	requiredFee := weightEstimate.Fee(feeRate)

	localPlainValue -= requiredFee
	if localPlainValue >= 330 {
		txOut1.Value = localPlainValue
		recoveryFeeIndexes = append(recoveryFeeIndexes, uint32(len(commitTx.TxOut)))
		commitTx.AddTxOut(txOut1)
	}

	PrintJsonTx(commitTx, fmt.Sprintf("commit %d", whoseCommit))
	Log.Infof("commitTx(%d->%d): feeRate=%d, requiredFee=%d", len(commitTx.TxIn), len(commitTx.TxOut), feeRate, requiredFee)

	var next []*InscribeResv
	var others []*CommitmentTx
	for _, item := range assetPlans {
		if err := item.protocol.CompleteRecovery(p, assetReq, item.plan); err != nil {
			return nil, nil, nil, nil, err
		}
		next = append(next, item.plan.NextTxs...)
		others = append(others, item.plan.OtherTxs...)
	}
	if serverCommit {
		// STP chooses the recovery branch and spending script; each protocol
		// materializes the assets required by that branch.
		sweeps, err := p.createServerCommitmentOtherSweeps(channel, commitTx, assetPlans, recoveryFeeIndexes, keyRing.GetRevealKey(), feeRate)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		others = append(others, sweeps...)
	}
	if err := verifySTPCommitmentAssets(assetReq, assetPlans); err != nil {
		return nil, nil, nil, nil, err
	}
	proofs, err := collectSTPAssetProofs(assetPlans)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if whoseCommit == 0 {
		channel.LocalCommitment.AssetProofs = proofs
		channel.LocalCommitment.RecoveryFeeIndexes = append([]uint32(nil), recoveryFeeIndexes...)
	} else {
		channel.RemoteCommitment.AssetProofs = proofs
		channel.RemoteCommitment.RecoveryFeeIndexes = append([]uint32(nil), recoveryFeeIndexes...)
	}

	return commitTx, inscribes, next, others, nil
}

func (p *Manager) createServerCommitmentOtherSweeps(channel *Channel, commitTx *wire.MsgTx,
	assetPlans []stpPlannedAsset, recoveryFeeIndexes []uint32, revealPrivKey []byte, feeRate int64) ([]*CommitmentTx, error) {

	// When a server commitment pays client-owned assets to the channel script,
	// client recovery needs an additional sweep chain. This keeps the client
	// independent from DKVS/server backup when recovering those assets.
	channelPkScript := channel.GetChannelPkScript()
	channelAddr, err := AddrFromPkScript(channelPkScript)
	if err != nil {
		return nil, err
	}
	var userPubKey *secp256k1.PublicKey
	if channel.IsInitiator {
		userPubKey = channel.LocalChanCfg.PaymentKey
	} else {
		userPubKey = channel.RemoteChanCfg.PaymentKey
	}
	recvPkScript, err := GetP2TRpkScript(userPubKey)
	if err != nil {
		return nil, err
	}

	materializedIndex := make(map[int]bool)
	protocolOutputs := make([][]*CommitAssetOutput, len(assetPlans))
	for n, item := range assetPlans {
		outputs := append([]*CommitAssetOutput{}, item.plan.LocalOutputs...)
		outputs = append(outputs, item.plan.RemoteOutputs...)
		for _, output := range outputs {
			index := locateCommitAssetOutput(commitTx, output)
			if index < 0 || index >= len(commitTx.TxOut) {
				return nil, fmt.Errorf("missing planned recovery output")
			}
			output.Index = index
			if bytes.Equal(commitTx.TxOut[index].PkScript, channelPkScript) {
				materializedIndex[index] = true
				protocolOutputs[n] = append(protocolOutputs[n], output)
			}
		}
	}

	var plainFeeOutput *TxOutput
	plainFeeIndex := -1
	for _, outputIndex := range recoveryFeeIndexes {
		i := int(outputIndex)
		if i >= len(commitTx.TxOut) || materializedIndex[i] {
			return nil, fmt.Errorf("invalid plain recovery reserve index %d", i)
		}
		if bytes.Equal(commitTx.TxOut[i].PkScript, channelPkScript) {
			if plainFeeOutput != nil {
				return nil, fmt.Errorf("multiple channel plain recovery reserves")
			}
			plainFeeIndex = i
			plainFeeOutput = indexer.GenerateTxOutput(commitTx, i)
		}
	}

	directAssetOutputs := make([]*TxOutput, 0)
	for i, txOut := range commitTx.TxOut {
		if !bytes.Equal(txOut.PkScript, channelPkScript) {
			continue
		}
		if materializedIndex[i] {
			continue
		}
		if i == plainFeeIndex {
			continue
		}
		directAssetOutputs = append(directAssetOutputs, indexer.GenerateTxOutput(commitTx, i))
	}

	var prevTxs []*wire.MsgTx
	var transferOutputs []*TxOutput
	var feeOutputs []*TxOutput
	if plainFeeOutput != nil {
		feeOutputs = append(feeOutputs, plainFeeOutput)
	}
	for n, item := range assetPlans {
		plan, err := item.protocol.PrepareOtherSweep(p, STPAssetOtherSweepRequest{
			ChannelAddress: channelAddr, CommitTx: commitTx, Outputs: protocolOutputs[n],
			FeeInputs: feeOutputs, RevealKey: revealPrivKey, FeeRate: feeRate,
		})
		if err != nil {
			return nil, err
		}
		prevTxs = append(prevTxs, plan.PrevTxs...)
		transferOutputs = append(transferOutputs, plan.Transfers...)
		feeOutputs = plan.FeeInputs
	}

	if len(directAssetOutputs) == 0 && len(transferOutputs) == 0 && len(feeOutputs) == 0 {
		return nil, nil
	}

	// Native recovery is bound only after account-based protocols have
	// materialized their transfers. The combined builder receives the same
	// asset order and one shared set of fee inputs as the legacy sweep.
	proofs, err := collectSTPAssetProofs(assetPlans)
	if err != nil {
		return nil, err
	}
	for _, item := range assetPlans {
		native, ok := item.protocol.(STPAssetChannelProtocol)
		if !ok || len(proofs[item.protocol.Protocol()]) == 0 {
			continue
		}
		candidate := NewChannelCommitment()
		candidate.CommitTx, candidate.AssetProofs = commitTx, proofs
		inputs := append(append([]*TxOutput{}, directAssetOutputs...), transferOutputs...)
		plan, err := native.BuildChannelRecovery(p, STPAssetRecoveryRequest{
			Channel: channel, Commitment: candidate, Operation: STPOpForceClose,
			Branch: "other-sweep", RecipientScript: recvPkScript,
			CommitmentScript: channel.RedeemScript, ScriptType: SCRIPT_TYPE_CHANNEL,
			Inputs: inputs, FeeInputs: feeOutputs, PrevTxs: prevTxs, FeeRate: feeRate,
		})
		if err != nil {
			return nil, err
		}
		if plan == nil {
			return nil, fmt.Errorf("native other-sweep returned no recovery plan")
		}
		if plan.NoNativeAssets {
			continue
		}
		if plan.Tx == nil || len(plan.NativeProofs[item.protocol.Protocol()]) == 0 {
			return nil, fmt.Errorf("native other-sweep lost its proof")
		}
		return []*CommitmentTx{{CommitTx: plan.Tx, PrevTxs: prevTxs, AssetProofs: plan.NativeProofs}}, nil
	}

	sweepTx, err := createOtherSweepTx(commitTx, prevTxs, directAssetOutputs, transferOutputs, feeOutputs, recvPkScript, feeRate)
	if err != nil {
		return nil, err
	}
	if sweepTx == nil {
		return nil, nil
	}
	return []*CommitmentTx{{CommitTx: sweepTx, PrevTxs: prevTxs}}, nil
}

func locateCommitAssetOutput(commitTx *wire.MsgTx, output *CommitAssetOutput) int {
	if commitTx == nil || output == nil {
		return -1
	}
	if output.TxOut != nil {
		for i, txOut := range commitTx.TxOut {
			if txOut == output.TxOut {
				return i
			}
		}
	}
	if output.Index >= 0 && output.Index < len(commitTx.TxOut) {
		return output.Index
	}
	return -1
}

func (p *Manager) mintTransferForOtherSweepBrc20(channelAddr string, commitTx *wire.MsgTx,
	brc20Outputs []*CommitAssetOutput, feeOutputs []*TxOutput, revealPrivKey []byte,
	feeRate int64) ([]*wire.MsgTx, []*TxOutput, []*TxOutput, error) {

	// The other-sweep path also prebuilds BRC20 transfer inscriptions. The
	// plain fee output is spent first, and any change becomes the fee source for
	// the next inscription or final sweep.
	prevTxs := make([]*wire.MsgTx, 0, len(brc20Outputs)*2)
	transferOutputs := make([]*TxOutput, 0, len(brc20Outputs))
	brc20PlainOutputs := make([]*TxOutput, 0, len(brc20Outputs))
	for _, output := range brc20Outputs {
		if len(feeOutputs) == 0 {
			return nil, nil, nil, fmt.Errorf("no plain fee output to mint brc20 transfer for other sweep")
		}
		brc20PlainOutputs = append(brc20PlainOutputs, indexer.GenerateTxOutput(commitTx, output.Index))

		defaultUtxos := append([]*TxOutput{}, feeOutputs...)
		inscribe, err := p.MintTransferV3_brc20(NewUtxoMgr(channelAddr, p.GetIndexerClient()),
			channelAddr, map[string]bool{}, &output.AssetName.AssetName, output.Amt, feeRate,
			defaultUtxos, true, revealPrivKey, SCRIPT_TYPE_CHANNEL, nil, false, false)
		if err != nil {
			return nil, nil, nil, err
		}
		PrintJsonTx(inscribe.CommitTx, "other prev transfer commit for channel sweep")
		PrintJsonTx(inscribe.RevealTx, "other prev transfer reveal for channel sweep")

		prevTxs = append(prevTxs, inscribe.CommitTx, inscribe.RevealTx)
		transferOutputs = append(transferOutputs, GenerateBRC20TransferOutput(inscribe.RevealTx,
			&output.AssetName.AssetName, output.Amt))
		feeOutputs = nil
		if change := inscribe.GetChangeOutput(); change != nil {
			feeOutputs = append(feeOutputs, change)
		}
	}
	feeOutputs = append(feeOutputs, brc20PlainOutputs...)
	return prevTxs, transferOutputs, feeOutputs, nil
}

func createOtherSweepTx(parentTx *wire.MsgTx, _ []*wire.MsgTx,
	directAssetOutputs, brc20TransferOutputs, feeOutputs []*TxOutput,
	recvPkScript []byte, feeRate int64) (*wire.MsgTx, error) {

	// Asset outputs are placed before the fee-only inputs. The final plain-sats
	// balance pays the network fee and optionally returns change to the user.
	sweepTx := wire.NewMsgTx(2)
	var weightEstimate utils.TxWeightEstimator
	assetOutputs := append([]*TxOutput{}, directAssetOutputs...)
	assetOutputs = append(assetOutputs, brc20TransferOutputs...)

	for _, output := range assetOutputs {
		sweepTx.AddTxIn(output.TxIn())
		weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
		txOut := wire.NewTxOut(output.Value(), recvPkScript)
		sweepTx.AddTxOut(txOut)
		weightEstimate.AddTxOutput(txOut)
	}

	var feeValue int64
	for _, output := range feeOutputs {
		feeValue += output.Value()
		sweepTx.AddTxIn(output.TxIn())
		weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
	}
	requiredFee := weightEstimate.Fee(feeRate)
	if feeValue < requiredFee {
		return nil, fmt.Errorf("other sweep no enough fee, require %d but %d", requiredFee, feeValue)
	}
	if len(assetOutputs) == 0 {
		weightEstimate.AddP2TROutput()
		requiredFeeWithChange := weightEstimate.Fee(feeRate)
		if feeValue < requiredFeeWithChange+330 {
			return nil, nil
		}
		requiredFee = requiredFeeWithChange
		sweepTx.AddTxOut(wire.NewTxOut(feeValue-requiredFee, recvPkScript))
	} else {
		weightEstimate.AddP2TROutput()
		requiredFeeWithChange := weightEstimate.Fee(feeRate)
		if feeValue >= requiredFeeWithChange+330 {
			requiredFee = requiredFeeWithChange
			sweepTx.AddTxOut(wire.NewTxOut(feeValue-requiredFee, recvPkScript))
		}
	}
	if len(sweepTx.TxOut) == 0 {
		return nil, nil
	}
	PrintJsonTx(sweepTx, fmt.Sprintf("other sweep for %s", parentTx.TxID()))
	return sweepTx, nil
}

func (p *Manager) HandleOutputsRunes(commitTx *wire.MsgTx, outputs []*AssetToOutput,
	localBalance, remoteBalance map[AssetName]*Decimal,
	stubs map[AssetName][]*TxOutput, localPkScript, remotePkScript []byte,
	weightEstimate *utils.TxWeightEstimator, channelId string) (int64, error) {
	return p.handleOutputsRunesAfterPrefix(commitTx, outputs, localBalance, remoteBalance, stubs,
		localPkScript, remotePkScript, weightEstimate, channelId, 0, 0)
}

func (p *Manager) handleOutputsRunesAfterPrefix(commitTx *wire.MsgTx, outputs []*AssetToOutput,
	localBalance, remoteBalance map[AssetName]*Decimal,
	_ map[AssetName][]*TxOutput, localPkScript, remotePkScript []byte,
	weightEstimate *utils.TxWeightEstimator, channelId string, prefixInputs, prefixOutputs int) (int64, error) {
	if prefixInputs < 0 || prefixInputs > len(commitTx.TxIn) || prefixOutputs < 0 || prefixOutputs > len(commitTx.TxOut) {
		return 0, fmt.Errorf("invalid native asset prefix")
	}

	// Runes uses edicts, so the first runes input/output pair is inserted at
	// the front and the remote amount is assigned by OP_RETURN output index.
	// The returned value is leftover plain sats to be handled by the final fee
	// step.
	transferEdicts := make([]runestone.Edict, 0)
	total := int64(0)
	firstInput := int64(0)
	outputIndex := len(commitTx.TxOut)
	for _, assetToOutput := range outputs {
		name := *assetToOutput.AssetName
		if p.isControlByContract(channelId, name.String()) {
			continue
		}

		uv := assetToOutput.Outputs
		localValue := localBalance[name].Clone()
		remoteValue := remoteBalance[name].Clone()

		localRemaining := localValue
		for _, utxo := range uv {
			if firstInput == 0 {
				commitTx.TxIn = append(commitTx.TxIn, nil)
				copy(commitTx.TxIn[prefixInputs+1:], commitTx.TxIn[prefixInputs:])
				commitTx.TxIn[prefixInputs] = utxo.TxIn()
				firstInput = utxo.Value()
				outputIndex++
			} else {
				commitTx.AddTxIn(utxo.TxIn())
			}
			weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)

			total += utxo.Value()
			amt := utxo.GetAsset(&name.AssetName)
			if localRemaining.Cmp(amt) >= 0 {
				localRemaining = localRemaining.Sub(amt)
			} else {
				localRemaining.SetValue(0)
			}
		}

		if remoteValue.Sign() > 0 {
			runeId, err := p.getRuneIdFromName(&name.AssetName)
			if err != nil {
				return 0, err
			}
			transferEdicts = append(transferEdicts, runestone.Edict{
				ID:     *runeId,
				Output: uint32(outputIndex),
				Amount: remoteValue.ToUint128(),
			})
		}
	}

	if total > 0 {
		total -= firstInput
		txOut1 := &wire.TxOut{PkScript: localPkScript, Value: firstInput}
		commitTx.TxOut = append(commitTx.TxOut, nil)
		copy(commitTx.TxOut[prefixOutputs+1:], commitTx.TxOut[prefixOutputs:])
		commitTx.TxOut[prefixOutputs] = txOut1
		weightEstimate.AddTxOutput(txOut1)

		if len(transferEdicts) > 0 {
			txOut2 := &wire.TxOut{PkScript: remotePkScript, Value: 330}
			commitTx.AddTxOut(txOut2)
			weightEstimate.AddTxOutput(txOut2)
			total -= 330

			var nullDataScript []byte
			var err error
			if prefixOutputs == 0 {
				// Preserve existing Runes-only commitment bytes and transaction IDs.
				nullDataScript, err = EncipherRunePayload(transferEdicts)
			} else {
				pointer := uint32(prefixOutputs)
				nullDataScript, err = EncipherRunePayloadWithPointer(transferEdicts, &pointer)
			}
			if err != nil {
				Log.Errorf("too many edicts, %d, %v", len(transferEdicts), err)
				return 0, err
			}

			txOut := &wire.TxOut{PkScript: nullDataScript, Value: 0}
			commitTx.AddTxOut(txOut)
			weightEstimate.AddTxOutput(txOut)
		} else if prefixOutputs != 0 {
			// The implicit Runes remainder destination is output zero. A native
			// prefix may occupy that output, so bind the actual Runes carrier.
			pointer := uint32(prefixOutputs)
			payload, err := EncipherRunePayloadWithPointer(nil, &pointer)
			if err != nil {
				return 0, err
			}
			txOut := wire.NewTxOut(0, payload)
			commitTx.AddTxOut(txOut)
			weightEstimate.AddTxOutput(txOut)
		}
	}

	return total, nil
}

func (p *Manager) HandleOutputsBrc20(commitTx *wire.MsgTx, outputs []*AssetToOutput,
	localBalance, remoteBalance map[AssetName]*Decimal, stubs map[AssetName][]*TxOutput,
	localPkScript, remotePkScript []byte, revealPrivKey []byte,
	weightEstimate *utils.TxWeightEstimator, feeRate int64,
	channelId string) ([]*InscribeResv, []*TxOutput, []*CommitAssetOutput, []*CommitAssetOutput, error) {

	// BRC20 balances are materialized as transfer inscriptions before the
	// commitment. Each side needs its own reveal output, and the returned stubs
	// carry any leftover white sats into later plain-sats fee handling.
	var result []*TxOutput
	localCommitOutputs := make([]*CommitAssetOutput, 0)
	remoteCommitOutputs := make([]*CommitAssetOutput, 0)
	inscribes := make([]*InscribeResv, 0)
	for _, assetToOutput := range outputs {
		name := *assetToOutput.AssetName
		if p.isControlByContract(channelId, name.String()) {
			continue
		}
		uv := assetToOutput.Outputs

		localValue := localBalance[name].Clone()
		remoteValue := remoteBalance[name].Clone()

		stubOutputs := stubs[name]
		stubForLocal := localValue.Sign() != 0
		stubForRemote := remoteValue.Sign() != 0

		if len(stubOutputs) != 2 {
			return nil, nil, nil, nil, fmt.Errorf("%s should provide two stubs first", name.String())
		}

		var defaultUtxos []*TxOutput
		defaultUtxos = append(defaultUtxos, stubOutputs...)
		defaultUtxos = append(defaultUtxos, uv...)

		if stubForLocal {
			inscribe, err := p.MintTransferV2_brc20(channelId, channelId, map[string]bool{}, &name.AssetName,
				localValue, feeRate, defaultUtxos, true, revealPrivKey, true, false, false)
			if err != nil {
				Log.Errorf("MintTransferV2_brc20 failed, %v", err)
				return nil, nil, nil, nil, err
			}
			PrintJsonTx(inscribe.CommitTx, "prev transfer commit for local")
			PrintJsonTx(inscribe.RevealTx, "prev transfer reveal for local")
			output := GenerateBRC20TransferOutput(inscribe.RevealTx, &name.AssetName, localValue)
			inscribes = append(inscribes, inscribe)

			commitTx.AddTxIn(output.TxIn())
			weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)

			txOut1 := &wire.TxOut{PkScript: localPkScript, Value: output.Value()}
			commitTx.AddTxOut(txOut1)
			weightEstimate.AddTxOutput(txOut1)
			localCommitOutputs = append(localCommitOutputs, &CommitAssetOutput{
				Index: len(commitTx.TxOut) - 1, TxOut: txOut1, AssetName: name, Amt: localValue.Clone(),
			})

			if change := inscribe.GetChangeOutput(); change != nil {
				defaultUtxos = []*TxOutput{change}
			} else {
				defaultUtxos = nil
			}
		}

		if stubForRemote {
			inscribe, err := p.MintTransferV2_brc20(channelId, channelId, map[string]bool{}, &name.AssetName,
				remoteValue, feeRate, defaultUtxos, true, revealPrivKey, true, false, false)
			if err != nil {
				Log.Errorf("MintTransferV2_brc20 failed, %v", err)
				return nil, nil, nil, nil, err
			}
			PrintJsonTx(inscribe.CommitTx, "prev transfer commit for remote")
			PrintJsonTx(inscribe.RevealTx, "prev transfer reveal for remote")
			output := GenerateBRC20TransferOutput(inscribe.RevealTx, &name.AssetName, remoteValue)
			inscribes = append(inscribes, inscribe)

			commitTx.AddTxIn(output.TxIn())
			weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)

			txOut2 := &wire.TxOut{PkScript: remotePkScript, Value: output.Value()}
			commitTx.AddTxOut(txOut2)
			weightEstimate.AddTxOutput(txOut2)
			remoteCommitOutputs = append(remoteCommitOutputs, &CommitAssetOutput{
				Index: len(commitTx.TxOut) - 1, TxOut: txOut2, AssetName: name, Amt: remoteValue.Clone(),
			})

			if change := inscribe.GetChangeOutput(); change != nil {
				defaultUtxos = []*TxOutput{change}
			} else {
				defaultUtxos = nil
			}
		}

		if len(defaultUtxos) != 0 {
			result = append(result, defaultUtxos...)
		}
	}

	return inscribes, result, localCommitOutputs, remoteCommitOutputs, nil
}

func (p *Manager) calcFeeForMintTransferForSweepBrc20(outputs []*AssetToOutput,
	localBalance map[AssetName]*Decimal, inputLen int,
	srcPkScript, destWitnessScript, destPkScript []byte,
	feeRate int64, channelId string) (int64, error) {

	destAddr, err := AddrFromPkScript(destPkScript)
	if err != nil {
		return 0, err
	}
	srcAddr, err := AddrFromPkScript(srcPkScript)
	if err != nil {
		return 0, err
	}

	var fee int64
	for _, assetToOutput := range outputs {
		name := *assetToOutput.AssetName
		if p.isControlByContract(channelId, name.String()) {
			continue
		}

		localValue := localBalance[name].Clone()
		if localValue.Sign() != 0 {
			f, err := CalcFeeForMintTransfer(inputLen, srcAddr, destAddr, SCRIPT_TYPE_SWEEP,
				destWitnessScript, &name.AssetName, localValue, feeRate)
			if err != nil {
				Log.Errorf("CalcFeeForMintTransfer failed, %v", err)
				return 0, err
			}
			fee += f
		}
	}

	return fee, nil
}

func (p *Manager) mintTransferForSweepBrc20(outputs []*AssetToOutput,
	localBalance map[AssetName]*Decimal, defaultUtxos []*TxOutput,
	destPkScript []byte, revealKey *secp256k1.PrivateKey,
	feeRate int64, channelId string) ([]*InscribeResv, []*TxOutput, error) {

	// These transfer inscriptions spend the reserved output from the commitment
	// tx, so they must be kept as commitment "next" transactions for later
	// broadcast and for punish/sweep input discovery.
	destAddr, err := AddrFromPkScript(destPkScript)
	if err != nil {
		return nil, nil, err
	}

	inscribes := make([]*InscribeResv, 0)
	for _, assetToOutput := range outputs {
		name := *assetToOutput.AssetName
		if p.isControlByContract(channelId, name.String()) || indexer.IsPlainAsset(&name.AssetName) {
			continue
		}

		localValue := localBalance[name].Clone()
		if localValue.Sign() != 0 {
			inscribe, err := p.MintTransferWithCommitPriKey(destAddr, &name.AssetName, localValue,
				feeRate, defaultUtxos, SCRIPT_TYPE_TAPROOTKEYSPEND, nil, revealKey)
			if err != nil {
				Log.Errorf("MintTransferWithCommitPriKey failed, %v", err)
				return nil, nil, err
			}
			PrintJsonTx(inscribe.CommitTx, "next transfer commit for sweep")
			PrintJsonTx(inscribe.RevealTx, "next transfer reveal for sweep")

			inscribes = append(inscribes, inscribe)
			if change := inscribe.GetChangeOutput(); change != nil {
				defaultUtxos = []*TxOutput{change}
			}
		}
	}

	return inscribes, defaultUtxos, nil
}

func (p *Manager) HandleOutputsOrdx(commitTx *wire.MsgTx, outputs []*AssetToOutput,
	localBalance, remoteBalance map[AssetName]*Decimal, stubs map[AssetName][]*TxOutput,
	localPkScript, remotePkScript []byte, weightEstimate *utils.TxWeightEstimator,
	channelId string) error {

	// ORDX assets are split by sat binding offsets. When a side's bound sats
	// would fall below dust but both sides hold the asset, a stub output is used
	// to keep both commitment outputs spendable.
	for _, assetToOutput := range outputs {
		name := *assetToOutput.AssetName
		if p.isControlByContract(channelId, name.String()) {
			continue
		}
		uv := assetToOutput.Outputs
		if indexer.IsPlainAsset(&name.AssetName) {
			continue
		}

		localValue := localBalance[name].Clone()
		remoteValue := remoteBalance[name].Clone()
		localSatNum := GetBindingSatNum(localValue, name.N)
		remoteSatNum := GetBindingSatNum(remoteValue, name.N)

		stubOutputs := stubs[name]
		stubForLocal := false
		stubForRemote := false
		if localSatNum < 330 && localSatNum != 0 && remoteSatNum != 0 {
			if len(stubOutputs) < 2 {
				return fmt.Errorf("%s local amount %d too small", name.String(), localSatNum)
			}
			stubForLocal = true
		}
		if remoteSatNum < 330 && remoteSatNum != 0 && localSatNum != 0 {
			if len(stubOutputs) < 2 {
				return fmt.Errorf("%s remote amount %d too small", name.String(), remoteSatNum)
			}
			stubForRemote = true
		}

		total := int64(0)
		if stubForLocal {
			commitTx.AddTxIn(stubOutputs[0].TxIn())
			total += stubOutputs[0].Value()
			weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
		}

		localOutput := int64(0)
		localRemaining := localValue
		for _, utxo := range uv {
			commitTx.AddTxIn(utxo.TxIn())
			weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)

			total += utxo.Value()
			amt := utxo.GetAsset(&name.AssetName)
			if localRemaining.Cmp(amt) >= 0 {
				localRemaining = localRemaining.Sub(amt)
				localOutput += utxo.Value()
			} else if localRemaining.Sign() > 0 {
				offset, err := utxo.GetAssetOffset(&name.AssetName, localRemaining)
				if err != nil {
					return err
				}
				localOutput += offset
				localRemaining.SetValue(0)
			}
		}
		if stubForRemote {
			commitTx.AddTxIn(stubOutputs[1].TxIn())
			total += stubOutputs[1].Value()
			weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
		}

		if stubForLocal {
			localOutput += stubOutputs[0].Value()
		}
		remoteOutput := total - localOutput

		if localOutput >= 330 {
			txOut1 := &wire.TxOut{PkScript: localPkScript, Value: localOutput}
			commitTx.AddTxOut(txOut1)
			weightEstimate.AddTxOutput(txOut1)
		}

		if remoteOutput >= 330 {
			txOut2 := &wire.TxOut{PkScript: remotePkScript, Value: remoteOutput}
			commitTx.AddTxOut(txOut2)
			weightEstimate.AddTxOutput(txOut2)
		}
	}
	return nil
}
