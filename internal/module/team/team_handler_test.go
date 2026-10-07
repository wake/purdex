package teammod

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/module/agent"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
	"github.com/wake/purdex/internal/tmux"
)

// newTeamFixture is newSpawnFixture with this host's alias set to "self"
// (the registry fakes answer "mlab/…" addresses, so the two differ).
func newTeamFixture(t *testing.T, maxMembers int) (*fixture, string) {
	f, root := newSpawnFixture(t, maxMembers)
	f.core.CfgMu.Lock()
	f.core.Cfg.Peers.Alias = "self"
	f.core.CfgMu.Unlock()
	return f, root
}

// member spawns op i whose Claude Code comes up as sid named name, and
// returns the stored row (its tmux identity as P4-5 recorded it).
func (f *fixture) member(i int, root, sid, name string, edit func(*team.SpawnRequest)) memberRow {
	f.t.Helper()
	f.register(fmt.Sprintf("%%%d", f.sessions.count()), sid)
	f.so.mu.Lock()
	o := f.so.members[sid]
	o.Name = name
	f.so.members[sid] = o
	f.so.mu.Unlock()
	if code, op, e := f.spawn(i, root, edit); code != 200 || op.State != team.SpawnDone {
		f.t.Fatalf("spawn %d: %d %+v %+v", i, code, op, e)
	}
	return memberBySpawn(f.t, f.m.store, spawnID(i))
}

func (f *fixture) teamView(inbox string) (int, team.TeamView, team.APIError) {
	f.t.Helper()
	code, body := f.do(http.MethodGet, "/api/team?origin_inbox="+url.QueryEscape(inbox), "")
	var v team.TeamView
	if code != http.StatusOK {
		return code, v, decodeErr(f.t, body)
	}
	if err := json.Unmarshal(body, &v); err != nil {
		f.t.Fatalf("decode team view: %v; %s", err, body)
	}
	return code, v, team.APIError{}
}

func (f *fixture) kill(inbox, target string) (int, team.Member, team.APIError) {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/team/kill", team.KillRequest{OriginInbox: inbox, Target: target})
	var mem team.Member
	if code != http.StatusOK {
		return code, mem, decodeErr(f.t, body)
	}
	if err := json.Unmarshal(body, &mem); err != nil {
		f.t.Fatalf("decode member: %v; %s", err, body)
	}
	return code, mem, team.APIError{}
}

// Spec §7.3, U20 (e): the lead's team, every member in any state, oldest
// first, each with the model and effort asked at spawn and the reading of
// what it runs (live; absent before its first statusline). A live member's
// address is the registry's; another's is <self alias>/<ref>.
func TestTeam_ListsMembersWithModelEffortAndContext(t *testing.T) {
	f, root := newTeamFixture(t, 3)
	m1 := f.member(1, root, "sid-m1", "w-one", func(r *team.SpawnRequest) { r.Model, r.Effort, r.Title = "opus[1m]", "high", "one" })
	m2 := f.member(2, root, "sid-m2", "w-two", nil)
	m3 := f.member(3, root, "sid-m3", "w-three", nil)
	if err := f.m.store.SetMemberState(m3.SpawnOp, team.MemberKilled, 9); err != nil {
		t.Fatal(err)
	}
	f.usage.setReading("sid-m1", agent.ContextUsage{UsedPercentage: pct(30), WindowSize: 1000000, ModelID: "claude-opus-5-5[1m]", Effort: "high", At: 77})

	code, v, e := f.teamView("/tmp/10.sock")
	if code != 200 || v.Team.ID != uid(1) || len(v.Members) != 3 {
		t.Fatalf("team = %d %+v %+v", code, v, e)
	}
	got1, got2, got3 := v.Members[0], v.Members[1], v.Members[2]
	if got1.SessionID != "sid-m1" || got1.Model != "opus[1m]" || got1.Effort != "high" || got1.Title != "one" ||
		got1.Address != "mlab/"+m1.Ref || got1.TmuxSession != m1.TmuxSession || got1.State != team.MemberActive {
		t.Fatalf("member 1 = %+v", got1)
	}
	if c := got1.Context; c == nil || *c.UsedPercentage != 30 || c.Window != 1000000 || c.ModelID != "claude-opus-5-5[1m]" || c.Effort != "high" || c.At != 77 {
		t.Fatalf("member 1 context = %+v", c)
	}
	if got2.SessionID != "sid-m2" || got2.Model != "" || got2.Effort != "" || got2.Context != nil {
		t.Fatalf("member 2 (no model asked, no statusline yet) = %+v", got2)
	}
	if got3.State != team.MemberKilled || got3.Address != "self/"+m3.Ref || m2.Ref == "" {
		t.Fatalf("member 3 (killed) = %+v", got3)
	}
}

