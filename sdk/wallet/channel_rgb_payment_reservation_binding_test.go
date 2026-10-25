package wallet

import (
	"errors"
	"testing"

	"github.com/btcsuite/btcd/wire"
)

func rgbPaymentReservationBindingFixture(t *testing.T) (*PaymentReservation, *STPRGBReservationEvidence) {
	t.Helper()
	channel, facts := rgbReservationBindingFixture(t)
	old := &Channel{ChannelInDB: *NewChannelInDB()}
	old.RemoteCommitment = NewChannelCommitment()
	old.RemoteCommitment.AssetProofs = make(map[string][]byte)
	old.RemoteCommitment.CommitTx = wire.NewMsgTx(2)
	old.RemoteCommitment.AssetProofs["rgb11"] = []byte("exact-old-payment-obligation")
	payment := &PaymentReservation{RevocationInfo: RevocationInfo{Channel: channel, OldChannel: old}}
	payment.Id, payment.Status = channel.ResvId, RS_INIT
	payment.ChannelId, payment.IsUnlock = channel.ChannelId, true
	manager := &Manager{channelMap: map[string]*Channel{channel.ChannelId: channel},
		paymentChannelMap: map[int64]*PaymentReservation{payment.Id: payment}}
	facts.manager = manager
	facts.paymentReservation, facts.paymentOldChannel = payment, old
	facts.reservationID, facts.reservationStatus = payment.Id, payment.Status
	facts.oldRemoteTxID = old.RemoteCommitment.CommitTx.TxID()
	facts.oldRemoteProof = append([]byte(nil), old.RemoteCommitment.AssetProofs["rgb11"]...)
	return payment, facts
}

func TestSTPRGBPaymentEvidenceBindsRequestPhaseAndOldObligation(t *testing.T) {
	for _, scenario := range []string{"unchanged", "request", "id", "phase", "payment-pointer", "old-channel", "old-transaction", "old-proof"} {
		t.Run(scenario, func(t *testing.T) {
			payment, facts := rgbPaymentReservationBindingFixture(t)
			request := []byte("one-request")
			if err := facts.ValidateForChannel(payment.Channel, request); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "request":
				request = []byte("different-request")
			case "id":
				payment.Id++
			case "phase":
				payment.Status = RS_PAYMENT_STARTED
			case "payment-pointer":
				replacement := *payment
				facts.manager.mutex.Lock()
				facts.manager.paymentChannelMap[payment.Id] = &replacement
				facts.manager.mutex.Unlock()
			case "old-channel":
				replacement := *payment.OldChannel
				payment.OldChannel = &replacement
			case "old-transaction":
				payment.OldChannel.RemoteCommitment.CommitTx.Version++
			case "old-proof":
				payment.OldChannel.RemoteCommitment.AssetProofs["rgb11"][0]++
			}
			payment.Channel.Mutex.Lock()
			err := facts.ValidateForChannel(payment.Channel, request)
			payment.Channel.Mutex.Unlock()
			if scenario == "unchanged" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("changed payment capture was allowed into the business phase")
			}
		})
	}
}

func TestSTPRGBPaymentEvidencePureLegacyNeedsNoProvider(t *testing.T) {
	channel := &Channel{ChannelInDB: *NewChannelInDB()}
	payment := &PaymentReservation{RevocationInfo: RevocationInfo{Channel: channel}}
	facts, err := (&Manager{}).PrepareSTPRGBPaymentReservationEvidence(payment, []byte("legacy-unlock"))
	if err != nil || facts != nil {
		t.Fatalf("legacy unlock acquired RGB facts or provider requirement: %v", err)
	}
	channel.Mutex.Lock()
	defer channel.Mutex.Unlock()
	if err := facts.ValidateForChannel(channel, []byte("legacy-unlock")); err != nil {
		t.Fatal(err)
	}
}

func TestSTPRGBPaymentEvidenceClonePreservesStickyMissing(t *testing.T) {
	payment, facts := rgbPaymentReservationBindingFixture(t)
	facts.useView().unavailable("transaction", "uncaptured-payment-dependency")
	clone := facts.Clone()
	if !errors.Is(clone.useView().complete(), errSTPRGBEvidenceMissing) {
		t.Fatal("detached payment evidence cleared sticky missing")
	}
	if err := facts.ValidateForChannel(payment.Channel, []byte("one-request")); !errors.Is(err, errSTPRGBEvidenceMissing) {
		t.Fatal("payment binding cleared sticky missing", err)
	}
}
