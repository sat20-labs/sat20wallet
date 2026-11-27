package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// RGB11 settlement receipts describe public evidence. Their transaction IDs,
// source amount and supplied witness never authorize inventory by themselves.
// Each accepted allocation must remain backed by one canonical mapping after
// the actual, signed Bitcoin consumers and paired L2 burns are applied.
func (p *Manager) ValidateRGB11ChannelReceipt(ctx context.Context, local common.Wallet,
	channelID string, source *RGB11ChannelSource) error {
	validated, err := p.validateRGB11ChannelSource(ctx, local, channelID, source, nativeproof.Current)
	if err != nil {
		return err
	}
	chains, err := p.newSettlementChains(ctx, local)
	if err != nil {
		return err
	}
	poolWitness, _, err := p.channelOwnerWitness(local, channelID)
	if err != nil {
		return err
	}
	poolScript, err := utils.WitnessScriptHash(poolWitness)
	if err != nil {
		return err
	}
	bootstrap := p.GetBootstrapNodePaymentPubKey()
	if bootstrap == nil {
		return fmt.Errorf("RGB11 receipt has no configured bootstrap identity")
	}
	graph := &rgb11ReceiptGraph{manager: p, chains: chains, asset: validated.Asset, contractID: validated.ContractID,
		bootstrap: bootstrap.SerializeCompressed(), states: make(map[schemas.InputRef]*rgb11ReceiptAllocation),
		steps: make(map[string]*rgb11ReceiptStep), points: make(map[string][]*rgb11ReceiptAllocation),
		anchorChecked: make(map[string]bool),
		descends:      make(map[string]*rgb11ReceiptDescend), usedDescends: make(map[string]bool),
		delayedPools: make(map[string]bool)}
	if len(source.PrivateWitness) != 0 {
		if _, err := rgb11ReceiptMultisigKeys(source.PrivateWitness); err != nil {
			return err
		}
		script, err := utils.WitnessScriptHash(source.PrivateWitness)
		if err != nil || bytes.Equal(script, poolScript) {
			return fmt.Errorf("RGB11 private receipt witness is not a private channel")
		}
		graph.requiredPrivate = string(script)
	}
	if err := graph.walkValidatedHistory(validated, source.DescendTxIDs); err != nil {
		return err
	}
	terminal := graph.points[source.Output.OutPointStr]
	if len(terminal) == 0 {
		return fmt.Errorf("RGB11 receipt has no exact terminal allocation")
	}
	for _, allocation := range terminal {
		if allocation.mapping == nil || !allocation.mapping.public || !bytes.Equal(allocation.mapping.poolScript, poolScript) || allocation.consumer != "" {
			return fmt.Errorf("RGB11 public source is not fully backed by an unrepeated mapping")
		}
	}
	if graph.requiredPrivate != "" && !graph.seenRequiredPrivate {
		return fmt.Errorf("RGB11 receipt does not descend from its declared private channel")
	}
	for id, used := range graph.usedDescends {
		if !used {
			return fmt.Errorf("RGB11 receipt contains an unrelated descending locator %s", id)
		}
	}
	return chains.stable()
}

type rgb11ReceiptAllocation struct {
	ref                schemas.InputRef
	point              string
	amount             *big.Int
	output             *wire.TxOut
	producer, consumer string
	mapping            *rgb11ReceiptMapping
}

type rgb11ReceiptMapping struct {
	public      bool
	assetName   swire.AssetName
	script      []byte
	witness     []byte
	poolScript  []byte
	poolWitness []byte
}

type rgb11ReceiptStep struct {
	id                 string
	inputs, outputs    []*rgb11ReceiptAllocation
	visiting, complete bool
}

type rgb11ReceiptDescend struct {
	id      string
	tx      *swire.MsgTx
	payload *sindexer.DescendPayload
	output  *swire.TxOut
}

