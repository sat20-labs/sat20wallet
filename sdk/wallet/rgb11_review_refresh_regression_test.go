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
	targetOutpoint := fmt.Sprintf("%s:%d", pending.State.WitnessTxID, pending.State.RecipientVout)
	fault := &reviewRGB11StatusFailure{
		BitcoinEvidenceProvider: evidence, targetTxID: pending.State.WitnessTxID,
		targetOutpoint: targetOutpoint, failure: failure,
	}
	manager.rgbManager.evidence = fault
	manager.rgbManager.markRGB11ProjectionVerified()
	before := snapshotRGB11ProjectionDB(t, manager.db)
	_, err = manager.rgbManager.refreshRGB11State(context.Background())
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "build RGB11 refresh candidate") || fault.hits != 1 {
		t.Fatalf("did not exercise receive build failure: hits=%d err=%v", fault.hits, err)
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
	targetTxID, targetOutpoint string
	failure                    error
	carrierVerified            bool
	hits                       int
}

func (e *reviewRGB11StatusFailure) GetUTXO(outpoint string) (*rgb11wallet.BitcoinUTXO, error) {
	utxo, err := e.BitcoinEvidenceProvider.GetUTXO(outpoint)
	if outpoint == e.targetOutpoint && err == nil && utxo != nil {
		e.carrierVerified = true
	}
	return utxo, err
}

func (e *reviewRGB11StatusFailure) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	if txid == e.targetTxID && e.carrierVerified {
		e.hits++
		return nil, e.failure
	}
	return e.BitcoinEvidenceProvider.GetTxStatus(txid)
}

type reviewRGB11EvidenceReads struct {
	rgb11wallet.BitcoinEvidenceProvider
	utxos, statuses, outspends, raw, tips int
	spent                                 bool
}

func (e *reviewRGB11EvidenceReads) GetUTXO(point string) (*rgb11wallet.BitcoinUTXO, error) {
	e.utxos++
	if e.BitcoinEvidenceProvider != nil {
		return e.BitcoinEvidenceProvider.GetUTXO(point)
	}
	return &rgb11wallet.BitcoinUTXO{OutPoint: point, PkScript: []byte{1}, Confirmations: 2}, nil
}
func (e *reviewRGB11EvidenceReads) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	e.statuses++
	if e.BitcoinEvidenceProvider != nil {
		return e.BitcoinEvidenceProvider.GetTxStatus(txid)
	}
	return &rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 2}, nil
}
func (e *reviewRGB11EvidenceReads) GetOutspend(point string) (*rgb11wallet.BitcoinOutspend, error) {
	e.outspends++
	if e.BitcoinEvidenceProvider != nil {
		return e.BitcoinEvidenceProvider.GetOutspend(point)
	}
	return &rgb11wallet.BitcoinOutspend{Spent: e.spent}, nil
}
func (e *reviewRGB11EvidenceReads) GetRawTx(txid string) ([]byte, error) {
	e.raw++
	if e.BitcoinEvidenceProvider != nil {
		return e.BitcoinEvidenceProvider.GetRawTx(txid)
	}
	return []byte{1, 2, 3}, nil
}

