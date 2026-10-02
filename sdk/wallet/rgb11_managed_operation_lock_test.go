package wallet

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestRGB11ManagedBaselineNetworkWaitDoesNotBlockRGBState(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	manager, _, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	remote := &blockedAccountConfigHTTP{
		rgb11MemoryDKVSHTTP: newRGB11MemoryDKVSHTTP(),
		started: make(chan struct{}), release: make(chan struct{}),
	}
	manager.http = remote
	manager.dkvs.mu.Lock()
	manager.dkvs.clients = make(map[string]*SatsNetDKVSClient)
	manager.dkvs.mu.Unlock()
	manager.accountProfile.RecoveryConfigured = true
	manager.accountProfile.ManagedDataDirty = true

	done := make(chan error, 1)
	go func() {
		_, err := runRGB11ManagedOperation(manager, context.Background(), rgb11ManagedOperationNew,
			func(*rgb11Manager) (struct{}, error) { return struct{}{}, nil })
		done <- err
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
		close(remote.release)
		<-done
		t.Fatal("GetRGB11State blocked behind managed baseline network wait")
	}
	close(remote.release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("managed operation did not finish after network release")
	}
}

func TestRGB11ManagedOperationWaitDoesNotBlockRGBState(t *testing.T) {
	manager, _, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	manager.accountProfile.RecoveryConfigured = false

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	done := make(chan error, 1)
	go func() {
		_, err := runRGB11ManagedOperation(manager, context.Background(), rgb11ManagedOperationNew,
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

func TestRGB11ManagedOperationKeepsScopeChangeBlocked(t *testing.T) {
	manager, _, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	manager.accountProfile.RecoveryConfigured = false

	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := runRGB11ManagedOperation(manager, context.Background(), rgb11ManagedOperationNew,
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

	if manager.rgbOperationMu.TryLock() {
		manager.rgbOperationMu.Unlock()
		close(release)
		<-done
		t.Fatal("managed operation no longer freezes RGB scope changes")
	}
	if manager.channelIdentityMu.TryLock() {
		manager.channelIdentityMu.Unlock()
		close(release)
		<-done
		t.Fatal("managed operation no longer freezes wallet identity changes")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}


func TestRGB11ManagedOperationWaitDoesNotBlockReadOnlyAPIs(t *testing.T) {
	manager, _, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	manager.accountProfile.RecoveryConfigured = false

	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := runRGB11ManagedOperation(manager, context.Background(), rgb11ManagedOperationNew,
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
