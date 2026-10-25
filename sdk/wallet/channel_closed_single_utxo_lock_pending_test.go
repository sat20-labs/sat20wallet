package wallet

import (
	"strings"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/common"
)

func addSingleUTXOPendingLockExpand(t *testing.T, f *singleUTXOExecutionFixture, w common.Wallet, channelID string) *LocalActionPerformData {
	t.Helper()
	action, err := NewLocalActionPerformData(f.manager.GenerateNewResvId(), LOCAL_ACTION_LOCK_WITH_EXPAND, &LocalActionParam_Expand{ChannelId: channelID}, 5, w.GetPaymentPubKey().SerializeCompressed())
	if err != nil {
		t.Fatal(err)
	}
	action.SetLocalWallet(w.Clone())
	action.Status = RS_PERFORM_ACTION_STARTED
	if err = f.manager.SaveWalletReservation(action); err != nil {
		t.Fatal(err)
	}
	f.manager.AddResv(action)
	return action
}
func TestSTPSingleUTXOTopupSameAccountLockExpandBlocksSignedRetry(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	f.rpc.testFailures = 1
	quote := f.quote(t)
	api := singleUTXOTopupAPIForTest(t, f.manager)
	if _, err := api.ConfirmClosedChannelSingleUTXOTopup(quote); err == nil {
		t.Fatal("fixture must retain signed original after temporary acceptance-test failure")
	}
	addSingleUTXOPendingLockExpand(t, f, f.manager.wallet.Clone(), f.channelID)
	if _, err := api.QuoteClosedChannelSingleUTXOTopup(5); err == nil || !strings.Contains(strings.ToLower(err.Error()), "lock") {
		t.Errorf("own recovery exception bypassed pending lock-with-expand in readonly quote: %v", err)
	}
	if _, err := api.ConfirmClosedChannelSingleUTXOTopup(quote); err == nil || !strings.Contains(strings.ToLower(err.Error()), "lock") {
		t.Errorf("cached exact quote bypassed pending lock-with-expand at execution: %v", err)
	}
	if len(f.rpc.broadcasts) != 0 || *f.signatures != 1 {
		t.Fatalf("blocked retry broadcast or re-signed: sends=%d signs=%d", len(f.rpc.broadcasts), *f.signatures)
	}
}
func TestSTPSingleUTXOTopupUnrelatedLockExpandDoesNotBlockOriginal(t *testing.T) {
	for _, kind := range []string{"other-account", "other-channel"} {
		t.Run(kind, func(t *testing.T) {
			f := newSingleUTXOExecutionFixture(t)
			f.rpc.testFailures = 1
			quote := f.quote(t)
			api := singleUTXOTopupAPIForTest(t, f.manager)
			if _, err := api.ConfirmClosedChannelSingleUTXOTopup(quote); err == nil {
				t.Fatal("fixture did not retain signed original")
			}
			owner := f.manager.wallet.Clone()
			channelID := f.channelID
			if kind == "other-account" {
				owner.SetSubAccount(1)
			} else {
				channelID = "unrelated-channel"
			}
			addSingleUTXOPendingLockExpand(t, f, owner, channelID)
			same, err := api.QuoteClosedChannelSingleUTXOTopup(5)
			if err != nil {
				t.Fatal(err)
			}
			if same != quote {
				t.Fatal("unrelated operation replaced original quote")
			}
			if _, err = api.ConfirmClosedChannelSingleUTXOTopup(same); err != nil {
				t.Fatal(err)
			}
			if len(f.rpc.broadcasts) != 1 || *f.signatures != 1 || f.rpc.durableError != nil {
				t.Fatal("unrelated operation changed original signed transaction or owner")
			}
		})
	}
}
