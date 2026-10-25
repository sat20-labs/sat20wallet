package wallet

import (
	"reflect"
	"testing"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// Cache presence is not chain confirmation. Preserve a successful observation
// byte-for-byte and leave its accept/reject policy to the existing validator.
func TestSTPRGBReservationPreservesObservedUnconfirmedStatus(t *testing.T) {
	for _, observed := range []*rgb11wallet.BitcoinTxStatus{
		{TxID: "observed-mined", Confirmed: true, BlockHeight: 10, BlockHash: "canonical", Confirmations: 2},
		{TxID: "observed-mempool", InMempool: true},
		{TxID: "observed-unknown"},
	} {
		t.Run(observed.TxID, func(t *testing.T) {
			facts := newSTPRGBReservationEvidence()
			facts.statuses[observed.TxID] = stpRGBStatusFact{value: observed}
			view := facts.newView()
			got, err := view.GetTxStatus(observed.TxID)
			if err != nil || !reflect.DeepEqual(got, observed) || view.complete() != nil {
				t.Fatalf("successful observed status was changed by reservation evidence: got=%+v err=%v complete=%v want=%+v", got, err, view.complete(), observed)
			}
			got.TxID = "consumer-mutated"
			again, err := view.GetTxStatus(observed.TxID)
			if err != nil || !reflect.DeepEqual(again, observed) {
				t.Fatal("status consumption mutated the captured observation")
			}
		})
	}
}
