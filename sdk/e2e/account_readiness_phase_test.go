package e2e

import (
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Discovery is an identity/lifecycle preparation phase, not an activation.
// Never overwrite profile flags or re-encrypt credentials after a cold unlock.
func requireE2EInactive(t *testing.T, manager *wallet.Manager) {
	t.Helper()
	require.NoError(t, manager.StartDKVSSync())
	t.Cleanup(manager.StopDKVSSync)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		status := manager.GetAccountManagementStatus()
		assert.Equal(c, "inactive", status.DiscoveryStatus)
		assert.True(c, status.LocalOnly)
		assert.False(c, status.RecoveryConfigured)
		assert.False(c, status.ActivationPending)
	}, 20*time.Second, 20*time.Millisecond, "real inactive discovery before local business or storage authorization")
}

// Activation returns before the asynchronous private backup is confirmed.
// A new device can recover only after that actual backup has completed.
func requireE2EFirstBackup(t *testing.T, manager *wallet.Manager) {
	t.Helper()
	// Observing readiness must not enqueue another sync and mask a missing
	// SDK wake-up. Activation/local edits already schedule their own backup.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		status := manager.GetAccountManagementStatus()
		assert.True(c, status.Active)
		assert.True(c, status.RecoveryConfigured)
		assert.False(c, status.ActivationPending)
		assert.True(c, status.DKVSWriteAllowed)
		assert.Equal(c, "backed_up", status.BackupState)
		assert.False(c, status.ManagedDataDirty)
		assert.Zero(c, status.PendingChanges)
	}, 30*time.Second, 20*time.Millisecond, "wait for the actual private recovery backup without requesting synchronization")
}
