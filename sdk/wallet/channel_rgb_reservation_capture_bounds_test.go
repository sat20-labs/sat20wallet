package wallet

import (
	"context"
	"testing"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// A fully valid ordinary two-step graph does not mean its whole history was
// independently constructed by the capture site. Only the matched final tx
// gains this specific local provenance; ancestors need their own provenance.
func TestSTPRGBStoredObligationDoesNotGrantPreparedToAllHistory(t *testing.T) {
	facts, sources, main, proof := rgbReservationNativeEvidenceFixture(t)
	pkg, err := rgb11wallet.DecodeChannelPackage(proof)
	if err != nil {
		t.Fatal(err)
	}
	view := facts.useView()
	if err := view.addLocallyConstructed(main, proof); err != nil {
		t.Fatal(err)
	}
	source, err := rgb11wallet.ChannelFundingFromStep(pkg.Steps[0], 1, sources[0].Amount)
	if err != nil {
		t.Fatal(err)
	}
	child := wire.NewMsgTx(2)
	point := wire.OutPoint{Hash: main.TxHash(), Index: 1}
	child.AddTxIn(wire.NewTxIn(&point, nil, nil))
	if _, err := rgb11wallet.ReserveChannelDBC(child, 0); err != nil {
		t.Fatal(err)
	}
	child.AddTxOut(wire.NewTxOut(500, sources[0].PkScript))
	step, err := rgb11wallet.BuildChannelStep(context.Background(), []rgb11wallet.ChannelFunding{source}, child, 0,
		[]rgb11wallet.ChannelAssignment{{OutputIndex: 1, Amount: sources[0].Amount}}, pkg.Steps[0].Context, view)
	if err != nil {
		t.Fatal(err)
	}
	pkg.Steps = append(pkg.Steps, *step)
	raw, err := rgb11wallet.EncodeChannelPackage(pkg)
	if err != nil {
		t.Fatal(err)
	}
	captured := newSTPRGBReservationEvidence()
	captured.channelID, captured.network, captured.height, captured.entropy = facts.channelID, facts.network, facts.height, facts.entropy
	captured.prepared = make(map[string][]byte)
	if err := captured.captureConstructedProof(child, raw, false); err != nil {
		t.Fatal(err)
	}
	if len(captured.prepared[child.TxID()]) == 0 {
		t.Fatal("exact captured final transaction lost local provenance")
	}
	if _, exists := captured.prepared[main.TxID()]; exists {
		t.Fatal("capturing one final obligation granted Prepared provenance to its entire history")
	}
}

func TestSTPRGBEvidenceRejectsReplacedRegisteredChannel(t *testing.T) {
	channel, _ := rgbReservationBindingFixture(t)
	manager := &Manager{channelMap: map[string]*Channel{channel.ChannelId: channel}}
	channel.manager = manager
	asset := &indexer.AssetName{Protocol: "rgb11", Type: indexer.ASSET_TYPE_FT, Ticker: "1111111111111111111111111111111111111111111111111111111111111111"}
	facts, _, _, err := captureSTPRGBEvidence(channel, asset, nil, nil, []byte("same-request"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	channel.Mutex.Lock()
	if err := facts.ValidateForChannel(channel, []byte("same-request")); err != nil {
		channel.Mutex.Unlock()
		t.Fatal(err)
	}
	channel.Mutex.Unlock()
	// An ordinary replacement leaves the captured old object unchanged.
	// Apply it before reacquiring the original operation lock, as peer sync
	// does during the query window; never mutate a map without its mutex.
	replacement := *channel
	manager.mutex.Lock()
	manager.channelMap[channel.ChannelId] = &replacement
	manager.mutex.Unlock()
	channel.Mutex.Lock()
	defer channel.Mutex.Unlock()
	if err := facts.ValidateForChannel(channel, []byte("same-request")); err == nil {
		t.Fatal("stale captured pointer remained usable after registered channel replacement")
	}
}
