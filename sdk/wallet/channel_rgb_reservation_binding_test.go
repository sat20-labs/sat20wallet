package wallet

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
)

func rgbReservationBindingFixture(t *testing.T) (*Channel, *STPRGBReservationEvidence) {
	t.Helper()
	owner := NewInternalWalletWithMnemonic(channelHeartbeatTestMnemonic, "", GetChainParam())
	channel := &Channel{ChannelInDB: *NewChannelInDB(), localWallet: owner}
	channel.LocalCommitment, channel.RemoteCommitment = NewChannelCommitment(), NewChannelCommitment()
	channel.ChannelId, channel.Status, channel.CommitHeight, channel.ResvId = "rgb-facts-binding", CS_READY, 7, 19
	asset := &indexer.AssetName{Protocol: "rgb11", Type: indexer.ASSET_TYPE_FT, Ticker: strings.Repeat("1", 64)}
	facts, _, _, err := captureSTPRGBEvidence(channel, asset, []string{"owned-input"}, nil, []byte("one-request"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return channel, facts
}

func TestSTPRGBReservationRejectsChangedRequestAndCaptureInputs(t *testing.T) {
	for _, scenario := range []string{"request", "channel-height", "channel-instance", "inventory", "reservation-input", "reservation-phase"} {
		t.Run(scenario, func(t *testing.T) {
			channel, facts := rgbReservationBindingFixture(t)
			if err := facts.ValidateForChannel(channel, []byte("one-request")); err != nil {
				t.Fatal(err)
			}
			request := []byte("one-request")
			switch scenario {
			case "request":
				request = []byte("another-request")
			case "channel-height":
				channel.CommitHeight++
			case "channel-instance":
				replacement := *channel
				channel = &replacement
			case "inventory":
				channel.AssetFunding["rgb11"] = []byte("changed-query-dependencies")
			default:
				resv := &SplicingReservation{RevocationInfo: RevocationInfo{Channel: channel},
					SplicingInputs: []*TxOutput{{OutPointStr: "owned-input"}}}
				resv.Id, resv.Status = 19, RS_INIT
				facts.reservation, facts.reservationID, facts.reservationStatus = resv, resv.Id, resv.Status
				if scenario == "reservation-input" {
					resv.SplicingInputs[0].OutPointStr = "different-input"
				} else {
					resv.Status = RS_CLOSED
				}
			}
			if err := facts.ValidateForChannel(channel, request); err == nil {
				t.Fatal("changed capture was allowed into signing phase")
			}
		})
	}
}

func TestSTPRGBReservationStickyMissingAndDeepClone(t *testing.T) {
	facts, _, main, proof := rgbReservationNativeEvidenceFixture(t)
	view := facts.useView()
	if view != facts.useView() {
		t.Fatal("one synchronous operation did not retain its exact view")
	}
	if err := view.addLocallyConstructed(main, proof); err != nil {
		t.Fatal(err)
	}
	clone := facts.Clone()
	clone.prepared[main.TxID()][0]++
	if bytes.Equal(clone.prepared[main.TxID()], facts.prepared[main.TxID()]) {
		t.Fatal("detached reservation aliases Prepared bytes")
	}
	if _, err := view.GetRawTx(strings.Repeat("f", 64)); !errors.Is(err, errSTPRGBEvidenceMissing) {
		t.Fatal(err)
	}
	if !errors.Is(facts.newView().complete(), errSTPRGBEvidenceMissing) {
		t.Fatal("a new helper view cleared sticky missing")
	}
	if !errors.Is(facts.Clone().useView().complete(), errSTPRGBEvidenceMissing) {
		t.Fatal("detached clone cleared sticky missing")
	}
}

func TestSTPRGBReservationPureLegacyDoesNotQueryOrAttachFacts(t *testing.T) {
	channel := &Channel{ChannelInDB: *NewChannelInDB()}
	// No RGB manager/provider or wallet is configured. A legacy operation
	// must still bypass native observation, with the runtime slot left nil.
	facts, err := (&Manager{}).PrepareSTPRGBSplicingEvidence(channel, &ASSET_PLAIN_SAT, nil, nil, []byte("legacy"), nil)
	if err != nil || facts != nil {
		t.Fatalf("legacy acquired RGB facts or provider requirement: %v", err)
	}
	if err := facts.ValidateForChannel(channel, []byte("legacy")); err != nil {
		t.Fatal(err)
	}
}
