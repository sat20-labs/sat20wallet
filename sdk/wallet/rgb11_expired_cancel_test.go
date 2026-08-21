package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

type expiredCancelEvidence struct {
	mu            sync.Mutex
	status        *rgb11wallet.BitcoinTxStatus
	statusErr     error
	outspends     map[string]*rgb11wallet.BitcoinOutspend
	outspendErr   map[string]error
	utxos         map[string]*rgb11wallet.BitcoinUTXO
	utxoErr       map[string]error
	statusEntered chan struct{}
	statusRelease chan struct{}
}

func (e *expiredCancelEvidence) GetUTXO(outpoint string) (*rgb11wallet.BitcoinUTXO, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.utxoErr[outpoint]; err != nil {
		return nil, err
	}
	value := e.utxos[outpoint]
	if value == nil {
		return nil, fmt.Errorf("unknown UTXO %s", outpoint)
	}
	copy := *value
	copy.PkScript = append([]byte(nil), value.PkScript...)
	return &copy, nil
}

func (*expiredCancelEvidence) GetRawTx(string) ([]byte, error) { return nil, errors.New("not found") }

func (e *expiredCancelEvidence) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	if e.statusEntered != nil {
		select {
		case e.statusEntered <- struct{}{}:
		default:
		}
	}
	if e.statusRelease != nil {
		<-e.statusRelease
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.statusErr != nil {
		return nil, e.statusErr
	}
	if e.status == nil {
		return &rgb11wallet.BitcoinTxStatus{TxID: txid}, nil
	}
	copy := *e.status
	return &copy, nil
}

func (e *expiredCancelEvidence) GetOutspend(outpoint string) (*rgb11wallet.BitcoinOutspend, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.outspendErr[outpoint]; err != nil {
		return nil, err
	}
	value := e.outspends[outpoint]
	if value == nil {
		return &rgb11wallet.BitcoinOutspend{}, nil
	}
	copy := *value
	return &copy, nil
}

func (*expiredCancelEvidence) GetTip() (*rgb11wallet.BitcoinTip, error) {
	return &rgb11wallet.BitcoinTip{Height: 1, BlockHash: "tip"}, nil
}

func (*expiredCancelEvidence) Broadcast([]byte) (string, error) {
	return "", errors.New("unexpected broadcast")
}

type expiredCancelFixture struct {
	manager     *Manager
	evidence    *expiredCancelEvidence
	transferIDs []string
	input       string
	witnessTxID string
}

