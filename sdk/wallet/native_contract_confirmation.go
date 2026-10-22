package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/seals"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// ConfirmedNativeContractWithdrawal is derived from exact transactions included
// in canonical blocks on both chains. A peer ACK or a mempool observation is
// never sufficient to settle a contract's withdrawal history.
type ConfirmedNativeContractWithdrawal struct {
	ContractURL string
	Bitcoin     *wire.MsgTx
	Descend     *swire.MsgTx
	Amount      *Decimal
	Fee         int64
	PoolSources []*NativeChannelSource
}

func nativeAllocationID(operation string, assignmentType, index uint32) string {
	return fmt.Sprintf("%s/%d/%d", operation, assignmentType, index)
}

func (p *Manager) LoadConfirmedNativeContractWithdrawal(ctx context.Context, owner common.Wallet, txid string) (*ConfirmedNativeContractWithdrawal, error) {
	raw, err := p.GetDB().Read(nativeContractWithdrawKey("signed", txid))
	if err != nil {
		return nil, err
	}
	var pair nativeContractBroadcastPair
	if json.Unmarshal(raw, &pair) != nil || pair.Version != 1 || pair.Proof == nil || len(pair.Proof.Consignment) == 0 {
		return nil, fmt.Errorf("invalid durable native withdrawal pair")
	}
	channel, assetText, _, err := ParseContractURL(pair.ContractURL)
	if err != nil {
		return nil, err
	}
	name := indexer.NewAssetNameFromString(assetText)
	if name == nil || name.Protocol != rgb11wallet.Protocol {
		return nil, fmt.Errorf("invalid native withdrawal asset")
	}
	witness, _, err := p.nativeChannelWitness(owner, channel)
	if err != nil {
		return nil, err
	}
	script, err := GetPkScriptFromAddress(channel)
	if err != nil {
		return nil, err
	}
	chains, err := p.newNativeSettlementChains(ctx, owner)
	if err != nil {
		return nil, err
	}
	bitcoin, err := chains.bitcoinTx(txid, true)
	if err != nil {
		return nil, err
	}
	var encoded bytes.Buffer
	if err := bitcoin.Serialize(&encoded); err != nil {
		return nil, err
	}
	if !bytes.Equal(encoded.Bytes(), pair.Bitcoin) {
		return nil, fmt.Errorf("confirmed Bitcoin witness differs from fixed signed pair")
	}
	if err := chains.verifyBitcoinSpend(bitcoin); err != nil {
		return nil, err
	}
	declared := new(swire.MsgTx)
	reader := bytes.NewReader(pair.Descend)
	if declared.Deserialize(reader) != nil || reader.Len() != 0 {
		return nil, fmt.Errorf("invalid durable native descend")
	}
	descend, err := chains.satoshiTx(declared.TxID())
	if err != nil {
		return nil, err
	}
	encoded.Reset()
	if err := descend.Serialize(&encoded); err != nil {
		return nil, err
	}
	if !bytes.Equal(encoded.Bytes(), pair.Descend) {
		return nil, fmt.Errorf("confirmed descend witness differs from fixed signed pair")
	}
	prev, err := chains.satoshiPrevious(descend)
	if err != nil {
		return nil, err
	}
	if err := VerifySignedTx_SatsNet(descend, prev); err != nil {
		return nil, err
	}
	var burned *Decimal
	for _, out := range descend.TxOut {
		kind, data, e := sindexer.ReadDataFromNullDataScript(out.PkScript)
		if e != nil || kind != sindexer.CONTENT_TYPE_DESCENDING {
			continue
		}
		if burned != nil || string(data) != txid || out.Value != 0 || len(out.Assets) != 1 || out.Assets[0].Name != *name || out.Assets[0].BindingSat != 0 || out.Assets[0].Amount.Sign() <= 0 {
			return nil, fmt.Errorf("invalid canonical native withdrawal burn")
		}
		burned = out.Assets[0].Amount.Clone()
	}
	if burned == nil {
		return nil, fmt.Errorf("canonical native descend has no asset burn")
	}
	var l2Input, l2Returned *Decimal
	for _, input := range descend.TxIn {
		previous := prev.FetchPrevOutput(input.PreviousOutPoint)
		for _, asset := range previous.Assets {
			if asset.Name != *name || asset.BindingSat != 0 || !bytes.Equal(previous.PkScript, script) {
				return nil, fmt.Errorf("native descend consumes an unrelated mapped source")
			}
			l2Input = l2Input.Add(&asset.Amount)
		}
	}
	for _, output := range descend.TxOut {
		if sindexer.IsOpReturn(output.PkScript) {
			continue
		}
		for _, asset := range output.Assets {
			if asset.Name != *name || asset.BindingSat != 0 || !bytes.Equal(output.PkScript, script) {
				return nil, fmt.Errorf("native descend returns assets outside its public channel")
			}
			l2Returned = l2Returned.Add(&asset.Amount)
		}
	}
	if l2Input.Cmp(l2Returned.Add(burned)) != 0 {
		return nil, fmt.Errorf("native descend does not conserve mapped asset quantity")
	}
	scope, err := p.stpRGB11Scope(owner)
	if err != nil {
		return nil, err
	}
	var reveals []seals.GraphBlindSeal
	for _, raw := range pair.Proof.ChangeSeals {
		seal, err := seals.DecodeGraphBlindSeal(raw)
		if err != nil {
			return nil, err
		}
		reveals = append(reveals, seal)
	}
	validator := rgb11wallet.NewNativeConsensusValidatorWithReveals(reveals...)
	history, err := validator.ValidateHistoricalConsignmentReport(ctx, pair.Proof.Consignment, scope.evidence)
	if err != nil {
		return nil, err
	}
	consumed := make(map[string]bool)
	for _, edge := range history.Consumers {
		if edge.WitnessTxID == txid {
			consumed[nativeAllocationID(edge.InputOperationID, edge.AssignmentType, edge.AssignmentIndex)] = true
		}
	}
	inputAmounts := make(map[string]*Decimal)
	outputAmounts := make(map[string]*Decimal)
	for _, historical := range history.Allocations {
		allocation := historical.Allocation
		if allocation.AssetName != *name {
			return nil, fmt.Errorf("native withdrawal proof includes another asset or control allocation")
		}
		if consumed[nativeAllocationID(allocation.OperationID, allocation.AssignmentType, allocation.AssignmentIndex)] {
			inputAmounts[allocation.OutPoint] = inputAmounts[allocation.OutPoint].Add(&allocation.Amount)
		}
		point, e := wire.NewOutPointFromString(allocation.OutPoint)
		if e != nil {
			return nil, e
		}
		if point.Hash.String() == txid {
			if uint64(point.Index) >= uint64(len(bitcoin.TxOut)) {
				return nil, fmt.Errorf("native proof references absent Bitcoin output")
			}
			outputAmounts[allocation.OutPoint] = outputAmounts[allocation.OutPoint].Add(&allocation.Amount)
		}
	}
	var totalIn, totalOut, outbound *Decimal
	var inputValue, outputValue, poolIn, poolOut, recipientCarriers int64
	var ancestry []string
	seenAncestry := make(map[string]bool)
	for _, input := range bitcoin.TxIn {
		point := input.PreviousOutPoint.String()
		previous, e := chains.bitcoinOutput(point)
		if e != nil {
			return nil, e
		}
		inputValue += previous.Value
		amount := inputAmounts[point]
		if bytes.Equal(previous.PkScript, script) {
			poolIn += previous.Value
		}
		if amount == nil || amount.Sign() == 0 {
			continue
		}
		if !bytes.Equal(previous.PkScript, script) {
			return nil, fmt.Errorf("native withdrawal consumes an external native carrier")
		}
		source, e := p.loadNativeChannelSource(channel, point)
		if e != nil {
			return nil, e
		}
		validated, e := p.validateNativeChannelSource(ctx, owner, channel, source, nativeproof.Historical)
		if e != nil {
			return nil, e
		}
		if validated.Asset.Name != *name || validated.Asset.Amount.Cmp(amount) != 0 || validated.Value != previous.Value {
			return nil, fmt.Errorf("native withdrawal does not consume the full authenticated source")
		}
		spent, e := scope.evidence.GetOutspend(point)
		if e != nil || spent == nil || !spent.Spent || spent.SpendingTx != txid {
			return nil, fmt.Errorf("native withdrawal source is not spent by the confirmed transaction")
		}
		totalIn = totalIn.Add(amount)
		delete(inputAmounts, point)
		for _, id := range source.DescendTxIDs {
			if !seenAncestry[id] {
				ancestry = append(ancestry, id)
				seenAncestry[id] = true
			}
		}
	}
	if len(inputAmounts) != 0 {
		return nil, fmt.Errorf("native history consumers do not match Bitcoin inputs")
	}
	material, err := nativeproof.BuildRGB11(pair.Proof.Consignment, pair.Proof.ChangeSeals)
	if err != nil {
		return nil, err
	}
	result := &ConfirmedNativeContractWithdrawal{ContractURL: pair.ContractURL, Bitcoin: bitcoin, Descend: descend, Amount: burned}
	for i, out := range bitcoin.TxOut {
		outputValue += out.Value
		if bytes.Equal(out.PkScript, script) {
			poolOut += out.Value
		}
		point := fmt.Sprintf("%s:%d", txid, i)
		amount := outputAmounts[point]
		if amount == nil || amount.Sign() == 0 {
			continue
		}
		totalOut = totalOut.Add(amount)
		if !bytes.Equal(out.PkScript, script) {
			outbound = outbound.Add(amount)
			recipientCarriers += out.Value
			continue
		}
		sourceOutput := indexer.NewTxOutput(out.Value)
		sourceOutput.OutPointStr, sourceOutput.OutValue.PkScript = point, append([]byte(nil), out.PkScript...)
		sourceOutput.Assets = indexer.TxAssets{{Name: *name, Amount: *amount.Clone(), BindingSat: 0}}
		source := &NativeChannelSource{Version: 1, ChannelID: channel, Output: sourceOutput, PublicProof: material, DescendTxIDs: append(append([]string(nil), ancestry...), descend.TxID())}
		result.PoolSources = append(result.PoolSources, source)
	}
	if totalIn.Sign() <= 0 || totalIn.Cmp(totalOut) != 0 || burned.Cmp(outbound) != 0 || poolIn <= 0 || poolOut != poolIn || inputValue < outputValue {
		return nil, fmt.Errorf("native withdrawal does not conserve its asset and full pool carrier")
	}
	// A zero-asset returned carrier still belongs to the pool and is checked
	// above, but cannot be registered as spendable native inventory.
	if len(result.PoolSources) > 1 {
		return nil, fmt.Errorf("native withdrawal returned multiple pool asset sources")
	}
	// Contract accounting includes the recipient's Bitcoin carrier, which
	// the operator funds in addition to the network fee. The original pool
	// carrier remains intact and cannot silently subsidize withdrawals.
	result.Fee = inputValue - outputValue + recipientCarriers
	if err := chains.stable(); err != nil {
		return nil, err
	}
	_ = witness // channelWitness authenticated the captured owner above.
	for _, source := range result.PoolSources {
		spent, err := scope.evidence.GetOutspend(source.Output.OutPointStr)
		if err != nil || spent == nil {
			return nil, fmt.Errorf("native pool change evidence unavailable: %w", err)
		}
		if spent.Spent {
			if !rgb11KnownObservedChannelSpend(scope.evidence, source.Output.OutPointStr, spent.SpendingTx) {
				return nil, fmt.Errorf("native pool change has an unknown successor")
			}
			continue
		}
		if err := p.RegisterNativeChannelSettlementSource(ctx, owner, channel, source); err != nil {
			return nil, err
		}
	}
	return result, nil
}
