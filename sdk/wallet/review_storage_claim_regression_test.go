package wallet

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func reviewStorageAuthorization(t *testing.T, manager *Manager) *AccountStorageAuthorization {
	t.Helper()
	value, err := manager.ConfirmAccountStorage(AccountStorageTemporary, 0)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// The entry point below is shared by the real WASM recovery and guardian
// operations. Only their network work is replaced by a deterministic barrier.
func TestReviewStorageAuthorizationOperationOwnership(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	t.Run("one_authorization_one_consumer", func(t *testing.T) {
		manager, _, secret := buildRootWrapperSource(t)
		defer zeroBytes(secret)
		authorization := reviewStorageAuthorization(t, manager)
		entered, resume := make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(resume) }) }
		defer release()
		first := make(chan error, 1)
		go func() {
			first <- manager.UseAccountStorageAuthorization(AccountStoragePurposeRecovery,
				func(value *AccountStorageAuthorization) error {
					if value.ID != authorization.ID {
						return errors.New("first operation received the wrong authorization")
					}
					close(entered)
					<-resume
					return nil
				})
		}()
		select {
		case <-entered:
		case err := <-first:
			t.Fatalf("first operation did not start: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("first operation did not enter")
		}
		type attempt struct { called bool; err error }
		second := make(chan attempt, 1)
		go func() {
			called := false
			err := manager.UseAccountStorageAuthorization(AccountStoragePurposeRecovery,
				func(*AccountStorageAuthorization) error { called = true; return nil })
			second <- attempt{called: called, err: err}
		}()
		select {
		case result := <-second:
			if result.called || result.err == nil {
				t.Errorf("second operation claimed the already-used authorization: called=%v err=%v", result.called, result.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("duplicate claim blocked behind network I/O instead of failing")
		}
		release()
		select {
		case err := <-first:
			if err != nil { t.Fatalf("original owner failed: %v", err) }
		case <-time.After(5 * time.Second):
			t.Fatal("original owner did not complete")
		}
		if value, err := manager.PendingAccountStorageAuthorization(); value != nil || !errors.Is(err, ErrAccountStorageAuthorizationMissing) {
			t.Fatalf("successful owner did not consume authorization: value=%+v err=%v", value, err)
		}
	})

	t.Run("cancelled_old_completion_cannot_clear_new_authorization", func(t *testing.T) {
		manager, _, secret := buildRootWrapperSource(t)
		defer zeroBytes(secret)
		old := reviewStorageAuthorization(t, manager)
		entered, resume := make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(resume) }) }
		defer release()
		done := make(chan error, 1)
		go func() {
			done <- manager.UseAccountStorageAuthorization(AccountStoragePurposeRecovery,
				func(*AccountStorageAuthorization) error { close(entered); <-resume; return nil })
		}()
		select {
		case <-entered:
		case err := <-done:
			t.Fatalf("operation did not start: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("operation did not enter")
		}
		manager.CancelPendingAccountStorageAuthorization()
		fresh := reviewStorageAuthorization(t, manager)
		if fresh.ID == old.ID { t.Fatal("fixture did not create a new authorization") }
		release()
		select {
		case err := <-done:
			if !errors.Is(err, ErrAccountStorageAuthorizationMissing) {
				t.Errorf("cancelled owner reported success: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("cancelled owner did not finish")
		}
		current, err := manager.PendingAccountStorageAuthorization()
		if err != nil || current == nil || current.ID != fresh.ID {
			t.Fatalf("old completion erased or replaced the new authorization: current=%+v fresh=%s err=%v", current, fresh.ID, err)
		}
	})

	t.Run("failed_use_stays_bound_to_its_purpose_and_can_retry", func(t *testing.T) {
		manager, _, secret := buildRootWrapperSource(t)
		defer zeroBytes(secret)
		original := reviewStorageAuthorization(t, manager)
		failure := errors.New("simulated recoverable publication failure")
		err := manager.UseAccountStorageAuthorization(AccountStoragePurposeRecovery,
			func(*AccountStorageAuthorization) error { return failure })
		if !errors.Is(err, failure) { t.Fatalf("lost operation failure: %v", err) }
		called := false
		err = manager.UseAccountStorageAuthorization(AccountStoragePurposeGuardian,
			func(*AccountStorageAuthorization) error { called = true; return nil })
		if called || err == nil {
			t.Errorf("recovery authorization was silently reused for guardian setup: called=%v err=%v", called, err)
		}
		err = manager.UseAccountStorageAuthorization(AccountStoragePurposeRecovery,
			func(value *AccountStorageAuthorization) error {
				if value.ID != original.ID { return errors.New("retry received a different authorization") }
				return nil
			})
		if err != nil { t.Fatalf("same-purpose retry failed: %v", err) }
	})
}