// Only a live team's lead sees its team; the registry being unreadable is a
// retry.
func TestTeam_NotLeadIs409(t *testing.T) {
	f, _ := newTeamFixture(t, 1)
	for _, c := range []struct {
		inbox  string
		status int
		code   string
	}{{"/tmp/20.sock", 409, team.ErrNotLead}, {"/tmp/99.sock", 400, team.ErrOriginUnknown}} {
		if code, _, e := f.teamView(c.inbox); code != c.status || e.Error != c.code {
			t.Fatalf("%s: %d %+v, want %d %s", c.inbox, code, e, c.status, c.code)
		}
	}
	f.origins.mu.Lock()
	f.origins.readErr = true
	f.origins.mu.Unlock()
	if code, _, e := f.teamView("/tmp/10.sock"); code != 503 || e.Error != team.ErrNotReady {
		t.Fatalf("registry unreadable: %d %+v, want 503 not_ready", code, e)
	}
}

// Spec §8.5: the reading persisted by the sweeper is served after a restart,
// while the agent module has none yet. Mutation gate: serve only live
// readings → red.
func TestTeam_ServesThePersistedReadingAfterARestart(t *testing.T) {
	f, root := newTeamFixture(t, 1)
	f.member(1, root, "sid-m1", "w-one", nil)
	f.usage.setReading("sid-m1", agent.ContextUsage{UsedPercentage: pct(41), WindowSize: 200000, ModelID: "claude-sonnet-5-5", Effort: "low", At: 50})
	livenessTick(f)
	f.usage.mu.Lock()
	f.usage.by = nil // the new process has heard no statusline yet
	f.usage.mu.Unlock()
	g := f.reboot(f.titles)
	code, v, e := g.teamView("/tmp/10.sock")
	if code != 200 || len(v.Members) != 1 {
		t.Fatalf("team after the restart = %d %+v %+v", code, v, e)
	}
	if c := v.Members[0].Context; c == nil || *c.UsedPercentage != 41 || c.ModelID != "claude-sonnet-5-5" || c.Effort != "low" || c.At != 50 {
		t.Fatalf("context after the restart = %+v, want the persisted reading", c)
	}
}

// Spec §7.3: only the member's own lead may kill it. A session leading no
// team is not_lead; another team's member is not_your_member and is left
// alone. The lead's own member: its recorded session is killed by id under
// its generation, and the row says killed. Mutation gate: drop the team_id
// filter → the cross-team kill goes through, red.
func TestKill_OnlyTheLeadsOwnMember(t *testing.T) {
	f, root := newTeamFixture(t, 2)
	m1 := f.member(1, root, "sid-m1", "w-one", nil)
	if code, _, e := f.kill("/tmp/20.sock", m1.Ref); code != 409 || e.Error != team.ErrNotLead {
		t.Fatalf("a non-lead's kill = %d %+v, want 409 not_lead", code, e)
	}
	seedTeam(t, f.m.store, uid(7), "sid-2", 1)
	other := newMember("op-9", uid(7), "sid-m9", ipeers.RefID("sid-m9"), 1)
	if err := f.m.store.InsertMember(other); err != nil {
		t.Fatal(err)
	}
	if code, _, e := f.kill("/tmp/10.sock", other.Ref); code != 409 || e.Error != team.ErrNotYourMember {
		t.Fatalf("another team's member = %d %+v, want 409 not_your_member", code, e)
	}
	if got := memberBySpawn(t, f.m.store, "op-9"); got.State != team.MemberActive {
		t.Fatalf("another team's member = %s, want untouched", got.State)
	}
	code, mem, e := f.kill("/tmp/10.sock", m1.Ref)
	if code != 200 || mem.State != team.MemberKilled || mem.SessionID != "sid-m1" {
		t.Fatalf("the lead's kill = %d %+v %+v", code, mem, e)
	}
	if calls := f.tmux.KillIfInstanceCalls(); len(calls) != 1 || calls[0] != (tmux.KillIfInstanceCall{SessionID: m1.TmuxID, Expected: m1.TmuxInstance}) {
		t.Fatalf("kill calls = %+v, want one for %s under %s", calls, m1.TmuxID, m1.TmuxInstance)
	}
	if f.tmux.HasSession(m1.TmuxSession) || memberBySpawn(t, f.m.store, m1.SpawnOp).State != team.MemberKilled {
		t.Fatal("the member's session is still there, or its row is not killed")
	}
}

