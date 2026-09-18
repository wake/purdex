// internal/tmux/kill_session_conditional_test.go — the conditional kill.
//
// Same shape as the conditional send: a kill authorised against one tmux
// generation must be evaluated and performed by the server that holds that
// generation. `kill-session -t <name>` after a restart would kill whoever
// reused the name on the new server; `if-shell -F` on the session ID makes the
// comparison and the kill one server connection.
package tmux_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/tmux"
)

func stubTmux(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// The exact argv: ONE `if-shell -F` carrying the condition, the kill by
// session id, and the refusal branch.
func TestKillSessionIfInstance_SingleInvocationCarriesConditionAndKill(t *testing.T) {
	stubTmux(t, `#!/bin/sh
if [ "$1" != "if-shell" ] || [ "$2" != "-F" ]; then
  printf 'not a single conditional invocation: %s\n' "$*" >&2
  exit 2
fi
if [ "$3" != '#{==:#{pid}:#{start_time},4471:1788740000}' ]; then
  printf 'unexpected condition: %s\n' "$3" >&2
  exit 2
fi
if [ "$4" != "kill-session -t '\$3'" ]; then
  printf 'unexpected kill: %s\n' "$4" >&2
  exit 2
fi
case "$5" in
  display-message*) ;;
  *) printf 'unexpected else branch: %s\n' "$5" >&2; exit 2 ;;
esac
if [ -n "$6" ]; then
  printf 'unexpected extra args: %s\n' "$*" >&2
  exit 2
fi
`)

	killed, err := (&tmux.RealExecutor{}).KillSessionIfInstance("$3", "4471:1788740000")
	if err != nil {
		t.Fatalf("KillSessionIfInstance returned error: %v", err)
	}
	if !killed {
		t.Fatal("KillSessionIfInstance reported the session was not killed")
	}
}

// The server evaluated the condition and declined: an answer, not a failure.
func TestKillSessionIfInstance_ServerRefuses_ReportsNotKilled(t *testing.T) {
	stubTmux(t, `#!/bin/sh
printf 'pdx-generation-refused\n'
`)
	killed, err := (&tmux.RealExecutor{}).KillSessionIfInstance("$0", "4471:1788740000")
	if err != nil {
		t.Fatalf("a refusal is not an error, got %v", err)
	}
	if killed {
		t.Fatal("a refused kill must not report as killed")
	}
}

// No server on the socket: an error, and nothing was killed.
func TestKillSessionIfInstance_InvocationFails_ReportsErrorNotKilled(t *testing.T) {
	stubTmux(t, `#!/bin/sh
printf 'no server running on /tmp/tmux-501/default\n' >&2
exit 1
`)
	killed, err := (&tmux.RealExecutor{}).KillSessionIfInstance("$0", "4471:1788740000")
	if killed || err == nil {
		t.Fatalf("a failed invocation must report not-killed and an error, got killed=%v err=%v", killed, err)
	}
}

// The generation matched but the session id no longer exists on that server:
// an error that says so (ErrNoSession), not a refusal.
func TestKillSessionIfInstance_SessionGone_ReportsErrNoSession(t *testing.T) {
	stubTmux(t, `#!/bin/sh
printf "can't find session: \$7\n" >&2
exit 1
`)
	killed, err := (&tmux.RealExecutor{}).KillSessionIfInstance("$7", "4471:1788740000")
	if killed {
		t.Fatal("nothing was killed")
	}
	if !errors.Is(err, tmux.ErrNoSession) {
		t.Fatalf("want ErrNoSession, got %v", err)
	}
}

// Anything that could change the shape of the format is refused before tmux
// is invoked at all.
func TestKillSessionIfInstance_RejectsUnsafeExpectation(t *testing.T) {
	stubTmux(t, `#!/bin/sh
printf 'tmux must not be invoked at all: %s\n' "$*" >&2
exit 3
`)
	for _, bad := range []string{"", "1,1}#{==:1,1", "4471:1788740000}", "#{pid}", "4471 1788740000"} {
		killed, err := (&tmux.RealExecutor{}).KillSessionIfInstance("$0", bad)
		if killed {
			t.Fatalf("expectation %q must not kill", bad)
		}
		if !errors.Is(err, tmux.ErrUnsafeInstance) {
			t.Fatalf("expectation %q: want ErrUnsafeInstance, got %v", bad, err)
		}
	}
}

// The target is a session ID, never a name: a name can be re-pointed between
// the caller's decision and the kill, and that is the whole bug this exists
// to close.
func TestKillSessionIfInstance_RejectsNonSessionIDTarget(t *testing.T) {
	stubTmux(t, `#!/bin/sh
printf 'tmux must not be invoked at all: %s\n' "$*" >&2
exit 3
`)
	for _, bad := range []string{"=dev", "dev", "$", "$0:", "", "proj-1"} {
		killed, err := (&tmux.RealExecutor{}).KillSessionIfInstance(bad, "4471:1788740000")
		if killed || err == nil {
			t.Fatalf("target %q must be refused, got killed=%v err=%v", bad, killed, err)
		}
	}
}

// The fake models the same contract: it holds a generation, refuses when the
// expectation does not match it, kills by id when it does, and records every
// call either way.
func TestFakeExecutor_KillSessionIfInstance(t *testing.T) {
	f := tmux.NewFakeExecutor()
	f.SetInstance("111:1000")
	f.AddSessionWithID("$4", "proj-1", "/work")

	killed, err := f.KillSessionIfInstance("$4", "222:2000")
	if err != nil || killed {
		t.Fatalf("mismatch must refuse: killed=%v err=%v", killed, err)
	}
	if !f.HasSession("proj-1") {
		t.Fatal("a refused kill must leave the session alone")
	}

	killed, err = f.KillSessionIfInstance("$9", "111:1000")
	if killed || !errors.Is(err, tmux.ErrNoSession) {
		t.Fatalf("unknown id must report ErrNoSession: killed=%v err=%v", killed, err)
	}

	killed, err = f.KillSessionIfInstance("$4", "111:1000")
	if err != nil || !killed {
		t.Fatalf("match must kill: killed=%v err=%v", killed, err)
	}
	if f.HasSession("proj-1") {
		t.Fatal("the session should be gone")
	}

	calls := f.KillIfInstanceCalls()
	want := []tmux.KillIfInstanceCall{{"$4", "222:2000"}, {"$9", "111:1000"}, {"$4", "111:1000"}}
	if len(calls) != len(want) {
		t.Fatalf("calls: got %+v want %+v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("call %d: got %+v want %+v", i, calls[i], want[i])
		}
	}

	f.FailKillIfInstance = true
	f.AddSessionWithID("$5", "proj-2", "/work")
	killed, err = f.KillSessionIfInstance("$5", "111:1000")
	if killed || err == nil {
		t.Fatalf("a simulated failure must report not-killed and an error: killed=%v err=%v", killed, err)
	}
	if !f.HasSession("proj-2") {
		t.Fatal("a failed kill leaves the session")
	}
}
