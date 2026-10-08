package teammod

import (
	"context"
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// Decision 2 / PL-1f′: a lead approve announces the roster on every path
// it can take — a click, a create while the switch is on, the switch-on
// sweep, the tick's reconciliation and boot — one changed each, from the
// one place all of them end in (afterApproved). Mutation gate: broadcast
// from the decide handler instead → the create-time, sweep, tick and boot
// rows red.
func TestRoster_ChangedOnEveryLeadApprovePath(t *testing.T) {
	paths := []struct {
		name string
		run  func(f *fixture, w *rosterWatch)
	}{
		{"click", func(f *fixture, w *rosterWatch) {
			f.create(uid(1))
			w.none("an open request")
			f.approveClick(uid(1))
		}},
		{"create-time", func(f *fixture, w *rosterWatch) {
			f.unatt.set(true)
			f.create(uid(1))
		}},
		{"switch-on sweep", func(f *fixture, w *rosterWatch) {
			f.create(uid(1))
			f.unatt.set(true)
			f.sweep()
		}},
		{"tick", func(f *fixture, w *rosterWatch) {
			f.create(uid(1))
			f.unatt.set(true)
			f.m.tick()
		}},
		{"boot", func(f *fixture, w *rosterWatch) {
			f.create(uid(1))
			f.unatt.set(true)
			if err := f.m.Start(context.Background()); err != nil {
				f.t.Fatal(err)
			}
		}},
	}
	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			f := newFixture(t)
			w := f.watchRoster()
			p.run(f, w)
			ev := w.one(p.name)
			if len(ev.Teams) != 1 || ev.Teams[0].ID != uid(1) || ev.Teams[0].Lead.SessionID != "sid-1" || len(ev.Teams[0].Members) != 0 {
				t.Fatalf("roster = %+v, want the new team led by sid-1", ev.Teams)
			}
		})
	}
}

func (f *fixture) approveClick(id string) {
	f.t.Helper()
	if code, body := f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide", appApprove(nil)); code != http.StatusOK {
		f.t.Fatalf("approve %s: %d %s", id, code, body)
	}
}

// A spawned member joining, its kill, a member marked gone and a team's end
// each announce the roster once, from the write itself (not from the
// liveness tick, which these tests do not run).
func TestRoster_ChangedAfterKillSpawnAndTeamEnd(t *testing.T) {
	f, root := newTeamFixture(t, 3)
	f.rosterBaselineNow()
	w := f.watchRoster()

	m1 := f.member(1, root, "sid-m1", "w-one", nil)
	if ev := w.one("spawn"); len(ev.Teams) != 1 || len(ev.Teams[0].Members) != 1 || ev.Teams[0].Members[0].SessionID != "sid-m1" {
		t.Fatalf("after the spawn: %+v", ev.Teams)
	}

	if code, _, e := f.kill("/tmp/10.sock", m1.Ref); code != 200 {
		t.Fatalf("kill: %d %+v", code, e)
	}
	if ev := w.one("kill"); len(ev.Teams) != 1 || len(ev.Teams[0].Members) != 0 {
		t.Fatalf("after the kill: %+v", ev.Teams)
	}

	seedMember(t, f.m.store, "op-9", uid(1), "sid-m9", f.clock.Load())
	f.m.rosterChanged()
	if ev := w.one("a member row"); len(ev.Teams[0].Members) != 1 {
		t.Fatalf("after the seeded member: %+v", ev.Teams)
	}
	f.origins.markDead("sid-m9")
	f.m.markGoneMembers()
	if ev := w.one("gone"); len(ev.Teams[0].Members) != 0 {
		t.Fatalf("after the member went: %+v", ev.Teams)
	}

	f.origins.markDead("sid-1")
	f.m.endGoneTeams()
	if ev := w.one("team end"); len(ev.Teams) != 0 {
		t.Fatalf("after the team ended: %+v", ev.Teams)
	}
}

// rosterBaselineNow records the current roster as already sent.
func (f *fixture) rosterBaselineNow() { f.m.rosterBaseline() }

// Rule 3: the liveness tick builds the roster every time and sends it only
// when it differs from the last one sent: two ticks with nothing changed
// send nothing; a title change in the registry (which the module never sees
// written) sends one. Mutation gate: broadcast on every tick → red.
func TestRoster_TickBroadcastsOnlyWhenItChanged(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", 1000)
	f.m.rosterChanged() // the roster as the last sent
	f.m.rosterFlush()
	w := f.watchRoster()

	livenessTick(f)
	livenessTick(f)
	w.none("two ticks, nothing changed")

	o := fixtureOrigins["/tmp/10.sock"]
	o.Title = "renamed"
	f.origins.show(o)
	livenessTick(f)
	if ev := w.one("a title change"); ev.Teams[0].Lead.Title != "renamed" {
		t.Fatalf("lead = %+v, want the new title", ev.Teams[0].Lead)
	}
	livenessTick(f)
	w.none("the tick after")
}

// A lead's relay moves the team's lead to the new session (store:
// relay_store_report.go moveTeamRoles): the cleared report announces the
// roster, and the lead's session id in it is the new one. A lead the
// registry no longer lists shows the team row's ref, not the request-time
// address (which names the old ref).
func TestRoster_ClearedMovesTheLeadsSession(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1))
	out := f.begin("sid-1")
	if code, body := f.decide(out.RequestID, "approve"); code != 200 {
		t.Fatalf("approve relay: %d %s", code, body)
	}
	for _, st := range []team.RelayState{team.RelayWriting, team.RelayWritten} {
		if code, _, ae := f.report(out.Op.ID, team.RelayReportRequest{State: st}); code != 200 {
			t.Fatalf("report %s: %d %+v", st, code, ae)
		}
	}
	f.m.rosterChanged() // sync the last-sent roster, whatever the steps above sent
	f.m.rosterFlush()
	w := f.watchRoster()

	f.origins.markDead("sid-1")
	if code, _, ae := f.report(out.Op.ID, team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-1b"}); code != 200 {
		t.Fatalf("cleared: %d %+v", code, ae)
	}
	ev := w.one("cleared")
	newRef := f.op(out.Op.ID).NewRef
	if lead := ev.Teams[0].Lead; lead.SessionID != "sid-1b" || lead.Ref != newRef || !lead.Live {
		t.Fatalf("lead = %+v, want the live sid-1b / %s", lead, newRef)
	}

	f.origins.hide("sid-1b") // not listed any more: the stored values
	alias, _ := f.m.selfHost()
	lead := f.getRoster().Teams[0].Lead
	if lead.SessionID != "sid-1b" || lead.Ref != newRef || lead.Address != alias+"/"+newRef || lead.Live {
		t.Fatalf("stored lead = %+v, want sid-1b with address %s/%s", lead, alias, newRef)
	}
}
