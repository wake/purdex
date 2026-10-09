// internal/module/team/commands_void_test.go
package teammod

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func voidCmd(id, commandID string) team.TeamCommand {
	c := adoptCmd(id, "", "")
	c.Kind, c.TargetRef, c.CommandID = team.CommandVoid, "", commandID
	return c
}

func voidState(t *testing.T, res CommandResult) string {
	t.Helper()
	var o struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(res.Body, &o); err != nil {
		t.Fatalf("body %s: %v", res.Body, err)
	}
	return o.State
}

// §3.3: an ack lost after M applied the adopt → the void undoes it (adopted row → released), owes the member the
// released notice, and a later copy of the adopt answers command_void, not the stored applied.
// Mutation gate: no void command → red.
func TestVoid_UndoesAnAppliedAdopt(t *testing.T) {
	s := openTestStore(t)
	adopt := plan(adoptCmd("c1", "c1", "sid-t"), true, targetOrigin("sid-t"))
	mustApply(t, s, adopt)

	res := mustApply(t, s, plan(voidCmd("v1", "c1"), false, nil))
	if res.Status != http.StatusOK || voidState(t, res) != "undone" {
		t.Fatalf("void = %d %s", res.Status, res.Body)
	}
	if row, _, _ := s.RemoteMember("c1"); row.State != remoteReleased {
		t.Fatalf("row = %+v", row)
	}
	if role, _ := s.SessionRole("sid-t"); role != sessionRoleNone {
		t.Fatalf("an undone adopt still holds role %s", role)
	}
	n := noticesOf(t, s, "c1")
	if len(n) != 2 || n[1].Kind != noticeReleased || n[1].CauseID != "v1" {
		t.Fatalf("notices = %+v", n)
	}
	// The voids table is read before the log: the late copy is void, whatever the log stored.
	late := mustApply(t, s, adopt)
	if late.Replayed || late.Status != http.StatusConflict || refusalCode(t, late) != team.ErrCommandVoided {
		t.Fatalf("late copy = %+v %s", late, late.Body)
	}
}

