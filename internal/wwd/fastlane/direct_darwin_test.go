//go:build darwin

package fastlane

import (
	"bytes"
	"context"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wake/purdex/internal/gate"
)

// TestDirectHelper is not a test: the command below re-runs this binary to get a process that is not a system binary (the kernel does
// not show the environment of those, so a sweep by the job mark could never find them) and that outlives the command.
func TestDirectHelper(t *testing.T) {
	if os.Getenv("DIRECT_HELPER") == "" {
		t.Skip("helper for the sweep tests")
	}
	time.Sleep(40 * time.Second)
}

// The real scanner, the real table: at a terminal an unrecorded run shares the caller's group and has no job mark of its own, so
// Direct marks it; a grandchild that outlives the command (the command exits at once) is found by that mark and ended.
func TestDirectEndsAGrandchildOfAnUnrecordedRunAtATerminal(t *testing.T) {
	old := isTTY
	isTTY = func() bool { return true }
	t.Cleanup(func() { isTTY = old })
	var out bytes.Buffer
	code, err := Direct{}.Run(context.Background(), gate.Options{
		Argv: []string{"sh", "-c", `"$0" -test.run='^TestDirectHelper$' >/dev/null 2>&1 & echo $!`, os.Args[0]},
		Env:  []string{"PATH=/usr/bin:/bin", "DIRECT_HELPER=1"}, Stdout: &out,
	})
	if err != nil || code != 0 {
		t.Fatalf("code %d, err %v", code, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatalf("the grandchild's pid: %q", out.String())
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) == nil {
		t.Fatalf("the grandchild %d is still running", pid)
	}
}