type rgb11ReceiptGraph struct {
	manager             *Manager
	chains              *settlementChains
	asset               swire.AssetInfo
	contractID          string
	bootstrap           []byte
	states              map[schemas.InputRef]*rgb11ReceiptAllocation
	steps               map[string]*rgb11ReceiptStep
	points              map[string][]*rgb11ReceiptAllocation
	anchorChecked       map[string]bool
	descends            map[string]*rgb11ReceiptDescend
	usedDescends        map[string]bool
	delayedPools        map[string]bool
	requiredPrivate     string
	seenRequiredPrivate bool
	// Set only by the account-bound closed-private source validator. This
	// ephemeral graph is never returned as a public receipt or inventory.
	closedPrivateProducer string
}

// walkValidatedHistory applies only the shared, independently observed graph
// facts. Public-pool and closed-private sources keep distinct terminal rules.
func (graph *rgb11ReceiptGraph) walkValidatedHistory(validated *nativeproof.Result, descendTxIDs []string) error {
	if len(validated.History.HistoricalStates) == 0 || len(validated.History.HistoricalStates) > 4096 || len(descendTxIDs) > 4096 {
		return fmt.Errorf("RGB11 settlement graph exceeds its bounded history")
	}
	opWitness := make(map[[32]byte]string)
	for _, edge := range validated.History.ValidatedConsumers {
		id := chainhash.Hash(edge.WitnessTxID).String()
		if old := opWitness[edge.ConsumerOperationID]; old != "" && old != id {
			return fmt.Errorf("RGB11 operation has conflicting Bitcoin witnesses")
		}
		opWitness[edge.ConsumerOperationID] = id
		if graph.steps[id] == nil {
			graph.steps[id] = &rgb11ReceiptStep{id: id}
		}
	}
	for _, historical := range validated.History.HistoricalStates {
		if historical.Reference.AssignmentType != 4000 || historical.State.Class != "fungible" {
			continue // Core has validated issuance/control states independently.
		}
		if graph.states[historical.Reference] != nil {
			return fmt.Errorf("RGB11 receipt repeats an exact allocation")
		}
		point := wire.OutPoint{Hash: historical.Outpoint.TxID, Index: historical.Outpoint.Vout}.String()
		output, err := graph.chains.bitcoinOutput(point)
		if err != nil {
			return err
		}
		allocation := &rgb11ReceiptAllocation{ref: historical.Reference, point: point, output: output,
			amount: new(big.Int).SetUint64(historical.State.Amount)}
		graph.states[historical.Reference] = allocation
		graph.points[point] = append(graph.points[point], allocation)
		if id := opWitness[historical.Reference.OperationID]; id != "" {
			if point != fmt.Sprintf("%s:%d", id, historical.Outpoint.Vout) {
				return fmt.Errorf("RGB11 output differs from its validated witness")
			}
			allocation.producer = id
			graph.steps[id].outputs = append(graph.steps[id].outputs, allocation)
		}
	}
	for _, edge := range validated.History.ValidatedConsumers {
		allocation := graph.states[edge.Input]
		if allocation == nil {
			continue
		}
		id := chainhash.Hash(edge.WitnessTxID).String()
		if allocation.consumer != "" {
			return fmt.Errorf("RGB11 receipt has duplicate allocation consumers")
		}
		allocation.consumer = id
		graph.steps[id].inputs = append(graph.steps[id].inputs, allocation)
	}
	for _, id := range descendTxIDs {
		if _, exists := graph.usedDescends[id]; !canonicalSettlementTxID(id) || exists {
			return fmt.Errorf("invalid or duplicate RGB11 descending locator")
		}
		graph.usedDescends[id] = false
		if err := graph.loadDescend(id); err != nil {
			return err
		}
	}
	// A deterministic topological walk never lets a proof attach a later
	// mapping to an earlier, already consumed allocation.
	var roots []string
	for point, allocations := range graph.points {
		if allocations[0].producer == "" {
			roots = append(roots, point)
		}
	}
	sort.Strings(roots)
	for _, point := range roots {
		if err := graph.applyAnchor(point); err != nil {
			return err
		}
	}
	var ids []string
	for id := range graph.steps {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := graph.walk(id); err != nil {
			return err
		}
	}
	return nil
}

