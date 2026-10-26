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
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

type ownedBrowserFixture struct {
	command             *exec.Cmd
	cancel              context.CancelFunc
	waited              chan error
	browser             int
	pids                []int
	closeOwner          func() error
	waitDone            chan struct{}
	expectedUnconfirmed bool
	identities          []ownedPWABrowser
	input               io.WriteCloser
	lines               chan string
}

func startOwnedBrowserFixture(t *testing.T, ignoreInterrupt bool, late ...bool) *ownedBrowserFixture {
	t.Helper()
	pwa, err := filepath.Abs("../../pwa")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	script := `const {chromium}=require('@playwright/test');(async()=>{
 const b=await chromium.launch({headless:true,...(process.env.SAT20_BROWSER_EXECUTABLE?{executablePath:process.env.SAT20_BROWSER_EXECUTABLE}:{})});
 const c=await b.newBrowserCDPSession();await b.newPage();const all=await c.send('SystemInfo.getProcessInfo');
 const {registerOwnedBrowser}=await import('./scripts/verify/pwa-owned-browser.mjs');
 if(process.env.SAT20_TEST_LATE_OWNER==='1'){process.removeAllListeners('SIGINT');process.on('SIGINT',()=>{});console.log(JSON.stringify({browser:all.processInfo.find(p=>p.type==='browser').id,pids:all.processInfo.map(p=>p.id)}));await new Promise(resolve=>process.stdin.once('data',resolve));}
 await registerOwnedBrowser(b);
 if(process.env.SAT20_TEST_IGNORE_INTERRUPT==='1'){process.removeAllListeners('SIGINT');process.on('SIGINT',()=>{});}
 console.log(JSON.stringify({browser:all.processInfo.find(p=>p.type==='browser').id,pids:all.processInfo.map(p=>p.id)}));process.stdin.on('data',async()=>{const page=await b.newPage();await page.setContent('<title>B still functional</title>');console.log(JSON.stringify({probe:await page.title()}));await page.close()});setInterval(()=>{},1000)
 })().catch(e=>{console.error(e.message);process.exit(1)})`
	cmd := exec.CommandContext(ctx, "node", "-e", script)
	cmd.Dir = pwa
	if ignoreInterrupt {
		cmd.Env = append(os.Environ(), "SAT20_TEST_IGNORE_INTERRUPT=1")
	}
	if len(late) > 0 && late[0] {
		if cmd.Env == nil {
			cmd.Env = os.Environ()
		}
		cmd.Env = append(cmd.Env, "SAT20_TEST_LATE_OWNER=1")
	}
	closeOwner := configurePWACancellation(t, cmd)
	input, err := cmd.StdinPipe()
	require.NoError(t, err)
	out, err := cmd.StdoutPipe()
	require.NoError(t, err)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	fixture := &ownedBrowserFixture{command: cmd, cancel: cancel, waited: make(chan error, 1), closeOwner: closeOwner, waitDone: make(chan struct{})}
	t.Cleanup(func() {
		cancel()
		select {
		case <-fixture.waitDone:
		case <-time.After(6 * time.Second):
			t.Error("fixture Node did not terminate")
		}
		if err := closeOwner(); err != nil && !fixture.expectedUnconfirmed {
			t.Errorf("fixture cleanup: %v", err)
		}
	})
	go func() { fixture.waited <- cmd.Wait(); close(fixture.waitDone) }()
	line := make(chan string, 8)
	fixture.input = input
	fixture.lines = line
	go func() {
		s := bufio.NewScanner(out)
		for s.Scan() {
			line <- s.Text()
		}
		close(line)
	}()
	var report struct {
		Browser int   `json:"browser"`
		Pids    []int `json:"pids"`
	}
	select {
	case raw := <-line:
		require.NoError(t, json.Unmarshal([]byte(raw), &report))
	case <-time.After(30 * time.Second):
		t.Fatal("owned browser did not register")
	}
	require.Positive(t, report.Browser)
	fixture.browser = report.Browser
	fixture.pids = report.Pids
	for _, pid := range report.Pids {
		if identity, e := ownedIdentity(pid, report.Browser); e == nil {
			fixture.identities = append(fixture.identities, identity)
		}
	}
	t.Logf("fixture owned Node=%d browser group=%d Chromium PIDs=%v", cmd.Process.Pid, report.Browser, report.Pids)
	return fixture
}

