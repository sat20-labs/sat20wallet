package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type ownedProcessFixture struct {
	command             *exec.Cmd
	cancel              context.CancelFunc
	waited              chan error
	group               int
	pids                []int
	closeOwner          func() error
	waitDone            chan struct{}
	expectedUnconfirmed bool
	identities          []ownedPWABrowser
	input               io.WriteCloser
	lines               chan string
}

type ownedProcessReport struct {
	Browser int    `json:"browser,omitempty"`
	Pids    []int  `json:"pids,omitempty"`
	Member  int    `json:"member,omitempty"`
	Probe   string `json:"probe,omitempty"`
}

// The helper subprocess is the registered "owner" process. Its owned process
// group consists only of local Go test helpers, never Chromium or Playwright.
func TestPWAOwnedProcessHelper(t *testing.T) {
	mode := os.Getenv("SAT20_TEST_OWNED_PROCESS_HELPER")
	if mode == "" {
		return
	}
	if mode == "exit23" {
		os.Exit(23)
	}
	require.Equal(t, "owner", mode)

	if os.Getenv("SAT20_TEST_IGNORE_INTERRUPT") == "1" {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt)
		defer signal.Stop(signals)
		go func() {
			for range signals {
			}
		}()
	}

	executable, err := os.Executable()
	require.NoError(t, err)
	group := exec.Command(executable, "-test.run=^TestPWAOwnedProcessGroupHelper$", "-test.v")
	group.Env = append(os.Environ(), "SAT20_TEST_OWNED_PROCESS_GROUP_HELPER=leader")
	group.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	groupInput, err := group.StdinPipe()
	require.NoError(t, err)
	groupOutput, err := group.StdoutPipe()
	require.NoError(t, err)
	group.Stderr = os.Stderr
	require.NoError(t, group.Start())

	output := bufio.NewScanner(groupOutput)
	member := readOwnedMember(t, output)
	report := ownedProcessReport{Browser: group.Process.Pid, Pids: []int{group.Process.Pid, member}}
	raw, err := json.Marshal(report)
	require.NoError(t, err)
	_, err = fmt.Fprintln(os.Stdout, string(raw))
	require.NoError(t, err)

	registered := false
	register := func() {
		t.Helper()
		if registered {
			return
		}
		writeOwnedProcessRegistration(t, report.Browser, report.Pids)
		registered = true
	}
	if os.Getenv("SAT20_TEST_LATE_OWNER") != "1" {
		register()
	}

	input := bufio.NewScanner(os.Stdin)
	for input.Scan() {
		switch strings.TrimSpace(input.Text()) {
		case "register":
			register()
		case "probe":
			_, err := fmt.Fprintln(groupInput, "probe")
			require.NoError(t, err)
			for output.Scan() {
				line := output.Text()
				var result ownedProcessReport
				if json.Unmarshal([]byte(line), &result) == nil && result.Probe != "" {
					_, err = fmt.Fprintln(os.Stdout, line)
					require.NoError(t, err)
					break
				}
			}
		}
	}
}

func writeOwnedProcessRegistration(t *testing.T, group int, pids []int) {
	t.Helper()
	fd, err := strconv.Atoi(os.Getenv("SAT20_PWA_BROWSER_OWNER_FD"))
	require.NoError(t, err)
	require.GreaterOrEqual(t, fd, 3)
	owner := os.NewFile(uintptr(fd), "owner-registration")
	require.NotNil(t, owner)
	defer owner.Close()
	raw, err := json.Marshal(struct {
		Node    int   `json:"node"`
		Browser int   `json:"browser"`
		Pids    []int `json:"pids"`
	}{Node: os.Getpid(), Browser: group, Pids: pids})
	require.NoError(t, err)
	_, err = fmt.Fprintln(owner, string(raw))
	require.NoError(t, err)
}

// This child remains in its parent's process group and echoes a small probe.
// It makes group cleanup observable without launching a browser.
func TestPWAOwnedProcessGroupHelper(t *testing.T) {
	mode := os.Getenv("SAT20_TEST_OWNED_PROCESS_GROUP_HELPER")
	switch mode {
	case "":
		return
	case "member":
		for {
			time.Sleep(time.Hour)
		}
	case "leader":
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}

	executable, err := os.Executable()
	require.NoError(t, err)
	member := exec.Command(executable, "-test.run=^TestPWAOwnedProcessGroupHelper$")
	member.Env = append(os.Environ(), "SAT20_TEST_OWNED_PROCESS_GROUP_HELPER=member")
	member.Stdin, member.Stdout, member.Stderr = os.Stdin, io.Discard, io.Discard
	require.NoError(t, member.Start())
	_, err = fmt.Fprintf(os.Stdout, "{\"member\":%d}\n", member.Process.Pid)
	require.NoError(t, err)

	input := bufio.NewScanner(os.Stdin)
	for input.Scan() {
		switch strings.TrimSpace(input.Text()) {
		case "probe":
			_, err := fmt.Fprintln(os.Stdout, "{\"probe\":\"B still functional\"}")
			require.NoError(t, err)
		case "exit":
			return
		}
	}
}

