// internal/module/team/pause_store_test.go — the pause state (unattended quota pause spec §3.1, UP-1a).
package teammod

import (
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func pauseRow(sid, cause, role string, since int64) PauseRow {
	return PauseRow{SessionID: sid, Cause: cause, Role: role, TeamID: "team-1", LeadSessionID: "lead-1", OnSince: 500, Since: since}
}

// The four tables reach a team.db written before them, and what was in it stays.
func TestPauseTables_MigrateAnExistingDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	seedTeam(t, s, "team-1", "lead-1", 1000)
	for _, tbl := range []string{"session_pauses", "pause_outbox", "pause_bounces", "pause_release_tokens"} {
		if _, err := s.db.Exec(`DROP TABLE ` + tbl); err != nil { // a team.db from before this migration has none of them
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatalf("reopen an old team.db: %v", err)
	}
	defer s.Close()
	for _, tbl := range []string{"session_pauses", "pause_outbox", "pause_bounces", "pause_release_tokens"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + tbl).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s: n=%d err=%v", tbl, n, err)
		}
	}
	if _, ok, err := s.LiveTeamByLead("lead-1"); err != nil || !ok {
		t.Fatalf("the team written before the migration: ok=%v err=%v", ok, err)
	}
	// opening it a second time changes nothing (idempotent)
	if _, err := s.AddPause(pauseRow("sid-1", team.PauseCauseQuota, team.PauseRoleSolo, 10)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if rows, err := s2.Pauses(); err != nil || len(rows) != 1 {
		t.Fatalf("pauses after a second open: %v err=%v", rows, err)
	}
}

func TestPause_CausesAreConstrained(t *testing.T) {
	s := openTestStore(t)
	for name, p := range map[string]PauseRow{
		"unknown cause": pauseRow("s", "boredom", team.PauseRoleSolo, 1),
		"unknown role":  pauseRow("s", team.PauseCauseQuota, "boss", 1),
		"no session":    pauseRow("", team.PauseCauseQuota, team.PauseRoleSolo, 1),
	} {
		if _, err := s.AddPause(p); err == nil {
			t.Errorf("%s was stored", name)
		}
	}
}

// Two causes coexist on one session; each is written once. Mutation gate: a non-idempotent write (replace, or a second row)
// → the first-write values are overwritten / a duplicate error (red).
func TestPause_TwoCausesCoexistAndAWriteIsIdempotentPerCause(t *testing.T) {
	s := openTestStore(t)
	created, err := s.AddPause(pauseRow("sid-m", team.PauseCauseQuota, team.PauseRoleMember, 100))
	if err != nil || !created {
		t.Fatalf("first write: created=%v err=%v", created, err)
	}
	created, err = s.AddPause(pauseRow("sid-m", team.PauseCauseLead, team.PauseRoleMember, 200))
	if err != nil || !created {
		t.Fatalf("a second cause: created=%v err=%v", created, err)
	}
	again := pauseRow("sid-m", team.PauseCauseQuota, team.PauseRoleMember, 999)
	again.OnSince = 777
	created, err = s.AddPause(again)
	if err != nil || created {
		t.Fatalf("the same (session, cause) again: created=%v err=%v, want a no-op", created, err)
	}
	rows, err := s.Pauses()
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %+v err=%v", rows, err)
	}
	for _, r := range rows {
		if r.Cause == team.PauseCauseQuota && (r.Since != 100 || r.OnSince != 500) {
			t.Fatalf("the repeated write changed the first row: %+v", r)
		}
	}
}

