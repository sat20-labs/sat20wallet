//go:build unix

package e2e

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestMain(m *testing.M) {
	lane := flag.String("sat20-e2e-lane", "", "isolated native E2E pilot lane (core or template)")
	flag.Parse()
	// Ownership helpers are child test binaries used only to exercise process
	// cleanup. They must not reacquire the package-wide E2E runtime lock held by
	// their parent, and they never start or clean SatoshiNet nodes.
	if isOwnedProcessTestHelper() {
		os.Exit(m.Run())
	}
	if err := flag.Set("test.parallel", "1"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := lockE2ERuntime(*lane); err != nil {
		fmt.Fprintln(os.Stderr, "lock SatoshiNet E2E runtime:", err)
		os.Exit(1)
	}
	rc := m.Run()
	started := time.Now()
	if err := cleanupE2ERuntime(); err != nil {
		fmt.Fprintln(os.Stderr, "clean owned E2E runtime:", err)
		rc = 1
	}
	fmt.Fprintf(os.Stderr, "e2e-phase: name=runtime_cleanup elapsed_seconds=%.3f\n", time.Since(started).Seconds())
	// Nodes inherit these descriptors. Close without explicitly unlocking so an
	// interrupted test cannot release shared directories while its nodes live.
	for _, lock := range satoshinetRuntimeLocks {
		_ = lock.Close()
	}
	os.Exit(rc)
}

func lockE2ERuntime(lane string) error {
	const splitPort = 25000
	lockPath := filepath.Join(os.TempDir(), "sat20wallet-satoshinet-rpctest.lock")
	mode := unix.LOCK_EX
	if lane != "" {
		selector := flag.Lookup("test.run").Value.String()
		switch lane {
		case "core":
			const childRun = "^TestSDKCoreModulesConnectedE2E$/^RGB11_fullsend_history_cold_recovery_reorg_and_competing_intent$"
			if selector != "^TestSDKCoreModulesE2E$" || os.Getenv("SAT20WALLET_CORE_E2E_CHILD_RUN") != childRun {
				return fmt.Errorf("core lane requires the native Core long-history selector")
			}
			lastHarnessPort = splitPort - 5
		case "template":
			if selector != "^TestSDKTemplateContractSharedNetwork$" {
				return fmt.Errorf("template lane requires the template contract shared-network selector")
			}
			nextHarnessPort = splitPort - 5
		default:
			return fmt.Errorf("unknown E2E pilot lane %q", lane)
		}
		mode = unix.LOCK_SH
	}
	// Default runs exclude every pilot lane. Pilots share the global lock and
	// hold their own exclusive lock for their assigned ports.
	if err := holdE2ELock(lockPath, mode); err != nil {
		return err
	}
	if lane != "" {
		if err := holdE2ELock(lockPath+"."+lane, unix.LOCK_EX); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "E2E pilot lane=%s ports=%d-%d\n", lane, nextHarnessPort+5, lastHarnessPort+4)
	}
	return nil
}

func holdE2ELock(path string, mode int) error {
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	err = unix.Flock(int(lock.Fd()), mode|unix.LOCK_NB)
	if err == unix.EWOULDBLOCK || err == unix.EAGAIN {
		fmt.Fprintln(os.Stderr, "waiting for the previous SatoshiNet E2E run and its nodes to exit")
		started := time.Now()
		err = unix.Flock(int(lock.Fd()), mode)
		fmt.Fprintf(os.Stderr, "e2e-phase: name=runtime_lock_wait elapsed_seconds=%.3f\n", time.Since(started).Seconds())
	}
	if err != nil {
		_ = lock.Close()
		return err
	}
	satoshinetRuntimeLocks = append(satoshinetRuntimeLocks, lock)
	return nil
}

func isOwnedProcessTestHelper() bool {
	switch os.Getenv("SAT20_TEST_OWNED_PROCESS_HELPER") {
	case "owner", "exit23":
		return true
	}
	switch os.Getenv("SAT20_TEST_OWNED_PROCESS_GROUP_HELPER") {
	case "leader", "member":
		return true
	}
	return false
}

func TestSatoshiNetRuntimeLockInheritedByNode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.lock")
	owner, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	require.NoError(t, err)
	defer owner.Close()
	require.NoError(t, unix.Flock(int(owner.Fd()), unix.LOCK_EX|unix.LOCK_NB))
	// Keep a child alive through a pipe, just as a node keeps the inherited
	// runtime lock alive if its test process exits unexpectedly.
	child := exec.Command("/bin/sh", "-c", "read -r line || true")
	child.ExtraFiles = []*os.File{owner}
	stdin, err := child.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, child.Start())
	t.Cleanup(func() { _ = stdin.Close(); _ = child.Process.Kill(); _ = child.Wait() })
	require.NoError(t, owner.Close())
	next, err := os.OpenFile(path, os.O_RDWR, 0o600)
	require.NoError(t, err)
	defer next.Close()
	err = unix.Flock(int(next.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	require.True(t, err == unix.EWOULDBLOCK || err == unix.EAGAIN,
		"live node must prevent another test from resetting its runtime: %v", err)
	require.NoError(t, stdin.Close())
	require.NoError(t, child.Wait())
	require.NoError(t, unix.Flock(int(next.Fd()), unix.LOCK_EX|unix.LOCK_NB))
}
