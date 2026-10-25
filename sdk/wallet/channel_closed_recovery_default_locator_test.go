package wallet

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestSTPClosedRecoveryDefaultLocatorUsesOnlyCapturedAccount(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	installSingleUTXOLocalOther(t, f)
	api := closedRecoveryChoicesAPIForTest(t, f.manager)
	raw, err := api.PreviewClosedChannelRecovery("")
	if err != nil {
		t.Fatal(err)
	}
	var options struct {
		WalletID               int64
		AccountIndex           uint32
		ChannelID, OldParentID string
		CanWithdraw            bool
	}
	if err := json.Unmarshal([]byte(raw), &options); err != nil {
		t.Fatal(err)
	}
	if !options.CanWithdraw || options.ChannelID != f.channelID || options.OldParentID != f.rpc.parent.TxID() || options.WalletID != f.manager.wallet.GetId() || options.AccountIndex != f.manager.wallet.GetSubAccount() {
		t.Fatalf("empty-ID local recovery did not locate this captured account's exact closed package: %s", raw)
	}
	if f.gate.calls != 0 || *f.signatures != 0 || len(f.rpc.broadcasts) != 0 || len(f.manager.GetLocalActionReservations()) != 0 || len(f.manager.utxoLockerL1.GetLockedUtxoList()) != 0 {
		t.Fatal("default locator performed server/funding/recovery actions")
	}
}

func TestSTPClosedRecoveryDefaultLocatorDoesNotSearchAnotherAccount(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	installSingleUTXOLocalOther(t, f)
	f.manager.wallet.SetSubAccount(1)
	currentID, err := f.manager.channelIDForWallet(f.manager.wallet.Clone())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := closedRecoveryChoicesAPIForTest(t, f.manager).PreviewClosedChannelRecovery("")
	if err != nil {
		t.Fatal(err)
	}
	var options struct {
		AccountIndex   uint32
		ChannelID      string
		CanWithdraw    bool
		WithdrawReason string
	}
	if err = json.Unmarshal([]byte(raw), &options); err != nil {
		t.Fatal(err)
	}
	if options.AccountIndex != 1 || options.ChannelID != currentID || options.CanWithdraw || options.WithdrawReason == "" || currentID == f.channelID {
		t.Fatalf("default locator leaked old account or omitted unavailable status: %s", raw)
	}
	if f.gate.calls != 0 || *f.signatures != 0 || len(f.rpc.broadcasts) != 0 || len(f.manager.GetLocalActionReservations()) != 0 {
		t.Fatal("wrong-account locator mutated wallet")
	}
}

func TestSTPClosedRecoveryDefaultLocatorDoesNotInventMissingPackage(t *testing.T) {
	for _, mode := range []string{"no-record", "missing-package"} {
		t.Run(mode, func(t *testing.T) {
			f := newSingleUTXOExecutionFixture(t)
			if mode == "no-record" {
				if err := f.manager.db.Delete([]byte(GetChannelKey(f.channelID))); err != nil {
					t.Fatal(err)
				}
			}
			raw, err := closedRecoveryChoicesAPIForTest(t, f.manager).PreviewClosedChannelRecovery("")
			if err != nil {
				t.Fatal(err)
			}
			var options struct {
				ChannelID      string
				CanWithdraw    bool
				WithdrawReason string
			}
			if err = json.Unmarshal([]byte(raw), &options); err != nil {
				t.Fatal(err)
			}
			reason := strings.ToLower(options.WithdrawReason)
			if options.ChannelID != f.channelID || options.CanWithdraw || reason == "" || (!strings.Contains(reason, "unavailable") && !strings.Contains(reason, "package") && !strings.Contains(reason, "material")) {
				t.Fatalf("missing local recovery material was invented or not clearly unavailable: %s", raw)
			}
			if f.gate.calls != 0 || *f.signatures != 0 || len(f.rpc.broadcasts) != 0 || len(f.manager.GetLocalActionReservations()) != 0 {
				t.Fatal("missing local package caused server sync or funding")
			}
		})
	}
}

func TestSTPClosedRecoveryDefaultLocatorWorksWithOfflineServer(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	installSingleUTXOLocalOther(t, f)
	f.gate.err = errors.New("server offline")
	raw, err := closedRecoveryChoicesAPIForTest(t, f.manager).PreviewClosedChannelRecovery("")
	if err != nil {
		t.Fatal(err)
	}
	var options struct {
		ChannelID, OldParentID string
		CanWithdraw            bool
		WithdrawFeeSats        int64
	}
	if err = json.Unmarshal([]byte(raw), &options); err != nil {
		t.Fatal(err)
	}
	if !options.CanWithdraw || options.ChannelID != f.channelID || options.OldParentID != f.rpc.parent.TxID() || options.WithdrawFeeSats != 500 {
		t.Fatalf("offline server hid a complete verified local exit: %s", raw)
	}
	if f.gate.calls != 0 || *f.signatures != 0 || len(f.rpc.broadcasts) != 0 || len(f.manager.GetLocalActionReservations()) != 0 {
		t.Fatal("offline preview contacted server or broadcast without approval")
	}
}
