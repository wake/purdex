package teammod

import (
	"net/http"
	"testing"
)

// GET /api/team shows relay_ask_until (unix ms) on a member while its ask is open (spec 2026-10-10-member-relay-ask §3.6).

func TestTeamView_RelayAskUntilOnlyWhileTheAskIsOpen(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("3")
	until := func() int64 {
		t.Helper()
		code, v, e := f.teamView("/tmp/10.sock")
		if code != http.StatusOK || len(v.Members) != 1 {
			t.Fatalf("view: %d %+v %+v", code, v, e)
		}
		return v.Members[0].RelayAskUntil
	}
	if got := until(); got != 0 {
		t.Fatalf("no ask: relay_ask_until = %d", got)
	}
	a := f.openAsk(rid(900))
	if got := until(); got != a.ExpiresAt {
		t.Fatalf("open ask: relay_ask_until = %d, want %d", got, a.ExpiresAt)
	}
	if _, err := f.m.store.db.Exec(`UPDATE relay_asks SET state = 'accepted', closed_at = 1 WHERE id = ?`, a.ID); err != nil {
		t.Fatal(err)
	}
	if got := until(); got != 0 {
		t.Fatalf("accepted ask: relay_ask_until = %d", got)
	}
	// an open ask whose window has passed (the sweeper has not closed it yet) is not shown either
	b := f.openAsk(rid(901))
	f.clock.Store(b.ExpiresAt)
	if got := until(); got != 0 {
		t.Fatalf("past its window: relay_ask_until = %d", got)
	}
}
