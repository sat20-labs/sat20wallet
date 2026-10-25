package wallet

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/rgb11/seals"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// Actual issuance and transition validators are used here. This fixture tests
// native evidence boundaries, not Bitcoin signature/broadcast authorization.
func rgbReservationNativeEvidenceFixture(t *testing.T) (*STPRGBReservationEvidence, []rgb11wallet.ChannelFunding, *wire.MsgTx, []byte) {
	t.Helper()
	genesis := wire.NewMsgTx(2)
	genesis.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: 42}, nil, nil))
	script := append([]byte{0, 32}, bytes.Repeat([]byte{2}, 32)...)
	genesis.AddTxOut(wire.NewTxOut(1000, script))
	hash := genesis.TxHash()
	seal, err := seals.NewGraphBlindSeal(hash[:], 0, 21)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := issuance.Issue(issuance.Spec{Kind: schemas.NIA, Network: issuance.BitcoinRegtest,
		Ticker: "RESV", Name: "Reservation evidence", Precision: 2,
		Allocations: []issuance.Allocation{{Seal: seal, Amount: 7000}}, Timestamp: 1_700_000_001})
	if err != nil {
		t.Fatal(err)
	}
	asset, err := rgb11wallet.NewContractAssetKey(issued.ContractID, indexer.ASSET_TYPE_FT)
	if err != nil {
		t.Fatal(err)
	}
	point := wire.OutPoint{Hash: hash}
	sources := []rgb11wallet.ChannelFunding{{OutPoint: point.String(), Value: 1000, PkScript: script,
		AssetName: asset, Amount: *indexer.NewDecimalWithScale(7000, 2), Consignment: []byte(issued.Armor)}}
	facts := newSTPRGBReservationEvidence()
	facts.channelID, facts.network, facts.height, facts.entropy = "rgb-resv-evidence", "regtest", 8, [32]byte{1, 2, 3}
	var raw bytes.Buffer
	if err := genesis.Serialize(&raw); err != nil {
		t.Fatal(err)
	}
	facts.rawTxs[genesis.TxID()] = stpRGBRawFact{raw: append([]byte(nil), raw.Bytes()...)}
	facts.statuses[genesis.TxID()] = stpRGBStatusFact{value: &rgb11wallet.BitcoinTxStatus{TxID: genesis.TxID(), Confirmed: true,
		BlockHeight: 10, BlockHash: strings.Repeat("1", 64), Confirmations: 2}}
	facts.outspends[point.String()] = stpRGBOutspendFact{value: &rgb11wallet.BitcoinOutspend{}}
	facts.utxos[point.String()] = stpRGBUTXOFact{value: &rgb11wallet.BitcoinUTXO{OutPoint: point.String(), Value: 1000, PkScript: append([]byte(nil), script...), Confirmations: 2}}
	main := wire.NewMsgTx(2)
	main.AddTxIn(wire.NewTxIn(&point, nil, nil))
	if _, err := rgb11wallet.ReserveChannelDBC(main, 0); err != nil {
		t.Fatal(err)
	}
	main.AddTxOut(wire.NewTxOut(700, script))
	view := facts.newView()
	step, err := rgb11wallet.BuildChannelStep(context.Background(), sources, main, 0,
		[]rgb11wallet.ChannelAssignment{{OutputIndex: 1, Amount: sources[0].Amount}}, rgb11wallet.ChannelContext{
			ChannelID: facts.channelID, Operation: "commit", Branch: "server", Height: facts.height,
			Entropy: facts.entropy, BitcoinNetwork: facts.network}, view)
	if err != nil || view.complete() != nil {
		t.Fatalf("actual native source evidence could not construct its main transition: %v, complete=%v", err, view.complete())
	}
	proof, err := rgb11wallet.EncodeChannelPackage(&rgb11wallet.ChannelPackage{Version: 1, Funding: sources, Steps: []rgb11wallet.ChannelStep{*step}})
	if err != nil {
		t.Fatal(err)
	}
	return facts, sources, main, proof
}

func TestSTPRGBReservationEvidenceDetectsCorePreparedFallback(t *testing.T) {
	facts, _, main, proof := rgbReservationNativeEvidenceFixture(t)
	pkg, err := rgb11wallet.DecodeChannelPackage(proof)
	if err != nil {
		t.Fatal(err)
	}
	view := facts.newView() // No explicit locally constructed main is installed.
	if _, err := rgb11wallet.ValidateChannelPackage(context.Background(), pkg, main, view); err != nil {
		t.Fatalf("control: real core no longer accepts this embedded Prepared branch: %v", err)
	}
	if !errors.Is(view.complete(), errSTPRGBEvidenceMissing) {
		t.Fatal("a core-accepted embedded witness hid the missing reservation network fact")
	}
	// The guarded private view must not call a provider or silently invent a
	// new local witness: an unknown ID remains an explicit missing fact.
	if _, err := view.GetRawTx(strings.Repeat("f", 64)); !errors.Is(err, errSTPRGBEvidenceMissing) {
		t.Fatal("unknown network witness was not rejected locally")
	}
}

func TestSTPRGBReservationPreparedRequiresExactLocalProofContext(t *testing.T) {
	facts, _, main, proof := rgbReservationNativeEvidenceFixture(t)
	view := facts.newView()
	if err := view.addLocallyConstructed(main, proof); err != nil {
		t.Fatal(err)
	}
	pkg, err := rgb11wallet.DecodeChannelPackage(proof)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rgb11wallet.ValidateChannelPackage(context.Background(), pkg, main, view); err != nil || view.complete() != nil {
		t.Fatalf("explicit exact local Prepared transaction was rejected: %v, complete=%v", err, view.complete())
	}
	changed := main.Copy()
	changed.TxOut[1].Value--
	if err := facts.newView().addLocallyConstructed(changed, proof); err == nil {
		t.Fatal("a different transaction reused a local Prepared proof")
	}
	other := *facts
	other.channelID = "another-channel"
	if err := other.newView().addLocallyConstructed(main, proof); err == nil {
		t.Fatal("a different reservation context reused a local Prepared proof")
	}
}

func TestSTPRGBReservationEvidenceReturnsIndependentFactBytes(t *testing.T) {
	facts, sources, _, _ := rgbReservationNativeEvidenceFixture(t)
	point, err := wire.NewOutPointFromString(sources[0].OutPoint)
	if err != nil {
		t.Fatal(err)
	}
	view := facts.newView()
	raw, err := view.GetRawTx(point.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	original := append([]byte(nil), raw...)
	raw[0]++
	got, err := view.GetRawTx(point.Hash.String())
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal("consumer mutated reservation-owned transaction bytes")
	}
	utxo, err := view.GetUTXO(point.String())
	if err != nil {
		t.Fatal(err)
	}
	utxo.PkScript[0]++
	again, err := view.GetUTXO(point.String())
	if err != nil || !bytes.Equal(again.PkScript, sources[0].PkScript) {
		t.Fatal("consumer mutated reservation-owned output script")
	}
	if (&RevocationInfo{}).RGBEvidence != nil {
		t.Fatal("ordinary reservation has RGB evidence")
	}
}
