//go:build darwin

package fastlane

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/wwd/run"
)

// TestDirectHelper is not a test: the command below re-runs this binary to get a process that is not a system binary (the kernel does
// not show the environment of those, so a sweep by the job mark could never find them) and that leaves the command's process group the
// way a dev server does.
//
//	DIRECT_HELPER=parent   starts a child in a session of its own and exits
//	DIRECT_HELPER=child    writes its pid to DIRECT_PIDFILE and sleeps
func TestDirectHelper(t *testing.T) {
	switch os.Getenv("DIRECT_HELPER") {
	case "":
		t.Skip("helper for the sweep test")
	case "parent":
		cmd := exec.Command(os.Args[0], "-test.run=^TestDirectHelper$")
		cmd.Env = append(os.Environ(), "DIRECT_HELPER=child")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
	case "child":
		if err := os.WriteFile(os.Getenv("DIRECT_PIDFILE"), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(40 * time.Second)
	}
}

// The real scanner, the real table: a process that left the command's group but kept the run's job mark is found and ended before Run
// returns.
func TestDirectEndsWhatLeftTheGroupWithTheRealScanner(t *testing.T) {
	var out bytes.Buffer
	pidFile := filepath.Join(t.TempDir(), "pid")
	// the command leaves only once the escaped process is up (a scan cannot read the environment of a process in the middle of exec)
	script := `"$0" -test.run='^TestDirectHelper$' >/dev/null 2>&1; while [ ! -s "$DIRECT_PIDFILE" ]; do sleep 0.05; done; cat "$DIRECT_PIDFILE"`
	code, err := Direct{}.Run(context.Background(), gate.Options{
		Argv: []string{"sh", "-c", script, os.Args[0]}, Stdin: strings.NewReader(""), Stdout: &out,
		Env: []string{"PATH=/usr/bin:/bin", "DIRECT_HELPER=parent", "DIRECT_PIDFILE=" + pidFile, run.MarkEnv + "=j_directtest01"},
	})
	if err != nil || code != 0 {
		t.Fatalf("code %d, err %v", code, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatalf("the escaped process's pid: %q", out.String())
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) == nil {
		t.Fatalf("the escaped process %d is still running", pid)
	}
}
