package wallet

import "testing"

const accountGroupUnprepared accountGroupState = "Unprepared"

// The outer business group chooses its account state for each Manager.
// Inactive discovery needs the real endpoint/replica validation path, not
// encrypted credentials or a mnemonic inferred from common.Wallet.
func prepareRGBGroup(t *testing.T, manager *Manager, state accountGroupState,
	credentials ...string) {
	t.Helper()
	if manager == nil {
		t.Fatal("RGB account group needs its original Manager")
	}
	if state == accountGroupReady {
		if len(credentials) != 2 || credentials[0] == "" || credentials[1] == "" {
			t.Fatal("active RGB group requires explicit matching mnemonic and password")
		}
		prepareAccountGroup(t, manager, state, credentials[0], credentials[1])
		return
	}
	if state != accountGroupInactive {
		t.Fatalf("unsupported RGB account group state %q", state)
	}
	remote := newRGB11MemoryDKVSHTTP()
	configureRGB11DKVSTestManager(manager, remote)
	t.Cleanup(func() {
		manager.StopDKVSSync()
		manager.stopAccountBackgroundTasks()
		manager.clearAccountManagementSession()
	})
	discoverInactiveAccount(t, manager)
	remote.mu.Lock()
	snapshots := remote.snapshotCalls
	remote.mu.Unlock()
	if snapshots == 0 {
		t.Fatal("inactive RGB group did not receive an endpoint snapshot")
	}
	t.Cleanup(func() {
		remote.mu.Lock()
		count := len(remote.records)
		remote.mu.Unlock()
		if count != 0 {
			t.Errorf("inactive RGB business group uploaded %d records", count)
		}
	})
}
