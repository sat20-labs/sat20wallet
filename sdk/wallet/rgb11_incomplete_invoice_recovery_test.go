package wallet

import (
	"errors"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	indexer "github.com/sat20-labs/indexer/common"
)

func TestRGB11IncompleteWitnessReservationIsNotRecoverable(t *testing.T) {
	w := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch",
		"",
		&chaincfg.TestNet4Params,
	)
	mgr := newRGB11FlowManager(t, w, &rgb11FlowIndexer{}, &rgb11FlowEvidence{}, 817)

	req, err := mgr.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode:          "witness",
		TransportMode: "out-of-band",
		AmountRaw:     "2",
		Expiry:        time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(req.WitnessScript) == 0 {
		t.Fatal("fixture did not create an independent witness receive key")
	}

	scope := mgr.rgbManager.rgb11ScopeKey()
	prefix := []byte("rgb11-" + scope + "-receive-key-")
	var receiveKeys [][]byte
	if err := mgr.db.BatchRead(prefix, false, func(key, _ []byte) error {
		receiveKeys = append(receiveKeys, append([]byte(nil), key...))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(receiveKeys) != 1 {
		t.Fatalf("fixture receive keys=%d, want 1", len(receiveKeys))
	}
	if err := mgr.db.Delete(receiveKeys[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.rgbManager.projectionStore.LoadReceiveKey(req.WitnessScript); !errors.Is(err, indexer.ErrKeyNotFound) {
		t.Fatalf("receive key still present after simulated crash boundary: %v", err)
	}

	restarted, err := newRGB11Manager(mgr, mgr.db, mgr.utxoLockerL1, mgr.rgbManager.evidence)
	if err != nil {
		t.Fatal(err)
	}
	mgr.rgbManager = restarted
	if err := restarted.selectRGB11Scope(); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.engine.LoadReceive(req.RequestID); err != nil {
		t.Fatalf("durable receive request missing after restart: %v", err)
	}

	views, err := restarted.rgb11ReservationViews()
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range views {
		if view.RequestID == req.RequestID {
			t.Fatalf("incomplete witness invoice was exposed as recoverable: %+v", view)
		}
	}
}
