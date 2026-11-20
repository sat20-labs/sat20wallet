package wallet

import (
	"errors"
	"testing"
	"time"
)

func TestDKVSStatusCallback(t *testing.T) {
	manager := &Manager{accountProfile: &accountManagementProfile{AccountID: "account"}}
	manager.dkvs = newDKVSManager(manager)
	notifications := make(chan AccountManagementStatus, 2)
	manager.dkvs.setStatusCallback(func() {
		notifications <- manager.GetAccountManagementStatus()
	})

	readNotification := func() AccountManagementStatus {
		t.Helper()
		done := make(chan struct{})
		go func() {
			manager.dkvs.notifyStatusCallback()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("status callback blocked while reading account status")
		}
		select {
		case status := <-notifications:
			return status
		case <-time.After(time.Second):
			t.Fatal("status callback did not publish an update")
			return AccountManagementStatus{}
		}
	}

	manager.dkvs.setLastSyncError(errors.New("remote DKVS unavailable"))
	status := readNotification()
	if !status.Active || status.LastDKVSSyncError == "" || status.LastDKVSSyncErrorCode != dkvsBackgroundSyncErrorCode || status.LastDKVSSyncErrorAt == 0 {
		t.Fatalf("status callback did not expose the sync error: %+v", status)
	}

	manager.dkvs.setLastSyncError(nil)
	status = readNotification()
	if status.LastDKVSSyncError != "" || status.LastDKVSSyncErrorCode != "" || status.LastDKVSSyncErrorAt != 0 {
		t.Fatalf("status callback did not expose the cleared sync error: %+v", status)
	}
}
