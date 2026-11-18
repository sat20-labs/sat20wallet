package wallet

import (
	"context"
	"reflect"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/common"
)

func TestUtxoLockClassification(t *testing.T) {
	for _, test := range []struct {
		reason, previous, category string
	}{
		{common.UtxoLockManual, "", "manual"},
		{common.UtxoLockBroadcasted, "", "other"},
		{common.UtxoLockContractDeposit, "", "other"},
		{common.UtxoLockStubPrefix + "withdraw", "", "other"},
		{common.UtxoLockClosedTopup, "", "other"},
		{"external-operation", "", "other"},
		{common.UtxoLockRGB, "", "rgb"},
		{common.UtxoLockRGBPending, "", "rgb"},
		{common.UtxoLockRGBWithdrawal, "", "rgb"},
		{common.UtxoLockContractDeposit, common.UtxoLockRGB, "rgb"},
	} {
		lock := &LockedUtxo{Reason: test.reason, ReservationPreviousReason: test.previous}
		view := DescribeUtxoLock(lock)
		if view.Category != test.category || view.Reason != test.reason || !reflect.DeepEqual(view.LockedUtxo, lock) {
			t.Fatalf("lock classification changed its source or data: %s", test.reason)
		}
		view.Reason = "presentation-copy"
		if lock.Reason != test.reason {
			t.Fatal("display row aliases the live lock")
		}
	}
}

func TestRGBLockViewReadOnly(t *testing.T) {
	sender, recipient, imported, evidence, _ := newRGB11GenericSendFixtureWithCarrier(t, 1_000)
	if err := sender.EnsureAccount(sender.status.CurrentWallet, 1, "separate scope", ""); err != nil {
		t.Fatal(err)
	}
	source := rgb11GenericFixtureSource(t, sender, imported)
	preview, err := sender.PrepareRGBTransfer(context.Background(), RGBSendRequest{
		Recipients: []RGBRecipient{{Destination: recipient.wallet.GetAddress(), Asset: imported.AssetName.String(), Amount: "20000"}},
		FeeRate:    2,
	})
	if err != nil {
		t.Fatal(err)
	}
	confirmed, err := sender.ConfirmRGBTransfer(context.Background(), preview.ID, nil)
	if err != nil || confirmed == nil || confirmed.Status != "awaiting_persistence" {
		t.Fatalf("prepare actual pending RGB lock fixture: result=%+v err=%v", confirmed, err)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(preview.ID)
	if err != nil || pending == nil || len(pending.State.InputOutPoints) < 2 {
		t.Fatalf("fixture must reserve a real carrier and separate fee input: %v", err)
	}
	// Join background work before checking byte-for-byte read-only behavior.
	sender.Stop()
	recipient.Stop()
	assertView := func(scope string) {
		t.Helper()
		before, err := GetItemsFromDB(nil, sender.db)
		if err != nil {
			t.Fatal(err)
		}
		locks := sender.utxoLockerL1.GetLockedUtxoList()
		evidence.rgb11FlowEvidence.mu.Lock()
		broadcasts := evidence.rgb11FlowEvidence.broadcastCount
		evidence.rgb11FlowEvidence.mu.Unlock()
		view, err := sender.GetRGBView()
		if err != nil {
			t.Fatal(err)
		}
		if view.LockScope.WalletID != sender.status.CurrentWallet || view.LockScope.AccountIndex != sender.status.CurrentAccount {
			t.Fatal("lock details were attributed to a different selected identity")
		}
		feeInputs := 0
		for _, input := range pending.State.InputOutPoints {
			var row *RGBLockView
			for i := range view.Locks {
				if view.Locks[i].Chain == "btc" && view.Locks[i].Outpoint == input {
					row = &view.Locks[i]
					break
				}
			}
			if row == nil || row.Scope != scope || row.Reason != common.UtxoLockRGBPending || locks[input] == nil {
				t.Fatalf("reserved input missing or incorrectly scoped: scope=%s row=%+v", scope, row)
			}
			if scope == "current" && !reflect.DeepEqual(row.TransferIDs, []string{preview.ID}) {
				t.Fatal("carrier or fee input lost its actual pending transfer association")
			}
			if scope == "unknown" && len(row.TransferIDs) != 0 {
				t.Fatal("another account's network lock was attributed to this account")
			}
			if input != source {
				feeInputs++
			}
		}
		if feeInputs == 0 {
			t.Fatal("no separate fee input was inspected")
		}
		after, err := GetItemsFromDB(nil, sender.db)
		if err != nil || !reflect.DeepEqual(after, before) || !reflect.DeepEqual(locks, sender.utxoLockerL1.GetLockedUtxoList()) {
			t.Fatal("RGB lock query changed the database or protection locks")
		}
		evidence.rgb11FlowEvidence.mu.Lock()
		defer evidence.rgb11FlowEvidence.mu.Unlock()
		if evidence.rgb11FlowEvidence.broadcastCount != broadcasts {
			t.Fatal("RGB lock query broadcast a transaction")
		}
	}
	assertView("current")
	if err := sender.SwitchAccount(1); err != nil {
		t.Fatal(err)
	}
	assertView("unknown")
}
