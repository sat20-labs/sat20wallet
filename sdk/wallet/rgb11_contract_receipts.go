package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

// These receipts let a second pool owner independently admit inventory that
// arrived through a private-channel exit. A source's public RGB11 history and
// exact canonical mapping/burn lineage are sufficient; private channel state
// and revocation material are never sent to the public-contract counterparty.
func (p *Manager) rgb11ContractSigningReceipts(ctx context.Context, local common.Wallet,
	channel string, bitcoin *wire.MsgTx, previous *txscript.MultiPrevOutFetcher) ([]*wwire.RGB11ChannelSettlementReceipt, error) {
	var receipts []*wwire.RGB11ChannelSettlementReceipt
	script, err := GetPkScriptFromAddress(channel)
	if err != nil {
		return nil, err
	}
	for _, input := range bitcoin.TxIn {
		output := previous.FetchPrevOutput(input.PreviousOutPoint)
		if output == nil {
			return nil, fmt.Errorf("RGB11 signing input has no authenticated prevout")
		}
		if !bytes.Equal(output.PkScript, script) {
			continue
		}
		source, err := p.loadRGB11ChannelSource(channel, input.PreviousOutPoint.String())
		if errors.Is(err, indexer.ErrKeyNotFound) {
			continue // Plain pool fee inputs have no RGB11 inventory record.
		}
		if err != nil {
			return nil, err
		}
		if len(source.PrivateWitness) == 0 && len(source.DescendTxIDs) == 0 {
			continue // The original public deposit was delivered to both owners.
		}
		receipt, err := p.ExportRGB11SettlementReceipt(ctx, local, channel, input.PreviousOutPoint.String())
		if err != nil {
			return nil, err
		}
		receipts = append(receipts, receipt)
	}
	return receipts, nil
}

// Call after ordinary contract authorization and before reconstructing the
// transaction. Admission is limited to exact inputs of this signed request;
// an unrelated valid receipt cannot alter the contract's inventory here.
func (p *ContractRuntimeBase) registerRGB11SigningReceipts(req *wwire.RemoteSignMoreData_Contract) error {
	if len(req.RGB11SettlementReceipts) == 0 {
		return nil
	}
	if !p.rgb11PublicContract() || !req.RGB11Withdrawal || req.ContractURL != p.URL() || len(req.RGB11SettlementReceipts) > 64 {
		return fmt.Errorf("unexpected RGB11 contract settlement receipts")
	}
	var bitcoin *wire.MsgTx
	for _, item := range req.Tx {
		if item != nil && item.L1Tx && item.Reason == "" {
			if bitcoin != nil {
				return fmt.Errorf("RGB11 receipts require exactly one Bitcoin transaction")
			}
			var err error
			bitcoin, err = DecodeMsgTx(item.Tx)
			if err != nil {
				return err
			}
		}
	}
	if bitcoin == nil || len(req.RGB11SettlementReceipts) > len(bitcoin.TxIn) {
		return fmt.Errorf("RGB11 receipt has no corresponding Bitcoin input")
	}
	inputs := make(map[string]bool, len(bitcoin.TxIn))
	for _, input := range bitcoin.TxIn {
		inputs[input.PreviousOutPoint.String()] = true
	}
	for _, receipt := range req.RGB11SettlementReceipts {
		if receipt == nil || !inputs[receipt.SourceOutpoint] {
			return fmt.Errorf("unrelated or repeated RGB11 settlement receipt")
		}
		delete(inputs, receipt.SourceOutpoint)
		if err := p.stp.GetWalletMgr().RegisterRGB11SettlementReceipt(context.Background(), p.localWallet, p.ChannelAddr, receipt); err != nil {
			return err
		}
	}
	return nil
}
