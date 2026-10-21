package wallet

import (
	"context"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"sync"
	"testing"
	"time"
)

// Account selection and local authentication must both finish while a real
// RGB transport request is still outstanding; no lifecycle lock spans it.
func TestPWAWarmUnlockAndIdentitySwitchDuringRGBNetworkWait(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	manager, _, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	remote := &blockedAccountConfigHTTP{rgb11MemoryDKVSHTTP: newRGB11MemoryDKVSHTTP(), started: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(remote.release) }) }
	defer release()
	manager.http = remote
	manager.dkvs.mu.Lock()
	manager.dkvs.clients = make(map[string]*SatsNetDKVSClient)
	manager.dkvs.mu.Unlock()
	manager.accountProfile.RecoveryConfigured = true
	manager.accountProfile.ManagedDataDirty = true
	rgbDone := make(chan error, 1)
	go func() {
		_, err := manager.syncRootRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
		rgbDone <- err
	}()
	select {
	case <-remote.started:
	case err := <-rgbDone:
		t.Fatalf("setup: remote boundary: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("setup: no remote boundary")
	}
	switched := make(chan error, 1)
	// Same-account selection exercises the real writer but changes no identity.
	go func() { switched <- manager.SwitchAccount(0) }()
	switchFinished := false
	select {
	case err := <-switched:
		if err != nil {
			t.Errorf("same-account switch: %v", err)
		}
		switchFinished = true
	case <-time.After(2 * time.Second):
		t.Error("identity selection waited for RGB network")
	}
	unlocked := make(chan error, 1)
	go func() { _, err := manager.UnlockWallet("password"); unlocked <- err }()
	finished := false
	var unlockErr error
	select {
	case unlockErr = <-unlocked:
		finished = true
	case <-time.After(2 * time.Second):
		t.Error("local reauthentication joined identity writer queue behind RGB network wait")
	}
	release()
	select {
	case <-rgbDone:
	case <-time.After(4 * time.Second):
		t.Fatal("RGB cleanup stalled")
	}
	if !switchFinished {
		select {
		case err := <-switched:
			if err != nil {
				t.Errorf("same-account selection failed: %v", err)
			}
		case <-time.After(4 * time.Second):
			t.Fatal("writer remained blocked after network release")
		}
	}
	if !finished {
		select {
		case unlockErr = <-unlocked:
		case <-time.After(4 * time.Second):
			t.Fatal("unlock cleanup stalled")
		}
	}
	if unlockErr != nil {
		t.Errorf("local reauthentication failed: %v", unlockErr)
	}
}
