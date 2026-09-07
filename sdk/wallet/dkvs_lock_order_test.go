package wallet

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestDKVSDeferredJobDoesNotStarveIndependentJobs(t *testing.T) {
	manager := &dkvsManager{jobs: make(map[string]func(*dkvsStore) error)}
	store := &dkvsStore{}
	runs := make([]string, 0, 2)
	manager.schedule(accountRootWrapperJobID, func(*dkvsStore) error {
		runs = append(runs, "wrapper")
		return errDKVSJobDeferred
	})
	manager.schedule(dkvsNotificationJobID, func(*dkvsStore) error {
		runs = append(runs, "notification")
		return nil
	})
	if err := manager.runPendingJobs(store); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(runs, []string{"wrapper", "notification"}) {
		t.Fatalf("job runs=%v", runs)
	}
	manager.mu.Lock()
	deferred := manager.jobs[accountRootWrapperJobID]
	_, duplicateNotification := manager.jobs[dkvsNotificationJobID]
	manager.mu.Unlock()
	if deferred == nil || duplicateNotification {
		t.Fatalf("deferred=%v duplicate notification=%v", deferred != nil, duplicateNotification)
	}
	if !errors.Is(deferred(store), errDKVSJobDeferred) {
		t.Fatal("deferred job was not preserved")
	}
}

func TestAccountOperationDoesNotHoldCoordinatorDuringLongWork(t *testing.T) {
	manager := &Manager{}
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- manager.runAccountOperation(context.Background(), func() error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	if !manager.accountSyncMu.TryLock() {
		close(release)
		t.Fatal("account coordinator mutex is held while operation work is blocked")
	}
	manager.accountSyncMu.Unlock()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDKVSTransportDoesNotHoldCoordinatorDuringRequest(t *testing.T) {
	manager := &dkvsManager{requestCtx: context.Background()}
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- manager.runTransport(func() error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	if !manager.runMu.TryLock() {
		close(release)
		t.Fatal("DKVS coordinator mutex is held while transport is blocked")
	}
	manager.runMu.Unlock()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDKVSNotificationsCoalesceAndRunOutsideInternalLocks(t *testing.T) {
	owner := &Manager{}
	manager := &dkvsManager{
		owner: owner, jobs: make(map[string]func(*dkvsStore) error),
		pendingNotifies: make(map[string]struct{}),
	}
	observerCalls, callbackCalls := 0, 0
	var observed []string
	manager.addObserver(func(paths []string) {
		observerCalls++
		observed = append([]string(nil), paths...)
		if !manager.mu.TryLock() {
			t.Fatal("observer ran while DKVS state mutex was held")
		}
		manager.mu.Unlock()
		if !manager.runMu.TryLock() {
			t.Fatal("observer ran while DKVS coordinator mutex was held")
		}
		manager.runMu.Unlock()
		if !owner.accountSyncMu.TryLock() {
			t.Fatal("observer ran while account coordinator mutex was held")
		}
		owner.accountSyncMu.Unlock()
	})
	manager.setCallback(func() { callbackCalls++ })
	manager.enqueueNotifications([]string{"beta", "alpha", "alpha"})
	manager.enqueueNotifications([]string{"gamma", "alpha"})

	manager.mu.Lock()
	if len(manager.jobs) != 1 || len(manager.pendingNotifies) != 3 || !manager.notifyQueued {
		manager.mu.Unlock()
		t.Fatalf("notifications were not coalesced: jobs=%d targets=%d queued=%v",
			len(manager.jobs), len(manager.pendingNotifies), manager.notifyQueued)
	}
	job := manager.jobs[dkvsNotificationJobID]
	manager.mu.Unlock()
	if job == nil {
		t.Fatal("coalesced notification job is unavailable")
	}
	if err := job(nil); err != nil {
		t.Fatal(err)
	}
	if observerCalls != 1 || callbackCalls != 1 ||
		!reflect.DeepEqual(observed, []string{"alpha", "beta", "gamma"}) {
		t.Fatalf("observer calls=%d callback calls=%d paths=%v",
			observerCalls, callbackCalls, observed)
	}
}

func TestDKVSPendingAccountJobDoesNotHoldRunMuWhileWaitingForAccountSync(t *testing.T) {
	manager, _ := finalDKVSTestManager(t)
	store, err := manager.ensureDKVSManager().primaryStore()
	if err != nil {
		t.Fatal(err)
	}
	key := accountTestKey(t, manager, "lock-order/value")

	manager.accountSyncMu.Lock()
	accountLockHeld := true
	defer func() {
		if accountLockHeld {
			manager.accountSyncMu.Unlock()
		}
	}()

	jobStarted := make(chan struct{})
	jobAcquiredAccountLock := make(chan struct{})
	manager.dkvs.schedule(accountRootWrapperJobID, func(*dkvsStore) error {
		close(jobStarted)
		manager.accountSyncMu.Lock()
		close(jobAcquiredAccountLock)
		manager.accountSyncMu.Unlock()
		return nil
	})

	syncDone := make(chan error, 1)
	go func() {
		_, syncErr := manager.syncDKVSOnce()
		syncDone <- syncErr
	}()

	select {
	case <-jobStarted:
	case <-time.After(2 * time.Second):
		manager.accountSyncMu.Unlock()
		accountLockHeld = false
		t.Fatal("pending account job did not start")
	}

	refreshDone := make(chan error, 1)
	go func() {
		refreshDone <- store.Refresh(key)
	}()

	select {
	case refreshErr := <-refreshDone:
		if refreshErr != nil {
			manager.accountSyncMu.Unlock()
			accountLockHeld = false
			<-syncDone
			t.Fatal(refreshErr)
		}
	case <-time.After(2 * time.Second):
		manager.accountSyncMu.Unlock()
		accountLockHeld = false
		<-syncDone
		<-refreshDone
		t.Fatal("DKVS Refresh blocked on runMu while a pending account job waited for accountSyncMu")
	}

	manager.accountSyncMu.Unlock()
	accountLockHeld = false
	select {
	case <-jobAcquiredAccountLock:
	case <-time.After(2 * time.Second):
		t.Fatal("pending account job did not resume after accountSyncMu was released")
	}
	select {
	case syncErr := <-syncDone:
		if syncErr != nil {
			t.Fatal(syncErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DKVS sync did not finish after pending account job completed")
	}
}
