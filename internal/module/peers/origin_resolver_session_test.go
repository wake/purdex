package peers

import (
	"os"
	"path/filepath"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
)

// Lead-team-relay spec §8.3: the relay routes attribute the caller by CC
// session id. Same Origin as the inbox path, same not-found and read-error
// contract.
func TestOriginResolver_ResolveOriginBySession(t *testing.T) {
	r, dir := resolverFixture(t, allLiveLiveness(fixture76973ProcStart))
	o, ok, err := r.ResolveOriginBySession("sid-1")
	if !ok || err != nil {
		t.Fatalf("sid-1 must resolve: ok=%v err=%v", ok, err)
	}
	byInbox, _, _ := r.ResolveOrigin(dir + "/10.sock")
	if o != byInbox {
		t.Fatalf("by session = %+v, by inbox = %+v; they must agree", o, byInbox)
	}
	if o.Address != "mlab/"+vname(t, "n10", "sid-1") || o.Title != "lead-team" || o.Ref != ipeers.RefID("sid-1") {
		t.Fatalf("origin = %+v", o)
	}
	for _, sid := range []string{"", "sid-99"} {
		if _, ok, err := r.ResolveOriginBySession(sid); ok || err != nil {
			t.Fatalf("%q: ok=%v err=%v, want not found without error", sid, ok, err)
		}
	}
	// A dead holder does not resolve (the registry was read; it is not live).
	live := allLiveLiveness(fixture76973ProcStart)
	live.PidAlive = func(pid int) bool { return pid != 10 }
	dead, _ := resolverFixture(t, live)
	if _, ok, err := dead.ResolveOriginBySession("sid-1"); ok || err != nil {
		t.Fatalf("dead: ok=%v err=%v", ok, err)
	}
}

// Adopt plan PL-1c0: `pdx adopt <ref>` names its target by the conversation's current ref. The origin is the one
// the inbox and session paths answer; a dead holder, an unknown ref and an empty one are "not found" without an
// error; an unreadable registry is an error.
func TestOriginResolver_ResolveOriginByRef(t *testing.T) {
	r, _ := resolverFixture(t, allLiveLiveness(fixture76973ProcStart))
	o, ok, err := r.ResolveOriginByRef(ipeers.RefID("sid-1"))
	if !ok || err != nil {
		t.Fatalf("sid-1's ref must resolve: ok=%v err=%v", ok, err)
	}
	bySession, _, _ := r.ResolveOriginBySession("sid-1")
	if o != bySession || o.SessionID != "sid-1" {
		t.Fatalf("by ref = %+v, by session = %+v; they must agree", o, bySession)
	}
	for _, ref := range []string{"", "_nosuch", "sid-1"} { // a session id is not a ref
		if _, ok, err := r.ResolveOriginByRef(ref); ok || err != nil {
			t.Fatalf("%q: ok=%v err=%v, want not found without error", ref, ok, err)
		}
	}
	live := allLiveLiveness(fixture76973ProcStart)
	live.PidAlive = func(pid int) bool { return pid != 10 }
	dead, _ := resolverFixture(t, live)
	if _, ok, err := dead.ResolveOriginByRef(ipeers.RefID("sid-1")); ok || err != nil {
		t.Fatalf("dead: ok=%v err=%v", ok, err)
	}
	bad, _ := resolverFixture(t, allLiveLiveness(fixture76973ProcStart))
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad.m.registryDir = file
	if _, ok, err := bad.ResolveOriginByRef(ipeers.RefID("sid-1")); ok || err == nil {
		t.Fatalf("unreadable registry: ok=%v err=%v, want an error", ok, err)
	}
}

func TestOriginResolver_InboxOf(t *testing.T) {
	r, dir := resolverFixture(t, allLiveLiveness(fixture76973ProcStart))
	if inbox, ok, err := r.InboxOf("sid-1"); !ok || err != nil || inbox != dir+"/10.sock" {
		t.Fatalf("sid-1 inbox = %q ok=%v err=%v", inbox, ok, err)
	}
	for _, sid := range []string{"", "sid-99"} {
		if _, ok, err := r.InboxOf(sid); ok || err != nil {
			t.Fatalf("%q: ok=%v err=%v", sid, ok, err)
		}
	}
	live := allLiveLiveness(fixture76973ProcStart)
	live.PidAlive = func(pid int) bool { return pid != 10 }
	dead, _ := resolverFixture(t, live)
	if _, ok, err := dead.InboxOf("sid-1"); ok || err != nil {
		t.Fatalf("dead: ok=%v err=%v", ok, err)
	}
}
