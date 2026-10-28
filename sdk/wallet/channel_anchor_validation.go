package wallet

import (
	"bytes"
	"fmt"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type ChannelAssetAmounts map[string]*Decimal

func (a ChannelAssetAmounts) amount(name string) *Decimal {
	if n := a[name]; n != nil {
		return n.Clone()
	}
	return indexer.NewDefaultDecimal(0)
}

func (a ChannelAssetAmounts) add(name string, n *Decimal) {
	if n == nil {
		return
	}
	v := a.amount(name)
	v.AddInPlaceAlignPrecision(n)
	a[name] = v
}

func (a ChannelAssetAmounts) addOutput(value int64, assets swire.TxAssets) error {
	if value < 0 {
		return fmt.Errorf("negative channel BTC value")
	}
	a.add(indexer.ASSET_PLAIN_SAT.String(), indexer.NewDefaultDecimal(value))
	for _, asset := range assets {
		if asset.Amount.Sign() < 0 {
			return fmt.Errorf("negative channel asset %s", asset.Name.String())
		}
		if indexer.IsPlainAsset(&asset.Name) {
			return fmt.Errorf("BTC must be represented by output value")
		}
		a.add(asset.Name.String(), &asset.Amount)
	}
	return nil
}

// SignChannelAnchorTx validates the source before releasing an anchor signature.
func (p *Manager) SignChannelAnchorTx(tx *swire.MsgTx, sign func([]byte) ([]byte, error)) ([]byte, error) {
	return p.SignChannelAnchorTxWithFunding(tx, nil, nil, sign)
}

// Managed funding may be unbroadcast. Authenticate its source and exclude Lucky
// backing; ordinary channel accounting remains owned by the channel commitment.
func (p *Manager) SignChannelAnchorTxWithFunding(tx *swire.MsgTx, funding *wire.MsgTx, parents map[string]*wire.MsgTx, sign func([]byte) ([]byte, error)) ([]byte, error) {
	if tx == nil || len(tx.TxIn) != 1 {
		return nil, fmt.Errorf("invalid channel anchor")
	}
	data, err := ParseStandardAnchorScript(tx.TxIn[0].SignatureScript)
	if err != nil {
		return nil, err
	}
	if funding == nil {
		if _, err := p.ValidateBTCLuckyAnchorSource(tx, data.WitnessScript); err != nil {
			return nil, err
		}
	} else {
		txid, vout, err := indexer.ParseUtxo(data.Utxo)
		if err != nil || txid != funding.TxID() || vout < 0 || vout >= len(funding.TxOut) {
			return nil, fmt.Errorf("anchor source does not match funding transaction")
		}
		script, err := utils.WitnessScriptHash(data.WitnessScript)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(script, funding.TxOut[vout].PkScript) || funding.TxOut[vout].Value < data.Value {
			return nil, fmt.Errorf("anchor source does not match channel funding output")
		}
		for _, parent := range parents {
			if err := p.RejectBTCLuckyL1Inputs(parent, parents); err != nil {
				return nil, err
			}
		}
		if err := p.RejectBTCLuckyL1Inputs(funding, parents); err != nil {
			return nil, err
		}
	}
	return p.SignAnchorTx(tx, sign)
}

func (p *Manager) SignRecoveredChannelAnchorTx(tx *swire.MsgTx, sign func([]byte) ([]byte, error)) ([]byte, error) {
	if tx == nil || len(tx.TxIn) != 1 {
		return nil, fmt.Errorf("invalid recovery anchor")
	}
	data, err := ParseStandardAnchorScript(tx.TxIn[0].SignatureScript)
	if err != nil {
		return nil, err
	}
	if err := p.rejectBTCLuckyChannelFunding(data.Utxo, data.WitnessScript); err != nil {
		return nil, err
	}
	script, err := utils.WitnessScriptHash(data.WitnessScript)
	if err != nil {
		return nil, err
	}
	channel, err := GetP2WSHaddressFromScript(script)
	if err != nil {
		return nil, err
	}
	l2 := p.l2IndexerClient.getActiveIndexer()
	tip, err := readChannelChainTip(l2)
	if err != nil {
		return nil, err
	}
	ledger, err := l2.GetChannelLedger(channel)
	if err != nil {
		return nil, err
	}
	found := false
	for _, entry := range ledger {
		if entry == nil || entry.ChannelId != channel || entry.Direction != "ascending" || entry.L2TxId == "" || entry.L2Height > tip.height {
			continue
		}
		for _, source := range entry.L1Outpoints {
			if source == data.Utxo {
				found = true
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("recovery anchor source is not indexed")
	}
	end, err := readChannelChainTip(l2)
	if err != nil || end != tip {
		return nil, fmt.Errorf("L2 changed during anchor recovery")
	}
	return p.SignAnchorTx(tx, sign)
}

type channelChainTip struct {
	height int
	hash   string
}

func readChannelChainTip(client IndexerRPCClient) (channelChainTip, error) {
	h := client.GetSyncHeight()
	if h < 0 || client.GetBestHeight() != int64(h) {
		return channelChainTip{}, fmt.Errorf("channel indexer is not synchronized")
	}
	hash, err := client.GetBlockHash(h)
	if err != nil || hash == "" {
		return channelChainTip{}, fmt.Errorf("channel chain tip unavailable: %v", err)
	}
	return channelChainTip{h, hash}, nil
}

func ChannelDeanchorAmounts(tx *swire.MsgTx, l1TxID string) (ChannelAssetAmounts, error) {
	amounts := make(ChannelAssetAmounts)
	if tx == nil {
		return amounts, nil
	}
	found := false
	for _, out := range tx.TxOut {
		kind, payload, err := sindexer.ReadDataFromNullDataScript(out.PkScript)
		if err != nil || kind != sindexer.CONTENT_TYPE_DESCENDING {
			continue
		}
		if found {
			return nil, fmt.Errorf("duplicate descending output")
		}
		found = true
		descend, err := sindexer.ParseDescendPayload(payload)
		if err != nil || descend.L1TxId != l1TxID {
			return nil, fmt.Errorf("deanchor does not match L1 withdrawal")
		}
		if err := amounts.addOutput(out.Value, out.Assets); err != nil {
			return nil, err
		}
	}
	if !found {
		return nil, fmt.Errorf("deanchor missing descending output")
	}
	return amounts, nil
}

// ChannelDeanchorInputs permits replay of the SAME paired withdrawal after
// its burn has been indexed. Historical prevouts are used only with that
// confirmed receipt, never to sign a fresh spend of consumed L2 outputs.
func (p *Manager) ChannelDeanchorInputs(channel, l1ID string, tx *swire.MsgTx) (*stxscript.MultiPrevOutFetcher, bool, error) {
	if tx == nil {
		return nil, false, fmt.Errorf("missing channel deanchor")
	}
	if _, err := ChannelDeanchorAmounts(tx, l1ID); err != nil {
		return nil, false, err
	}
	client := p.l2IndexerClient.getActiveIndexer()
	tip, err := readChannelChainTip(client)
	if err != nil {
		return nil, false, err
	}
	ledger, err := client.GetChannelLedger(channel)
	if err != nil {
		return nil, false, err
	}
	indexed := false
	for _, entry := range ledger {
		if entry != nil && entry.ChannelId == channel && entry.Direction == "descending" && entry.L1TxId == l1ID && entry.L2TxId == tx.TxID() && entry.L2Height <= tip.height {
			indexed = true
		}
	}
	previous := stxscript.NewMultiPrevOutFetcher(nil)
	for _, input := range tx.TxIn {
		if !indexed {
			output, err := client.GetTxOutput(input.PreviousOutPoint.String())
			if err != nil {
				return nil, false, err
			}
			converted := OutputToSatsNet(output)
			previous.AddPrevOut(input.PreviousOutPoint, &converted.OutValue)
			continue
		}
		raw, err := client.GetRawTx(input.PreviousOutPoint.Hash.String())
		if err != nil {
			return nil, false, err
		}
		parent, err := DecodeMsgTx_SatsNet(raw)
		if err != nil || parent.TxID() != input.PreviousOutPoint.Hash.String() || int(input.PreviousOutPoint.Index) >= len(parent.TxOut) {
			return nil, false, fmt.Errorf("invalid historical deanchor input")
		}
		previous.AddPrevOut(input.PreviousOutPoint, parent.TxOut[input.PreviousOutPoint.Index])
	}
	end, err := readChannelChainTip(client)
	if err != nil || end != tip {
		return nil, false, fmt.Errorf("L2 changed while checking deanchor")
	}
	return previous, indexed, nil
}
