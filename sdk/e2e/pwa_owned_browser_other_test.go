//go:build !darwin

package e2e

import (
	"os/exec"
	"testing"
)

// This change is verified on macOS; other platforms retain their existing path.
func configureOwnedPWACancellation(_ *testing.T, _ *exec.Cmd) func() error {
	return func() error { return nil }
}
