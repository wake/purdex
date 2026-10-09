// internal/module/team/commands_store_test.go
package teammod

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

const leadHostA = "host-A"

func adoptCmd(id, mk, target string) team.TeamCommand {
	return team.TeamCommand{ID: id, Kind: team.CommandAdopt, ToHostID: "h:1", TeamID: "team-L", TeamName: "T", MK: mk,
		Lead:            team.TeamLead{SessionID: "lead-sid", Ref: "_lead01", Title: "lead", Address: "lead/x [lead01]", PID: 7, ProcStart: "ps1"},
		TargetSessionID: target, TargetRef: "_tgt001"}
}

func targetOrigin(sid string) *team.Origin {
	return &team.Origin{SessionID: sid, Ref: "_tgt001", PID: 42, ProcStart: "ps2", Cwd: "/w", Title: "m", Tmux: "tm:@1.%3"}
}

func plan(cmd team.TeamCommand, consent bool, target *team.Origin) CommandPlan {
	body, _ := json.Marshal(cmd)
	return CommandPlan{LeadHostID: leadHostA, Body: body, Consent: consent, Target: target, Now: 1000}
}

func mustApply(t *testing.T, s *Store, p CommandPlan) CommandResult {
	t.Helper()
	res, err := s.ApplyTeamCommand(p)
	if err != nil {
		t.Fatalf("apply %s: %v", p.Body, err)
	}
	return res
}

func refusalCode(t *testing.T, res CommandResult) string {
	t.Helper()
	var r team.CommandRefusal
	if err := json.Unmarshal(res.Body, &r); err != nil {
		t.Fatalf("body %s: %v", res.Body, err)
	}
	return r.Error
}