func TestPause_PausedSessionsListsCausesSinceAndBounces(t *testing.T) {
	s := openTestStore(t)
	if got, err := s.PausedSessions(); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("none paused: %#v err=%v (must be [] not nil)", got, err)
	}
	for _, p := range []PauseRow{
		pauseRow("sid-m", team.PauseCauseLead, team.PauseRoleMember, 200),
		pauseRow("sid-m", team.PauseCauseQuota, team.PauseRoleMember, 100),
		pauseRow("sid-solo", team.PauseCauseQuota, team.PauseRoleSolo, 50),
	} {
		if _, err := s.AddPause(p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO pause_bounces (session_id, from_addr, text_hash, at, first_line) VALUES ('sid-m', 'a', 'h1', 1, 'x'), ('sid-m', 'b', 'h2', 2, 'y'), ('other', 'a', 'h1', 1, 'z')`); err != nil {
		t.Fatal(err)
	}
	got, err := s.PausedSessions()
	if err != nil || len(got) != 2 {
		t.Fatalf("paused = %+v err=%v", got, err)
	}
	solo, m := got[0], got[1] // by since: solo (50) before the member (earliest 100)
	if solo.SessionID != "sid-solo" || solo.Role != team.PauseRoleSolo || len(solo.Causes) != 1 || solo.Since != 50 || solo.Bounced != 0 {
		t.Fatalf("solo = %+v", solo)
	}
	if m.SessionID != "sid-m" || m.Role != team.PauseRoleMember || m.Since != 100 || m.Bounced != 2 || m.LeadSessionID != "lead-1" || m.TeamID != "team-1" ||
		len(m.Causes) != 2 || m.Causes[0] != team.PauseCauseLead || m.Causes[1] != team.PauseCauseQuota {
		t.Fatalf("member = %+v (causes sorted: lead, quota)", m)
	}
}

// "A live local member of a lead": an active row of the lead's live team on THIS host (the persisted predicate of
// ActiveMembersOfLiveTeams). Mutation gate: count a killing member → the killing case below is listed (red).
func TestPause_LiveLocalMembersOfLead(t *testing.T) {
	s := openTestStore(t)
	s.localHostID = "h:1"
	seedTeam(t, s, "team-1", "lead-1", 1000)
	seedTeam(t, s, "team-2", "lead-2", 1000)
	seedTeam(t, s, "team-3", "lead-3", 1000)
	seedMember(t, s, "op-active", "team-1", "sid-active", 2000)
	set := func(op, sid string, state team.MemberState) {
		seedMember(t, s, op, "team-1", sid, 2000)
		if err := s.SetMemberState(op, state, 3000); err != nil {
			t.Fatal(err)
		}
	}
	set("op-killing", "sid-killing", team.MemberKilling)
	set("op-releasing", "sid-releasing", team.MemberReleasing)
	set("op-gone", "sid-gone", team.MemberGone)
	set("op-killed", "sid-killed", team.MemberKilled)
	set("op-joining", "sid-joining", team.MemberJoining)
	remote := newMember("op-remote", "team-1", "sid-remote", "_remote", 2000)
	remote.HostID = "other:2"
	if err := s.InsertMember(remote); err != nil {
		t.Fatal(err)
	}
	seedMember(t, s, "op-other-team", "team-2", "sid-other-team", 2000) // another lead's member
	seedMember(t, s, "op-ended", "team-3", "sid-ended", 2000)
	if ended, err := s.EndTeam("team-3", "lead-3", "done", 4000); err != nil || !ended {
		t.Fatalf("end team: %v %v", ended, err)
	}

	got, err := s.LiveLocalMembersOfLead("lead-1")
	if err != nil || len(got) != 1 || got[0].SessionID != "sid-active" {
		t.Fatalf("members of lead-1 = %+v err=%v, want only the active local one", got, err)
	}
	for lead, want := range map[string]string{"lead-2": "sid-other-team", "lead-3": "", "nobody": ""} {
		got, err := s.LiveLocalMembersOfLead(lead)
		switch {
		case err != nil:
			t.Fatal(err)
		case want == "" && len(got) != 0:
			t.Fatalf("%s: %+v, want none", lead, got)
		case want != "" && (len(got) != 1 || got[0].SessionID != want):
			t.Fatalf("%s: %+v, want %s", lead, got, want)
		}
	}
	// a bare store (no local host id) filters no host out, like the other local readers
	s.localHostID = ""
	if got, _ := s.LiveLocalMembersOfLead("lead-1"); len(got) != 2 {
		t.Fatalf("with no local host id: %d members, want the active local and the active remote", len(got))
	}
}
