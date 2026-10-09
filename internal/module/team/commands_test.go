package teammod

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
)

// L's commands outbox (cross-host team spec §3.1, §3.2, §3.3, §11; plan X3a). The pump is driven by hand (drain), with the
// fixture's clock, against a scripted HostCaller.

type fakeCall struct {
	Host, Path string
	Body       json.RawMessage
}

type fakeHostCaller struct {
	mu      sync.Mutex
	calls   []fakeCall
	script  func(host string, body map[string]any) peersmod.CallResult
	paired  map[string]bool
	caps    map[string]ipeers.TeamCaps
	capsErr error
}

func (f *fakeHostCaller) Call(_ context.Context, host, path string, body any) peersmod.CallResult {
	raw, _ := json.Marshal(body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{host, path, raw})
	script := f.script
	f.mu.Unlock()
	if script == nil {
		return peersmod.CallResult{Class: peersmod.ClassDone, Body: json.RawMessage(fmt.Sprintf(`{"id":%q,"host_id":%q,"outcome":{"state":"ok"}}`, m["id"], host))}
	}
	return script(host, m)
}
func (f *fakeHostCaller) Paired(h string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.paired == nil || f.paired[h]
}
func (f *fakeHostCaller) TeamCaps(_ context.Context, h string) (ipeers.TeamCaps, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.capsErr != nil {
		return ipeers.TeamCaps{}, f.capsErr
	}
	return f.caps[h], nil
}
func (f *fakeHostCaller) sent() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeCall(nil), f.calls...)
}

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

// cmdFixture is the fixture with the commands outbox wired to a fake caller and a pump nobody runs.
func cmdFixture(t *testing.T) (*fixture, *fakeHostCaller, *recordingOutcomes) {
	t.Helper()
	f := newFixture(t)
	f.approveLead(uid(1))
	fc := &fakeHostCaller{}
	out := &recordingOutcomes{}
	f.m.cmdCaller, f.m.outcomes = fc, out
	ob := &commandOutbox{s: f.m.store, out: out, now: f.m.now, unpair: f.m.unpairHost, onChange: f.m.rosterChanged}
	f.m.cmdPump = newOutboxPump("commands", fc, ob, f.m.now, f.m.logf, f.m.stopCtx, &f.m.sweepWG)
	return f, fc, out
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

func TestPumpBackoff_ThirtySecondsDoublingToTenMinutes(t *testing.T) {
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 10 * time.Minute, 10 * time.Minute}
	for i, w := range want {
		if got := pumpBackoff(i + 1); got != w {
			t.Errorf("backoff(%d) = %s, want %s", i+1, got, w)
		}
	}
}

// Crash cut 1 (spec §11): the command is written in the cause's transaction. Mutation: enqueue after the commit → the
// rollback case leaves a command → red.
func TestCommands_EnqueueIsInTheCausesTransaction(t *testing.T) {
	f, _, _ := cmdFixture(t)
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
	wrongHost := f.cmd("c2", CmdRelease, "hostM", "mk1")
	wrongHost.HostID = "hostZ" // the body names hostM
	if err := f.m.store.EnqueueCommand(tx2, wrongHost, 1); err == nil {
		t.Fatal("a body that does not name the command's host was accepted")
	}
}

func TestPump_FirstAttemptRightAfterTheKickAndOutcomeApplied(t *testing.T) {
	f, fc, out := cmdFixture(t)
	f.m.sweepWG.Add(1)
	go f.m.cmdPump.run()
	f.enqueue(f.cmd("c1", CmdRelease, "hostM", "mk1"))
	f.m.kickCommands()
	waitFor(t, func() bool { return f.cmdState("c1").State == cmdDone })
	if calls := fc.sent(); len(calls) != 1 || calls[0].Host != "hostM" || calls[0].Path != commandsPath {
		t.Fatalf("calls = %+v", calls)
	}
	if len(out.seen) != 1 || out.seen[0] != "c1:done:" {
		t.Fatalf("outcomes = %v", out.seen)
	}
	if len(f.cmdState("c1").Outcome) == 0 {
		t.Fatal("the answer was not stored")
	}
}