func readOwnedMember(t *testing.T, scanner *bufio.Scanner) int {
	t.Helper()
	for scanner.Scan() {
		var report ownedProcessReport
		if json.Unmarshal(scanner.Bytes(), &report) == nil && report.Member > 1 {
			return report.Member
		}
	}
	require.NoError(t, scanner.Err())
	t.Fatal("owned process-group helper did not report its member")
	return 0
}

func awaitOwnedProcessReport(t *testing.T, lines <-chan string, timeout time.Duration,
	accept func(ownedProcessReport) bool) (ownedProcessReport, string) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case raw, ok := <-lines:
			if !ok {
				t.Fatal("owned helper exited before reporting process state")
				return ownedProcessReport{}, ""
			}
			var report ownedProcessReport
			if json.Unmarshal([]byte(raw), &report) == nil && accept(report) {
				return report, raw
			}
		case <-timer.C:
			t.Fatal("timed out waiting for owned helper process state")
			return ownedProcessReport{}, ""
		}
	}
}

func startOwnedProcessFixture(t *testing.T, ignoreInterrupt bool, late ...bool) *ownedProcessFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPWAOwnedProcessHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "SAT20_TEST_OWNED_PROCESS_HELPER=owner")
	if ignoreInterrupt {
		cmd.Env = append(cmd.Env, "SAT20_TEST_IGNORE_INTERRUPT=1")
	}
	if len(late) > 0 && late[0] {
		cmd.Env = append(cmd.Env, "SAT20_TEST_LATE_OWNER=1")
	}
	closeOwner := configurePWACancellation(t, cmd)
	input, err := cmd.StdinPipe()
	require.NoError(t, err)
	out, err := cmd.StdoutPipe()
	require.NoError(t, err)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())

	fixture := &ownedProcessFixture{
		command: cmd, cancel: cancel, waited: make(chan error, 1),
		closeOwner: closeOwner, waitDone: make(chan struct{}), input: input,
		lines: make(chan string, 8),
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-fixture.waitDone:
		case <-time.After(6 * time.Second):
			t.Error("owned helper process did not terminate")
		}
		if fixture.group > 0 {
			if err := signalOwnedGroup(fixture.identities, fixture.group); err != nil && !fixture.expectedUnconfirmed {
				t.Errorf("owned process-group cleanup: %v", err)
			}
		}
		if err := closeOwner(); err != nil && !fixture.expectedUnconfirmed {
			t.Errorf("fixture cleanup: %v", err)
		}
	})
	go func() {
		fixture.waited <- cmd.Wait()
		close(fixture.waitDone)
	}()
	go func() {
		scanner := bufio.NewScanner(out)
		for scanner.Scan() {
			fixture.lines <- scanner.Text()
		}
		close(fixture.lines)
	}()
	report, _ := awaitOwnedProcessReport(t, fixture.lines, 30*time.Second,
		func(report ownedProcessReport) bool { return report.Browser > 1 && len(report.Pids) > 0 })
	require.Len(t, report.Pids, 2)
	fixture.group, fixture.pids = report.Browser, report.Pids
	for _, pid := range report.Pids {
		if identity, e := ownedIdentity(pid, report.Browser); e == nil {
			fixture.identities = append(fixture.identities, identity)
		}
	}
	require.Len(t, fixture.identities, 2)
	t.Logf("fixture owner helper=%d group=%d member PIDs=%v", cmd.Process.Pid, report.Browser, report.Pids)
	return fixture
}