func rgb11ReceiptMultisigKeys(witness []byte) ([][]byte, error) {
	kind, addresses, required, err := txscript.ExtractPkScriptAddrs(witness, GetChainParam())
	if err != nil || kind != txscript.MultiSigTy || required != 2 || len(addresses) != 2 {
		return nil, fmt.Errorf("RGB11 receipt requires a canonical 2-of-2 channel witness")
	}
	keys := [][]byte{addresses[0].ScriptAddress(), addresses[1].ScriptAddress()}
	expected, err := utils.GenMultiSigScript(keys[0], keys[1])
	if err != nil || !bytes.Equal(expected, witness) {
		return nil, fmt.Errorf("RGB11 receipt has a noncanonical channel witness")
	}
	return keys, nil
}

func (g *rgb11ReceiptGraph) loadDescend(id string) error {
	// The same locator can appear only once, and one Bitcoin consumer can
	// have only one paired RGB11 descending transaction in this receipt.
	for _, previous := range g.descends {
		if previous.id == id {
			return fmt.Errorf("duplicate RGB11 descending locator")
		}
	}
	tx, err := g.chains.satoshiTx(id)
	if err != nil {
		return err
	}
	previous, err := g.chains.satoshiPrevious(tx)
	if err != nil {
		return err
	}
	if err := VerifySignedTx_SatsNet(tx, previous); err != nil {
		return err
	}
	var receipt *rgb11ReceiptDescend
	for _, output := range tx.TxOut {
		kind, data, err := sindexer.ReadDataFromNullDataScript(output.PkScript)
		if err != nil || kind != sindexer.CONTENT_TYPE_DESCENDING {
			continue
		}
		payload, err := sindexer.ParseDescendPayload(data)
		if err != nil || receipt != nil || !canonicalSettlementTxID(payload.L1TxId) || payload.L1TxId == zeroL1TxId {
			return fmt.Errorf("RGB11 receipt has an invalid descending payload")
		}
		receipt = &rgb11ReceiptDescend{id: id, tx: tx, payload: payload, output: output}
	}
	if receipt == nil || g.descends[receipt.payload.L1TxId] != nil {
		return fmt.Errorf("RGB11 receipt has no unique paired descending transaction")
	}
	g.descends[receipt.payload.L1TxId] = receipt
	return nil
}

func rgb11AscendMissing(point string, err error) bool {
	// Only explicit absence permits inheriting the existing parent mapping.
	// Transport, decoding and server failures cannot erase a mapping.
	return errors.Is(err, indexer.ErrKeyNotFound)
}

