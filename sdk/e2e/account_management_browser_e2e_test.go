//go:build pwa

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

func TestPWADKVSActivationWatch(t *testing.T) {
	runAccountPWABrowser(t,
		"usage: SDK creation page delivers mnemonic without redundant PWA persistence",
		"PWA account: existing recovery file rehearsal abort and cold restore",
		"PWA account: root-only backup activates with temporary storage",
		"PWA account: private-key cold unlock and activation refusal",
	)
}

func TestPWARootBackup(t *testing.T) {
	runAccountPWABrowser(t,
		"PWA account: root-only backup activates with temporary storage",
		"PWA account: existing recovery file rehearsal abort and cold restore",
	)
}

func TestPWAImportDiscoveryPending(t *testing.T) {
	runAccountPWABrowser(t,
		"usage: import page and cold selection use SDK without redundant PWA persistence",
	)
}

func TestPWAImportDiscoveryFound(t *testing.T) {
	runAccountPWABrowser(t,
		"PWA root mnemonic import discovers latest account without recovery shares",
	)
}

func TestPWAUnlockNavigation(t *testing.T) {
	runAccountPWABrowser(t,
		"usage: maintenance rehearsal locks after inactivity and discards its session",
	)
}

func TestPWAAccountSwitch(t *testing.T) {
	runAccountPWABrowser(t,
		"usage: background catalog refresh cannot invalidate a subaccount switch",
	)
}

func TestPWAAccountOfflineWrite(t *testing.T) {
	runAccountPWABrowser(t,
		"usage: background backup failure and recovery remain visible on the account page",
	)
}

func TestPWAPasswordTxRollback(t *testing.T) {
	runAccountPWABrowser(t,
		"usage: browser password transaction failure preserves every wallet and original password",
	)
}

func TestPWATabAccountSwitch(t *testing.T) {
	runAccountPWABrowser(t,
		"usage: same-page subaccount switch discards a stale account response",
	)
}

func TestPWATabWalletSwitch(t *testing.T) {
	runAccountPWABrowser(t,
		"usage: committed wallet selection survives single Manager handoff",
	)
}

func TestPWASingleManager(t *testing.T) {
	runAccountPWABrowser(t,
		"usage: same-context PWA pages serialize Manager lifetime",
		"usage: refresh serializes PWA Manager startup",
		"usage: active Manager rejects a pending account write after same-page lock",
	)
}

func TestPWATabPasswordHandoff(t *testing.T) {
	runAccountPWABrowser(t,
		"usage: password changes apply on single Manager handoff",
	)
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
