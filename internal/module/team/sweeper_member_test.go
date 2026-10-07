package teammod

import (
	"fmt"
	"testing"

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/team"
)

// setReading makes u the agent module's last statusline reading of sid.
func (f *fakeUsage) setReading(sid string, u agent.ContextUsage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.by == nil {
		f.by = map[string]agent.ContextUsage{}
	}
	f.by[sid] = u
}

func pct(v float64) *float64 { return &v }

// memberUsage reads one member row's persisted reading (tests only).
func memberUsage(t *testing.T, s *Store, spawnOp string) *team.MemberContext {
	t.Helper()
	rows, err := s.MembersOf(memberBySpawn(t, s, spawnOp).TeamID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.SpawnOp == spawnOp {
			return r.Usage
		}
	}
	t.Fatalf("no member %s", spawnOp)
	return nil
}

// Spec §8.5 "Persist it for teams only": on the liveness tick each active
// member's reading, and each live lead's, is copied onto its row when it is
// newer than the one stored; an older or equal one changes nothing.
func TestTick_PersistsTheNewerReadingOfMembersAndLeads(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", f.clock.Load())
	seedMember(t, f.m.store, "op-1", uid(1), "sid-m1", 1)
	f.usage.setReading("sid-m1", agent.ContextUsage{UsedPercentage: pct(41), WindowSize: 200000, ModelID: "claude-sonnet-5-5", Effort: "low", At: 50})
	f.usage.setReading("sid-1", agent.ContextUsage{UsedPercentage: pct(12), WindowSize: 1000000, ModelID: "claude-opus-5-5", Effort: "high", At: 60})
	livenessTick(f)
	want := team.MemberContext{UsedPercentage: pct(41), Window: 200000, ModelID: "claude-sonnet-5-5", Effort: "low", At: 50}
	if got := memberUsage(t, f.m.store, "op-1"); got == nil || *got.UsedPercentage != 41 || got.Window != want.Window ||
		got.ModelID != want.ModelID || got.Effort != want.Effort || got.At != 50 {
		t.Fatalf("member reading = %+v, want %+v", got, want)
	}
	var leadPct float64
	var leadModel string
	var leadAt int64
	if err := f.m.store.db.QueryRow(`SELECT lead_usage_pct, lead_usage_model, lead_usage_at FROM teams WHERE id = ?`, uid(1)).
		Scan(&leadPct, &leadModel, &leadAt); err != nil || leadPct != 12 || leadModel != "claude-opus-5-5" || leadAt != 60 {
		t.Fatalf("lead reading = %v %q %d (%v)", leadPct, leadModel, leadAt, err)
	}

	f.usage.setReading("sid-m1", agent.ContextUsage{UsedPercentage: pct(99), At: 50}) // not newer
	f.usage.setReading("sid-1", agent.ContextUsage{UsedPercentage: pct(98), At: 59})  // older
	livenessTick(f)
	if got := memberUsage(t, f.m.store, "op-1"); *got.UsedPercentage != 41 {
		t.Fatalf("a reading no newer than the stored one replaced it: %+v", got)
	}
	if err := f.m.store.db.QueryRow(`SELECT lead_usage_pct FROM teams WHERE id = ?`, uid(1)).Scan(&leadPct); err != nil || leadPct != 12 {
		t.Fatalf("an older lead reading replaced the stored one: %v (%v)", leadPct, err)
	}
	f.usage.setReading("sid-m1", agent.ContextUsage{At: 70}) // newer, no percentage yet
	livenessTick(f)
	if got := memberUsage(t, f.m.store, "op-1"); got.UsedPercentage != nil || got.At != 70 {
		t.Fatalf("the newer reading = %+v, want a nil percentage at 70", got)
	}
}

