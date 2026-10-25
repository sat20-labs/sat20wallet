package wallet

import (
	"bytes"
	"errors"
	"testing"

	"github.com/btcsuite/btcd/wire"
)

// This fixture isolates exact Bitcoin authorization and journal atomicity.
// Its RGB sidecar is opaque; native RGB consensus remains a separate mandatory
// integration test. No native verifier is relaxed or replaced in production.
func rgbRecoveryExchangeFixture(t *testing.T) (*otherJournalFixture, []string) {
	t.Helper()
	f := newOtherJournalFixture(t, false)
	full := append([]string(nil), f.packages[0]...)
	f.channel.RemoteCommitment.RGBClientRecovery = cloneRGBClientRecovery(f.channel.RemoteCommitment.Others[0])
	f.channel.RemoteCommitment.RGBClientRecovery.AssetProofs = map[string][]byte{"rgb11": []byte("opaque independently retained proof")}
	f.channel.RemoteCommitment.Others = nil
	f.channel.RemoteCommitment.AssetProofs["rgb11"] = []byte("opaque parent proof context")
	return f, full
}

func TestSTPRGBRecoveryRejectsChangedOrPartialPeerPackage(t *testing.T) {
	for _, scenario := range []string{"missing", "extra", "changed-output", "missing-signature", "wrong-parent"} {
		t.Run(scenario, func(t *testing.T) {
			f, encoded := rgbRecoveryExchangeFixture(t)
			before, err := EncodeToBytes(f.channel.RemoteCommitment.RGBClientRecovery)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing":
				encoded = nil
			case "extra":
				encoded = append(encoded, encoded[0])
			default:
				tx, err := DecodeMsgTx(encoded[0])
				if err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "changed-output":
					tx.TxOut[0].Value--
				case "missing-signature":
					tx.TxIn[0].Witness[1] = nil
				case "wrong-parent":
					tx.TxIn[0].PreviousOutPoint.Hash[0]++
				}
				encoded[0], err = EncodeMsgTx(tx)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := validateRGBClientRecovery(f.channel, encoded); err == nil {
				t.Fatal("invalid RGB recovery authorization accepted")
			}
			after, err := EncodeToBytes(f.channel.RemoteCommitment.RGBClientRecovery)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("validation changed the frozen RGB candidate")
			}
		})
	}
}

func TestSTPRGBRecoveryAcceptanceIsAtomicAndDormant(t *testing.T) {
	f, encoded := rgbRecoveryExchangeFixture(t)
	before, _ := f.read(t, "remote")
	partial, err := EncodeToBytes(f.channel.RemoteCommitment.RGBClientRecovery)
	if err != nil {
		t.Fatal(err)
	}
	f.db.failFlush = true
	if err := f.manager.acceptSTPRecoveryTransactionsWithJournal(f.channel, nil, encoded, nil); !errors.Is(err, errNativeSettlementWrite) {
		t.Fatalf("failed atomic publication was not reported: %v", err)
	}
	after, _ := f.read(t, "remote")
	live, err := EncodeToBytes(f.channel.RemoteCommitment.RGBClientRecovery)
	if err != nil || !bytes.Equal(partial, live) || !bytes.Equal(before, after) {
		t.Fatal("failed batch published RGB signatures or changed its old journal")
	}
	f.db.failFlush = false
	if err := f.manager.acceptSTPRecoveryTransactionsWithJournal(f.channel, nil, encoded, nil); err != nil {
		t.Fatal(err)
	}
	_, record := f.read(t, "remote")
	if record.Commitment.RGBClientRecovery == nil || len(record.Commitment.Others) != 0 ||
		!equalRGBClientRecovery(record.Commitment.RGBClientRecovery, f.channel.RemoteCommitment.RGBClientRecovery) {
		t.Fatal("complete RGB recovery was not stored independently of Others")
	}
	_, err = encodeRGBClientRecovery(f.channel, f.channel.RemoteCommitment)
	if err != nil {
		t.Fatalf("accepted package lost complete Bitcoin authorization: %v", err)
	}
	proof := append([]byte(nil), record.Commitment.RGBClientRecovery.AssetProofs["rgb11"]...)
	f.channel.RemoteCommitment.RGBClientRecovery.AssetProofs["rgb11"][0]++
	_, immutable := f.read(t, "remote")
	if !bytes.Equal(proof, immutable.Commitment.RGBClientRecovery.AssetProofs["rgb11"]) {
		t.Fatal("live proof mutation aliased the durable RGB recovery")
	}
	f.reopen(t)
	_, restored := f.read(t, "remote")
	if !equalRGBClientRecovery(record.Commitment.RGBClientRecovery, restored.Commitment.RGBClientRecovery) {
		t.Fatal("database reopen lost exact RGB child witness/proof bytes")
	}
}

