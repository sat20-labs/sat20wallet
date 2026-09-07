package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	coreissuance "github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/schemas"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"os"
	"testing"
	"time"
)

func newRGB11GenericSendFixture(t *testing.T) (*Manager, *Manager, *RGB11ImportResult, *rgb11AddressEvidence, *rgb11FlowIndexer) {
	t.Helper()
	senderWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	recipientWallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params,
	)
	senderScript, err := AddrToPkScript(senderWallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}

	const sourceOutpoint = "14295d5bb1a191cdb6286dc0944df938421e3dfcbf0811353ccac4100c2068c5:1"
	const plainOutpoint = "3333333333333333333333333333333333333333333333333333333333333333:0"
	baseEvidence := &rgb11FlowEvidence{
		utxos: map[string]*rgb11wallet.BitcoinUTXO{
			sourceOutpoint: {OutPoint: sourceOutpoint, Value: 10_000, PkScript: senderScript, Confirmations: 6},
		},
		rawTx:      make(map[string][]byte),
		spendingTx: make(map[string]string),
	}
	evidence := &rgb11AddressEvidence{rgb11FlowEvidence: baseEvidence, statuses: make(map[string]*rgb11wallet.BitcoinTxStatus)}
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	sourceOutput := indexer.NewTxOutput(10_000)
	sourceOutput.OutPointStr = sourceOutpoint
	sourceOutput.OutValue.PkScript = senderScript
	rpc.outputs[sourceOutpoint] = sourceOutput
	plainOutput := indexer.NewTxOutput(100_000)
	plainOutput.OutPointStr = plainOutpoint
	plainOutput.OutValue.PkScript = senderScript
	rpc.outputs[plainOutpoint] = plainOutput
	rpc.plain = []*indexerwire.TxOutputInfo{
		{OutPoint: sourceOutpoint, Value: 10_000, PkScript: senderScript},
		{OutPoint: plainOutpoint, Value: 100_000, PkScript: senderScript},
	}

	sender := newRGB11FlowManager(t, senderWallet, rpc, evidence, 101)
	recipient := newRGB11FlowManager(t, recipientWallet, rpc, evidence, 102)
	contract, err := os.ReadFile("../../../rgb11/testvectors/rc11/nia-example.rgba")
	if err != nil {
		t.Fatal(err)
	}
	imported, err := sender.ImportRGB11Contract(context.Background(), contract)
	if err != nil {
		t.Fatal(err)
	}
	remote := newRGB11MemoryDKVSHTTP()
	messageClient := newRGB11MessageNodeClient(remote)
	configure := func(manager *Manager) {
		manager.cfg = &sdkcommon.Config{
			Env: "test", Chain: "testnet",
			IndexerL2: &sdkcommon.Indexer{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"},
		}
		manager.http = remote
		manager.serverNode = NewNode(
			messageClient, "message.test", SERVER_NODE,
			messageClient.CoreNodePubKey(), messageClient.CoreNodePubKey(),
		)
	}
	configure(sender)
	configure(recipient)

	endpoint, err := recipient.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{RecordOptions: dkvsindexer.RecordOptions{TTL: testRGB11FreeLocalTTL}})
	if err != nil {
		t.Fatal(err)
	}
	target, err := mailboxSubscriptionTarget(endpoint.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if err := recipient.SubscribeDKVSPrefix(target); err != nil {
		t.Fatal(err)
	}
	return sender, recipient, imported, evidence, rpc
}

