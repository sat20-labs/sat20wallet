package e2e

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The production build runs in a copy, with current production WASM and its
// matching Go runtime. The checkout's public/, generated/ and dist/ are untouched.
func TestPWAProductionReleaseE2E(t *testing.T) {
	runPWAReleaseE2E(t, false)
}

func TestPWARegtestReleaseE2E(t *testing.T) {
	runPWAReleaseE2E(t, true)
}

func runPWAReleaseE2E(t *testing.T, regtest bool) {
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	sdk := filepath.Dir(filepath.Dir(source))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if deadline, ok := t.Deadline(); ok {
		var deadlineCancel context.CancelFunc
		ctx, deadlineCancel = context.WithDeadline(ctx, deadline.Add(-30*time.Second))
		defer deadlineCancel()
	}
	wasm, runtimeJS := buildPWAWalletRuntime(t, ctx, sdk)
	args := []string{"scripts/verify/pwa-release-e2e.mjs", wasm, runtimeJS, t.TempDir()}
	if regtest {
		args = append(args, "--regtest")
		switch os.Getenv("SAT20_PWA_RELEASE_CONTINUE") {
		case "after-build":
			args = append(args, "--continue-after-build")
		case "page-errors":
			args = append(args, "--page-errors-only")
		}
	}
	command := exec.CommandContext(ctx, "node", args...)
	command.Dir = filepath.Join(sdk, "..", "pwa")
	var output bytes.Buffer
	command.Stdout = io.MultiWriter(&output, os.Stdout)
	command.Stderr = command.Stdout
	err := command.Run()
	require.NoError(t, err, "production PWA release gate failed:\n%s", output.String())
}