func noticesOf(t *testing.T, s *Store, mk string) []remoteNoticeRow {
	t.Helper()
	n, err := s.RemoteNotices(mk)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestApplyAdopt_WritesRowNoticeAndLogInOneStep(t *testing.T) {
	s := openTestStore(t)
	res := mustApply(t, s, plan(adoptCmd("c1", "mk-1", "sid-t"), true, targetOrigin("sid-t")))
	if res.Status != http.StatusOK || res.Replayed {
		t.Fatalf("res = %+v", res)
	}
	var out team.AdoptOutcome
	if err := json.Unmarshal(res.Body, &out); err != nil || out.State != "applied" || out.MemberSession != "sid-t" || out.PID != 42 {
		t.Fatalf("outcome = %s (%v)", res.Body, err)
	}
	row, ok, _ := s.RemoteMember("mk-1")
	if !ok || row.State != remoteActive || row.MemberSessionID != "sid-t" || row.LeadHostID != leadHostA || row.TeamID != "team-L" ||
		row.LeadAddress != "lead/x [lead01]" || row.LeadPID != 7 || row.Origin != "adopted" || row.Cwd != "/w" {
		t.Fatalf("row = %+v", row)
	}
	n := noticesOf(t, s, "mk-1")
	if len(n) != 1 || n[0].Kind != noticeAdopted || n[0].State != noticeOwed || n[0].CauseID != "c1" {
		t.Fatalf("notices = %+v", n)
	}
	if role, _ := s.SessionRole("sid-t"); role != sessionRoleMemberRemote {
		t.Fatalf("role = %s", role)
	}
}

// Rule 3: the same id with the same content answers the stored outcome and writes nothing; another content is
// id_conflict; ids are scoped by the lead host that sent them.
func TestApplyCommand_IdempotentByIDAndContent(t *testing.T) {
	s := openTestStore(t)
	cmd := adoptCmd("c1", "mk-1", "sid-t")
	first := mustApply(t, s, plan(cmd, true, targetOrigin("sid-t")))
	again := mustApply(t, s, plan(cmd, true, targetOrigin("sid-t")))
	if !again.Replayed || again.Status != first.Status || string(again.Body) != string(first.Body) {
		t.Fatalf("replay = %+v, first = %+v", again, first)
	}
	if n := noticesOf(t, s, "mk-1"); len(n) != 1 {
		t.Fatalf("a replay wrote another notice: %+v", n)
	}
	other := cmd
	other.TargetRef = "_other1"
	if _, err := s.ApplyTeamCommand(plan(other, true, targetOrigin("sid-t"))); !errors.Is(err, ErrCommandIDConflict) {
		t.Fatalf("same id, other content: err = %v, want ErrCommandIDConflict", err)
	}
	// Host B's command with the same id is its own command, not a replay of A's.
	pb := plan(adoptCmd("c1", "mk-b", "sid-u"), true, targetOrigin("sid-u"))
	pb.LeadHostID = "host-B"
	if res := mustApply(t, s, pb); res.Replayed || res.Status != http.StatusOK {
		t.Fatalf("host B's c1: %+v", res)
	}
}

// The hash is over the bytes as received: a field this version does not know still makes a different command
// (a version-skewed sender cannot pass one off as a replay), and the same id under another kind is a conflict
// (codex attack).
func TestApplyCommand_HashCoversUnknownFieldsAndKind(t *testing.T) {
	s := openTestStore(t)
	cmd := adoptCmd("c1", "mk-1", "sid-t")
	body, _ := json.Marshal(cmd)
	p := plan(cmd, true, targetOrigin("sid-t"))
	mustApply(t, s, p)

	var m map[string]any
	_ = json.Unmarshal(body, &m)
	m["a_field_of_a_newer_version"] = "x"
	skewed, _ := json.Marshal(m)
	q := p
	q.Body = skewed
	if _, err := s.ApplyTeamCommand(q); !errors.Is(err, ErrCommandIDConflict) {
		t.Fatalf("unknown field: err = %v, want ErrCommandIDConflict", err)
	}

	m = nil
	_ = json.Unmarshal(body, &m)
	m["kind"] = team.CommandRelease
	other, _ := json.Marshal(m)
	q.Body = other
	if _, err := s.ApplyTeamCommand(q); !errors.Is(err, ErrCommandIDConflict) {
		t.Fatalf("other kind: err = %v, want ErrCommandIDConflict", err)
	}
	if again := mustApply(t, s, p); !again.Replayed {
		t.Fatalf("the original bytes no longer replay: %+v", again)
	}
	if _, err := s.ApplyTeamCommand(CommandPlan{LeadHostID: leadHostA, Body: []byte(`{"id":""}`)}); !errors.Is(err, ErrCommandBadBody) {
		t.Fatalf("empty id: err = %v, want ErrCommandBadBody", err)
	}
}

// A notice carries the lead and team as its own event left them: a later lead_moved does not rewrite it.
func TestApplyCommand_NoticeSnapshotsItsEvent(t *testing.T) {
	s := openTestStore(t)
	mustApply(t, s, plan(adoptCmd("c1", "mk-1", "sid-t"), true, targetOrigin("sid-t")))
	for i, addr := range []string{"lead/second [l2]", "lead/third [l3]\x07"} {
		mv := relCmd("m"+string(rune('1'+i)), team.CommandLeadMoved, "")
		mv.LeadSessionID, mv.LeadRef = "lead-"+addr[5:6], "_lead0"+addr[6:7]
		mv.Lead.Address = addr
		mustApply(t, s, plan(mv, false, nil))
	}
	n := noticesOf(t, s, "mk-1")
	if len(n) != 3 {
		t.Fatalf("notices = %+v", n)
	}
	for i, want := range []string{"lead/x [lead01]", "lead/second [l2]", "lead/third [l3]"} { // control chars are stripped
		if n[i].LeadAddress != want || n[i].TeamName != "T" {
			t.Fatalf("notice %d = %+v, want lead %q team T", i, n[i], want)
		}
	}
}

// Refusals are stored with their status: a replay after the condition changed still answers the refusal.
func TestApplyAdopt_RefusalsAreStoredAndWriteNothing(t *testing.T) {
	cases := []struct {
		name    string
		prep    func(t *testing.T, s *Store)
		consent bool
		target  *team.Origin
		status  int
		code    string
	}{
		{"no consent", nil, false, targetOrigin("sid-t"), http.StatusForbidden, team.ErrCommandHostNotAllowed},
		{"target not found", nil, true, nil, http.StatusConflict, team.ErrAdoptTargetNotFound},
		{"target leads", func(t *testing.T, s *Store) { seedTeam(t, s, "team-1", "sid-t", 1000) }, true, targetOrigin("sid-t"), http.StatusConflict, team.ErrAdoptTargetIsLead},
		{"target is a local member", func(t *testing.T, s *Store) {
			seedTeam(t, s, "team-1", "lead-1", 1000)
			seedMember(t, s, "op-1", "team-1", "sid-t", 1000)
		}, true, targetOrigin("sid-t"), http.StatusConflict, team.ErrAdoptAlreadyMember},
		{"target is a remote member", func(t *testing.T, s *Store) { seedRemote(t, s, "mk-x", "sid-t", 1000) }, true, targetOrigin("sid-t"), http.StatusConflict, team.ErrAdoptAlreadyMember},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			if tc.prep != nil {
				tc.prep(t, s)
			}
			cmd := adoptCmd("c1", "mk-1", "sid-t")
			res := mustApply(t, s, plan(cmd, tc.consent, tc.target))
			if res.Status != tc.status || refusalCode(t, res) != tc.code {
				t.Fatalf("res = %d %s, want %d %s", res.Status, res.Body, tc.status, tc.code)
			}
			if _, ok, _ := s.RemoteMember("mk-1"); ok {
				t.Fatal("a refused adopt stored a row")
			}
			if n := noticesOf(t, s, "mk-1"); len(n) != 0 {
				t.Fatalf("a refused adopt owes a notice: %+v", n)
			}
			// The condition changes (consent given, target found); the same id still answers the refusal.
			again := mustApply(t, s, plan(cmd, true, targetOrigin("sid-fresh")))
			if !again.Replayed || again.Status != tc.status || refusalCode(t, again) != tc.code {
				t.Fatalf("replay = %+v", again)
			}
		})
	}
}