func stopOwnedBrowserFixture(t *testing.T, f *ownedBrowserFixture) {
	t.Helper()
	f.cancel()
	select {
	case err := <-f.waited:
		require.Error(t, err)
		t.Logf("owned Node wait: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("owned Node did not exit")
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
			t.Logf("all %d registered Chromium PIDs absent", len(f.pids))
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("owned Chromium survived parent cancellation")
}

func TestPWACancellationReapsOwnedChromium(t *testing.T)              { runPWACancellation(t, false) }
func TestPWACancellationForceFallbackReapsOwnedChromium(t *testing.T) { runPWACancellation(t, true) }
func runPWACancellation(t *testing.T, ignoreInterrupt bool) {
	a := startOwnedBrowserFixture(t, ignoreInterrupt)
	b := startOwnedBrowserFixture(t, false)
	require.NotEqual(t, a.browser, b.browser)
	for _, f := range []*ownedBrowserFixture{a, b} {
		group, err := syscall.Getpgid(f.browser)
		require.NoError(t, err)
		require.Equal(t, f.browser, group)
		nodeGroup, err := syscall.Getpgid(f.command.Process.Pid)
		require.NoError(t, err)
		require.NotEqual(t, nodeGroup, group)
	}
	// Exercise the PID-reuse guard against a still-live owned B group. An old
	// start identity must never signal it, even with the same PID/group number.
	info, err := unix.SysctlKinfoProc("kern.proc.pid", b.browser)
	require.NoError(t, err)
	stale := ownedPWABrowser{pid: b.browser, started: info.Proc.P_starttime}
	stale.started.Usec++
	require.ErrorContains(t, killOwnedPWABrowser(stale), "identity changed")
	require.NoError(t, syscall.Kill(b.browser, 0))
	t.Log("stale process-start identity rejected; B control remains alive")
	stopOwnedBrowserFixture(t, a)
	require.NoError(t, syscall.Kill(b.command.Process.Pid, 0))
	require.NoError(t, syscall.Kill(b.browser, 0))
	// Chromium utility PIDs can exit naturally. Verify the durable browser
	// identity and a real page operation instead of assuming they never churn.
	current, err := unix.SysctlKinfoProc("kern.proc.pid", b.browser)
	require.NoError(t, err)
	require.Equal(t, info.Proc.P_starttime, current.Proc.P_starttime)
	_, err = io.WriteString(b.input, "probe\n")
	require.NoError(t, err)
	select {
	case line := <-b.lines:
		var probe struct {
			Probe string `json:"probe"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &probe))
		require.Equal(t, "B still functional", probe.Probe)
	case <-time.After(5 * time.Second):
		t.Fatal("B browser stopped responding after A cleanup")
	}
	t.Log("A cleanup preserved B Node/browser identity and real page operation")
	stopOwnedBrowserFixture(t, b)
}

func TestPWAOwnedLeaderExit(t *testing.T) {
	cmd := exec.Command("node", "-e", `const {spawn}=require('child_process');const c=spawn(process.execPath,['-e','setInterval(()=>{},1000)'],{stdio:'ignore'});console.log(c.pid);process.stdin.once('data',()=>process.exit(0))`)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.StdoutPipe()
	require.NoError(t, err)
	input, err := cmd.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	scan := bufio.NewScanner(out)
	require.True(t, scan.Scan())
	var child int
	_, err = fmt.Sscan(scan.Text(), &child)
	require.NoError(t, err)
	leader, err := unix.SysctlKinfoProc("kern.proc.pid", cmd.Process.Pid)
	require.NoError(t, err)
	member, err := unix.SysctlKinfoProc("kern.proc.pid", child)
	require.NoError(t, err)
	group := cmd.Process.Pid
	defer func() {
		current, e := unix.SysctlKinfoProc("kern.proc.pid", child)
		if e == nil && current.Proc.P_starttime == member.Proc.P_starttime && current.Eproc.Pgid == int32(group) {
			_ = syscall.Kill(-group, syscall.SIGKILL)
		}
	}()
	_, err = io.WriteString(input, "exit\n")
	require.NoError(t, err)
	require.NoError(t, cmd.Wait())
	_ = leader
	require.NoError(t, signalOwnedGroup([]ownedPWABrowser{{pid: child, started: member.Proc.P_starttime, group: group}}, group))
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(child, 0) == syscall.ESRCH {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("owned member survives group leader exit")
}

func TestPWAOwnedLateRegistration(t *testing.T) {
	f := startOwnedBrowserFixture(t, true, true)
	f.cancel()
	time.Sleep(3200 * time.Millisecond)
	_, err := io.WriteString(f.input, "register\n")
	require.NoError(t, err)
	stopOwnedBrowserFixture(t, f)
}
func TestPWAOwnedUnregisteredCancellation(t *testing.T) {
	f := startOwnedBrowserFixture(t, true, true)
	// Only this fixture has the out-of-band CDP marker; the actual helper must
	// report unconfirmed rather than claim success or guess an unknown group.
	defer func() { _ = signalOwnedGroup(f.identities, f.browser) }()
	f.cancel()
	select {
	case <-f.waited:
	case <-time.After(10 * time.Second):
		t.Fatal("setup Node not reaped")
	}
	err := f.closeOwner()
	f.expectedUnconfirmed = true
	require.ErrorContains(t, err, "never registered")
	require.NoError(t, signalOwnedGroup(f.identities, f.browser))
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if ownedGroupGone(f.identities, f.browser) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("fixture-owned unregistered group not cleaned")
}
func TestPWAOwnedSetupFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "-e", "process.exit(23)")
	closeOwner := configurePWACancellation(t, cmd)
	err := cmd.Run()
	require.Error(t, err)
	require.True(t, cmd.ProcessState.Exited())
	require.ErrorContains(t, closeOwner(), "never registered")
	t.Log("setup error preserved; Node exited; missing ownership explicitly unconfirmed")
}
