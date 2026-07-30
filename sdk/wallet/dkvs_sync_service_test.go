package wallet

import (
	"strings"
	"testing"
	"time"
)

func TestDKVSManagerStopWaitsForWorker(t *testing.T) {
	manager := &Manager{}
	dkvs := newDKVSManager(manager)
	stop := make(chan struct{})
	done := make(chan struct{})
	dkvs.stop = stop
	dkvs.done = done

	stopped := make(chan struct{})
	go func() {
		dkvs.stopAndWait()
		close(stopped)
	}()

	select {
	case <-stop:
	case <-time.After(time.Second):
		t.Fatal("background sync worker was not stopped")
	}
	select {
	case <-stopped:
		t.Fatal("stop returned before the worker exited")
	case <-time.After(20 * time.Millisecond):
	}

	close(done)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop did not return after the worker exited")
	}
	if dkvs.stop != nil || dkvs.done != nil {
		t.Fatal("background sync state was not cleared")
	}
}

func TestDKVSManagerWakeKeepsWorkerState(t *testing.T) {
	manager := newDKVSManager(&Manager{})
	stop := make(chan struct{})
	done := make(chan struct{})
	wake := make(chan struct{}, 1)
	manager.stop = stop
	manager.done = done
	manager.wake = wake

	manager.wakeSync()

	if manager.stop != stop || manager.done != done || manager.wake != wake {
		t.Fatal("waking DKVS synchronization replaced the active worker")
	}
	select {
	case <-wake:
	default:
		t.Fatal("waking DKVS synchronization did not notify the active worker")
	}
}

func TestDKVSManagerReusesEndpointSession(t *testing.T) {
	manager := newDKVSManager(&Manager{})
	first, err := manager.clientFor("http", "dkvs.test", "testnet", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.clientFor("http", "dkvs.test", "testnet", nil)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("DKVS endpoint session was not reused")
	}
}

func TestDKVSManagerManagesCollectionAndExactKeys(t *testing.T) {
	accountID := strings.Repeat("0", 64)
	manager := newDKVSManager(&Manager{})
	managed := []string{
		"/personal/" + accountID + "/rgb/head",
		"/svc/autopay/config",
		"/mail/" + accountID + "/msg/" + accountID + "/message",
		"/blob/" + accountID + "/snapshot",
	}
	for _, key := range managed {
		if !manager.managesKey(key) {
			t.Fatalf("expected collection key to be managed: %s", key)
		}
	}
	exact := []string{
		"/account/testnet/tb1qexample",
		"/name/example",
		"/tmp/example",
		"/sys/params",
	}
	for _, key := range exact {
		if !manager.managesKey(key) {
			t.Fatalf("expected exact key to be managed: %s", key)
		}
	}
	if manager.managesKey("/invalid/key") {
		t.Fatal("invalid key must not be treated as a managed collection key")
	}
}
