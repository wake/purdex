package teammod

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/team"
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
