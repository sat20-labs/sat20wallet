package wallet

import (
	"errors"
	"testing"
)

func TestAccountManagementStatusExposesLastDKVSSyncError(t *testing.T) {
	manager := &Manager{accountProfile: &accountManagementProfile{AccountID: "account"}}
	manager.dkvs = newDKVSManager(manager)
	manager.dkvs.setLastSyncError(errors.New("remote DKVS unavailable"))

	status := manager.GetAccountManagementStatus()
	if !status.Active || status.LastDKVSSyncError == "" || status.LastDKVSSyncErrorCode != dkvsBackgroundSyncErrorCode || status.LastDKVSSyncErrorAt == 0 {
		t.Fatalf("missing DKVS error status: %+v", status)
	}
	manager.dkvs.setLastSyncError(nil)
	status = manager.GetAccountManagementStatus()
	if status.LastDKVSSyncError != "" || status.LastDKVSSyncErrorCode != "" || status.LastDKVSSyncErrorAt != 0 {
		t.Fatalf("DKVS error status was not cleared: %+v", status)
	}
}
