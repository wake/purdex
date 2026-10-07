package teammod

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// livenessTick runs the sweeper through its next liveness tick (every
// livenessEvery-th), the only one that looks at teams.
func livenessTick(f *fixture) {
	for {
		f.m.tick()
		if f.m.tickN%livenessEvery == 0 {
			return
		}
	}
}

// Spec §7.1: a team ends when its lead's conversation ends. The sweeper
// sees that on the liveness tick — not before — ends the team once with
// lead_gone and logs one line. A live lead's team is kept, and so is an
// open request of another live session.
func TestTick_EndsTheTeamOfAGoneLead(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	if code, body := f.do(http.MethodPost, "/api/team/approvals/"+uid(1)+"/decide", appApprove(nil)); code != 200 {
		t.Fatalf("approve: %d %s", code, body)
	}
	open := f.createReq(uid(2)) // an open request, so this tick is not the empty-sweep one
	open.OriginInbox = "/tmp/20.sock"
	if code, body := f.do(http.MethodPost, "/api/team/approvals", open); code != 201 {
		t.Fatalf("open request: %d %s", code, body)
	}
	seedTeam(t, f.m.store, uid(7), "sid-2", f.clock.Load()) // and a live lead's team

	ends := 0 // log lines saying uid(1)'s team ended
	f.m.logf = func(format string, args ...any) {
		if strings.Contains(fmt.Sprintf(format, args...), "team "+uid(1)+" ended") {
			ends++
		}
	}
	f.origins.markDead("sid-1")
	f.clock.Add(7)

	for i := 1; i < livenessEvery; i++ {
		f.m.tick()
		if _, ok, _ := f.m.store.LiveTeamByLead("sid-1"); !ok {
			t.Fatalf("tick %d: the team ended before the liveness tick", i)
		}
	}
	f.m.tick()
	got, ok := getTeam(t, f.m.store, uid(1))
	if !ok || got.EndedAt != 1_000_007 || got.EndReason != team.TeamEndLeadGone {
		t.Fatalf("team after the liveness tick = %+v, want ended lead_gone at 1000007", got)
	}
	// The lead's process is the one its request recorded (pid 10 and its start).
	f.origins.mu.Lock()
	asked := f.origins.leadAsked["sid-1"]
	f.origins.mu.Unlock()
	if asked != "10 Sun Sep 13 15:22:36 2026" {
		t.Fatalf("LeadPresence asked about %q, want the request's origin process", asked)
	}
	if _, ok, _ := f.m.store.LiveTeamByLead("sid-2"); !ok {
		t.Fatal("the live lead's team was ended")
	}
	if a, _, _ := f.m.store.Get(uid(2)); a.State != team.StateOpen {
		t.Fatalf("the live session's open request = %s, want open", a.State)
	}

	f.clock.Add(1)
	livenessTick(f)
	if got, _ := getTeam(t, f.m.store, uid(1)); got.EndedAt != 1_000_007 {
		t.Fatalf("a later tick re-ended the team: %+v", got)
	}
	if ends != 1 {
		t.Fatalf("end log lines over two liveness ticks = %d, want exactly 1", ends)
	}
}

// The relay guard (spec §7.1: the lead's SessionEnd ends the team "with any
// reason other than a relay's /clear"): the old session leaves the registry
// about 0.6 s after the relay's /clear while its op is still written, so a
// gone lead whose op is claimed, writing or written keeps its team. Any
// other op state — awaiting approval, terminal — or no op at all does not.
// Once the op ends, the next liveness tick ends the team.
func TestTick_KeepsTheTeamWhileItsLeadIsRelaying(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		sid   string
		state team.RelayState // "" = no op
		kept  bool
	}{
		{"lead-claimed", team.RelayClaimed, true},
		{"lead-writing", team.RelayWriting, true},
		{"lead-written", team.RelayWritten, true},
		{"lead-awaiting", team.RelayAwaitingApproval, false},
		{"lead-failed", team.RelayFailed, false},
		{"lead-none", "", false},
	}
	for i, tc := range cases {
		seedTeam(t, f.m.store, uid(100+i), tc.sid, f.clock.Load())
		if tc.state != "" {
			op := selfOp(fmt.Sprintf("op-%d", i), tc.sid, "_abc123", f.clock.Load())
			op.State = tc.state
			if err := f.m.store.CreateRelayOp(op); err != nil {
				t.Fatal(err)
			}
		}
		f.origins.markDead(tc.sid)
	}
	livenessTick(f)
	for i, tc := range cases {
		got, _ := getTeam(t, f.m.store, uid(100+i))
		if live := got.EndedAt == 0; live != tc.kept {
			t.Errorf("%s (op %q): team live=%v, want %v", tc.sid, tc.state, live, tc.kept)
		}
	}

	// The written op ends (the relay failed): its team ends on the next liveness tick.
	mustReport(t, f.m.store, "op-2", RelayReport{State: team.RelayFailed, Reason: "handoff_incomplete", At: f.clock.Load()})
	f.clock.Add(1)
	livenessTick(f)
	if got, _ := getTeam(t, f.m.store, uid(102)); got.EndedAt != f.clock.Load() || got.EndReason != team.TeamEndLeadGone {
		t.Fatalf("written lead's team after its op failed = %+v, want ended", got)
	}
	if got, _ := getTeam(t, f.m.store, uid(100)); got.EndedAt != 0 {
		t.Fatalf("claimed lead's team = %+v, want still live", got)
	}
}

