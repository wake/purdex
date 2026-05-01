//go:build integration

package codexbroker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestInventory_LiveBroker spawns a real app-server-broker.mjs against an
// isolated cwd, then asserts the scanner observes a single broker with all
// three Sources bits set. Verifies AC2 against real codex CLI artefacts and
// supplements AC7 by checking the live process count is unchanged across
// two scans.
//
// Run with:
//
//	go test -tags=integration ./internal/codexbroker/...
//
// Requires:
//   - macOS or Linux
//   - node in $PATH
//   - codex plugin installed at ~/.claude/plugins/cache/openai-codex/codex/<ver>/scripts/app-server-broker.mjs
//
// Skipped automatically when prerequisites are missing.
func TestInventory_LiveBroker(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("integration test requires darwin/linux; runtime is %s", runtime.GOOS)
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found in PATH")
	}
	brokerScript := findBrokerScript(t)
	if brokerScript == "" {
		t.Skip("app-server-broker.mjs not found in any installed codex plugin cache")
	}

	// Unique workspace cwd; broker derives state-dir suffix from sha256 of
	// realpath(cwd), so a fresh tmpdir guarantees no collision with any
	// existing live broker on this machine.
	workCwd := t.TempDir()

	// Socket / pid live in their own tmpdir (codex normally puts them in
	// $TMPDIR/cxc-XXXXXX, but we control placement here for cleanup).
	sockTmp, err := os.MkdirTemp("", "pdx-codexbroker-it-")
	if err != nil {
		t.Fatalf("mkdir sock tmp: %v", err)
	}
	sockPath := filepath.Join(sockTmp, "broker.sock")
	pidPath := filepath.Join(sockTmp, "broker.pid")

	preBrokerPids := snapshotBrokerPids(t)

	// Spawn broker. setpgid so we can killpg cleanly on cleanup.
	cmd := exec.Command("node", brokerScript, "serve",
		"--cwd", workCwd,
		"--endpoint", "unix:"+sockPath,
		"--pid-file", pidPath,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn broker: %v", err)
	}
	t.Logf("spawned broker pid=%d cwd=%s sock=%s", cmd.Process.Pid, workCwd, sockPath)

	// Cleanup is critical: register immediately so panics / failed asserts
	// still reap the broker. Steps: SIGTERM → wait 2s → SIGKILL → rm tmp dirs.
	t.Cleanup(func() {
		// Try to read the actual broker pid from the pidfile (broker may
		// have forked / exec'd since cmd.Process.Pid was captured).
		brokerPid := cmd.Process.Pid
		if data, err := os.ReadFile(pidPath); err == nil {
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(data))); perr == nil && pid > 0 {
				brokerPid = pid
			}
		}
		pgid, _ := syscall.Getpgid(brokerPid)
		if pgid <= 1 {
			pgid = brokerPid
		}
		_ = syscall.Kill(-pgid, syscall.SIGTERM)

		done := make(chan struct{})
		go func() {
			_, _ = cmd.Process.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			<-done
		}

		// Clean broker-created state directory. State dir name is
		// "<basename(cwd)>-<sha256(realpath(cwd))[:16]>".
		if root, err := PluginDataRoot(); err == nil {
			fs := NewOsFS()
			ci := IsCaseInsensitiveVolume(root)
			key, _, _ := BrokerKey(workCwd, fs, ci)
			if key != "" {
				stateDir := filepath.Join(root, filepath.Base(workCwd)+"-"+key)
				_ = os.RemoveAll(stateDir)
			}
		}
		_ = os.RemoveAll(sockTmp)
	})

	// Wait for broker to publish broker.json + open socket.
	stateDir := waitForBrokerArtefacts(t, workCwd, 5*time.Second)

	scanner := NewScanner(ScannerOpts{
		FS:     NewOsFS(),
		Lister: NewPsLister(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := scanner.Scan(ctx)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	// Find our record by matching against the spawned process pid.
	want := cmd.Process.Pid
	if data, err := os.ReadFile(pidPath); err == nil {
		if pid, perr := strconv.Atoi(strings.TrimSpace(string(data))); perr == nil {
			want = pid
		}
	}
	var found *BrokerRecord
	for i := range res.Brokers {
		if res.Brokers[i].PID == want {
			found = &res.Brokers[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("broker pid %d not found in scan; %d records returned (state-dir=%s)",
			want, len(res.Brokers), stateDir)
	}

	// AC2: all three sources observed.
	wantSources := SourceProcess | SourceStateDir | SourceSocket
	if found.Sources != wantSources {
		t.Errorf("Sources = %d, want %d (process=%v state=%v socket=%v)",
			found.Sources, wantSources,
			found.Sources&SourceProcess != 0,
			found.Sources&SourceStateDir != 0,
			found.Sources&SourceSocket != 0)
	}

	// Run a second scan; ps should still show exactly the same broker pids
	// (supplementary AC7 evidence; the structural read-only proof is in
	// read_only_audit_test.go).
	if _, err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	postBrokerPids := snapshotBrokerPids(t)
	if !pidSetEqual(preBrokerPids, removeOne(postBrokerPids, want)) {
		t.Errorf("ps broker pid set diverged across scan: pre=%v post(less spawned)=%v",
			preBrokerPids, removeOne(postBrokerPids, want))
	}
}

// findBrokerScript returns the absolute path to the most recent
// app-server-broker.mjs in the codex plugin cache, or "" if none.
func findBrokerScript(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	pattern := filepath.Join(home, ".claude/plugins/cache/openai-codex/codex/*/scripts/app-server-broker.mjs")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return ""
	}
	// Prefer last lexically-sorted (highest version).
	return matches[len(matches)-1]
}

// waitForBrokerArtefacts polls until either the state dir's broker.json
// exists or the deadline expires. Returns the resolved state-dir path.
func waitForBrokerArtefacts(t *testing.T, workCwd string, deadline time.Duration) string {
	t.Helper()
	root, err := PluginDataRoot()
	if err != nil {
		t.Fatalf("PluginDataRoot: %v", err)
	}
	fs := NewOsFS()
	ci := IsCaseInsensitiveVolume(root)
	key, _, _ := BrokerKey(workCwd, fs, ci)
	if key == "" {
		t.Fatalf("BrokerKey: empty for cwd=%s", workCwd)
	}
	stateDir := filepath.Join(root, filepath.Base(workCwd)+"-"+key)
	brokerJSON := filepath.Join(stateDir, "broker.json")

	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	until := time.After(deadline)
	for {
		select {
		case <-until:
			t.Fatalf("broker.json never appeared at %s", brokerJSON)
		case <-tick.C:
			if _, err := os.Stat(brokerJSON); err == nil {
				return stateDir
			}
		}
	}
}

// snapshotBrokerPids returns the set of pids whose argv matches the broker
// pattern at this instant. Uses ps via the same lister the scanner uses.
func snapshotBrokerPids(t *testing.T) map[int]struct{} {
	t.Helper()
	rows, err := NewPsLister().List(context.Background())
	if err != nil {
		var pe *exec.Error
		if errors.As(err, &pe) {
			t.Skipf("ps unavailable: %v", err)
		}
		t.Fatalf("ps List: %v", err)
	}
	out := make(map[int]struct{})
	for _, r := range rows {
		if strings.Contains(r.Cmdline, "app-server-broker.mjs") {
			out[r.PID] = struct{}{}
		}
	}
	return out
}

func pidSetEqual(a, b map[int]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

// removeOne returns a copy of m without the given pid.
func removeOne(m map[int]struct{}, pid int) map[int]struct{} {
	out := make(map[int]struct{}, len(m))
	for k := range m {
		if k != pid {
			out[k] = struct{}{}
		}
	}
	return out
}

// suppress unused-import in non-default builds
var _ = fmt.Sprintf
