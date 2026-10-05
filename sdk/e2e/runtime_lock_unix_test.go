//go:build unix

package e2e

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestMain(m *testing.M) {
	flag.Parse()
	// Fixed node directories are shared by successive test processes. Serialize
	// the whole E2E run, including builds, before touching either runtime variant.
	if err := flag.Set("test.parallel", "1"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	lock, err := os.OpenFile(filepath.Join(os.TempDir(), "sat20wallet-satoshinet-rpctest.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open SatoshiNet E2E runtime lock:", err)
		os.Exit(1)
	}
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
			fmt.Fprintln(os.Stderr, "lock SatoshiNet E2E runtime:", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "waiting for the previous SatoshiNet E2E run and its nodes to exit")
		if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
			fmt.Fprintln(os.Stderr, "lock SatoshiNet E2E runtime:", err)
			os.Exit(1)
		}
	}
	satoshinetRuntimeLock = lock
	rc := m.Run()
	// Nodes inherit this descriptor. Close without explicitly unlocking so an
	// interrupted test cannot release shared directories while its nodes live.
	_ = lock.Close()
	os.Exit(rc)
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
