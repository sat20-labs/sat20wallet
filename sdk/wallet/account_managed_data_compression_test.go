package wallet

import (
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
)

func TestShouldReuseRemoteManagedDataEnvelopeMigratesBeneficialLegacyEncoding(t *testing.T) {
	state := account.ManagedState{DataRevision: 4, DataHash: "content-hash"}
	legacy := &accountManagedDataSnapshot{Hash: "content-hash", Envelope: []byte("legacy")}
	if shouldReuseRemoteManagedDataEnvelope(state, "content-hash", legacy, true) {
		t.Fatal("beneficial legacy envelope was incorrectly reused")
	}
	if !shouldReuseRemoteManagedDataEnvelope(state, "content-hash", legacy, false) {
		t.Fatal("non-beneficial legacy envelope was unnecessarily rewritten")
	}
	compressed := &accountManagedDataSnapshot{
		Hash: "content-hash", Envelope: []byte("compressed"), Compressed: true,
	}
	if !shouldReuseRemoteManagedDataEnvelope(state, "content-hash", compressed, true) {
		t.Fatal("already compressed envelope was unnecessarily rewritten")
	}
	if shouldReuseRemoteManagedDataEnvelope(state, "different", compressed, true) {
		t.Fatal("different logical content was reused")
	}
}
