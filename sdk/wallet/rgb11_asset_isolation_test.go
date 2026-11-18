package wallet

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/schemas"
)

// Both assets are RGB11 issued contracts with current Bitcoin evidence. The
// shared variant really imports both genesis allocations onto the same UTXO;
// it does not move an existing proof or invent an allocation after validation.
type rgbFaultFixture struct {
	sender    *Manager
	recipient *Manager
	a         *RGB11ImportResult
	b         *RGB11ImportResult
	aPoint    string
	bPoint    string
	evidence  *rgb11AddressEvidence
	rpc       *rgb11FlowIndexer
}

func newRGBFaultFixture(t *testing.T, shared bool) *rgbFaultFixture {
	t.Helper()
	sender, recipient, a, evidence, rpc := newRGB11GenericSendFixture(t)
	sender.rgbManager.scopeStates.stopReconciliations()
	recipient.rgbManager.scopeStates.stopReconciliations()
	aPoint := rgb11GenericFixtureSource(t, sender, a)
	bPoint := aPoint
	if !shared {
		bPoint = rpc.plain[len(rpc.plain)-1].OutPoint
	}
	allocations, err := rgb11IssueAllocations([]string{bPoint}, []uint64{7000})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := issuance.Issue(issuance.Spec{
		Kind: schemas.NIA, Network: rgb11IssuanceNetwork(GetChainParam()),
		Ticker: "FAULTB", Name: "Independent isolation asset B",
		Allocations: allocations, Timestamp: 1_700_000_123,
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := sender.ImportRGB11Contract(context.Background(), []byte(issued.Armor))
	if err != nil || b == nil || b.Projected != 1 || b.ContractID == a.ContractID {
		t.Fatalf("import second RGB11 contract: result=%+v err=%v", b, err)
	}
	remote := sender.http.(*rgb11MemoryDKVSHTTP)
	installRGB11PublicContractFixture(t, sender, remote, b.ContractID)
	// Keep a separate plain fee UTXO: asset B must never become a fee input.
	script := rpc.outputs[aPoint].OutValue.PkScript
	feePoint := seedRGB11FlowEvidenceUTXO(t, evidence.rgb11FlowEvidence, script, 100_000, 94)
	feeOutput := indexer.NewTxOutput(100_000)
	feeOutput.OutPointStr, feeOutput.OutValue.PkScript = feePoint, append([]byte(nil), script...)
	rpc.outputs[feePoint] = feeOutput
	rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{
		OutPoint: feePoint, Value: 100_000, PkScript: append([]byte(nil), script...),
	})
	if _, err := sender.RefreshRGB11State(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err := sender.GetRGB11State()
	if err != nil || state == nil || !rgbFaultHasAsset(state.AvailableAssets, a.AssetName) ||
		!rgbFaultHasAsset(state.AvailableAssets, b.AssetName) {
		t.Fatalf("dual RGB11 baseline is not available: state_present=%t err=%v", state != nil, err)
	}
	return &rgbFaultFixture{sender: sender, recipient: recipient, a: a, b: b,
		aPoint: aPoint, bPoint: bPoint, evidence: evidence, rpc: rpc}
}

func rgbFaultHasAsset(assets indexer.TxAssets, name indexer.AssetName) bool {
	for _, asset := range assets {
		if asset.Name == name && asset.Amount.Sign() > 0 {
			return true
		}
	}
	return false
}

func (f *rgbFaultFixture) breakB(t *testing.T, kind string) (string, []byte, []byte) {
	t.Helper()
	key := "rgb11-" + f.sender.rgbManager.rgb11ScopeKey() + "-proof-" + f.bPoint + "-" + f.b.AssetName.String()
	original, err := f.sender.db.Read([]byte(key))
	if err != nil {
		t.Fatal(err)
	}
	damaged := []byte{0xff, 0x00, 0xff}
	if kind == "binding" {
		proof, err := f.sender.rgbManager.projectionStore.LoadProof(f.bPoint, f.b.AssetName)
		if err != nil || proof == nil || bytes.Count(original, []byte(proof.ValidationHash)) != 1 {
			t.Fatalf("cannot target exact stored binding: %v", err)
		}
		damaged = bytes.Replace(original, []byte(proof.ValidationHash), []byte(strings.Repeat("0", len(proof.ValidationHash))), 1)
	}
	if err := f.sender.db.Write([]byte(key), damaged); err != nil {
		t.Fatal(err)
	}
	return key, append([]byte(nil), original...), append([]byte(nil), damaged...)
}

func TestRGBFaultIndependentSend(t *testing.T) {
	for _, kind := range []string{"binding", "decode"} {
		t.Run(kind, func(t *testing.T) {
			f := newRGBFaultFixture(t, false)
			ctx := context.Background()
			key, _, damaged := f.breakB(t, kind)
			if _, err := f.sender.RefreshRGB11State(ctx); err == nil {
				t.Fatal("damaged asset B was not reported")
			}
			state, err := f.sender.GetRGB11State()
			if err != nil || state == nil || !rgbFaultHasAsset(state.AvailableAssets, f.a.AssetName) ||
				rgbFaultHasAsset(state.AvailableAssets, f.b.AssetName) {
				t.Fatalf("B damage contaminated independent A: state_present=%t err=%v", state != nil, err)
			}
			// A visible balance alone is insufficient. Exercise the same public
			// prepare, recipient validation, signing and broadcast path as PWA.
			beforeGeneration := rgb11LocalModuleForTest(t, f.sender).Generation
			invoice, err := f.recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
				Mode: "witness", TransportMode: "out-of-band", ContractID: f.a.ContractID,
				AmountRaw: "10000", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
			})
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := f.sender.PrepareRGB11Transfer(ctx, RGB11SendRequest{
				Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1,
			})
			if err != nil {
				t.Fatalf("independent A could not prepare an actual send: %v", err)
			}
			for _, point := range prepared.State.InputOutPoints {
				if point == f.bPoint {
					t.Fatal("asset B was consumed as a carrier or fee input")
				}
			}
			if _, err := f.recipient.PrepareRGB11Consignment(ctx, invoice.RequestID,
				[]byte(prepared.RecipientConsignment)); err != nil {
				t.Fatal(err)
			}
			txid, err := f.sender.BroadcastRGB11OutOfBand([]string{prepared.State.TransferID})
			if err != nil || txid != prepared.TxID {
				t.Fatalf("independent A did not broadcast: txid=%s err=%v", txid, err)
			}
			var witness wire.MsgTx
			if witness.Deserialize(bytes.NewReader(f.evidence.broadcasted)) != nil ||
				!witness.HasWitness() || witness.TxHash().String() != txid || f.evidence.broadcastCount != 1 {
				t.Fatal("actual signed witness was not broadcast exactly once")
			}
			for _, input := range witness.TxIn {
				if input.PreviousOutPoint.String() == f.bPoint {
					t.Fatal("signed witness spent asset B")
				}
			}
			id, err := f.sender.RGB11WalletID()
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := f.sender.rgbManager.exportRGB11WalletSnapshot(id); err == nil {
				t.Fatal("incomplete backup was reported complete after A sent")
			}
			module := rgb11LocalModuleForTest(t, f.sender)
			if module.Generation <= beforeGeneration || module.Generation <= module.Confirmed {
				t.Fatal("A mutation lost its pending backup generation")
			}
			stored, err := f.sender.db.Read([]byte(key))
			if err != nil || !bytes.Equal(stored, damaged) {
				t.Fatal("isolation changed or discarded the original damaged record")
			}
		})
	}
}