func (g *rgb11ReceiptGraph) applyAnchor(point string) error {
	if g.anchorChecked[point] {
		return nil
	}
	g.anchorChecked[point] = true
	allocations := g.points[point]
	if len(allocations) == 0 || !txscript.IsPayToWitnessScriptHash(allocations[0].output.PkScript) {
		return nil
	}
	locator, err := g.manager.l2IndexerClient.GetAscendData(point)
	if rgb11AscendMissing(point, err) {
		return nil
	}
	if err != nil || locator == nil || !canonicalSettlementTxID(locator.AnchorTxId) {
		return fmt.Errorf("RGB11 mapping lookup unavailable: %w", err)
	}
	tx, err := g.chains.satoshiTx(locator.AnchorTxId)
	if err != nil {
		return err
	}
	if len(tx.TxIn) != 1 || tx.TxIn[0].PreviousOutPoint.Index != swire.AnchorTxOutIndex ||
		tx.TxIn[0].Sequence != swire.AnchorTxOutIndex || tx.TxIn[0].PreviousOutPoint.Hash != ([32]byte{}) {
		return fmt.Errorf("RGB11 mapping locator does not identify an anchor")
	}
	data, signer, err := CheckAnchorPkScript(tx.TxIn[0].SignatureScript, tx.TxOut, true)
	if err != nil || data.Utxo != point {
		return fmt.Errorf("RGB11 mapping source or output-bound signature mismatch: %w", err)
	}
	keys, err := rgb11ReceiptMultisigKeys(data.WitnessScript)
	if err != nil {
		return err
	}
	script, err := utils.WitnessScriptHash(data.WitnessScript)
	if err != nil || !bytes.Equal(script, allocations[0].output.PkScript) || data.Value < 0 || data.Value > allocations[0].output.Value {
		return fmt.Errorf("RGB11 mapping does not bind the actual Bitcoin carrier")
	}
	amount := new(big.Int)
	for _, allocation := range allocations {
		if allocation.mapping != nil {
			return fmt.Errorf("RGB11 source attempts to map an already mapped ancestor again")
		}
		amount.Add(amount, allocation.amount)
	}
	if len(data.Assets) != 1 || data.Assets[0].BindingSat != 0 ||
		data.Assets[0].Amount.Validate() != nil || data.Assets[0].Amount.Precision != g.asset.Amount.Precision || data.Assets[0].Amount.Value.Cmp(amount) != 0 {
		return fmt.Errorf("RGB11 mapping did not claim the complete canonical asset amount")
	}
	proof, present, err := sindexer.NativeAssetProof(tx)
	if err != nil || !present {
		return fmt.Errorf("RGB11 mapping has no authenticated RGB history: %w", err)
	}
	validated, err := nativeproof.Validate(proof, nativeproof.Source{Outpoint: point, Value: data.Value,
		PkScript: script, Assets: data.Assets, BitcoinNetwork: GetChainParam().Name, VerifyName: dkvs.VerifyRGB11Name},
		rgb11wallet.RGB11BitcoinResolver(g.chains.ctx, g.chains.bitcoin), nativeproof.Historical)
	if err != nil || validated.Value != allocations[0].output.Value ||
		g.asset.Name != validated.Asset.Name || g.contractID != validated.ContractID {
		return fmt.Errorf("RGB11 mapping history differs from its Bitcoin carrier: %w", err)
	}
	mapping := &rgb11ReceiptMapping{script: script, witness: append([]byte(nil), data.WitnessScript...), assetName: data.Assets[0].Name}
	if bytes.Equal(keys[0], g.bootstrap) || bytes.Equal(keys[1], g.bootstrap) {
		mapping.public, mapping.poolScript, mapping.poolWitness = true, script, mapping.witness
	} else {
		mapping.poolWitness, mapping.poolScript, err = GetP2WSHscript(signer, g.bootstrap)
		if err != nil {
			return err
		}
		if string(script) == g.requiredPrivate {
			g.seenRequiredPrivate = true
		}
	}
	for _, allocation := range allocations {
		allocation.mapping = mapping
	}
	return nil
}

func sameRGB11ReceiptMapping(left, right *rgb11ReceiptMapping) bool {
	return left != nil && right != nil && left.public == right.public && left.assetName == right.assetName &&
		bytes.Equal(left.script, right.script) && bytes.Equal(left.poolScript, right.poolScript)
}

func (g *rgb11ReceiptGraph) walk(id string) error {
	step := g.steps[id]
	if step == nil || step.complete {
		return nil
	}
	if step.visiting {
		return fmt.Errorf("RGB11 receipt graph contains a cycle")
	}
	step.visiting = true
	for _, input := range step.inputs {
		if input.producer != "" {
			if err := g.walk(input.producer); err != nil {
				return err
			}
		}
	}
	tx, err := g.chains.bitcoinTx(id, true)
	if err != nil {
		return err
	}
	if err := g.chains.verifyBitcoinSpend(tx); err != nil {
		return err
	}
	var mapping *rgb11ReceiptMapping
	mappedIn, totalIn, totalOut := new(big.Int), new(big.Int), new(big.Int)
	for _, input := range step.inputs {
		totalIn.Add(totalIn, input.amount)
		if input.mapping != nil {
			if mapping == nil {
				mapping = input.mapping
			} else if !sameRGB11ReceiptMapping(mapping, input.mapping) {
				return fmt.Errorf("RGB11 consumer mixes independent mapping authorities")
			}
			mappedIn.Add(mappedIn, input.amount)
		}
	}
	for _, output := range step.outputs {
		totalOut.Add(totalOut, output.amount)
	}
	if mapping != nil {
		if mappedIn.Cmp(totalIn) != 0 || totalIn.Cmp(totalOut) != 0 {
			return fmt.Errorf("RGB11 mapped consumer %s has incomplete allocation flow (mapped=%s input=%s output=%s)", step.id, mappedIn, totalIn, totalOut)
		}
		if err := g.applyMappedStep(step, tx, mapping, mappedIn); err != nil {
			return err
		}
	}
	seen := make(map[string]bool)
	for _, output := range step.outputs {
		if !seen[output.point] {
			seen[output.point] = true
			if err := g.applyAnchor(output.point); err != nil {
				return err
			}
		}
	}
	step.visiting, step.complete = false, true
	return nil
}