func (e *reviewRGB11EvidenceReads) GetTip() (*rgb11wallet.BitcoinTip, error) {
	e.tips++
	return e.BitcoinEvidenceProvider.GetTip()
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
	sender, recipient, imported, evidence, _ := newRGB11GenericSendFixture(t)
	sender.StopDKVSSync()
	recipient.StopDKVSSync()
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
	reads := &reviewRGB11EvidenceReads{BitcoinEvidenceProvider: sender.rgbManager.evidence}
	sender.rgbManager.evidence = reads
	const iterations = 3
	started := time.Now()
	for i := 0; i < iterations; i++ {
		iterationStarted := time.Now()
		if _, err := sender.rgbManager.RefreshRGB11State(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Logf("unchanged RGB refresh sample=%d elapsed=%s", i+1, time.Since(iterationStarted))
	}
	t.Logf("unchanged RGB refresh iterations=%d elapsed=%s tips=%d utxos=%d statuses=%d outspends=%d raw=%d snapshot_writes=%d",
		iterations, time.Since(started), reads.tips, reads.utxos, reads.statuses, reads.outspends, reads.raw, database.snapshotWrites)
	if reads.tips != 3*iterations || reads.utxos != 2*iterations || reads.statuses != 2*iterations ||
		reads.outspends != 2*iterations || reads.raw != iterations {
		t.Fatalf("unchanged refresh must resolve once and recheck mutable evidence before publication: %+v", reads)
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
	beforeLocal, err := sender.rgbManager.readRGB11Local(walletID)
	if err != nil {
		t.Fatal(err)
	}
	assertRejected := func(t *testing.T, err error, message string) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), message) {
			t.Fatalf("publication accepted changed chain evidence: %v", err)
		}
		local, readErr := sender.rgbManager.readRGB11Local(walletID)
		if readErr != nil || !reflect.DeepEqual(beforeLocal, local) ||
			!reflect.DeepEqual(beforeLocks, sender.utxoLockerL1.GetLockedUtxoList()) {
			t.Fatalf("rejected publication changed durable snapshot or locks: %v", readErr)
		}
		if sender.rgbManager.rgb11ProjectionAvailable() {
			t.Fatal("rejected publication retained verified ownership")
		}
	}
	t.Run("final_tip_rechecked", func(t *testing.T) {
		fault := &rgb11ProjectionFinalTipChangeEvidence{BitcoinEvidenceProvider: evidence}
		sender.rgbManager.evidence = fault
		defer func() { sender.rgbManager.evidence = evidence }()
		_, err := sender.rgbManager.RefreshRGB11State(context.Background())
		assertRejected(t, err, "chain tip changed before projection commit")
		if fault.callCount() != 3 {
			t.Fatalf("tip reads=%d, want first resolution plus publication recheck", fault.callCount())
		}
	})
	if _, err := sender.rgbManager.RefreshRGB11State(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Run("same_tip_outspend_rechecked", func(t *testing.T) {
		fault := &rgb11ProjectionSameTipOutspendEvidence{
			BitcoinEvidenceProvider: evidence, target: rgb11GenericFixtureSource(t, sender, imported),
			changeAt: 2, spendingTx: strings.Repeat("a", 64),
		}
		sender.rgbManager.evidence = fault
		defer func() { sender.rgbManager.evidence = evidence }()
		_, err := sender.rgbManager.RefreshRGB11State(context.Background())
		assertRejected(t, err, "Bitcoin evidence changed while building RGB11 projection")
		if fault.callCount() != 2 {
			t.Fatalf("outspend reads=%d, want initial resolution and publication recheck", fault.callCount())
		}
	})
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
							state.Invoice != "" || !state.SyntheticInvoiceRemoved {
							t.Errorf("first %s commit omitted address metadata", state.Direction)
						}
						if state.Direction == "send" {
							relay, relayErr := dkvsindexer.MailMsgKey(state.ReceiverAccountID, state.SenderAccountID, state.AddressMessageID)
							ack, ackErr := dkvsindexer.MailMsgKey(state.SenderAccountID, state.ReceiverAccountID, state.AddressMessageID)
							if relayErr != nil || ackErr != nil || state.RelayRecordKey != relay || state.AckRecordKey != ack {
								t.Error("send commit lost stable business correlation keys")
							}
							if state.DeliveryMode == "" {
								if state.DeliveryRecordKey != "" || state.DeliveryRecordHash != "" {
									t.Error("undelivered send commit contains a fabricated mailbox receipt")
								}
							} else if state.DeliveryRecordKey == "" || state.DeliveryRecordHash == "" {
								t.Error("delivered send commit omitted actual mailbox receipt")
							}
						} else if state.DeliveryRecordKey == "" || state.DeliveryRecordHash == "" {
							t.Error("first receive commit omitted actual mailbox receipt")
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
				if state.DeliveryRecordKey != "" || state.DeliveryRecordHash != "" || state.DeliveryMode != "" {
					t.Fatal("prepared transfer must not claim a mailbox delivery")
				}
				result, err := sender.rgbManager.deliverRGB11AddressTransferStore(mustRGB11ConfiguredStore(t, sender), state.TransferID, RGB11AddressDeliveryOptions{})
				if err != nil {
					t.Fatal(err)
				}
				persisted, err := sender.rgbManager.projectionStore.LoadPendingTransfer(state.TransferID)
				if err != nil || result.RecordKey == "" || result.RecordHash == "" || persisted == nil ||
					persisted.State.DeliveryRecordKey != result.RecordKey || persisted.State.DeliveryRecordHash != result.RecordHash ||
					persisted.State.RelayRecordKey != state.RelayRecordKey || persisted.State.AckRecordKey != state.AckRecordKey {
					t.Fatalf("delivered receipt or stable business keys not committed: err=%v", err)
				}
			}
			result, err := recipient.rgbManager.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
			if err != nil || result.Received != outputs || result.Invalid != 0 {
				t.Fatalf("address receive result=%+v err=%v", result, err)
			}
		})
	}
}