func TestRGB11DirectBatchRequiresEveryOutputACK(t *testing.T) {
	sender, recipient, imported, evidence, _ := newRGB11GenericSendFixture(t)
	request := RGB11AddressSendRequest{ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName, AmountRaw: "20000", FeeRate: 2, MinConfirmations: 1}
	prepared, err := runRGB11ManagedOperation(sender, context.Background(), rgb11ManagedOperationNew, func(m *rgb11Manager) (*RGB11PreparedTransfer, error) {
		return m.prepareRGB11AddressBatch(context.Background(), []RGB11AddressSendRequest{request, request}, dkvsindexer.RecordVerificationOptions{})
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.States) != 2 || prepared.States[0].TransferID == prepared.States[1].TransferID {
		t.Fatal("batch output identity lost")
	}
	for i, state := range prepared.States {
		_, err := sender.rgbManager.deliverRGB11AddressTransferStore(mustRGB11ConfiguredStore(t, sender), state.TransferID, RGB11AddressDeliveryOptions{})
		if err != nil {
			t.Fatal(err)
		}
		syncResult, err := recipient.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
		if err != nil || syncResult.Invalid != 0 || syncResult.Received != 1 {
			t.Fatalf("output %d receive=%+v err=%v", i, syncResult, err)
		}
		_, err = sender.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if _, err := sender.BroadcastRGB11AddressTransfer(state.TransferID); !errors.Is(err, ErrRGB11AddressDeliveryRequired) {
				t.Fatalf("partial ACK allowed broadcast: %v", err)
			}
			evidence.mu.Lock()
			sent := len(evidence.broadcasted) != 0
			evidence.mu.Unlock()
			if sent {
				t.Fatal("broadcast before every recipient persisted")
			}
		}
	}
	// A delivered recovery snapshot omits the PSBT. Resuming the same
	// transaction must still work after importing that persisted state.
	snapshot, err := sender.rgbManager.projectionStore.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.rgbManager.projectionStore.ImportSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	txid, fee, err := sender.ResumeRGB11Send(ctx, prepared.State.TransferID)
	if err != nil || txid != prepared.TxID || fee <= 0 {
		t.Fatalf("resume=%s fee=%d err=%v", txid, fee, err)
	}
	for _, state := range prepared.States {
		local, err := recipient.rgbManager.projectionStore.LoadTransferState(state.TransferID)
		if err != nil || len(local.OutputOutPoints) != 1 || local.OutputOutPoints[0] != fmt.Sprintf("%s:%d", txid, state.RecipientVout) {
			t.Fatalf("recipient state=%+v err=%v", local, err)
		}
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	witness := wire.NewMsgTx(2)
	if err := witness.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
		t.Fatal(err)
	}
	evidence.mu.Lock()
	evidence.rawTx[txid] = append([]byte(nil), pending.SignedTx...)
	for _, input := range witness.TxIn {
		evidence.spendingTx[input.PreviousOutPoint.String()] = txid
	}
	for i, output := range witness.TxOut {
		point := fmt.Sprintf("%s:%d", txid, i)
		evidence.utxos[point] = &rgb11wallet.BitcoinUTXO{OutPoint: point, Value: output.Value, PkScript: output.PkScript, Confirmations: 1}
	}
	evidence.mu.Unlock()
	evidence.setStatus(txid, rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 1})
	if _, err := recipient.RefreshRGB11State(context.Background()); err != nil {
		t.Fatal(err)
	}
	balance, err := recipient.GetRGB11AssetBalance(&imported.AssetName)
	if err != nil || balance == nil || balance.Value.Uint64() != 40000 {
		t.Fatalf("batch receive balance=%v err=%v", balance, err)
	}
	if _, err := sender.RefreshRGB11State(context.Background()); err != nil {
		t.Fatal(err)
	}

}

func TestGenericBatchSendAssetsRGB11(t *testing.T) {
	sender, recipient, imported, evidence, _ := newRGB11GenericSendFixture(t)
	type result struct {
		err error
		fee int64
	}
	done := make(chan result, 1)
	go func() {
		tx, fee, err := sender.BatchSendAssets(recipient.wallet.GetAddress(), imported.AssetName.String(), indexer.NewDecimalWithScale(20000, sender.getTickerInfo(&imported.AssetName).Divisibility).String(), 2, 2, nil)
		if err == nil && tx == nil {
			err = fmt.Errorf("missing sent transaction")
		}
		done <- result{err, fee}
	}()
	finished := false
	defer func() {
		if !finished {
			<-done
		}
	}()
	deadline := time.NewTimer(130 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case r := <-done:
			finished = true
			if r.err != nil || r.fee <= 0 {
				t.Fatalf("generic RGB batch fee=%d err=%v", r.fee, r.err)
			}
			evidence.mu.Lock()
			sent := len(evidence.broadcasted) > 0
			evidence.mu.Unlock()
			if !sent {
				t.Fatal("generic send succeeded without broadcast")
			}
			return
		case <-deadline.C:
			t.Fatal("generic RGB send did not finish")
		case <-tick.C:
			syncResult, err := recipient.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
			if err != nil || syncResult.Invalid != 0 {
				t.Fatalf("receive=%+v err=%v", syncResult, err)
			}
		}
	}
}

