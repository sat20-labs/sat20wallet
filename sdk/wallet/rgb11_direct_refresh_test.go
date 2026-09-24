package wallet

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func TestRGB11DirectReplayChain(t *testing.T) {
	sender, recipient, imported, evidence, rpc := newRGB11GenericSendFixture(t)
	ctx := context.Background()
	makeDirect := func(amount string) *rgb11wallet.PendingTransfer {
		t.Helper()
		request := RGB11AddressSendRequest{
			ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName,
			AmountRaw: amount, FeeRate: 2, MinConfirmations: 1,
		}
		prepared, err := runRGB11ManagedOperation(sender, ctx, rgb11ManagedOperationNew,
			func(m *rgb11Manager) (*RGB11PreparedTransfer, error) {
				return m.prepareRGB11AddressBatch(ctx, []RGB11AddressSendRequest{request},
					dkvsindexer.RecordVerificationOptions{})
			})
		if err != nil {
			t.Fatal(err)
		}
		pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
		if err != nil {
			t.Fatal(err)
		}
		return confirmRGB11Pending(t, ctx, sender, pending, evidence, rpc)
	}
	first := makeDirect("20000")
	second := makeDirect("10000")

	prepared, err := recipient.rgbManager.confirmedRGB11AddressState(ctx, second.RecipientConsignment)
	if err != nil {
		t.Fatalf("replay second confirmed Direct after %s: %v", first.State.WitnessTxID, err)
	}
	if len(prepared.WitnessTxIDs) != 1 || prepared.WitnessTxIDs[0] != second.State.WitnessTxID {
		t.Fatalf("replay witnesses=%v want=%s", prepared.WitnessTxIDs, second.State.WitnessTxID)
	}
	var oldAllocation, newAllocation *rgb11wallet.ValidatedAllocation
	for index := range prepared.Receipt.Allocations {
		allocation := &prepared.Receipt.Allocations[index]
		switch allocation.WitnessTxID {
		case first.State.WitnessTxID:
			oldAllocation = allocation
		case second.State.WitnessTxID:
			newAllocation = allocation
		}
	}
	if oldAllocation == nil || newAllocation == nil {
		t.Fatalf("replay allocations=%+v", prepared.Receipt.Allocations)
	}
	mainScript, err := AddrToPkScript(recipient.wallet.GetAddress(), GetChainParam())
	if err != nil {
		t.Fatal(err)
	}
	prepared.Receipt.Allocations = []rgb11wallet.ValidatedAllocation{*oldAllocation, *newAllocation}
	prepared.Outputs = map[string]*rgb11wallet.BitcoinUTXO{
		oldAllocation.OutPoint: {OutPoint: oldAllocation.OutPoint, PkScript: mainScript},
		newAllocation.OutPoint: {OutPoint: newAllocation.OutPoint, PkScript: mainScript},
	}
	allocation, witnessTxID, err := recipient.rgbManager.findPreparedRGB11AddressAllocation(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if witnessTxID != second.State.WitnessTxID ||
		allocation.OutPoint != fmt.Sprintf("%s:%d", second.State.WitnessTxID, second.State.RecipientVout) {
		t.Fatalf("replay selected outpoint=%s witness=%s", allocation.OutPoint, witnessTxID)
	}
	senderID, err := dkvsAccountID(sender.wallet)
	if err != nil {
		t.Fatal(err)
	}
	receiverID, err := dkvsAccountID(recipient.wallet)
	if err != nil {
		t.Fatal(err)
	}
	key, err := dkvsindexer.MailMsgKey(receiverID, senderID, second.State.AddressMessageID)
	if err != nil {
		t.Fatal(err)
	}
	record := &swire.DKVSRecord{Version: dkvsindexer.Version, Key: key, Seq: 1, IssueHeight: 1}
	if _, _, err := recipient.rgbManager.acceptRGB11AddressMailboxDecoded(
		ctx, record, second.RecipientConsignment, "direct",
		func(string, string, RGB11AddressACK) (*swire.DKVSRecord, error) {
			return &swire.DKVSRecord{Version: dkvsindexer.Version}, nil
		},
	); err != nil {
		t.Fatalf("accept second confirmed Direct: %v", err)
	}
	if lock := recipient.utxoLockerL1.GetLockedUtxoList()[allocation.OutPoint]; lock == nil || lock.Reason != rgb11wallet.LockReasonRGB ||
		!recipient.rgbManager.isL1SendInputProtected(allocation.OutPoint) {
		t.Fatalf("recovered Direct carrier is selectable: %+v", lock)
	}
}

func TestRGB11RefreshLocalNext(t *testing.T) {
	sender, recipient, imported, evidence, rpc := newRGB11GenericSendFixture(t)
	ctx := context.Background()
	first := confirmRGB11Send(t, ctx, sender, recipient, imported.ContractID, "20000", evidence, rpc)

	request, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID,
		AmountRaw: "10000", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := sender.rgbManager.PrepareRGB11Transfer(ctx, RGB11SendRequest{
		Invoice: request.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	next, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	next.State.Status = "delivered"
	next.State.AckStatus = "accepted"
	next.State.AddressMode = true
	if err := sender.rgbManager.projectionStore.SavePendingTransferState(next); err != nil {
		t.Fatal(err)
	}

	shared := rgb11PendingChangeOutpoints(first)[0]
	if len(next.State.InputOutPoints) != 1 || next.State.InputOutPoints[0] != shared {
		t.Fatalf("successor did not spend first change: %v", next.State.InputOutPoints)
	}
	lock := sender.utxoLockerL1.GetLockedUtxoList()[shared]
	if lock == nil || lock.ReservationID != next.ReservationID {
		t.Fatalf("successor reservation missing: %+v", lock)
	}

	for attempt := 0; attempt < 2; attempt++ {
		if _, err := sender.rgbManager.RefreshRGB11State(ctx); err != nil {
			t.Fatalf("refresh %d: %v", attempt, err)
		}
		lock = sender.utxoLockerL1.GetLockedUtxoList()[shared]
		if lock == nil || lock.ReservationID != next.ReservationID ||
			lock.Reason != rgb11wallet.LockReasonPending {
			t.Fatalf("successor reservation changed: %+v", lock)
		}
		stored, err := sender.rgbManager.projectionStore.LoadPendingTransfer(next.State.TransferID)
		if err != nil || stored.State.Status != "delivered" {
			t.Fatalf("successor state changed: %+v err=%v", stored, err)
		}
		proofs, err := sender.rgbManager.projectionStore.ListProofs()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, proof := range proofs {
			if proof.OutPoint == shared {
				found = true
				if proof.Status != "spending" {
					t.Fatalf("successor input proof=%s", proof.Status)
				}
			}
		}
		if !found {
			t.Fatal("successor input proof missing")
		}
	}
}

func confirmRGB11Send(t *testing.T, ctx context.Context, sender, recipient *Manager,
	contractID, amount string, evidence *rgb11AddressEvidence, rpc *rgb11FlowIndexer,
) *rgb11wallet.PendingTransfer {
	t.Helper()
	request, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: contractID,
		AmountRaw: amount, WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := sender.rgbManager.PrepareRGB11Transfer(ctx, RGB11SendRequest{
		Invoice: request.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	return confirmRGB11Pending(t, ctx, sender, pending, evidence, rpc)
}

func confirmRGB11Pending(t *testing.T, ctx context.Context, sender *Manager,
	pending *rgb11wallet.PendingTransfer, evidence *rgb11AddressEvidence,
	rpc *rgb11FlowIndexer,
) *rgb11wallet.PendingTransfer {
	t.Helper()
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
		t.Fatal(err)
	}
	txid := tx.TxHash().String()
	evidence.mu.Lock()
	evidence.rawTx[txid] = append([]byte(nil), pending.SignedTx...)
	for _, input := range tx.TxIn {
		outpoint := input.PreviousOutPoint.String()
		evidence.spendingTx[outpoint] = txid
		delete(evidence.utxos, outpoint)
		delete(rpc.outputs, outpoint)
	}
	rpc.plain = nil
	senderScript, err := AddrToPkScript(sender.wallet.GetAddress(), GetChainParam())
	if err != nil {
		evidence.mu.Unlock()
		t.Fatal(err)
	}
	for vout, output := range tx.TxOut {
		outpoint := fmt.Sprintf("%s:%d", txid, vout)
		evidence.utxos[outpoint] = &rgb11wallet.BitcoinUTXO{
			OutPoint: outpoint, Value: output.Value, PkScript: output.PkScript, Confirmations: 6,
		}
		if bytes.Equal(output.PkScript, senderScript) {
			item := indexer.NewTxOutput(output.Value)
			item.OutPointStr, item.OutValue.PkScript = outpoint, output.PkScript
			rpc.outputs[outpoint] = item
		}
	}
	for outpoint, output := range rpc.outputs {
		rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{
			OutPoint: outpoint, Value: output.OutValue.Value, PkScript: output.OutValue.PkScript,
		})
	}
	evidence.mu.Unlock()
	status := &rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 6}
	evidence.statusMu.Lock()
	evidence.statuses[txid] = status
	evidence.statusMu.Unlock()
	if err := sender.rgbManager.applyRGB11LocalChange(ctx, pending, status); err != nil {
		t.Fatal(err)
	}
	pending.State.Status, pending.State.AckStatus = "settled", "accepted"
	if err := sender.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
		t.Fatal(err)
	}
	if err := sender.rgbManager.finalizeRGB11PendingChangeReservation(pending); err != nil {
		t.Fatal(err)
	}
	return pending
}
