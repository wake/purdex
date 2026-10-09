package teammod

import (
	"context"
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// The roster's adopt / release delta (adopt plan PL-1f″). sid-1 leads; sid-2 (_def456) is the adopted target.

// adoptedRoster makes sid-2 a tmux-hosted session (the user's own tmux session "mine") the lead can adopt.
func (f *fixture) showTarget() {
	f.origins.show(team.Origin{SessionID: "sid-2", Ref: "_def456", Name: "two", PID: 20, ProcStart: "Sun Sep 13 15:22:36 2026", Cwd: "/w2", Tmux: "mine:@1.%3", Address: "mlab/two"})
}

func rosterMembers(t *testing.T, ev team.RosterEventValue) []team.RosterMember {
	t.Helper()
	if len(ev.Teams) != 1 {
		t.Fatalf("roster teams = %+v", ev.Teams)
	}
	return ev.Teams[0].Members
}

// Mutation gate: leave the origin constant in place → the adopted member reads spawned → red.
func TestRoster_AdoptedMemberIsListedWithItsOriginAndTmuxName(t *testing.T) {
	f := newFixture(t)
	f.showTarget()
	f.adoptedMember(t)
	ms := f.getRoster().Teams[0].Members
	if len(ms) != 1 || ms[0].SessionID != "sid-2" || ms[0].Origin != team.MemberOriginAdopted || ms[0].TmuxSession != "mine" {
		t.Fatalf("members = %+v, want the adopted sid-2 (origin adopted, the user's tmux session \"mine\")", ms)
	}
}

// Mutation gate: include released members → red.
func TestRoster_ReleasedMemberIsOut(t *testing.T) {
	f := newFixture(t)
	f.adoptedMember(t)
	if code, _, _, body := f.release("_def456"); code != http.StatusOK {
		t.Fatalf("release: %d %s", code, body)
	}
	if ms := f.getRoster().Teams[0].Members; len(ms) != 0 {
		t.Fatalf("members = %+v, want the released one out", ms)
	}
}

// Every approve path of an adopt request announces the roster once, from the winner point
// (afterApproved). Mutation gate: announce from handleCreateAdopt instead → the click, sweep, tick and boot paths red.
func TestRoster_ChangedOnEveryAdoptApprovePath(t *testing.T) {
	paths := map[string]func(f *fixture){
		"click":  func(f *fixture) { f.adoptOK(uid(10), "_def456"); f.decide(uid(10), "approve") },
		"create": func(f *fixture) { f.unatt.set(true); f.adoptOK(uid(10), "_def456") },
		"sweep":  func(f *fixture) { f.adoptOK(uid(10), "_def456"); f.unatt.set(true); f.sweep() },
		"tick":   func(f *fixture) { f.adoptOK(uid(10), "_def456"); f.unatt.set(true); f.m.tick() },
		"boot": func(f *fixture) {
			f.adoptOK(uid(10), "_def456")
			f.unatt.set(true)
			_ = f.m.Start(context.Background())
		},
	}
	for name, run := range paths {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.approveLead(uid(1))
			f.rosterBaselineNow()
			w := f.watchRoster()
			w.drain()
			run(f)
			ms := rosterMembers(t, w.one(name))
			if len(ms) != 1 || ms[0].SessionID != "sid-2" || ms[0].Origin != team.MemberOriginAdopted {
				t.Fatalf("roster members = %+v, want the adopted sid-2", ms)
			}
		})
	}
	t.Run("open request announces nothing", func(t *testing.T) {
		f := newFixture(t)
		f.approveLead(uid(1))
		f.rosterBaselineNow()
		w := f.watchRoster()
		f.adoptOK(uid(10), "_def456")
		w.none("an open adopt request")
	})
}

// A release and the kill of an adopted member each announce the roster once, from the write itself.
func TestRoster_ChangedAfterReleaseAndAdoptedKill(t *testing.T) {
	f := newFixture(t)
	f.adoptedMember(t)
	f.rosterBaselineNow()
	w := f.watchRoster()
	w.drain() // a signal the setup left pending would otherwise stand in for the release's own
	if code, _, _, body := f.release("_def456"); code != http.StatusOK {
		t.Fatalf("release: %d %s", code, body)
	}
	if ms := rosterMembers(t, w.one("release")); len(ms) != 0 {
		t.Fatalf("after the release: %+v", ms)
	}
	again := f.adoptOK(uid(30), "_def456")
	f.decide(again.ID, "approve")
	if ms := rosterMembers(t, w.one("re-adopt")); len(ms) != 1 {
		t.Fatalf("after the re-adoption: %+v", ms)
	}
	f.m.killProcess = (&killRec{}).kill
	if code, _, _, body := f.killTarget("_def456"); code != http.StatusOK {
		t.Fatalf("kill: %d %s", code, body)
	}
	if ms := rosterMembers(t, w.one("kill")); len(ms) != 0 {
		t.Fatalf("after the kill: %+v", ms)
	}
}

// GET /api/team names an adopted member's origin and its adoption's request id; the wire's spawn_op stays empty.
// Mutation gate: memberView back to the constant → red.
func TestTeamGet_AdoptedMemberOriginIsAdopted(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	code, v, e := f.teamView("/tmp/10.sock")
	if code != http.StatusOK || len(v.Members) != 1 {
		t.Fatalf("GET /api/team = %d %+v %+v", code, v, e)
	}
	if m := v.Members[0]; m.Origin != team.MemberOriginAdopted || m.AdoptRequest != key || m.SpawnOp != "" {
		t.Fatalf("member = %+v, want origin adopted, adopt_request %s, no spawn_op", m, key)
	}
}
