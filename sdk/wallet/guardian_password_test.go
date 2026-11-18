//go:build guardian

package wallet

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountGuardianPasswordAuthenticationAndRotation(t *testing.T) {
	manager, _, _ := newInactiveAccountGroup(t, "", "original-password")
	_, err := manager.GetOrCreateAccountGuardianIdentity("incorrect-password")
	require.Error(t, err, "a first guardian identity must authenticate the wallet password")
	identity, err := manager.GetOrCreateAccountGuardianIdentity("original-password")
	require.NoError(t, err)
	original, err := manager.LoadAccountGuardianPrivateKey("original-password")
	require.NoError(t, err)
	defer zeroBytes(original)
	database := manager.db
	manager.db = &accountGuardianReadErrorDB{KVDB: database}
	_, err = manager.GetOrCreateAccountGuardianIdentity("original-password")
	require.ErrorContains(t, err, "guardian storage read failed")
	manager.db = &passwordChangeFailFlushDB{KVDB: database}
	require.Error(t, manager.ChangePassword("original-password", "replacement-password"))
	manager.db = database
	unchanged, err := manager.LoadAccountGuardianPrivateKey("original-password")
	require.NoError(t, err)
	require.Equal(t, original, unchanged)
	zeroBytes(unchanged)
	require.NoError(t, manager.ChangePassword("original-password", "replacement-password"))
	_, err = manager.LoadAccountGuardianPrivateKey("original-password")
	require.Error(t, err)
	restored, err := manager.LoadAccountGuardianPrivateKey("replacement-password")
	require.NoError(t, err)
	require.Equal(t, original, restored)
	zeroBytes(restored)
	next, err := manager.GetOrCreateAccountGuardianIdentity("replacement-password")
	require.NoError(t, err)
	require.Equal(t, identity, next)
}
