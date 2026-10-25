package wallet

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/atom/coloring"
)

func TestClosedWithdrawalInputFactsRequireCompleteNativeAllocation(t *testing.T) {
	atomicalsTestEnvironment(t)
	name := atomicalsTestName()
	manager := safetyTestManager(t, &Channel{ChannelInDB: *NewChannelInDB()})
	atomicalsTestConfigure(manager, name)
	input := atomicalsTestInput(77, name, 700, 900, []byte{0x51})
	fact, err := manager.indexedClosedWithdrawalFact(input)
	if err != nil || fact.Protocol != indexer.PROTOCOL_NAME_ATOM || fact.Identity == "" {
		t.Fatalf("complete native fact positive control: %v", err)
	}
	if roundTrip, err := manager.indexedClosedWithdrawalFact(closedWithdrawalFactOutput(fact)); err != nil || !reflect.DeepEqual(fact, roundTrip) {
		t.Fatalf("native fact round trip changed protocol/identity/quantity: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*TxOutput)
	}{
		{"precision", func(o *TxOutput) { o.Assets[0].Amount.Precision = 1 }},
		{"binding", func(o *TxOutput) { o.Assets[0].BindingSat = 0 }},
		{"unknown_protocol", func(o *TxOutput) { o.Assets[0].Name.Protocol = "unknown" }},
		{"mixed_asset", func(o *TxOutput) { o.Assets = append(o.Assets, o.Assets[0]) }},
		{"over_capacity", func(o *TxOutput) { o.OutValue.Value = 699 }},
		{"invalid", func(o *TxOutput) { o.Invalids = map[indexer.AssetName]bool{name.AssetName: true} }},
		{"ordinal_offsets", func(o *TxOutput) {
			o.Offsets = map[indexer.AssetName]indexer.AssetOffsets{name.AssetName: {{Start: 0, End: 1}}}
		}},
		{"sat_binding_map", func(o *TxOutput) { o.SatBindingMap = map[int64]*indexer.AssetInfo{0: o.Assets[0].Clone()} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copy := input.Clone()
			test.mutate(copy)
			if _, err := manager.indexedClosedWithdrawalFact(copy); err == nil {
				t.Fatal("incomplete, mixed or non-native facts were accepted")
			}
		})
	}
	manager.tickerInfoMap[name.String()].InscriptionId = "not-a-native-identity"
	if _, err := manager.indexedClosedWithdrawalFact(input); err == nil {
		t.Fatal("an invalid native identity was accepted")
	}
}

func TestClosedWithdrawalInputFactsRemainOptionalOwnerJSON(t *testing.T) {
	legacy := &closedChannelIntent{Version: 1, ChannelID: "channel", OldParentID: "parent", Choice: "withdraw", Step: "prepared"}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, exists := decoded["InputFacts"]; exists {
		t.Fatal("old legacy owners acquired native verification metadata")
	}
	legacy.InputFacts = &closedWithdrawalInputFacts{Version: 1, Protocol: indexer.PROTOCOL_NAME_ATOM,
		Inputs: []closedWithdrawalInputFact{{Outpoint: "point", Protocol: indexer.PROTOCOL_NAME_ATOM, Identity: "native-id", Value: 900, PkScript: []byte{1}}}}
	raw, err = json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	var restored closedChannelIntent
	if err := json.Unmarshal(raw, &restored); err != nil || !reflect.DeepEqual(restored.InputFacts, legacy.InputFacts) {
		t.Fatalf("optional owner JSON lost its exact verified metadata: %v", err)
	}
	if _, err := NewLocalActionPerformData(1, localActionClosedChannelRecovery, string(raw), 1, nil); err == nil {
		t.Fatal("public local-action construction accepted caller-injected recovery facts")
	}
}

func TestClosedWithdrawalNativeClassificationUsesActualHeight(t *testing.T) {
	atomicalsTestEnvironment(t)
	name := atomicalsTestName()
	manager := safetyTestManager(t, &Channel{ChannelInDB: *NewChannelInDB()})
	atomicalsTestConfigure(manager, name)
	heights, err := coloring.HeightsForNetwork(GetChainParam().Net)
	if err != nil {
		t.Fatal(err)
	}
	input := atomicalsTestInput(78, name, 700, 700, []byte{0x51})
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(input.TxIn())
	tx.AddTxOut(wire.NewTxOut(700, []byte{0x51}))
	outputs := []*TxOutput{indexer.GenerateTxOutput(tx, 0)}
	if err := manager.classifySTPAtomicalsOutputsAtHeight(tx, []*TxOutput{input}, outputs, heights.Activation-1); err == nil {
		t.Fatal("historical pre-activation transfer used today's prospective rules")
	}
	if len(outputs[0].Assets) != 0 {
		t.Fatal("failed historical classification partially mutated its output")
	}
	if err := manager.classifySTPAtomicalsOutputsAtHeight(tx, []*TxOutput{input}, outputs, heights.Activation); err != nil || outputs[0].GetAsset(&name.AssetName).Int64() != 700 {
		t.Fatalf("actual native activation-height positive control: %v", err)
	}
}

func TestClosedWithdrawalFrontierIncludesEveryFixedClientMainOutput(t *testing.T) {
	_, channel := newSignedChannelSyncState(t, &channelHeartbeatTestClient{})
	parent := channel.RemoteCommitment.CommitTx.Copy()
	parent.AddTxOut(wire.NewTxOut(1000, append([]byte(nil), channel.GetChannelPkScript()...)))
	child := wire.NewMsgTx(2)
	point := wire.OutPoint{Hash: parent.TxHash(), Index: 0}
	child.AddTxIn(wire.NewTxIn(&point, nil, nil))
	child.AddTxOut(wire.NewTxOut(1000, []byte{0x51}))
	points, _, err := indexedClosedWithdrawalFrontier(channel, parent, []*wire.MsgTx{parent, child})
	if err != nil || len(points) != 2 {
		t.Fatalf("frontier omitted an unspent fixed-client main carrier: %v %v", points, err)
	}
	child.TxIn = append(child.TxIn, child.TxIn[0])
	if _, _, err := indexedClosedWithdrawalFrontier(channel, parent, []*wire.MsgTx{parent, child}); err == nil {
		t.Fatal("native frontier accepted duplicate package consumption")
	}
}