func stopOwnedProcessFixture(t *testing.T, f *ownedProcessFixture) {
	t.Helper()
	f.cancel()
	select {
	case err := <-f.waited:
		require.Error(t, err)
		t.Logf("owned helper wait: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("owned helper process did not exit")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		gone := true
		for _, pid := range f.pids {
			if syscall.Kill(pid, 0) != syscall.ESRCH {
				gone = false
			}
		}
		if gone {
			require.NoError(t, f.closeOwner())
			t.Logf("all %d registered process-group members absent", len(f.pids))
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("owned process group survived parent cancellation")
}

func TestPWACancelReapsGroup(t *testing.T) {
	runPWACancellation(t, false)
}

func TestPWACancelFallbackReapsGroup(t *testing.T) {
	runPWACancellation(t, true)
}

func runPWACancellation(t *testing.T, ignoreInterrupt bool) {
	a := startOwnedProcessFixture(t, ignoreInterrupt)
	b := startOwnedProcessFixture(t, false)
	require.NotEqual(t, a.group, b.group)
	for _, f := range []*ownedProcessFixture{a, b} {
		group, err := syscall.Getpgid(f.group)
		require.NoError(t, err)
		require.Equal(t, f.group, group)
		nodeGroup, err := syscall.Getpgid(f.command.Process.Pid)
		require.NoError(t, err)
		require.NotEqual(t, nodeGroup, group)
	}
	// Exercise the PID-reuse guard against a still-live owned B group. An old
	// start identity must never signal it, even with the same PID/group number.
	info, err := unix.SysctlKinfoProc("kern.proc.pid", b.group)
	require.NoError(t, err)
	stale := ownedPWABrowser{pid: b.group, started: info.Proc.P_starttime}
	stale.started.Usec++
	require.ErrorContains(t, killOwnedPWABrowser(stale), "identity changed")
	require.NoError(t, syscall.Kill(b.group, 0))
	t.Log("stale process-start identity rejected; B control remains alive")
	stopOwnedProcessFixture(t, a)
	require.NoError(t, syscall.Kill(b.command.Process.Pid, 0))
	require.NoError(t, syscall.Kill(b.group, 0))
	// Verify B's owned group identity and a real helper-process operation rather
	// than assuming every child PID remains unchanged.
	current, err := unix.SysctlKinfoProc("kern.proc.pid", b.group)
	require.NoError(t, err)
	require.Equal(t, info.Proc.P_starttime, current.Proc.P_starttime)
	_, err = io.WriteString(b.input, "probe\n")
	require.NoError(t, err)
	probe, _ := awaitOwnedProcessReport(t, b.lines, 5*time.Second,
		func(report ownedProcessReport) bool { return report.Probe != "" })
	require.Equal(t, "B still functional", probe.Probe)
	t.Log("A cleanup preserved B helper process identity and probe response")
	stopOwnedProcessFixture(t, b)
}

func TestPWAOwnedLeaderExit(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestPWAOwnedProcessGroupHelper$", "-test.v")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.StdoutPipe()
	require.NoError(t, err)
	input, err := cmd.StdinPipe()
	require.NoError(t, err)
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "SAT20_TEST_OWNED_PROCESS_GROUP_HELPER=leader")
	require.NoError(t, cmd.Start())
	group := cmd.Process.Pid
	member := readOwnedMember(t, bufio.NewScanner(out))
	identity, err := ownedIdentity(member, group)
	require.NoError(t, err)
	defer func() { _ = signalOwnedGroup([]ownedPWABrowser{identity}, group) }()
	_, err = io.WriteString(input, "exit\n")
	require.NoError(t, err)
	require.NoError(t, cmd.Wait())
	require.NoError(t, signalOwnedGroup([]ownedPWABrowser{identity}, group))
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(member, 0) == syscall.ESRCH {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("owned member survives group leader exit")
}

func TestPWAOwnedLateRegistration(t *testing.T) {
	f := startOwnedProcessFixture(t, true, true)
	f.cancel()
	time.Sleep(3200 * time.Millisecond)
	_, err := io.WriteString(f.input, "register\n")
	require.NoError(t, err)
	stopOwnedProcessFixture(t, f)
}

func TestPWAOwnedUnregisteredCancellation(t *testing.T) {
	f := startOwnedProcessFixture(t, true, true)
	// Out-of-band helper identities are available, but the owner pipe is not
	// registered. The cleanup helper must report unconfirmed before this test
	// explicitly reaps its isolated fixture group.
	defer func() { _ = signalOwnedGroup(f.identities, f.group) }()
	f.cancel()
	select {
	case <-f.waited:
	case <-time.After(10 * time.Second):
		t.Fatal("setup owner process not reaped")
	}
	err := f.closeOwner()
	f.expectedUnconfirmed = true
	require.ErrorContains(t, err, "never registered")
	require.NoError(t, signalOwnedGroup(f.identities, f.group))
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if ownedGroupGone(f.identities, f.group) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("fixture-owned unregistered group not cleaned")
}

func TestPWAOwnedSetupFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPWAOwnedProcessHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "SAT20_TEST_OWNED_PROCESS_HELPER=exit23")
	closeOwner := configurePWACancellation(t, cmd)
	err := cmd.Run()
	require.Error(t, err)
	require.True(t, cmd.ProcessState.Exited())
	require.ErrorContains(t, closeOwner(), "never registered")
}