func (g *rgb11ReceiptGraph) applyMappedStep(step *rgb11ReceiptStep, tx *wire.MsgTx, mapping *rgb11ReceiptMapping, mappedIn *big.Int) error {
	if g.closedPrivateProducer != "" && step.id == g.closedPrivateProducer {
		return g.applyClosedPrivateStep(step, mapping)
	}
	descend := g.descends[step.id]
	mappedOut := new(big.Int)
	var nextPool *rgb11ReceiptMapping
	poolMapping := func() *rgb11ReceiptMapping {
		if nextPool == nil {
			nextPool = &rgb11ReceiptMapping{public: true, assetName: mapping.assetName, script: mapping.poolScript, witness: mapping.poolWitness,
				poolScript: mapping.poolScript, poolWitness: mapping.poolWitness}
		}
		return nextPool
	}
	if !mapping.public {
		if descend == nil || descend.payload.Version != 2 || (descend.payload.Operation != sindexer.DESCEND_OP_SPLICING_OUT &&
			descend.payload.Operation != sindexer.DESCEND_OP_CLOSE && descend.payload.Operation != sindexer.DESCEND_OP_FORCE_CLOSE) {
			return fmt.Errorf("mapped private RGB consumer has no canonical v2 exit")
		}
		var returned []uint32
		if descend.payload.Operation == sindexer.DESCEND_OP_SPLICING_OUT {
			for i, output := range tx.TxOut {
				if bytes.Equal(output.PkScript, mapping.script) {
					returned = append(returned, uint32(i))
				}
			}
		}
		if len(returned) != len(descend.payload.ReturnedOutputVouts) {
			return fmt.Errorf("RGB11 exit changed its returned channel outputs")
		}
		for i, vout := range returned {
			if descend.payload.ReturnedOutputVouts[i] != vout {
				return fmt.Errorf("RGB11 exit returned outputs are not canonical")
			}
		}
		for _, output := range step.outputs {
			if descend.payload.Operation == sindexer.DESCEND_OP_SPLICING_OUT && bytes.Equal(output.output.PkScript, mapping.script) {
				output.mapping = mapping
			} else {
				pool, err := g.isPoolCarrier(output, mapping)
				if err != nil {
					return err
				}
				if pool {
					output.mapping = poolMapping()
				}
			}
			if output.mapping != nil {
				mappedOut.Add(mappedOut, output.amount)
			}
		}
	} else {
		for _, output := range step.outputs {
			pool, err := g.isPoolCarrier(output, mapping)
			if err != nil {
				return err
			}
			if pool {
				output.mapping = mapping
				mappedOut.Add(mappedOut, output.amount)
			}
		}
		if descend != nil && descend.payload.Version != 1 {
			return fmt.Errorf("public RGB11 withdrawal changed its canonical transaction commitment")
		}
		if descend == nil {
			// A pool-controlled CSV recovery is only a change of carrier; its
			// private close already settled the external allocation. A normal
			// pool withdrawal still requires its independently confirmed burn.
			for _, input := range step.inputs {
				if bytes.Equal(input.output.PkScript, mapping.poolScript) {
					return fmt.Errorf("RGB11 public consumer has no paired asset burn")
				}
				pool, err := g.isPoolCarrier(input, mapping)
				if err != nil || !pool {
					return fmt.Errorf("RGB11 mapped recovery lacks its exact pool authority: %w", err)
				}
			}
			if mappedIn.Cmp(mappedOut) != 0 {
				return fmt.Errorf("RGB11 recovery released mapped assets without a burn")
			}
			return nil
		}
	}
	burned := new(big.Int).Sub(new(big.Int).Set(mappedIn), mappedOut)
	if burned.Sign() < 0 || descend == nil {
		return fmt.Errorf("RGB11 exit creates a mapped asset amount")
	}
	if err := g.verifyDescend(descend, mapping, burned); err != nil {
		return err
	}
	g.usedDescends[descend.id] = true
	return nil
}

