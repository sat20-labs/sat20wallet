package rgb11wallet

import (
	"bytes"
	"testing"
)

func TestAllocationProofIdentitySurvivesStorageRoundTrip(t *testing.T) {
	proof := AllocationProof{
		OutPoint: "producer:0", ConsignmentHash: "consignment", ValidationHash: "receipt",
		OperationID: "genesis", StateClass: "fungible", WitnessTxID: "producer",
		CarrierBinding: &CarrierBinding{}, Status: "settled", Confirmations: 6,
	}
	raw, err := encode(&proof)
	if err != nil {
		t.Fatal(err)
	}
	var restored AllocationProof
	if err := decode(raw, &restored); err != nil {
		t.Fatal(err)
	}
	reencoded, err := encode(&restored)
	if err != nil || !bytes.Equal(raw, reencoded) {
		t.Fatalf("storage round trip changed bytes: %v", err)
	}
	if !sameAllocationProofIdentity(proof, restored) {
		t.Fatal("identical persisted proof lost its immutable identity")
	}
}

func TestAllocationProofIdentityRejectsChangedMaterialHash(t *testing.T) {
	proof := AllocationProof{ConsignmentHash: "original", ValidationHash: "receipt"}
	changed := proof
	changed.ConsignmentHash = "different"
	if sameAllocationProofIdentity(proof, changed) {
		t.Fatal("changed consignment hash was accepted")
	}
	changed = proof
	changed.ValidationHash = "different"
	if sameAllocationProofIdentity(proof, changed) {
		t.Fatal("changed validation hash was accepted")
	}
}
