package peers

import (
	"os"
	"path/filepath"
	"testing"
)

// SessionPresence (lead-team spec §7.1, P4-2 review) keeps "could not tell"
// apart from "gone": a team ends only on PresenceGone, so a registry that
// cannot be read or does not exist, or a file whose pid is alive but whose
// contents cannot be verified, never reads as a lead that left.
func TestOriginResolver_SessionPresence(t *testing.T) {
	r, dir := resolverFixture(t, allLiveLiveness(fixture76973ProcStart))
	for sid, want := range map[string]Presence{"sid-1": PresenceLive, "sid-9": PresenceGone, "": PresenceGone} {
		if got := r.SessionPresence(sid); got != want {
			t.Errorf("%q: presence %v, want %v", sid, got, want)
		}
	}

	// The lead's own file truncated mid-write while its pid is alive: the
	// registry reads without error and LiveSession says false, but presence
	// is unknown — for any session the registry does not list as live.
	writeRegistryFixture(t, dir, "10.json", `{"pid":10,"sessionId":"sid-1","cwd":"/w","procSt`)
	if r.LiveSession("sid-1") {
		t.Fatal("precondition: a truncated file is not a live entry")
	}
	for _, sid := range []string{"sid-1", "sid-9"} {
		if got := r.SessionPresence(sid); got != PresenceUnknown {
			t.Errorf("%q with a truncated file of a live pid: presence %v, want unknown", sid, got)
		}
	}
	if got := r.SessionPresence("sid-2"); got != PresenceLive {
		t.Errorf("sid-2 (listed live): presence %v, want live", got)
	}
	// The same file with its pid dead blocks nothing: gone.
	r.m.liveness.PidAlive = func(pid int) bool { return pid != 10 }
	if got := r.SessionPresence("sid-1"); got != PresenceGone {
		t.Errorf("truncated file of a dead pid: presence %v, want gone", got)
	}

	// A registry that cannot be listed, or a registry dir that is missing
	// (ReadRegistry reads that as empty), is unknown.
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, d := range map[string]string{"unreadable": file, "missing": filepath.Join(t.TempDir(), "missing")} {
		r.m.registryDir = d
		if got := r.SessionPresence("sid-1"); got != PresenceUnknown {
			t.Errorf("%s registry: presence %v, want unknown", name, got)
		}
	}
}
