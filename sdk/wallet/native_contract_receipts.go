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
// arrived through a private-channel exit. A source's public native history and
// exact canonical mapping/burn lineage are sufficient; private channel state
// and revocation material are never sent to the public-contract counterparty.
func (p *Manager) nativeContractSigningReceipts(ctx context.Context, local common.Wallet,
	channel string, bitcoin *wire.MsgTx, previous *txscript.MultiPrevOutFetcher) ([]*wwire.NativeChannelSettlementReceipt, error) {
	var receipts []*wwire.NativeChannelSettlementReceipt
	script, err := GetPkScriptFromAddress(channel)
	if err != nil {
		return nil, err
	}
	for _, input := range bitcoin.TxIn {
		output := previous.FetchPrevOutput(input.PreviousOutPoint)
		if output == nil {
			return nil, fmt.Errorf("native signing input has no authenticated prevout")
		}
		if !bytes.Equal(output.PkScript, script) {
			continue
		}
		source, err := p.loadNativeChannelSource(channel, input.PreviousOutPoint.String())
		if errors.Is(err, indexer.ErrKeyNotFound) {
			continue // Plain pool fee inputs have no native inventory record.
		}
		if err != nil {
			return nil, err
		}
		if len(source.PrivateWitness) == 0 && len(source.DescendTxIDs) == 0 {
			continue // The original public deposit was delivered to both owners.
		}
		receipt, err := p.ExportNativeChannelSettlementReceipt(ctx, local, channel, input.PreviousOutPoint.String())
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
func (p *ContractRuntimeBase) registerNativeContractSigningReceipts(req *wwire.RemoteSignMoreData_Contract) error {
	if len(req.NativeSettlementReceipts) == 0 {
		return nil
	}
	if !p.nativePublicContract() || !req.NativeWithdrawal || req.ContractURL != p.URL() || len(req.NativeSettlementReceipts) > 64 {
		return fmt.Errorf("unexpected native contract settlement receipts")
	}
	var bitcoin *wire.MsgTx
	for _, item := range req.Tx {
		if item != nil && item.L1Tx && item.Reason == "" {
			if bitcoin != nil {
				return fmt.Errorf("native receipts require exactly one Bitcoin transaction")
			}
			var err error
			bitcoin, err = DecodeMsgTx(item.Tx)
			if err != nil {
				return err
			}
		}
	}
	if bitcoin == nil || len(req.NativeSettlementReceipts) > len(bitcoin.TxIn) {
		return fmt.Errorf("native receipt has no corresponding Bitcoin input")
	}
	inputs := make(map[string]bool, len(bitcoin.TxIn))
	for _, input := range bitcoin.TxIn {
		inputs[input.PreviousOutPoint.String()] = true
	}
	for _, receipt := range req.NativeSettlementReceipts {
		if receipt == nil || !inputs[receipt.SourceOutpoint] {
			return fmt.Errorf("unrelated or repeated native settlement receipt")
		}
		delete(inputs, receipt.SourceOutpoint)
		if err := p.stp.GetWalletMgr().RegisterNativeChannelSettlementReceipt(context.Background(), p.localWallet, p.ChannelAddr, receipt); err != nil {
			return err
		}
	}
	return nil
}