func TestRGBFaultSharedCarrier(t *testing.T) {
	f := newRGBFaultFixture(t, true)
	key, _, damaged := f.breakB(t, "decode")
	if _, err := f.sender.RefreshRGB11State(context.Background()); err == nil {
		t.Fatal("shared carrier damage was not reported")
	}
	state, err := f.sender.GetRGB11State()
	if err != nil || state == nil || rgbFaultHasAsset(state.AvailableAssets, f.a.AssetName) ||
		rgbFaultHasAsset(state.AvailableAssets, f.b.AssetName) {
		t.Fatalf("shared carrier remained available: state_present=%t err=%v", state != nil, err)
	}
	if _, _, _, _, _, _, err := f.sender.rgbManager.selectRGB11Allocations(f.a.ContractID, 1, 1); err == nil {
		t.Fatal("ordinary RGB selection spent a shared damaged carrier")
	}
	if inputs, err := (rgb11STPProtocol{}).PrepareInputs(f.sender, STPAssetInputRequest{
		Asset: &f.a.AssetName, Amount: indexer.NewDecimal(1, 0), Operation: STPOpLock,
	}); err == nil || inputs != nil && len(inputs.Inputs) != 0 {
		t.Fatal("STP selection spent a shared damaged carrier")
	}
	stored, err := f.sender.db.Read([]byte(key))
	if err != nil || !bytes.Equal(stored, damaged) || f.evidence.broadcastCount != 0 {
		t.Fatal("shared carrier refusal changed original data or broadcast")
	}
}

