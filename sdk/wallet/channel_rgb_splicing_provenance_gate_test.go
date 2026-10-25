package wallet

import (
	"bytes"
	"strings"
	"testing"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// Exercise the exact true-RGB branch in the public reservation-preparation
// helper. The source/package comes from the real core issuance/transition
// fixture. Each deliberate corruption must fail locally before querying a
// provider, signing, sending a request or changing the channel inventory.
func TestSTPRGBSplicingProvenanceRejectsWrongParentAndContext(t *testing.T) {
	for _, mode := range []string{"parent", "context"} {
		t.Run(mode, func(t *testing.T) {
			facts, sources, main, proof := rgbReservationNativeEvidenceFixture(t)
			pkg, err := rgb11wallet.DecodeChannelPackage(proof)
			if err != nil {
				t.Fatal(err)
			}
			channel, _ := rgbReservationBindingFixture(t)
			channel.ChannelId, channel.AssetEntropy = facts.channelID, facts.entropy
			// These empty current/old commitment sidecars cannot be the corruption
			// source: this test isolates inventory provenance for the RGB splice itself.
			channel.LocalCommitment.AssetProofs = nil
			channel.RemoteCommitment.AssetProofs = nil
			splice := main.Copy()
			if mode == "parent" {
				splice.LockTime++
			} else {
				pkg.Steps[len(pkg.Steps)-1].Context.ChannelID = "different-reservation-channel"
			}
			raw, err := rgb11wallet.EncodeChannelPackage(pkg)
			if err != nil {
				t.Fatal(err)
			}
			inventory, err := rgb11wallet.EncodeChannelInventoryWithLast(sources, raw)
			if err != nil {
				t.Fatal(err)
			}
			channel.AssetFunding = map[string][]byte{"rgb11": inventory}
			resv := &SplicingReservation{RevocationInfo: RevocationInfo{Channel: channel}}
			resv.Id, resv.Status = channel.ResvId, RS_INIT
			resv.AssetName = &AssetName{AssetName: sources[0].AssetName}
			resv.SplicingTx = splice
			before := append([]byte(nil), inventory...)
			// No DB/network client exists. A changed scope condition that skips this
			// RGB branch cannot satisfy the exact local error below by querying later.
			_, err = (&Manager{}).PrepareSTPRGBSplicingReservationEvidence(resv, []byte("true-rgb-splice-"+mode))
			want := "stored RGB obligation changed its canonical parent"
			if mode == "context" {
				want = "stored RGB obligation changed its captured channel context"
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("true RGB %s corruption missed exact local capture gate: %v", mode, err)
			}
			if !bytes.Equal(before, channel.AssetFunding["rgb11"]) || channel.ResvId != resv.Id || resv.Status != RS_INIT {
				t.Fatal("corrupt RGB splice changed inventory or reservation ownership")
			}
		})
	}
}
