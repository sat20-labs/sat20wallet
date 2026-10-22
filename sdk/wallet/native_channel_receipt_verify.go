package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// Native settlement receipts describe public evidence. Their transaction IDs,
// source amount and supplied witness never authorize inventory by themselves.
// Each accepted allocation must remain backed by one canonical mapping after
// the actual, signed Bitcoin consumers and paired L2 burns are applied.
func (p *Manager) ValidateNativeChannelReceipt(ctx context.Context, local common.Wallet,
	channelID string, source *NativeChannelSource) error {
	validated, err := p.validateNativeChannelSource(ctx, local, channelID, source, nativeproof.Current)
	if err != nil {
		return err
	}
	chains, err := p.newNativeSettlementChains(ctx, local)
	if err != nil {
		return err
	}
	poolWitness, _, err := p.nativeChannelWitness(local, channelID)
	if err != nil {
		return err
	}
	poolScript, err := utils.WitnessScriptHash(poolWitness)
	if err != nil {
		return err
	}
	bootstrap := p.GetBootstrapNodePaymentPubKey()
	if bootstrap == nil {
		return fmt.Errorf("native receipt has no configured bootstrap identity")
	}
	graph := &nativeReceiptGraph{manager: p, chains: chains, asset: validated.Asset,
		bootstrap: bootstrap.SerializeCompressed(), states: make(map[schemas.InputRef]*nativeReceiptAllocation),
		steps: make(map[string]*nativeReceiptStep), points: make(map[string][]*nativeReceiptAllocation),
		anchorChecked: make(map[string]bool),
		descends:      make(map[string]*nativeReceiptDescend), usedDescends: make(map[string]bool),
		delayedPools: make(map[string]bool)}
	if len(source.PrivateWitness) != 0 {
		if _, err := nativeReceiptMultisigKeys(source.PrivateWitness); err != nil {
			return err
		}
		script, err := utils.WitnessScriptHash(source.PrivateWitness)
		if err != nil || bytes.Equal(script, poolScript) {
			return fmt.Errorf("native private receipt witness is not a private channel")
		}
		graph.requiredPrivate = string(script)
	}
	if len(validated.History.HistoricalStates) == 0 || len(validated.History.HistoricalStates) > 4096 || len(source.DescendTxIDs) > 4096 {
		return fmt.Errorf("native settlement graph exceeds its bounded history")
	}
	opWitness := make(map[[32]byte]string)
	for _, edge := range validated.History.ValidatedConsumers {
		id := chainhash.Hash(edge.WitnessTxID).String()
		if old := opWitness[edge.ConsumerOperationID]; old != "" && old != id {
			return fmt.Errorf("native operation has conflicting Bitcoin witnesses")
		}
		opWitness[edge.ConsumerOperationID] = id
		if graph.steps[id] == nil {
			graph.steps[id] = &nativeReceiptStep{id: id}
		}
	}
	for _, historical := range validated.History.HistoricalStates {
		if historical.Reference.AssignmentType != 4000 || historical.State.Class != "fungible" {
			continue // Core has validated issuance/control states independently.
		}
		if graph.states[historical.Reference] != nil {
			return fmt.Errorf("native receipt repeats an exact allocation")
		}
		point := wire.OutPoint{Hash: historical.Outpoint.TxID, Index: historical.Outpoint.Vout}.String()
		output, err := chains.bitcoinOutput(point)
		if err != nil {
			return err
		}
		allocation := &nativeReceiptAllocation{ref: historical.Reference, point: point, output: output,
			amount: new(big.Int).SetUint64(historical.State.Amount)}
		graph.states[historical.Reference] = allocation
		graph.points[point] = append(graph.points[point], allocation)
		if id := opWitness[historical.Reference.OperationID]; id != "" {
			if point != fmt.Sprintf("%s:%d", id, historical.Outpoint.Vout) {
				return fmt.Errorf("native output differs from its validated witness")
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
			return fmt.Errorf("native receipt has duplicate allocation consumers")
		}
		allocation.consumer = id
		graph.steps[id].inputs = append(graph.steps[id].inputs, allocation)
	}
	for _, id := range source.DescendTxIDs {
		if _, exists := graph.usedDescends[id]; !canonicalNativeTxID(id) || exists {
			return fmt.Errorf("invalid or duplicate native descending locator")
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
	terminal := graph.points[source.Output.OutPointStr]
	if len(terminal) == 0 {
		return fmt.Errorf("native receipt has no exact terminal allocation")
	}
	for _, allocation := range terminal {
		if allocation.mapping == nil || !allocation.mapping.public || !bytes.Equal(allocation.mapping.poolScript, poolScript) || allocation.consumer != "" {
			return fmt.Errorf("native public source is not fully backed by an unrepeated mapping")
		}
	}
	if graph.requiredPrivate != "" && !graph.seenRequiredPrivate {
		return fmt.Errorf("native receipt does not descend from its declared private channel")
	}
	for id, used := range graph.usedDescends {
		if !used {
			return fmt.Errorf("native receipt contains an unrelated descending locator %s", id)
		}
	}
	return chains.stable()
}

type nativeReceiptAllocation struct {
	ref                schemas.InputRef
	point              string
	amount             *big.Int
	output             *wire.TxOut
	producer, consumer string
	mapping            *nativeReceiptMapping
}

type nativeReceiptMapping struct {
	public      bool
	script      []byte
	witness     []byte
	poolScript  []byte
	poolWitness []byte
}

type nativeReceiptStep struct {
	id                 string
	inputs, outputs    []*nativeReceiptAllocation
	visiting, complete bool
}

type nativeReceiptDescend struct {
	id      string
	tx      *swire.MsgTx
	payload *sindexer.DescendPayload
	output  *swire.TxOut
}

type nativeReceiptGraph struct {
	manager             *Manager
	chains              *nativeSettlementChains
	asset               swire.AssetInfo
	bootstrap           []byte
	states              map[schemas.InputRef]*nativeReceiptAllocation
	steps               map[string]*nativeReceiptStep
	points              map[string][]*nativeReceiptAllocation
	anchorChecked       map[string]bool
	descends            map[string]*nativeReceiptDescend
	usedDescends        map[string]bool
	delayedPools        map[string]bool
	requiredPrivate     string
	seenRequiredPrivate bool
}

func nativeReceiptMultisigKeys(witness []byte) ([][]byte, error) {
	kind, addresses, required, err := txscript.ExtractPkScriptAddrs(witness, GetChainParam())
	if err != nil || kind != txscript.MultiSigTy || required != 2 || len(addresses) != 2 {
		return nil, fmt.Errorf("native receipt requires a canonical 2-of-2 channel witness")
	}
	keys := [][]byte{addresses[0].ScriptAddress(), addresses[1].ScriptAddress()}
	expected, err := utils.GenMultiSigScript(keys[0], keys[1])
	if err != nil || !bytes.Equal(expected, witness) {
		return nil, fmt.Errorf("native receipt has a noncanonical channel witness")
	}
	return keys, nil
}

func (g *nativeReceiptGraph) loadDescend(id string) error {
	// The same locator can appear only once, and one Bitcoin consumer can
	// have only one paired native descending transaction in this receipt.
	for _, previous := range g.descends {
		if previous.id == id {
			return fmt.Errorf("duplicate native descending locator")
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
	var receipt *nativeReceiptDescend
	for _, output := range tx.TxOut {
		kind, data, err := sindexer.ReadDataFromNullDataScript(output.PkScript)
		if err != nil || kind != sindexer.CONTENT_TYPE_DESCENDING {
			continue
		}
		payload, err := sindexer.ParseDescendPayload(data)
		if err != nil || receipt != nil || !canonicalNativeTxID(payload.L1TxId) || payload.L1TxId == zeroL1TxId {
			return fmt.Errorf("native receipt has an invalid descending payload")
		}
		receipt = &nativeReceiptDescend{id: id, tx: tx, payload: payload, output: output}
	}
	if receipt == nil || g.descends[receipt.payload.L1TxId] != nil {
		return fmt.Errorf("native receipt has no unique paired descending transaction")
	}
	g.descends[receipt.payload.L1TxId] = receipt
	return nil
}

func nativeAscendMissing(point string, err error) bool {
	// Preserve the existing endpoint's exact not-found responses. Transport,
	// decoding, timeout and arbitrary server failures cannot erase a mapping.
	return err != nil && (err.Error() == "not found" || err.Error() == "GetAscendData "+point+" not found")
}

func (g *nativeReceiptGraph) applyAnchor(point string) error {
	if g.anchorChecked[point] {
		return nil
	}
	g.anchorChecked[point] = true
	allocations := g.points[point]
	if len(allocations) == 0 || !txscript.IsPayToWitnessScriptHash(allocations[0].output.PkScript) {
		return nil
	}
	locator, err := g.manager.l2IndexerClient.GetAscendData(point)
	if nativeAscendMissing(point, err) {
		return nil
	}
	if err != nil || locator == nil || !canonicalNativeTxID(locator.AnchorTxId) {
		return fmt.Errorf("native mapping lookup unavailable: %w", err)
	}
	tx, err := g.chains.satoshiTx(locator.AnchorTxId)
	if err != nil {
		return err
	}
	if len(tx.TxIn) != 1 || tx.TxIn[0].PreviousOutPoint.Index != swire.AnchorTxOutIndex ||
		tx.TxIn[0].Sequence != swire.AnchorTxOutIndex || tx.TxIn[0].PreviousOutPoint.Hash != ([32]byte{}) {
		return fmt.Errorf("native mapping locator does not identify an anchor")
	}
	data, signer, err := CheckAnchorPkScript(tx.TxIn[0].SignatureScript, tx.TxOut, true)
	if err != nil || data.Utxo != point {
		return fmt.Errorf("native mapping source or output-bound signature mismatch: %w", err)
	}
	keys, err := nativeReceiptMultisigKeys(data.WitnessScript)
	if err != nil {
		return err
	}
	script, err := utils.WitnessScriptHash(data.WitnessScript)
	if err != nil || !bytes.Equal(script, allocations[0].output.PkScript) || data.Value < 0 || data.Value > allocations[0].output.Value {
		return fmt.Errorf("native mapping does not bind the actual Bitcoin carrier")
	}
	amount := new(big.Int)
	for _, allocation := range allocations {
		if allocation.mapping != nil {
			return fmt.Errorf("native source attempts to map an already mapped ancestor again")
		}
		amount.Add(amount, allocation.amount)
	}
	if len(data.Assets) != 1 || data.Assets[0].Name != g.asset.Name || data.Assets[0].BindingSat != 0 ||
		data.Assets[0].Amount.Validate() != nil || data.Assets[0].Amount.Precision != g.asset.Amount.Precision || data.Assets[0].Amount.Value.Cmp(amount) != 0 {
		return fmt.Errorf("native mapping did not claim the complete canonical asset amount")
	}
	proof, present, err := sindexer.NativeAssetProof(tx)
	if err != nil || !present {
		return fmt.Errorf("native mapping has no authenticated RGB history: %w", err)
	}
	validated, err := nativeproof.Validate(proof, nativeproof.Source{Outpoint: point, Value: data.Value,
		PkScript: script, Assets: data.Assets, BitcoinNetwork: GetChainParam().Name},
		rgb11wallet.NativeBitcoinResolver(g.chains.ctx, g.chains.bitcoin), nativeproof.Historical)
	if err != nil || validated.Value != allocations[0].output.Value {
		return fmt.Errorf("native mapping history differs from its Bitcoin carrier: %w", err)
	}
	mapping := &nativeReceiptMapping{script: script, witness: append([]byte(nil), data.WitnessScript...)}
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

func sameNativeReceiptMapping(left, right *nativeReceiptMapping) bool {
	return left != nil && right != nil && left.public == right.public && bytes.Equal(left.script, right.script) && bytes.Equal(left.poolScript, right.poolScript)
}

func (g *nativeReceiptGraph) walk(id string) error {
	step := g.steps[id]
	if step == nil || step.complete {
		return nil
	}
	if step.visiting {
		return fmt.Errorf("native receipt graph contains a cycle")
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
	var mapping *nativeReceiptMapping
	mappedIn, totalIn, totalOut := new(big.Int), new(big.Int), new(big.Int)
	for _, input := range step.inputs {
		totalIn.Add(totalIn, input.amount)
		if input.mapping != nil {
			if mapping == nil {
				mapping = input.mapping
			} else if !sameNativeReceiptMapping(mapping, input.mapping) {
				return fmt.Errorf("native consumer mixes independent mapping authorities")
			}
			mappedIn.Add(mappedIn, input.amount)
		}
	}
	for _, output := range step.outputs {
		totalOut.Add(totalOut, output.amount)
	}
	if mapping != nil {
		if mappedIn.Cmp(totalIn) != 0 || totalIn.Cmp(totalOut) != 0 {
			return fmt.Errorf("native mapped consumer %s has incomplete allocation flow (mapped=%s input=%s output=%s)", step.id, mappedIn, totalIn, totalOut)
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

func (g *nativeReceiptGraph) applyMappedStep(step *nativeReceiptStep, tx *wire.MsgTx, mapping *nativeReceiptMapping, mappedIn *big.Int) error {
	descend := g.descends[step.id]
	mappedOut := new(big.Int)
	var nextPool *nativeReceiptMapping
	poolMapping := func() *nativeReceiptMapping {
		if nextPool == nil {
			nextPool = &nativeReceiptMapping{public: true, script: mapping.poolScript, witness: mapping.poolWitness,
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
			return fmt.Errorf("native exit changed its returned channel outputs")
		}
		for i, vout := range returned {
			if descend.payload.ReturnedOutputVouts[i] != vout {
				return fmt.Errorf("native exit returned outputs are not canonical")
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
			return fmt.Errorf("public native withdrawal changed its canonical transaction commitment")
		}
		if descend == nil {
			// A pool-controlled CSV recovery is only a change of carrier; its
			// private close already settled the external allocation. A normal
			// pool withdrawal still requires its independently confirmed burn.
			for _, input := range step.inputs {
				if bytes.Equal(input.output.PkScript, mapping.poolScript) {
					return fmt.Errorf("native public consumer has no paired asset burn")
				}
				pool, err := g.isPoolCarrier(input, mapping)
				if err != nil || !pool {
					return fmt.Errorf("native mapped recovery lacks its exact pool authority: %w", err)
				}
			}
			if mappedIn.Cmp(mappedOut) != 0 {
				return fmt.Errorf("native recovery released mapped assets without a burn")
			}
			return nil
		}
	}
	burned := new(big.Int).Sub(new(big.Int).Set(mappedIn), mappedOut)
	if burned.Sign() < 0 || descend == nil {
		return fmt.Errorf("native exit creates a mapped asset amount")
	}
	if err := g.verifyDescend(descend, mapping, burned); err != nil {
		return err
	}
	g.usedDescends[descend.id] = true
	return nil
}

func (g *nativeReceiptGraph) verifyDescend(receipt *nativeReceiptDescend, mapping *nativeReceiptMapping, burned *big.Int) error {
	amount, err := nativeReceiptAssetAmount(receipt.output.Assets, &g.asset)
	if err != nil || amount.Cmp(burned) != 0 {
		return fmt.Errorf("native descending amount does not match the exact mapped Bitcoin exit")
	}
	previous, err := g.chains.satoshiPrevious(receipt.tx)
	if err != nil {
		return err
	}
	inputAmount, outputAmount := new(big.Int), new(big.Int)
	for _, input := range receipt.tx.TxIn {
		out := previous.FetchPrevOutput(input.PreviousOutPoint)
		amount, err := nativeReceiptAssetAmount(out.Assets, &g.asset)
		if err != nil {
			return err
		}
		if amount.Sign() > 0 && !bytes.Equal(out.PkScript, mapping.script) {
			return fmt.Errorf("native descending assets belong to another channel")
		}
		inputAmount.Add(inputAmount, amount)
	}
	for _, out := range receipt.tx.TxOut {
		amount, err := nativeReceiptAssetAmount(out.Assets, &g.asset)
		if err != nil {
			return err
		}
		if out != receipt.output && amount.Sign() > 0 && !bytes.Equal(out.PkScript, mapping.script) {
			return fmt.Errorf("native descending change left its authenticated channel")
		}
		outputAmount.Add(outputAmount, amount)
	}
	if inputAmount.Cmp(outputAmount) != 0 || (burned.Sign() > 0 && inputAmount.Sign() <= 0) {
		return fmt.Errorf("native descending transaction does not conserve the mapped asset")
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
			return fmt.Errorf("native descending marker names another channel")
		}
		markers++
	}
	if markers != 1 {
		return fmt.Errorf("native descending transaction has no unique channel identity")
	}
	return nil
}

func nativeReceiptAssetAmount(assets swire.TxAssets, expected *swire.AssetInfo) (*big.Int, error) {
	amount := new(big.Int)
	seen := false
	for _, asset := range assets {
		if asset.Name != expected.Name {
			continue
		}
		if seen || asset.BindingSat != 0 || asset.Amount.Validate() != nil || asset.Amount.Precision != expected.Amount.Precision || asset.Amount.Sign() < 0 {
			return nil, fmt.Errorf("native descending allocation is not canonical")
		}
		seen = true
		amount.Set(asset.Amount.Value)
	}
	return amount, nil
}

func (g *nativeReceiptGraph) isPoolCarrier(allocation *nativeReceiptAllocation, mapping *nativeReceiptMapping) (bool, error) {
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
	keys, err := nativeReceiptMultisigKeys(mapping.poolWitness)
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
