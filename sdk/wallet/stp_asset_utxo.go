package wallet

import (
	"fmt"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/runes/runestone"
)

type btcSTPProtocol struct {
	btcSTPRules
	stpUTXORecovery
}

type ordxSTPProtocol struct {
	ordxSTPRules
	stpUTXORecovery
}

type runesSTPProtocol struct {
	runesSTPRules
	stpUTXORecovery
}

// UTXO-based protocols recover directly from the commitment outputs. BRC20
// overrides these methods because its balance needs another transfer inscription.
type stpUTXORecovery struct{}

func (stpUTXORecovery) ReserveRecovery(*Manager, STPAssetCommitmentRequest, *STPAssetPlan) (*wire.TxOut, error) {
	return nil, nil
}

func (stpUTXORecovery) CompleteRecovery(*Manager, STPAssetCommitmentRequest, *STPAssetPlan) error {
	return nil
}

func (stpUTXORecovery) PrepareOtherSweep(_ *Manager, req STPAssetOtherSweepRequest) (*STPAssetOtherSweepPlan, error) {
	return &STPAssetOtherSweepPlan{FeeInputs: req.FeeInputs}, nil
}

func (stpUTXORecovery) RecoveryInputs(*ChannelCommitment, map[AssetName]*Decimal) (STPAssetRecoveryInputs, error) {
	return STPAssetRecoveryInputs{}, nil
}

func (stpUTXORecovery) Verify(req STPAssetVerification) error {
	return verifySTPAssetLayout(req)
}

func verifySTPInput(asset *AssetName, input *TxOutput) error {
	if asset == nil || input == nil {
		return fmt.Errorf("missing STP asset input")
	}
	if (IsPlainAsset(asset) && input.HasAsset()) || HasMultiAsset(input, &asset.AssetName) {
		return fmt.Errorf("STP input %s carries unrelated assets", input.OutPointStr)
	}
	return nil
}

func (stpUTXORecovery) VerifyInput(asset *AssetName, input *TxOutput) error {
	return verifySTPInput(asset, input)
}

func (btcSTPProtocol) EstimateSplicePayload(*AssetName, int) ([]byte, error)  { return nil, nil }
func (ordxSTPProtocol) EstimateSplicePayload(*AssetName, int) ([]byte, error) { return nil, nil }
func (runesSTPProtocol) EstimateSplicePayload(asset *AssetName, index int) ([]byte, error) {
	return GetRunesEstimatePayload(asset, index)
}

func prepareSTPUTXOInputs(p *Manager, req STPAssetInputRequest) (*STPAssetInputs, error) {
	// Splicing-out selection must use the channel's own funding records. STP
	// performs that selection after checking capacity and fee ownership.
	if req.Operation == STPOpSpliceOut {
		return &STPAssetInputs{}, nil
	}
	inputs, err := p.getUtxosWithAssetV3(req.AssetAddress, req.Amount, req.Asset, req.ExcludedUtxos)
	if err != nil {
		return nil, err
	}
	return &STPAssetInputs{Inputs: inputs}, nil
}

func (btcSTPProtocol) PrepareInputs(p *Manager, req STPAssetInputRequest) (*STPAssetInputs, error) {
	return prepareSTPUTXOInputs(p, req)
}

func (ordxSTPProtocol) PrepareInputs(p *Manager, req STPAssetInputRequest) (*STPAssetInputs, error) {
	return prepareSTPUTXOInputs(p, req)
}

func (runesSTPProtocol) PrepareInputs(p *Manager, req STPAssetInputRequest) (*STPAssetInputs, error) {
	return prepareSTPUTXOInputs(p, req)
}

func (btcSTPProtocol) BuildTransfer(p *Manager, req STPAssetTransferRequest) (*STPAssetPlan, error) {
	tx, fetcher, fee, err := p.buildBatchSendTxWithAddressHeight_btc(req.Address, req.Destinations,
		req.FeeRate, req.Memo, true, req.ExcludeRecentBlock, req.MaxConfirmedInputHeight)
	return &STPAssetPlan{Tx: tx, PrevFetcher: fetcher, Fee: fee}, err
}

