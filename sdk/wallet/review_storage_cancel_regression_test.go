package wallet

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestReviewStorageCancelInvalidatesInflightConfirmation(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	for _, cancelBeforeReturn := range []bool{false, true} {
		name := "successful_confirmation_control"
		if cancelBeforeReturn {
			name = "cancel_before_policy_returns"
		}
		t.Run(name, func(t *testing.T) {
			manager, _, secret := buildRootWrapperSource(t)
			defer zeroBytes(secret)
			remote := &blockedAccountConfigHTTP{
				rgb11MemoryDKVSHTTP: newRGB11MemoryDKVSHTTP(),
				started: make(chan struct{}), release: make(chan struct{}),
				continueAfterRelease: true,
			}
			manager.http = remote
			manager.dkvs.mu.Lock()
			manager.dkvs.clients = make(map[string]*SatsNetDKVSClient)
			manager.dkvs.mu.Unlock()
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(remote.release) }) }
			defer release()

			done := make(chan error, 1)
			go func() {
				_, err := manager.ConfirmAccountStorage(AccountStorageTemporary, 0)
				done <- err
			}()
			select {
			case <-remote.started:
			case err := <-done:
				t.Fatalf("confirmation did not reach the policy request: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("confirmation did not reach the policy request")
			}
			if cancelBeforeReturn {
				manager.CancelPendingAccountStorageAuthorization()
			}
			release()
			select {
			case err := <-done:
				if cancelBeforeReturn && !errors.Is(err, ErrAccountStorageAuthorizationMissing) {
					t.Errorf("cancelled confirmation must report invalid authorization, not success or a transport failure: %v", err)
				}
				if !cancelBeforeReturn && err != nil {
					t.Fatalf("successful control failed: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("confirmation did not finish after releasing the policy request")
			}
			value, err := manager.PendingAccountStorageAuthorization()
			if cancelBeforeReturn {
				if !errors.Is(err, ErrAccountStorageAuthorizationMissing) || value != nil {
					t.Fatalf("late confirmation resurrected a cancelled authorization: value=%+v err=%v", value, err)
				}
			} else if err != nil || value == nil || value.ID == "" || value.Mode != AccountStorageTemporary {
				t.Fatalf("successful control did not retain authorization: value=%+v err=%v", value, err)
			}
		})
	}
}
