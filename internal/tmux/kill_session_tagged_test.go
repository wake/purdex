// internal/tmux/kill_session_tagged_test.go — the kill that also compares the spawn tag.
//
// KillSessionIfInstance compares the generation in the one tmux invocation that kills, but not WHO the session belongs to:
// a boot sweep that read the owner (the @pdx_spawn_op tag) and then killed left a window in which the user could clear or
// rewrite the tag and the kill would still land (#2350). KillSessionIfTagged puts the tag in the same condition.
//
// The real-tmux tests run on their OWN tmux server: a PATH wrapper puts `-L <unique label>` in front of every tmux call the
// executor makes, and TMUX is cleared, so nothing here can reach the user's server (and kill-server is only ever run through
// that wrapper, labelled).
package tmux_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/tmux"
)

const (
	tagOpt = "@pdx_spawn_op"
	opA    = "11111111-1111-4111-8111-111111111111"
	opB    = "22222222-2222-4222-8222-222222222222"
)

// ownTmux points every tmux call of this test at a private server and returns a runner for setup commands.
func ownTmux(t *testing.T) func(args ...string) string {
	t.Helper()
	real, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed")
	}
	label := fmt.Sprintf("pdxtest-%d-%d", os.Getpid(), time.Now().UnixNano())
	dir := t.TempDir()
	wrapper := fmt.Sprintf("#!/bin/sh\nexec %q -L %q \"$@\"\n", real, label)
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", "") // never the user's own server
	run := func(args ...string) string {
		out, err := exec.Command("tmux", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-server").Run() }) // through the wrapper: labelled
	return run
}

// session makes a detached session tagged with value ("" = untagged) and returns its identity as the executor reads it.
func session(t *testing.T, run func(...string) string, name, value string) tmux.PaneIdentity {
	t.Helper()
	run("new-session", "-d", "-s", name, "-c", os.TempDir())
	if value != "" {
		run("set-option", "-t", "="+name+":", tagOpt, value)
	}
	id, err := (&tmux.RealExecutor{}).PaneIdentity(context.Background(), "="+name+":", tagOpt)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func alive(run func(...string) string, name string) bool {
	return exec.Command("tmux", "has-session", "-t", "="+name).Run() == nil
}

func TestKillSessionIfTagged_TheTagStillMatches_Kills(t *testing.T) {
	run := ownTmux(t)
	id := session(t, run, "match", opA)
	killed, err := (&tmux.RealExecutor{}).KillSessionIfTagged(id.SessionID, id.Instance, tagOpt, opA)
	if err != nil || !killed {
		t.Fatalf("killed=%v err=%v, want the session killed", killed, err)
	}
	if alive(run, "match") {
		t.Fatal("the session is still there")
	}
}

// The point of the change: the owner changed after the read, and the kill — evaluated by the server — declines.
// Mutation gate: drop the tag from the condition → these three are red.
func TestKillSessionIfTagged_TheTagChanged_DeclinesAndTheSessionLives(t *testing.T) {
	for name, change := range map[string]func(run func(...string) string){
		"rewritten to another op": func(run func(...string) string) { run("set-option", "-t", "=chg:", tagOpt, opB) },
		"cleared":                 func(run func(...string) string) { run("set-option", "-t", "=chg:", tagOpt, "") },
		"unset":                   func(run func(...string) string) { run("set-option", "-u", "-t", "=chg:", tagOpt) },
	} {
		run := ownTmux(t)
		id := session(t, run, "chg", opA) // the read that found it as an orphan of opA
		change(run)                       // ... and then the user takes it over
		killed, err := (&tmux.RealExecutor{}).KillSessionIfTagged(id.SessionID, id.Instance, tagOpt, opA)
		if err != nil || killed {
			t.Fatalf("%s: killed=%v err=%v, want a decline", name, killed, err)
		}
		if !alive(run, "chg") {
			t.Fatalf("%s: the session was killed although its tag no longer matched", name)
		}
	}
}

func TestKillSessionIfTagged_AnotherGeneration_Declines(t *testing.T) {
	run := ownTmux(t)
	id := session(t, run, "gen", opA)
	killed, err := (&tmux.RealExecutor{}).KillSessionIfTagged(id.SessionID, "1:1", tagOpt, opA)
	if err != nil || killed || !alive(run, "gen") {
		t.Fatalf("killed=%v err=%v alive=%v, want a decline with the session alive", killed, err, alive(run, "gen"))
	}
}

// Only the session the id names: another session with the very same tag is not touched.
func TestKillSessionIfTagged_OnlyTheNamedSession(t *testing.T) {
	run := ownTmux(t)
	a := session(t, run, "one", opA)
	session(t, run, "two", opA)
	if killed, err := (&tmux.RealExecutor{}).KillSessionIfTagged(a.SessionID, a.Instance, tagOpt, opA); err != nil || !killed {
		t.Fatalf("killed=%v err=%v", killed, err)
	}
	if alive(run, "one") || !alive(run, "two") {
		t.Fatalf("one alive=%v two alive=%v, want only one gone", alive(run, "one"), alive(run, "two"))
	}
}

// The value goes into a tmux format: only a spawn op id (a lower-case UUID) is accepted, so no `}`, `,`, `#` or quote can
// rewrite the condition. A refusal is an error and tmux is never run. Mutation gate: skip the check → red.
func TestKillSessionIfTagged_ValueThatIsNoSpawnOpId_IsRefusedBeforeTmuxRuns(t *testing.T) {
	stubTmux(t, `#!/bin/sh
printf 'tmux must not run for a bad value: %s\n' "$*" >&2
exit 3
`)
	for _, v := range []string{"", "x", opA + "}", "}{,#{1}", "#{pid}", opA + ",#{==:1,1}", "ABCDEF12-3456-4789-8ABC-DEF123456789", "11111111-1111-4111-8111-11111111111", "a b", "'; kill-server; '"} {
		killed, err := (&tmux.RealExecutor{}).KillSessionIfTagged("$1", "4471:1788740000", tagOpt, v)
		if err == nil || killed || strings.Contains(err.Error(), "must not run") {
			t.Errorf("value %q: killed=%v err=%v, want a refusal before tmux", v, killed, err)
		}
	}
	for _, o := range []string{"", "pdx_spawn_op", "@a b", "@x}", "@x,y"} {
		if killed, err := (&tmux.RealExecutor{}).KillSessionIfTagged("$1", "4471:1788740000", o, opA); err == nil || killed {
			t.Errorf("option %q: killed=%v err=%v, want a refusal", o, killed, err)
		}
	}
	if killed, err := (&tmux.RealExecutor{}).KillSessionIfTagged("name", "4471:1788740000", tagOpt, opA); err == nil || killed {
		t.Errorf("session id %q: killed=%v err=%v, want a refusal", "name", killed, err)
	}
}

// The exact argv: ONE `if-shell -F -t '<id>:'` whose condition has the generation AND the tag.
func TestKillSessionIfTagged_SingleInvocationCarriesGenerationTagAndKill(t *testing.T) {
	stubTmux(t, fmt.Sprintf(`#!/bin/sh
[ "$1" = "if-shell" ] && [ "$2" = "-F" ] && [ "$3" = "-t" ] && [ "$4" = '$3:' ] || { printf 'bad head: %%s\n' "$*" >&2; exit 2; }
[ "$5" = '#{&&:#{==:#{pid}:#{start_time},4471:1788740000},#{==:#{%s},%s}}' ] || { printf 'bad condition: %%s\n' "$5" >&2; exit 2; }
[ "$6" = "kill-session -t '\$3'" ] || { printf 'bad kill: %%s\n' "$6" >&2; exit 2; }
case "$7" in display-message*) ;; *) printf 'bad else: %%s\n' "$7" >&2; exit 2 ;; esac
[ -z "$8" ] || { printf 'extra args: %%s\n' "$*" >&2; exit 2; }
`, tagOpt, opA))
	killed, err := (&tmux.RealExecutor{}).KillSessionIfTagged("$3", "4471:1788740000", tagOpt, opA)
	if err != nil || !killed {
		t.Fatalf("killed=%v err=%v", killed, err)
	}
}

// A session that is already gone is a decline, not a kill of anything: the kill names its target by id, so it can only ever land
// on that session, and no other session (here one with the very same tag) is touched.
func TestKillSessionIfTagged_NoSuchSession_DeclinesAndTouchesNothing(t *testing.T) {
	run := ownTmux(t)
	id := session(t, run, "keep", opA) // a live server, so the generation matches
	killed, err := (&tmux.RealExecutor{}).KillSessionIfTagged("$999", id.Instance, tagOpt, opA)
	if killed || (err != nil && !errors.Is(err, tmux.ErrNoSession)) {
		t.Fatalf("killed=%v err=%v, want nothing killed (a decline or ErrNoSession)", killed, err)
	}
	if !alive(run, "keep") {
		t.Fatal("an unrelated session — one carrying the very same tag — was killed")
	}
}

// The fake keeps the real contract: generation AND tag are compared where the kill happens, a decline touches nothing.
func TestFakeKillSessionIfTagged_FollowsTheRealContract(t *testing.T) {
	f := tmux.NewFakeExecutor()
	f.SetInstance("4471:1788740000")
	f.AddSession("s", "/w")
	f.SetSessionTag("s", tagOpt, opA)
	ss, _ := f.ListSessions(context.Background())
	id := ss[0].ID
	inst := (func() string { p, _ := f.PaneIdentity(context.Background(), "="+"s"+":", tagOpt); return p.Instance })()
	if killed, err := f.KillSessionIfTagged(id, inst, tagOpt, opB); err != nil || killed {
		t.Fatalf("another tag: killed=%v err=%v", killed, err)
	}
	if killed, err := f.KillSessionIfTagged(id, "x:1", tagOpt, opA); err != nil || killed {
		t.Fatalf("another generation: killed=%v err=%v", killed, err)
	}
	if killed, err := f.KillSessionIfTagged(id, inst, tagOpt, "not-a-uuid"); err == nil || killed {
		t.Fatalf("a value that is no spawn op id: killed=%v err=%v, want a refusal", killed, err)
	}
	if !f.HasSession("s") {
		t.Fatal("a declined kill removed the session")
	}
	if killed, err := f.KillSessionIfTagged(id, inst, tagOpt, opA); err != nil || !killed || f.HasSession("s") {
		t.Fatalf("a match: killed=%v err=%v has=%v", killed, err, f.HasSession("s"))
	}
}

// The condition reads the TARGET session's option, not the server's current session's (`-t '$N:'`). Here the session to kill
// matches and the one created after it — the server's current one — carries another tag: without the target the condition would
// read the wrong session and decline. Mutation gate: drop `-t` → red.
func TestKillSessionIfTagged_ReadsTheTargetSessionsTagNotTheCurrentOnes(t *testing.T) {
	run := ownTmux(t)
	target := session(t, run, "target", opA)
	session(t, run, "newer", opB) // created last: the one the server would call current
	killed, err := (&tmux.RealExecutor{}).KillSessionIfTagged(target.SessionID, target.Instance, tagOpt, opA)
	if err != nil || !killed {
		t.Fatalf("killed=%v err=%v, want the matching session killed whatever the current one carries", killed, err)
	}
	if alive(run, "target") || !alive(run, "newer") {
		t.Fatalf("target alive=%v newer alive=%v, want only target gone", alive(run, "target"), alive(run, "newer"))
	}
	// and the reverse: the target does NOT match while the current session does
	a := session(t, run, "mismatch", opB)
	session(t, run, "current-matches", opA)
	if killed, err := (&tmux.RealExecutor{}).KillSessionIfTagged(a.SessionID, a.Instance, tagOpt, opA); err != nil || killed || !alive(run, "mismatch") {
		t.Fatalf("killed=%v err=%v alive=%v, want a decline: the target's own tag is not opA", killed, err, alive(run, "mismatch"))
	}
}
