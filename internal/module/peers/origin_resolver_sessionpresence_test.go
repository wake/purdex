package peers

import (
	"os"
	"path/filepath"
	"testing"
)

// #1857: SessionPresence is LiveSession's three-state form for a caller that knows only the session id. Gone only when the
// registry was read and nothing could still be the session: no listed entry has it AND no live pid has a file that could not be
// verified (that file might be it). A registry that cannot be read, or such a file, is unknown. Mutation gates: unknown as gone
// (each unknown row red); a bad file of a DEAD pid blocking (the dead-pid row red).
func TestOriginResolver_SessionPresence(t *testing.T) {
	r, dir := resolverFixture(t, allLiveLiveness(fixture76973ProcStart))
	r.m.liveness.PidAlive = func(pid int) bool { return pid != 30 }
	check := func(name, sid string, want Presence) {
		t.Helper()
		if got := r.SessionPresence(sid); got != want {
			t.Errorf("%s: presence %v, want %v", name, got, want)
		}
	}
	check("listed", "sid-1", PresenceLive)
	check("not listed, registry clean", "sid-9", PresenceGone)
	check("no session id", "", PresenceGone)

	// pid 40 is alive and its file is truncated: it might be anyone, so an unlisted session is unknown, not gone.
	writeRegistryFixture(t, dir, "40.json", `{"pid":40,"sessionId":"sid-4","procSt`)
	check("not listed, a live pid's file unverifiable", "sid-9", PresenceUnknown)
	check("listed, beside an unverifiable file", "sid-1", PresenceLive)

	// the same kind of file under a DEAD pid proves nothing about a live session
	if err := os.Remove(filepath.Join(dir, "40.json")); err != nil {
		t.Fatal(err)
	}
	writeRegistryFixture(t, dir, "30.json", `{"pid":30,"sessionId":"sid-3","procSt`)
	check("not listed, an unverifiable file of a dead pid", "sid-9", PresenceGone)

	// a registry that cannot be listed is unknown; a missing dir is an empty registry (gone)
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.m.registryDir = file
	check("unlistable registry", "sid-1", PresenceUnknown)
	r.m.registryDir = filepath.Join(t.TempDir(), "missing")
	check("missing registry dir", "sid-1", PresenceGone)
}
