//go:build rgb11discard

package wallet

import (
	"context"
	"testing"
	"time"
)

func TestRGB11ConsistencyDiag(t *testing.T) {
	sender, recipient, imported, _, _ := newRGB11GenericSendFixture(t)
	invoice, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID,
		AmountRaw: "10000", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
		Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	pending.State.Status = "settled"
	pending.SignedTx = nil
	if err := sender.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
		t.Fatal(err)
	}
	diagnostic, err := sender.DiagnoseRGB11Consistency(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostic.Issues) != 1 || diagnostic.Issues[0].Check != "send_journal" ||
		diagnostic.Issues[0].TransferID != prepared.State.TransferID {
		t.Fatalf("diagnostic=%+v", diagnostic)
	}
}

func TestRGB11DiagUnlockNoWrite(t *testing.T) {
	manager := newAccountManagementAutoTestManager(t)
	if _, err := manager.ImportWallet(rgb11ManagedOperationTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	outpoint, _, _ := seedRGB11DurableAccountRecoveryState(t, manager.rgbManager)
	if err := manager.utxoLockerL1.UnlockUtxo(outpoint); err != nil {
		t.Fatal(err)
	}
	manager.rgbManager.consistencyStatus = "sentinel"
	manager.wallet = nil
	for _, info := range manager.walletInfoMap {
		info.Wallet = nil
	}
	manager.clearAccountManagementSession()
	if _, err := manager.UnlockRGB11Diag("password"); err != nil {
		t.Fatal(err)
	}
	if manager.utxoLockerL1.GetLockedUtxoList()[outpoint] != nil {
		t.Fatal("diagnostic unlock rebuilt a persistent RGB lock")
	}
	if manager.rgbManager.consistencyStatus != "sentinel" {
		t.Fatalf("diagnostic unlock changed consistency=%s", manager.rgbManager.consistencyStatus)
	}
}
