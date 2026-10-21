package e2e

import (
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

const defaultSDKPWARegtestBrowserPort = 5179

func parseSDKPWARegtestBrowserPort(value string) (int, error) {
	if value == "" {
		return defaultSDKPWARegtestBrowserPort, nil
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("SAT20_PWA_E2E_PORT must be an integer from 1 to 65535")
	}
	return port, nil
}

func sdkPWARegtestBrowserPort(t testing.TB) int {
	t.Helper()
	port, err := parseSDKPWARegtestBrowserPort(os.Getenv("SAT20_PWA_E2E_PORT"))
	require.NoError(t, err)
	return port
}

func sdkPWARegtestBrowserOrigin(t testing.TB) string {
	t.Helper()
	return fmt.Sprintf("http://127.0.0.1:%d", sdkPWARegtestBrowserPort(t))
}

func TestSDKPWARegtestBrowserPortSelection(t *testing.T) {
	t.Run("default preserves the existing port", func(t *testing.T) {
		t.Setenv("SAT20_PWA_E2E_PORT", "")
		port, err := parseSDKPWARegtestBrowserPort(os.Getenv("SAT20_PWA_E2E_PORT"))
		require.NoError(t, err)
		require.Equal(t, 5179, port)
	})
	t.Run("accepts an isolated browser port", func(t *testing.T) {
		t.Setenv("SAT20_PWA_E2E_PORT", "5189")
		port, err := parseSDKPWARegtestBrowserPort(os.Getenv("SAT20_PWA_E2E_PORT"))
		require.NoError(t, err)
		require.Equal(t, 5189, port)
	})
	for _, value := range []string{"0", "65536", "5189x", "-1"} {
		t.Run("rejects "+value, func(t *testing.T) {
			_, err := parseSDKPWARegtestBrowserPort(value)
			require.Error(t, err)
		})
	}
}
