package wallet

import (
	"context"
	"reflect"
	"testing"
	"time"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// A sizing preview must validate the embedded prepared transaction without
// requiring that same transaction to have already been broadcast. Its output
// must remain an ephemeral snapshot, never a durable ownership projection.
func TestRGB11PreparedChangeCapacityPreviewBeforeBroadcast(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	evidence := newCoreE2EChain()
	senderWallet := NewInternalWalletWithMnemonic(coreE2ESenderMnemonic, "", GetChainParam())
	recipientWallet := NewInternalWalletWithMnemonic(coreE2EReceiverMnemonic, "", GetChainParam())
	if senderWallet == nil || recipientWallet == nil {
		t.Fatal("construct existing test wallets")
	}
	evidence.fund(t, senderWallet.GetAddress(), 3)
	evidence.fund(t, recipientWallet.GetAddress(), 2)
	rpc := &coreE2EL1Indexer{chain: evidence}
	sender := newRGB11FlowManager(t, senderWallet, rpc, evidence, 901)
	recipient := newRGB11FlowManager(t, recipientWallet, rpc, evidence, 902)
	issued, err := sender.rgbManager.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "CAPREPRO", Name: "Prepared capacity regression", Amounts: []uint64{50000},
	})
	if err != nil {
		t.Fatalf("issue contract against actual controlled funding transaction: %v", err)
	}
	invoice, err := recipient.rgbManager.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
		AmountRaw: "20000", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("construct actual receive invoice: %v", err)
	}
	prepared, err := sender.rgbManager.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
		Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatalf("construct valid unbroadcast prepared transfer: %v", err)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil || pending == nil || len(pending.ChangeSeals) == 0 || len(pending.LocalConsignment) == 0 {
		t.Fatalf("fixture must contain actual prepared change: %v", err)
	}
	if _, err := evidence.GetRawTx(pending.State.WitnessTxID); err == nil {
		t.Fatal("fixture unexpectedly exposes the unbroadcast witness in chain evidence")
	}
	validator := rgb11wallet.NewNativeConsensusValidatorWithReveals(pending.ChangeSeals...)
	if _, err := rgb11wallet.ValidatePreparedWith(context.Background(), validator,
		pending.LocalConsignment, evidence); err != nil {
		t.Fatalf("prepared consignment positive control: %v", err)
	}
	walletID, err := sender.rgbManager.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := sender.rgbManager.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := sender.rgbManager.addRGB11PreparedChangeCapacityPreview(context.Background(), before, pending)
	if err != nil {
		t.Fatalf("unbroadcast prepared change must be measurable: %v", err)
	}
	if preview == nil || len(preview.ProjectionRecords) <= len(before.ProjectionRecords) {
		t.Fatal("preview omitted the eventual change recovery records")
	}
	after, _, err := sender.rgbManager.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("capacity preview changed durable RGB ownership or recovery state")
	}
	evidence.mu.Lock()
	broadcasts := evidence.broadcasts
	evidence.mu.Unlock()
	if broadcasts != 0 {
		t.Fatal("capacity measurement must not broadcast")
	}
}