func TestRGBFaultReadFailureRecovery(t *testing.T) {
	f := newRGBFaultFixture(t, false)
	owner := f.sender
	runtime, database := owner.rgbManager, owner.db
	walletID, err := runtime.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := runtime.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := owner.GetRGB11State()
	if err != nil || baseline == nil || !rgbFaultHasAsset(baseline.AvailableAssets, f.a.AssetName) ||
		!rgbFaultHasAsset(baseline.AvailableAssets, f.b.AssetName) {
		t.Fatalf("missing dual-asset baseline: %v", err)
	}
	wantAssets := baseline.AvailableAssets.Clone()
	scope := runtime.rgb11ScopeKey()
	beforeRaw := rgb11SnapshotRawDBRecords(t, database, scope)
	want := errors.New("temporary RGB scope read unavailable")
	fault := &rgb11SnapshotReadFailureDB{KVDB: database,
		prefix: []byte("rgb11-" + scope + "-"), failure: want}
	owner.db = fault
	owner.rgbManager, err = newRGB11Manager(owner, fault, owner.utxoLockerL1, f.evidence)
	if err != nil {
		t.Fatal(err)
	}
	owner.rgbManager.scopeStates = runtime.scopeStates
	if err := owner.rgbManager.selectRGB11Scope(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.db, owner.rgbManager = database, runtime })
	fault.armed = true
	_, refreshErr := owner.RefreshRGB11State(context.Background())
	if fault.hits == 0 || !errors.Is(refreshErr, want) {
		t.Fatalf("unlocatable DB failure did not fire with its cause: hits=%d err=%v", fault.hits, refreshErr)
	}
	refreshHits := fault.hits
	state, err := owner.GetRGB11State()
	if fault.hits <= refreshHits || !errors.Is(err, want) || state != nil {
		t.Fatalf("Get must report its own failed read without a state: hits=%d->%d state_present=%t err=%v",
			refreshHits, fault.hits, state != nil, err)
	}
	// Only restore DB readability; keep the runtime that observed the failure.
	fault.armed = false
	state, err = owner.GetRGB11State()
	if err != nil || state == nil || !wantAssets.Equal(state.AvailableAssets) {
		t.Fatalf("read recovery did not restore both original balances: %v", err)
	}
	after, _, err := owner.rgbManager.exportRGB11WalletSnapshot(walletID)
	if err != nil || !reflect.DeepEqual(before, after) ||
		!reflect.DeepEqual(beforeRaw, rgb11SnapshotRawDBRecords(t, owner.db, scope)) {
		t.Fatalf("read failure modified durable records: %v", err)
	}
	refreshed, err := owner.RefreshRGB11State(context.Background())
	if err != nil || refreshed == nil || refreshed.Unresolved != 0 || len(refreshed.Inconsistent) != 0 {
		t.Fatalf("revalidation did not complete: result=%+v err=%v", refreshed, err)
	}
	state, err = owner.GetRGB11State()
	if err != nil || state == nil || state.ConsistencyStatus != "ok" || !wantAssets.Equal(state.AvailableAssets) {
		t.Fatalf("revalidated state did not restore both original balances: %v", err)
	}
	if f.evidence.broadcastCount != 0 {
		t.Fatalf("read recovery broadcast %d transactions", f.evidence.broadcastCount)
	}
}