func TestRGB11SendRejectsCarriersContainingOtherAssets(t *testing.T) {
	for _, protocol := range []string{indexer.PROTOCOL_NAME_ORDX, indexer.PROTOCOL_NAME_RUNES, indexer.PROTOCOL_NAME_BRC20} {
		t.Run(protocol, func(t *testing.T) {
			sender, recipient, imported, evidence, rpc := newRGB11GenericSendFixture(t)
			source := "14295d5bb1a191cdb6286dc0944df938421e3dfcbf0811353ccac4100c2068c5:1"
			rpc.outputs[source].Assets = indexer.TxAssets{{Name: indexer.AssetName{Protocol: protocol, Type: "f", Ticker: "OTHER"}, Amount: *indexer.NewDefaultDecimal(7)}}
			request := RGB11AddressSendRequest{ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName, AmountRaw: "20000", FeeRate: 2, MinConfirmations: 1}
			_, _, err := sender.PrepareConfiguredRGB11AddressTransfer(context.Background(), request, dkvsindexer.RecordVerificationOptions{})
			if !errors.Is(err, ErrRGB11AssetPreservation) {
				t.Fatalf("mixed carrier was not refused: %v", err)
			}
			evidence.mu.Lock()
			sent := len(evidence.broadcasted) != 0
			evidence.mu.Unlock()
			if sent {
				t.Fatal("mixed carrier broadcast")
			}
			transfers, err := sender.rgbManager.projectionStore.ListTransfers()
			if err != nil || len(transfers) != 0 {
				t.Fatalf("failed preservation check persisted transfer: %v %v", transfers, err)
			}
		})
	}
}

func TestRGB11FeeSelectionRejectsStalePlainAssetView(t *testing.T) {
	sender, recipient, imported, evidence, rpc := newRGB11GenericSendFixture(t)
	source := "14295d5bb1a191cdb6286dc0944df938421e3dfcbf0811353ccac4100c2068c5:1"
	unsafe := "3333333333333333333333333333333333333333333333333333333333333333:0"
	plain := "4444444444444444444444444444444444444444444444444444444444444444:0"
	// Force an extra fee input. The plain-list cache is intentionally stale.
	evidence.utxos[source].Value = 330
	rpc.outputs[source].OutValue.Value = 330
	rpc.outputs[unsafe].Assets = indexer.TxAssets{{Name: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ORDX, Type: "f", Ticker: "OTHER"}, Amount: *indexer.NewDefaultDecimal(7)}}
	plainOutput := indexer.NewTxOutput(10000)
	plainOutput.OutPointStr = plain
	plainOutput.OutValue.PkScript = rpc.outputs[source].OutValue.PkScript
	rpc.outputs[plain] = plainOutput
	rpc.plain = append([]*indexerwire.TxOutputInfo{{OutPoint: plain, Value: 10000, PkScript: plainOutput.OutValue.PkScript}}, rpc.plain...)
	request := RGB11AddressSendRequest{ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName, AmountRaw: "20000", FeeRate: 2, MinConfirmations: 1}
	prepared, _, err := sender.PrepareConfiguredRGB11AddressTransfer(context.Background(), request, dkvsindexer.RecordVerificationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	foundPlain := false
	for _, point := range prepared.State.InputOutPoints {
		if point == unsafe {
			t.Fatal("RGB fee selection consumed another asset")
		}
		if point == plain {
			foundPlain = true
		}
	}
	if !foundPlain {
		t.Fatal("test did not force the clean fee input")
	}
}