func (ordxSTPProtocol) BuildTransfer(p *Manager, req STPAssetTransferRequest) (*STPAssetPlan, error) {
	tx, fetcher, fee, err := p.buildBatchSendTxWithAddressHeight_ordx(req.Wallet, req.Address, req.Destinations,
		req.Asset, req.FeeRate, req.Memo, true, req.ExcludeRecentBlock, req.PayFeeByCurrentAddress, req.MaxConfirmedInputHeight)
	return &STPAssetPlan{Tx: tx, PrevFetcher: fetcher, Fee: fee}, err
}

func (runesSTPProtocol) BuildTransfer(p *Manager, req STPAssetTransferRequest) (*STPAssetPlan, error) {
	tx, fetcher, fee, err := p.buildBatchSendTxWithAddressHeight_runes(req.Wallet, req.Address, req.Destinations,
		req.Asset, req.FeeRate, true, req.ExcludeRecentBlock, req.PayFeeByCurrentAddress, req.MaxConfirmedInputHeight)
	return &STPAssetPlan{Tx: tx, PrevFetcher: fetcher, Fee: fee}, err
}

func (btcSTPProtocol) BuildCommitment(_ *Manager, req STPAssetCommitmentRequest) (*STPAssetPlan, error) {
	// STP adds native value last, after all asset carriers and fee reserves.
	// Bitcoin value never becomes an entry in TxAssets.
	return &STPAssetPlan{Tx: req.Tx}, nil
}

func (ordxSTPProtocol) BuildCommitment(p *Manager, req STPAssetCommitmentRequest) (*STPAssetPlan, error) {
	err := p.HandleOutputsOrdx(req.Tx, req.Channel.GetFundingOutputWithProtocol(indexer.PROTOCOL_NAME_ORDX),
		req.LocalBalance, req.RemoteBalance, req.Channel.GetStubUtxos(indexer.PROTOCOL_NAME_ORDX),
		req.LocalScript, req.RemoteScript, req.Weight, req.Channel.ChannelId)
	return &STPAssetPlan{Tx: req.Tx}, err
}

func (runesSTPProtocol) BuildCommitment(p *Manager, req STPAssetCommitmentRequest) (*STPAssetPlan, error) {
	plain, err := p.handleOutputsRunesAfterPrefix(req.Tx, req.Channel.GetFundingOutputWithProtocol(indexer.PROTOCOL_NAME_RUNES),
		req.LocalBalance, req.RemoteBalance, req.Channel.GetStubUtxos(indexer.PROTOCOL_NAME_RUNES),
		req.LocalScript, req.RemoteScript, req.Weight, req.Channel.ChannelId, req.PrefixInputs, req.PrefixOutputs)
	return &STPAssetPlan{Tx: req.Tx, ExtraPlainSats: plain}, err
}

func (btcSTPProtocol) BindSplice(*Manager, STPAssetSpliceRequest) error  { return nil }
func (ordxSTPProtocol) BindSplice(*Manager, STPAssetSpliceRequest) error { return nil }

func (runesSTPProtocol) BindSplice(p *Manager, req STPAssetSpliceRequest) error {
	resv := req.Reservation
	if resv.SplicingChangeOutput == nil {
		return nil
	}
	change := resv.SplicingChangeOutput.GetAsset(&resv.AssetName.AssetName)
	if req.Operation == STPOpSpliceIn && change.Sign() <= 0 {
		return nil
	}
	id, err := p.getRuneIdFromName(&resv.AssetName.AssetName)
	if err != nil {
		return err
	}
	var edicts []runestone.Edict
	if change.Sign() > 0 {
		if len(req.Tx.TxOut) <= 1 || resv.SplicingChange < 330 {
			return fmt.Errorf("missing Runes asset change output")
		}
		edicts = append(edicts, runestone.Edict{ID: *id, Output: 1, Amount: change.ToUint128()})
	}
	payload, err := EncipherRunePayload(edicts)
	if err != nil {
		return err
	}
	req.Tx.AddTxOut(wire.NewTxOut(0, payload))
	return nil
}
