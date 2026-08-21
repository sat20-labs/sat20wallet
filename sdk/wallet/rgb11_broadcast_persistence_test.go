package wallet

import (
	"errors"
	"testing"
)

func TestRGB11BroadcastPersistenceErrorPreservesTxID(t *testing.T) {
	cause := errors.New("persist pending state failed")
	err := &RGB11BroadcastPersistenceError{
		TxID: "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
		Err:  cause,
	}
	if err.TxID == "" {
		t.Fatal("broadcast persistence error lost transaction id")
	}
	if !errors.Is(err, ErrRGB11BroadcastPersistence) {
		t.Fatalf("error does not expose broadcast-success persistence class: %v", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("error does not preserve persistence cause: %v", err)
	}
}
