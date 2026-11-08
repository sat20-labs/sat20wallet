package e2e

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var (
	runtimeDirsMu     sync.Mutex
	runtimeRoot       string
	runtimeFailedCase string
	runtimeKeepPaths  []string
)

// The process owns this fresh root. Shared build caches live until TestMain
// finishes; no cleanup scans or removes directories from an earlier run.
func e2eRuntimeDirectory(t *testing.T) string {
	t.Helper()
	runtimeDirsMu.Lock()
	defer runtimeDirsMu.Unlock()
	if runtimeRoot == "" {
		var err error
		runtimeRoot, err = os.MkdirTemp("", "sat20wallet-e2e-run-")
		require.NoError(t, err)
	}
	return runtimeRoot
}

// Retain the original files of at most one failed top-level case, including
// its nodes and child log. Never make another copy of the failure scene.
func keepFailedRuntime(t *testing.T, path string) bool {
	t.Helper()
	if !t.Failed() {
		return false
	}
	name, rest, _ := strings.Cut(t.Name(), "/")
	if name == "TestSDKCoreModulesE2E" && rest != "" {
		// Core now closes one independent environment per segment. Retain
		// only the first failed segment rather than all three environments.
		segment, _, _ := strings.Cut(rest, "/")
		name += "/" + segment
	}
	runtimeDirsMu.Lock()
	defer runtimeDirsMu.Unlock()
	if runtimeFailedCase == "" {
		runtimeFailedCase = name
	}
	if runtimeFailedCase != name {
		return false
	}
	runtimeKeepPaths = append(runtimeKeepPaths, path)
	t.Logf("failed case data retained in place: %s", path)
	return true
}

// Called after m.Run has completed all cleanup (including node kill/wait),
// while the process still owns the E2E runtime lock.
func cleanupE2ERuntime() error {
	if runtimeRoot == "" {
		return nil
	}
	var clean func(string) error
	clean = func(path string) error {
		for _, keep := range runtimeKeepPaths {
			if path == keep {
				return nil
			}
		}
		for _, keep := range runtimeKeepPaths {
			if strings.HasPrefix(keep, path+string(os.PathSeparator)) {
				entries, err := os.ReadDir(path)
				if err != nil {
					return err
				}
				for _, entry := range entries {
					if err := clean(filepath.Join(path, entry.Name())); err != nil {
						return err
					}
				}
				return nil
			}
		}
		return os.RemoveAll(path)
	}
	return clean(runtimeRoot)
}

func e2ePhase(t *testing.T, name string) func() {
	t.Helper()
	started := time.Now()
	return func() {
		t.Helper()
		t.Logf("e2e-phase: name=%s elapsed_seconds=%.3f", name, time.Since(started).Seconds())
	}
}

func testCaseDirectory(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(e2eRuntimeDirectory(t), "case-")
	require.NoError(t, err)
	t.Cleanup(func() {
		if keepFailedRuntime(t, dir) {
			return
		}
		require.NoError(t, os.RemoveAll(dir))
	})
	return dir
}

// Exercise the existing real-node fixture without depending on contract or
// account business rules: restart one node, then reuse all three slots.
func TestSatoshiNetRuntimeNodeLifecycle(t *testing.T) {
	for _, variant := range []string{"normal", "dkvs"} {
		t.Run(variant, func(t *testing.T) {
			checkSatoshiNetRuntimeNodeLifecycle(t, variant)
		})
	}
}

func checkSatoshiNetRuntimeNodeLifecycle(t *testing.T, variant string) {
	t.Helper()
	bootstrapKey := keyFromMnemonic(t, bootstrapMnemonic, 0)
	coreKey := keyFromMnemonic(t, coreMnemonic, 0)
	_, lockedScript, err := getP2WSHScript(bootstrapKey.PubKey().SerializeCompressed(), coreKey.PubKey().SerializeCompressed())
	require.NoError(t, err)
	previousDirs := make(map[string]string)
	previousFiles := make(map[string]os.FileInfo)
	for _, name := range []string{"first_case", "next_case"} {
		t.Run(name, func(t *testing.T) {
			t.Logf("runtime case start: %s", time.Now().Format(time.RFC3339Nano))
			t.Cleanup(func() { t.Logf("runtime case stopped: %s", time.Now().Format(time.RFC3339Nano)) })
			fakeL1 := newFakeL1Indexer(t, hex.EncodeToString(bootstrapKey.PubKey().SerializeCompressed()), lockedScript, nil)
			configureFastPOSTimers(t)
			var network *realSatoshiNet
			if variant == "dkvs" {
				network = newDKVSNoPluginNetworkWithArgs(t, fakeL1, nil, nil, nil)
			} else {
				network = newRealSatoshiNet(t, fakeL1)
			}
			for _, node := range network.Nodes {
				require.Equal(t, node.nodeDir, node.cmd.Dir)
				if name == "first_case" {
					previousDirs[node.role] = node.nodeDir
				} else {
					require.Equal(t, previousDirs[node.role], node.nodeDir)
					_, err := os.Stat(filepath.Join(node.nodeDir, "keep-on-restart"))
					require.True(t, os.IsNotExist(err), "previous case state was retained: %v", err)
				}
				plugin := "stpd.so"
				if node.role == "miner" {
					plugin = "wallet.so"
				}
				for _, path := range []string{node.cmd.Path, filepath.Join(node.nodeDir, plugin)} {
					info, err := os.Stat(path)
					require.NoError(t, err)
					if name == "first_case" {
						previousFiles[path] = info
					} else {
						require.True(t, os.SameFile(previousFiles[path], info), "runtime replaced: %s", path)
						require.Equal(t, previousFiles[path].ModTime(), info.ModTime(), "runtime rewritten: %s", path)
					}
				}
			}
			if name == "first_case" {
				marker := filepath.Join(network.Core.nodeDir, "keep-on-restart")
				require.NoError(t, os.WriteFile(marker, []byte("retain across restart"), 0o600))
				restartTestHarness(t, network.Core)
				require.FileExists(t, marker)
				require.NoError(t, connectNode(network.Core, network.Bootstrap))
				require.NoError(t, joinBlocks(network.Nodes))
			}
		})
	}
}

