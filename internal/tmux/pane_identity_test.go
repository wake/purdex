package tmux

import (
	"errors"
	"github.com/wake/purdex/internal/claudeenv"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

const testTag = "11111111-2222-4333-8444-555555555555"

// One tmux invocation reads the server's generation, the pane's session and
// pane ids, one session user option and the pane's directory, so all of
// them are one server's answer (lead-team P4-5 review H3/H4).
func TestPaneIdentityArgs(t *testing.T) {
	got, err := paneIdentityArgs("%5", "@pdx_spawn_op")
	want := []string{"display-message", "-p", "-t", "%5",
		"#{pid}:#{start_time} #{session_id} #{pane_id} [#{@pdx_spawn_op}] #{pane_current_path}"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, %v", got, err)
	}
	for _, bad := range []string{"pdx", "@a}b", "@", "@a b"} {
		if _, err := paneIdentityArgs("%5", bad); err == nil {
			t.Errorf("option %q accepted", bad)
		}
	}
}

// A target tmux cannot find prints an empty line and exits 0 (measured,
// tmux 3.6a), so every field must be there and well formed.
func TestParsePaneIdentity(t *testing.T) {
	got, err := parsePaneIdentity("86338:1791397025 $0 %0 [" + testTag + "] /private/tmp/a b\n")
	want := PaneIdentity{Instance: "86338:1791397025", SessionID: "$0", PaneID: "%0", Tag: testTag, Cwd: "/private/tmp/a b"}
	if err != nil || got != want {
		t.Fatalf("parse = %+v, %v", got, err)
	}
	if got, err := parsePaneIdentity("86338:1791397025 $1 %1 [] /w\n"); err != nil || got.Tag != "" || got.Cwd != "/w" {
		t.Fatalf("untagged = %+v, %v", got, err)
	}
	for _, bad := range []string{"\n", "", "86338:1 0 %0 [] /w", "86338:1 $0 0 [] /w", "86338:1 $0 %0 x /w", "86338:1 $0 %0 []"} {
		_, err := parsePaneIdentity(bad)
		if err == nil {
			t.Errorf("%q parsed", bad)
		}
		if absent := strings.TrimSpace(bad) == ""; errors.Is(err, ErrNoSession) != absent {
			t.Errorf("%q: ErrNoSession = %v, want %v (only the empty line says the target is gone)", bad, !absent, absent)
		}
	}
}

// A failed identity read is ErrNoSession only when tmux said so (no server,
// a target it cannot find); anything else is unknown (lead-team P4-6 critic).
func TestIdentityReadErr_OnlyANoSessionAnswerIsErrNoSession(t *testing.T) {
	exit := func(stderr string) error { return &exec.ExitError{Stderr: []byte(stderr)} }
	for _, c := range []struct {
		err    error
		absent bool
	}{
		{exit("no server running on /private/tmp/tmux-501/default\n"), true},
		{exit("can't find session: $7\n"), true},
		{exit("error connecting to " + t.TempDir() + " (Permission denied)\n"), false}, // the socket is there
		{exit("server exited unexpectedly\n"), false},
		{errors.New("signal: killed"), false},
	} {
		if got := identityReadErr(c.err); got == nil || errors.Is(got, ErrNoSession) != c.absent {
			t.Errorf("%v → %v, want ErrNoSession=%v", c.err, got, c.absent)
		}
	}
}

// new-session and set-option go in one command list: the option lands on
// the session being created and nowhere else, and new-session prints the
// id and generation of the session it made even when the set-option after
// it fails (exit 1; nothing printed when new-session itself fails;
// measured, tmux 3.6a).
func TestNewSessionTaggedArgs(t *testing.T) {
	got, err := newSessionTaggedArgs("tm-abc", "/w", "@pdx_spawn_op", testTag)
	want := append(claudeenv.TmuxGlobalUnsetArgs(), "new-session", "-d", "-s", "tm-abc", "-c", "/w", "-P", "-F", "#{session_id} #{pid}:#{start_time}",
		";", "set-option", "@pdx_spawn_op", testTag) // behind the Claude session variables' global unset (#2122)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, %v", got, err)
	}
	if id, inst := parseCreatedSession("$1 86338:1791397025\n"); id != "$1" || inst != "86338:1791397025" {
		t.Fatalf("created = %q %q", id, inst)
	}
	for _, bad := range []string{"", "\n", "$1", "1 86338:1", "$1 a;b"} {
		if id, inst := parseCreatedSession(bad); id != "" || inst != "" {
			t.Errorf("%q parsed as %q %q", bad, id, inst)
		}
	}
	for _, bad := range [][2]string{{"pdx", testTag}, {"@pdx_spawn_op", "a b"}, {"@pdx_spawn_op", "a;"}, {"@pdx_spawn_op", ""}} {
		if _, err := newSessionTaggedArgs("tm-abc", "/w", bad[0], bad[1]); err == nil {
			t.Errorf("option %q value %q accepted", bad[0], bad[1])
		}
	}
}

// The fake answers a tagged session's identity by its pane id or its
// exact-name target, and fails for a pane it does not hold.
func TestFakeExecutor_TaggedSessionIdentity(t *testing.T) {
	f := NewFakeExecutor()
	f.SetInstance("1:2")
	if id, inst, err := f.NewSessionTaggedContext(t.Context(), "tm-abc", "/w", "@pdx_spawn_op", testTag); err != nil || id != "$0" || inst != "1:2" {
		t.Fatalf("create = %q %q %v", id, inst, err)
	}
	f.SetActivePaneMetadata("tm-abc", TmuxPaneMetadata{SessionID: "$0", SessionName: "tm-abc", PaneID: "%0"})
	f.SetPaneCwd("%0", "/w")
	want := PaneIdentity{Instance: "1:2", SessionID: "$0", PaneID: "%0", Tag: testTag, Cwd: "/w"}
	for _, target := range []string{"%0", "=tm-abc:", "$0:"} {
		if got, err := f.PaneIdentity(t.Context(), target, "@pdx_spawn_op"); err != nil || got != want {
			t.Fatalf("identity of %s = %+v, %v", target, got, err)
		}
	}
	if _, err := f.PaneIdentity(t.Context(), "%9", "@pdx_spawn_op"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("a pane the fake does not hold = %v, want ErrNoSession", err)
	}
	f.SetPaneIdentityErr(errors.New("tmux: busy"))
	if _, err := f.PaneIdentity(t.Context(), "%0", "@pdx_spawn_op"); err == nil || errors.Is(err, ErrNoSession) {
		t.Fatalf("an injected failure = %v, want a plain error", err)
	}
}
