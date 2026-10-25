package wallet

import (
	"bytes"
	"strings"
	"testing"
)

// Negative-only tests of the new binding helper use real old-parent 2-of-2
// Bitcoin authorization. The opaque proof must never pass RGB consensus.
func TestSTPRGBPunishRecoveryChecksEachOldBinding(t *testing.T) {
	for _, scenario := range []struct {
		name string
		want string
	}{
		{"wrong-server-role", "old server commitment"},
		{"missing-parent-proof", "old server commitment"},
		{"missing-proof", "invalid RGB client recovery package"},
		{"foreign-parent", "detached from its parent"},
		{"missing-signature", "not fully signed"},
		{"foreign-proof", "revoked RGB client recovery proof"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f, encoded := rgbRecoveryExchangeFixture(t)
			f.channel.IsInitiator = true
			old := f.channel.RemoteCommitment
			child, err := DecodeMsgTx(encoded[0])
			if err != nil {
				t.Fatal(err)
			}
			old.RGBClientRecovery.CommitTx = child
			switch scenario.name {
			case "wrong-server-role":
				f.channel.IsInitiator = false
			case "missing-parent-proof":
				delete(old.AssetProofs, "rgb11")
			case "missing-proof":
				old.RGBClientRecovery.AssetProofs = nil
			case "foreign-parent":
				child.TxIn[0].PreviousOutPoint.Hash[0]++
			case "missing-signature":
				child.TxIn[0].Witness[1] = nil
			case "foreign-proof":
				old.RGBClientRecovery.AssetProofs["rgb11"] = []byte("foreign native package")
			}
			before, err := EncodeToBytes(old.RGBClientRecovery)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.manager.verifiedPunishRGBClientRecovery(f.channel, nil); err == nil || !strings.Contains(err.Error(), scenario.want) {
				t.Fatalf("wrong binding gate for %s: %v", scenario.name, err)
			}
			after, err := EncodeToBytes(old.RGBClientRecovery)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("validation generated or changed recovery signature/proof bytes")
			}
		})
	}
}

func TestSTPRGBPunishRecoveryNilPreservesLegacy(t *testing.T) {
	f := newOtherJournalFixture(t, false)
	if child, err := f.manager.verifiedPunishRGBClientRecovery(f.channel, nil); err != nil || child != nil {
		t.Fatalf("legacy commitment acquired an RGB recovery obligation: %v", err)
	}
}
