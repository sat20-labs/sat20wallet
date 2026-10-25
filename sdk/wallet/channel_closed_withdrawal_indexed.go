package wallet

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"reflect"
	"sort"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// These are internally observed current facts, not a native proof supplied by
// a caller. They live only in the existing owner's JSON and are committed with
// the exact approved closing package, before its first broadcast. Old legacy
// owners have nil InputFacts and never consume this envelope.
type closedWithdrawalInputFacts struct {
	Version  int
	Protocol string
	Inputs   []closedWithdrawalInputFact
}

type closedWithdrawalInputFact struct {
	Outpoint, Protocol, Identity string
	Value                        int64
	PkScript                     []byte
	Assets                       indexer.TxAssets
}

func closedWithdrawalUsesInputFacts(channel *ChannelInDB) bool {
	if channel == nil {
		return false
	}
	if channel.HasProtocolAsset(indexer.PROTOCOL_NAME_ATOM) {
		return true
	}
	for _, commitment := range []*ChannelCommitment{channel.LocalCommitment, channel.RemoteCommitment} {
		if commitment == nil {
			continue
		}
		for _, balances := range []map[AssetName]*Decimal{commitment.LocalBalance, commitment.RemoteBalance} {
			for name, amount := range balances {
				if name.Protocol == indexer.PROTOCOL_NAME_ATOM && amount != nil && amount.Sign() > 0 {
					return true
				}
			}
		}
	}
	return false
}

// Adapter admission is deliberately bounded to the existing indexed native
// classifier and plain fees. It does not give another registered protocol an
// unimplemented receipt merely because its Bitcoin signatures are valid.
func validateIndexedClosedWithdrawalChannel(channel *Channel) error {
	if channel == nil || channel.LocalWallet() == nil || !channel.IsInitiator || channel.RemoteCommitment == nil ||
		channel.RemoteCommitment.DeAnchorTx == nil || channel.RemoteCommitment.RGBClientRecovery != nil ||
		len(channel.AssetFunding) != 0 || len(channel.ClosingAssetProofs) != 0 {
		return fmt.Errorf("missing or mixed indexed closed withdrawal context")
	}
	w := channel.LocalWallet()
	if channel.LocalWalletId != w.GetId() || channel.LocalChanCfg.WalletId != w.GetSubAccount() ||
		channel.LocalChanCfg.PaymentKey == nil || w.GetPaymentPubKey() == nil || !channel.LocalChanCfg.PaymentKey.IsEqual(w.GetPaymentPubKey()) {
		return fmt.Errorf("indexed withdrawal belongs to another account")
	}
	for name, outputs := range channel.FundingUtxos {
		if len(outputs) != 0 && name.Protocol != indexer.PROTOCOL_NAME_ATOM && !indexer.IsPlainAsset(&name.AssetName) {
			return fmt.Errorf("unsupported co-asset in indexed closed withdrawal")
		}
	}
	for _, commitment := range []*ChannelCommitment{channel.LocalCommitment, channel.RemoteCommitment} {
		if commitment == nil {
			continue
		}
		if len(commitment.AssetProofs) != 0 || len(commitment.RecoveryProofs) != 0 {
			return fmt.Errorf("indexed withdrawal cannot consume native sidecar proofs")
		}
		for _, balances := range []map[AssetName]*Decimal{commitment.LocalBalance, commitment.RemoteBalance} {
			for name, amount := range balances {
				if amount != nil && amount.Sign() > 0 && name.Protocol != indexer.PROTOCOL_NAME_ATOM && !indexer.IsPlainAsset(&name.AssetName) {
					return fmt.Errorf("unsupported co-asset in indexed closed withdrawal")
				}
			}
		}
	}
	for _, other := range channel.RemoteCommitment.Others {
		if other == nil || len(other.AssetProofs) != 0 || len(other.RecoveryProofs) != 0 || other.DeAnchorTx != nil {
			return fmt.Errorf("unsupported indexed recovery sidecar")
		}
	}
	return nil
}