// Spec §8.4 (U3): after a relay the member's old ref still reaches it
// (lineage), as does the new one. Mutation gate: drop the lineage tier → red.
func TestKill_ByTheOldRefAfterARelay(t *testing.T) {
	f, root := newTeamFixture(t, 1)
	m1 := f.member(1, root, "sid-m1", "w-one", nil)
	claimedOp(t, f.m.store, "relay-1", "sid-m1", m1.Ref)
	mustReport(t, f.m.store, "relay-1", RelayReport{State: team.RelayCleared, NewSessionID: "sid-m1b", NewRef: ipeers.RefID("sid-m1b"), At: 5})
	code, mem, e := f.kill("/tmp/10.sock", strings.TrimPrefix(m1.Ref, "_"))
	if code != 200 || mem.State != team.MemberKilled || mem.SessionID != "sid-m1b" || mem.Ref != ipeers.RefID("sid-m1b") {
		t.Fatalf("kill by the old ref = %d %+v %+v", code, mem, e)
	}
	if len(f.tmux.KillIfInstanceCalls()) != 1 {
		t.Fatal("the relayed member's session was not killed")
	}
}

// The kill touches only the session its spawn created (P4-5: id, generation
// and the @pdx_spawn_op tag): a second kill answers 200 and kills nothing; a
// session that lost the tag is refused and left running; on a restarted
// server (another generation) nothing is killed — the member died with the
// old server — and the row says killed. An active member whose session
// cannot be read is a retry, until the sweeper confirms it gone.
func TestKill_IsIdempotentAndGenerationGuarded(t *testing.T) {
	f, root := newTeamFixture(t, 4)
	m1 := f.member(1, root, "sid-m1", "w-one", nil)
	m2 := f.member(2, root, "sid-m2", "w-two", nil)
	m3 := f.member(3, root, "sid-m3", "w-three", nil)
	m4 := f.member(4, root, "sid-m4", "w-four", nil)
	for i := 0; i < 2; i++ {
		if code, mem, e := f.kill("/tmp/10.sock", m1.Ref); code != 200 || mem.State != team.MemberKilled {
			t.Fatalf("kill %d of m1 = %d %+v %+v", i+1, code, mem, e)
		}
	}
	if n := len(f.tmux.KillIfInstanceCalls()); n != 1 {
		t.Fatalf("kill calls after two kills = %d, want 1", n)
	}

	f.tmux.SetSessionTag(m2.TmuxSession, spawnTagOption, spawnID(9)) // no longer this member's tag
	if code, _, e := f.kill("/tmp/10.sock", m2.Ref); code != 409 || e.Error != team.ErrNotYourMember {
		t.Fatalf("an untagged session = %d %+v, want 409 not_your_member", code, e)
	}
	if !f.tmux.HasSession(m2.TmuxSession) || memberBySpawn(t, f.m.store, m2.SpawnOp).State != team.MemberActive {
		t.Fatal("a session without the member's tag was touched")
	}

	_ = f.tmux.KillSession(m4.TmuxSession) // gone outside the daemon; the sweeper has not looked yet
	if code, _, e := f.kill("/tmp/10.sock", m4.Ref); code != 503 || e.Error != team.ErrNotReady {
		t.Fatalf("an active member whose session cannot be read = %d %+v, want 503 not_ready", code, e)
	}
	if err := f.m.store.SetMemberState(m4.SpawnOp, team.MemberGone, 9); err != nil {
		t.Fatal(err)
	}
	if code, mem, e := f.kill("/tmp/10.sock", m4.Ref); code != 200 || mem.State != team.MemberKilled {
		t.Fatalf("a gone member's kill = %d %+v %+v, want 200 killed", code, mem, e)
	}
	bare := newMember("op-5", uid(1), "sid-m5", ipeers.RefID("sid-m5"), 1) // no tmux session recorded
	if err := f.m.store.InsertMember(bare); err != nil {
		t.Fatal(err)
	}
	if code, _, e := f.kill("/tmp/10.sock", bare.Ref); code != 503 || e.Error != team.ErrNotReady {
		t.Fatalf("an active member with no recorded session = %d %+v, want 503", code, e)
	}

	f.tmux.SetInstance("5151:1800000000") // the tmux server restarted; $N now names a stranger
	if code, mem, e := f.kill("/tmp/10.sock", m3.Ref); code != 200 || mem.State != team.MemberKilled {
		t.Fatalf("kill after a server restart = %d %+v %+v", code, mem, e)
	}
	if n := len(f.tmux.KillIfInstanceCalls()); n != 1 || !f.tmux.HasSession(m3.TmuxSession) {
		t.Fatalf("a stranger's session was killed (kill calls %d)", n)
	}
}

