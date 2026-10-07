//go:build integration

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Isolation audit done: every path a module reaches is covered by isolatedEnv
// except /tmp/cc-socks (peers helperSockDir, not overridable), which is
// unreachable because the test sends no peer message.

// TestMain doubles as the daemon: the integration test starts this test
// binary with PDX_RESTART_HELPER=1 and `serve` args, and a restart re-execs
// the same argv+env, so the new image lands here again.
func TestMain(m *testing.M) {
	if os.Getenv("PDX_RESTART_HELPER") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func bootID(base string) (string, error) {
	c := http.Client{Timeout: time.Second}
	resp, err := c.Get(base + "/api/health")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var body struct {
		BootID string `json:"boot_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.BootID, nil
}

func waitBootID(t *testing.T, base, not string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if id, err := bootID(base); err == nil && id != "" && id != not {
			return id
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("no boot id other than %q within %v", not, timeout)
	return ""
}

// isolatedEnv is os.Environ() minus every key the helper daemon must not
// inherit, plus the overrides — built explicitly rather than appended so no
// duplicate key decides which value wins (codex plan review #3).
func isolatedEnv(dir, tmuxDir string) []string {
	drop := map[string]bool{"HOME": true, "TMUX": true, "TMUX_PANE": true, "TMUX_TMPDIR": true}
	var env []string
	for _, kv := range os.Environ() {
		// Every inherited PDX_* goes (#1569): the ones the helper needs are set below.
		if k, _, _ := strings.Cut(kv, "="); !drop[k] && !strings.HasPrefix(k, "PDX_") {
			env = append(env, kv)
		}
	}
	codex := filepath.Join(dir, "codex")
	os.MkdirAll(codex, 0700)
	return append(env,
		"PDX_RESTART_HELPER=1",
		"HOME="+dir,                     // every ~/... a module touches (~/.claude, ~/.codex, …)
		"TMUX_TMPDIR="+tmuxDir,          // tmux calls hit a private, absent server — never the user's sessions
		"PDX_CODEX_STATE_ROOT="+codex,   // codexbroker state …
		"PDX_CODEX_SOCKET_ROOTS="+codex, // … and its socket glob (default globs the real /var/folders/*/*/T)
		"PDX_DEV_MODE=0",                // observable env: dev routes stay off across the re-exec (D3)
	)
}

var itestClient = &http.Client{Timeout: 5 * time.Second}

func devCheckStatus(t *testing.T, base string) int {
	t.Helper()
	req, _ := http.NewRequest("GET", base+"/api/dev/daemon/check", nil)
	req.Header.Set("Authorization", "Bearer itest-token")
	resp, err := itestClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestRestart_ReexecKeepsPidNewBootID(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)
	cfgPath := filepath.Join(dir, "config.toml")
	// [dev] update = true mounts the dev module; PDX_DEV_MODE=0 keeps its
	// routes unregistered. If the re-exec lost the boot env, the new image
	// would see PDX_DEV_MODE unset (= on) and /api/dev/daemon/check would appear.
	cfg := fmt.Sprintf("bind = \"127.0.0.1\"\nport = %d\ndata_dir = %q\ntoken = \"itest-token\"\n\n[dev]\nupdate = true\nrepo_root = %q\n", port, dir, dir)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Create(filepath.Join(dir, "serve.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "serve", "--config", cfgPath)
	// Short path: a long t.TempDir under /var/folders can exceed the ~104-byte
	// unix socket limit if tmux ever starts.
	tmuxDir, err := os.MkdirTemp("", "pdxt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmuxDir) })
	cmd.Env = isolatedEnv(dir, tmuxDir)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// One waiter for the child's whole life: if the original process ever
	// exits, this fires — exec keeps the process, a crash-and-respawn does not.
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		defer logFile.Close()
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(15 * time.Second):
			cmd.Process.Kill()
			<-exited
		}
		if t.Failed() {
			b, _ := os.ReadFile(filepath.Join(dir, "serve.log"))
			t.Logf("serve.log:\n%s", b)
		}
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	first := waitBootID(t, base, "", 30*time.Second)
	if code := devCheckStatus(t, base); code != http.StatusNotFound {
		t.Fatalf("before restart: /api/dev/daemon/check = %d, want 404 (PDX_DEV_MODE=0)", code)
	}

	// Lock-gap probe: from just before the POST until the new boot id answers,
	// try a shared flock on pdx.pid every millisecond. A success means the
	// lock was free — the window a concurrent `pdx start` could take.
	var gaps atomic.Int32
	stopProbe := make(chan struct{})
	probeDone := make(chan struct{})
	go func() {
		defer close(probeDone)
		pidPath := filepath.Join(dir, "pdx.pid")
		for {
			select {
			case <-stopProbe:
				return
			case <-time.After(time.Millisecond):
			}
			fd, err := syscall.Open(pidPath, syscall.O_RDONLY, 0)
			if err != nil {
				continue
			}
			if syscall.Flock(fd, syscall.LOCK_SH|syscall.LOCK_NB) == nil {
				gaps.Add(1)
				syscall.Flock(fd, syscall.LOCK_UN)
			}
			syscall.Close(fd)
		}
	}()

	req, _ := http.NewRequest("POST", base+"/api/daemon/restart", nil)
	req.Header.Set("Authorization", "Bearer itest-token")
	resp, err := itestClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var accepted struct {
		BootID string `json:"boot_id"`
	}
	json.NewDecoder(resp.Body).Decode(&accepted)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted || accepted.BootID != first {
		t.Fatalf("restart: %d %q, want 202 with boot id %q", resp.StatusCode, accepted.BootID, first)
	}

	second := waitBootID(t, base, first, 60*time.Second)
	close(stopProbe)
	<-probeDone
	if n := gaps.Load(); n != 0 {
		t.Fatalf("pid lock was free %d time(s) during the restart: it must be handed across the exec", n)
	}
	if second == first {
		t.Fatal("boot id unchanged")
	}
	// Every image's runServe logs its readiness line (#1767): the first boot
	// and the re-exec'd image each contributed one.
	if b, err := os.ReadFile(filepath.Join(dir, "serve.log")); err != nil {
		t.Fatal(err)
	} else if n := strings.Count(string(b), "startup: ready in "); n < 2 {
		t.Fatalf("serve.log has %d %q lines, want one per image (>=2)", n, "startup: ready in")
	}
	// Same process: the child we started never exited (exec replaces the
	// image in place; a waiter on it would have fired on any exit).
	select {
	case err := <-exited:
		t.Fatalf("original process exited during restart: %v", err)
	default:
	}
	// Same environment: dev routes are still off (D3).
	if code := devCheckStatus(t, base); code != http.StatusNotFound {
		t.Fatalf("after restart: /api/dev/daemon/check = %d, want 404 — the boot env (PDX_DEV_MODE=0) was not kept", code)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "serve.log")); !strings.Contains(string(b), "pid lock: adopted from the previous image") {
		t.Fatalf("serve.log has no pid lock adoption line:\n%s", b)
	}
	pidData, err := os.ReadFile(filepath.Join(dir, "pdx.pid"))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := strconv.Atoi(strings.TrimSpace(string(pidData))); got != cmd.Process.Pid {
		t.Fatalf("pid file = %d, want %d (unchanged across restart)", got, cmd.Process.Pid)
	}
}
