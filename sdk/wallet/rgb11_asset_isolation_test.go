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
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// Both assets are native issued contracts with current Bitcoin evidence. The
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
		t.Fatalf("import second native contract: result=%+v err=%v", b, err)
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
		t.Fatalf("dual native baseline is not available: state_present=%t err=%v", state != nil, err)
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
	want := errors.New("temporary RGB scope read unavailable")
	fault := &rgb11SnapshotReadFailureDB{KVDB: database,
		prefix: []byte("rgb11-" + runtime.rgb11ScopeKey() + "-"), failure: want}
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
	fault.armed = false
	if fault.hits == 0 || !errors.Is(refreshErr, want) {
		t.Fatalf("unlocatable DB failure did not fire with its cause: hits=%d err=%v", fault.hits, refreshErr)
	}
	state, err := owner.GetRGB11State()
	if err == nil && state != nil && len(state.AvailableAssets) != 0 {
		t.Fatal("unlocatable DB failure retained spendable assets")
	}
	// A restored DB provider is not itself fresh proof of spendability.
	owner.db, owner.rgbManager = database, runtime
	state, err = owner.GetRGB11State()
	if err != nil || state == nil || len(state.AvailableAssets) != 0 {
		t.Fatalf("read recovery became ready without revalidation: %v", err)
	}
	after, _, err := runtime.exportRGB11WalletSnapshot(walletID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("read failure modified durable records: %v", err)
	}
	if _, err := owner.RefreshRGB11State(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err = owner.GetRGB11State()
	if err != nil || state == nil || !rgbFaultHasAsset(state.AvailableAssets, f.a.AssetName) ||
		!rgbFaultHasAsset(state.AvailableAssets, f.b.AssetName) {
		t.Fatalf("fresh revalidation did not restore both assets: %v", err)
	}
	if f.evidence.broadcastCount != 0 {
		t.Fatalf("read recovery broadcast %d transactions", f.evidence.broadcastCount)
	}
}

func TestRGBFaultCapacityBounds(t *testing.T) {
	f := newRGBFaultFixture(t, false)
	key, _, damaged := f.breakB(t, "decode")
	if _, err := f.sender.RefreshRGB11State(context.Background()); err == nil {
		t.Fatal("missing fault")
	}
	id, err := f.sender.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	full, err := f.sender.rgbManager.readRGB11Local(id)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := f.sender.rgbManager.rgb11AccountManagedScopeID()
	if err != nil {
		t.Fatal(err)
	}
	provider := &accountManagedCapacityProvider{scope: scope, payload: []byte{1}}
	if err := f.sender.RegisterAccountManagedDataProvider(provider); err != nil {
		t.Fatal(err)
	}
	size, err := f.sender.preflightRGB11TerminalRecovery(scope, full)
	if err != nil || !size.Conservative || !size.BackupPending {
		t.Fatalf("localized complete capacity was not measured: %+v %v", size, err)
	}
	baseline := size
	// Another provider owns another KV. Its overflow must not inflate this
	// scope or discard any damaged original records from the conservative bound.
	provider.payload = make([]byte, swire.MaxDKVSBlobValueSize+1)
	size, err = f.sender.preflightRGB11TerminalRecovery(scope, full)
	if err != nil || size != baseline {
		t.Fatalf("independent module changed RGB recovery capacity: %+v %v", size, err)
	}
	// Measure the actual current RGB module codec, rather than an old aggregate
	// bundle. Locate its effective envelope/blob boundary without guessed overhead.
	low, high := 0, swire.MaxDKVSBlobValueSize+1
	for low+1 < high {
		mid := low + (high-low)/2
		_, err := measureRGB11ModuleCapacity(rgb11RecoveryBundleSize{}, f.sender.accountSecret,
			f.sender.accountProfile.AccountID, f.sender.cfg.Chain, scope, make([]byte, mid))
		if err == nil {
			low = mid
		} else {
			high = mid
		}
	}
	if low < baseline.RPKEncodedBytes {
		t.Fatal("effective module capacity is smaller than the real recovery closure")
	}
	if _, err := measureRGB11ModuleCapacity(rgb11RecoveryBundleSize{}, f.sender.accountSecret,
		f.sender.accountProfile.AccountID, f.sender.cfg.Chain, scope, make([]byte, low)); err != nil {
		t.Fatalf("exact RGB module capacity rejected: %v", err)
	}
	if _, err := measureRGB11ModuleCapacity(rgb11RecoveryBundleSize{}, f.sender.accountSecret,
		f.sender.accountProfile.AccountID, f.sender.cfg.Chain, scope, make([]byte, low+1)); err == nil {
		t.Fatal("one-byte RGB module overflow accepted")
	}
	if _, err := rgb11wallet.RecoveryPackageFromSnapshot(full, 0); err == nil {
		t.Fatal("sizing created a valid incomplete recovery package")
	}
	stored, err := f.sender.db.Read([]byte(key))
	if err != nil || !bytes.Equal(stored, damaged) {
		t.Fatal("capacity check changed raw B")
	}
}
