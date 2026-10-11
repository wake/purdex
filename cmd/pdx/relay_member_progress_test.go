package main

import (
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// fakeRelayClock makes relayNow advance by step at every reading, so the elapsed seconds in the progress lines are exact.
func fakeRelayClock(t *testing.T, step time.Duration) {
	t.Helper()
	old := relayNow
	at := time.Unix(1_700_000_000, 0)
	relayNow = func() time.Time { at = at.Add(step); return at }
	t.Cleanup(func() { relayNow = old })
}

// c933b0-129 ③: `pdx relay <ref> --wait` printed nothing until the op ended, so a lead waiting minutes for a member's seed turn
// looked hung and was interrupted (Aigora 2026-10-11). Every state change is now one stderr line with the elapsed seconds; the
// exit code and stdout are untouched. Mutation: drop the progress print → the order check is red.
func TestRelayCmd_WaitPrintsOneLinePerStateChangeInOrder(t *testing.T) {
	fakeRelayClock(t, 5*time.Second)
	seen := opIn(team.RelayRequested, "")
	seen.SeenAt = 1
	d := &fakeMemberRelayDaemon{polls: []team.RelayOp{
		opIn(team.RelayRequested, ""), // the same state again: no line
		seen,                          // the member's mod saw the control message
		opIn(team.RelayClaimed, ""), opIn(team.RelayClaimed, ""),
		opIn(team.RelayWriting, ""), opIn(team.RelayWritten, ""), opIn(team.RelayCleared, ""), opIn(team.RelayDone, ""),
	}}
	code, out, errs := driveMemberRelay(t, d, "/tmp/x.sock", "_mem001", "--wait", "5m")
	if code != ExitOK || !strings.Contains(out, `"state":"done"`) {
		t.Fatalf("exit %d out=%q: the final output and exit code must not change", code, out)
	}
	var lines []string
	for _, l := range strings.Split(errs, "\n") {
		if strings.HasPrefix(l, "接力 ") {
			lines = append(lines, l)
		}
	}
	want := []string{"已看到", "claimed", "writing", "written", "cleared", "done"}
	if len(lines) != len(want) {
		t.Fatalf("%d progress lines, want %d:\n%s", len(lines), len(want), errs)
	}
	for i, w := range want {
		if !strings.Contains(lines[i], w) || !strings.Contains(lines[i], "s）") {
			t.Errorf("line %d = %q, want it to mention %q and the elapsed seconds", i, lines[i], w)
		}
	}
	if !strings.Contains(lines[len(lines)-1], "cleared → done") {
		t.Errorf("last line = %q", lines[len(lines)-1])
	}
}

// A state that lasts (the seed turn after `cleared`) must not leave the terminal silent either: a line every minute.
func TestRelayCmd_WaitSaysItIsStillGoingWhileAStateLasts(t *testing.T) {
	fakeRelayClock(t, 20*time.Second)
	polls := []team.RelayOp{opIn(team.RelayCleared, "")}
	for i := 0; i < 8; i++ {
		polls = append(polls, opIn(team.RelayCleared, ""))
	}
	polls = append(polls, opIn(team.RelayDone, ""))
	d := &fakeMemberRelayDaemon{polls: polls}
	code, _, errs := driveMemberRelay(t, d, "/tmp/x.sock", "_mem001", "--wait", "5m")
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if n := strings.Count(errs, "仍在進行"); n < 2 {
		t.Fatalf("%d heartbeat lines over ~3 minutes in one state, want at least 2:\n%s", n, errs)
	}
}
