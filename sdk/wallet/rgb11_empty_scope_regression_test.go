package wallet

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/sat20-labs/rgb11/invoicing"
	corewallet "github.com/sat20-labs/rgb11/wallet"
)

// An account with no RGB records has no Bitcoin-dependent projection to
// rebuild. Account recovery must not acquire a new dependency on L1 access.
func TestRGB11EmptyScopeRecoveryWithoutBitcoinEvidence(t *testing.T) {
	manager := newAccountManagementAutoTestManager(t)
	if _, err := manager.ImportWallet(accountRootWrapperTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	walletID, err := manager.rgbManager.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
	if err != nil || len(snapshot.ProjectionRecords) != 0 || len(snapshot.EngineRecords) != 0 {
		t.Fatalf("fixture must be a genuinely empty RGB scope: %v", err)
	}
	if manager.rgbManager.evidence != nil {
		t.Fatal("fixture unexpectedly has L1 evidence")
	}
	resolved, err := manager.rgbManager.resolveRGB11RecoveryProjection(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("empty RGB scope blocks otherwise valid account recovery: %v", err)
	}
	if !resolved.complete || !reflect.DeepEqual(snapshot, resolved.snapshot) || len(resolved.lockReasons) != 0 {
		t.Fatal("empty scope resolution invented ownership or altered its recovery records")
	}
	if err := resolved.checkEvidence(); err != nil {
		t.Fatal(err)
	}
	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := manager.rgbManager.resolveRGB11RecoveryProjection(ctx, snapshot); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled empty recovery accepted: %v", err)
		}
	})
	t.Run("different scope", func(t *testing.T) {
		other := *snapshot
		other.AccountIndex++
		if _, err := manager.rgbManager.resolveRGB11RecoveryProjection(context.Background(), &other); err == nil {
			t.Fatal("empty recovery bypassed account identity")
		}
	})
	// A real engine receive is local recovery material even with no projection
	// proof or common reservation. An empty incoming snapshot cannot skip it.
	if _, err := manager.rgbManager.engine.CreateReceive(corewallet.ReceiveParams{
		Network: invoicing.BitcoinTestnet4, RecipientID: "empty-scope-negative-control",
		WitnessVout: 0, Expiry: time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	nonempty, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
	if err != nil || len(nonempty.EngineRecords) == 0 {
		t.Fatalf("negative control did not persist real RGB material: %v", err)
	}
	for name, candidate := range map[string]*RGB11WalletSnapshot{"empty candidate over local material": snapshot, "nonempty candidate": nonempty} {
		t.Run(name, func(t *testing.T) {
			if _, err := manager.rgbManager.resolveRGB11RecoveryProjection(context.Background(), candidate); err == nil {
				t.Fatal("RGB recovery material accepted without Bitcoin evidence")
			}
			after, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
			if err != nil || !reflect.DeepEqual(nonempty, after) {
				t.Fatal("failed recovery altered local RGB material")
			}
		})
	}
}
