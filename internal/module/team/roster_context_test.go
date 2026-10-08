package teammod

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/team"
)

// ctxJSON is a reading as it travels on the wire ("null" for none).
func ctxJSON(t *testing.T, c *team.MemberContext) string {
	t.Helper()
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// persistedMember stores reading c on member spawnOp's row.
func (f *fixture) persistedMember(spawnOp, sid string, c team.MemberContext) {
	f.t.Helper()
	if ok, err := f.m.store.SetMemberUsage(spawnOp, sid, c); err != nil || !ok {
		f.t.Fatalf("persist member reading: %v %v", ok, err)
	}
}

// PL-1f′3: a roster member carries the model and effort it was spawned with
// and its context: the live reading wins over the persisted one, the
// persisted one is used when nothing is live, nil when neither. Mutation
// gate: the roster ignores the persisted fallback → red.
func TestRoster_MemberCarriesModelEffortAndContext(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", 1000)
	seedMember(t, f.m.store, "op-1", uid(1), "sid-m1", 2000) // live reading and a persisted one
	seedMember(t, f.m.store, "op-2", uid(1), "sid-m2", 2100) // persisted only
	seedMember(t, f.m.store, "op-3", uid(1), "sid-m3", 2200) // neither
	f.persistedMember("op-1", "sid-m1", team.MemberContext{UsedPercentage: pct(10), Window: 200000, ModelID: "old", At: 5})
	f.persistedMember("op-2", "sid-m2", team.MemberContext{UsedPercentage: pct(33), Window: 200000, ModelID: "claude-sonnet-5-5", Effort: "low", At: 6})
	f.usage.setReading("sid-m1", agent.ContextUsage{UsedPercentage: pct(41), WindowSize: 1000000, ModelID: "claude-opus-5-5", Effort: "high", At: 50})

	ms := f.getRoster().Teams[0].Members
	if len(ms) != 3 {
		t.Fatalf("members = %+v", ms)
	}
	live, persisted, none := ms[0], ms[1], ms[2]
	if live.Model != "sonnet" || live.Effort != "high" { // newMember's spawn values
		t.Errorf("model/effort = %q/%q, want the spawn values sonnet/high", live.Model, live.Effort)
	}
	if got, want := ctxJSON(t, live.Context), ctxJSON(t, &team.MemberContext{UsedPercentage: pct(41), Window: 1000000, ModelID: "claude-opus-5-5", Effort: "high", At: 50}); got != want {
		t.Errorf("live member context = %s, want the live reading %s", got, want)
	}
	if got, want := ctxJSON(t, persisted.Context), ctxJSON(t, &team.MemberContext{UsedPercentage: pct(33), Window: 200000, ModelID: "claude-sonnet-5-5", Effort: "low", At: 6}); got != want {
		t.Errorf("persisted member context = %s, want the stored reading %s", got, want)
	}
	if none.Context != nil {
		t.Errorf("member with no reading: context = %+v, want nil", none.Context)
	}
}

// PL-1f′3: the lead carries its context (live, else the one the sweeper
// persisted on the team row) and no model / effort (it was not spawned).
// Mutation gate: drop the lead's persisted fallback → red.
func TestRoster_LeadCarriesContext(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", 1000)
	f.origins.show(team.Origin{SessionID: "sid-1", Ref: "_abc123", Address: "self/boss", PID: 10})

	if l := f.getRoster().Teams[0].Lead; l.Context != nil || l.Model != "" || l.Effort != "" {
		t.Fatalf("lead without a reading = %+v, want no context / model / effort", l)
	}

	if ok, err := f.m.store.SetLeadUsage(uid(1), "sid-1", team.MemberContext{UsedPercentage: pct(12), Window: 1000000, ModelID: "claude-opus-5-5", Effort: "high", At: 60}); err != nil || !ok {
		t.Fatalf("persist lead reading: %v %v", ok, err)
	}
	persisted := ctxJSON(t, &team.MemberContext{UsedPercentage: pct(12), Window: 1000000, ModelID: "claude-opus-5-5", Effort: "high", At: 60})
	if l := f.getRoster().Teams[0].Lead; ctxJSON(t, l.Context) != persisted || l.Model != "" || l.Effort != "" {
		t.Fatalf("lead = %+v (context %s), want the persisted reading %s and no model / effort", l, ctxJSON(t, l.Context), persisted)
	}

	f.usage.setReading("sid-1", agent.ContextUsage{UsedPercentage: pct(77), WindowSize: 1000000, ModelID: "claude-opus-5-5", Effort: "max", At: 90})
	live := ctxJSON(t, &team.MemberContext{UsedPercentage: pct(77), Window: 1000000, ModelID: "claude-opus-5-5", Effort: "max", At: 90})
	if l := f.getRoster().Teams[0].Lead; ctxJSON(t, l.Context) != live || l.Model != "" || l.Effort != "" {
		t.Fatalf("lead context = %s, want the live reading %s and no model / effort", ctxJSON(t, l.Context), live)
	}
}

// PL-1f′3, the single-read-point guarantee: for one fixture the roster's
// member context is byte-for-byte what GET /api/team answers for the same
// member (live, persisted and none), so the App, iOS and pdx team agree.
// Mutation gate: the roster computes its own copy → red.
func TestRoster_ContextEqualsTeamGet(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", 1000)
	seedMember(t, f.m.store, "op-1", uid(1), "sid-m1", 2000)
	seedMember(t, f.m.store, "op-2", uid(1), "sid-m2", 2001)
	seedMember(t, f.m.store, "op-3", uid(1), "sid-m3", 2002)
	f.persistedMember("op-1", "sid-m1", team.MemberContext{UsedPercentage: pct(10), Window: 200000, At: 5})
	f.persistedMember("op-2", "sid-m2", team.MemberContext{UsedPercentage: pct(33), Window: 200000, ModelID: "m", Effort: "low", At: 6})
	f.usage.setReading("sid-m1", agent.ContextUsage{UsedPercentage: pct(41), WindowSize: 1000000, ModelID: "claude-opus-5-5", Effort: "high", At: 50})

	code, body := f.do(http.MethodGet, "/api/team?origin_inbox=/tmp/10.sock", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/team: %d %s", code, body)
	}
	var view team.TeamView
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for _, m := range view.Members {
		want[m.SessionID] = ctxJSON(t, m.Context)
	}
	ms := f.getRoster().Teams[0].Members
	if len(ms) != 3 || len(want) != 3 {
		t.Fatalf("roster members %d, team members %d, want 3 each", len(ms), len(want))
	}
	for _, m := range ms {
		if got := ctxJSON(t, m.Context); got != want[m.SessionID] {
			t.Errorf("%s: roster context %s, GET /api/team %s", m.SessionID, got, want[m.SessionID])
		}
	}
}

// PL-1f′3: a new live reading is an ordinary roster change. The liveness
// tick builds the roster, sees the context differ and sends one changed
// carrying it; the tick after, with nothing new, sends nothing. Mutation
// gate: leave the context out of what is compared → red.
func TestRoster_ContextChangeIsAnnouncedOnTheTick(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", 1000)
	seedMember(t, f.m.store, "op-1", uid(1), "sid-m1", 2000)
	f.m.rosterChanged() // the roster as the last sent
	f.m.rosterFlush()
	w := f.watchRoster()
	livenessTick(f)
	w.none("a tick with no reading")

	f.usage.setReading("sid-m1", agent.ContextUsage{UsedPercentage: pct(41), WindowSize: 200000, ModelID: "claude-sonnet-5-5", Effort: "low", At: 50})
	livenessTick(f)
	ev := w.one("a new live reading")
	if got := ev.Teams[0].Members[0].Context; got == nil || got.UsedPercentage == nil || *got.UsedPercentage != 41 {
		t.Fatalf("changed carries context %+v, want 41%%", got)
	}
	livenessTick(f)
	w.none("the tick after")

	f.usage.setReading("sid-m1", agent.ContextUsage{UsedPercentage: pct(42), WindowSize: 200000, ModelID: "claude-sonnet-5-5", Effort: "low", At: 60})
	livenessTick(f)
	if ev := w.one("a moved reading"); *ev.Teams[0].Members[0].Context.UsedPercentage != 42 {
		t.Fatalf("changed carries %+v, want 42%%", ev.Teams[0].Members[0].Context)
	}
}

// PL-1f′3 review A-1: after a relay's cleared the new session has no
// statusline yet, so its context is none, never the old session's stored
// reading. Checked on the roster (lead and member) and on GET /api/team
// (member). Mutation gates: drop the usage reset for the lead → the lead
// half red; for the member → the member half red.
func TestRoster_ClearedDoesNotCarryTheOldSessionsContext(t *testing.T) {
	old := team.MemberContext{UsedPercentage: pct(88), Window: 200000, ModelID: "claude-opus-5-5", Effort: "high", At: 5}

	t.Run("lead", func(t *testing.T) {
		f := newFixture(t)
		seedTeam(t, f.m.store, uid(1), "sid-1", 1000)
		if ok, err := f.m.store.SetLeadUsage(uid(1), "sid-1", old); err != nil || !ok {
			t.Fatalf("persist lead reading: %v %v", ok, err)
		}
		if l := f.getRoster().Teams[0].Lead; l.Context == nil {
			t.Fatal("precondition: the old lead's reading is not shown")
		}
		claimedOp(t, f.m.store, "relay-1", "sid-1", "_abc123")
		mustReport(t, f.m.store, "relay-1", RelayReport{State: team.RelayCleared, NewSessionID: "sid-1b", NewRef: "_lll222", At: 5000})
		if l := f.getRoster().Teams[0].Lead; l.SessionID != "sid-1b" || l.Context != nil {
			t.Fatalf("new lead = %s, context %+v, want sid-1b with no context", l.SessionID, l.Context)
		}
	})

	t.Run("member", func(t *testing.T) {
		f := newFixture(t)
		seedTeam(t, f.m.store, uid(1), "sid-1", 1000)
		seedMember(t, f.m.store, "op-1", uid(1), "sid-m1", 2000)
		f.persistedMember("op-1", "sid-m1", old)
		if m := f.getRoster().Teams[0].Members[0]; m.Context == nil {
			t.Fatal("precondition: the old member's reading is not shown")
		}
		claimedOp(t, f.m.store, "relay-1", "sid-m1", "_memop-1")
		mustReport(t, f.m.store, "relay-1", RelayReport{State: team.RelayCleared, NewSessionID: "sid-m2", NewRef: "_mmm222", At: 5000})

		if m := f.getRoster().Teams[0].Members[0]; m.SessionID != "sid-m2" || m.Context != nil {
			t.Fatalf("roster member = %s, context %+v, want sid-m2 with no context", m.SessionID, m.Context)
		}
		code, body := f.do(http.MethodGet, "/api/team?origin_inbox=/tmp/10.sock", nil)
		if code != http.StatusOK {
			t.Fatalf("GET /api/team: %d %s", code, body)
		}
		var view team.TeamView
		if err := json.Unmarshal(body, &view); err != nil {
			t.Fatal(err)
		}
		if len(view.Members) != 1 || view.Members[0].SessionID != "sid-m2" || view.Members[0].Context != nil {
			t.Fatalf("GET /api/team members = %+v, want sid-m2 with no context", view.Members)
		}
	})
}
