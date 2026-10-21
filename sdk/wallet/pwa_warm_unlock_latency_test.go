package wallet

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// Reuses the existing account fixture and HTTP boundary. The real public RGB
// refresh or private background-mailbox adapter owns the lock; this test never
// locks channelIdentityMu to manufacture the failure.
func TestPWAWarmUnlockDuringRGBNetworkWait(t *testing.T) {
	for _, entry := range []string{"public-refresh", "background-mailbox"} {
		for _, wrong := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/wrong-password=%t", entry, wrong), func(t *testing.T) {
				oldChain := _chain
				_chain = "testnet"
				defer func() { _chain = oldChain }()
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
				runtimeWallet := manager.wallet
				selected := manager.status.CurrentWallet
				generation := manager.accountGeneration
				rgbDone := make(chan error, 1)
				go func() {
					if entry == "public-refresh" {
						_, e := manager.RefreshRGB11State(context.Background())
						rgbDone <- e
					} else {
						_, e := manager.syncRootRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
						rgbDone <- e
					}
				}()
				select {
				case <-remote.started:
				case err := <-rgbDone:
					t.Fatalf("setup: real RGB entry ended before remote boundary: %v", err)
				case <-time.After(3 * time.Second):
					t.Fatal("setup: remote boundary not reached")
				}
				if !manager.channelIdentityMu.TryLock() {
					t.Fatal("RGB network request retained identity lock")
				}
				manager.channelIdentityMu.Unlock()
				if !manager.mutex.TryLock() {
					t.Fatal("setup: unexpected manager data lock owner")
				}
				manager.mutex.Unlock()
				unlocked := make(chan error, 1)
				password := "password"
				if wrong {
					password = "wrong-password"
				}
				go func() { _, err := manager.UnlockWallet(password); unlocked <- err }()
				var result error
				finished := false
				select {
				case result = <-unlocked:
					finished = true
				case <-time.After(2 * time.Second):
					stack := make([]byte, 128*1024)
					n := runtime.Stack(stack, true)
					for _, line := range strings.Split(string(stack[:n]), "\n") {
						if strings.Contains(line, "UnlockWallet(") || strings.Contains(line, "runRGB11ManagedOperationWithManager") || strings.Contains(line, "blockedAccountConfigHTTP).DKVSClientConfig(") {
							t.Log(line)
						}
					}
					t.Error("warm reauthentication waited for RGB remote request instead of checking local persisted credentials")
				}
				// Network access must not retain the lifecycle lock after reauthentication.
				if !manager.channelIdentityMu.TryLock() {
					t.Error("RGB request still retained identity lock")
				} else {
					manager.channelIdentityMu.Unlock()
				}
				release()
				select {
				case <-rgbDone:
				case <-time.After(4 * time.Second):
					t.Fatal("RGB cleanup stalled")
				}
				if !finished {
					select {
					case result = <-unlocked:
					case <-time.After(4 * time.Second):
						t.Fatal("unlock did not resume after network release")
					}
				}
				if wrong && result == nil {
					t.Error("incorrect persisted password accepted")
				}
				if !wrong && result != nil {
					t.Errorf("correct persisted password rejected: %v", result)
				}
				if manager.wallet != runtimeWallet || manager.status.CurrentWallet != selected || manager.accountGeneration != generation {
					t.Error("reauthentication changed live wallet/selection/account generation")
				}
			})
		}
	}
}
