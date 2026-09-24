package wallet

import (
	"context"
	"errors"
	"testing"
	"time"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func TestRGB11RefreshLockContext(t *testing.T) {
	manager := newRGB11MultiDeviceManager(t, newRGB11StatusPrivateKey(t), 57)
	manager.rgbManager.evidence = &rgb11StatusEvidence{
		statuses:  make(map[string]*rgb11wallet.BitcoinTxStatus),
		outspends: make(map[string]*rgb11wallet.BitcoinOutspend),
	}
	beforeConsistency := manager.rgbManager.consistencyStatus
	beforeLocks := manager.utxoLockerL1.GetLockedUtxoList()
	manager.rgbManager.scopeStates.chainRefresh.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := manager.RefreshRGB11State(ctx)
		done <- err
	}()

	select {
	case err := <-done:
		manager.rgbManager.scopeStates.chainRefresh.Unlock()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("refresh lock error=%v", err)
		}
		if manager.rgbManager.consistencyStatus != beforeConsistency {
			t.Fatalf("consistency changed while waiting: %s", manager.rgbManager.consistencyStatus)
		}
		if len(manager.utxoLockerL1.GetLockedUtxoList()) != len(beforeLocks) {
			t.Fatal("locks changed while waiting for refresh ownership")
		}
	case <-time.After(200 * time.Millisecond):
		manager.rgbManager.scopeStates.chainRefresh.Unlock()
		<-done
		t.Fatal("refresh ignored context while waiting for the chain lock")
	}
}
