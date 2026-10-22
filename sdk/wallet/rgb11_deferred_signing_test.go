package wallet

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/txscript"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// Count real Bitcoin signatures while keeping the real derivation/signing code.
// Message authentication signatures are deliberately outside this counter.
type rgb11CountingSigner struct {
	*InternalWallet
	calls *atomic.Int64
}

// Expiry only cancels an undelivered, unsigned preparation. It cannot manufacture
// a late chain transaction; the canceled ID must stay unusable after inputs free.
func TestRGB11UnsignedExpiredCancelCannotResumeAndReleasesInputs(t *testing.T) {
	sender, recipient, imported, evidence, _ := newRGB11GenericSendFixture(t)
	calls := countRGB11Signatures(t, sender)
	expiry := time.Now().Add(10 * time.Second).Unix()
	request := RGB11AddressSendRequest{ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName,
		AmountRaw: "20000", FeeRate: 2, MinConfirmations: 1, Expiry: expiry}
	prepared, _, err := sender.rgbManager.PrepareConfiguredRGB11AddressTransfer(context.Background(), request, dkvsindexer.RecordVerificationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	original := assertRGB11UnsignedPending(t, sender.rgbManager, prepared.State.TransferID)
	// This fixture reports unknown IDs as RPC errors by default. Model the
	// successful negative status lookup required by the cancellation contract.
	evidence.setStatus(prepared.TxID, rgb11wallet.BitcoinTxStatus{TxID: prepared.TxID})
	if calls.Load() != 0 {
		t.Fatalf("preparation signed %d times", calls.Load())
	}
	sender.rgbManager.wallet = sender.rgbManager.wallet.(*rgb11CountingSigner).InternalWallet
	if err := sender.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	coreAssertRGB11Reservation(t, sender, original.State.TransferID, true)
	if delay := time.Until(time.Unix(expiry, 0)); delay > 0 {
		time.Sleep(delay)
	}
	if err := sender.CancelExpiredRGB11Transfer(original.State.TransferID); err != nil {
		t.Fatal(err)
	}
	coreAssertRGB11Reservation(t, sender, original.State.TransferID, false)
	delivery, err := sender.DeliverAndBroadcastConfiguredRGB11AddressTransfer(original.State.TransferID, RGB11AddressDeliveryOptions{})
	if err != nil || delivery == nil || delivery.Broadcast || delivery.TxID != "" || delivery.RecordKey != "" || delivery.RecordHash != "" || delivery.ObjectID != "" {
		t.Fatalf("canceled preparation performed delivery/broadcast: result=%+v err=%v", delivery, err)
	}
	if _, err := sender.rgbManager.deliverRGB11AddressTransferStore(mustRGB11ConfiguredStore(t, sender), original.State.TransferID, RGB11AddressDeliveryOptions{}); !errors.Is(err, ErrRGB11AddressDeliveryRequired) {
		t.Fatalf("canceled preparation reached direct delivery: %v", err)
	}
	if _, err := sender.BroadcastRGB11AddressTransfer(original.State.TransferID); !errors.Is(err, ErrRGB11AddressDeliveryRequired) {
		t.Fatalf("canceled preparation broadcast: %v", err)
	}
	canceled, err := sender.rgbManager.projectionStore.LoadPendingTransfer(original.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if canceled.State.Status != "rejected" || canceled.State.AckStatus != "rejected" ||
		canceled.State.RejectReason != rgb11RejectReasonInvoiceExpired ||
		len(canceled.SignedTx) != 0 || len(canceled.SignedPSBT) != 0 || len(canceled.RecipientConsignment) != 0 || len(canceled.LocalConsignment) != 0 {
		t.Fatal("canceled transfer became resumable or retained private transaction material")
	}
	request.Expiry = time.Now().Add(time.Hour).Unix()
	next, _, err := sender.PrepareConfiguredRGB11AddressTransfer(context.Background(), request, dkvsindexer.RecordVerificationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if next.State.TransferID == original.State.TransferID || !overlapsRGB11Outpoints(next.State.InputOutPoints, original.State.InputOutPoints) {
		t.Fatal("cancellation did not free original inputs for a new distinct preparation")
	}
	assertRGB11UnsignedPending(t, sender.rgbManager, next.State.TransferID)
	evidence.mu.Lock()
	broadcasts := evidence.broadcastCount
	evidence.mu.Unlock()
	if broadcasts != 0 {
		t.Fatalf("cancel/resume/new preparation reached broadcast %d times", broadcasts)
	}
	mailbox, err := recipient.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
	if err != nil || mailbox == nil || mailbox.Received != 0 || mailbox.ACKs != 0 || mailbox.Invalid != 0 {
		t.Fatalf("canceled or unshared preparation leaked to recipient mailbox: result=%+v err=%v", mailbox, err)
	}
}

// Reuse actual signing, mailbox ACKs and chain recording. Only the RPC response
// and initial transaction-status/output visibility are lost at that boundary.
type rgb11LostBroadcastResponseEvidence struct {
	*rgb11ChannelRecoveryEvidence
	txID   string
	hidden atomic.Bool
}

func (e *rgb11LostBroadcastResponseEvidence) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	if txid == e.txID && e.hidden.Load() {
		return &rgb11wallet.BitcoinTxStatus{TxID: txid}, nil
	}
	return e.rgb11ChannelRecoveryEvidence.GetTxStatus(txid)
}

func (e *rgb11LostBroadcastResponseEvidence) GetUTXO(outpoint string) (*rgb11wallet.BitcoinUTXO, error) {
	if e.hidden.Load() && strings.HasPrefix(outpoint, e.txID+":") {
		return nil, errors.New("broadcast outputs are not visible yet")
	}
	return e.rgb11ChannelRecoveryEvidence.GetUTXO(outpoint)
}

func (e *rgb11LostBroadcastResponseEvidence) Broadcast(raw []byte) (string, error) {
	txID, err := e.rgb11ChannelRecoveryEvidence.Broadcast(raw)
	if err != nil {
		return "", err
	}
	tx, err := decodeRGB11BroadcastTx(raw)
	if err != nil {
		return "", err
	}
	if err := e.recordSignedBroadcast(txID, tx); err != nil {
		return "", err
	}
	return "", context.DeadlineExceeded
}

func TestRGB11DirectBroadcastTimeoutCannotCancelAndResolvesWithoutRebroadcast(t *testing.T) {
	sender, recipient, imported, base, _ := newRGB11GenericSendFixture(t)
	expiry := time.Now().Add(15 * time.Second).Unix()
	prepared, _, err := sender.PrepareConfiguredRGB11AddressTransfer(context.Background(), RGB11AddressSendRequest{
		ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName,
		AmountRaw: "20000", FeeRate: 2, MinConfirmations: 1, Expiry: expiry,
	}, dkvsindexer.RecordVerificationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	original := assertRGB11UnsignedPending(t, sender.rgbManager, prepared.State.TransferID)
	delivered, err := sender.DeliverAndBroadcastConfiguredRGB11AddressTransfer(original.State.TransferID, RGB11AddressDeliveryOptions{})
	if err != nil || delivered == nil || !delivered.AwaitingACK || delivered.Broadcast {
		t.Fatalf("delivery before ACK: result=%+v err=%v", delivered, err)
	}
	received, err := recipient.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
	if err != nil || received == nil || received.Received != 1 || received.Invalid != 0 {
		t.Fatalf("real receiver ACK: result=%+v err=%v", received, err)
	}
	evidence := &rgb11LostBroadcastResponseEvidence{rgb11ChannelRecoveryEvidence: &rgb11ChannelRecoveryEvidence{rgb11AddressEvidence: base}, txID: prepared.TxID}
	evidence.hidden.Store(true)
	sender.rgbManager.evidence = evidence
	// Drive reconciliation explicitly so it cannot race the deliberately hidden
	// transaction status while this test exercises cancellation after timeout.
	sender.rgbManager.scopeStates = nil
	ack, err := sender.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
	if !errors.Is(err, ErrRGB11BroadcastResultUnknown) || !errors.Is(err, context.DeadlineExceeded) || ack == nil || ack.ACKs != 1 || ack.Invalid != 0 {
		t.Fatalf("ACK/broadcast lost response: result=%+v err=%v", ack, err)
	}
	uncertain, err := sender.rgbManager.projectionStore.LoadPendingTransfer(original.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if uncertain.State.Status != rgb11StatusBroadcastAttempted || !validRGB11SigningTransition(original, uncertain) || evidence.calls() != 1 {
		t.Fatal("timeout did not retain exactly one valid signed broadcast attempt")
	}
	coreAssertRGB11Reservation(t, sender, original.State.TransferID, true)
	if delay := time.Until(time.Unix(expiry, 0)); delay > 0 {
		time.Sleep(delay)
	}
	if err := sender.CancelExpiredRGB11Transfer(original.State.TransferID); !errors.Is(err, ErrRGB11ExpiredCancel) {
		t.Fatalf("expired broadcast attempt cancellation: %v", err)
	}
	coreAssertRGB11Reservation(t, sender, original.State.TransferID, true)
	afterCancel, err := sender.rgbManager.projectionStore.LoadPendingTransfer(original.State.TransferID)
	if err != nil || afterCancel.State.Status != rgb11StatusBroadcastAttempted || !sameRGB11BroadcastIntent(uncertain, afterCancel) {
		t.Fatalf("rejected cancellation changed the signed intent: %v", err)
	}
	evidence.hidden.Store(false)
	for attempt := 0; attempt < 2; attempt++ {
		txID, err := sender.BroadcastRGB11AddressTransfer(original.State.TransferID)
		if err != nil || txID != prepared.TxID {
			t.Fatalf("resolve original timed-out transaction: txid=%s err=%v", txID, err)
		}
	}
	recovered, err := sender.rgbManager.projectionStore.LoadPendingTransfer(original.State.TransferID)
	if err != nil || !rgb11BroadcastCompleteStatus(recovered.State.Status) || !sameRGB11BroadcastIntent(uncertain, recovered) || evidence.calls() != 1 {
		t.Fatalf("late visibility replaced or rebroadcast the signed intent: calls=%d err=%v", evidence.calls(), err)
	}
	base.mu.Lock()
	broadcasts, raw := base.broadcastCount, append([]byte(nil), base.broadcasted...)
	base.mu.Unlock()
	if broadcasts != 1 || !bytes.Equal(raw, recovered.SignedTx) {
		t.Fatal("recovery did not retain the one transaction actually submitted to the backend")
	}
}

func (w *rgb11CountingSigner) Clone() sdkcommon.Wallet {
	return &rgb11CountingSigner{InternalWallet: w.InternalWallet.Clone().(*InternalWallet), calls: w.calls}
}
func (w *rgb11CountingSigner) CloneByPubKey(pub []byte) sdkcommon.Wallet {
	return &rgb11CountingSigner{InternalWallet: w.InternalWallet.CloneByPubKey(pub).(*InternalWallet), calls: w.calls}
}
func (w *rgb11CountingSigner) SignPsbt(packet *psbt.Packet) error {
	w.calls.Add(1)
	return w.InternalWallet.SignPsbt(packet)
}
func (w *rgb11CountingSigner) SignRGB11Psbt(packet *psbt.Packet, keys map[int]RGB11InputSigningKey) error {
	w.calls.Add(1)
	return w.InternalWallet.SignRGB11Psbt(packet, keys)
}
func countRGB11Signatures(t *testing.T, manager *Manager) *atomic.Int64 {
	t.Helper()
	counter := &atomic.Int64{}
	// Keep the account-recovery root native. The existing RGB scope has its
	// own wallet clone and still delegates recovery/CAS to that original root.
	scoped, err := manager.rootRGB11Manager()
	if err != nil {
		t.Fatal(err)
	}
	scoped.wallet = &rgb11CountingSigner{InternalWallet: scoped.wallet.(*InternalWallet), calls: counter}
	manager.rgbManager = scoped
	return counter
}
func assertRGB11UnsignedPending(t *testing.T, runtime *rgb11Manager, transferID string) *rgb11wallet.PendingTransfer {
	t.Helper()
	pending, err := runtime.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := decodeRGB11BroadcastTx(pending.SignedTx)
	if err != nil {
		t.Fatal(err)
	}
	if tx.HasWitness() {
		t.Fatal("pre-broadcast pending already contains Bitcoin signatures")
	}
	packet, err := psbt.NewFromRawBytes(bytes.NewReader(pending.SignedPSBT), false)
	if err != nil {
		t.Fatal(err)
	}
	if !sameRGB11UnsignedTx(tx, packet.UnsignedTx) {
		t.Fatal("prepared PSBT changed transaction body")
	}
	for _, input := range packet.Inputs {
		if len(input.PartialSigs) != 0 || len(input.TaprootKeySpendSig) != 0 || len(input.TaprootScriptSpendSig) != 0 || len(input.FinalScriptSig) != 0 || len(input.FinalScriptWitness) != 0 {
			t.Fatal("pre-broadcast PSBT contains a Bitcoin signature")
		}
	}
	return pending
}
func TestRGB11OrdinaryBatchSignsOnlyAfterEveryACK(t *testing.T) {
	sender, recipient, imported, evidence, _ := newRGB11GenericSendFixture(t)
	calls := countRGB11Signatures(t, sender)
	request := RGB11AddressSendRequest{ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName, AmountRaw: "10000", FeeRate: 2, MinConfirmations: 1}
	prepared, err := sender.rgbManager.prepareRGB11AddressBatch(context.Background(), []RGB11AddressSendRequest{request, request}, dkvsindexer.RecordVerificationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatalf("prepare made %d Bitcoin signing calls", calls.Load())
	}
	// Direct-message encryption intentionally requires the native wallet type.
	// Remove only the counter after proving zero prepare signatures; retain
	// real transport/ACK processing and inspect actual witness bytes below.
	sender.rgbManager.wallet = sender.rgbManager.wallet.(*rgb11CountingSigner).InternalWallet
	original := assertRGB11UnsignedPending(t, sender.rgbManager, prepared.States[0].TransferID)
	for i, state := range prepared.States {
		if _, err := sender.rgbManager.deliverRGB11AddressTransferStore(mustRGB11ConfiguredStore(t, sender), state.TransferID, RGB11AddressDeliveryOptions{}); err != nil {
			t.Fatal(err)
		}
		result, err := recipient.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
		if err != nil || result == nil || result.Received != 1 || result.Invalid != 0 {
			t.Fatalf("receive output %d: result=%+v err=%v", i, result, err)
		}
		assertRGB11UnsignedPending(t, sender.rgbManager, state.TransferID)
		if _, err := sender.rgbManager.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			assertRGB11UnsignedPending(t, sender.rgbManager, state.TransferID)
			evidence.mu.Lock()
			broadcasted := evidence.broadcastCount
			evidence.mu.Unlock()
			if broadcasted != 0 {
				t.Fatal("partial ACK broadcast the batch")
			}
		}
	}
	signed, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.States[0].TransferID)
	if err != nil {
		t.Fatal(err)
	}
	unsignedTx, err := decodeRGB11BroadcastTx(original.SignedTx)
	if err != nil {
		t.Fatal(err)
	}
	signedTx, err := decodeRGB11BroadcastTx(signed.SignedTx)
	if err != nil {
		t.Fatal(err)
	}
	if !sameRGB11UnsignedTx(unsignedTx, signedTx) || !signedTx.HasWitness() || signed.State.ConsignmentHash != original.State.ConsignmentHash || !bytes.Equal(original.SignedPSBT, signed.SignedPSBT) {
		t.Fatal("signing changed the committed transaction, proof or prepared PSBT")
	}
	packet, err := psbt.NewFromRawBytes(bytes.NewReader(original.SignedPSBT), false)
	if err != nil {
		t.Fatal(err)
	}
	prev := txscript.NewMultiPrevOutFetcher(nil)
	for i, input := range packet.UnsignedTx.TxIn {
		prev.AddPrevOut(input.PreviousOutPoint, packet.Inputs[i].WitnessUtxo)
	}
	if err := VerifySignedTx(signedTx, prev); err != nil {
		t.Fatalf("broadcast signature invalid: %v", err)
	}
	if !validRGB11SigningTransition(original, signed) {
		t.Fatal("strict broadcast guard rejected the real ordinary signing transition")
	}
	tampered := cloneRGB11BroadcastIntent(signed)
	badWitness := signedTx.Copy()
	badWitness.TxIn[0].Witness[0] = append([]byte(nil), badWitness.TxIn[0].Witness[0]...)
	badWitness.TxIn[0].Witness[0][0] ^= 1
	var badRaw bytes.Buffer
	if err := badWitness.Serialize(&badRaw); err != nil {
		t.Fatal(err)
	}
	tampered.SignedTx = badRaw.Bytes()
	if validRGB11SigningTransition(original, tampered) {
		t.Fatal("equal txid admitted an invalid Bitcoin signature")
	}
	tampered = cloneRGB11BroadcastIntent(signed)
	tampered.SignedPSBT = append(tampered.SignedPSBT, 0)
	if validRGB11SigningTransition(original, tampered) {
		t.Fatal("signing changed the authenticated prepared PSBT")
	}
	if validRGB11SigningTransition(signed, signed) {
		t.Fatal("an already signed witness was admitted as a new signing transition")
	}

	evidence.mu.Lock()
	broadcasted := evidence.broadcastCount
	evidence.mu.Unlock()
	if broadcasted != 1 || !rgb11BroadcastCompleteStatus(signed.State.Status) {
		t.Fatal("all ACKs did not broadcast exactly one valid transaction")
	}
}

func TestRGB11OrdinaryRecoversObservedWitnessWithoutSigningOrBroadcast(t *testing.T) {
	for _, phase := range []string{rgb11StatusBroadcastAttempted, "broadcast"} {
		t.Run(phase, func(t *testing.T) {
			fixture := newRGB11BroadcastFixture(t)
			runtime := fixture.manager.rgbManager
			runtime.scopeStates = nil
			pending, err := runtime.projectionStore.LoadPendingTransfer(fixture.transferIDs[0])
			if err != nil {
				t.Fatal(err)
			}
			// The chain has the real signed witness while this device retains
			// only the exact unsigned preparation and its original PSBT.
			signedRaw := append([]byte(nil), pending.SignedTx...)
			packet, err := psbt.NewFromRawBytes(bytes.NewReader(pending.SignedPSBT), false)
			if err != nil {
				t.Fatal(err)
			}
			var unsigned bytes.Buffer
			if err := packet.UnsignedTx.Serialize(&unsigned); err != nil {
				t.Fatal(err)
			}
			pending.SignedTx, pending.State.Status = unsigned.Bytes(), phase
			if err := runtime.projectionStore.SavePendingTransferState(pending); err != nil {
				t.Fatal(err)
			}
			prepared := assertRGB11UnsignedPending(t, runtime, pending.State.TransferID)
			calls := &atomic.Int64{}
			runtime.wallet = &rgb11CountingSigner{InternalWallet: runtime.wallet.(*InternalWallet), calls: calls}
			evidence := &controlledRGB11BroadcastEvidence{BitcoinEvidenceProvider: expectedSpendEvidence{
				status: &rgb11wallet.BitcoinTxStatus{TxID: pending.State.WitnessTxID, InMempool: true}, raw: signedRaw,
			}}
			runtime.evidence = evidence
			for attempt := 0; attempt < 2; attempt++ {
				current, err := runtime.projectionStore.LoadPendingTransfer(pending.State.TransferID)
				if err != nil {
					t.Fatal(err)
				}
				txID, err := runtime.broadcastRGB11PendingBatch([]*rgb11wallet.PendingTransfer{current}, nil)
				if err != nil || txID != pending.State.WitnessTxID {
					t.Fatalf("recover observed witness: txid=%s err=%v", txID, err)
				}
			}
			recovered, err := runtime.projectionStore.LoadPendingTransfer(pending.State.TransferID)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(recovered.SignedTx, signedRaw) || !bytes.Equal(recovered.SignedPSBT, prepared.SignedPSBT) ||
				!validRGB11SigningTransition(prepared, recovered) || !rgb11BroadcastCompleteStatus(recovered.State.Status) {
				t.Fatal("chain recovery did not preserve the exact valid signed preparation")
			}
			if calls.Load() != 0 || evidence.calls() != 0 {
				t.Fatalf("observed witness was signed or broadcast again: signatures=%d broadcasts=%d", calls.Load(), evidence.calls())
			}
		})
	}
}
