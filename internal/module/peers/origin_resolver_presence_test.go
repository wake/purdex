package peers

import (
	"os"
	"path/filepath"
	"testing"
)

// LeadPresence (lead-team spec §7.1, P4-2 review H-3) is tied to the lead's
// own process: gone only when that process is dead or reused, or alive in
// another conversation (a manual /clear). Another pid's unverifiable file,
// an empty or missing registry, or the lead's own unreadable file never
// make a live lead process read as gone.
func TestOriginResolver_LeadPresence(t *testing.T) {
	r, dir := resolverFixture(t, allLiveLiveness(fixture76973ProcStart))
	const other = "Mon Sep 14 09:00:00 2026" // a different start time: pid reused
	// pid 40 is alive and its file is truncated: unverifiable, someone else's.
	writeRegistryFixture(t, dir, "40.json", `{"pid":40,"sessionId":"sid-4","procSt`)
	r.m.liveness.PidAlive = func(pid int) bool { return pid != 30 }
	check := func(name, sid string, pid int, procStart string, want Presence) {
		t.Helper()
		if got := r.LeadPresence(sid, pid, procStart); got != want {
			t.Errorf("%s: presence %v, want %v", name, got, want)
		}
	}
	check("listed live", "sid-1", 10, targetProcStart, PresenceLive)
	check("listed live in another process", "sid-2", 30, targetProcStart, PresenceLive)
	check("its pid is dead, beside another pid's bad file", "sid-3", 30, targetProcStart, PresenceGone)
	check("its pid alive with another start time (reused)", "sid-3", 50, other, PresenceGone)
	check("its pid alive in another conversation (manual /clear)", "sid-1-old", 10, targetProcStart, PresenceGone)
	check("its pid alive with no file of its own", "sid-5", 50, targetProcStart, PresenceUnknown)
	check("no recorded process", "sid-3", 0, "", PresenceUnknown)

	// Its own file truncated while its pid lives: unknown, not gone.
	writeRegistryFixture(t, dir, "10.json", `{"pid":10,"sessionId":"sid-1","cwd":"/w","procSt`)
	check("its own file truncated", "sid-1", 10, targetProcStart, PresenceUnknown)

	// An empty, missing or unlistable registry never makes a live pid gone.
	// A dead pid is gone only from a registry that was read (empty): one
	// that is missing or unlistable cannot show the session living on.
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, d := range map[string]string{"empty": t.TempDir(), "missing": filepath.Join(t.TempDir(), "missing"), "unlistable": file} {
		r.m.registryDir = d
		check(name+" registry, live pid", "sid-1", 10, targetProcStart, PresenceUnknown)
		wantDead := PresenceUnknown
		if name == "empty" {
			wantDead = PresenceGone
		}
		check(name+" registry, dead pid", "sid-3", 30, targetProcStart, wantDead)
	}
}
