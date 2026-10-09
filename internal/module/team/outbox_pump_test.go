package teammod

import (
	"context"
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

// L's commands outbox pump (cross-host team spec §3.1 rules 1, 6, 7, §3.2; plan X3a-2), driven by hand with the fixture's
// clock against a scripted HostCaller.

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

// pumpFixture is the X3a-1 fixture with the commands outbox wired to a fake caller and a pump nobody runs.
func pumpFixture(t *testing.T) (*fixture, *fakeHostCaller, *recordingOutcomes) {
	t.Helper()
	f, out := cmdFixture(t)
	fc := &fakeHostCaller{}
	f.m.cmdCaller, f.m.outcomes = fc, out
	ob := &commandOutbox{s: f.m.store, out: out, now: f.m.now, unpair: f.m.unpairHost, onChange: f.m.rosterChanged}
	f.m.cmdPump = newOutboxPump("commands", fc, ob, f.m.now, f.m.logf, f.m.stopCtx, &f.m.sweepWG)
	return f, fc, out
}

func TestPumpBackoff_ThirtySecondsDoublingToTenMinutes(t *testing.T) {
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 10 * time.Minute, 10 * time.Minute}
	for i, w := range want {
		if got := pumpBackoff(i + 1); got != w {
			t.Errorf("backoff(%d) = %s, want %s", i+1, got, w)
		}
	}
}

func TestPump_FirstAttemptRightAfterTheKickAndOutcomeApplied(t *testing.T) {
	f, fc, out := pumpFixture(t)
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
	f, fc, _ := pumpFixture(t)
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
	f, fc, _ := pumpFixture(t)
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

// A permanent refusal (and wrong_host) settles the entry with the refusal as the outcome, and the next one goes at once.
func TestPump_APermanentRefusalSettlesAndTheNextGoes(t *testing.T) {
	f, fc, out := pumpFixture(t)
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
	f, fc, _ := pumpFixture(t)
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
	f, fc, _ := pumpFixture(t)
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
	f, fc, _ := pumpFixture(t)
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
	f, _, _ := pumpFixture(t)
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

func TestCapability_AnUnsupportedKindIsRefusedAndNeverQueued(t *testing.T) {
	f, fc, _ := pumpFixture(t)
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

// §3.2: removing the peer entry is final on this side; the tick finds a host that is not paired any more.
func TestUnpairing_TheTickEndsWhatTheMissingPairingLeaves(t *testing.T) {
	f, fc, _ := pumpFixture(t)
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

// Rule 6 + codex R1 of X3a: the next look at a 401 is never later than the end of its 10 minutes, so the escalation is on
// time rather than at the next doubling step after it (0, 30, 90, 210, 450, 930 s would have judged at 15.5 minutes).
func TestPump_A401IsJudgedAtTenMinutesNotAtTheNextDoubling(t *testing.T) {
	f, fc, _ := pumpFixture(t)
	fc.script = func(string, map[string]any) peersmod.CallResult {
		return peersmod.CallResult{Class: peersmod.ClassUnauthorized, Status: 401}
	}
	f.remoteRow("op-r", "hostM", "mk1", rowActive)
	f.enqueue(f.cmd("c1", CmdRelease, "hostM", "mk1"))
	start := f.clock.Load()
	for i := 0; i < 20 && f.cmdState("c1").State == cmdPending; i++ {
		f.m.cmdPump.drain("hostM")
		if c := f.cmdState("c1"); c.State == cmdPending {
			f.clock.Store(c.NextAt)
		}
	}
	if f.cmdState("c1").State != cmdDropped {
		t.Fatal("never judged")
	}
	if got := f.clock.Load() - start; got != 10*60_000 {
		t.Fatalf("judged %d ms after the first 401, want exactly 10 minutes", got)
	}
}

// The void command that expiry queues is kicked at once (the pump runs on the sweeper's tick, not at the next backoff).
func TestPump_ExpiryKicksThePump(t *testing.T) {
	f, _, _ := pumpFixture(t)
	f.enqueue(f.cmd("c1", CmdAdopt, "hostM", "mk1"))
	f.clock.Add(commandExpiryMS)
	f.m.expireCommands()
	select {
	case <-f.m.cmdPump.sig:
	default:
		t.Fatal("expiry queued a void without kicking the pump")
	}
}