func TestSTPRGBRecoveryNilDoesNotChangeNonRGBExchange(t *testing.T) {
	f := newOtherJournalFixture(t, false)
	if f.channel.LocalCommitment.RGBClientRecovery != nil || f.channel.RemoteCommitment.RGBClientRecovery != nil {
		t.Fatal("non-RGB fixture acquired RGB state")
	}
	if encoded, err := encodeRGBClientRecovery(f.channel, f.channel.LocalCommitment); err != nil || len(encoded) != 0 {
		t.Fatalf("non-RGB encoding changed: %v", err)
	}
	if _, err := validateRGBClientRecovery(f.channel, []string{"unexpected"}); err == nil {
		t.Fatal("non-RGB channel accepted an unsolicited RGB recovery")
	}
}

func TestSTPRGBRecoveryCloneKeepsIndependentBytes(t *testing.T) {
	f, _ := rgbRecoveryExchangeFixture(t)
	f.channel.LocalCommitment.RGBClientRecovery = cloneRGBClientRecovery(f.channel.RemoteCommitment.RGBClientRecovery)
	cloned := f.channel.Clone()
	if !equalRGBClientRecovery(f.channel.LocalCommitment.RGBClientRecovery, cloned.LocalCommitment.RGBClientRecovery) ||
		!equalRGBClientRecovery(f.channel.RemoteCommitment.RGBClientRecovery, cloned.RemoteCommitment.RGBClientRecovery) {
		t.Fatal("channel clone dropped its RGB recovery obligation")
	}
	cloned.LocalCommitment.RGBClientRecovery.CommitTx.TxOut[0].Value--
	cloned.RemoteCommitment.RGBClientRecovery.AssetProofs["rgb11"][0]++
	if equalRGBClientRecovery(f.channel.LocalCommitment.RGBClientRecovery, cloned.LocalCommitment.RGBClientRecovery) ||
		equalRGBClientRecovery(f.channel.RemoteCommitment.RGBClientRecovery, cloned.RemoteCommitment.RGBClientRecovery) {
		t.Fatal("channel clone shared RGB transaction or proof bytes")
	}
}

// A dormant RGB slot must not change the ordinary dependency package or wait
// for its child to confirm. The signed child is a disjoint main output spend.
func TestSTPRGBRecoverySlotNeverJoinsDefaultForceClose(t *testing.T) {
	f := newReservedOthersFixture(t)
	parent := f.channel.LocalCommitment.CommitTx
	child := f.spend(t, wire.OutPoint{Hash: parent.TxHash(), Index: 1}, parent.TxOut[1], 40_000)
	f.channel.LocalCommitment.RGBClientRecovery = &CommitmentTx{CommitTx: child,
		AssetProofs: map[string][]byte{"rgb11": []byte("opaque dormant package proof")}}
	original, err := EncodeToBytes(f.channel.LocalCommitment.RGBClientRecovery)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.manager.ForcelyCloseChannel(f.channel, 1); err != nil {
		t.Fatalf("dormant child blocked the original default close: %v", err)
	}
	reservedOthersAssertPackage(t, f.rpc.broadcasts, f.expected, "RGB dormant default network package")
	var pending *ClosingReservation
	for _, candidate := range f.manager.GetClosingReservations() {
		pending = candidate
	}
	if pending == nil {
		t.Fatal("default package did not remain durable")
	}
	reservedOthersAssertPackage(t, pending.PreTxs, f.expected, "RGB dormant durable default package")
	after, err := EncodeToBytes(f.channel.LocalCommitment.RGBClientRecovery)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("default close changed dormant signed RGB recovery bytes")
	}
}
