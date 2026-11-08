package wallet

import (
	"encoding/binary"
	"testing"

	"github.com/sat20-labs/rgb11/operations"
)

func TestRGB11InflationBundleAssignsEachStateToOneTransition(t *testing.T) {
	var contract, predecessor, recipientSeal, changeSeal [32]byte
	contract[0], predecessor[0], recipientSeal[0], changeSeal[0] = 1, 2, 3, 4
	control := operations.TransitionInput{OperationID: predecessor, AssignmentType: 4010, Index: 0}
	asset := operations.TransitionInput{OperationID: predecessor, AssignmentType: 4000, Index: 0}
	issue, issueID, err := operations.BuildTransition(operations.TransitionSpec{
		ContractID: contract, Nonce: 1, TransitionType: 8000,
		Metadata: map[uint16][]byte{1000: make([]byte, 8)},
		Globals:  map[uint16][][]byte{2010: [][]byte{{1, 0, 0, 0, 0, 0, 0, 0}}},
		Inputs:   []operations.TransitionInput{control},
		// Full allowance consumption emits no zero-valued 4010 assignment.
		Outputs: []operations.TransitionOutput{{AssignmentType: 4000, Class: "fungible", Amount: 1, SecretSeal: recipientSeal}},
	})
	if err != nil {
		t.Fatal(err)
	}
	preserve, preserveID, err := operations.BuildTransition(operations.TransitionSpec{
		ContractID: contract, Nonce: 2, TransitionType: 10000,
		Inputs:  []operations.TransitionInput{asset},
		Outputs: []operations.TransitionOutput{{AssignmentType: 4000, Class: "fungible", Amount: 9, SecretSeal: changeSeal}},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, commitment, err := buildRGB11TwoTransitionBundle(
		[]operations.TransitionInput{control}, issue, issueID,
		[]operations.TransitionInput{asset}, preserve, preserveID,
	)
	if err != nil || len(commitment.Transitions) != 2 {
		t.Fatalf("bundle transitions=%d err=%v", len(commitment.Transitions), err)
	}
	inputMap, ok := bundle.Field("inputMap")
	if !ok || len(inputMap.Unwrap().Entries) != 2 {
		t.Fatalf("bundle input map missing or incomplete")
	}
	consumers := make(map[uint64][32]byte)
	for _, entry := range inputMap.Unwrap().Entries {
		consumer, bytesOK := entry.Value.Bytes()
		if len(entry.Key.Encoded) != 36 || !bytesOK || len(consumer) != 32 {
			t.Fatal("malformed bundle input map")
		}
		assignment := uint64(binary.LittleEndian.Uint16(entry.Key.Encoded[32:34]))
		var operation [32]byte
		copy(operation[:], consumer)
		consumers[assignment] = operation
	}
	if consumers[4010] != issueID.OperationID || consumers[4000] != preserveID.OperationID {
		t.Fatal("control and asset inputs were assigned to the wrong transition")
	}
	if _, _, err := buildRGB11TwoTransitionBundle(
		[]operations.TransitionInput{control}, issue, issueID,
		[]operations.TransitionInput{control}, preserve, preserveID,
	); err == nil {
		t.Fatal("duplicate input was accepted")
	}
}