// FIFO per host: a stuck head blocks its own host only.
func TestPump_FifoPerHostAndAHostDoesNotBlockAnother(t *testing.T) {
	f, fc, _ := cmdFixture(t)
	fc.script = func(host string, body map[string]any) peersmod.CallResult {
		if host == "hostA" {
			return peersmod.CallResult{Class: peersmod.ClassTransient}
		}
		return peersmod.CallResult{Class: peersmod.ClassDone, Body: json.RawMessage(`{"host_id":"hostB"}`)}
	}
	f.enqueue(f.cmd("a1", CmdRelease, "hostA", "m"))
	f.enqueue(f.cmd("a2", CmdRelease, "hostA", "m"))
	f.enqueue(f.cmd("b1", CmdRelease, "hostB", "m"))
	f.m.cmdPump.drain("hostA")
	f.m.cmdPump.drain("hostB")
	if f.cmdState("b1").State != cmdDone {
		t.Fatal("hostB waited for hostA")
	}
	if f.cmdState("a1").State != cmdPending || f.cmdState("a2").State != cmdPending {
		t.Fatal("hostA's entries left the queue")
	}
	for _, c := range fc.sent() {
		if c.Host == "hostA" {
			var b map[string]any
			_ = json.Unmarshal(c.Body, &b)
			if b["id"] != "a1" {
				t.Fatalf("hostA was sent %v before its head was done", b["id"])
			}
		}
	}
}

func TestPump_TransientBacksOffThirtySecondsThenDoubles(t *testing.T) {
	f, fc, _ := cmdFixture(t)
	fc.script = func(string, map[string]any) peersmod.CallResult {
		return peersmod.CallResult{Class: peersmod.ClassTransient}
	}
	f.enqueue(f.cmd("c1", CmdRelease, "hostM", "m"))
	f.m.cmdPump.drain("hostM")
	c := f.cmdState("c1")
	if c.Attempts != 1 || c.NextAt != f.clock.Load()+30_000 {
		t.Fatalf("after one failure: attempts=%d next_at=%d (now %d)", c.Attempts, c.NextAt, f.clock.Load())
	}
	f.m.cmdPump.drain("hostM") // not yet due
	if len(fc.sent()) != 1 {
		t.Fatalf("%d calls before the backoff ended, want 1", len(fc.sent()))
	}
	f.clock.Add(30_000)
	f.m.cmdPump.drain("hostM")
	c = f.cmdState("c1")
	if c.Attempts != 2 || c.NextAt != f.clock.Load()+60_000 {
		t.Fatalf("after two failures: attempts=%d next_at=%d", c.Attempts, c.NextAt)
	}
}

// A done answer and its outcome are ONE transaction. Mutation: mark done first, apply after → a failing apply leaves the
// command done → red.
func TestPump_DoneAndOutcomeAreOneTransaction(t *testing.T) {
	f, fc, out := cmdFixture(t)
	out.fail = fmt.Errorf("apply failed")
	f.enqueue(f.cmd("c1", CmdRelease, "hostM", "m"))
	f.m.cmdPump.drain("hostM")
	if f.cmdState("c1").State != cmdPending {
		t.Fatal("a command whose outcome could not be applied was marked done")
	}
	out.fail = nil
	f.m.cmdPump.drain("hostM") // sent again; the receiver answers its stored outcome
	if f.cmdState("c1").State != cmdDone || len(fc.sent()) != 2 {
		t.Fatalf("state=%s calls=%d", f.cmdState("c1").State, len(fc.sent()))
	}
}

// A permanent refusal (and wrong_host) settles the entry with the refusal as the outcome, and the next one goes at once.
func TestPump_APermanentRefusalSettlesAndTheNextGoes(t *testing.T) {
	f, fc, out := cmdFixture(t)
	fc.script = func(host string, body map[string]any) peersmod.CallResult {
		switch body["id"] {
		case "c1":
			return peersmod.CallResult{Class: peersmod.ClassRefused, Code: "unsupported_kind"}
		case "c2":
			return peersmod.CallResult{Class: peersmod.ClassWrongHost, Code: "wrong_host"}
		}
		return peersmod.CallResult{Class: peersmod.ClassDone, Body: json.RawMessage(`{"host_id":"hostM"}`)}
	}
	for _, id := range []string{"c1", "c2", "c3"} {
		f.enqueue(f.cmd(id, CmdRelease, "hostM", "m"))
	}
	f.m.cmdPump.drain("hostM")
	for _, id := range []string{"c1", "c2", "c3"} {
		if f.cmdState(id).State != cmdDone {
			t.Fatalf("%s = %s, want done", id, f.cmdState(id).State)
		}
	}
	if len(out.seen) != 3 || out.seen[0] != "c1:refused:unsupported_kind" || out.seen[1] != "c2:wrong_host:wrong_host" {
		t.Fatalf("outcomes = %v", out.seen)
	}
	var o map[string]string
	_ = json.Unmarshal(f.cmdState("c1").Outcome, &o)
	if o["refused"] != "unsupported_kind" {
		t.Fatalf("stored outcome = %s", f.cmdState("c1").Outcome)
	}
}

