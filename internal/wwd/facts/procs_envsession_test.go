package facts

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

func TestSessionIDFromEnv(t *testing.T) {
	const id = "7c1f2a9e-5b3d-4e68-9a0c-1d2e3f4a5b6c"
	if got, ok := SessionIDFromEnv([]string{"PATH=/usr/bin", "CLAUDE_CODE_SESSION_ID=" + id, "HOME=/x"}); !ok || got != id {
		t.Fatalf("%q %v", got, ok)
	}
	for name, env := range map[string][]string{
		"absent":         {"PATH=/usr/bin"},
		"empty":          {"CLAUDE_CODE_SESSION_ID="},
		"has a space":    {"CLAUDE_CODE_SESSION_ID=a b c d e f g h"},
		"too short":      {"CLAUDE_CODE_SESSION_ID=abc"},
		"too long":       {"CLAUDE_CODE_SESSION_ID=" + strings.Repeat("a", 80)},
		"control chars":  {"CLAUDE_CODE_SESSION_ID=abcdefgh\nijkl"},
		"a lookalike":    {"XCLAUDE_CODE_SESSION_ID=" + id, "CLAUDE_CODE_SESSION_ID_X=" + id},
		"other id names": {"CLAUDE_PID=1234", "CODEX_COMPANION_SESSION_ID=" + id},
	} {
		if got, ok := SessionIDFromEnv(env); ok {
			t.Errorf("%s: %q should not be a session id", name, got)
		}
	}
}

// A helper that was started from a session and then reparented (the codex broker: parent 1) has no session above it. Its
// environment says whose it is, and everything in it inherits that.
func envSessionFixture() (ProcSnapshot, ProcContext, *int) {
	snap := ProcSnapshot{Procs: []ProcRec{
		{PID: 1, PPID: 0, UID: 0, Name: "launchd"},
		{PID: 100, PPID: 1, UID: 501, Name: "claude", FootprintBytes: mb(900)}, // a session, ref _abc123
		{PID: 110, PPID: 100, UID: 501, Name: "bash", FootprintBytes: mb(10)},  // under the session: needs no environment
		{PID: 700, PPID: 1, UID: 501, Name: "node", FootprintBytes: mb(80)},    // the reparented broker
		{PID: 710, PPID: 700, UID: 501, Name: "node", FootprintBytes: mb(60)},  // its codex wrapper
		{PID: 720, PPID: 710, UID: 501, Name: "codex", FootprintBytes: mb(40)}, // and the codex run
		{PID: 800, PPID: 1, UID: 501, Name: "node", FootprintBytes: mb(30)},    // reparented, from nowhere known
	}}
	args := map[int][]string{
		110: {"bash"},
		700: {"node", "/x/codex/scripts/app-server-broker.mjs", "serve"},
		710: {"node", "/x/codex/bin/codex.js", "app-server"},
		720: {"codex", "app-server"},
		800: {"node", "/x/playwright/cli.js", "mcp"},
	}
	reads := 0
	ctx := ProcContext{
		SessionOf: func(pid int) (string, bool) { return "_abc123", pid == 100 },
		ArgsOf:    func(pid int) []string { return args[pid] },
		EnvSessionOf: func(pid int) (string, bool) {
			reads++
			if pid == 700 {
				return "_env999", true
			}
			return "", false
		},
	}
	return snap, ctx, &reads
}

func byPID(snap ProcSnapshot, pid int) ProcRec {
	for _, p := range snap.Procs {
		if p.PID == pid {
			return p
		}
	}
	return ProcRec{}
}

func TestAttributeFallsBackToTheEnvironmentSession(t *testing.T) {
	snap, ctx, _ := envSessionFixture()
	ctx.ensureTable(snap)
	// the broker is a resident whose session is the environment's, not "orphan"
	if o := Attribute(byPID(snap, 700), ctx); o == nil || o.Kind != wire.OwnerResident || o.Session != "_env999" {
		t.Fatalf("broker: %+v", o)
	}
	// what it started inherits the session through the chain: the nearest ancestor with one is the broker
	if o := Attribute(byPID(snap, 720), ctx); o == nil || o.Kind != wire.OwnerCodex || o.Session != "_env999" {
		t.Fatalf("codex run: %+v", o)
	}
	// a reparented process with nothing to say stays unattributed (and a resident of nobody stays orphan)
	if o := Attribute(byPID(snap, 800), ctx); o == nil || o.Kind != wire.OwnerResident || o.Session != "orphan" {
		t.Fatalf("lost helper: %+v", o)
	}
}

func TestResidentsUseTheEnvironmentSession(t *testing.T) {
	snap, ctx, _ := envSessionFixture()
	got := BuildResidents(snap, ctx)
	want := map[string]string{"_env999": "codex-broker", "orphan": "playwright"}
	seen := map[string]string{}
	for _, r := range got {
		seen[r.Session] = r.Kind
	}
	for session, kind := range want {
		if seen[session] != kind {
			t.Errorf("residents %+v: want %s of %s", got, kind, session)
		}
	}
}

// The environment is read only for a process whose ancestors lead to no session: one under a session costs nothing.
func TestEnvironmentIsAskedOnlyWhereNoSessionIsAbove(t *testing.T) {
	snap, ctx, reads := envSessionFixture()
	ctx.ensureTable(snap)
	if o := Attribute(byPID(snap, 110), ctx); o == nil || o.Kind != wire.OwnerSession || o.Session != "_abc123" {
		t.Fatalf("under a session: %+v", o)
	}
	if *reads != 0 {
		t.Fatalf("a process under a session read the environment %d times", *reads)
	}
}