func (p *Manager) indexedClosedWithdrawalFact(output *TxOutput) (closedWithdrawalInputFact, error) {
	var fact closedWithdrawalInputFact
	if output == nil || output.OutPoint() == nil || output.Value() <= 0 || len(output.Invalids) != 0 ||
		len(output.Offsets) != 0 || len(output.SatBindingMap) != 0 {
		return fact, fmt.Errorf("indexed withdrawal requires complete valid native input facts")
	}
	fact.Outpoint, fact.Value, fact.PkScript = output.OutPointStr, output.Value(), append([]byte(nil), output.OutValue.PkScript...)
	fact.Assets = output.Assets.Clone()
	if len(output.Assets) == 0 {
		if err := atomicalsPlainInput(output); err != nil {
			return fact, err
		}
		return fact, nil
	}
	if len(output.Assets) != 1 || output.Assets[0].Name.Protocol != indexer.PROTOCOL_NAME_ATOM {
		return fact, fmt.Errorf("indexed withdrawal contains an unsupported or mixed native carrier")
	}
	name := &AssetName{AssetName: output.Assets[0].Name, N: 1}
	protocol, err := stpAssetProtocols.Resolve(&name.AssetName, STPOpForceClose)
	if err != nil {
		return fact, err
	}
	if err := protocol.VerifyInput(name, output); err != nil {
		return fact, err
	}
	fact.Protocol = protocol.Protocol()
	fact.Identity, err = p.atomicalsIdentity(name)
	return fact, err
}

func closedWithdrawalFactOutput(fact closedWithdrawalInputFact) *TxOutput {
	output := indexer.NewTxOutput(fact.Value)
	output.OutPointStr, output.OutValue.PkScript = fact.Outpoint, append([]byte(nil), fact.PkScript...)
	output.Assets = fact.Assets.Clone()
	return output
}

func indexedClosedWithdrawalFrontier(channel *Channel, parent *wire.MsgTx, txs []*wire.MsgTx) ([]string, map[string]string, error) {
	if parent == nil || len(txs) < 2 || len(txs) > 128 {
		return nil, nil, fmt.Errorf("incomplete indexed withdrawal package")
	}
	roots, produced, consumers := make(map[string]bool), make(map[string]bool), make(map[string]string)
	for i, output := range parent.TxOut {
		if output == nil {
			return nil, nil, fmt.Errorf("nil canonical parent output")
		}
		if bytes.Equal(output.PkScript, channel.GetChannelPkScript()) {
			roots[fmt.Sprintf("%s:%d", parent.TxID(), i)] = true
		}
	}
	for _, tx := range txs[1:] {
		if tx == nil || len(tx.TxIn) == 0 || len(tx.TxOut) == 0 {
			return nil, nil, fmt.Errorf("incomplete indexed withdrawal transaction")
		}
		for _, input := range tx.TxIn {
			if input == nil {
				return nil, nil, fmt.Errorf("nil indexed withdrawal input")
			}
			point := input.PreviousOutPoint.String()
			if consumers[point] != "" {
				return nil, nil, fmt.Errorf("indexed withdrawal repeats an input")
			}
			consumers[point] = tx.TxID()
			if !produced[point] {
				roots[point] = true
			}
		}
		for i, output := range tx.TxOut {
			if output == nil || output.Value < 0 {
				return nil, nil, fmt.Errorf("invalid indexed withdrawal output")
			}
			produced[fmt.Sprintf("%s:%d", tx.TxID(), i)] = true
		}
	}
	if len(roots) == 0 || len(roots) > 4096 {
		return nil, nil, fmt.Errorf("indexed withdrawal frontier exceeds its bound")
	}
	points := make([]string, 0, len(roots))
	for point := range roots {
		points = append(points, point)
	}
	sort.Strings(points)
	return points, consumers, nil
}

// The receiver is the captured-account SDK view created below, never the
// live selected wallet. Its mandatory getL1TxOutput preserves RPC failures.
func (p *Manager) validateIndexedWithdrawalCurrent(chains *nativeSettlementChains, output *TxOutput) error {
	spent, err := chains.bitcoin.GetOutspend(output.OutPointStr)
	if err != nil || spent == nil || spent.Spent || spent.SpendingTx != "" {
		return fmt.Errorf("indexed withdrawal carrier is not independently current: %v", err)
	}
	current, err := p.getL1TxOutput(output.OutPointStr)
	if err != nil {
		return fmt.Errorf("read complete current native allocation: %w", err)
	}
	want, err := p.indexedClosedWithdrawalFact(output)
	if err != nil {
		return err
	}
	actual, err := p.indexedClosedWithdrawalFact(current)
	if err != nil || !reflect.DeepEqual(want, actual) {
		return fmt.Errorf("current native allocation differs from validated coloring: %v", err)
	}
	utxo, err := chains.bitcoin.GetUTXO(output.OutPointStr)
	if err != nil || utxo == nil || utxo.OutPoint != output.OutPointStr || utxo.Value != output.Value() || !bytes.Equal(utxo.PkScript, output.OutValue.PkScript) {
		return fmt.Errorf("current native carrier differs from canonical Bitcoin: %v", err)
	}
	return nil
}

