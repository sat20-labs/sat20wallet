package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Browser dependencies are required by the ordinary account E2E gate.
// The browser uses the same real temporary node fixture.
func TestSDKAccountPWAConnectedBrowser(t *testing.T) {
	runAccountPWABrowser(t)
}

func runAccountPWABrowser(t *testing.T, cases ...string) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	pwa := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "pwa"))
	// This gate includes native WASM storage checks, node preparation and all
	// account usage scenarios; individual browser operation timeouts still apply.
	// The 66-case gate includes fresh-device Guardian recoveries using only
	// page-copied material and the real 30-second background refresh boundary.
	// This is the whole-run resource budget, not a browser assertion timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	// The full browser gate first exercises the actual Go WASM KVDB against native
	// IndexedDB; no mocked database and no separate test runner are needed.
	if len(cases) == 0 {
		storageDir := t.TempDir()
		storageWasm := filepath.Join(storageDir, "storage.test.wasm")
		compile := exec.CommandContext(ctx, "go", "test", "-c", "-o", storageWasm, "./wallet/lightnode")
		compile.Dir = filepath.Join(pwa, "..", "sdk")
		compile.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
		compileOutput, err := compile.CombinedOutput()
		require.NoError(t, err, "compile WASM storage tests: %s", compileOutput)
		runtimeJS, err := os.ReadFile(filepath.Join(runtime.GOROOT(), "lib", "wasm", "wasm_exec.js"))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(storageDir, "wasm_exec.js"), runtimeJS, 0600))
		storage := exec.CommandContext(ctx, "node", "scripts/verify/account-management-e2e.mjs", "--storage-wasm", storageWasm)
		storage.Dir = pwa
		var storageOutput bytes.Buffer
		storage.Stdout = io.MultiWriter(&storageOutput, os.Stdout)
		storage.Stderr = storage.Stdout
		require.NoError(t, storage.Run(), "WASM storage gate failed:\n%s", storageOutput.String())
	}
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	wasmPath, runtimePath := buildPWAWalletRuntime(t, ctx, filepath.Join(pwa, "..", "sdk"))
	t.Setenv("SAT20_PWA_E2E_WASM", wasmPath)
	t.Setenv("SAT20_PWA_E2E_WASM_RUNTIME", runtimePath)
	config, _ := accountReviewConfig(t, network)
	encoded, err := json.Marshal(config)
	require.NoError(t, err)
	args := []string{"scripts/verify/account-management-e2e.mjs"}
	if len(cases) > 0 {
		args = append(args, "--usage-cases")
		args = append(args, cases...)
	}
	command := exec.CommandContext(ctx, "node", args...)
	command.Dir = pwa
	command.Env = append(os.Environ(), "SAT20_ACCOUNT_E2E_CONFIG="+string(encoded))
	var output bytes.Buffer
	writer := io.MultiWriter(&output, os.Stdout)
	command.Stdout, command.Stderr = writer, writer
	require.NoError(t, command.Run(), "PWA account gate failed:\n%s", output.String())
}
