package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func TestRGB11RefreshBuildFailureRevokesVerifiedProjection(t *testing.T) {
	sender, manager, imported, evidence, rpc := newRGB11GenericSendFixture(t)
	sender.rgbManager.scopeStates.stopReconciliations()
	manager.rgbManager.scopeStates.stopReconciliations()
	ctx := context.Background()
	invoice, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID,
		AmountRaw: "10000", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := sender.PrepareRGB11Transfer(ctx, RGB11SendRequest{Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1})
	if err != nil {
		t.Fatal(err)
	}
	pending, _ := confirmRGB11ProjectionTestTransfer(t, sender, evidence, rpc, prepared.State.TransferID)
	if _, err := manager.AcceptRGB11Consignment(ctx, invoice.RequestID, pending.RecipientConsignment); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.rgbManager.RefreshRGB11State(ctx); err != nil {
		t.Fatal(err)
	}
	live, err := manager.GetRGB11State()
	if err != nil || len(live.AvailableAssets) == 0 {
		t.Fatalf("positive control has no available receive: %v", err)
	}
	failure := errors.New("receive witness lookup unavailable")
	manager.rgbManager.evidence = &reviewRGB11StatusFailure{
		BitcoinEvidenceProvider: evidence, failure: failure,
	}
	manager.rgbManager.markRGB11ProjectionVerified()
	before := snapshotRGB11ProjectionDB(t, manager.db)
	_, err = manager.rgbManager.refreshRGB11State(context.Background())
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "build RGB11 refresh candidate") {
		t.Fatalf("did not exercise receive build failure: %v", err)
	}
	if manager.rgbManager.rgb11ProjectionAvailable() || manager.rgbManager.rgb11ProjectionStableConfirmed() {
		t.Fatal("failed candidate build retained verified ownership")
	}
	state, err := manager.GetRGB11State()
	if err != nil || len(state.AvailableAssets) != 0 {
		t.Fatalf("failed refresh still exposed available assets: state=%v err=%v", state, err)
	}
	if !reflect.DeepEqual(before, snapshotRGB11ProjectionDB(t, manager.db)) {
		t.Fatal("failed refresh changed durable recovery material")
	}
}

type reviewRGB11StatusFailure struct {
	rgb11wallet.BitcoinEvidenceProvider
	failure error
}

func (e *reviewRGB11StatusFailure) GetTxStatus(string) (*rgb11wallet.BitcoinTxStatus, error) {
	return nil, e.failure
}

type reviewRGB11EvidenceReads struct {
	rgb11wallet.BitcoinEvidenceProvider
	utxos, statuses, outspends, raw int
	spent                           bool
}

func (e *reviewRGB11EvidenceReads) GetUTXO(point string) (*rgb11wallet.BitcoinUTXO, error) {
	e.utxos++
	return &rgb11wallet.BitcoinUTXO{OutPoint: point, PkScript: []byte{1}, Confirmations: 2}, nil
}
func (e *reviewRGB11EvidenceReads) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	e.statuses++
	return &rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 2}, nil
}
func (e *reviewRGB11EvidenceReads) GetOutspend(string) (*rgb11wallet.BitcoinOutspend, error) {
	e.outspends++
	return &rgb11wallet.BitcoinOutspend{Spent: e.spent}, nil
}
func (e *reviewRGB11EvidenceReads) GetRawTx(string) ([]byte, error) {
	e.raw++
	return []byte{1, 2, 3}, nil
}

func TestRGB11CandidateEvidenceReusesReadsAndRechecksBeforePublish(t *testing.T) {
	provider := &reviewRGB11EvidenceReads{}
	evidence := newRGB11CandidateEvidence(provider)
	for i := 0; i < 3; i++ {
		utxo, err := evidence.GetUTXO("point")
		if err != nil || utxo.PkScript[0] != 1 {
			t.Fatalf("UTXO cache corrupted: %v", err)
		}
		utxo.PkScript[0] = 9
		status, err := evidence.GetTxStatus("tx")
		if err != nil || status.Confirmations != 2 {
			t.Fatalf("status cache corrupted: %v", err)
		}
		status.Confirmations = 9
		spent, err := evidence.GetOutspend("point")
		if err != nil || spent.Spent {
			t.Fatalf("outspend cache corrupted: %v", err)
		}
		spent.Spent = true
		raw, err := evidence.GetRawTx("tx")
		if err != nil || !bytes.Equal(raw, []byte{1, 2, 3}) {
			t.Fatalf("raw transaction cache corrupted: %v", err)
		}
		raw[0] = 9
	}
	if provider.utxos != 1 || provider.statuses != 1 || provider.outspends != 1 || provider.raw != 1 {
		t.Fatalf("repeated reads reached provider: %+v", provider)
	}
	if err := evidence.verifyStable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.utxos != 2 || provider.statuses != 2 || provider.outspends != 2 {
		t.Fatal("publication did not re-read mutable chain evidence")
	}
	provider.spent = true
	if err := evidence.verifyStable(context.Background()); err == nil {
		t.Fatal("same-tip outspend change was accepted")
	}
}