// Capacity belongs to each module KV. An unrelated oversized provider must
// not disable any of the existing send/receive operations or release their locks.
func TestRGB11AddressCapacityFailureKeepsLocalRecoveryAndLocks(t *testing.T) {
	for _, path := range []string{"single_send", "batch_send", "receive"} {
		t.Run(path, func(t *testing.T) {
			sender, recipient, imported, evidence, _ := newRGB11GenericSendFixture(t)
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
			if _, err := manager.RefreshRGB11State(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := manager.SyncAccountManagementState(context.Background()); err != nil {
				t.Fatal(err)
			}
			scope, err := manager.rgbManager.rgb11AccountManagedScopeID()
			if err != nil {
				t.Fatal(err)
			}
			walletID, err := manager.RGB11WalletID()
			if err != nil {
				t.Fatal(err)
			}
			before, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
			if err != nil {
				t.Fatal(err)
			}
			capacityBefore, err := manager.preflightRGB11TerminalRecovery(scope, before)
			if err != nil {
				t.Fatal(err)
			}
			provider := &accountManagedCapacityProvider{scope: scope, payload: make([]byte, swire.MaxDKVSBlobValueSize+1)}
			if err := manager.RegisterAccountManagedDataProvider(provider); err != nil {
				t.Fatal(err)
			}
			if err := manager.SyncAccountManagementState(context.Background()); err == nil {
				t.Fatal("independent oversized module did not reach its real backup failure")
			}
			failed, err := manager.readAccountLocal(manager.accountProfile.AccountID, provider.ID(), scope)
			if err != nil || failed == nil || failed.LastError == "" || failed.Generation <= failed.Confirmed {
				t.Fatalf("oversized provider lost its scoped pending/error marker: %v", err)
			}
			if err := manager.CheckAccountWrite(rgb11AccountManagedProviderID, scope); err != nil {
				t.Fatalf("independent capacity failure blocked healthy RGB business: %v", err)
			}
			capacityAfter, err := manager.preflightRGB11TerminalRecovery(scope, before)
			if err != nil || capacityAfter != capacityBefore {
				t.Fatalf("unrelated provider inflated RGB capacity: %v", err)
			}
			locks := manager.utxoLockerL1.GetLockedUtxoList()
			reservations, err := manager.rgbManager.loadRGB11Reservations()
			if err != nil {
				t.Fatal(err)
			}
			beforeBroadcasts := evidence.broadcastCount
			if path == "receive" {
				result, err := manager.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
				if err != nil || result == nil || result.Received != 1 || result.Invalid != 0 {
					t.Fatalf("independent failure blocked receive: %+v err=%v", result, err)
				}
			} else if path == "single_send" {
				if _, _, err := sender.PrepareConfiguredRGB11AddressTransfer(context.Background(), request, dkvsindexer.RecordVerificationOptions{}); err != nil {
					t.Fatal(err)
				}
			} else if _, err := sender.rgbManager.prepareRGB11AddressBatch(context.Background(), []RGB11AddressSendRequest{request, request}, dkvsindexer.RecordVerificationOptions{}); err != nil {
				t.Fatal(err)
			}
			after, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := rgb11wallet.RecoveryPackageFromSnapshot(after, 0); err != nil {
				t.Fatalf("local operation lost complete recovery: %v", err)
			}
			if !rgb11SnapshotChanged(before, after) || !rgb11LocalModulePendingForTest(t, manager) {
				t.Fatal("business operation did not retain its durable pending recovery change")
			}
			afterReservations, err := manager.rgbManager.loadRGB11Reservations()
			if err != nil {
				t.Fatal(err)
			}
			for _, prior := range reservations {
				found := false
				for _, current := range afterReservations {
					found = found || reflect.DeepEqual(prior, current)
				}
				if !found {
					t.Fatal("unrelated reservation changed during capacity-isolated business")
				}
			}
			pending, err := rgb11wallet.PendingTransfersFromProjectionSnapshot(after.ProjectionRecords)
			if err != nil {
				t.Fatal(err)
			}
			inputs := make(map[string]bool)
			for _, value := range pending {
				for _, point := range value.State.InputOutPoints {
					inputs[point] = true
				}
				if value.State.Direction == "send" && !rgb11TransferKeepsInputsLocked(&value.State) {
					t.Fatal("prepared send lost its input protection")
				}
			}
			afterLocks := manager.utxoLockerL1.GetLockedUtxoList()
			for point := range inputs {
				lock := afterLocks[point]
				if lock == nil || lock.Reason != rgb11wallet.LockReasonPending || !manager.isL1SendInputProtected(point) {
					t.Fatal("prepared send lost its actual input lock")
				}
			}
			for point, prior := range locks {
				if !inputs[point] && !reflect.DeepEqual(prior, afterLocks[point]) {
					t.Fatal("independent capacity failure changed an unrelated lock")
				}
			}
			afterRows := make(map[string][]byte, len(after.ProjectionRecords))
			for _, record := range after.ProjectionRecords {
				afterRows[record.Key] = record.Value
			}
			for _, record := range before.ProjectionRecords {
				if strings.HasPrefix(record.Key, "object-") &&
					!bytes.Equal(record.Value, afterRows[record.Key]) {
					t.Fatal("capacity-isolated business changed an existing consignment")
				}
			}
			if evidence.broadcastCount != beforeBroadcasts {
				t.Fatal("prepare/receive crossed the sender broadcast boundary")
			}
			balance, err := manager.GetRGB11AssetBalance(&imported.AssetName)
			want := uint64(50000)
			if path == "receive" {
				want = 0
			}
			if err != nil || want == 0 && balance.Sign() != 0 || want != 0 && (balance == nil || balance.Value.Uint64() != want) {
				t.Fatalf("prepare/receive changed asset ownership: %v", err)
			}
		})
	}
}

func (d *reviewRGB11WritesDB) NewAtomicWriteBatch() indexer.WriteBatch {
	return &reviewRGB11WritesBatch{WriteBatch: d.KVDB.(indexer.AtomicWriteBatchDB).NewAtomicWriteBatch(), owner: d}
}
