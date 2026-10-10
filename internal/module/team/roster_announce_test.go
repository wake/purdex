package teammod

import (
	"context"
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// R1-1: a spawned member is announced after its title claim, not before it.
// The roster's title comes from the title store, so a changed sent between
// the member insert and the claim would carry no title and nothing would
// re-announce. The claim hook flushes the publisher at the moment the claim
// runs (a signal sent before it would be published right there, titleless)
// and then writes the title the way the store does. The roster the clients
// end up with must name the member; an earlier changed may not (the accept's
// pending signal is published by that same flush). Mutation gate: signal
// before the claim and none after → red.
func TestRoster_SpawnAnnouncesTheClaimedTitle(t *testing.T) {
	f, root := newTeamFixture(t, 3)
	if err := f.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := f.watchRoster()
	f.titles.mu.Lock()
	f.titles.onClaim = func(sid, label string) {
		f.m.rosterFlush()
		f.so.mu.Lock()
		o := f.so.members[sid]
		o.Title = label
		f.so.members[sid] = o
		f.so.mu.Unlock()
	}
	f.titles.mu.Unlock()

	f.member(1, root, "sid-m1", "w-one", func(r *team.SpawnRequest) { r.Title = "worker-one" })

	// The claim hook flushes whatever signal is pending when the claim runs — the accept's, for one — and that
	// changed may legitimately carry the member before its title. What must hold is that the roster the clients
	// are left with names it: the LAST changed that carries the member has the title.
	var last *team.RosterEventValue
	var lastTitle string
	evs := w.drain()
	for i, ev := range evs {
		for _, tr := range ev.Teams {
			for _, mem := range tr.Members {
				if mem.SessionID == "sid-m1" {
					last, lastTitle = &evs[i], mem.Title
				}
			}
		}
	}
	if last != nil {
		if lastTitle != "worker-one" {
			t.Fatalf("the last %s that carried the new member had title %q, want %q", last.Op, lastTitle, "worker-one")
		}
		return
	}
	t.Fatal("no roster event carried the new member")
}

// A-2: rosterChanged only signals. With the publisher stuck inside the
// resolver, rosterChanged returns, and so does a create whose unattended
// approval runs afterApproved under createMu: no roster I/O is done on the
// caller's thread. Once the resolver is released the publisher announces the
// new team. Mutation gate: rosterSync(true) inside rosterChanged → red.
func TestRosterChanged_NeverBlocks(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(9), "sid-2", 500) // a roster with a session to resolve
	if err := f.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := f.watchRoster()
	entered, release := f.blockResolver()

	returnsPromptly(t, "rosterChanged", f.m.rosterChanged)
	waitEntered(t, entered) // the publisher is in the resolver, and stays there
	returnsPromptly(t, "rosterChanged again", f.m.rosterChanged)

	f.unatt.set(true)
	var code int
	returnsPromptly(t, "an unattended create (afterApproved under createMu)", func() {
		code, _ = f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1)))
	})
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create = %d", code)
	}

	release()
	evs := w.drain()
	if len(evs) == 0 || len(evs[len(evs)-1].Teams) != 2 {
		t.Fatalf("roster events after the release = %+v, want the last to hold the seeded team and the approved one", evs)
	}
}
