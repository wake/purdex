package peers

import (
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
	if o.Address != "mlab/n10" || o.Title != "lead-team" || o.Ref != ipeers.RefID("sid-1") {
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