func newExpiredCancelFixture(t *testing.T, batchSize int) *expiredCancelFixture {
	t.Helper()
	testWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire",
		"", &chaincfg.TestNet4Params,
	)
	if testWallet == nil {
		t.Fatal("create wallet")
	}
	inputHash := chainhash.Hash{1, 2, 3, 4}
	input := wire.OutPoint{Hash: inputHash, Index: 2}
	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(wire.NewTxIn(&input, nil, nil))
	for i := 0; i <= batchSize; i++ {
		tx.AddTxOut(wire.NewTxOut(330+int64(i), []byte{0x51}))
	}
	var raw bytes.Buffer
	if err := tx.Serialize(&raw); err != nil {
		t.Fatal(err)
	}
	packet, err := psbt.NewFromUnsignedTx(tx)
	if err != nil {
		t.Fatal(err)
	}
	var encodedPSBT bytes.Buffer
	if err := packet.Serialize(&encodedPSBT); err != nil {
		t.Fatal(err)
	}
	witnessTxID := tx.TxHash().String()
	inputText := input.String()
	evidence := &expiredCancelEvidence{
		outspends:   make(map[string]*rgb11wallet.BitcoinOutspend),
		outspendErr: make(map[string]error), utxoErr: make(map[string]error),
		utxos: map[string]*rgb11wallet.BitcoinUTXO{
			inputText: {OutPoint: inputText, Value: 10_000, Confirmations: 6},
		},
	}
	manager := newRGB11FlowManager(t, testWallet, &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}, evidence, 201)
	ids := make([]string, batchSize)
	for i := range ids {
		ids[i] = testRGB11ConsignmentID(t, fmt.Sprintf("expired-transfer-%d", i))
	}
	expiry := time.Now().Add(-time.Hour).Unix()
	senderID, err := dkvsAccountID(testWallet)
	if err != nil {
		t.Fatal(err)
	}
	receiverWallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch",
		"", &chaincfg.TestNet4Params,
	)
	receiverID, err := dkvsAccountID(receiverWallet)
	if err != nil {
		t.Fatal(err)
	}
	consignment := []byte("expired-recipient-consignment")
	localConsignment := []byte("expired-local-consignment")
	consignmentHash := rgb11ObjectHash(consignment)
	pendingList := make([]*rgb11wallet.PendingTransfer, 0, batchSize)
	for i, transferID := range ids {
		messageID, err := rgb11AddressMessageID(transferID)
		if err != nil {
			t.Fatal(err)
		}
		relayKey, err := dkvsindexer.MailMsgKey(receiverID, senderID, messageID)
		if err != nil {
			t.Fatal(err)
		}
		ackKey, err := dkvsindexer.MailMsgKey(senderID, receiverID, messageID)
		if err != nil {
			t.Fatal(err)
		}
		pendingList = append(pendingList, &rgb11wallet.PendingTransfer{
			State: rgb11wallet.TransferState{
				TransferID: transferID, BatchID: "expired-batch", BatchTransferIDs: append([]string(nil), ids...),
				BatchSize: batchSize, RecipientVout: uint32(i + 1), TransportMode: RGB11AddressTransport,
				Direction: "send", RecipientID: receiverID, AddressMode: true,
				AddressMessageID: messageID, SenderAccountID: senderID, ReceiverAccountID: receiverID,
				SyntheticInvoiceRemoved: true,
				InputOutPoints:          []string{inputText}, OutputOutPoints: []string{fmt.Sprintf("%s:%d", witnessTxID, i+1)},
				Expiry: expiry, RelayExpiry: expiry, ConsignmentHash: consignmentHash,
				WitnessTxID: witnessTxID, AckStatus: "awaiting-persistence", Status: "prepared",
				DeliveryRecordKey: relayKey, RelayRecordKey: relayKey, AckRecordKey: ackKey, RelayDurability: "LOCAL_ONLY",
			},
			RecipientConsignment: append([]byte(nil), consignment...),
			LocalConsignment:     append([]byte(nil), localConsignment...),
			SignedTx:             append([]byte(nil), raw.Bytes()...), SignedPSBT: append([]byte(nil), encodedPSBT.Bytes()...),
			ReservationID: "expired-reservation", CreatedAt: time.Now().Add(-2 * time.Hour).Unix(),
		})
	}
	if err := manager.rgbManager.projectionStore.SavePendingTransfers(pendingList); err != nil {
		t.Fatal(err)
	}
	if err := manager.utxoLockerL1.TryReserve([]string{inputText}, rgb11wallet.LockReasonPending, "expired-reservation"); err != nil {
		t.Fatal(err)
	}
	return &expiredCancelFixture{manager: manager, evidence: evidence, transferIDs: ids, input: inputText, witnessTxID: witnessTxID}
}

func assertExpiredTombstone(t *testing.T, fixture *expiredCancelFixture) {
	t.Helper()
	for _, id := range fixture.transferIDs {
		pending, err := fixture.manager.rgbManager.projectionStore.LoadPendingTransfer(id)
		if err != nil {
			t.Fatal(err)
		}
		if pending.State.Status != "rejected" || pending.State.AckStatus != "rejected" ||
			pending.State.RejectReason != rgb11RejectReasonInvoiceExpired || pending.State.WitnessTxID != fixture.witnessTxID ||
			len(pending.State.InputOutPoints) == 0 || len(pending.State.OutputOutPoints) == 0 ||
			pending.State.ConsignmentHash == "" {
			t.Fatalf("incomplete tombstone: %+v", pending.State)
		}
		if len(pending.SignedTx) != 0 || len(pending.SignedPSBT) != 0 ||
			len(pending.RecipientConsignment) != 0 || len(pending.LocalConsignment) != 0 ||
			pending.RecipientObjectHash != "" || pending.LocalObjectHash != "" {
			t.Fatal("private payload was not compacted")
		}
	}
	if lock := fixture.manager.utxoLockerL1.GetLockedUtxoList()[fixture.input]; lock != nil {
		t.Fatalf("reservation was not released: %+v", lock)
	}
}

func TestCancelExpiredRGB11TransferSuccessBatchAndIdempotent(t *testing.T) {
	for _, batchSize := range []int{1, 2} {
		t.Run(fmt.Sprintf("batch-%d", batchSize), func(t *testing.T) {
			fixture := newExpiredCancelFixture(t, batchSize)
			if err := fixture.manager.CancelExpiredRGB11Transfer(fixture.transferIDs[0]); err != nil {
				t.Fatal(err)
			}
			assertExpiredTombstone(t, fixture)
			if err := fixture.manager.CancelExpiredRGB11Transfer(fixture.transferIDs[len(fixture.transferIDs)-1]); err != nil {
				t.Fatalf("idempotent cancel failed: %v", err)
			}
			assertExpiredTombstone(t, fixture)
		})
	}
}

