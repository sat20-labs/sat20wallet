package wallet

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestRGB11BackupNetworkWaitDoesNotBlockRGBState(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	manager, baselineRemote := newRGB11OnlineLockTestManager(t)
	remote := &blockedAccountConfigHTTP{
		rgb11MemoryDKVSHTTP: baselineRemote,
		started:             make(chan struct{}), release: make(chan struct{}),
	}
	manager.http = remote
	client, err := manager.GetDKVSClient()
	if err != nil {
		t.Fatal(err)
	}
	client.Http = remote
	var releaseOnce sync.Once
	releaseRemote := func() { releaseOnce.Do(func() { close(remote.release) }) }
	defer releaseRemote()

	done := make(chan error, 1)
	go func() {
		done <- manager.SyncAccountManagementState(context.Background())
	}()
	select {
	case <-remote.started:
	case err := <-done:
		t.Fatalf("managed operation ended before network gate: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("managed operation did not reach DKVS network gate")
	}

	stateDone := make(chan error, 1)
	go func() {
		_, err := manager.GetRGB11State()
		stateDone <- err
	}()
	select {
	case err := <-stateDone:
		if err != nil {
			t.Fatalf("read RGB state during managed baseline wait: %v", err)
		}
	case <-time.After(time.Second):
		releaseRemote()
		<-done
		t.Fatal("GetRGB11State blocked behind managed baseline network wait")
	}
	releaseRemote()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("managed operation did not finish after network release")
	}
}

func TestRGB11ManagedOperationWaitDoesNotBlockRGBState(t *testing.T) {
	manager, baselineRemote := newRGB11OnlineLockTestManager(t)
	_ = baselineRemote

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	done := make(chan error, 1)
	go func() {
		_, err := runRGB11ManagedOperation(manager, context.Background(), rgb11ManagedOperationRefresh,
			func(*rgb11Manager) (struct{}, error) {
				once.Do(func() { close(started) })
				<-release
				return struct{}{}, nil
			})
		done <- err
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("managed operation ended before wait: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("managed operation did not start")
	}

	stateDone := make(chan error, 1)
	go func() {
		_, err := manager.GetRGB11State()
		stateDone <- err
	}()
	select {
	case err := <-stateDone:
		if err != nil {
			t.Fatalf("read RGB state during managed operation wait: %v", err)
		}
	case <-time.After(time.Second):
		close(release)
		<-done
		t.Fatal("GetRGB11State blocked behind managed RGB operation")
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("managed operation did not finish")
	}
}

func TestRGB11ManagedOperationKeepsCapturedScope(t *testing.T) {
	manager, baselineRemote := newRGB11OnlineLockTestManager(t)
	_ = baselineRemote
	originalAddress := manager.wallet.GetAddress()
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	go func() {
		_, err := runRGB11ManagedOperation(manager, context.Background(), rgb11ManagedOperationRefresh, func(current *rgb11Manager) (struct{}, error) {
			close(started)
			<-release
			if current.status.CurrentAccount != 0 || current.wallet.GetAddress() != originalAddress {
				return struct{}{}, fmt.Errorf("captured RGB scope followed UI selection")
			}
			return struct{}{}, nil
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("managed operation did not start")
	}
	switched := make(chan error, 1)
	go func() { switched <- manager.SwitchAccount(1) }()
	finished := false
	select {
	case err := <-switched:
		if err != nil {
			t.Errorf("selection: %v", err)
		}
		finished = true
	case <-time.After(2 * time.Second):
		t.Error("selection waited for managed remote phase")
	}
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("managed cleanup stalled")
	}
	if !finished {
		select {
		case err := <-switched:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(4 * time.Second):
			t.Fatal("selection cleanup stalled")
		}
	}
	if manager.status.CurrentAccount != 1 {
		t.Errorf("UI selection=%d", manager.status.CurrentAccount)
	}
}

func TestRGB11ManagedOperationWaitDoesNotBlockReadOnlyAPIs(t *testing.T) {
	manager, baselineRemote := newRGB11OnlineLockTestManager(t)
	_ = baselineRemote

	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := runRGB11ManagedOperation(manager, context.Background(), rgb11ManagedOperationRefresh,
			func(*rgb11Manager) (struct{}, error) {
				close(started)
				<-release
				return struct{}{}, nil
			})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("managed operation did not start")
	}

	checkFast := func(name string, call func() error) {
		t.Helper()
		result := make(chan error, 1)
		go func() { result <- call() }()
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("%s during managed operation: %v", name, err)
			}
		case <-time.After(time.Second):
			close(release)
			<-done
			t.Fatalf("%s blocked behind managed RGB operation", name)
		}
	}
	checkFast("ListRGB11Outputs", func() error {
		_, err := manager.ListRGB11Outputs()
		return err
	})
	checkFast("RGB11WalletID", func() error {
		_, err := manager.RGB11WalletID()
		return err
	})

	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func newRGB11OnlineLockTestManager(t *testing.T) (*Manager, *rgb11MemoryDKVSHTTP) {
	t.Helper()
	old := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = old })
	manager, remote, _, _ := accountTTLNoopFixture(t)
	manager.accountBackgroundWG.Wait()
	return manager, remote
}