func TestApplyAdopt_MKReusedForAnotherSessionIsRefused(t *testing.T) {
	s := openTestStore(t)
	mustApply(t, s, plan(adoptCmd("c1", "mk-1", "sid-t"), true, targetOrigin("sid-t")))
	res := mustApply(t, s, plan(adoptCmd("c2", "mk-1", "sid-u"), true, targetOrigin("sid-u")))
	if res.Status != http.StatusConflict || refusalCode(t, res) != team.ErrCommandMKConflict {
		t.Fatalf("res = %d %s", res.Status, res.Body)
	}
	if row, _, _ := s.RemoteMember("mk-1"); row.MemberSessionID != "sid-t" {
		t.Fatalf("row = %+v", row)
	}
}

func relCmd(id, kind, mk string) team.TeamCommand {
	c := adoptCmd(id, mk, "")
	c.Kind, c.TargetRef = kind, ""
	return c
}

func TestApplyRelease_ActiveToReleasedOnce(t *testing.T) {
	s := openTestStore(t)
	mustApply(t, s, plan(adoptCmd("c1", "mk-1", "sid-t"), true, targetOrigin("sid-t")))
	res := mustApply(t, s, plan(relCmd("c2", team.CommandRelease, "mk-1"), false, nil))
	if res.Status != http.StatusOK {
		t.Fatalf("release = %d %s", res.Status, res.Body)
	}
	if row, _, _ := s.RemoteMember("mk-1"); row.State != remoteReleased {
		t.Fatalf("row = %+v", row)
	}
	if role, _ := s.SessionRole("sid-t"); role != sessionRoleNone {
		t.Fatalf("a released session still has role %s", role)
	}
	var kinds []string
	for _, n := range noticesOf(t, s, "mk-1") {
		kinds = append(kinds, n.Kind)
	}
	if len(kinds) != 2 || kinds[1] != noticeReleased {
		t.Fatalf("notices = %v", kinds)
	}
	// A second release (a new command id) finds no live row.
	res = mustApply(t, s, plan(relCmd("c3", team.CommandRelease, "mk-1"), false, nil))
	if res.Status != http.StatusConflict || refusalCode(t, res) != team.ErrCommandNotYourMember {
		t.Fatalf("second release = %d %s", res.Status, res.Body)
	}
}

// release needs the lead host's own row of that team: another host, another team, or no mk is not_your_member.
func TestApplyRelease_OnlyTheOwnersRowOfThatTeam(t *testing.T) {
	s := openTestStore(t)
	mustApply(t, s, plan(adoptCmd("c1", "mk-1", "sid-t"), true, targetOrigin("sid-t")))
	other := plan(relCmd("c2", team.CommandRelease, "mk-1"), false, nil)
	other.LeadHostID = "host-B"
	wrongTeam := relCmd("c3", team.CommandRelease, "mk-1")
	wrongTeam.TeamID = "team-other"
	for name, p := range map[string]CommandPlan{
		"another lead host": other,
		"another team":      plan(wrongTeam, false, nil),
		"unknown mk":        plan(relCmd("c4", team.CommandRelease, "mk-nope"), false, nil),
	} {
		res := mustApply(t, s, p)
		if res.Status != http.StatusConflict || refusalCode(t, res) != team.ErrCommandNotYourMember {
			t.Fatalf("%s: %d %s", name, res.Status, res.Body)
		}
	}
	if row, _, _ := s.RemoteMember("mk-1"); row.State != remoteActive {
		t.Fatalf("row = %+v", row)
	}
}

