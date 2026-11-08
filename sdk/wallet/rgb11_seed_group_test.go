package wallet

import "testing"

// A seed phase uses the group's actual endpoint. A later outer activation may
// legitimately publish records, so no lifetime zero-upload assertion belongs here.
func prepareRGBEndpoint(t *testing.T, manager *Manager, remote *rgb11MemoryDKVSHTTP) {
	t.Helper()
	if manager == nil || remote == nil {
		t.Fatal("RGB seed needs its original Manager and endpoint")
	}
	configureRGB11DKVSTestManager(manager, remote)
	t.Cleanup(func() {
		manager.StopDKVSSync()
		manager.stopAccountBackgroundTasks()
		manager.clearAccountManagementSession()
	})
	discoverInactiveAccount(t, manager)
}
