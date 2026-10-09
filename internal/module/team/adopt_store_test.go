package teammod

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// Adopt in the store (plan PL-1b1): the approve of an `adopt` request re-checks every refusal under the
// write lock and takes the target in as an adopted member in the same transaction.

const adoptAt = 5000

// adoptWorld: team-1 led by lead-1 (3 places) and a live target session "sid-t".
func adoptWorld(t *testing.T) *Store {
	t.Helper()
	s := openTestStore(t)
	seedTeam(t, s, "team-1", "lead-1", 1000)
	return s
}

func adoptPayload(teamID, lead, target string) team.AdoptPayload {
	return team.AdoptPayload{TeamID: teamID, LeadSessionID: lead, TargetRef: "_tgt001", TargetSessionID: target,
		TargetCwd: "/w/t", TargetTmux: "mine:@1.%3"}
}

// openAdopt stores an open adopt request id for payload p, from p's lead, on host h:1.
func openAdopt(t *testing.T, s *Store, id string, p team.AdoptPayload) {
	t.Helper()
	payload, _ := json.Marshal(p)
	a := team.Approval{ID: id, Kind: team.KindAdopt, HostID: "h:1",
		Origin:  team.Origin{SessionID: p.LeadSessionID, Ref: "_abc123", PID: 10, Cwd: "/w"},
		Payload: payload, State: team.StateOpen, CreatedAt: 1000, DeadlineAt: 1000 + 540_000, LeaseUntil: 1000 + 30_000}
	if _, _, _, err := s.Create(a, "h-"+id); err != nil {
		t.Fatal(err)
	}
}

func adoptClose() Close {
	return Close{State: team.StateApproved, DecidedAt: adoptAt, DecidedBy: &team.Client{Kind: "app", Label: "Purdex.app @ air26"}}
}

// adoptedRow is the member row an approve of request id inserts for payload p.
func adoptedRow(id string, p team.AdoptPayload) memberRow {
	return memberRow{SpawnOp: id, TeamID: p.TeamID, HostID: "h:1", SessionID: p.TargetSessionID, Ref: p.TargetRef, Cwd: p.TargetCwd,
		TmuxSession: "mine", PID: 77, ProcStart: "p77", PaneID: "%3", State: team.MemberActive, Origin: team.MemberOriginAdopted,
		NoticePending: team.NoticeAdopted, NoticeSince: adoptAt, CreatedAt: adoptAt, UpdatedAt: adoptAt}
}

func chkOK() adoptCheck { return adoptCheck{HostID: "h:1", TargetLive: true} }

