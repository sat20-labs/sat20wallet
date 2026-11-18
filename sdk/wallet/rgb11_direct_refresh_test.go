package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"

	"github.com/stretchr/testify/require"
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
	message := &accountMessage{Sender: senderID, Recipient: receiverID,
		ID: second.State.AddressMessageID, Kind: AccountMessageKindRGB11Consignment,
		Body: second.RecipientConsignment, Receipt: accountMessageReceipt{Key: key, IssueHeight: 1}}
	if _, _, err := recipient.rgbManager.acceptRGBMessage(ctx, message,
		func(string, string, RGB11AddressACK) (*accountMessageReceipt, error) {
			return &accountMessageReceipt{}, nil
		}); err != nil {
		t.Fatalf("accept second confirmed Direct: %v", err)
	}

	if lock := recipient.utxoLockerL1.GetLockedUtxoList()[allocation.OutPoint]; lock == nil || lock.Reason != sdkcommon.UtxoLockRGB ||
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
	// This successor is genuinely prepared and unbroadcast. Refresh must keep
	// its reservation while the confirmed parent proof remains chain-derived.

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
			lock.Reason != sdkcommon.UtxoLockRGBPending {
			t.Fatalf("successor reservation changed: %+v", lock)
		}
		stored, err := sender.rgbManager.projectionStore.LoadPendingTransfer(next.State.TransferID)
		if err != nil || stored.State.Status != "prepared" {
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
				if proof.Status != "settled" {
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
		item := indexer.NewTxOutput(output.Value)
		item.OutPointStr, item.OutValue.PkScript = outpoint, output.PkScript
		rpc.outputs[outpoint] = item
	}
	for outpoint, output := range rpc.outputs {
		if !bytes.Equal(output.OutValue.PkScript, senderScript) {
			continue
		}
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

// The Bitcoin evidence is shared by the two wallets. Only the receiver is
// instrumented, after mining and before it opens its mailbox.
type directReadProbe struct {
	rgb11wallet.BitcoinEvidenceProvider
	sibling            string
	tips, siblingReads atomic.Int64
}

func (p *directReadProbe) GetTip() (*rgb11wallet.BitcoinTip, error) {
	height := p.tips.Add(1)
	return &rgb11wallet.BitcoinTip{Height: height, BlockHash: fmt.Sprintf("%064x", height)}, nil
}
func (p *directReadProbe) GetOutspend(point string) (*rgb11wallet.BitcoinOutspend, error) {
	if point == p.sibling {
		p.siblingReads.Add(1)
	}
	return p.BitcoinEvidenceProvider.GetOutspend(point)
}
func (p *directReadProbe) GetUTXO(point string) (*rgb11wallet.BitcoinUTXO, error) {
	if point == p.sibling {
		p.siblingReads.Add(1)
	}
	return p.BitcoinEvidenceProvider.GetUTXO(point)
}

type directOutputProbe struct {
	*rgb11FlowIndexer
	mu     sync.Mutex
	points []string
}

func (p *directOutputProbe) GetTxOutput(point string) (*TxOutput, error) {
	p.mu.Lock()
	p.points = append(p.points, point)
	p.mu.Unlock()
	return p.rgb11FlowIndexer.GetTxOutput(point)
}

func TestDirectSpentSibling(t *testing.T) {
	sender, recipient, asset, evidence, rpc := rgb11SendFixture(t, 10000, 2)
	ctx := context.Background()
	recipient.rgbManager.scopeStates.stopReconciliations()
	sender.rgbManager.scopeStates.stopReconciliations()
	_, err := sender.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
	require.NoError(t, err)
	recipient.StopDKVSSync()
	req := RGB11AddressSendRequest{ReceiverAddress: recipient.wallet.GetAddress(), AssetName: asset.AssetName, AmountRaw: "1025", FeeRate: 2, MinConfirmations: 1}
	other := req
	other.ReceiverAddress = sender.wallet.GetAddress()
	other.AmountRaw = "500"
	prepared, err := sender.rgbManager.prepareRGB11AddressBatch(ctx, []RGB11AddressSendRequest{req, other}, dkvsindexer.RecordVerificationOptions{})
	require.NoError(t, err)
	first, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.States[0].TransferID)
	require.NoError(t, err)
	_, err = sender.rgbManager.deliverRGBAddress(first.State.TransferID)
	require.NoError(t, err)
	parent, err := decodeRGB11BroadcastTx(first.SignedTx)
	require.NoError(t, err)
	// Publish a signed fixture transaction while the receiver has not read its mail.
	prev := txscript.NewMultiPrevOutFetcher(nil)
	for _, in := range parent.TxIn {
		out, err := rpc.GetTxOutput(in.PreviousOutPoint.String())
		require.NoError(t, err)
		prev.AddPrevOut(in.PreviousOutPoint, out.TxOut())
	}
	parent, err = SignTxWithWallet(sender.wallet, parent, prev)
	require.NoError(t, err)
	var raw bytes.Buffer
	require.NoError(t, parent.Serialize(&raw))
	require.NoError(t, acceptRGB11FlowTx(evidence, rpc, raw.Bytes(), parent.TxID()))
	evidence.setStatus(parent.TxID(), rgb11wallet.BitcoinTxStatus{TxID: parent.TxID(), Confirmed: true, Confirmations: 2})
	own := fmt.Sprintf("%s:%d", parent.TxID(), first.State.RecipientVout)
	sibling := wire.OutPoint{Hash: parent.TxHash(), Index: prepared.States[1].RecipientVout}
	require.NotEqual(t, own, sibling.String())
	change := parent.TxOut[sibling.Index]
	child := wire.NewMsgTx(2)
	child.AddTxIn(wire.NewTxIn(&sibling, nil, nil))
	feePoint, err := wire.NewOutPointFromString(seedRGB11FlowEvidenceUTXO(t, evidence.rgb11FlowEvidence, change.PkScript, 10000, 83))
	require.NoError(t, err)
	child.AddTxIn(wire.NewTxIn(feePoint, nil, nil))
	child.AddTxOut(wire.NewTxOut(change.Value+10000-500, change.PkScript))
	childPrev := txscript.NewMultiPrevOutFetcher(nil)
	childPrev.AddPrevOut(sibling, change)
	childPrev.AddPrevOut(*feePoint, wire.NewTxOut(10000, change.PkScript))
	child, err = SignTxWithWallet(sender.wallet, child, childPrev)
	require.NoError(t, err)
	raw.Reset()
	require.NoError(t, child.Serialize(&raw))
	require.NoError(t, acceptRGB11FlowTx(evidence, rpc, raw.Bytes(), child.TxID()))
	evidence.setStatus(child.TxID(), rgb11wallet.BitcoinTxStatus{TxID: child.TxID(), Confirmed: true, Confirmations: 1})
	require.Equal(t, child.TxID(), evidence.spendingTx[sibling.String()])
	require.Nil(t, rpc.outputs[sibling.String()])
	require.NotNil(t, rpc.outputs[own])
	evidence.utxos[own].Confirmations = 2
	before, err := recipient.GetRGB11AssetBalance(&asset.AssetName)
	require.NoError(t, err)
	probe := &directReadProbe{BitcoinEvidenceProvider: evidence, sibling: sibling.String()}
	recipient.rgbManager.evidence = probe
	l1 := &directOutputProbe{rgb11FlowIndexer: rpc}
	recipient.SetIndexerHttpClient(l1)
	require.NoError(t, recipient.StartDKVSSync())
	require.Eventually(t, func() bool {
		err := recipient.rgbManager.checkRGB11Account(rgb11ManagedOperationContinue)
		return !errors.Is(err, ErrDKVSPathNotSynced)
	}, time.Minute, 100*time.Millisecond)
	result, err := recipient.SyncConfiguredRGB11AddressMailbox(ctx, dkvsindexer.RecordVerificationOptions{})
	require.NoError(t, err)
	require.Zero(t, result.Invalid, "%v", result.ErrorDetails)
	require.Positive(t, result.Received+result.AlreadyDone)
	after, err := recipient.GetRGB11AssetBalance(&asset.AssetName)
	require.NoError(t, err)
	require.Equal(t, "10.25", after.Sub(before).String())
	recipient.StopDKVSSync()
	l1.mu.Lock()
	points := append([]string(nil), l1.points...)
	l1.mu.Unlock()
	require.NotEmpty(t, points)
	for _, point := range points {
		require.Equal(t, own, point)
	}
	require.Zero(t, probe.siblingReads.Load())
	require.Zero(t, probe.tips.Load())
	t.Logf("offline Direct received %s RGB, own=%s sibling=%s confirmed spender=%s", after.Sub(before), own, sibling.String(), child.TxID())
}
