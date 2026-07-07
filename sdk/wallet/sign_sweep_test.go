package wallet

import (
	"testing"

	"github.com/btcsuite/btcd/wire"
)

func TestDelayedPathWitness(t *testing.T) {
	witnessScript := []byte{0x51, 0x52}

	clientWitness := delayedPathWitness(witnessScript, nil)
	if len(clientWitness) != 3 {
		t.Fatalf("client delayed witness length = %d, want 3", len(clientWitness))
	}
	if clientWitness[0] != nil {
		t.Fatalf("client signature slot should start empty")
	}
	if clientWitness[1] != nil {
		t.Fatalf("client delayed path selector should be OP_0")
	}
	if string(clientWitness[2]) != string(witnessScript) {
		t.Fatalf("client witness script mismatch")
	}

	serverWitness := delayedPathWitness(witnessScript, []byte{0x02})
	if len(serverWitness) != 5 {
		t.Fatalf("server delayed witness length = %d, want 5", len(serverWitness))
	}
	if serverWitness[0] != nil {
		t.Fatalf("server multisig dummy slot should start empty")
	}
	if serverWitness[1] != nil || serverWitness[2] != nil {
		t.Fatalf("server signature slots should start empty")
	}
	if serverWitness[3] != nil {
		t.Fatalf("server delayed path selector should be OP_0")
	}
	if string(serverWitness[4]) != string(witnessScript) {
		t.Fatalf("server witness script mismatch")
	}

	txWitness := wire.TxWitness(serverWitness)
	if txWitness.SerializeSize() == 0 {
		t.Fatalf("server witness should serialize")
	}
}
