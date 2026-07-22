package rgb11wallet

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAutoBackupPolicySupportsContinuousAutopay(t *testing.T) {
	policy := &RGB11AutoBackupPolicy{Version: 1, Enabled: true}
	encoded, err := EncodeAutoBackupPolicy(policy)
	require.NoError(t, err)
	decoded, err := DecodeAutoBackupPolicy(encoded)
	require.NoError(t, err)
	require.Equal(t, policy, decoded)
}

func TestAutoBackupPolicyRejectsHeightOnlyLease(t *testing.T) {
	_, err := EncodeAutoBackupPolicy(&RGB11AutoBackupPolicy{
		Version: 1, Enabled: true, ExpiryHeight: 100,
	})
	require.ErrorIs(t, err, ErrRGB11Inconsistent)
}