func TestSatoshiNetRuntimeDirectoryReuse(t *testing.T) {
	root := t.TempDir()
	artifacts := satoshinetArtifacts{
		coreExecutable: filepath.Join(root, "satoshinet-core-rpctest"),
		corePlugin:     filepath.Join(root, "stpd.so"),
	}
	for _, path := range []string{artifacts.coreExecutable, artifacts.corePlugin} {
		require.NoError(t, os.WriteFile(path, []byte("runtime-v1"), 0o755))
	}
	var firstDir string
	var firstExecutable, firstPlugin os.FileInfo
	t.Run("first_case", func(t *testing.T) {
		firstDir = prepareSatoshiNetNodeDir(t, artifacts, "core")
		var err error
		firstExecutable, err = os.Stat(filepath.Join(firstDir, filepath.Base(artifacts.coreExecutable)))
		require.NoError(t, err)
		firstPlugin, err = os.Stat(filepath.Join(firstDir, "stpd.so"))
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Join(firstDir, "data"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(firstDir, "data", "old.db"), []byte("old state"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(firstDir, "conf.yaml"), []byte("old config"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(firstDir, "wallet.pass"), []byte("old password"), 0o600))
		// A second live node with the same role must have its own directory.
		other := prepareSatoshiNetNodeDir(t, artifacts, "core")
		require.NotEqual(t, firstDir, other)
		require.FileExists(t, filepath.Join(firstDir, "data", "old.db"))
	})
	t.Run("next_case", func(t *testing.T) {
		dir := prepareSatoshiNetNodeDir(t, artifacts, "core")
		require.Equal(t, firstDir, dir)
		for _, name := range []string{"data", "conf.yaml", "wallet.pass"} {
			_, err := os.Stat(filepath.Join(dir, name))
			require.True(t, os.IsNotExist(err), "%s was not reset: %v", name, err)
		}
		for name, previous := range map[string]os.FileInfo{
			filepath.Base(artifacts.coreExecutable): firstExecutable, "stpd.so": firstPlugin,
		} {
			current, err := os.Stat(filepath.Join(dir, name))
			require.NoError(t, err)
			require.True(t, os.SameFile(previous, current), "%s was replaced", name)
			require.Equal(t, previous.ModTime(), current.ModTime(), "%s was rewritten", name)
		}
	})
}

func TestSatoshiNetRuntimeDirectoryUpdatesChangedFiles(t *testing.T) {
	root := t.TempDir()
	artifacts := satoshinetArtifacts{
		coreExecutable: filepath.Join(root, "satoshinet-core-rpctest"),
		corePlugin:     filepath.Join(root, "stpd.so"),
	}
	for _, path := range []string{artifacts.coreExecutable, artifacts.corePlugin} {
		require.NoError(t, os.WriteFile(path, []byte("runtime-v1"), 0o755))
	}
	var dir string
	t.Run("old_build", func(t *testing.T) {
		dir = prepareSatoshiNetNodeDir(t, artifacts, "bootstrap")
	})
	// Equal-size changes must also invalidate the installed executable/plugin.
	for _, path := range []string{artifacts.coreExecutable, artifacts.corePlugin} {
		require.NoError(t, os.WriteFile(path, []byte("runtime-v2"), 0o755))
	}
	t.Run("new_build", func(t *testing.T) {
		require.Equal(t, dir, prepareSatoshiNetNodeDir(t, artifacts, "bootstrap"))
		for _, name := range []string{filepath.Base(artifacts.coreExecutable), "stpd.so"} {
			data, err := os.ReadFile(filepath.Join(dir, name))
			require.NoError(t, err)
			require.Equal(t, "runtime-v2", string(data))
		}
	})
}