// 404 / a non-JSON 403 (the route is missing) blocks the host's queue and is retried: nothing else could apply either.
func TestPump_UnsupportedBlocksTheQueue(t *testing.T) {
	f, fc, _ := cmdFixture(t)
	fc.script = func(string, map[string]any) peersmod.CallResult {
		return peersmod.CallResult{Class: peersmod.ClassUnsupported}
	}
	f.enqueue(f.cmd("c1", CmdRelease, "hostM", "m"))
	f.enqueue(f.cmd("c2", CmdRelease, "hostM", "m"))
	f.m.cmdPump.drain("hostM")
	if f.cmdState("c1").State != cmdPending || f.cmdState("c2").State != cmdPending || len(fc.sent()) != 1 || f.cmdState("c1").Attempts != 1 {
		t.Fatalf("c1=%s c2=%s calls=%d attempts=%d", f.cmdState("c1").State, f.cmdState("c2").State, len(fc.sent()), f.cmdState("c1").Attempts)
	}
}

// Rule 6, 401: transient for 10 minutes from the first one, then unpaired_by_peer and the clean-up. Mutation: treat 401 as
// transient forever → the queue head is never dropped → red.
func TestPump_A401ForTenMinutesIsUnpairedByPeer(t *testing.T) {
	f, fc, _ := cmdFixture(t)
	fc.script = func(string, map[string]any) peersmod.CallResult {
		return peersmod.CallResult{Class: peersmod.ClassUnauthorized, Status: 401}
	}
	f.remoteRow("op-r", "hostM", "mk1", rowActive)
	f.remoteRow("op-done", "hostM", "mk2", "killed")
	f.enqueue(f.cmd("c1", CmdRelease, "hostM", "mk1"))
	f.m.cmdPump.drain("hostM")
	if c := f.cmdState("c1"); c.State != cmdPending || c.First401At != f.clock.Load() {
		t.Fatalf("first 401: state=%s first_401_at=%d", c.State, c.First401At)
	}
	f.clock.Add(9 * 60_000)
	f.m.cmdPump.drain("hostM")
	if f.cmdState("c1").State != cmdPending {
		t.Fatal("unpaired_by_peer before 10 minutes")
	}
	f.clock.Add(61_000)
	f.m.cmdPump.drain("hostM")
	if f.cmdState("c1").State != cmdDropped {
		t.Fatalf("after 10 minutes of 401: %s, want dropped", f.cmdState("c1").State)
	}
	if st, why := f.memberRowState("op-r"); st != rowGone || why != "unpaired_by_peer" {
		t.Fatalf("live remote row = %s/%s, want gone/unpaired_by_peer", st, why)
	}
	if st, _ := f.memberRowState("op-done"); st != "killed" {
		t.Fatalf("a terminal row was rewritten: %s", st)
	}
}

// Any other answer ends a 401 run: the 10 minutes count from the first of a CONTINUOUS run.
func TestPump_ANon401AnswerResetsTheRun(t *testing.T) {
	f, fc, _ := cmdFixture(t)
	n := 0
	fc.script = func(string, map[string]any) peersmod.CallResult {
		n++
		if n == 2 {
			return peersmod.CallResult{Class: peersmod.ClassTransient}
		}
		return peersmod.CallResult{Class: peersmod.ClassUnauthorized, Status: 401}
	}
	f.enqueue(f.cmd("c1", CmdRelease, "hostM", "m"))
	f.m.cmdPump.drain("hostM") // 401: run starts
	f.clock.Add(35_000)
	f.m.cmdPump.drain("hostM") // transient: run cleared
	if f.cmdState("c1").First401At != 0 {
		t.Fatal("a non-401 answer left the 401 run open")
	}
}