// A lead's own relay keeps its team (spec §7.1, §8.4; P4-3 with P4-2b's
// LeadPresence). After the relay's /clear the old session id leaves the
// registry; the cleared report moves lead_session_id to the new one, so
// the sweeper asks LeadPresence about the NEW session with the same process
// (the lead request's pid and start time — /clear keeps the process), which
// the registry lists: the team stays, through cleared and after done.
// Without the move the sweeper would ask about the old session, gone, and
// — the op past written — end the team.
func TestTick_ALeadRelayKeepsItsTeam(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	if code, body := f.do(http.MethodPost, "/api/team/approvals/"+uid(1)+"/decide", appApprove(nil)); code != 200 {
		t.Fatalf("approve lead: %d %s", code, body)
	}
	out := f.begin("sid-1") // the lead self-relays (relay.self_lead on)
	if code, body := f.decide(out.RequestID, "approve"); code != 200 {
		t.Fatalf("approve relay: %d %s", code, body)
	}
	report := func(req team.RelayReportRequest) {
		t.Helper()
		if code, body := f.do(http.MethodPost, "/api/relay/ops/"+out.Op.ID+"/report", req); code != 200 {
			t.Fatalf("report %s: %d %s", req.State, code, body)
		}
	}
	report(team.RelayReportRequest{State: team.RelayWriting})
	report(team.RelayReportRequest{State: team.RelayWritten})
	f.origins.markDead("sid-1") // /clear: the old session id leaves the registry
	report(team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-1b"})

	livenessTick(f)
	got, ok, err := f.m.store.LiveTeamByLead("sid-1b")
	if err != nil || !ok || got.ID != uid(1) || got.LeadRef != f.op(out.Op.ID).NewRef {
		t.Fatalf("team after the lead's cleared = %+v ok=%v err=%v, want %s led by sid-1b", got, ok, err, uid(1))
	}
	f.origins.mu.Lock()
	asked := f.origins.leadAsked["sid-1b"]
	f.origins.mu.Unlock()
	if asked != "10 Sun Sep 13 15:22:36 2026" {
		t.Fatalf("LeadPresence asked about sid-1b with %q, want the lead request's process", asked)
	}

	report(team.RelayReportRequest{State: team.RelayDone})
	f.clock.Add(1)
	livenessTick(f)
	if got, _ := getTeam(t, f.m.store, uid(1)); got.EndedAt != 0 || got.LeadSessionID != "sid-1b" {
		t.Fatalf("team after done = %+v, want live under sid-1b", got)
	}
}

// The relay guard and the end are one statement (P4-2 review): a relay
// claimed after the sweeper decided the lead is gone — here in that window,
// through the beforeEndTeam seam — still keeps the team. Once the op ends,
// the next liveness tick ends it.
func TestTick_ARelayClaimedAfterTheLivenessReadKeepsTheTeam(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", f.clock.Load())
	if err := f.m.store.CreateRelayOp(selfOp("op-1", "sid-1", "_abc123", f.clock.Load())); err != nil { // awaiting approval
		t.Fatal(err)
	}
	f.origins.markDead("sid-1")
	seen := 0
	f.m.beforeEndTeam = func(team.Team) {
		seen++
		mustReport(t, f.m.store, "op-1", RelayReport{State: team.RelayClaimed, At: f.clock.Load()})
	}
	livenessTick(f)
	if got, _ := getTeam(t, f.m.store, uid(1)); seen != 1 || got.EndedAt != 0 {
		t.Fatalf("seam ran %d times; team = %+v, want kept (claimed before the end)", seen, got)
	}
	f.m.beforeEndTeam = nil
	mustReport(t, f.m.store, "op-1", RelayReport{State: team.RelayFailed, Reason: "handoff_incomplete", At: f.clock.Load()})
	livenessTick(f)
	if got, _ := getTeam(t, f.m.store, uid(1)); got.EndedAt == 0 || got.EndReason != team.TeamEndLeadGone {
		t.Fatalf("team after its lead's op failed = %+v, want ended", got)
	}
}

// revive undoes markDead: the session is listed in the registry again.
func (f *fakeOrigins) revive(sid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.dead, sid)
}