// Observe real atomic commits rather than replacing the RGB builder or codec.
type reviewRGB11WritesDB struct {
	indexer.KVDB
	snapshotWrites int
	afterFlush     func()
}
type reviewRGB11WritesBatch struct {
	indexer.WriteBatch
	owner *reviewRGB11WritesDB
}

func (d *reviewRGB11WritesDB) NewWriteBatch() indexer.WriteBatch {
	return &reviewRGB11WritesBatch{WriteBatch: d.KVDB.NewWriteBatch(), owner: d}
}
func (b *reviewRGB11WritesBatch) Put(key, value []byte) error {
	if bytes.HasPrefix(key, []byte("rgb11-")) {
		b.owner.snapshotWrites++
	}
	return b.WriteBatch.Put(key, value)
}
func (b *reviewRGB11WritesBatch) Delete(key []byte) error {
	if bytes.HasPrefix(key, []byte("rgb11-")) {
		b.owner.snapshotWrites++
	}
	return b.WriteBatch.Delete(key)
}
func (b *reviewRGB11WritesBatch) Flush() error {
	if err := b.WriteBatch.Flush(); err != nil {
		return err
	}
	if b.owner.afterFlush != nil {
		b.owner.afterFlush()
	}
	return nil
}
func observeReviewRGB11Writes(t *testing.T, manager *Manager) *reviewRGB11WritesDB {
	t.Helper()
	manager.rgbManager.scopeStates.stopReconciliations()
	database := &reviewRGB11WritesDB{KVDB: manager.db}
	manager.db = database
	runtime, err := newRGB11Manager(manager, database, manager.utxoLockerL1, manager.rgbManager.evidence)
	if err != nil {
		t.Fatal(err)
	}
	runtime.scopeStates = manager.rgbManager.scopeStates
	manager.rgbManager = runtime
	if err := runtime.selectRGB11Scope(); err != nil {
		t.Fatal(err)
	}
	return database
}

func TestRGB11UnchangedRefreshDoesNotRewriteSnapshot(t *testing.T) {
	sender, recipient, _, _, _ := newRGB11GenericSendFixture(t)
	recipient.rgbManager.scopeStates.stopReconciliations()
	database := observeReviewRGB11Writes(t, sender)
	if _, err := sender.rgbManager.RefreshRGB11State(context.Background()); err != nil {
		t.Fatal(err)
	}
	walletID, err := sender.rgbManager.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := sender.rgbManager.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		t.Fatal(err)
	}
	beforeLocks := sender.utxoLockerL1.GetLockedUtxoList()
	database.snapshotWrites = 0
	if _, err := sender.rgbManager.RefreshRGB11State(context.Background()); err != nil {
		t.Fatal(err)
	}
	if database.snapshotWrites != 0 {
		t.Fatalf("unchanged refresh wrote %d engine/projection records", database.snapshotWrites)
	}
	after, _, err := sender.rgbManager.exportRGB11WalletSnapshot(walletID)
	if err != nil || rgb11SnapshotChanged(before, after) || !reflect.DeepEqual(beforeLocks, sender.utxoLockerL1.GetLockedUtxoList()) {
		t.Fatalf("unchanged refresh changed state or locks: %v", err)
	}
	if !sender.rgbManager.rgb11ProjectionAvailable() {
		t.Fatal("verified unchanged refresh lost readiness")
	}
}

