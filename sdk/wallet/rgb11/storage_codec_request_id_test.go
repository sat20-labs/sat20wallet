package rgb11wallet

import (
	"strings"
	"testing"
)

func TestTransferStateCodecPreservesReceiveRequestID(t *testing.T) {
	state := &TransferState{
		TransferID:       "receive-transfer",
		Direction:        "receive",
		Status:           "awaiting_broadcast",
		RecipientID:      "recipient",
		ReceiveRequestID: strings.Repeat("ab", 32),
		Invoice:          "rgb:~/~/~/witness",
	}
	encoded, err := encode(state)
	if err != nil {
		t.Fatal(err)
	}
	var decoded TransferState
	if err := decode(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ReceiveRequestID != state.ReceiveRequestID {
		t.Fatalf("receive request id lost: got=%q want=%q", decoded.ReceiveRequestID, state.ReceiveRequestID)
	}
}