func TestCancelExpiredRGB11TransferRejectsUnsafeStateAndEvidence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *expiredCancelFixture)
	}{
		{"invoice-state-expiry-mismatch", func(t *testing.T, f *expiredCancelFixture) {
			pending, _ := f.manager.rgbManager.projectionStore.LoadPendingTransfer(f.transferIDs[0])
			pending.State.Expiry--
			if err := f.manager.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
				t.Fatal(err)
			}
		}},
		{"witness-txid-mismatch", func(t *testing.T, f *expiredCancelFixture) {
			pending, _ := f.manager.rgbManager.projectionStore.LoadPendingTransfer(f.transferIDs[0])
			pending.State.WitnessTxID = chainhash.Hash{9}.String()
			if err := f.manager.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
				t.Fatal(err)
			}
		}},
		{"signed-tx-mismatch", func(t *testing.T, f *expiredCancelFixture) {
			pending, _ := f.manager.rgbManager.projectionStore.LoadPendingTransfer(f.transferIDs[0])
			pending.SignedTx = append([]byte(nil), pending.SignedTx...)
			pending.SignedTx[len(pending.SignedTx)-1] ^= 1
			if err := f.manager.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
				t.Fatal(err)
			}
		}},
		{"input-mismatch", func(t *testing.T, f *expiredCancelFixture) {
			pending, _ := f.manager.rgbManager.projectionStore.LoadPendingTransfer(f.transferIDs[0])
			pending.State.InputOutPoints = []string{chainhash.Hash{7}.String() + ":0"}
			if err := f.manager.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
				t.Fatal(err)
			}
		}},
		{"output-mismatch", func(t *testing.T, f *expiredCancelFixture) {
			pending, _ := f.manager.rgbManager.projectionStore.LoadPendingTransfer(f.transferIDs[0])
			pending.State.OutputOutPoints = []string{pending.State.WitnessTxID + ":99"}
			if err := f.manager.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
				t.Fatal(err)
			}
		}},
		{"consignment-mismatch", func(t *testing.T, f *expiredCancelFixture) {
			pending, _ := f.manager.rgbManager.projectionStore.LoadPendingTransfer(f.transferIDs[0])
			pending.State.ConsignmentHash = chainhash.Hash{8}.String()
			if err := f.manager.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
				t.Fatal(err)
			}
		}},
		{"mempool", func(_ *testing.T, f *expiredCancelFixture) {
			f.evidence.status = &rgb11wallet.BitcoinTxStatus{TxID: f.witnessTxID, InMempool: true}
		}},
		{"confirmed", func(_ *testing.T, f *expiredCancelFixture) {
			f.evidence.status = &rgb11wallet.BitcoinTxStatus{TxID: f.witnessTxID, Confirmed: true}
		}},
		{"spent-input", func(_ *testing.T, f *expiredCancelFixture) {
			f.evidence.outspends[f.input] = &rgb11wallet.BitcoinOutspend{Spent: true, SpendingTx: "other"}
		}},
		{"evidence-error", func(_ *testing.T, f *expiredCancelFixture) {
			f.evidence.outspendErr[f.input] = errors.New("evidence unavailable")
		}},
		{"missing-utxo", func(_ *testing.T, f *expiredCancelFixture) {
			delete(f.evidence.utxos, f.input)
		}},
		{"reservation-owner-mismatch", func(t *testing.T, f *expiredCancelFixture) {
			if err := f.manager.utxoLockerL1.ReleaseReservation([]string{f.input}, "expired-reservation"); err != nil {
				t.Fatal(err)
			}
			if err := f.manager.utxoLockerL1.TryReserve([]string{f.input}, rgb11wallet.LockReasonPending, "other-owner"); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExpiredCancelFixture(t, 1)
			test.mutate(t, fixture)
			if err := fixture.manager.CancelExpiredRGB11Transfer(fixture.transferIDs[0]); err == nil {
				t.Fatal("unsafe cancellation succeeded")
			}
			pending, err := fixture.manager.rgbManager.projectionStore.LoadPendingTransfer(fixture.transferIDs[0])
			if err != nil {
				t.Fatal(err)
			}
			if pending.State.Status != "prepared" {
				t.Fatalf("failed cancellation changed state to %s", pending.State.Status)
			}
		})
	}
}

func TestCancelExpiredRGB11TransferRejectsPartialUnexpiredBatch(t *testing.T) {
	fixture := newExpiredCancelFixture(t, 2)
	pending, err := fixture.manager.rgbManager.projectionStore.LoadPendingTransfer(fixture.transferIDs[1])
	if err != nil {
		t.Fatal(err)
	}
	pending.State.Expiry = time.Now().Add(time.Hour).Unix()
	pending.State.RelayExpiry = pending.State.Expiry
	if err := fixture.manager.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
		t.Fatal(err)
	}
	if err := fixture.manager.CancelExpiredRGB11Transfer(fixture.transferIDs[0]); err == nil {
		t.Fatal("partially unexpired batch was cancelled")
	}
	if lock := fixture.manager.utxoLockerL1.GetLockedUtxoList()[fixture.input]; lock == nil {
		t.Fatal("partial expiry released reservation")
	}
}

