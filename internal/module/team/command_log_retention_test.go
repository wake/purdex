// internal/module/team/command_log_retention_test.go
package teammod

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// #2265: team_command_log is pruned by age (30 days), batch by batch; team_command_voids is never touched. The receiver
// has NO age check of its own for adopt / spawn (only the lead host settles or voids them within 10 minutes), so the one
// record kept is an adopt whose member row is still active: a late void must still undo it.
//
// Replay results after a prune (what the tests below lock):
//   release / end / lead_moved / void  — an idempotent no-op: no row, notice or void-table change.
//   adopt (member row ended)            — pruned; a late void only records itself in the void table.
//   adopt (member row active)           — kept (even past 30 days); a late void still undoes it.
//   spawn                               — pruned after 30 days; premise: the lead host does not resend it (settled/voided
//                                         within 10 minutes) — the receiver has no age check to catch it.
// Mutation gates: prune that deletes the active adopt / touches the void table / ignores age → red.

const logDay = int64(24 * 60 * 60 * 1000)

func countRows(t *testing.T, s *Store, q string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func logIDs(t *testing.T, s *Store) map[string]bool {
	t.Helper()
	rows, err := s.db.Query(`SELECT id FROM team_command_log`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out[id] = true
	}
	return out
}

// seedLog applies one command of every kind (plan time 1000) and returns the ids by role.
func seedLog(t *testing.T, s *Store) {
	t.Helper()
	mustApply(t, s, plan(adoptCmd("c-active", "c-active", "sid-a"), true, targetOrigin("sid-a")))
	mustApply(t, s, plan(adoptCmd("c-ended", "c-ended", "sid-b"), true, targetOrigin("sid-b")))
	mustApply(t, s, plan(relCmd("c-release", team.CommandRelease, "c-ended"), false, nil))
	mustApply(t, s, plan(adoptCmd("c-refused", "c-refused", "sid-c"), false, nil)) // no consent: refused, no row
	other := relCmd("c-end", team.CommandEnd, "")
	other.TeamID = "team-other"
	mustApply(t, s, plan(other, false, nil))
	mv := relCmd("c-moved", team.CommandLeadMoved, "")
	mv.TeamID = "team-other"
	mv.LeadSessionID, mv.LeadRef = "lead-new", "_lead02"
	mv.Lead = team.TeamLead{SessionID: "lead-new", Ref: "_lead02", Address: "lead/y [lead02]", PID: 9, ProcStart: "ps9"}
	mustApply(t, s, plan(mv, false, nil))
	vd := relCmd("c-void", team.CommandVoid, "")
	vd.CommandID = "c-never-seen"
	mustApply(t, s, plan(vd, false, nil))
	if _, err := s.db.Exec(`INSERT INTO team_command_log (lead_host_id, id, kind, body_hash, status, outcome_json, at) VALUES (?, 'c-spawn', 'spawn', 'h', 200, '{}', 1000)`, leadHostA); err != nil {
		t.Fatal(err)
	}
}

func TestPruneCommandLog_ByAgeAndKind(t *testing.T) {
	s := openTestStore(t)
	seedLog(t, s)
	now := 1000 + 31*logDay
	// one record that is only 29 days old stays whatever it is
	if _, err := s.db.Exec(`UPDATE team_command_log SET at = ? WHERE id = 'c-moved'`, now-29*logDay); err != nil {
		t.Fatal(err)
	}
	voids := countRows(t, s, `SELECT COUNT(*) FROM team_command_voids`)

	if _, err := s.PruneCommandLog(now-30*logDay, 500); err != nil {
		t.Fatal(err)
	}
	got := logIDs(t, s)
	want := map[string]bool{"c-active": true, "c-moved": true} // the active adopt (its member row is live) and the young one
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("log after the prune = %v, want %v", got, want)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM team_command_voids`); n != voids || n == 0 {
		t.Fatalf("void table: %d rows, want the %d it had (and not empty)", n, voids)
	}
}

// (a) An adopt whose member row is still active is kept past 30 days, so a late void still undoes it.
func TestPruneCommandLog_ActiveAdoptKeepsItsRecordAndALateVoidUndoesIt(t *testing.T) {
	s := openTestStore(t)
	mustApply(t, s, plan(adoptCmd("c-active", "c-active", "sid-a"), true, targetOrigin("sid-a")))
	if _, err := s.PruneCommandLog(1000+400*logDay, 500); err != nil { // a year-old record
		t.Fatal(err)
	}
	if !logIDs(t, s)["c-active"] {
		t.Fatal("the record of an adopt whose member is active was pruned")
	}
	vd := relCmd("c-void", team.CommandVoid, "")
	vd.CommandID = "c-active"
	res := mustApply(t, s, plan(vd, false, nil))
	if res.Status != http.StatusOK || !bodyHas(res.Body, "state", "undone") {
		t.Fatalf("late void = %d %s, want undone", res.Status, res.Body)
	}
	if row, _, _ := s.RemoteMember("c-active"); row.State != remoteReleased {
		t.Fatalf("row = %+v, want released", row)
	}
}

// (b) An adopt whose member row has ended (or never existed) is pruned; a late void only records itself.
func TestPruneCommandLog_EndedAdoptIsPrunedAndALateVoidOnlyRecords(t *testing.T) {
	for _, name := range []string{"released", "refused"} {
		t.Run(name, func(t *testing.T) {
			s := openTestStore(t)
			target := "c-adopt"
			if name == "released" {
				mustApply(t, s, plan(adoptCmd(target, target, "sid-a"), true, targetOrigin("sid-a")))
				mustApply(t, s, plan(relCmd("c-rel", team.CommandRelease, target), false, nil))
			} else {
				mustApply(t, s, plan(adoptCmd(target, target, "sid-a"), false, nil))
			}
			if _, err := s.PruneCommandLog(1000+400*logDay, 500); err != nil {
				t.Fatal(err)
			}
			if logIDs(t, s)[target] {
				t.Fatal("the record of an adopt with no live member row was kept")
			}
			before := snapshot(t, s)
			vd := relCmd("c-void", team.CommandVoid, "")
			vd.CommandID = target
			res := mustApply(t, s, plan(vd, false, nil))
			if res.Status != http.StatusOK || !bodyHas(res.Body, "state", "recorded") {
				t.Fatalf("late void = %d %s", res.Status, res.Body)
			}
			after := snapshot(t, s)
			if after.members != before.members || after.notices != before.notices {
				t.Fatalf("a late void changed a member row or a notice: %+v → %+v", before, after)
			}
			if after.voids != before.voids+1 {
				t.Fatalf("void table: %d → %d, want the void recorded", before.voids, after.voids)
			}
		})
	}
}

// (c) release / end / lead_moved / void replayed after their records are pruned change nothing.
func TestPruneCommandLog_ReplayedControlCommandsAreNoOps(t *testing.T) {
	s := openTestStore(t)
	mustApply(t, s, plan(adoptCmd("c-live", "c-live", "sid-a"), true, targetOrigin("sid-a")))
	mustApply(t, s, plan(adoptCmd("c-gone", "c-gone", "sid-b"), true, targetOrigin("sid-b")))
	rel := relCmd("c-release", team.CommandRelease, "c-gone")
	mustApply(t, s, plan(rel, false, nil))
	end := relCmd("c-end", team.CommandEnd, "")
	end.TeamID = "team-other"
	mustApply(t, s, plan(end, false, nil))
	mv := relCmd("c-moved", team.CommandLeadMoved, "")
	mv.LeadSessionID, mv.LeadRef = "lead-new", "_lead02"
	mv.Lead = team.TeamLead{SessionID: "lead-new", Ref: "_lead02", Address: "lead/y [lead02]", PID: 9, ProcStart: "ps9"}
	mustApply(t, s, plan(mv, false, nil))
	vd := relCmd("c-void", team.CommandVoid, "")
	vd.CommandID = "c-never-seen"
	mustApply(t, s, plan(vd, false, nil))

	if _, err := s.PruneCommandLog(1000+400*logDay, 500); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"c-release", "c-end", "c-moved", "c-void", "c-gone"} {
		if logIDs(t, s)[id] {
			t.Fatalf("record %s was kept", id)
		}
	}
	before := snapshot(t, s)
	for _, c := range []team.TeamCommand{rel, end, mv, vd} {
		res, err := s.ApplyTeamCommand(plan(c, false, nil))
		if err != nil {
			t.Fatalf("replay of %s: %v", c.Kind, err)
		}
		t.Logf("replay of %s after the prune: %d %s", c.Kind, res.Status, res.Body)
	}
	if after := snapshot(t, s); after != before {
		t.Fatalf("a replay after the prune changed state: %+v → %+v", before, after)
	}
}

// The prune works in batches: no call deletes more than the batch, and repeating reaches the end.
func TestPruneCommandLog_InBatches(t *testing.T) {
	s := openTestStore(t)
	for i := 0; i < 1200; i++ {
		if _, err := s.db.Exec(`INSERT INTO team_command_log (lead_host_id, id, kind, body_hash, status, outcome_json, at) VALUES (?, ?, 'release', 'h', 200, '{}', 1000)`,
			leadHostA, fmt.Sprintf("old-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	total, calls := 0, 0
	for {
		n, err := s.PruneCommandLog(1000+31*logDay, 500)
		if err != nil {
			t.Fatal(err)
		}
		if n > 500 {
			t.Fatalf("a batch deleted %d rows, want at most 500", n)
		}
		total += n
		calls++
		if n == 0 {
			break
		}
		if calls > 10 {
			t.Fatal("the prune does not end")
		}
	}
	if total != 1200 || countRows(t, s, `SELECT COUNT(*) FROM team_command_log`) != 0 {
		t.Fatalf("deleted %d of 1200", total)
	}
}

// The module's sweep prunes everything past the retention, in batches until none is left, and nothing younger.
func TestSweep_PrunesTheCommandLogPastThirtyDays(t *testing.T) {
	f := newFixture(t)
	f.clock.Store(100 * logDay)
	for i := 0; i < 1300; i++ {
		at := int64(1) // far past the retention
		if i%100 == 0 {
			at = f.clock.Load() - 29*logDay // inside it
		}
		if _, err := f.m.store.db.Exec(`INSERT INTO team_command_log (lead_host_id, id, kind, body_hash, status, outcome_json, at) VALUES (?, ?, 'release', 'h', 200, '{}', ?)`,
			leadHostA, fmt.Sprintf("c-%d", i), at); err != nil {
			t.Fatal(err)
		}
	}
	f.m.pruneCommandLog()
	if n := countRows(t, f.m.store, `SELECT COUNT(*) FROM team_command_log`); n != 13 {
		t.Fatalf("%d records left, want the 13 younger than 30 days", n)
	}
}

type stateSnap struct {
	members        string
	notices, voids int
}

func snapshot(t *testing.T, s *Store) stateSnap {
	t.Helper()
	var members sql.NullString
	if err := s.db.QueryRow(`SELECT group_concat(mk || ':' || state || ':' || lead_session_id || ':' || lead_address, ',') FROM (SELECT * FROM remote_members ORDER BY mk)`).Scan(&members); err != nil {
		t.Fatal(err)
	}
	return stateSnap{members: members.String, notices: countRows(t, s, `SELECT COUNT(*) FROM remote_notices`),
		voids: countRows(t, s, `SELECT COUNT(*) FROM team_command_voids`)}
}

// bodyHas says whether the JSON object body has the string field key equal to want.
func bodyHas(body []byte, key, want string) bool {
	var m map[string]any
	return json.Unmarshal(body, &m) == nil && m[key] == want
}
