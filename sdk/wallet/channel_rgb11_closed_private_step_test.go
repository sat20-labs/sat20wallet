package wallet

import (
	"math/big"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
)

func rgbClosedPrivateStepFixture(t *testing.T) (*rgbClosedPrivateBurnFixture, *nativeReceiptStep) {
	t.Helper()
	f := newRGBClosedPrivateBurnFixture(t)
	id := f.receipt.payload.L1TxId
	f.graph.closedPrivateProducer, f.graph.requiredPrivate = id, string(f.mapping.script)
	f.graph.descends = map[string]*nativeReceiptDescend{id: f.receipt}
	f.graph.usedDescends = map[string]bool{f.receipt.id: false}
	step := &nativeReceiptStep{id: id, inputs: []*nativeReceiptAllocation{{amount: big.NewInt(6000), mapping: f.mapping}}}
	for i, amount := range []int64{2500, 2475, 1025} {
		script := f.mapping.script
		if i == 2 {
			script = []byte{0x51}
		}
		step.outputs = append(step.outputs, &nativeReceiptAllocation{point: id + ":" + string(rune('0'+i)), producer: id,
			amount: big.NewInt(amount), output: wire.NewTxOut(330, script)})
	}
	return f, step
}

func TestSTPRGBClosedPrivateStepSumsAllClientAllocationsOnly(t *testing.T) {
	for _, csvSpent := range []bool{false, true} {
		name := "before-sibling-csv"
		if csvSpent {
			name = "after-sibling-csv"
		}
		t.Run(name, func(t *testing.T) {
			f, step := rgbClosedPrivateStepFixture(t)
			if csvSpent {
				step.outputs[2].consumer = strings.Repeat("d", 64)
			}
			if err := f.graph.applyClosedPrivateStep(step, f.mapping); err != nil {
				t.Fatalf("complete 2500+2475 client set rejected: %v", err)
			}
			for _, output := range step.outputs[:2] {
				if output.mapping != nil {
					t.Fatal("fixed-client allocation remained mapped")
				}
			}
			if step.outputs[2].mapping != f.mapping || step.outputs[2].mapping.public {
				t.Fatal("unclaimed sibling was released or exported as public")
			}
			if !f.graph.usedDescends[f.receipt.id] {
				t.Fatal("exact paired client burn was not consumed")
			}
		})
	}
}

func TestSTPRGBClosedPrivateStepRejectsPartialOrForeignAuthority(t *testing.T) {
	for _, name := range []string{"selectedOnly2500", "historical6000", "wrongMain", "wrongScope", "wrongChannel", "publicMapping", "splicingOut", "returnedVout", "legacy", "missingClient"} {
		t.Run(name, func(t *testing.T) {
			f, step := rgbClosedPrivateStepFixture(t)
			switch name {
			case "selectedOnly2500":
				f.receipt.output.Assets[0].Amount = *indexer.NewDecimalWithScale(2500, 2)
			case "historical6000":
				f.receipt.output.Assets[0].Amount = *indexer.NewDecimalWithScale(6000, 2)
			case "wrongMain":
				f.receipt.payload.L1TxId = strings.Repeat("e", 64)
			case "wrongScope":
				f.graph.closedPrivateProducer = strings.Repeat("e", 64)
			case "wrongChannel":
				f.graph.requiredPrivate = "another-private-channel"
			case "publicMapping":
				f.mapping.public = true
			case "splicingOut":
				f.receipt.payload.Operation = sindexer.DESCEND_OP_SPLICING_OUT
			case "returnedVout":
				f.receipt.payload.ReturnedOutputVouts = []uint32{0}
			case "legacy":
				f.receipt.payload.Version = 1
			case "missingClient":
				for _, output := range step.outputs {
					output.output.PkScript = []byte{0x51}
				}
			}
			if err := f.graph.applyClosedPrivateStep(step, f.mapping); err == nil {
				t.Fatal("invalid closed-private authority accepted")
			}
			if f.graph.usedDescends[f.receipt.id] {
				t.Fatal("failed verification consumed a descending receipt")
			}
			for _, output := range step.outputs {
				if output.mapping != nil {
					t.Fatal("failed verification changed an output mapping")
				}
			}
		})
	}
}

