package wallet

import (
	"testing"
	"time"
)

func TestActionMonitorStopCancelsBeforeWaiting(t *testing.T) {
	manager := &Manager{}
	manager.startActionMonitor()
	manager.actionMonitorLock.Lock()
	cancelInstalled := manager.actionMonitorCancel != nil
	manager.actionMonitorLock.Unlock()
	if !cancelInstalled {
		t.Fatal("action monitor did not install a cancellation context")
	}

	done := make(chan struct{})
	go func() {
		manager.stopActionMonitor()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("action monitor stop did not cancel and finish promptly")
	}
}