func TestCancelExpiredRGB11TransferRejectsDamagedTerminalTombstone(t *testing.T) {
	fixture := newExpiredCancelFixture(t, 1)
	if err := fixture.manager.CancelExpiredRGB11Transfer(fixture.transferIDs[0]); err != nil {
		t.Fatal(err)
	}
	pending, err := fixture.manager.rgbManager.projectionStore.LoadPendingTransfer(fixture.transferIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	pending.State.AckStatus = "awaiting"
	if err := fixture.manager.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
		t.Fatal(err)
	}
	if err := fixture.manager.CancelExpiredRGB11Transfer(fixture.transferIDs[0]); !errors.Is(err, ErrRGB11Inconsistent) {
		t.Fatalf("damaged terminal tombstone was accepted: %v", err)
	}
}

type failFlushDB struct{ indexer.KVDB }
type failFlushBatch struct{ indexer.WriteBatch }

func (db *failFlushDB) NewWriteBatch() indexer.WriteBatch {
	return &failFlushBatch{WriteBatch: db.KVDB.NewWriteBatch()}
}
func (*failFlushBatch) Flush() error { return errors.New("injected flush failure") }

func TestCancelExpiredRGB11TransferPersistenceFailureKeepsReservation(t *testing.T) {
	fixture := newExpiredCancelFixture(t, 1)
	store := rgb11wallet.NewProjectionStore(&failFlushDB{KVDB: fixture.manager.db}, fixture.manager.utxoLockerL1)
	if err := store.SetScope(rgb11StorageScope(fixture.manager.status.CurrentWallet, fixture.manager.status.CurrentAccount)); err != nil {
		t.Fatal(err)
	}
	fixture.manager.rgbManager.projectionStore = store
	if err := fixture.manager.CancelExpiredRGB11Transfer(fixture.transferIDs[0]); err == nil {
		t.Fatal("expected persistence failure")
	}
	pending, err := store.LoadPendingTransfer(fixture.transferIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if pending.State.Status != "prepared" {
		t.Fatalf("terminal state leaked despite failed flush: %s", pending.State.Status)
	}
	lock := fixture.manager.utxoLockerL1.GetLockedUtxoList()[fixture.input]
	if lock == nil || lock.ReservationID != "expired-reservation" {
		t.Fatalf("reservation released after failed persistence: %+v", lock)
	}
}

func TestCancelExpiredRGB11TransferSerializesRGB11Operations(t *testing.T) {
	fixture := newExpiredCancelFixture(t, 1)
	fixture.evidence.statusEntered = make(chan struct{}, 1)
	fixture.evidence.statusRelease = make(chan struct{})
	cancelDone := make(chan error, 1)
	go func() { cancelDone <- fixture.manager.CancelExpiredRGB11Transfer(fixture.transferIDs[0]) }()
	select {
	case <-fixture.evidence.statusEntered:
	case <-time.After(time.Second):
		t.Fatal("cancel did not enter evidence check")
	}
	readerAcquired := make(chan struct{})
	go func() {
		release := fixture.manager.beginRGB11Operation()
		close(readerAcquired)
		release()
	}()
	select {
	case <-readerAcquired:
		t.Fatal("ordinary RGB11 operation entered during cancellation")
	case <-time.After(25 * time.Millisecond):
	}
	close(fixture.evidence.statusRelease)
	if err := <-cancelDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-readerAcquired:
	case <-time.After(time.Second):
		t.Fatal("ordinary RGB11 operation did not resume")
	}
}

func TestRefreshRGB11StateDetectsLateExpiredWitnessWithoutSignedTx(t *testing.T) {
	fixture := newExpiredCancelFixture(t, 1)
	if err := fixture.manager.CancelExpiredRGB11Transfer(fixture.transferIDs[0]); err != nil {
		t.Fatal(err)
	}
	assertExpiredTombstone(t, fixture)
	fixture.evidence.status = &rgb11wallet.BitcoinTxStatus{TxID: fixture.witnessTxID, InMempool: true}
	result, err := fixture.manager.rgbManager.RefreshRGB11State(context.Background())
	if !errors.Is(err, ErrRGB11Inconsistent) || result == nil || result.Conflicted != 1 {
		t.Fatalf("late witness was not detected: result=%+v err=%v", result, err)
	}
	pending, loadErr := fixture.manager.rgbManager.projectionStore.LoadPendingTransfer(fixture.transferIDs[0])
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if pending.State.Status != "conflicted" || pending.State.AckStatus != "invalidated" || len(pending.SignedTx) != 0 {
		t.Fatalf("unexpected late witness tombstone: %+v", pending.State)
	}
}
