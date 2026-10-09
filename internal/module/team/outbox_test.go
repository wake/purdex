package teammod

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	peersmod "github.com/wake/purdex/internal/module/peers"
)

// L's commands outbox, the store half (cross-host team spec §3.1 rules 2–5, §3.2, §3.3, §11; plan X3a-1): no network, no
// pump. The answers a pump will bring are fed to SettleCommand by hand.

// recordingOutcomes is X3b-1's seam for these tests.
type recordingOutcomes struct {
	mu   sync.Mutex
	seen []string
	fail error
}

func (r *recordingOutcomes) ApplyOutcome(_ *sql.Tx, c commandRow, res peersmod.CallResult) error {
	if r.fail != nil {
		return r.fail
	}
	r.mu.Lock()
	r.seen = append(r.seen, c.ID+":"+string(res.Class)+":"+res.Code)
	r.mu.Unlock()
	return nil
}

// cmdFixture is the fixture with an approved lead team (uid(1)) and X3b-1's seam replaced by a recorder.
func cmdFixture(t *testing.T) (*fixture, *recordingOutcomes) {
	t.Helper()
	f := newFixture(t)
	f.approveLead(uid(1))
	out := &recordingOutcomes{}
	return f, out
}

func (f *fixture) cmd(id, kind, host, mk string) Command {
	body, _ := json.Marshal(map[string]any{"id": id, "kind": kind, "to_host_id": host, "team_id": uid(1), "mk": mk, "lead": map[string]any{"session_id": "sid-1"}})
	return Command{ID: id, Kind: kind, TeamID: uid(1), MK: mk, HostID: host, Body: body}
}