// A void that arrives before its command is recorded; the command then answers 409 command_void (void table
// first) and applies nothing.
func TestVoid_BeforeItsCommandMakesTheCommandVoid(t *testing.T) {
	s := openTestStore(t)
	res := mustApply(t, s, plan(voidCmd("v1", "c1"), false, nil))
	if res.Status != http.StatusOK || voidState(t, res) != "recorded" {
		t.Fatalf("void = %d %s", res.Status, res.Body)
	}
	late := mustApply(t, s, plan(adoptCmd("c1", "c1", "sid-t"), true, targetOrigin("sid-t")))
	if late.Status != http.StatusConflict || refusalCode(t, late) != team.ErrCommandVoided {
		t.Fatalf("late adopt = %d %s", late.Status, late.Body)
	}
	if _, ok, _ := s.RemoteMember("c1"); ok {
		t.Fatal("a voided adopt stored a row")
	}
	if role, _ := s.SessionRole("sid-t"); role != sessionRoleNone {
		t.Fatalf("role = %s", role)
	}
	if n := noticesOf(t, s, "c1"); len(n) != 0 {
		t.Fatalf("a voided adopt owes a notice: %+v", n)
	}
	var logged int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM team_command_log WHERE id = 'c1'`).Scan(&logged)
	if logged != 0 {
		t.Fatal("command_void was logged as the command's outcome")
	}
}

// A void that arrived early names an adopt or a spawn; it must not swallow a release, end or lead_moved that
// happens to carry that id (codex R1): those keep queuing and are decided on their own.
func TestVoid_EarlyVoidOnlyVoidsAdoptAndSpawn(t *testing.T) {
	s := openTestStore(t)
	mustApply(t, s, plan(adoptCmd("c1", "c1", "sid-t"), true, targetOrigin("sid-t")))
	mustApply(t, s, plan(voidCmd("v1", "r1"), false, nil)) // r1 was never seen: recorded
	res := mustApply(t, s, plan(relCmd("r1", team.CommandRelease, "c1"), false, nil))
	if res.Status != http.StatusOK {
		t.Fatalf("release r1 = %d %s, want it applied despite the void table", res.Status, res.Body)
	}
	if row, _, _ := s.RemoteMember("c1"); row.State != remoteReleased {
		t.Fatalf("row = %+v", row)
	}
}

// The void is itself idempotent (rule 3), and scoped to the host that sent it.
func TestVoid_IdempotentAndScopedToItsHost(t *testing.T) {
	s := openTestStore(t)
	v := plan(voidCmd("v1", "c1"), false, nil)
	first := mustApply(t, s, v)
	again := mustApply(t, s, v)
	if !again.Replayed || string(again.Body) != string(first.Body) {
		t.Fatalf("replay = %+v", again)
	}
	other := voidCmd("v1", "c1")
	other.TeamName = "changed"
	if _, err := s.ApplyTeamCommand(plan(other, false, nil)); !errors.Is(err, ErrCommandIDConflict) {
		t.Fatalf("same void id, other content: %v", err)
	}
	// Host B's adopt with the same command id is not void: host A's void concerns A's command only.
	pb := plan(adoptCmd("c1", "c1", "sid-t"), true, targetOrigin("sid-t"))
	pb.LeadHostID = "host-B"
	if res := mustApply(t, s, pb); res.Status != http.StatusOK {
		t.Fatalf("host B's c1 = %d %s", res.Status, res.Body)
	}
}

// Nothing to undo is not an error: the member already left (release), or the command was refused. A void of a
// command that is no adopt/spawn is refused.
func TestVoid_NothingToUndoAndNotVoidable(t *testing.T) {
	s := openTestStore(t)
	mustApply(t, s, plan(adoptCmd("c1", "c1", "sid-t"), true, targetOrigin("sid-t")))
	mustApply(t, s, plan(relCmd("r1", team.CommandRelease, "c1"), false, nil))
	if res := mustApply(t, s, plan(voidCmd("v1", "c1"), false, nil)); res.Status != http.StatusOK || voidState(t, res) != "ok" {
		t.Fatalf("void after release = %d %s", res.Status, res.Body)
	}
	if n := noticesOf(t, s, "c1"); len(n) != 2 {
		t.Fatalf("a no-op void owed a notice: %+v", n)
	}

	// A refused adopt: nothing was applied, nothing to record — a replay of it still answers the refusal.
	refused := plan(adoptCmd("c2", "c2", "sid-x"), false, nil)
	mustApply(t, s, refused)
	if res := mustApply(t, s, plan(voidCmd("v2", "c2"), false, nil)); res.Status != http.StatusOK || voidState(t, res) != "ok" {
		t.Fatalf("void of a refusal = %d %s", res.Status, res.Body)
	}

	// The release is no adopt/spawn.
	res := mustApply(t, s, plan(voidCmd("v3", "r1"), false, nil))
	if res.Status != http.StatusConflict || refusalCode(t, res) != team.ErrCommandNotVoidable {
		t.Fatalf("void of a release = %d %s", res.Status, res.Body)
	}
}

// One transaction: the undo, its owed notice, the voids row and the log entry stand or fall together.
func TestVoid_IsOneTransaction(t *testing.T) {
	s := openTestStore(t)
	mustApply(t, s, plan(adoptCmd("c1", "c1", "sid-t"), true, targetOrigin("sid-t")))
	s.failBeforeCommandLog = func() error { return errors.New("injected crash") }
	v := plan(voidCmd("v1", "c1"), false, nil)
	if _, err := s.ApplyTeamCommand(v); err == nil {
		t.Fatal("injected failure was swallowed")
	}
	if row, _, _ := s.RemoteMember("c1"); row.State != remoteActive {
		t.Fatalf("the undo survived a rolled-back apply: %+v", row)
	}
	var voids int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM team_command_voids`).Scan(&voids)
	if voids != 0 || len(noticesOf(t, s, "c1")) != 1 {
		t.Fatalf("voids=%d notices=%+v", voids, noticesOf(t, s, "c1"))
	}
	s.failBeforeCommandLog = nil
	if res := mustApply(t, s, v); res.Replayed || voidState(t, res) != "undone" {
		t.Fatalf("retry = %+v %s", res, res.Body)
	}
}
