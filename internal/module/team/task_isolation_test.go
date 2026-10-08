package teammod

import (
	"net/http"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// The isolation matrix (plan T-1b1, review finding 13). Whatever a caller may
// not see answers EXACTLY what an id that never existed answers: the same
// status, code and detail text, for every verb, so existence cannot be told
// apart. Mutation gates: look a task up without the team id → the foreign
// team and malformed rows red; key ownership by ref → the relay test red;
// answer a member's foreign task with 403 / not_task_owner → every member row
// red; skip the owner check on GET /{id} → the member rows red.
func TestTasks_Isolation(t *testing.T) {
	w := newTaskWorld(t)
	w.mustTask(leadInbox, w.ma.Ref, "a task", nil)                            // 000000-1, owner ma
	b := w.mustTask(leadInbox, w.mb.Ref, "b task", nil)                       // 000000-2, owner mb
	w.mustTask(lead2, w.mx.Ref, "team two's own task", nil)                   // 000000-1 of team 2: the same display id
	w.mustTask(leadB, w.my.Ref, "team B's task", nil)                         // abcdef-1
	moved := w.mustTask(leadInbox, w.ma.Ref, "handed over", nil)              // 000000-3, ma then mb
	if code, _, e := w.reassign(leadInbox, moved.ID, w.mb.Ref); code != 200 { // (4) ma lost it
		t.Fatalf("reassign: %d %+v", code, e)
	}

	// What a nonexistent id answers, per verb.
	miss := "000000-999"
	type answer struct {
		status int
		code   string
		detail string
	}
	verbs := map[string]func(inbox, id string) (int, team.APIError){
		"show": func(inbox, id string) (int, team.APIError) {
			c, _, e := w.showTask(inbox, id)
			return c, e
		},
		"status": func(inbox, id string) (int, team.APIError) {
			c, _, e := w.setStatus(inbox, id, team.TaskInProgress)
			return c, e
		},
		"reassign": func(inbox, id string) (int, team.APIError) {
			c, _, e := w.reassign(inbox, id, w.mb.Ref)
			return c, e
		},
	}
	want := map[string]answer{}
	for name, call := range verbs {
		code, e := call(leadInbox, miss)
		if code != http.StatusConflict || e.Error != team.ErrTaskNotFound || e.Detail == "" {
			t.Fatalf("%s of a nonexistent id = %d %+v, want 409 %s", name, code, e, team.ErrTaskNotFound)
		}
		want[name] = answer{code, e.Error, e.Detail}
	}

	cases := []struct {
		name   string
		inbox  string
		id     string
		isLead bool // lead-only verbs (reassign) are asked only of leads
	}{
		{"(1) another team's lead asks with this team's id", leadB, "000000-2", true},
		{"(1) another team's lead, the same prefix: its seq 2 does not exist", lead2, "000000-2", true},
		{"(2) member A, member B's task", maInbox, b.ID, false},
		{"(2) member B, member A's task", mbInbox, "000000-1", false},
		{"(3) a member of team A, team B's id", maInbox, "abcdef-1", false},
		{"(3) a member of team B, team A's id", myInbox, "000000-1", false},
		{"(3) a member of team 2 (same prefix), team A's seq 2", mxInbox, b.ID, false},
		{"(4) the member it was reassigned away from", maInbox, moved.ID, false},
		{"(8) malformed: no number", leadInbox, "abc", true},
		{"(8) malformed: x-1", leadInbox, "x-1", true},
		{"(8) malformed: zero", leadInbox, "000000-0", true},
		{"(8) malformed: leading zero", leadInbox, "000000-01", true},
		{"(8) malformed: negative", leadInbox, "000000--1", true},
		{"(8) malformed: plus sign", leadInbox, "000000-+1", true},
		{"(8) malformed: foreign prefix with a real seq", leadInbox, "deadbe-1", true},
		{"(8) malformed: bare minus", leadInbox, "-1", true},
		{"(8) malformed: trailing space", leadInbox, "000000-1%20", true},
		{"(8) malformed: member, no number", maInbox, "abc", false},
		{"(8) malformed: member, deadbe-1", maInbox, "deadbe-1", false},
	}
	for _, c := range cases {
		for name, call := range verbs {
			if name == "reassign" && !c.isLead {
				continue
			}
			t.Run(c.name+"/"+name, func(t *testing.T) {
				code, e := call(c.inbox, c.id)
				if got, w := (answer{code, e.Error, e.Detail}), want[name]; got != w {
					t.Fatalf("%s %s by %s = %+v, want exactly what a nonexistent id answers %+v", name, c.id, c.inbox, got, w)
				}
			})
		}
	}

	// Nothing above changed a task, and a team's own colliding id answers its own.
	for _, tc := range []struct {
		teamID  string
		seq     int
		owner   string
		status  team.TaskStatus
		subject string
	}{{uid(1), 1, "op-a", team.TaskPending, "a task"}, {uid(1), 2, "op-b", team.TaskPending, "b task"},
		{uid(1), 3, "op-b", team.TaskPending, "handed over"}, {uid(2), 1, "op-x", team.TaskPending, "team two's own task"}} {
		got := mustGetTask(t, w.m.store, tc.teamID, tc.seq)
		if got.OwnerKey != tc.owner || got.Status != tc.status || got.Subject != tc.subject {
			t.Fatalf("task %s/%d = %+v, want owner %s %s %q", tc.teamID, tc.seq, got, tc.owner, tc.status, tc.subject)
		}
	}
	if code, d, _ := w.showTask(lead2, "000000-1"); code != 200 || d.Task.Subject != "team two's own task" || d.Task.TeamID != uid(2) {
		t.Fatalf("team 2's lead asking its own 000000-1 = %d %+v", code, d.Task)
	}

	// (2) lists never find another member's task; (3) nor another team's.
	if _, l, _ := w.listTasks(maInbox, "&all=1"); len(l.Tasks) != 1 || l.Tasks[0].Subject != "a task" {
		t.Fatalf("ma's list = %+v, want only its own", taskIDs(l.Tasks))
	}
	if _, l, _ := w.listTasks(lead2, "&all=1"); len(l.Tasks) != 1 || l.Tasks[0].TeamID != uid(2) {
		t.Fatalf("team 2's list = %+v", taskIDs(l.Tasks))
	}
}

// (6) a killed member's tasks stay in the lead's list with the owner's state;
// (7) a released member's too, and the lead can hand them on. A task never
// disappears with its owner.
func TestTasks_KilledAndReleasedOwnersStayVisible(t *testing.T) {
	w := newTaskWorld(t)
	killed := w.mustTask(leadInbox, w.ma.Ref, "of a member that dies", nil)
	released := w.mustTask(leadInbox, w.mb.Ref, "of a member that leaves", nil)
	if err := w.m.store.SetMemberState("op-a", team.MemberKilled, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := w.m.store.db.Exec(`UPDATE team_members SET state = 'released' WHERE spawn_op = 'op-b'`); err != nil { // PL-1b writes it; no setter yet
		t.Fatal(err)
	}
	code, l, e := w.listTasks(leadInbox, "")
	if code != 200 || len(l.Tasks) != 2 {
		t.Fatalf("list = %d %+v %+v", code, l, e)
	}
	byID := map[string]team.Task{l.Tasks[0].ID: l.Tasks[0], l.Tasks[1].ID: l.Tasks[1]}
	if o := byID[killed.ID].Owner; o.State != "killed" || o.Ref != w.ma.Ref {
		t.Fatalf("killed owner = %+v", o)
	}
	if o := byID[released.ID].Owner; o.State != "released" || o.Ref != w.mb.Ref {
		t.Fatalf("released owner = %+v", o)
	}
	if code, d, _ := w.showTask(leadInbox, killed.ID); code != 200 || d.Task.Owner.State != "killed" {
		t.Fatalf("show killed = %d %+v", code, d.Task)
	}
	w.taskMember(uid(1), "op-c", "sid-mc", "/tmp/mc.sock")
	mc := ipeers.RefID("sid-mc")
	for _, id := range []string{killed.ID, released.ID} {
		if code, got, e := w.reassign(leadInbox, id, mc); code != 200 || got.Owner.Ref != mc || got.Owner.State != "active" {
			t.Fatalf("reassign %s = %d %+v %+v", id, code, got, e)
		}
	}
}

// A session that is neither a live team's lead nor its active member gets
// not_member on list, show and status (not an empty answer); on the lead-only
// verbs not_lead. A member whose team ended is nobody.
func TestTasks_NeitherLeadNorMemberIsNotMember(t *testing.T) {
	w := newTaskWorld(t)
	tk := w.mustTask(leadInbox, w.ma.Ref, "a", nil)
	for _, inbox := range []string{"/tmp/n.sock"} {
		code, _, e := w.listTasks(inbox, "")
		wantErr(t, "list", code, e, 409, team.ErrNotMember)
		code, _, e = w.showTask(inbox, tk.ID)
		wantErr(t, "show", code, e, 409, team.ErrNotMember)
		code, _, e = w.setStatus(inbox, tk.ID, team.TaskInProgress)
		wantErr(t, "status", code, e, 409, team.ErrNotMember)
		code, _, e = w.reassign(inbox, tk.ID, w.mb.Ref)
		wantErr(t, "reassign", code, e, 409, team.ErrNotLead)
	}
	code, _, e := w.listTasks("/tmp/99.sock", "")
	wantErr(t, "unknown origin", code, e, 400, team.ErrOriginUnknown)
	w.origins.setReadErr(true)
	code, _, e = w.listTasks(leadInbox, "")
	wantErr(t, "unreadable registry", code, e, 503, team.ErrNotReady)
	w.origins.setReadErr(false)
	if _, err := w.m.store.EndTeam(uid(1), "sid-1", team.TeamEndLeadGone, 9); err != nil {
		t.Fatal(err)
	}
	for _, inbox := range []string{leadInbox, maInbox} {
		code, _, e = w.listTasks(inbox, "")
		wantErr(t, "an ended team", code, e, 409, team.ErrNotMember)
	}
}

// (5) A task follows its member across a relay: it is keyed by the member
// row, not by a ref or session. The REAL cleared path (ReportRelay →
// moveTeamRoles) moves the member to a new session and ref; then the lead
// still names it by the OLD ref (lineage), the answer shows the NEW ref, and
// the new session fetches and starts the task while the old one is nobody.
// Mutation gate: key ownership by ref → red.
func TestTaskOwner_FollowsTheMemberAcrossARelay(t *testing.T) {
	w := newTaskWorld(t)
	oldRef := w.ma.Ref
	tk := w.mustTask(leadInbox, oldRef, "survives a relay", nil)
	other := w.mustTask(leadInbox, w.mb.Ref, "not ma's", nil)

	claimedOp(t, w.m.store, "op-relay", "sid-ma", oldRef)
	mustReport(t, w.m.store, "op-relay", RelayReport{State: team.RelayCleared, NewSessionID: "sid-ma2", NewRef: ipeers.RefID("sid-ma2"), At: 9})
	w.session("/tmp/ma2.sock", "sid-ma2")
	newRef := ipeers.RefID("sid-ma2")

	code, d, e := w.showTask(leadInbox, tk.ID)
	if code != 200 || d.Task.Owner.Ref != newRef || d.Task.Owner.State != "active" || d.Task.Owner.Address != "mlab/"+newRef {
		t.Fatalf("show after the relay = %d %+v %+v, want owner %s", code, d.Task.Owner, e, newRef)
	}
	if _, l, _ := w.listTasks(leadInbox, "&member="+oldRef); len(l.Tasks) != 1 || l.Tasks[0].ID != tk.ID { // lineage
		t.Fatalf("list by the old ref = %v", taskIDs(l.Tasks))
	}
	if _, l, _ := w.listTasks("/tmp/ma2.sock", ""); len(l.Tasks) != 1 || l.Tasks[0].ID != tk.ID || l.Tasks[0].Owner.Ref != newRef {
		t.Fatalf("the new session's list = %+v", l.Tasks)
	}
	if code, got, e := w.setStatus("/tmp/ma2.sock", tk.ID, team.TaskInProgress); code != 200 || got.Status != team.TaskInProgress {
		t.Fatalf("the new session starts it: %d %+v %+v", code, got, e)
	}
	if code, _, e := w.showTask("/tmp/ma2.sock", other.ID); code != 409 || e.Error != team.ErrTaskNotFound {
		t.Fatalf("the new session asks mb's task: %d %+v", code, e)
	}
	if code, _, e := w.showTask(maInbox, tk.ID); code != 409 || e.Error != team.ErrNotMember {
		t.Fatalf("the old session is nobody now: %d %+v", code, e)
	}
	if code, got, e := w.reassign(leadInbox, tk.ID, oldRef); code != 200 || got.Owner.Ref != newRef { // handing it to "itself" by the old ref
		t.Fatalf("reassign by the old ref = %d %+v %+v", code, got, e)
	}
}
