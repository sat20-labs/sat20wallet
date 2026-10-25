package wallet

import (
	"testing"
)

// This is the shared C2 CURRENT-fact boundary, not a synthetic ARC fact fault.
// A real RGB issuance and its ordinary validated projection already occupy an
// otherwise public-plain carrier. A C2 plain fee/stub frontier must not lose
// that locally known allocation merely because the Bitcoin RPC cannot see it.
func TestClosedWithdrawalCurrentFrontierIncludesCapturedLocalRGB(t *testing.T) {
	manager, _, imported, evidence, rpc := newRGB11GenericSendFixture(t)
	captured := manager.wallet.Clone()
	point := rgb11GenericFixtureSource(t, manager, imported)
	chainOnly, err := rpc.GetTxOutput(point)
	if err != nil || chainOnly == nil || len(chainOnly.Assets) != 0 {
		t.Fatalf("ordinary public chain fixture was altered: output=%v err=%v", chainOnly, err)
	}
	// Use the existing SDK captured-account composition route. Its projection
	// is produced by real IssueRGB11Asset above, not a mocked RGB receipt.
	scoped, err := manager.newScopedRGB11Manager(localRGB11Account{
		WalletID: captured.GetId(), AccountIndex: captured.GetSubAccount(), Address: captured.GetAddress(), Wallet: captured,
	})
	if err != nil {
		t.Fatal(err)
	}
	merged, err := scoped.getL1TxOutput(point)
	if err != nil || merged == nil || merged.GetAsset(&imported.AssetName).Sign() <= 0 {
		t.Fatalf("actual validated local RGB projection is unavailable: %v", err)
	}
	if _, err := scoped.Manager.indexedClosedWithdrawalFact(merged); err == nil {
		t.Fatal("native fact verifier did not reject the real local RGB co-asset")
	}
	chains := &nativeSettlementChains{bitcoin: evidence}
	t.Run("same-captured-account", func(t *testing.T) {
		if err := scoped.Manager.validateIndexedWithdrawalCurrent(chains, chainOnly); err == nil {
			t.Fatal("CURRENT C2 native frontier accepted a public-plain fee carrier with a real locally validated RGB allocation")
		}
	})
	t.Run("later-selection-does-not-change-captured-scope", func(t *testing.T) {
		oldWallet, oldAccount := manager.wallet, manager.status.CurrentAccount
		manager.wallet = captured.Clone()
		manager.wallet.SetSubAccount(captured.GetSubAccount() + 1)
		manager.status.CurrentAccount = manager.wallet.GetSubAccount()
		if err := manager.rgbManager.selectRGB11Scope(); err != nil {
			t.Fatal(err)
		}
		defer func() {
			manager.wallet, manager.status.CurrentAccount = oldWallet, oldAccount
			if err := manager.rgbManager.selectRGB11Scope(); err != nil {
				t.Error(err)
			}
		}()
		selected, err := manager.getL1TxOutput(point)
		if err != nil || selected == nil || len(selected.Assets) != 0 {
			t.Fatalf("new selection was not an independent empty RGB scope: %v", err)
		}
		retained, err := scoped.getL1TxOutput(point)
		if err != nil || retained.GetAsset(&imported.AssetName).Sign() <= 0 {
			t.Fatalf("captured scope lost its actual RGB allocation after selection changed: %v", err)
		}
		if err := scoped.Manager.validateIndexedWithdrawalCurrent(chains, chainOnly); err == nil {
			t.Fatal("CURRENT C2 frontier used a raw or later-selected view instead of its captured RGB overlay")
		}
	})
	t.Run("genuine-plain-fee-remains-accepted", func(t *testing.T) {
		if len(rpc.plain) < 2 {
			t.Fatal("ordinary fixture did not retain its separate plain fee output")
		}
		plain, err := rpc.GetTxOutput(rpc.plain[1].OutPoint)
		if err != nil {
			t.Fatal(err)
		}
		if err := scoped.Manager.validateIndexedWithdrawalCurrent(chains, plain); err != nil {
			t.Fatalf("same-source plain fee positive control: %v", err)
		}
	})
	t.Run("overlay-never-resurrects-unavailable-rpc-facts", func(t *testing.T) {
		retained := rpc.outputs[point]
		delete(rpc.outputs, point)
		defer func() { rpc.outputs[point] = retained }()
		if _, err := scoped.getL1TxOutput(point); err == nil {
			t.Fatal("SDK overlay replaced unavailable public facts with a projected holding")
		}
		if err := scoped.Manager.validateIndexedWithdrawalCurrent(chains, chainOnly); err == nil {
			t.Fatal("CURRENT C2 frontier accepted unavailable public facts because a projection existed")
		}
	})
}
