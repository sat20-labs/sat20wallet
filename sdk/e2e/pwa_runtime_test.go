package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Each browser entry builds the current checkout in a private directory.
// The PWA's normal integrity loader verifies these bytes; public/ is untouched.
func buildPWAWalletRuntime(t *testing.T, ctx context.Context, sdk string, posActivation ...int32) (string, string) {
	t.Helper()
	dir := t.TempDir()
	wasmPath := filepath.Join(dir, "wallet.wasm")
	require.LessOrEqual(t, len(posActivation), 1)
	args := []string{"build", "-ldflags=-s -w", "-o", wasmPath}
	if len(posActivation) == 1 {
		require.Positive(t, posActivation[0])
		// The node's rpctest override lives in package main, so it does not reach
		// browser SDKs. Configure the same private-chain schedule at build time;
		// no release source, loader, signing rule or production parameter changes.
		source := filepath.Clean(filepath.Join(sdk, "..", "..", "satoshinet", "chaincfg", "pos.go"))
		replacements := make(map[string]string)
		goFlags := strings.Fields(os.Getenv("GOFLAGS"))
		for i := 0; i < len(goFlags); i++ {
			flag := strings.Trim(goFlags[i], "\"'")
			var base string
			if flag == "-overlay" {
				require.Less(t, i+1, len(goFlags), "GOFLAGS -overlay requires the frozen candidate path")
				i++
				base = strings.Trim(goFlags[i], "\"'")
			} else if strings.HasPrefix(flag, "-overlay=") {
				base = strings.TrimPrefix(flag, "-overlay=")
			}
			if base == "" {
				continue
			}
			var inherited struct {
				Replace map[string]string `json:"Replace"`
			}
			encoded, err := os.ReadFile(base)
			require.NoError(t, err, "read inherited GOFLAGS overlay")
			require.NoError(t, json.Unmarshal(encoded, &inherited), "decode inherited GOFLAGS overlay")
			require.NotEmpty(t, inherited.Replace, "the inherited candidate overlay must contain source replacements")
			for original, replacement := range inherited.Replace {
				replacements[original] = replacement
			}
		}
		contentSource := source
		if inheritedSource := replacements[source]; inheritedSource != "" {
			contentSource = inheritedSource
		}
		content, err := os.ReadFile(contentSource)
		require.NoError(t, err, "read the effective frozen SatoshiNet POS source")
		content = append(content, []byte(fmt.Sprintf("\nfunc init() { TestNetParams.POSV2Height = %d; RegressionNetParams.POSV2Height = %d }\n", posActivation[0], posActivation[0]))...)
		overlaySource := filepath.Join(dir, "pos.go")
		require.NoError(t, os.WriteFile(overlaySource, content, 0600))
		replacements[source] = overlaySource
		mapping, err := json.Marshal(map[string]any{"Replace": replacements})
		require.NoError(t, err)
		overlay := filepath.Join(dir, "network-overlay.json")
		require.NoError(t, os.WriteFile(overlay, mapping, 0600))
		args = append(args, "-overlay", overlay)
		t.Logf("PWA test WASM uses the isolated POS v2 activation height %d", posActivation[0])
	}
	args = append(args, "./wasm")
	compile := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), args...)
	compile.Dir = sdk
	compile.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	output, err := compile.CombinedOutput()
	require.NoError(t, err, "compile current wallet WASM: %s", output)
	runtimeJS, err := os.ReadFile(filepath.Join(runtime.GOROOT(), "lib", "wasm", "wasm_exec.js"))
	require.NoError(t, err)
	runtimePath := filepath.Join(dir, "wasm_exec.js")
	require.NoError(t, os.WriteFile(runtimePath, runtimeJS, 0600))
	return wasmPath, runtimePath
}
