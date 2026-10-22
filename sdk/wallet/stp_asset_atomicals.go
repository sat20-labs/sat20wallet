package wallet

import (
	"bytes"
	"fmt"
	"math"
	"strings"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/atom/coloring"
)

type atomicalsSTPProtocol struct{ stpUTXORecovery }

func (atomicalsSTPProtocol) Protocol() string { return indexer.PROTOCOL_NAME_ATOM }

func (atomicalsSTPProtocol) ValidateAsset(name *indexer.AssetName, operation STPAssetOperation) error {
	if err := validateSTPFungibleAsset(name, indexer.PROTOCOL_NAME_ATOM, operation); err != nil {
		return err
	}
	if len(name.Ticker) > 21 {
		return fmt.Errorf("invalid ARC20 ticker length")
	}
	for _, char := range name.Ticker {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') {
			return fmt.Errorf("ARC20 ticker must be canonical lowercase alphanumeric")
		}
	}
	return nil
}

func (protocol atomicalsSTPProtocol) NormalizeContract(name *indexer.AssetName) error {
	if name == nil {
		return protocol.ValidateAsset(nil, STPOpContract)
	}
	normalized := *name
	normalized.Ticker = strings.ToLower(normalized.Ticker)
	if err := protocol.ValidateAsset(&normalized, STPOpContract); err != nil {
		return err
	}
	*name = normalized
	return nil
}

func (atomicalsSTPProtocol) Policy(*AssetName) STPAssetPolicy {
	return STPAssetPolicy{StubCount: 2, StubValue: 330, PartialTransfer: true, NoOrdinalInputStub: true, ExchangeRecoveryPackages: true}
}

func (atomicalsSTPProtocol) EstimateSplicePayload(*AssetName, int) ([]byte, error) { return nil, nil }

func atomicalsAmount(amount *Decimal) (int64, error) {
	if amount == nil {
		return 0, nil
	}
	if amount.Value == nil || amount.Precision != 0 || !amount.Value.IsInt64() || amount.Sign() < 0 {
		return 0, fmt.Errorf("ARC20 requires nonnegative integral int64 units")
	}
	return amount.Int64(), nil
}

func atomicalsAdd(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, fmt.Errorf("ARC20 value overflow")
	}
	return a + b, nil
}

func (protocol atomicalsSTPProtocol) VerifyInput(name *AssetName, input *TxOutput) error {
	if name == nil || name.N != 1 || input == nil || input.Value() <= 0 || input.TxIn() == nil {
		return fmt.Errorf("invalid ARC20 input or capacity")
	}
	if err := protocol.ValidateAsset(&name.AssetName, STPOpCommit); err != nil {
		return err
	}
	// Check all declared state, including invalid assets, before any generic
	// AlignAsset operation could remove unrelated state from the view.
	if len(input.Assets) != 1 || input.Assets[0].Name != name.AssetName || len(input.Invalids) != 0 || len(input.SatBindingMap) != 0 {
		return fmt.Errorf("ARC20 input %s carries unrelated or invalid assets", input.OutPointStr)
	}
	asset := input.Assets[0]
	amount, err := atomicalsAmount(&asset.Amount)
	if err != nil || amount <= 0 || amount > input.Value() || asset.BindingSat != 1 || len(input.Offsets) != 0 {
		return fmt.Errorf("invalid native ARC20 allocation in %s", input.OutPointStr)
	}
	return nil
}

func atomicalsPlainInput(input *TxOutput) error {
	if input == nil || input.Value() <= 0 || input.TxIn() == nil || len(input.Assets) != 0 || len(input.Invalids) != 0 || len(input.Offsets) != 0 || len(input.SatBindingMap) != 0 {
		return fmt.Errorf("ARC20 fee/stub input must be a known asset-free output")
	}
	return nil
}

func (p *Manager) atomicalsIdentity(name *AssetName) (string, error) {
	if name == nil || name.N != 1 {
		return "", fmt.Errorf("invalid ARC20 capacity")
	}
	ticker := p.getTickerInfo(&name.AssetName)
	if ticker == nil || ticker.AssetName != name.AssetName || ticker.N != 1 || ticker.Divisibility != 0 {
		return "", fmt.Errorf("missing or inconsistent ARC20 ticker metadata")
	}
	if _, err := coloring.AtomicalIDKey(ticker.InscriptionId); err != nil {
		return "", fmt.Errorf("ARC20 ticker has no canonical native identity: %w", err)
	}
	return ticker.InscriptionId, nil
}

// Only native asset facts are carried here. Remaining inputs must be supplied
// by STP's authenticated prevout fetcher after the other adapters have checked
// their own protocol state. A prevout fetcher cannot introduce ARC20 balances.
type atomicalsPlanEvidence struct {
	height   int
	heights  coloring.ActivationHeights
	inputs   map[wire.OutPoint]coloring.Input
	scripts  map[wire.OutPoint][]byte
	expected map[*wire.TxOut]map[string]int64
}

func (p *Manager) newAtomicalsEvidence() (*atomicalsPlanEvidence, error) {
	heights, err := coloring.HeightsForNetwork(GetChainParam().Net)
	if err != nil {
		return nil, err
	}
	height := p.GetSyncHeightL1() + 1
	if height < heights.Activation {
		return nil, fmt.Errorf("ARC20 is not active at height %d", height)
	}
	return &atomicalsPlanEvidence{
		height: height, heights: heights,
		inputs: make(map[wire.OutPoint]coloring.Input), scripts: make(map[wire.OutPoint][]byte),
		expected: make(map[*wire.TxOut]map[string]int64),
	}, nil
}