// The targets pdx kill takes (plan v3 P4-6): a ref with or without its
// underscore, bare or behind this host's alias or id; <host>/<name> by the
// live registry name; <host>/<name> [<ref>] when both name the member.
// Anything else — another host, a bare name, a name or ref of no member —
// is not_your_member.
func TestKill_TargetForms(t *testing.T) {
	cases := []struct {
		target func(ref string) string
		ok     bool
	}{
		{func(r string) string { return r }, true},
		{func(r string) string { return r[1:] }, true},
		{func(r string) string { return "self/" + r }, true},
		{func(r string) string { return "h:1/" + r[1:] }, true},
		{func(string) string { return "self/w-one" }, true},
		{func(r string) string { return "self/w-one [" + r[1:] + "]" }, true},
		{func(r string) string { return "air26/" + r }, false},
		{func(string) string { return "w-one" }, false},
		{func(string) string { return "self/w-two" }, false},
		{func(r string) string { return "self/w-two [" + r[1:] + "]" }, false},
		{func(string) string { return "self/_zzzzzz" }, false},
	}
	for i, c := range cases {
		f, root := newTeamFixture(t, 1)
		m1 := f.member(1, root, "sid-m1", "w-one", nil)
		target := c.target(m1.Ref)
		code, _, e := f.kill("/tmp/10.sock", target)
		if ok := code == 200; ok != c.ok || (!ok && e.Error != team.ErrNotYourMember) {
			t.Errorf("case %d %q: %d %+v, want matched=%v", i, target, code, e, c.ok)
		}
	}
}

// P4-6 review R1 [P1]: the kill marks the row it read, and only that one.
// A member mid-relay (claimed, writing, written) is refused before anything
// is killed: 409 relay_open with the op. Between the read and the mark —
// the beforeKillMark seam — a relay may claim (the mark is refused) or
// complete (cleared moved the row to the new session: the new session is
// never marked killed); both answer 409 relay_open and leave the row as it
// is. Another kill that marked it first makes this one a 200 (its time
// kept); the sweeper marking it gone meanwhile does not undo the kill.
// Mutation gates: drop session_id from the mark → the moved row is killed,
// red; drop the relay guard from the mark → red; drop the check before the
// tmux kill → a session is killed mid-relay, red.
func TestKill_MarksOnlyTheRowItRead(t *testing.T) {
	f, root := newTeamFixture(t, 4)
	m1 := f.member(1, root, "sid-m1", "w-one", nil)
	claimedOp(t, f.m.store, "relay-1", "sid-m1", m1.Ref)
	code, _, e := f.kill("/tmp/10.sock", m1.Ref)
	if code != 409 || e.Error != team.ErrRelayOpen || e.Op == nil || e.Op.ID != "relay-1" || len(f.tmux.KillIfInstanceCalls()) != 0 {
		t.Fatalf("a member mid-relay = %d %+v (kill calls %d), want 409 relay_open with its op and nothing killed", code, e, len(f.tmux.KillIfInstanceCalls()))
	}

	cases := []struct {
		name    string
		race    func(mr memberRow)
		code    int
		state   team.MemberState
		session string
		at      int64 // updated_at, when checked
	}{
		{"a relay completes", func(mr memberRow) {
			claimedOp(t, f.m.store, "relay-2", mr.SessionID, mr.Ref)
			mustReport(t, f.m.store, "relay-2", RelayReport{State: team.RelayCleared, NewSessionID: "sid-m2b", NewRef: ipeers.RefID("sid-m2b"), At: 5})
		}, 409, team.MemberActive, "sid-m2b", 0},
		{"a relay claims", func(mr memberRow) { claimedOp(t, f.m.store, "relay-3", mr.SessionID, mr.Ref) }, 409, team.MemberActive, "sid-m3", 0},
		{"another kill marks it first", func(mr memberRow) {
			if err := f.m.store.SetMemberState(mr.SpawnOp, team.MemberKilled, 6); err != nil {
				t.Fatal(err)
			}
		}, 200, team.MemberKilled, "sid-m4", 6},
		{"the sweeper marks it gone", func(mr memberRow) {
			if err := f.m.store.SetMemberState(mr.SpawnOp, team.MemberGone, 7); err != nil {
				t.Fatal(err)
			}
		}, 200, team.MemberKilled, "sid-m5", 0},
	}
	for i, c := range cases {
		mr := f.member(i+2, root, fmt.Sprintf("sid-m%d", i+2), fmt.Sprintf("w-%d", i+2), nil)
		f.m.beforeKillMark = c.race
		code, _, e := f.kill("/tmp/10.sock", mr.Ref)
		f.m.beforeKillMark = nil
		got := memberBySpawn(t, f.m.store, mr.SpawnOp)
		if code != c.code || (code == 409 && e.Error != team.ErrRelayOpen) || got.State != c.state || got.SessionID != c.session ||
			(c.at != 0 && got.UpdatedAt != c.at) {
			t.Errorf("%s: %d %+v, row %s %s; want %d, row %s %s", c.name, code, e, got.State, got.SessionID, c.code, c.state, c.session)
		}
	}
}