// end and lead_moved are team-level: no mk, every live row of the team from that lead host.
func TestApplyEnd_EveryLiveRowOfTheTeamFromThatHost(t *testing.T) {
	s := openTestStore(t)
	mustApply(t, s, plan(adoptCmd("c1", "mk-1", "sid-1"), true, targetOrigin("sid-1")))
	mustApply(t, s, plan(adoptCmd("c2", "mk-2", "sid-2"), true, targetOrigin("sid-2")))
	mustApply(t, s, plan(adoptCmd("c3", "mk-3", "sid-3"), true, targetOrigin("sid-3")))
	mustApply(t, s, plan(relCmd("c4", team.CommandRelease, "mk-3"), false, nil))
	elsewhere := adoptCmd("c5", "mk-4", "sid-4")
	elsewhere.TeamID = "team-other"
	mustApply(t, s, plan(elsewhere, true, targetOrigin("sid-4")))

	end := relCmd("c6", team.CommandEnd, "")
	res := mustApply(t, s, plan(end, false, nil))
	if res.Status != http.StatusOK {
		t.Fatalf("end = %d %s", res.Status, res.Body)
	}
	for mk, want := range map[string]string{"mk-1": remoteEnded, "mk-2": remoteEnded, "mk-3": remoteReleased, "mk-4": remoteActive} {
		if row, _, _ := s.RemoteMember(mk); row.State != want {
			t.Fatalf("%s = %s, want %s", mk, row.State, want)
		}
	}
	if n := noticesOf(t, s, "mk-1"); len(n) != 2 || n[1].Kind != noticeTeamEnded {
		t.Fatalf("notices = %+v", n)
	}
	// A team with no live rows is not an error: the end is simply already true.
	if res := mustApply(t, s, plan(relCmd("c7", team.CommandEnd, ""), false, nil)); res.Status != http.StatusOK {
		t.Fatalf("second end = %d %s", res.Status, res.Body)
	}
}

func TestApplyLeadMoved_ReplacesTheLeadFieldsOfLiveRows(t *testing.T) {
	s := openTestStore(t)
	mustApply(t, s, plan(adoptCmd("c1", "mk-1", "sid-1"), true, targetOrigin("sid-1")))
	mustApply(t, s, plan(adoptCmd("c2", "mk-2", "sid-2"), true, targetOrigin("sid-2")))
	mustApply(t, s, plan(relCmd("c3", team.CommandRelease, "mk-2"), false, nil))

	mv := relCmd("c4", team.CommandLeadMoved, "")
	mv.LeadSessionID, mv.LeadRef = "lead-new", "_lead02"
	mv.Lead = team.TeamLead{SessionID: "lead-new", Ref: "_lead02", Title: "lead2", Address: "lead/y [lead02]", PID: 9, ProcStart: "ps9"}
	if res := mustApply(t, s, plan(mv, false, nil)); res.Status != http.StatusOK {
		t.Fatalf("lead_moved = %d %s", res.Status, res.Body)
	}
	row, _, _ := s.RemoteMember("mk-1")
	if row.State != remoteActive || row.LeadSessionID != "lead-new" || row.LeadRef != "_lead02" || row.LeadAddress != "lead/y [lead02]" || row.LeadPID != 9 || row.LeadProcStart != "ps9" {
		t.Fatalf("live row = %+v", row)
	}
	if n := noticesOf(t, s, "mk-1"); len(n) != 2 || n[1].Kind != noticeHandover {
		t.Fatalf("notices = %+v", n)
	}
	if row, _, _ := s.RemoteMember("mk-2"); row.LeadSessionID != "lead-sid" || row.State != remoteReleased {
		t.Fatalf("a released row was touched: %+v", row)
	}
}

// §11 crash cut: the row, its owed notice and the log entry are one transaction. A failure before the log
// insert leaves nothing; the retry then applies normally (mutation: notice or log written outside the tx → red).
func TestApplyCommand_IsOneTransaction(t *testing.T) {
	s := openTestStore(t)
	s.failBeforeCommandLog = func() error { return errors.New("injected crash") }
	p := plan(adoptCmd("c1", "mk-1", "sid-t"), true, targetOrigin("sid-t"))
	if _, err := s.ApplyTeamCommand(p); err == nil {
		t.Fatal("injected failure was swallowed")
	}
	if _, ok, _ := s.RemoteMember("mk-1"); ok {
		t.Fatal("the row survived a rolled-back apply")
	}
	if n := noticesOf(t, s, "mk-1"); len(n) != 0 {
		t.Fatalf("notice survived: %+v", n)
	}
	s.failBeforeCommandLog = nil
	if res := mustApply(t, s, p); res.Replayed || res.Status != http.StatusOK {
		t.Fatalf("retry = %+v", res)
	}
}