// Rule 1 / §11 Identity: by host id, never by alias. The alias is re-created for host B; an entry for host A is never
// delivered to B, and the host is unpaired.
func TestPump_AnEntryForHostAIsNeverDeliveredToHostB(t *testing.T) {
	f, _, _ := cmdFixture(t)
	var hits int
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srvB.Close()
	hosts := []config.PeerHost{{Alias: "m", URL: srvB.URL, HostID: "hostB", Token: "tok"}} // the alias now names host B
	real := peersmod.NewHostCaller(func() []config.PeerHost { return hosts }, nil)
	f.m.cmdCaller = real
	ob := &commandOutbox{s: f.m.store, out: noOutcomes{}, now: f.m.now, unpair: f.m.unpairHost}
	f.m.cmdPump = newOutboxPump("commands", real, ob, f.m.now, f.m.logf, f.m.stopCtx, &f.m.sweepWG)
	f.remoteRow("op-r", "hostA", "mk1", rowActive)
	f.enqueue(f.cmd("c1", CmdRelease, "hostA", "mk1"))
	f.m.cmdPump.drain("hostA")
	if hits != 0 {
		t.Fatalf("host B received %d request(s) meant for host A", hits)
	}
	if f.cmdState("c1").State != cmdDropped {
		t.Fatalf("command = %s, want dropped (host A is unpaired)", f.cmdState("c1").State)
	}
	if st, why := f.memberRowState("op-r"); st != rowGone || why != "unpaired" {
		t.Fatalf("row = %s/%s", st, why)
	}
}

// §3.2: removing the peer entry is final on this side; the tick finds a host that is not paired any more.
func TestUnpairing_TheTickEndsWhatTheMissingPairingLeaves(t *testing.T) {
	f, fc, _ := cmdFixture(t)
	fc.paired = map[string]bool{"hostM": true, "hostN": false}
	f.remoteRow("op-m", "hostM", "mk1", rowActive)
	f.remoteRow("op-n1", "hostN", "mk2", rowJoining)
	f.remoteRow("op-n2", "hostN", "mk3", rowActive)
	f.remoteRow("op-n3", "hostN", "mk4", rowReleasing)
	f.remoteRow("op-n4", "hostN", "mk5", "released")
	f.enqueue(f.cmd("c1", CmdAdopt, "hostN", "mk2"))
	f.m.scanUnpaired()
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
	f, _, _ := cmdFixture(t)
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
	f, _, out := cmdFixture(t)
	f.enqueue(f.cmd("c1", CmdSpawn, "hostM", "mk1"))
	f.clock.Add(commandExpiryMS)
	f.m.expireCommands()
	ob := f.m.cmdPump.store.(*commandOutbox)
	c := f.cmdState("c1")
	if err := ob.Settle(outboxEntry{ID: c.ID, HostID: c.HostID}, peersmod.CallResult{Class: peersmod.ClassDone, Body: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if f.cmdState("c1").State != cmdVoid || len(out.seen) != 0 {
		t.Fatalf("state=%s outcomes=%v: a late answer resurrected a void command", f.cmdState("c1").State, out.seen)
	}
}

func TestCapability_AnUnsupportedKindIsRefusedAndNeverQueued(t *testing.T) {
	f, fc, _ := cmdFixture(t)
	fc.caps = map[string]ipeers.TeamCaps{
		"hostOld": {Kinds: []string{}},
		"hostOff": {Kinds: []string{"adopt", "release"}, AllowTeam: false},
		"hostOK":  {Kinds: []string{"adopt", "release"}, AllowTeam: true},
	}
	for _, c := range []struct{ host, kind, code string }{
		{"hostOld", CmdAdopt, "remote_unsupported"},
		{"hostOff", CmdAdopt, "host_not_allowed"},
		{"hostOK", CmdKill, "remote_unsupported"},
		{"hostOK", CmdAdopt, ""},
	} {
		err := f.m.checkRemoteKind(context.Background(), c.host, c.kind)
		var ce *CapError
		switch {
		case c.code == "" && err != nil:
			t.Errorf("%s/%s refused: %v", c.host, c.kind, err)
		case c.code != "" && (err == nil || !asCap(err, &ce) || ce.Code != c.code):
			t.Errorf("%s/%s = %v, want %s", c.host, c.kind, err, c.code)
		}
	}
	fc.capsErr = fmt.Errorf("HTTP 500")
	if err := f.m.checkRemoteKind(context.Background(), "hostOK", CmdAdopt); err == nil {
		t.Error("a host that could not be asked was accepted")
	}
	var n int
	_ = f.m.store.db.QueryRow(`SELECT COUNT(*) FROM team_commands`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d commands queued by a capability check", n)
	}
}

func asCap(err error, out **CapError) bool {
	ce, ok := err.(*CapError)
	*out = ce
	return ok
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
