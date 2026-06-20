package wallet

import (
	"encoding/json"
	"fmt"

	"github.com/btcsuite/btcd/txscript"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

type FundingInitOptions struct {
	FeeRate             int64
	Amount              int64
	Outpoints           []string
	FundingUtxo         *TxOutput
	Memo                string
	NeedSendFundingTx   bool
	SkipOpeningAnchorTx bool
	L2DrainTxId         string
	InitialCapacity     int64
}

func (p *Manager) GetServerWalletAddress() string {
	if p.serverNode == nil || p.serverNode.Pubkey == nil {
		return ""
	}
	return PublicKeyToP2TRAddress(p.serverNode.Pubkey)
}

func (p *Manager) SelectWalletFundingOutpoints(feeRate, amt int64, feeCfg *ChannelFeeConfig) ([]string, error) {
	address := p.wallet.GetAddress()
	outputs := p.GetIndexerClient().GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)
	if len(outputs) == 0 {
		Log.Errorf("no plain sats")
		return nil, fmt.Errorf("no plain sats")
	}
	if feeCfg == nil {
		feeCfg = NewFeeConfig()
	}
	feeToDAO := feeCfg.FeeToDAO()
	if feeToDAO < 0 {
		return nil, fmt.Errorf("invalid fee config: fee to dao is %d", feeToDAO)
	}

	var weightEstimate utils.TxWeightEstimator
	weightEstimate.AddP2WSHOutput()
	if feeToDAO > 0 {
		weightEstimate.AddP2WSHOutput()
	}
	weightEstimate.AddP2TROutput()

	value := int64(0)
	utxos := make([]string, 0)
	p.GetUtxoLocker().Reload(address)
	for _, out := range outputs {
		if p.GetUtxoLocker().IsLocked(out.OutPoint) {
			continue
		}
		value += out.Value
		utxos = append(utxos, out.OutPoint)
		weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
		requiredFee := weightEstimate.Fee(feeRate)
		if value >= amt+requiredFee {
			break
		}
	}
	required := amt + weightEstimate.Fee(feeRate)
	if value < required {
		return nil, fmt.Errorf("no enough plain sats to pay fee, required %d but only %d", required, value)
	}

	return utxos, nil
}

func (p *Manager) InitInitiatorFundingReservation(opts FundingInitOptions) (*FundingReservation, error) {
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	if p.serverNode == nil || p.serverNode.NodeId == nil || p.serverNode.Pubkey == nil {
		return nil, fmt.Errorf("server node is not configured")
	}

	resv := &FundingReservation{}
	resv.InitRuntime()
	resv.Status = RS_INIT
	resv.IsInitiator = true
	resv.SetLocalWallet(p.wallet.Clone())
	resv.WalletId = resv.LocalWallet().GetWalletId()
	resv.NeedSendFundingTx = opts.NeedSendFundingTx
	resv.SkipOpeningAnchorTx = opts.SkipOpeningAnchorTx

	outpoints := append([]string(nil), opts.Outpoints...)
	if !opts.NeedSendFundingTx {
		if opts.FundingUtxo == nil {
			return nil, fmt.Errorf("funding utxo is nil")
		}
		resv.FundingUtxos = []*TxOutput{opts.FundingUtxo}
		outpoints = []string{opts.FundingUtxo.OutPointStr}
	}

	req := &wwire.OpenChannelRequest{
		MsgHeader:           wwire.NewMsgHeader(),
		NodeId:              resv.LocalWallet().GetNodePubKey().SerializeCompressed(),
		ChannelWalletId:     int(resv.LocalWallet().GetSubAccount()),
		FundingKey:          resv.LocalWallet().GetPaymentPubKey().SerializeCompressed(),
		FeeRate:             opts.FeeRate,
		LocalFundingAmount:  opts.Amount,
		Outpoints:           outpoints,
		NeedSendFundingTx:   opts.NeedSendFundingTx,
		SkipOpeningAnchorTx: opts.SkipOpeningAnchorTx,
		L2DrainTxId:         opts.L2DrainTxId,
		Memo:                []byte(opts.Memo),
	}
	msg, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	reqSig, err := resv.LocalWallet().SignMessageWithIndex(msg, 0)
	if err != nil {
		return nil, err
	}
	resv.Req = req
	resv.ReqSig = reqSig

	revBaseKey := resv.LocalWallet().GetRevocationBaseKey()
	paymentKey := resv.LocalWallet().GetPaymentPubKey()

	resv.Channel = NewChannel(nil, p)
	resv.Channel.PeerNodeId = p.serverNode.NodeId.SerializeCompressed()
	resv.Channel.IsInitiator = true
	resv.Channel.LocalChanCfg.PaymentKey = paymentKey
	resv.Channel.LocalChanCfg.RevocationBasePoint = revBaseKey
	resv.Channel.LocalChanCfg.WalletId = resv.LocalWallet().GetSubAccount()
	resv.Channel.Memo = opts.Memo
	if opts.InitialCapacity > 0 {
		resv.Channel.Capacity = opts.InitialCapacity
	}

	return resv, nil
}
