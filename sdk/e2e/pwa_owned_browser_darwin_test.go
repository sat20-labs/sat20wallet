package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
)

type ownedPWABrowser struct {
	pid     int
	started unix.Timeval
	group   int
}

func ownedIdentity(pid, group int) (ownedPWABrowser, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return ownedPWABrowser{}, err
	}
	if info.Proc.P_pid == 0 || info.Eproc.Pgid != int32(group) {
		return ownedPWABrowser{}, fmt.Errorf("process group changed")
	}
	return ownedPWABrowser{pid: pid, started: info.Proc.P_starttime, group: group}, nil
}
func killOwnedPWABrowser(owner ownedPWABrowser) error {
	group := owner.group
	if group == 0 {
		group = owner.pid
	}
	current, err := ownedIdentity(owner.pid, group)
	if err != nil {
		if syscall.Kill(owner.pid, 0) == syscall.ESRCH {
			return nil
		}
		return err
	}
	if current.started != owner.started {
		return fmt.Errorf("browser process identity changed; refusing group signal")
	}
	// macOS has no atomic identity-bound group kill. This check narrows, but
	// cannot eliminate, the check-to-signal race. Never signal a bare old PGID.
	return syscall.Kill(-group, syscall.SIGKILL)
}
func ownedGroupGone(owners []ownedPWABrowser, group int) bool {
	if syscall.Kill(-group, 0) != syscall.ESRCH {
		return false
	}
	for _, p := range owners {
		if syscall.Kill(p.pid, 0) != syscall.ESRCH {
			return false
		}
	}
	return true
}
func signalOwnedGroup(owners []ownedPWABrowser, group int) error {
	for _, p := range owners {
		now, err := ownedIdentity(p.pid, group)
		if err == nil && now.started == p.started {
			return killOwnedPWABrowser(p)
		}
	}
	if ownedGroupGone(owners, group) {
		return nil
	}
	return fmt.Errorf("cleanup unconfirmed: no registered member proves group identity")
}
func configureOwnedPWACancellation(t *testing.T, command *exec.Cmd) func() error {
	t.Helper()
	read, write, err := os.Pipe()
	require.NoError(t, err)
	fd := 3 + len(command.ExtraFiles)
	command.ExtraFiles = append(command.ExtraFiles, write)
	env := command.Env
	if env == nil {
		env = os.Environ()
	}
	command.Env = append(env, "SAT20_PWA_BROWSER_OWNER_FD="+strconv.Itoa(fd))
	var mu sync.Mutex
	var owners []ownedPWABrowser
	var group int
	var registrationErr error
	var timer *time.Timer
	var cancelledAt time.Time
	closed := false
	done := make(chan struct{})
	go func() {
		defer close(done)
		scan := bufio.NewScanner(read)
		scan.Buffer(make([]byte, 1024), 8192)
		for scan.Scan() {
			var report struct {
				Node    int   `json:"node"`
				Browser int   `json:"browser"`
				Pids    []int `json:"pids"`
			}
			decodeErr := json.Unmarshal(scan.Bytes(), &report)
			mu.Lock()
			if decodeErr != nil || closed || group != 0 || command.Process == nil || report.Node != command.Process.Pid || report.Browser <= 1 {
				registrationErr = fmt.Errorf("cleanup unconfirmed: invalid owner registration")
				mu.Unlock()
				continue
			}
			leader, e := unix.SysctlKinfoProc("kern.proc.pid", report.Browser)
			if e != nil || leader.Eproc.Ppid != int32(report.Node) || leader.Eproc.Pgid != int32(report.Browser) {
				registrationErr = fmt.Errorf("cleanup unconfirmed: browser parent/group unavailable")
				mu.Unlock()
				continue
			}
			group = report.Browser
			owners = append(owners, ownedPWABrowser{pid: group, started: leader.Proc.P_starttime, group: group})
			for _, pid := range report.Pids {
				if pid == group {
					continue
				}
				if identity, e := ownedIdentity(pid, group); e == nil {
					owners = append(owners, identity)
				}
			}
			t.Logf("pipe registered Node=%d browser group=%d members=%d", report.Node, group, len(owners))
			if !cancelledAt.IsZero() && time.Since(cancelledAt) >= 3*time.Second {
				_ = signalOwnedGroup(owners, group)
			}
			mu.Unlock()
		}
		if e := scan.Err(); e != nil {
			mu.Lock()
			if !closed {
				registrationErr = fmt.Errorf("cleanup unconfirmed: owner pipe read failed")
			}
			mu.Unlock()
		}
	}()
	graceful := command.Cancel
	command.Cancel = func() error {
		mu.Lock()
		if !closed && cancelledAt.IsZero() {
			cancelledAt = time.Now()
			timer = time.AfterFunc(3*time.Second, func() {
				mu.Lock()
				defer mu.Unlock()
				if !closed && group != 0 {
					_ = signalOwnedGroup(owners, group)
				}
			})
		}
		mu.Unlock()
		return graceful()
	}
	var once sync.Once
	var result error
	closeOwner := func() error {
		once.Do(func() {
			// Called after Cmd.Wait: close the parent's inherited writer, then drain
			// registrations already written by Node before it exited.
			_ = write.Close()
			select {
			case <-done:
			case <-time.After(time.Second):
			}
			mu.Lock()
			closed = true
			if timer != nil {
				timer.Stop()
			}
			snapshot := append([]ownedPWABrowser(nil), owners...)
			g := group
			result = registrationErr
			mu.Unlock()
			_ = read.Close()
			if g == 0 {
				result = fmt.Errorf("cleanup unconfirmed: browser owner was never registered")
				return
			}
			if result != nil {
				return
			}
			if e := signalOwnedGroup(snapshot, g); e != nil && e != syscall.ESRCH {
				result = e
				return
			}
			deadline := time.Now().Add(4 * time.Second)
			for time.Now().Before(deadline) {
				if ownedGroupGone(snapshot, g) {
					return
				}
				time.Sleep(25 * time.Millisecond)
			}
			result = fmt.Errorf("cleanup unconfirmed: owned group termination not observed")
		})
		return result
	}
	return closeOwner
}