// After a restart (P4-2 review, spec §9.2) team liveness gets the boot
// grace open requests get: the registry may not list a lead yet when the
// first liveness tick runs. A lead back within the grace keeps its team;
// one still absent after it ends.
func TestTick_TeamsSurviveARestartUntilTheBootGraceEnds(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", f.clock.Load())
	seedTeam(t, f.m.store, uid(2), "sid-2", f.clock.Load())
	f.origins.markDead("sid-1")
	f.origins.markDead("sid-2")        // the registry lists neither lead at boot
	g := f.reboot(f.titles)            // the same team.db; boot at 1_000_000
	_ = g.m.Stop(context.Background()) // join the real sweeper: this test drives the ticks
	for _, at := range []int64{10_000, 20_000, team.BootGraceS*1000 - 1} {
		g.clock.Store(1_000_000 + at)
		livenessTick(g)
		for _, id := range []string{uid(1), uid(2)} {
			if got, _ := getTeam(t, g.m.store, id); got.EndedAt != 0 {
				t.Fatalf("boot+%d ms: team %s ended inside the grace", at, id)
			}
		}
	}
	f.origins.revive("sid-1") // its lead is listed again within the grace
	g.clock.Store(1_000_000 + team.BootGraceS*1000)
	livenessTick(g)
	if got, _ := getTeam(t, g.m.store, uid(1)); got.EndedAt != 0 {
		t.Fatalf("the returned lead's team = %+v, want live", got)
	}
	if got, _ := getTeam(t, g.m.store, uid(2)); got.EndedAt != 1_030_000 || got.EndReason != team.TeamEndLeadGone {
		t.Fatalf("the absent lead's team after the grace = %+v, want ended at 1030000", got)
	}
}

// Only a lead confirmed gone ends its team (P4-2 review): one the registry
// cannot place — its pid alive but its own file truncated, or the registry
// missing or empty (TestOriginResolver_LeadPresence), which LiveSession
// reads as not live — keeps the team until it is confirmed.
func TestTick_ALeadTheRegistryCannotPlaceKeepsItsTeam(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", f.clock.Load())
	f.origins.mu.Lock()
	f.origins.unknown = map[string]bool{"sid-1": true}
	f.origins.mu.Unlock()
	livenessTick(f)
	if got, _ := getTeam(t, f.m.store, uid(1)); got.EndedAt != 0 {
		t.Fatalf("team of an unknown lead = %+v, want live", got)
	}
	f.origins.mu.Lock()
	f.origins.unknown = nil
	f.origins.mu.Unlock()
	f.origins.markDead("sid-1")
	livenessTick(f)
	if got, _ := getTeam(t, f.m.store, uid(1)); got.EndedAt == 0 {
		t.Fatalf("team of a confirmed-gone lead = %+v, want ended", got)
	}
}

// The team check runs on the liveness tick whether or not any approval is
// open: the sweep's early return on an empty open set must not skip it.
func TestTick_EndsTeamsEvenWithNoOpenApproval(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", f.clock.Load())
	if open, _ := f.m.store.ListOpen(); len(open) != 0 {
		t.Fatalf("open approvals = %d, want 0", len(open))
	}
	f.origins.markDead("sid-1")
	livenessTick(f)
	if got, _ := getTeam(t, f.m.store, uid(1)); got.EndedAt == 0 || got.EndReason != team.TeamEndLeadGone {
		t.Fatalf("team with no open approval = %+v, want ended", got)
	}
}