// Only the exact old server-main step in the closed-private validator can
// release its fixed-client allocations independently of the sibling's future
// CSV spend. The shared walk has already checked the complete RGB flow,
// identical input authority, canonical confirmed Bitcoin transaction and spend.
func (g *rgb11ReceiptGraph) applyClosedPrivateStep(step *rgb11ReceiptStep, mapping *rgb11ReceiptMapping) error {
	descend := g.descends[step.id]
	if step.id != g.closedPrivateProducer || mapping == nil || mapping.public || g.requiredPrivate == "" ||
		string(mapping.script) != g.requiredPrivate || descend == nil || descend.payload.Version != 2 ||
		descend.payload.L1TxId != step.id || (descend.payload.Operation != sindexer.DESCEND_OP_CLOSE &&
		descend.payload.Operation != sindexer.DESCEND_OP_FORCE_CLOSE) || len(descend.payload.ReturnedOutputVouts) != 0 {
		return fmt.Errorf("RGB closed source has no exact private server-main exit")
	}
	// Derive the burn from every validated allocation of this asset paid to
	// the fixed client script, never from the selected carrier's amount.
	burned := new(big.Int)
	for _, output := range step.outputs {
		if bytes.Equal(output.output.PkScript, mapping.script) {
			burned.Add(burned, output.amount)
		}
	}
	if burned.Sign() <= 0 {
		return fmt.Errorf("RGB closed source has no fixed-client allocation")
	}
	if err := g.verifyDescend(descend, mapping, burned); err != nil {
		return err
	}
	for _, output := range step.outputs {
		if !bytes.Equal(output.output.PkScript, mapping.script) {
			// Conservative, graph-local non-release only. This does not prove
			// the sibling's destination or spendable mapping, claim its assets,
			// or export it as a public receipt/current inventory source.
			output.mapping = mapping
		}
	}
	g.usedDescends[descend.id] = true
	return nil
}

func (g *rgb11ReceiptGraph) verifyDescend(receipt *rgb11ReceiptDescend, mapping *rgb11ReceiptMapping, burned *big.Int) error {
	asset := g.asset
	asset.Name = mapping.assetName
	amount, err := rgb11ReceiptAssetAmount(receipt.output.Assets, &asset)
	if err != nil || amount.Cmp(burned) != 0 {
		return fmt.Errorf("RGB11 descending amount does not match the exact mapped Bitcoin exit")
	}
	previous, err := g.chains.satoshiPrevious(receipt.tx)
	if err != nil {
		return err
	}
	inputAmount, outputAmount := new(big.Int), new(big.Int)
	for _, input := range receipt.tx.TxIn {
		out := previous.FetchPrevOutput(input.PreviousOutPoint)
		amount, err := rgb11ReceiptAssetAmount(out.Assets, &asset)
		if err != nil {
			return err
		}
		if amount.Sign() > 0 && !bytes.Equal(out.PkScript, mapping.script) {
			return fmt.Errorf("RGB11 descending assets belong to another channel")
		}
		inputAmount.Add(inputAmount, amount)
	}
	for _, out := range receipt.tx.TxOut {
		amount, err := rgb11ReceiptAssetAmount(out.Assets, &asset)
		if err != nil {
			return err
		}
		if out != receipt.output && amount.Sign() > 0 && !bytes.Equal(out.PkScript, mapping.script) {
			return fmt.Errorf("RGB11 descending change left its authenticated channel")
		}
		outputAmount.Add(outputAmount, amount)
	}
	if inputAmount.Cmp(outputAmount) != 0 || (burned.Sign() > 0 && inputAmount.Sign() <= 0) {
		return fmt.Errorf("RGB11 descending transaction does not conserve the mapped asset")
	}
	channel, err := AddrFromPkScript(mapping.script)
	if err != nil {
		return err
	}
	markers := 0
	for _, out := range receipt.tx.TxOut {
		kind, data, err := sindexer.ReadDataFromNullDataScript(out.PkScript)
		if err != nil || kind != sindexer.CONTENT_TYPE_CHANNELID {
			continue
		}
		if string(data) != channel && !strings.HasPrefix(string(data), channel+"-") {
			return fmt.Errorf("RGB11 descending marker names another channel")
		}
		markers++
	}
	if markers != 1 {
		return fmt.Errorf("RGB11 descending transaction has no unique channel identity")
	}
	return nil
}

