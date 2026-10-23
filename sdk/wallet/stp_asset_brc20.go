package wallet

import (
	"fmt"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
)

type brc20STPProtocol struct{ brc20STPRules }

// Invalid BRC20 records are consumed transfer inscriptions or already-applied
// account credits. Their Bitcoin carriers remain spendable, but the display
// amount is not a second transferable allocation. No other protocol inherits
// this interpretation merely by declaring an account-based policy.
func (protocol brc20STPProtocol) IsSTPHistoricalDisplayOnly(asset *indexer.AssetInfo) bool {
	return asset != nil && protocol.ValidateAsset(&asset.Name, STPOpForceClose) == nil &&
		asset.Amount.Validate() == nil && asset.Amount.Sign() >= 0
}

func (brc20STPProtocol) EstimateSplicePayload(*AssetName, int) ([]byte, error) { return nil, nil }

func (brc20STPProtocol) VerifyInput(asset *AssetName, input *TxOutput) error {
	return verifySTPInput(asset, input)
}

func (brc20STPProtocol) PrepareInputs(p *Manager, req STPAssetInputRequest) (*STPAssetInputs, error) {
	output, inscribe, err := p.getUtxosForBRC20(req.FundingAddress, req.AssetAddress, req.FundingUtxos,
		req.ExcludedUtxos, req.Asset, req.Amount, req.PreviousInputs, req.RevealKey, req.FeeRate, req.LockInputs)
	if err != nil {
		return nil, err
	}
	return &STPAssetInputs{Inputs: []*TxOutput{output}, Inscribe: inscribe}, nil
}

func (brc20STPProtocol) BuildTransfer(p *Manager, req STPAssetTransferRequest) (*STPAssetPlan, error) {
	tx, fetcher, fee, prev, err := p.buildBatchSendTxWithAddressHeight_brc20(req.Wallet, req.Address, req.Destinations,
		req.Asset, req.FeeRate, req.Memo, true, req.ExcludeRecentBlock, req.PayFeeByCurrentAddress, req.MaxConfirmedInputHeight)
	return &STPAssetPlan{Tx: tx, PrevFetcher: fetcher, Fee: fee, PrevTxs: prev}, err
}

func (brc20STPProtocol) BuildCommitment(p *Manager, req STPAssetCommitmentRequest) (*STPAssetPlan, error) {
	prev, stubs, local, remote, err := p.HandleOutputsBrc20(req.Tx,
		req.Channel.GetFundingOutputWithProtocol(indexer.PROTOCOL_NAME_BRC20), req.LocalBalance, req.RemoteBalance,
		req.Channel.GetStubUtxos(indexer.PROTOCOL_NAME_BRC20), req.LocalScript, req.RemoteScript,
		req.RevealKey, req.Weight, req.FeeRate, req.Channel.ChannelId)
	return &STPAssetPlan{Tx: req.Tx, PrevTxs: prev, PlainInputs: stubs, LocalOutputs: local, RemoteOutputs: remote}, err
}

func (brc20STPProtocol) BindSplice(_ *Manager, req STPAssetSpliceRequest) error {
	if req.Operation == STPOpSpliceIn && req.Reservation.SplicingChangeOutput != nil &&
		req.Reservation.SplicingChangeOutput.GetAsset(&req.Reservation.AssetName.AssetName).Sign() != 0 {
		return fmt.Errorf("brc20 asset change is unexpected")
	}
	return nil
}

func (brc20STPProtocol) ReserveRecovery(p *Manager, req STPAssetCommitmentRequest, plan *STPAssetPlan) (*wire.TxOut, error) {
	if req.Operation != STPOpCommit || len(plan.PrevTxs) == 0 {
		return nil, nil
	}
	if req.KeyRing == nil || req.KeyRing.RevealPrivKey == nil {
		return nil, fmt.Errorf("missing BRC20 recovery reveal key")
	}
	pkScript, err := GetP2TRpkScript(req.KeyRing.RevealPrivKey.PubKey())
	if err != nil {
		return nil, err
	}
	fee, err := p.calcFeeForMintTransferForSweepBrc20(req.Channel.GetFundingOutputWithProtocol(indexer.PROTOCOL_NAME_BRC20),
		req.LocalBalance, 1, pkScript, req.WitnessScript, req.LocalScript, req.FeeRate, req.Channel.ChannelId)
	if err != nil || fee == 0 {
		return nil, err
	}
	if fee < 330 {
		return nil, fmt.Errorf("invalid BRC20 recovery fee %d", fee)
	}
	return wire.NewTxOut(fee, pkScript), nil
}

func (brc20STPProtocol) CompleteRecovery(p *Manager, req STPAssetCommitmentRequest, plan *STPAssetPlan) error {
	if plan.RecoveryOutput == nil {
		return nil
	}
	index := -1
	for i, output := range req.Tx.TxOut {
		if output == plan.RecoveryOutput {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("missing BRC20 recovery output")
	}
	next, _, err := p.mintTransferForSweepBrc20(req.Channel.GetFundingOutputWithProtocol(indexer.PROTOCOL_NAME_BRC20),
		req.LocalBalance, []*TxOutput{indexer.GenerateTxOutput(req.Tx, index)}, req.LocalScript,
		req.KeyRing.RevealPrivKey, req.FeeRate, req.Channel.ChannelId)
	plan.NextTxs = next
	return err
}

func (brc20STPProtocol) PrepareOtherSweep(p *Manager, req STPAssetOtherSweepRequest) (*STPAssetOtherSweepPlan, error) {
	if len(req.Outputs) == 0 {
		return &STPAssetOtherSweepPlan{FeeInputs: req.FeeInputs}, nil
	}
	prev, transfers, fees, err := p.mintTransferForOtherSweepBrc20(req.ChannelAddress, req.CommitTx,
		req.Outputs, req.FeeInputs, req.RevealKey, req.FeeRate)
	return &STPAssetOtherSweepPlan{PrevTxs: prev, Transfers: transfers, FeeInputs: fees}, err
}

func (brc20STPProtocol) RecoveryInputs(commit *ChannelCommitment, balances map[AssetName]*Decimal) (STPAssetRecoveryInputs, error) {
	var result STPAssetRecoveryInputs
	for name, amount := range balances {
		if name.Protocol == indexer.PROTOCOL_NAME_BRC20 && amount.Sign() != 0 {
			result.LeadingPlainOutputs++
		}
	}
	if result.LeadingPlainOutputs == 0 {
		return result, nil
	}
	if commit == nil || len(commit.NextTxs) == 0 || len(commit.NextTxs)%2 != 0 {
		return STPAssetRecoveryInputs{}, fmt.Errorf("should construct brc20 transfer inscription before")
	}
	for i := 1; i < len(commit.NextTxs); i += 2 {
		tx := commit.NextTxs[i]
		if tx == nil || len(tx.TxOut) == 0 {
			return STPAssetRecoveryInputs{}, fmt.Errorf("invalid brc20 recovery predecessor")
		}
		result.Transfers = append(result.Transfers, indexer.GenerateTxOutput(tx, 0))
	}
	return result, nil
}

func (brc20STPProtocol) Verify(req STPAssetVerification) error {
	if err := verifySTPAssetLayout(req); err != nil {
		return err
	}
	if req.Plan.RecoveryOutput != nil && len(req.Plan.NextTxs) == 0 {
		return fmt.Errorf("BRC20 recovery transactions were not prepared")
	}
	return nil
}