func (f *fixture) enqueue(c Command) {
	f.t.Helper()
	tx, err := f.m.store.db.Begin()
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.m.store.EnqueueCommand(tx, c, f.clock.Load()); err != nil {
		f.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) cmdState(id string) commandRow {
	f.t.Helper()
	c, ok, err := f.m.store.GetCommand(id)
	if err != nil || !ok {
		f.t.Fatalf("command %s: %v ok=%v", id, err, ok)
	}
	return c
}

// remoteRow seeds a remote member row (host hostID, state) of the fixture's team.
func (f *fixture) remoteRow(spawnOp, hostID, mk, state string) {
	f.t.Helper()
	m := newMember(spawnOp, uid(1), "sid-"+spawnOp, "_r"+spawnOp, f.clock.Load())
	m.HostID = hostID
	if err := f.m.store.InsertMember(m); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = ?, mk = ? WHERE spawn_op = ?`, state, mk, spawnOp); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) memberRowState(spawnOp string) (state, reason string) {
	f.t.Helper()
	if err := f.m.store.db.QueryRow(`SELECT state, end_reason FROM team_members WHERE spawn_op = ?`, spawnOp).Scan(&state, &reason); err != nil {
		f.t.Fatal(err)
	}
	return
}

// Crash cut 1 (spec §11): the command is written in the cause's transaction. Mutation: enqueue after the commit → the
// rollback case leaves a command → red.
func TestCommands_EnqueueIsInTheCausesTransaction(t *testing.T) {
	f, _ := cmdFixture(t)
	tx, _ := f.m.store.db.Begin()
	if err := f.m.store.EnqueueCommand(tx, f.cmd("c1", CmdRelease, "hostM", "mk1"), 1); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback() // the cause failed
	if _, ok, _ := f.m.store.GetCommand("c1"); ok {
		t.Fatal("a rolled-back cause left a command behind")
	}
	f.enqueue(f.cmd("c1", CmdRelease, "hostM", "mk1"))
	f.enqueue(f.cmd("c1", CmdRelease, "hostM", "mk1")) // a replay of the same command
	var n int
	_ = f.m.store.db.QueryRow(`SELECT COUNT(*) FROM team_commands`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d commands after a replay, want 1", n)
	}
	bad := f.cmd("c1", CmdRelease, "hostM", "mk1")
	bad.Body = json.RawMessage(`{"id":"c1","kind":"release","to_host_id":"hostM","other":1}`)
	tx2, _ := f.m.store.db.Begin()
	defer tx2.Rollback()
	if err := f.m.store.EnqueueCommand(tx2, bad, 1); err == nil {
		t.Fatal("the same id with another body was accepted")
	}
	// the stored routing columns must agree with the bytes that are sent
	for name, mut := range map[string]func(*Command){"team": func(c *Command) { c.TeamID = "other-team" }, "mk": func(c *Command) { c.MK = "other-mk" }} {
		c := f.cmd("c3", CmdRelease, "hostM", "mk1")
		mut(&c)
		if err := f.m.store.EnqueueCommand(tx2, c, 1); err == nil {
			t.Fatalf("a body whose %s differs from the command's was accepted", name)
		}
	}
	// a replay under the same id must agree in the persisted fields too
	f.enqueue(f.cmd("c4", CmdRelease, "hostM", "mk1"))
	other := f.cmd("c4", CmdRelease, "hostM", "mk1")
	other.Body = json.RawMessage(string(other.Body)) // same bytes …
	other.MK = "mk-other"                            // … another member key
	if err := f.m.store.EnqueueCommand(tx2, other, 1); err == nil {
		t.Fatal("a replay with another member key was accepted")
	}
	wrongHost := f.cmd("c2", CmdRelease, "hostM", "mk1")
	wrongHost.HostID = "hostZ" // the body names hostM
	if err := f.m.store.EnqueueCommand(tx2, wrongHost, 1); err == nil {
		t.Fatal("a body that does not name the command's host was accepted")
	}
}

// A done answer and its outcome are ONE transaction. Mutation: mark done first, apply after → a failing apply leaves the
// command done → red.
func TestSettle_DoneAndOutcomeAreOneTransaction(t *testing.T) {
	f, out := cmdFixture(t)
	out.fail = fmt.Errorf("apply failed")
	f.enqueue(f.cmd("c1", CmdRelease, "hostM", "m"))
	done := peersmod.CallResult{Class: peersmod.ClassDone, Body: json.RawMessage(`{"id":"c1","host_id":"hostM","outcome":{"state":"ok"}}`)}
	if _, err := f.m.store.SettleCommand("c1", done, f.clock.Load(), out); err == nil {
		t.Fatal("a failing outcome was not reported")
	}
	if f.cmdState("c1").State != cmdPending {
		t.Fatal("a command whose outcome could not be applied was marked done")
	}
	out.fail = nil
	if ok, err := f.m.store.SettleCommand("c1", done, f.clock.Load(), out); err != nil || !ok {
		t.Fatalf("second settle: %v %v", ok, err)
	}
	if c := f.cmdState("c1"); c.State != cmdDone || len(c.Outcome) == 0 {
		t.Fatalf("state=%s outcome=%s", c.State, c.Outcome)
	}
	if ok, _ := f.m.store.SettleCommand("c1", done, f.clock.Load(), out); ok || len(out.seen) != 1 {
		t.Fatalf("a replayed answer applied twice: %v", out.seen)
	}
}

// A permanent refusal (and wrong_host) is stored as the outcome and handed to the seam with its code.
func TestSettle_APermanentRefusalIsTheOutcome(t *testing.T) {
	f, out := cmdFixture(t)
	f.enqueue(f.cmd("c1", CmdRelease, "hostM", "m"))
	f.enqueue(f.cmd("c2", CmdKill, "hostM", "m"))
	f.m.store.SettleCommand("c1", peersmod.CallResult{Class: peersmod.ClassRefused, Code: "unsupported_kind"}, 1, out)
	f.m.store.SettleCommand("c2", peersmod.CallResult{Class: peersmod.ClassWrongHost, Code: "wrong_host"}, 1, out)
	if len(out.seen) != 2 || out.seen[0] != "c1:refused:unsupported_kind" || out.seen[1] != "c2:wrong_host:wrong_host" {
		t.Fatalf("outcomes = %v", out.seen)
	}
	var o map[string]string
	_ = json.Unmarshal(f.cmdState("c1").Outcome, &o)
	if o["refused"] != "unsupported_kind" || f.cmdState("c1").State != cmdDone {
		t.Fatalf("stored = %s state=%s", f.cmdState("c1").Outcome, f.cmdState("c1").State)
	}
}

// §3.2: removing the peer entry is final on this side; live remote rows of the host go gone, terminal rows stay, pending commands are dropped.
func TestUnpairing_EndsTheRelationOnThisSide(t *testing.T) {
	f, _ := cmdFixture(t)
	f.remoteRow("op-m", "hostM", "mk1", rowActive)
	f.remoteRow("op-n1", "hostN", "mk2", rowJoining)
	f.remoteRow("op-n2", "hostN", "mk3", rowActive)
	f.remoteRow("op-n3", "hostN", "mk4", rowReleasing)
	f.remoteRow("op-n4", "hostN", "mk5", "released")
	f.enqueue(f.cmd("c1", CmdAdopt, "hostN", "mk2"))
	if err := f.m.unpairHost("hostN", "unpaired"); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"op-n1", "op-n2", "op-n3"} {
		if st, why := f.memberRowState(op); st != rowGone || why != "unpaired" {
			t.Fatalf("%s = %s/%s, want gone/unpaired", op, st, why)
		}
	}
	if st, _ := f.memberRowState("op-n4"); st != "released" {
		t.Fatalf("a terminal row was rewritten: %s", st)
	}
	if st, _ := f.memberRowState("op-m"); st != rowActive {
		t.Fatalf("the other host's row = %s", st)
	}
	if f.cmdState("c1").State != cmdDropped {
		t.Fatalf("command = %s", f.cmdState("c1").State)
	}
}

// §3.3: a spawn or adopt not done within 10 minutes is void; a void command follows, and it never expires. Mutation: no
// void command → the last assertions red.
func TestExpiry_AnAdoptNotDoneInTenMinutesIsVoid(t *testing.T) {
	f, _ := cmdFixture(t)
	f.remoteRow("op-j", "hostM", "mk1", rowJoining)
	f.enqueue(f.cmd("c1", CmdAdopt, "hostM", "mk1"))
	f.enqueue(f.cmd("c2", CmdRelease, "hostM", "mk9"))
	f.clock.Add(commandExpiryMS - 1)
	f.m.expireCommands()
	if f.cmdState("c1").State != cmdPending {
		t.Fatal("voided before 10 minutes")
	}
	f.clock.Add(1)
	f.m.expireCommands()
	if f.cmdState("c1").State != cmdVoid {
		t.Fatalf("c1 = %s, want void", f.cmdState("c1").State)
	}
	if st, why := f.memberRowState("op-j"); st != rowFailed || why != "remote_unreachable" {
		t.Fatalf("row = %s/%s, want failed/remote_unreachable (the seat is free)", st, why)
	}
	if f.cmdState("c2").State != cmdPending {
		t.Fatal("a release expired")
	}
	var id, kind, body string
	if err := f.m.store.db.QueryRow(`SELECT id, kind, body_json FROM team_commands WHERE kind = 'void'`).Scan(&id, &kind, &body); err != nil {
		t.Fatalf("no void command was queued: %v", err)
	}
	var b map[string]any
	_ = json.Unmarshal([]byte(body), &b)
	if b["command_id"] != "c1" || b["to_host_id"] != "hostM" || b["mk"] != "mk1" || b["id"] == "c1" {
		t.Fatalf("void body = %s", body)
	}
	f.clock.Add(24 * 3600_000) // days later: release and void are still queued
	f.m.expireCommands()
	if f.cmdState("c2").State != cmdPending || f.cmdState(id).State != cmdPending {
		t.Fatal("release / void expired")
	}
	f.m.expireCommands() // a second look queues no second void
	var n int
	_ = f.m.store.db.QueryRow(`SELECT COUNT(*) FROM team_commands WHERE kind = 'void'`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d void commands, want 1", n)
	}
}

// A late answer for a command that was voided (or dropped) meanwhile changes nothing (rule 5).
func TestExpiry_ALateAnswerForAVoidCommandIsIgnored(t *testing.T) {
	f, out := cmdFixture(t)
	f.enqueue(f.cmd("c1", CmdSpawn, "hostM", "mk1"))
	f.clock.Add(commandExpiryMS)
	f.m.expireCommands()
	if ok, err := f.m.store.SettleCommand("c1", peersmod.CallResult{Class: peersmod.ClassDone, Body: json.RawMessage(`{}`)}, f.clock.Load(), out); err != nil || ok {
		t.Fatalf("settle of a void command = %v %v", ok, err)
	}
	if f.cmdState("c1").State != cmdVoid || len(out.seen) != 0 {
		t.Fatalf("state=%s outcomes=%v: a late answer resurrected a void command", f.cmdState("c1").State, out.seen)
	}
}

// The schema: `mk` is the spawn_op for every local row, whichever path inserted it, and the migration can run again.
func TestMigration_MkIsTheSpawnOpForLocalRows(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1))
	seedMember(t, f.m.store, "op-a", uid(1), "sid-a", f.clock.Load())
	var mk string
	if err := f.m.store.db.QueryRow(`SELECT mk FROM team_members WHERE spawn_op = 'op-a'`).Scan(&mk); err != nil || mk != "op-a" {
		t.Fatalf("mk = %q err=%v, want op-a", mk, err)
	}
	if err := migrateCrossHostL(f.m.store.db); err != nil {
		t.Fatalf("the migration is not repeatable: %v", err)
	}
}