// A member is marked gone only when that is confirmed (P4-6, P4-2b's rule):
// its own process — the pid and start time its row recorded — is dead,
// reused or in another conversation (LeadPresence gone), and no relay op of
// its session is claimed, writing or written. One the registry cannot place
// stays active, as does a live one; a gone one mid-relay stays until its op
// ends.
func TestTick_MarksAGoneMemberButNotOneMidRelay(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", f.clock.Load())
	cases := []struct {
		sid      string
		relay    team.RelayState // "" = no op
		presence string          // "gone" | "unknown" | "live"
		gone     bool
	}{
		{"m-gone", "", "gone", true},
		{"m-unknown", "", "unknown", false},
		{"m-live", "", "live", false},
		{"m-claimed", team.RelayClaimed, "gone", false},
		{"m-writing", team.RelayWriting, "gone", false},
		{"m-written", team.RelayWritten, "gone", false},
		{"m-awaiting", team.RelayAwaitingApproval, "gone", true},
		{"m-failed", team.RelayFailed, "gone", true},
	}
	f.origins.mu.Lock()
	f.origins.unknown = map[string]bool{}
	f.origins.mu.Unlock()
	for i, tc := range cases {
		row := newMember(fmt.Sprintf("op-%d", i), uid(1), tc.sid, fmt.Sprintf("_mem%03d", i), 1)
		row.PID, row.ProcStart = 100+i, "Sun Sep 13 15:22:36 2026"
		if err := f.m.store.InsertMember(row); err != nil {
			t.Fatal(err)
		}
		if tc.relay != "" {
			op := selfOp(fmt.Sprintf("relay-%d", i), tc.sid, row.Ref, 1)
			op.State = tc.relay
			if err := f.m.store.CreateRelayOp(op); err != nil {
				t.Fatal(err)
			}
		}
		switch tc.presence {
		case "gone":
			f.origins.markDead(tc.sid)
		case "unknown":
			f.origins.mu.Lock()
			f.origins.unknown[tc.sid] = true
			f.origins.mu.Unlock()
		}
	}
	livenessTick(f)
	for i, tc := range cases {
		got := memberBySpawn(t, f.m.store, fmt.Sprintf("op-%d", i))
		if (got.State == team.MemberGone) != tc.gone || (!tc.gone && got.State != team.MemberActive) {
			t.Errorf("%s (presence %s, relay %q): state %s, want gone=%v", tc.sid, tc.presence, tc.relay, got.State, tc.gone)
		}
	}
	f.origins.mu.Lock()
	asked := f.origins.leadAsked["m-gone"]
	f.origins.mu.Unlock()
	if asked != "100 Sun Sep 13 15:22:36 2026" {
		t.Fatalf("presence asked about m-gone with %q, want its row's process", asked)
	}

	// The written relay fails: its member is marked gone on the next tick.
	mustReport(t, f.m.store, "relay-5", RelayReport{State: team.RelayFailed, Reason: "handoff_incomplete", At: 2})
	livenessTick(f)
	if got := memberBySpawn(t, f.m.store, "op-5"); got.State != team.MemberGone {
		t.Fatalf("m-written after its relay failed = %s, want gone", got.State)
	}
	if got := memberBySpawn(t, f.m.store, "op-3"); got.State != team.MemberActive {
		t.Fatalf("m-claimed = %s, want still active", got.State)
	}
}

// The relay guard is in the statement that marks the member gone: a relay
// claimed after the presence read keeps the member. Within BootGraceS of
// Start nothing is marked (the registry may not list a member yet).
func TestTick_AMemberRelayClaimedLateOrTheBootGraceKeepsTheMember(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", f.clock.Load())
	seedMember(t, f.m.store, "op-1", uid(1), "sid-m1", 1)
	if err := f.m.store.CreateRelayOp(selfOp("relay-1", "sid-m1", "_memop-1", 1)); err != nil { // awaiting approval
		t.Fatal(err)
	}
	f.origins.markDead("sid-m1")
	f.m.bootAt = f.clock.Load()
	livenessTick(f)
	if got := memberBySpawn(t, f.m.store, "op-1"); got.State != team.MemberActive {
		t.Fatalf("inside the boot grace = %s, want active", got.State)
	}
	f.clock.Add(team.BootGraceS * 1000)
	seen := 0
	f.m.beforeMarkGone = func(memberRow) {
		seen++
		mustReport(t, f.m.store, "relay-1", RelayReport{State: team.RelayClaimed, At: 2})
	}
	livenessTick(f)
	if got := memberBySpawn(t, f.m.store, "op-1"); seen != 1 || got.State != team.MemberActive {
		t.Fatalf("seam ran %d times; member = %s, want active (claimed before the mark)", seen, got.State)
	}
}

// The P4-5 deployment premise, end to end: the spawn limit counts team.db
// alone, so a member that left holds its place until the sweeper marks it
// gone; then the next spawn is accepted.
func TestSpawn_AMemberThatLeftFreesItsPlaceOnceMarkedGone(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	f.register("%0", "sid-m1")
	if code, op, e := f.spawn(1, root, nil); code != 200 || op.State != team.SpawnDone {
		t.Fatalf("first spawn = %d %+v %+v", code, op, e)
	}
	f.register("%1", "sid-m2")
	if code, _, e := f.spawn(2, root, nil); code != 409 || e.Error != team.ErrTeamFull {
		t.Fatalf("a full team = %d %+v, want 409 team_full", code, e)
	}
	f.origins.markDead("sid-m1") // the member's Claude Code exited
	if code, _, e := f.spawn(2, root, nil); code != 409 || e.Error != team.ErrTeamFull {
		t.Fatalf("before the sweeper = %d %+v, want 409 team_full", code, e)
	}
	livenessTick(f)
	if got := memberBySpawn(t, f.m.store, spawnID(1)); got.State != team.MemberGone {
		t.Fatalf("the member that left = %s, want gone", got.State)
	}
	if code, op, e := f.spawn(2, root, nil); code != 200 || op.State != team.SpawnDone || op.Member.SessionID != "sid-m2" {
		t.Fatalf("after the sweeper = %d %+v %+v, want the second member", code, op, e)
	}
}
