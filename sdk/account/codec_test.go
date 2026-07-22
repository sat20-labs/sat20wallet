package account

import (
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocatorEncodingRoundTrip(t *testing.T) {
	locator := Locator{
		Version:      Version,
		AccountID:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		PackageID:    "0123456789abcdef0123456789abcdef",
		RecoveryMode: RecoveryMode2Of3,
	}
	encoded, err := EncodeLocator(locator)
	require.NoError(t, err)
	decoded, err := DecodeLocator(encoded)
	require.NoError(t, err)
	require.Equal(t, locator, decoded)
}

func TestRecoveryShareEncodingRoundTrip(t *testing.T) {
	secret := make([]byte, accountSecretSize)
	_, err := rand.Read(secret)
	require.NoError(t, err)
	shares, err := splitSecret(secret, "0123456789abcdef0123456789abcdef", RecoveryMode2Of3, rand.Reader)
	require.NoError(t, err)
	encoded, err := EncodeRecoveryShare(shares[0])
	require.NoError(t, err)
	decoded, err := DecodeRecoveryShare(encoded)
	require.NoError(t, err)
	require.Equal(t, shares[0], decoded)
}