func (e *atomicalsPlanEvidence) addInput(input *TxOutput, nativeID string, amount int64) error {
	if input == nil || input.TxIn() == nil {
		return fmt.Errorf("invalid ARC20 previous output")
	}
	outpoint := input.TxIn().PreviousOutPoint
	if _, exists := e.inputs[outpoint]; exists {
		return fmt.Errorf("duplicate ARC20 input %s", outpoint)
	}
	fact := coloring.Input{Value: input.Value()}
	if nativeID != "" {
		fact.Assets = []coloring.Asset{{AtomicalID: nativeID, Amount: amount}}
	}
	e.inputs[outpoint] = fact
	e.scripts[outpoint] = append([]byte(nil), input.OutValue.PkScript...)
	return nil
}

type atomicalsPayment struct {
	script []byte
	amount int64
	value  int64
}

// Regular coloring permits a single partial final output per native asset.
// Other recipients must carry exactly their token quantity in satoshis. A
// request with two sub-dust recipients needs a different native operation and
// cannot be represented by adding ordinary Bitcoin stub inputs.
func atomicalsPaymentOutputs(payments []atomicalsPayment) ([]atomicalsPayment, error) {
	result := make([]atomicalsPayment, 0, len(payments))
	var small *atomicalsPayment
	for _, payment := range payments {
		if payment.amount <= 0 || len(payment.script) == 0 || coloring.IsUnspendable(wire.NewTxOut(payment.amount, payment.script)) {
			return nil, fmt.Errorf("invalid ARC20 payment")
		}
		payment.script = append([]byte(nil), payment.script...)
		payment.value = payment.amount
		if payment.amount < 330 {
			if small != nil {
				return nil, fmt.Errorf("regular ARC20 coloring cannot split into two sub-dust recipients")
			}
			payment.value = 330
			copy := payment
			small = &copy
		} else {
			result = append(result, payment)
		}
	}
	if small != nil {
		result = append(result, *small)
	}
	return result, nil
}

func (protocol atomicalsSTPProtocol) Verify(req STPAssetVerification) error {
	if err := verifySTPAssetLayout(req); err != nil {
		return err
	}
	if req.Plan.ProtocolData == nil {
		// Every registered adapter is called even when the channel has none
		// of its assets. No evidence is needed for that empty plan.
		if req.Plan.FixedPrefix || len(req.Plan.Inputs) != 0 || len(req.Plan.Layout) != 0 {
			return fmt.Errorf("missing ARC20 evidence for a nonempty plan")
		}
		return nil
	}
	evidence, ok := req.Plan.ProtocolData.(*atomicalsPlanEvidence)
	if !ok || evidence == nil || req.Plan.PrevFetcher == nil {
		return fmt.Errorf("missing authenticated ARC20 plan evidence")
	}
	facts := make([]coloring.Input, len(req.Tx.TxIn))
	var inputValue int64
	for index, input := range req.Tx.TxIn {
		prev := req.Plan.PrevFetcher.FetchPrevOutput(input.PreviousOutPoint)
		if prev == nil {
			return fmt.Errorf("missing ARC20 transaction prevout %s", input.PreviousOutPoint)
		}
		fact, exists := evidence.inputs[input.PreviousOutPoint]
		if exists {
			if prev.Value != fact.Value || !bytes.Equal(prev.PkScript, evidence.scripts[input.PreviousOutPoint]) {
				return fmt.Errorf("ARC20 previous output changed after planning")
			}
		} else {
			fact.Value = prev.Value
		}
		facts[index] = fact
		var err error
		inputValue, err = atomicalsAdd(inputValue, fact.Value)
		if err != nil {
			return err
		}
	}
	result, err := coloring.ValidateRegularTransfer(req.Tx, facts, evidence.height, evidence.heights)
	if err != nil {
		return err
	}
	want := make(map[string]map[int]int64)
	for index, output := range req.Tx.TxOut {
		if output.Value > inputValue {
			return fmt.Errorf("ARC20 transaction spends more bitcoin than its inputs")
		}
		inputValue -= output.Value
		for id, amount := range evidence.expected[output] {
			if want[id] == nil {
				want[id] = make(map[int]int64)
			}
			want[id][index] = amount
		}
	}
	for id, assignments := range result.Assignments {
		if len(want[id]) != len(assignments) {
			return fmt.Errorf("ARC20 allocation differs from requested recipients")
		}
		for _, assignment := range assignments {
			if want[id][assignment.OutputIndex] != assignment.Amount {
				return fmt.Errorf("ARC20 coloring differs from requested quantity or recipient")
			}
		}
		delete(want, id)
	}
	if len(want) != 0 {
		return fmt.Errorf("ARC20 requested allocation has no input asset")
	}
	return nil
}

func (protocol atomicalsSTPProtocol) PrepareInputs(p *Manager, req STPAssetInputRequest) (*STPAssetInputs, error) {
	if err := protocol.ValidateAsset(req.Asset, req.Operation); err != nil {
		return nil, err
	}
	if req.Operation == STPOpSpliceOut {
		return &STPAssetInputs{}, nil
	}
	inputs, err := prepareSTPUTXOInputs(p, req)
	if err != nil {
		return nil, err
	}
	name := &AssetName{AssetName: *req.Asset, N: 1}
	if _, err := p.atomicalsIdentity(name); err != nil {
		return nil, err
	}
	for index, input := range inputs.Inputs {
		full, err := p.authenticatedAtomicalsInput(name, input)
		if err != nil {
			return nil, err
		}
		inputs.Inputs[index] = full
	}
	return inputs, nil
}
