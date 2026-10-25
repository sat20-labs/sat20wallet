package wallet

import (
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"testing"
)

func TestSTPRGBSpliceFactsDoNotRequireUnusedTipEndpoint(t *testing.T) {
	observed, sources, _, _ := rgbReservationNativeEvidenceFixture(t)
	// Complete actual source raw/status/outspend/UTXO facts are present. Tip
	// deliberately is not: STP/core consumers in this slice never request it.
	provider := observed.newView()
	manager := &Manager{}
	manager.rgbManager = &rgb11Manager{Manager: manager, accountOwner: manager, evidence: provider}
	inventory, err := rgb11wallet.EncodeChannelInventory(sources)
	if err != nil {
		t.Fatal(err)
	}
	facts := newSTPRGBReservationEvidence()
	if err := manager.querySTPRGBEvidence(facts, [][]byte{inventory}, nil); err != nil {
		t.Fatalf("unused tip endpoint became an extra STP availability requirement: %v", err)
	}
	if err := facts.useView().complete(); err != nil {
		t.Fatal(err)
	}
	if observed.missing != nil {
		t.Fatal("collector queried an endpoint outside its STP dependency set")
	}
}