func TestRGB11SendDoesNotUseAnotherRGBAssetForFees(t *testing.T) {
	sender, recipient, imported, evidence, rpc := newRGB11GenericSendFixture(t)
	source := "14295d5bb1a191cdb6286dc0944df938421e3dfcbf0811353ccac4100c2068c5:1"
	other := "3333333333333333333333333333333333333333333333333333333333333333:0"
	plain := "5555555555555555555555555555555555555555555555555555555555555555:0"
	evidence.utxos[other] = &rgb11wallet.BitcoinUTXO{OutPoint: other, Value: 100000, PkScript: rpc.outputs[other].OutValue.PkScript, Confirmations: 6}
	issued, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{Schema: "NIA", Ticker: "OTHER", Name: "Other RGB fee protection", Amounts: []uint64{50}})
	if err != nil || len(issued.OutPoints) != 1 || issued.OutPoints[0] != other {
		t.Fatalf("issue second RGB asset: %+v %v", issued, err)
	}
	if err := sender.utxoLockerL1.UnlockUtxo(other); err != nil {
		t.Fatal(err)
	}
	evidence.utxos[source].Value = 330
	rpc.outputs[source].OutValue.Value = 330
	out := indexer.NewTxOutput(10000)
	out.OutPointStr = plain
	out.OutValue.PkScript = rpc.outputs[source].OutValue.PkScript
	rpc.outputs[plain] = out
	rpc.plain = append([]*indexerwire.TxOutputInfo{{OutPoint: plain, Value: 10000, PkScript: out.OutValue.PkScript}}, rpc.plain...)
	request := RGB11AddressSendRequest{ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName, AmountRaw: "20000", FeeRate: 2, MinConfirmations: 1}
	prepared, _, err := sender.PrepareConfiguredRGB11AddressTransfer(context.Background(), request, dkvsindexer.RecordVerificationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, point := range prepared.State.InputOutPoints {
		if point == other {
			t.Fatal("RGB send used another RGB asset for fees")
		}
	}
	balance, err := sender.GetRGB11AssetBalance(&issued.AssetName)
	if err != nil || balance == nil || balance.Value.Uint64() != 50 {
		t.Fatalf("other RGB balance=%v err=%v", balance, err)
	}
}

func TestRGB11SendPreservesOtherContractOnSameCarrier(t *testing.T) {
	sender, recipient, imported, evidence, _ := newRGB11GenericSendFixture(t)
	const source = "14295d5bb1a191cdb6286dc0944df938421e3dfcbf0811353ccac4100c2068c5:1"
	allocations, err := rgb11IssueAllocations([]string{source}, []uint64{7})
	if err != nil {
		t.Fatal(err)
	}
	// Import a second real genesis sealed to the same Bitcoin output. This is
	// possible for externally created RGB contracts even though our issuer
	// deliberately selects only plain outputs.
	issued, err := coreissuance.Issue(coreissuance.Spec{
		Kind: schemas.NIA, Network: rgb11IssuanceNetwork(&chaincfg.TestNet4Params),
		Ticker: "SHARED", Name: "Shared carrier asset", Allocations: allocations,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := sender.ImportRGB11Contract(context.Background(), []byte(issued.Armor))
	if err != nil || other.Projected != 1 {
		t.Fatalf("import shared carrier: %+v %v", other, err)
	}
	request := RGB11AddressSendRequest{ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName, AmountRaw: "20000", FeeRate: 2, MinConfirmations: 1}
	for _, status := range []string{"settled", "inconsistent"} {
		proof, err := sender.rgbManager.projectionStore.LoadProof(source, other.AssetName)
		if err != nil {
			t.Fatal(err)
		}
		proof.Status = status
		if err := sender.rgbManager.projectionStore.SaveProofState(proof); err != nil {
			t.Fatal(err)
		}
		_, _, err = sender.PrepareConfiguredRGB11AddressTransfer(context.Background(), request, dkvsindexer.RecordVerificationOptions{})
		if !errors.Is(err, ErrRGB11HistoryMerge) && !errors.Is(err, ErrRGB11AssetPreservation) {
			t.Fatalf("other contract status=%s did not prevent spending shared carrier: %v", status, err)
		}
		transfers, err := sender.rgbManager.projectionStore.ListTransfers()
		if err != nil || len(transfers) != 0 {
			t.Fatalf("failed preservation check persisted transfer: %v %v", transfers, err)
		}
	}
	evidence.mu.Lock()
	sent := len(evidence.broadcasted) != 0
	evidence.mu.Unlock()
	if sent {
		t.Fatal("shared carrier was broadcast")
	}
}