func TestRGB11AddressMetadataIncludedInFirstAtomicCommit(t *testing.T) {
	for _, outputs := range []int{1, 2} {
		t.Run(fmt.Sprint(outputs), func(t *testing.T) {
			sender, recipient, imported, _, _ := newRGB11GenericSendFixture(t)
			for _, manager := range []*Manager{sender, recipient} {
				database := observeReviewRGB11Writes(t, manager)
				runtime := manager.rgbManager
				database.afterFlush = func() {
					records, err := runtime.projectionStore.ExportSnapshot()
					if err != nil {
						t.Error(err)
						return
					}
					states, err := rgb11wallet.TransfersFromProjectionSnapshot(records)
					if err != nil {
						t.Error(err)
						return
					}
					for _, state := range states {
						if !state.AddressMode || state.SenderAccountID == "" || state.ReceiverAccountID == "" ||
							state.DeliveryRecordKey == "" || state.Invoice != "" || !state.SyntheticInvoiceRemoved {
							t.Errorf("first %s commit omitted address metadata", state.Direction)
						}
					}
				}
			}
			request := RGB11AddressSendRequest{ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName,
				AmountRaw: "10000", FeeRate: 2, MinConfirmations: 1, Expiry: time.Now().Add(time.Hour).Unix()}
			var prepared *RGB11PreparedTransfer
			var err error
			if outputs == 1 {
				prepared, _, err = sender.rgbManager.PrepareConfiguredRGB11AddressTransfer(context.Background(), request, dkvsindexer.RecordVerificationOptions{})
			} else {
				prepared, err = sender.rgbManager.prepareRGB11AddressBatch(context.Background(), []RGB11AddressSendRequest{request, request}, dkvsindexer.RecordVerificationOptions{})
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, state := range prepared.States {
				if _, err := sender.rgbManager.deliverRGB11AddressTransferStore(mustRGB11ConfiguredStore(t, sender), state.TransferID, RGB11AddressDeliveryOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			result, err := recipient.rgbManager.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
			if err != nil || result.Received != outputs || result.Invalid != 0 {
				t.Fatalf("address receive result=%+v err=%v", result, err)
			}
		})
	}
}

func TestRGB11AddressCapacityFailureKeepsLocalRecoveryAndLocks(t *testing.T) {
	for _, path := range []string{"single_send", "batch_send", "receive"} {
		t.Run(path, func(t *testing.T) {
			sender, recipient, imported, _, _ := newRGB11GenericSendFixture(t)
			sender.rgbManager.scopeStates.stopReconciliations()
			recipient.rgbManager.scopeStates.stopReconciliations()
			request := RGB11AddressSendRequest{ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName,
				AmountRaw: "10000", FeeRate: 2, MinConfirmations: 1, Expiry: time.Now().Add(time.Hour).Unix()}
			manager := sender
			if path == "receive" {
				prepared, _, err := sender.PrepareConfiguredRGB11AddressTransfer(context.Background(), request, dkvsindexer.RecordVerificationOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := sender.rgbManager.deliverRGB11AddressTransferStore(mustRGB11ConfiguredStore(t, sender), prepared.State.TransferID, RGB11AddressDeliveryOptions{}); err != nil {
					t.Fatal(err)
				}
				manager = recipient
			}
			if _, err := manager.rgbManager.RefreshRGB11State(context.Background()); err != nil {
				t.Fatal(err)
			}
			// Establish the ordinary policy result before measuring the recovery
			// boundary; a policy refresh is independent of transfer persistence.
			if path != "receive" {
				if _, _, _, _, _, _, err := manager.rgbManager.selectRGB11Allocations(imported.ContractID, 10000, 1); err != nil {
					t.Fatal(err)
				}
			}
			catalog, err := manager.accountManagedDataCatalog()
			if err != nil || len(catalog.Scopes) != 1 {
				t.Fatalf("catalog: %v", err)
			}
			bundle, err := coreNextManagedBundle(manager, catalog)
			if err != nil {
				t.Fatal(err)
			}
			payload := accountManagedPayloadForEnvelopeSize(t, manager.accountSecret, manager.accountProfile.AccountID,
				bundle, "zz-capacity", catalog.Scopes[0].ID(), swire.MaxDKVSBlobValueSize-8)
			defer zeroBytes(payload)
			if err := manager.RegisterAccountManagedDataProvider(&accountManagedCapacityProvider{scope: catalog.Scopes[0].ID(), payload: payload}); err != nil {
				t.Fatal(err)
			}
			walletID, err := manager.rgbManager.RGB11WalletID()
			if err != nil {
				t.Fatal(err)
			}
			before, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := manager.preflightRGB11TerminalRecovery(catalog.Scopes[0].ID(), before); err != nil {
				t.Fatalf("boundary baseline must fit: %v", err)
			}
			locks := manager.utxoLockerL1.GetLockedUtxoList()
			reservations, err := manager.rgbManager.loadRGB11Reservations()
			if err != nil {
				t.Fatal(err)
			}
			if path == "receive" {
				result, err := recipient.rgbManager.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
				if err != nil || result.Received != 0 || result.Invalid != 1 || !strings.Contains(strings.Join(result.ErrorDetails, " "), "limit") {
					t.Fatalf("expected capacity-rejected receive: %+v err=%v", result, err)
				}
			} else {
				if path == "single_send" {
					_, _, err = sender.rgbManager.PrepareConfiguredRGB11AddressTransfer(context.Background(), request, dkvsindexer.RecordVerificationOptions{})
				} else {
					_, err = sender.rgbManager.prepareRGB11AddressBatch(context.Background(), []RGB11AddressSendRequest{request, request}, dkvsindexer.RecordVerificationOptions{})
				}
				if err == nil || !strings.Contains(err.Error(), "limit") {
					t.Fatalf("expected send capacity rejection: %v", err)
				}
			}
			after, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
			if err != nil || rgb11SnapshotChanged(before, after) {
				t.Fatalf("capacity rejection changed recovery state: %v", err)
			}
			afterReservations, err := manager.rgbManager.loadRGB11Reservations()
			if err != nil || !reflect.DeepEqual(reservations, afterReservations) || !reflect.DeepEqual(locks, manager.utxoLockerL1.GetLockedUtxoList()) {
				t.Fatalf("capacity rejection changed reservations or locks: %v", err)
			}
		})
	}
}