func memberByKey(t *testing.T, s *Store, teamID, key string) (memberRow, bool) {
	t.Helper()
	rows, err := s.MembersOf(teamID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range rows {
		if m.SpawnOp == key {
			return m, true
		}
	}
	return memberRow{}, false
}

func TestCloseAdoptApproved_InsertsTheAdoptedMemberInOneTx(t *testing.T) {
	s := adoptWorld(t)
	p := adoptPayload("team-1", "lead-1", "sid-t")
	openAdopt(t, s, "ad-1", p)
	a, won, refused, err := s.CloseAdoptApproved("ad-1", adoptClose(), p, chkOK(), adoptedRow("ad-1", p))
	if err != nil || !won || refused != "" || a.State != team.StateApproved || a.CloseReason != "" {
		t.Fatalf("approve: %+v won=%v refused=%q err=%v", a, won, refused, err)
	}
	m, ok := memberByKey(t, s, "team-1", "ad-1")
	if !ok || m.Origin != team.MemberOriginAdopted || m.State != team.MemberActive || m.SessionID != "sid-t" ||
		m.NoticePending != team.NoticeAdopted || m.NoticeSince != adoptAt || m.PID != 77 || m.PaneID != "%3" {
		t.Fatalf("member = %+v ok=%v", m, ok)
	}
	if _, tm, found, err := s.ActiveMemberInLiveTeam("sid-t"); err != nil || !found || tm.ID != "team-1" {
		t.Fatalf("the target is not an active member of the team: %v %v %+v", found, err, tm)
	}
}

// Every refusal of D-U24-2 is checked again at approve, each answering its code: the request closes
// cancelled with the code as close_reason and no member row exists. Mutation gate: drop one re-check →
// its sub-test red; insert the member after the commit → every sub-test red (a member for a cancelled request).
func TestCloseAdoptApproved_EachRefusalCancelsWithItsCode(t *testing.T) {
	for _, tc := range []struct {
		name  string
		code  string
		setup func(t *testing.T, s *Store, p *team.AdoptPayload) adoptCheck
	}{
		{"not_lead", team.ErrNotLead, func(t *testing.T, s *Store, p *team.AdoptPayload) adoptCheck {
			if ended, err := s.EndTeam("team-1", "lead-1", team.TeamEndLeadGone, 2000); err != nil || !ended {
				t.Fatal(ended, err)
			}
			return chkOK()
		}},
		{"remote_unsupported", team.ErrRemoteUnsupported, func(*testing.T, *Store, *team.AdoptPayload) adoptCheck {
			return adoptCheck{HostID: "h:other", TargetLive: true}
		}},
		{"adopt_target_not_found", team.ErrAdoptTargetNotFound, func(*testing.T, *Store, *team.AdoptPayload) adoptCheck {
			return adoptCheck{HostID: "h:1", TargetLive: false}
		}},
		{"adopt_self", team.ErrAdoptSelf, func(t *testing.T, s *Store, p *team.AdoptPayload) adoptCheck {
			return chkOK() // the stored request names the lead itself as the target (see below)
		}},
		{"adopt_target_is_lead", team.ErrAdoptTargetIsLead, func(t *testing.T, s *Store, p *team.AdoptPayload) adoptCheck {
			seedTeam(t, s, "team-2", "sid-t", 1500)
			return chkOK()
		}},
		{"adopt_already_member", team.ErrAdoptAlreadyMember, func(t *testing.T, s *Store, p *team.AdoptPayload) adoptCheck {
			seedMember(t, s, "op-m", "team-1", "sid-t", 1500)
			return chkOK()
		}},
		{"request_open", team.ErrRequestOpen, func(t *testing.T, s *Store, p *team.AdoptPayload) adoptCheck {
			other := adoptPayload("team-1", "lead-1", "sid-t")
			openAdopt(t, s, "ad-other", other) // a second open row for the target, straight into the DB
			return chkOK()
		}},
		{"team_full", team.ErrTeamFull, func(t *testing.T, s *Store, p *team.AdoptPayload) adoptCheck {
			for i, sid := range []string{"sid-a", "sid-b", "sid-c"} {
				seedMember(t, s, "op-"+string(rune('a'+i)), "team-1", sid, 1500)
			}
			return chkOK()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := adoptWorld(t)
			p := adoptPayload("team-1", "lead-1", "sid-t")
			if tc.name == "adopt_self" {
				p.TargetSessionID = p.LeadSessionID // a request the create would have refused, stored directly
			}
			openAdopt(t, s, "ad-1", p) // created while everything was fine
			chk := tc.setup(t, s, &p)
			a, won, refused, err := s.CloseAdoptApproved("ad-1", adoptClose(), p, chk, adoptedRow("ad-1", p))
			if err != nil || !won || refused != tc.code {
				t.Fatalf("won=%v refused=%q err=%v, want won with %q", won, refused, err, tc.code)
			}
			if a.State != team.StateCancelled || a.CloseReason != tc.code || a.DecidedBy != nil {
				t.Fatalf("row = state %q reason %q decided_by %+v", a.State, a.CloseReason, a.DecidedBy)
			}
			if _, ok := memberByKey(t, s, "team-1", "ad-1"); ok {
				t.Fatal("a cancelled request has a member row")
			}
		})
	}
}

// The target's `active` row in an ended team would break team_members_one_active: the approve retires it.
func TestCloseAdoptApproved_RetiresAStaleRowOfAnEndedTeam(t *testing.T) {
	s := adoptWorld(t)
	seedTeam(t, s, "team-old", "lead-old", 500)
	seedMember(t, s, "op-old", "team-old", "sid-t", 600)
	if ended, err := s.EndTeam("team-old", "lead-old", team.TeamEndLeadGone, 900); err != nil || !ended {
		t.Fatal(ended, err)
	}
	p := adoptPayload("team-1", "lead-1", "sid-t")
	openAdopt(t, s, "ad-1", p)
	if _, won, refused, err := s.CloseAdoptApproved("ad-1", adoptClose(), p, chkOK(), adoptedRow("ad-1", p)); err != nil || !won || refused != "" {
		t.Fatalf("won=%v refused=%q err=%v", won, refused, err)
	}
	old, ok := memberByKey(t, s, "team-old", "op-old")
	if !ok || old.State != team.MemberReleased || old.EndedAt != adoptAt {
		t.Fatalf("stale row = %+v ok=%v", old, ok)
	}
	if fresh, ok := memberByKey(t, s, "team-1", "ad-1"); !ok || fresh.State != team.MemberActive {
		t.Fatalf("new row = %+v ok=%v", fresh, ok)
	}
}

// Another writer closed the request first: nothing is written and nothing is reported.
func TestCloseAdoptApproved_LostCASWritesNothing(t *testing.T) {
	s := adoptWorld(t)
	p := adoptPayload("team-1", "lead-1", "sid-t")
	openAdopt(t, s, "ad-1", p)
	if _, err := s.db.Exec(`UPDATE approval_requests SET state = 'denied', decided_at = 4000 WHERE id = 'ad-1'`); err != nil {
		t.Fatal(err)
	}
	a, won, refused, err := s.CloseAdoptApproved("ad-1", adoptClose(), p, chkOK(), adoptedRow("ad-1", p))
	if err != nil || won || refused != "" || a.State != team.StateDenied {
		t.Fatalf("a=%+v won=%v refused=%q err=%v", a, won, refused, err)
	}
	// and a refusal that would have applied does not rewrite the winner's close
	a, won, refused, err = s.CloseAdoptApproved("ad-1", adoptClose(), p, adoptCheck{HostID: "h:1"}, adoptedRow("ad-1", p))
	if err != nil || won || refused != "" || a.State != team.StateDenied || a.CloseReason != "" {
		t.Fatalf("a=%+v won=%v refused=%q err=%v", a, won, refused, err)
	}
	if _, ok := memberByKey(t, s, "team-1", "ad-1"); ok {
		t.Fatal("a member row for a request someone else closed")
	}
}

// The second request for a session that is already a member (the first approve inserted it) is refused.
func TestCloseAdoptApproved_AfterTheFirstTheSecondLeadIsRefused(t *testing.T) {
	s := adoptWorld(t)
	seedTeam(t, s, "team-2", "lead-2", 1100)
	p1 := adoptPayload("team-1", "lead-1", "sid-t")
	openAdopt(t, s, "ad-1", p1)
	if _, won, refused, err := s.CloseAdoptApproved("ad-1", adoptClose(), p1, chkOK(), adoptedRow("ad-1", p1)); err != nil || !won || refused != "" {
		t.Fatal(won, refused, err)
	}
	p2 := adoptPayload("team-2", "lead-2", "sid-t")
	openAdopt(t, s, "ad-2", p2)
	_, won, refused, err := s.CloseAdoptApproved("ad-2", adoptClose(), p2, chkOK(), adoptedRow("ad-2", p2))
	if err != nil || !won || refused != team.ErrAdoptAlreadyMember {
		t.Fatalf("won=%v refused=%q err=%v", won, refused, err)
	}
}

// A member row that does not describe the request is a misuse: an error, nothing written.
func TestCloseAdoptApproved_MisuseWritesNothing(t *testing.T) {
	s := adoptWorld(t)
	p := adoptPayload("team-1", "lead-1", "sid-t")
	openAdopt(t, s, "ad-1", p)
	for name, mut := range map[string]func(*memberRow){
		"another key":     func(m *memberRow) { m.SpawnOp = "other" },
		"spawned origin":  func(m *memberRow) { m.Origin = team.MemberOriginSpawned },
		"another session": func(m *memberRow) { m.SessionID = "sid-x" },
		"no notice owed":  func(m *memberRow) { m.NoticePending = "" },
		"not active":      func(m *memberRow) { m.State = team.MemberGone },
	} {
		m := adoptedRow("ad-1", p)
		mut(&m)
		if _, _, _, err := s.CloseAdoptApproved("ad-1", adoptClose(), p, chkOK(), m); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if a, _, _ := s.Get("ad-1"); a.State != team.StateOpen {
		t.Fatalf("the request changed: %+v", a)
	}
	if _, _, _, err := s.CloseAdoptApproved("ad-1", Close{State: team.StateDenied, DecidedAt: 1}, p, chkOK(), adoptedRow("ad-1", p)); err == nil {
		t.Error("a denied close was taken as an approve")
	}
}

func TestOpenAdoptForTargetAndSeatsUsed(t *testing.T) {
	s := adoptWorld(t)
	if _, ok, err := s.OpenAdoptForTarget("sid-t"); err != nil || ok {
		t.Fatalf("none yet: ok=%v err=%v", ok, err)
	}
	p := adoptPayload("team-1", "lead-1", "sid-t")
	openAdopt(t, s, "ad-1", p)
	if a, ok, err := s.OpenAdoptForTarget("sid-t"); err != nil || !ok || a.ID != "ad-1" {
		t.Fatalf("open adopt = %+v ok=%v err=%v", a, ok, err)
	}
	if _, ok, _ := s.OpenAdoptForTarget("sid-other"); ok {
		t.Fatal("another target's request found")
	}
	if _, _, _, err := s.CloseAdoptApproved("ad-1", adoptClose(), p, chkOK(), adoptedRow("ad-1", p)); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.OpenAdoptForTarget("sid-t"); ok {
		t.Fatal("a closed request is still open")
	}
	seedMember(t, s, "op-1", "team-1", "sid-a", 1500)
	if used, limit, err := s.SeatsUsed("team-1"); err != nil || used != 2 || limit != 3 { // the adopted one and op-1
		t.Fatalf("seats = %d/%d err=%v", used, limit, err)
	}
	if used, limit, err := s.SeatsUsed("no-such-team"); err != nil || used != 0 || limit != 0 {
		t.Fatalf("unknown team = %d/%d err=%v", used, limit, err)
	}
}

// Rows written before PL-1b read as spawned, not ended, nothing owed; the migration keeps data and runs twice.
func TestMigrateAdopt_AddsColumnsOnceKeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE approval_requests (id TEXT PRIMARY KEY, kind TEXT NOT NULL, host_id TEXT NOT NULL, origin_session_id TEXT NOT NULL,
			origin_json TEXT NOT NULL, payload_json TEXT NOT NULL, request_hash TEXT NOT NULL, state TEXT NOT NULL, created_at INTEGER NOT NULL,
			deadline_at INTEGER NOT NULL, lease_until INTEGER NOT NULL, decided_by_json TEXT, decided_at INTEGER NOT NULL DEFAULT 0, grant_json TEXT)`,
		`INSERT INTO approval_requests (id, kind, host_id, origin_session_id, origin_json, payload_json, request_hash, state, created_at, deadline_at, lease_until)
			VALUES ('old', 'lead', 'h:1', 'sid', '{}', '{}', 'h', 'denied', 1, 2, 3)`,
		`CREATE TABLE team_members (spawn_op TEXT PRIMARY KEY, team_id TEXT NOT NULL, host_id TEXT NOT NULL, session_id TEXT NOT NULL, ref TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '', cwd TEXT NOT NULL, tmux_session TEXT NOT NULL, tmux_id TEXT NOT NULL DEFAULT '', tmux_instance TEXT NOT NULL DEFAULT '',
			pane_id TEXT NOT NULL DEFAULT '', pid INTEGER NOT NULL DEFAULT 0, proc_start TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '',
			effort TEXT NOT NULL DEFAULT '', state TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
		`INSERT INTO team_members (spawn_op, team_id, host_id, session_id, ref, cwd, tmux_session, state, created_at, updated_at)
			VALUES ('op-old', 'team-1', 'h:1', 'sid-m', '_m', '/w', 'tm', 'active', 1, 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	for i := 0; i < 2; i++ { // the second open finds the columns and adds nothing
		s, err := OpenStore(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		a, ok, err := s.Get("old")
		if err != nil || !ok || a.CloseReason != "" || a.State != team.StateDenied {
			t.Fatalf("old request = %+v ok=%v err=%v", a, ok, err)
		}
		m, ok := memberByKey(t, s, "team-1", "op-old")
		if !ok || m.Origin != team.MemberOriginSpawned || m.EndedAt != 0 || m.NoticePending != "" || m.NoticeSince != 0 || m.State != team.MemberActive {
			t.Fatalf("old member = %+v ok=%v", m, ok)
		}
		s.Close()
	}
}

// MemberLastTurnAts reads only active rows: a released row's age is not the team view's business.
func TestMemberLastTurnAts_OnlyActiveRows(t *testing.T) {
	s := adoptWorld(t)
	seedMember(t, s, "op-a", "team-1", "sid-a", 1500)
	seedMember(t, s, "op-b", "team-1", "sid-b", 1600)
	if _, err := s.SetLastTurn("sid-a", "a.", 100, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetLastTurn("sid-b", "b.", 200, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE team_members SET state = 'released' WHERE spawn_op = 'op-b'`); err != nil {
		t.Fatal(err)
	}
	got, err := s.MemberLastTurnAts("team-1")
	if err != nil || len(got) != 1 || got["op-a"] != 100 {
		t.Fatalf("last turns = %v err=%v", got, err)
	}
}

// A request id that is already a team_members key must not leave an approved request without its member
// (codex R1): the whole transaction rolls back and the request stays open.
// Mutation gate: ignore the insert's RowsAffected → red.
func TestCloseAdoptApproved_ATakenKeyRollsBack(t *testing.T) {
	s := adoptWorld(t)
	seedMember(t, s, "ad-1", "team-1", "sid-old", 1500) // a row that already holds the key
	if _, err := s.db.Exec(`UPDATE team_members SET state = 'gone' WHERE spawn_op = 'ad-1'`); err != nil {
		t.Fatal(err)
	}
	p := adoptPayload("team-1", "lead-1", "sid-t")
	openAdopt(t, s, "ad-1", p)
	if _, _, _, err := s.CloseAdoptApproved("ad-1", adoptClose(), p, chkOK(), adoptedRow("ad-1", p)); err == nil {
		t.Fatal("an approve whose member key is taken succeeded")
	}
	if a, _, _ := s.Get("ad-1"); a.State != team.StateOpen {
		t.Fatalf("the request closed: %+v", a)
	}
}

// The approve acts on the STORED request: a payload that is not the row's (another target, another team,
// another lead), or a row that is not an adopt, is an error and nothing is written (codex attack on PL-1b1).
// Mutation gate: skip the comparison → red.
func TestCloseAdoptApproved_ActsOnlyOnTheStoredRequest(t *testing.T) {
	s := adoptWorld(t)
	seedTeam(t, s, "team-2", "lead-2", 1100)
	p := adoptPayload("team-1", "lead-1", "sid-t")
	openAdopt(t, s, "ad-1", p)
	for name, bad := range map[string]team.AdoptPayload{
		"another target": adoptPayload("team-1", "lead-1", "sid-other"),
		"another team":   adoptPayload("team-2", "lead-1", "sid-t"),
		"another lead":   adoptPayload("team-1", "lead-2", "sid-t"),
	} {
		if _, _, _, err := s.CloseAdoptApproved("ad-1", adoptClose(), bad, chkOK(), adoptedRow("ad-1", bad)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// a request that is not an adopt (a lead request id)
	if _, _, _, err := s.CloseAdoptApproved("team-2", adoptClose(), p, chkOK(), adoptedRow("team-2", p)); err == nil {
		t.Error("a lead request taken as an adopt")
	}
	if a, _, _ := s.Get("ad-1"); a.State != team.StateOpen {
		t.Fatalf("the request changed: %+v", a)
	}
	if rows, _ := s.MembersOf("team-1"); len(rows) != 0 {
		t.Fatalf("members written: %+v", rows)
	}
}