func addIndexedWithdrawalAmounts(totals map[indexer.AssetName]int64, output *TxOutput) error {
	for _, asset := range output.Assets {
		amount, err := atomicalsAmount(&asset.Amount)
		if err != nil || asset.Name.Protocol != indexer.PROTOCOL_NAME_ATOM || asset.BindingSat != 1 {
			return fmt.Errorf("invalid indexed withdrawal native amount")
		}
		totals[asset.Name], err = atomicalsAdd(totals[asset.Name], amount)
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *Manager) prepareIndexedClosedWithdrawal(channel *Channel, parent *wire.MsgTx, intent *closedChannelIntent, complete bool) (*ClosingDataInDB, error) {
	if err := validateIndexedClosedWithdrawalChannel(channel); err != nil {
		return nil, err
	}
	if intent == nil || parent == nil || channel.RemoteCommitment.CommitTx == nil || parent.TxID() != channel.RemoteCommitment.CommitTx.TxID() {
		return nil, fmt.Errorf("indexed withdrawal changed its exact server main")
	}
	txs, err := closedWithdrawalPackage(parent, channel.RemoteCommitment)
	if err != nil {
		return nil, err
	}
	points, consumers, err := indexedClosedWithdrawalFrontier(channel, parent, txs)
	if err != nil {
		return nil, err
	}
	client := p.GetIndexerRPCClient()
	if client == nil {
		return nil, fmt.Errorf("indexed withdrawal Bitcoin evidence is unavailable")
	}
	chains, err := p.newNativeSettlementChainsWithEvidence(context.Background(), newIndexerBitcoinEvidenceProvider(client))
	if err != nil {
		return nil, err
	}
	if chains.tip.Height >= math.MaxInt32 {
		return nil, fmt.Errorf("indexed withdrawal target height is out of range")
	}
	actual, err := chains.bitcoinTx(parent.TxID(), true)
	if err != nil || !equalSTPBitcoinTx(actual, parent) {
		return nil, fmt.Errorf("indexed withdrawal server main is not exact canonical confirmed bytes: %v", err)
	}
	if err := chains.verifyBitcoinSpend(actual); err != nil {
		return nil, err
	}
	witness, _, err := p.nativeChannelWitness(channel.LocalWallet(), channel.ChannelId)
	if err != nil || !bytes.Equal(witness, channel.RedeemScript) {
		return nil, fmt.Errorf("indexed withdrawal changed its fixed channel pair")
	}
	// Compose current public facts with the captured account's existing RGB
	// projection. This is the established scoped SDK view, not a new scan or
	// the potentially changed p.wallet/p.rgbManager selection.
	captured := channel.LocalWallet().Clone()
	view, err := p.newScopedRGB11Manager(localRGB11Account{
		WalletID: captured.GetId(), AccountIndex: captured.GetSubAccount(), Address: captured.GetAddress(), Wallet: captured,
	})
	if err != nil {
		return nil, err
	}
	first := intent.InputFacts == nil
	if first && (intent.Step == "withdraw_pending" || complete) {
		return nil, fmt.Errorf("pending indexed withdrawal has no verified current-frontier snapshot")
	}
	if !first && (intent.Step != "withdraw_pending" || intent.Choice != "withdraw" || intent.ChannelID != channel.ChannelId || intent.OldParentID != parent.TxID()) {
		return nil, fmt.Errorf("indexed input facts have no matching durable withdrawal owner")
	}
	snapshot := intent.InputFacts
	if first {
		snapshot = &closedWithdrawalInputFacts{Version: 1, Protocol: indexer.PROTOCOL_NAME_ATOM}
	} else if snapshot.Version != 1 || snapshot.Protocol != indexer.PROTOCOL_NAME_ATOM || len(snapshot.Inputs) != len(points) {
		return nil, fmt.Errorf("invalid indexed withdrawal input-fact snapshot")
	}
	available := make(map[string]*TxOutput)
	expectedBurn := make(map[indexer.AssetName]int64)
	nativeIDs := make(map[string]indexer.AssetName)
	for i, point := range points {
		canonical, err := chains.bitcoinOutput(point)
		if err != nil || !bytes.Equal(canonical.PkScript, channel.GetChannelPkScript()) {
			return nil, fmt.Errorf("indexed withdrawal root changed its fixed-client branch: %v", err)
		}
		var output *TxOutput
		if first {
			output, err = view.getL1TxOutput(point)
			if err != nil {
				return nil, fmt.Errorf("first indexed withdrawal requires all frontier outputs current: %w", err)
			}
		} else {
			if snapshot.Inputs[i].Outpoint != point {
				return nil, fmt.Errorf("indexed withdrawal snapshot changed its exact input closure")
			}
			output = closedWithdrawalFactOutput(snapshot.Inputs[i])
		}
		if output == nil || output.OutPointStr != point || output.Value() != canonical.Value || !bytes.Equal(output.OutValue.PkScript, canonical.PkScript) {
			return nil, fmt.Errorf("indexed withdrawal input facts differ from canonical Bitcoin")
		}
		fact, err := p.indexedClosedWithdrawalFact(output)
		if err != nil {
			return nil, err
		}
		if first {
			snapshot.Inputs = append(snapshot.Inputs, fact)
		} else if !reflect.DeepEqual(fact, snapshot.Inputs[i]) {
			return nil, fmt.Errorf("indexed withdrawal native protocol or identity changed")
		}
		if fact.Identity != "" {
			if name, exists := nativeIDs[fact.Identity]; exists && name != fact.Assets[0].Name {
				return nil, fmt.Errorf("indexed withdrawal has conflicting native identities")
			}
			nativeIDs[fact.Identity] = fact.Assets[0].Name
		}
		pointID, _ := wire.NewOutPointFromString(point)
		if len(output.Assets) != 0 {
			if pointID.Hash != parent.TxHash() || consumers[point] == "" {
				return nil, fmt.Errorf("indexed withdrawal did not recover every fixed-client main allocation")
			}
			if err := addIndexedWithdrawalAmounts(expectedBurn, output); err != nil {
				return nil, err
			}
		}
		spent, err := chains.bitcoin.GetOutspend(point)
		if err != nil || spent == nil || !spent.Spent && spent.SpendingTx != "" {
			return nil, fmt.Errorf("indexed withdrawal input spend is unknown or contradictory: %v", err)
		}
		if spent.Spent {
			if first || consumers[point] == "" || spent.SpendingTx != consumers[point] {
				return nil, fmt.Errorf("indexed withdrawal frontier was spent without this verified package")
			}
		} else if err := view.Manager.validateIndexedWithdrawalCurrent(chains, output); err != nil {
			return nil, err
		}
		available[point] = output.Clone()
	}
	if len(expectedBurn) == 0 {
		return nil, fmt.Errorf("indexed withdrawal has no independently current native client allocation")
	}
	deanchor, err := p.validateIndexedWithdrawalRelease(chains, channel, parent.TxID(), expectedBurn)
	if err != nil {
		return nil, err
	}
	walletScript, err := GetP2TRpkScript(channel.LocalWallet().GetPaymentPubKey())
	if err != nil {
		return nil, err
	}
	allConfirmed := true
	observed := make(map[string]bool)
	for _, tx := range txs[1:] {
		height := int(chains.tip.Height) + 1
		if !first {
			status, err := chains.bitcoin.GetTxStatus(tx.TxID())
			if err != nil || status == nil || status.TxID != tx.TxID() {
				return nil, fmt.Errorf("indexed recovery child status is unavailable: %v", err)
			}
			if status.Confirmed || status.InMempool {
				actual, err := chains.bitcoinTx(tx.TxID(), status.Confirmed)
				if err != nil || !equalSTPBitcoinTx(actual, tx) {
					return nil, fmt.Errorf("indexed recovery child differs from its authorized signed bytes: %v", err)
				}
				observed[tx.TxID()] = true
			}
			if status.Confirmed {
				if status.BlockHeight <= 0 || status.BlockHeight > math.MaxInt32 {
					return nil, fmt.Errorf("indexed recovery child has an invalid confirmed height")
				}
				height = int(status.BlockHeight)
			} else {
				allConfirmed = false
			}
		} else {
			allConfirmed = false
		}
		inputs := make([]*TxOutput, 0, len(tx.TxIn))
		fetcher := txscript.NewMultiPrevOutFetcher(nil)
		inAmounts, outAmounts := make(map[indexer.AssetName]int64), make(map[indexer.AssetName]int64)
		for _, input := range tx.TxIn {
			previous := available[input.PreviousOutPoint.String()]
			if previous == nil {
				return nil, fmt.Errorf("indexed recovery child is missing a complete input dependency")
			}
			inputs = append(inputs, previous.Clone())
			fetcher.AddPrevOut(input.PreviousOutPoint, previous.TxOut())
			if err := addIndexedWithdrawalAmounts(inAmounts, previous); err != nil {
				return nil, err
			}
		}
		if err := VerifySignedTx(tx, fetcher); err != nil {
			return nil, fmt.Errorf("indexed recovery child is not completely signed: %w", err)
		}
		outputs := make([]*TxOutput, len(tx.TxOut))
		for i := range outputs {
			outputs[i] = indexer.GenerateTxOutput(tx, i)
		}
		if err := p.classifySTPAtomicalsOutputsAtHeight(tx, inputs, outputs, height); err != nil {
			return nil, err
		}
		for _, output := range outputs {
			if err := addIndexedWithdrawalAmounts(outAmounts, output); err != nil {
				return nil, err
			}
			available[output.OutPointStr] = output
			if len(output.Assets) != 0 && consumers[output.OutPointStr] == "" && !bytes.Equal(output.OutValue.PkScript, walletScript) {
				return nil, fmt.Errorf("indexed recovery terminal does not pay the exact client wallet")
			}
		}
		if !reflect.DeepEqual(inAmounts, outAmounts) {
			return nil, fmt.Errorf("indexed recovery burns or changes a native allocation")
		}
	}
	if complete && !allConfirmed {
		return nil, fmt.Errorf("indexed recovery is not completely confirmed")
	}
	for point, output := range available {
		pointID, _ := wire.NewOutPointFromString(point)
		if pointID.Hash == parent.TxHash() {
			continue // Root spend/current evidence was checked above.
		}
		if !observed[pointID.Hash.String()] {
			continue // An unbroadcast, fully signed candidate is not a UTXO.
		}
		spent, err := chains.bitcoin.GetOutspend(point)
		if err != nil || spent == nil || !spent.Spent && spent.SpendingTx != "" {
			return nil, fmt.Errorf("indexed recovery output spend is unavailable: %v", err)
		}
		if spent.Spent {
			if consumers[point] == "" || spent.SpendingTx != consumers[point] {
				return nil, fmt.Errorf("indexed recovery output was consumed outside its authorized package")
			}
		} else if len(output.Assets) != 0 {
			if err := view.Manager.validateIndexedWithdrawalCurrent(chains, output); err != nil {
				return nil, err
			}
		}
	}
	// An outspend locator alone is insufficient: every actual consumer must
	// be observed with exactly the signed bytes authorized by the owner.
	for point, consumer := range consumers {
		if output := available[point]; output != nil {
			pointID, _ := wire.NewOutPointFromString(point)
			root := false
			for _, rootPoint := range points {
				root = root || rootPoint == point
			}
			if !root && !observed[pointID.Hash.String()] {
				continue
			}
			spent, err := chains.bitcoin.GetOutspend(point)
			if err != nil || spent == nil || !spent.Spent && spent.SpendingTx != "" {
				return nil, fmt.Errorf("indexed withdrawal consumer evidence is unavailable or contradictory: %v", err)
			}
			if spent.Spent && (spent.SpendingTx != consumer || !observed[consumer]) || !spent.Spent && observed[consumer] {
				return nil, fmt.Errorf("indexed withdrawal has no exact observed authorized consumer")
			}
		}
	}
	if err := chains.stable(); err != nil {
		return nil, err
	}
	if first {
		intent.InputFacts = snapshot // Publish only fully verified facts.
	}
	return &ClosingDataInDB{CloseTx: parent.Copy(), PreTxs: cloneSTPBitcoinChain(txs), DeAnchorTx: deanchor.Copy()}, nil
}

func (p *Manager) validateIndexedWithdrawalRelease(chains *nativeSettlementChains, channel *Channel, parentID string,
	expected map[indexer.AssetName]int64) (*swire.MsgTx, error) {
	// Normal exchange constructs the sidecar before advancing CommitHeight.
	// Preserve its exact marker, rather than rebuilding a different generation
	// at close/restart. Its full non-witness bytes are bound by the stored txid.
	storedMarker := ""
	for _, output := range channel.RemoteCommitment.DeAnchorTx.TxOut {
		if output == nil {
			return nil, fmt.Errorf("nil stored indexed release output")
		}
		kind, data, err := sindexer.ReadDataFromNullDataScript(output.PkScript)
		if err == nil && kind == sindexer.CONTENT_TYPE_CHANNELID {
			if storedMarker != "" {
				return nil, fmt.Errorf("duplicate stored indexed release marker")
			}
			storedMarker = string(data)
		}
	}
	currentMarker := fmt.Sprintf("%s-%d", channel.ChannelId, channel.CommitHeight)
	previousMarker := fmt.Sprintf("%s-%d", channel.ChannelId, channel.CommitHeight-1)
	if channel.CommitHeight < 0 || storedMarker != currentMarker && (channel.CommitHeight == 0 || storedMarker != previousMarker) {
		return nil, fmt.Errorf("stored indexed release marker has a foreign channel generation")
	}
	tx, err := chains.satoshiTx(channel.RemoteCommitment.DeAnchorTx.TxID())
	if err != nil {
		return nil, err
	}
	previous, err := chains.satoshiPrevious(tx)
	if err != nil {
		return nil, err
	}
	inputs, outputs, burned := make(map[indexer.AssetName]int64), make(map[indexer.AssetName]int64), make(map[indexer.AssetName]int64)
	checkAssets := func(assets swire.TxAssets, totals map[indexer.AssetName]int64) error {
		seen := make(map[indexer.AssetName]bool)
		for _, asset := range assets {
			amount, err := atomicalsAmount(&asset.Amount)
			if err != nil || asset.Name.Protocol != indexer.PROTOCOL_NAME_ATOM || asset.BindingSat != 1 || expected[asset.Name] <= 0 || seen[asset.Name] {
				return fmt.Errorf("indexed release contains an invalid or unknown native allocation")
			}
			seen[asset.Name] = true
			totals[asset.Name], err = atomicalsAdd(totals[asset.Name], amount)
			if err != nil {
				return err
			}
		}
		return nil
	}
	for _, input := range tx.TxIn {
		if input == nil || previous.FetchPrevOutput(input.PreviousOutPoint) == nil {
			return nil, fmt.Errorf("indexed release is missing an L2 input dependency")
		}
		output := previous.FetchPrevOutput(input.PreviousOutPoint)
		if !bytes.Equal(output.PkScript, channel.GetChannelPkScript()) {
			return nil, fmt.Errorf("indexed release input belongs to another channel")
		}
		if err := checkAssets(output.Assets, inputs); err != nil {
			return nil, err
		}
	}
	if err := VerifySignedTx_SatsNet(tx, previous); err != nil {
		return nil, err
	}
	descends, markers := 0, 0
	for _, output := range tx.TxOut {
		if output == nil {
			return nil, fmt.Errorf("nil indexed release output")
		}
		if err := checkAssets(output.Assets, outputs); err != nil {
			return nil, err
		}
		kind, data, parseErr := sindexer.ReadDataFromNullDataScript(output.PkScript)
		if parseErr == nil && kind == sindexer.CONTENT_TYPE_DESCENDING {
			payload, err := sindexer.ParseDescendPayload(data)
			if err != nil || payload.Version != 2 || payload.L1TxId != parentID || len(payload.ReturnedOutputVouts) != 0 ||
				(payload.Operation != sindexer.DESCEND_OP_CLOSE && payload.Operation != sindexer.DESCEND_OP_FORCE_CLOSE) {
				return nil, fmt.Errorf("indexed release changed its exact server-main exit")
			}
			descends++
			if err := checkAssets(output.Assets, burned); err != nil {
				return nil, err
			}
		} else if len(output.Assets) != 0 {
			return nil, fmt.Errorf("indexed close retained or redirected a native client allocation")
		}
		if parseErr == nil && kind == sindexer.CONTENT_TYPE_CHANNELID {
			if string(data) != storedMarker {
				return nil, fmt.Errorf("indexed release marker changed its channel generation")
			}
			markers++
		}
	}
	// Only the fixed-client main frontier contributes to expected. Server
	// CSV/pool allocations remain outside this claim and are never counted.
	if descends != 1 || markers != 1 || !reflect.DeepEqual(expected, burned) || !reflect.DeepEqual(inputs, outputs) {
		return nil, fmt.Errorf("indexed release is not the exact conserved client allocation burn")
	}
	return tx, nil
}
