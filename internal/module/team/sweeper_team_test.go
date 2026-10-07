package teammod

import (
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
