package peers

import (
	"os"
	"path/filepath"
	"testing"
)

// PL-1f′: the roster resolves a whole set in one call. It answers what the
// single form answers for each session (same Origin, same not-listed rule)
// and fails only when the registry cannot be read.
func TestOriginResolver_ResolveOriginsBySession(t *testing.T) {
	r, _ := resolverFixture(t, allLiveLiveness(fixture76973ProcStart))
	got, err := r.ResolveOriginsBySession([]string{"sid-1", "sid-2", "sid-99", "", "sid-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("origins = %+v, want sid-1 and sid-2 only", got)
	}
	for _, sid := range []string{"sid-1", "sid-2"} {
		one, ok, err := r.ResolveOriginBySession(sid)
		if !ok || err != nil || got[sid] != one {
			t.Fatalf("%s: batch %+v, single %+v (ok=%v err=%v); they must agree", sid, got[sid], one, ok, err)
		}
	}

	// A dead holder is not listed (the registry was read).
	live := allLiveLiveness(fixture76973ProcStart)
	live.PidAlive = func(pid int) bool { return pid != 10 }
	dead, _ := resolverFixture(t, live)
	if m, err := dead.ResolveOriginsBySession([]string{"sid-1", "sid-2"}); err != nil || len(m) != 1 || m["sid-2"].SessionID != "sid-2" {
		t.Fatalf("dead sid-1: %v %v", m, err)
	}

	// No ids: an empty map, and the registry is not read at all.
	r.m.registryDir = filepath.Join(t.TempDir(), "no-such", "x", "not-a-dir-either")
	if m, err := r.ResolveOriginsBySession(nil); err != nil || len(m) != 0 {
		t.Fatalf("no ids: %v %v", m, err)
	}

	// An unreadable registry is an error, not "nobody is live".
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.m.registryDir = file
	if m, err := r.ResolveOriginsBySession([]string{"sid-1"}); err == nil || m != nil {
		t.Fatalf("unreadable registry: %v %v, want an error", m, err)
	}
}

// Two live entries for one session id (a process pair mid-resume): the
// batch answers the first in registry order, as the single form does.
func TestOriginResolver_ResolveOriginsBySession_DuplicateEntryIsTheFirst(t *testing.T) {
	r, dir := resolverFixture(t, allLiveLiveness(fixture76973ProcStart))
	writeRegistryFixture(t, dir, "11.json", `{"pid":11,"sessionId":"sid-1","cwd":"/w","procStart":"`+targetProcStart+`","version":"2.1.270","messagingSocketPath":"`+dir+`/11.sock","name":"n11","status":"idle"}`)
	one, ok, err := r.ResolveOriginBySession("sid-1")
	if !ok || err != nil {
		t.Fatal(ok, err)
	}
	got, err := r.ResolveOriginsBySession([]string{"sid-1"})
	if err != nil || got["sid-1"] != one {
		t.Fatalf("batch %+v, single %+v (%v)", got["sid-1"], one, err)
	}
}
