package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// healthBody serves a /api/health-shaped 200 with the given body.
func healthServer(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
}

// #959: another daemon (a different data dir, or an older build) already answers on the address. Its 200 is not the
// child's: with a pid to match, the start is not reported healthy, and when the child then dies of "address already in
// use" that exit is what the caller sees. Mutation gate: drop the pid comparison → red.
func TestWaitForHealthy_AnotherDaemonOnTheAddressIsNotOurs(t *testing.T) {
	srv := healthServer(`{"ok":true,"pid":4242}`)
	defer srv.Close()

	childExited := make(chan error, 1)
	go func() {
		time.Sleep(150 * time.Millisecond)
		childExited <- errors.New("exit status 1")
	}()
	err := waitForHealthy(srv.URL, 99999, childExited, 3*time.Second, 20*time.Millisecond, nil)
	if err == nil || !strings.Contains(err.Error(), "exited during startup") {
		t.Fatalf("err = %v, want the child's exit, not a success on the other daemon's 200", err)
	}
}

// With nothing dying, the timeout names the stranger so the cause is not a mystery.
func TestWaitForHealthy_TimeoutNamesTheOtherDaemon(t *testing.T) {
	srv := healthServer(`{"ok":true,"pid":4242}`)
	defer srv.Close()

	err := waitForHealthy(srv.URL, 99999, make(chan error, 1), 300*time.Millisecond, 20*time.Millisecond, nil)
	if err == nil {
		t.Fatal("a 200 from another pid must not count")
	}
	if !strings.Contains(err.Error(), "pid 4242") || !strings.Contains(err.Error(), "99999") {
		t.Fatalf("err = %q, want both the answering pid and the expected one", err)
	}
}

// An older daemon answers 200 without a pid: with a pid to match it is not ours either.
func TestWaitForHealthy_AnAnswerWithoutPidIsNotOurs(t *testing.T) {
	srv := healthServer(`{"ok":true}`)
	defer srv.Close()
	if err := waitForHealthy(srv.URL, 99999, make(chan error, 1), 300*time.Millisecond, 20*time.Millisecond, nil); err == nil {
		t.Fatal("a 200 without a pid must not count when a pid is expected")
	}
}

// The child's own answer is healthy, and wantPID 0 keeps the old "any 200" behaviour.
func TestWaitForHealthy_OurPidIsHealthy_ZeroDisablesTheCheck(t *testing.T) {
	srv := healthServer(`{"ok":true,"pid":4242}`)
	defer srv.Close()
	if err := waitForHealthy(srv.URL, 4242, make(chan error, 1), 2*time.Second, 20*time.Millisecond, nil); err != nil {
		t.Fatalf("matching pid: %v", err)
	}
	if err := waitForHealthy(srv.URL, 0, make(chan error, 1), 2*time.Second, 20*time.Millisecond, nil); err != nil {
		t.Fatalf("wantPID 0: %v", err)
	}
}

// #958: a failed start kills the child's whole process group, grandchildren included. The test starts its own group:
// a shell that backgrounds a sleep and records the sleep's pid. Mutation gate: kill only the pid → the sleep lives → red.
func TestKillProcessGroup_TakesTheGrandchildToo(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	cmd := exec.Command("sh", "-c", "sleep 300 & echo $! > "+pidFile+"; wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan struct{})
	go func() { cmd.Wait(); close(waited) }()
	t.Cleanup(func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })

	var grandchild int
	deadline := time.Now().Add(5 * time.Second)
	for grandchild == 0 {
		if b, err := os.ReadFile(pidFile); err == nil {
			grandchild, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		if grandchild == 0 && time.Now().After(deadline) {
			t.Fatal("the shell never recorded its child")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := killProcessGroup(cmd.Process.Pid); err != nil {
		t.Fatalf("killProcessGroup: %v", err)
	}
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("the group leader survived")
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if err := syscall.Kill(grandchild, 0); errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the grandchild %d survived the group kill", grandchild)
		}
	}
	// A group that is already gone is not an error: the failure path may run after the child exited.
	if err := killProcessGroup(cmd.Process.Pid); err != nil {
		t.Fatalf("killProcessGroup on an empty group: %v", err)
	}
}

// The tmux server a daemon starts is not in the daemon's process group (it daemonizes and leaves it), so the group kill
// cannot take the user's tmux with it. Own server only: -L label, TMUX unset, killed at the end. Skipped without tmux.
func TestKillProcessGroup_LeavesATmuxServerStartedInsideTheGroup(t *testing.T) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}
	label := fmt.Sprintf("pdx-pgtest-%d", os.Getpid())
	var env []string // never inherit TMUX: this must not reach the user's own server
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "TMUX=") {
			env = append(env, e)
		}
	}
	t.Cleanup(func() { exec.Command(tmux, "-L", label, "kill-server").Run() })

	cmd := exec.Command("sh", "-c", fmt.Sprintf("unset TMUX; %s -L %s new-session -d -s pgtest 'sleep 300'; wait", tmux, label))
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go cmd.Wait()
	t.Cleanup(func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })

	var serverPid int
	for deadline := time.Now().Add(5 * time.Second); serverPid == 0; time.Sleep(20 * time.Millisecond) {
		out, err := exec.Command(tmux, "-L", label, "display-message", "-p", "#{pid}").Output()
		if err == nil {
			serverPid, _ = strconv.Atoi(strings.TrimSpace(string(out)))
		}
		if serverPid == 0 && time.Now().After(deadline) {
			t.Fatal("the test tmux server did not come up")
		}
	}
	if pgid, err := syscall.Getpgid(serverPid); err != nil || pgid == cmd.Process.Pid {
		t.Fatalf("the tmux server is in the starter's process group (pgid %d, err %v): the group kill would take it", pgid, err)
	}
	if err := killProcessGroup(cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := syscall.Kill(serverPid, 0); err != nil {
		t.Fatalf("the group kill took the tmux server with it: %v", err)
	}
}

// The probe asks the address directly: an environment proxy (HTTP_PROXY) can answer for a non-loopback bind such as a
// Tailscale IP, and without the child's pid in that answer the start would be reported failed and a healthy daemon's group
// killed. And a redirect is not followed to somewhere else. Mutation gate: default transport → red.
func TestHealthClient_NoProxyNoRedirect(t *testing.T) {
	c := healthClient()
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.Proxy != nil {
		t.Fatalf("transport = %#v, want an *http.Transport with no Proxy", c.Transport)
	}
	if c.CheckRedirect == nil {
		t.Fatal("redirects must not be followed")
	}
}

func TestWaitForHealthy_ARedirectIsNotFollowedToAnotherDaemon(t *testing.T) {
	ours := healthServer(`{"ok":true,"pid":4242}`)
	defer ours.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, ours.URL, http.StatusFound)
	}))
	defer redirector.Close()
	if err := waitForHealthy(redirector.URL, 4242, make(chan error, 1), 300*time.Millisecond, 20*time.Millisecond, nil); err == nil {
		t.Fatal("a redirect to a server with the right pid must not make the address healthy")
	}
}