func rgb11ReceiptAssetAmount(assets swire.TxAssets, expected *swire.AssetInfo) (*big.Int, error) {
	amount := new(big.Int)
	seen := false
	for _, asset := range assets {
		if asset.Name != expected.Name {
			continue
		}
		if seen || asset.BindingSat != 0 || asset.Amount.Validate() != nil || asset.Amount.Precision != expected.Amount.Precision || asset.Amount.Sign() < 0 {
			return nil, fmt.Errorf("RGB11 descending allocation is not canonical")
		}
		seen = true
		amount.Set(asset.Amount.Value)
	}
	return amount, nil
}

func (g *rgb11ReceiptGraph) isPoolCarrier(allocation *rgb11ReceiptAllocation, mapping *rgb11ReceiptMapping) (bool, error) {
	if bytes.Equal(allocation.output.PkScript, mapping.poolScript) {
		return true, nil
	}
	if !txscript.IsPayToWitnessScriptHash(allocation.output.PkScript) || allocation.consumer == "" {
		return false, nil
	}
	cacheKey := allocation.point + ":" + hex.EncodeToString(mapping.poolScript)
	if g.delayedPools[cacheKey] {
		return true, nil
	}
	consumer, err := g.chains.bitcoinTx(allocation.consumer, true)
	if err != nil {
		return false, err
	}
	if err := g.chains.verifyBitcoinSpend(consumer); err != nil {
		return false, err
	}
	var redeem []byte
	for _, input := range consumer.TxIn {
		if input.PreviousOutPoint.String() == allocation.point && len(input.Witness) != 0 {
			redeem = input.Witness[len(input.Witness)-1]
			break
		}
	}
	script, err := utils.WitnessScriptHash(redeem)
	if err != nil || !bytes.Equal(script, allocation.output.PkScript) {
		return false, nil
	}
	// Match the existing server CSV template exactly. The actual consumer's
	// script verification above separately proves the authorized spend path.
	tokenizer := txscript.MakeScriptTokenizer(0, redeem)
	var ops []byte
	var data [][]byte
	for tokenizer.Next() {
		ops = append(ops, tokenizer.Opcode())
		data = append(data, append([]byte(nil), tokenizer.Data()...))
	}
	if tokenizer.Err() != nil || len(ops) != 13 || ops[0] != txscript.OP_IF || len(data[1]) != 33 ||
		ops[2] != txscript.OP_CHECKSIG || ops[3] != txscript.OP_ELSE || ops[5] != txscript.OP_CHECKSEQUENCEVERIFY ||
		ops[6] != txscript.OP_DROP || ops[7] != txscript.OP_2 || ops[10] != txscript.OP_2 ||
		ops[11] != txscript.OP_CHECKMULTISIG || ops[12] != txscript.OP_ENDIF {
		return false, nil
	}
	keys, err := rgb11ReceiptMultisigKeys(mapping.poolWitness)
	if err != nil || !bytes.Equal(keys[0], data[8]) || !bytes.Equal(keys[1], data[9]) {
		return false, nil
	}
	var delay int64
	if ops[4] >= txscript.OP_1 && ops[4] <= txscript.OP_16 {
		delay = int64(ops[4] - (txscript.OP_1 - 1))
	} else {
		value, err := txscript.MakeScriptNum(data[4], true, 5)
		if err != nil {
			return false, nil
		}
		delay = int64(value)
	}
	if delay <= 0 || delay > 65535 {
		return false, nil
	}
	first, err := btcec.ParsePubKey(keys[0])
	if err != nil {
		return false, err
	}
	second, err := btcec.ParsePubKey(keys[1])
	if err != nil {
		return false, err
	}
	revocation, err := btcec.ParsePubKey(data[1])
	if err != nil {
		return false, err
	}
	expected, err := utils.CommitScriptToSelf2(uint32(delay), first, second, revocation)
	if err != nil || !bytes.Equal(expected, redeem) {
		return false, nil
	}
	g.delayedPools[cacheKey] = true
	return true, nil
}