func TestSTPRGBClosedPrivateScopeDoesNotRelaxPublicReceiptGraph(t *testing.T) {
	f, step := rgbClosedPrivateStepFixture(t)
	f.graph.closedPrivateProducer = "" // Public receipt construction never sets it.
	if err := f.graph.applyMappedStep(step, wire.NewMsgTx(2), f.mapping, big.NewInt(6000)); err == nil {
		t.Fatal("ordinary receipt graph accepted a partial private burn as a full exit")
	}
	if f.graph.usedDescends[f.receipt.id] {
		t.Fatal("public path consumed a partial client-only burn")
	}
}

func TestSTPRGBClosedPrivateTerminalRejectsConsumedOrRemappedSelection(t *testing.T) {
	for _, name := range []string{"exact", "selectedMissing", "foreignProducer", "foreignScript", "rgbConsumer", "remappedClient", "releasedSibling", "publicSibling", "unseenPrivate", "incompleteMain", "unusedDescend"} {
		t.Run(name, func(t *testing.T) {
			f, step := rgbClosedPrivateStepFixture(t)
			if err := f.graph.applyClosedPrivateStep(step, f.mapping); err != nil {
				t.Fatal(err)
			}
			step.complete = true
			f.graph.steps = map[string]*nativeReceiptStep{step.id: step}
			selected := step.outputs[0]
			f.graph.points = map[string][]*nativeReceiptAllocation{selected.point: {selected}}
			f.graph.seenRequiredPrivate = true
			switch name {
			case "selectedMissing":
				delete(f.graph.points, selected.point)
			case "foreignProducer":
				selected.producer = strings.Repeat("e", 64)
			case "foreignScript":
				selected.output.PkScript = []byte{0x51}
			case "rgbConsumer":
				selected.consumer = strings.Repeat("e", 64)
			case "remappedClient":
				selected.mapping = f.mapping
			case "releasedSibling":
				step.outputs[2].mapping = nil
			case "publicSibling":
				step.outputs[2].mapping = &nativeReceiptMapping{public: true}
			case "unseenPrivate":
				f.graph.seenRequiredPrivate = false
			case "incompleteMain":
				step.complete = false
			case "unusedDescend":
				f.graph.usedDescends[strings.Repeat("f", 64)] = false
			}
			err := f.graph.validateClosedPrivateTerminal(selected.point, f.mapping.script, step.id)
			if name == "exact" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("invalid retained client selection accepted")
			}
		})
	}
}

func TestSTPRGBClosedPrivateRequiresExactCurrentSource(t *testing.T) {
	key, _ := btcec.PrivKeyFromBytes([]byte{91})
	manager := &Manager{bootstrapNode: []*Node{{Pubkey: key.PubKey()}}}
	id := strings.Repeat("b", 64)
	point := id + ":0"
	script := append([]byte{0, 32}, make([]byte, 32)...)
	for _, name := range []string{"historical", "prepared", "wrongPoint", "wrongScript", "invalidProducer"} {
		t.Run(name, func(t *testing.T) {
			validated := &nativeproof.Result{Mode: nativeproof.Current, Outpoint: point, PkScript: script}
			producer := id
			switch name {
			case "historical":
				validated.Mode = nativeproof.Historical
			case "prepared":
				validated.Mode = nativeproof.Prepared
			case "wrongPoint":
				validated.Outpoint = id + ":1"
			case "wrongScript":
				validated.PkScript = []byte{0x51}
			case "invalidProducer":
				producer = "not-a-transaction"
			}
			if err := manager.validateSTPRGBClosedPrivateTerminal(&nativeSettlementChains{}, validated, point, script, producer, nil); err == nil {
				t.Fatal("non-current or foreign private carrier reached settlement validation")
			}
		})
	}
}
