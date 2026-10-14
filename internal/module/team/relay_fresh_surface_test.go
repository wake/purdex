package teammod

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/team"
)

// ---- RT-3a2 T32: the surfaces of a fresh half ----

func (f *fixture) getFresh() team.RelayFreshView {
	f.t.Helper()
	code, body := f.do(http.MethodGet, team.RelayFreshRoute, nil)
	if code != http.StatusOK {
		f.t.Fatalf("GET %s: %d %s", team.RelayFreshRoute, code, body)
	}
	var v team.RelayFreshView
	if err := json.Unmarshal(body, &v); err != nil {
		f.t.Fatalf("decode: %v; %s", err, body)
	}
	return v
}

// freshWatch keeps only the relay.fresh frames; every read flushes the publisher first.
type freshWatch struct {
	t   *testing.T
	m   *Module
	sub *core.EventSubscriber
}

func (f *fixture) watchFresh() *freshWatch {
	f.t.Helper()
	sub := f.core.Events.AddTestSubscriber()
	f.t.Cleanup(func() { f.core.Events.RemoveTestSubscriber(sub) })
	return &freshWatch{t: f.t, m: f.m, sub: sub}
}

func (w *freshWatch) drain() []team.RelayFreshEventValue {
	w.t.Helper()
	w.m.rosterFlush()
	var out []team.RelayFreshEventValue
	for {
		select {
		case raw := <-w.sub.SendCh():
			var ev core.HostEvent
			if err := json.Unmarshal(raw, &ev); err != nil {
				w.t.Fatalf("decode HostEvent: %v", err)
			}
			if ev.Type != team.RelayFreshEventType {
				continue
			}
			var v team.RelayFreshEventValue
			if err := json.Unmarshal([]byte(ev.Value), &v); err != nil {
				w.t.Fatalf("decode RelayFreshEventValue: %v", err)
			}
			out = append(out, v)
		default:
			return out
		}
	}
}

// GET follows the table: empty is [] and not null; a half shows with its ref, op, path and time; it goes with the row.
// Mutation gates: GET answers null for none → red; the row's path left out → red; a list that ignores deletion → red.
func TestFreshSurface_GetFollowsTheTable(t *testing.T) {
	f := newFixture(t)
	code, body := f.do(http.MethodGet, team.RelayFreshRoute, nil)
	if code != http.StatusOK || !strings.Contains(string(body), `"sessions":[]`) {
		t.Fatalf("none: %d %s, want sessions:[]", code, body)
	}
	half := f.freshHalfWithFile("sid-1")
	v := f.getFresh()
	if len(v.Sessions) != 1 {
		t.Fatalf("sessions = %+v, want one", v.Sessions)
	}
	s := v.Sessions[0]
	if s.SessionID != "sid-1" || s.OpID != half.ID || s.Path != half.HandoffPath || s.WrittenAt == 0 || s.Ref == "" {
		t.Fatalf("session = %+v, want sid-1 / %s / %s with a ref and a time", s, half.ID, half.HandoffPath)
	}
	if c, _ := f.do(http.MethodPost, "/api/relay/fresh", team.RelayFreshRequest{SessionID: "sid-1", OpID: half.ID, Stale: true}); c != http.StatusOK {
		t.Fatal("stale report failed")
	}
	if v := f.getFresh(); len(v.Sessions) != 0 {
		t.Fatalf("after the row went: %+v", v.Sessions)
	}
}

// The event: a snapshot to a new subscriber, changed after each change (a half written, a stale report), nothing when nothing changed.
// Mutation gates: no event on written → red; no event on stale → red; a changed that repeats an unchanged list → red.
func TestFreshSurface_EventFollowsTheTable(t *testing.T) {
	f := newFixture(t)
	w := f.watchFresh()
	f.m.rosterFlush()
	if evs := w.drain(); len(evs) != 0 {
		t.Fatalf("nothing changed yet: %+v", evs)
	}
	half := f.freshHalfWithFile("sid-1")
	evs := w.drain()
	if len(evs) != 1 || evs[0].Op != "changed" || len(evs[0].Sessions) != 1 || evs[0].Sessions[0].OpID != half.ID {
		t.Fatalf("after written: %+v, want one changed with the half", evs)
	}
	if evs := w.drain(); len(evs) != 0 {
		t.Fatalf("an unchanged list was sent again: %+v", evs)
	}
	f.do(http.MethodPost, "/api/relay/fresh", team.RelayFreshRequest{SessionID: "sid-1", OpID: half.ID, Stale: true})
	evs = w.drain()
	if len(evs) != 1 || evs[0].Op != "changed" || len(evs[0].Sessions) != 0 {
		t.Fatalf("after stale: %+v, want one changed with no sessions", evs)
	}
	// a new subscriber is given the snapshot of what is there now
	f.freshHalfWithFile("sid-1")
	w.drain()
	w2 := f.watchFresh()
	f.m.sendFreshSnapshot(w2.sub)
	evs = w2.drain()
	if len(evs) != 1 || evs[0].Op != "snapshot" || len(evs[0].Sessions) != 1 {
		t.Fatalf("snapshot: %+v, want one snapshot with the half", evs)
	}
}

// The roster carries the lead's fresh half (and a member's), and nothing for a session without one.
// Mutation gate: the roster leaves it out → red.
func TestFreshSurface_RosterCarriesItForALead(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("8")
	if r := f.getRoster(); r.Teams[0].Lead.FreshHandoff != nil || r.Teams[0].Members[0].FreshHandoff != nil {
		t.Fatalf("no half yet: %+v", r.Teams[0].Lead.FreshHandoff)
	}
	half := f.freshHalfWithFile("sid-1")
	r := f.getRoster()
	got := r.Teams[0].Lead.FreshHandoff
	if got == nil || got.OpID != half.ID || got.Path != half.HandoffPath || got.WrittenAt == 0 {
		t.Fatalf("lead fresh_handoff = %+v, want the half %s", got, half.ID)
	}
	if r.Teams[0].Members[0].FreshHandoff != nil {
		t.Fatal("the member got the lead's half")
	}
	raw, _ := json.Marshal(r.Teams[0].Members[0])
	if strings.Contains(string(raw), "fresh_handoff") {
		t.Fatalf("a member without a half carries the key: %s", raw)
	}
}

// The roster event follows a half too: a half written is a roster change.
func TestFreshSurface_RosterEventFollowsIt(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("8")
	rw := f.watchRoster()
	rw.drain()
	f.freshHalfWithFile("sid-1")
	var seen bool
	for _, ev := range rw.drain() {
		for _, tm := range ev.Teams {
			if tm.Lead.FreshHandoff != nil {
				seen = true
			}
		}
	}
	if !seen {
		t.Fatal("no roster event carried the lead's fresh_handoff after the half was written")
	}
}

// ---- RT-3a2 T33: retention ----

// halfOnDisk is a cancelled half op whose file is <relayDir>/<id>.md, with its fresh row; the clock is at `at`.
func (f *fixture) halfOnDisk(id, sid string, at int64) team.RelayOp {
	f.t.Helper()
	relayDir := filepath.Join(f.core.Cfg.DataDir, "relay")
	f.m.relayDir = relayDir
	op := selfOp(id, sid, "_r"+id, at)
	op.State, op.Reason, op.Mode = team.RelayCancelled, team.RelayReasonHalf, team.RelayModeHalf
	op.HandoffPath = filepath.Join(relayDir, id+".md")
	if err := f.m.store.CreateRelayOp(op); err != nil {
		f.t.Fatal(err)
	}
	if err := os.MkdirAll(relayDir, 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(op.HandoffPath, []byte("# HANDOFF\n"), 0o600); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.m.store.db.Exec(`INSERT INTO relay_fresh_half (session_id, op_id, path, written_at) VALUES (?, ?, ?, ?)`, sid, id, op.HandoffPath, at); err != nil {
		f.t.Fatal(err)
	}
	return op
}

// A cancelled half named by the fresh row is no victim however old; once the row goes its 3 days count from then.
// Mutation gates: the exemption removed → the old half's file is gone (red); the row's deletion leaves updated_at alone → the file is
// removed at once (red).
func TestRetention_AFreshHalfKeepsItsFileUntilTheRowGoesThenThreeDays(t *testing.T) {
	f := newFixture(t)
	op := f.halfOnDisk("half-1", "sid-1", 1*day)
	f.clock.Store(10 * day) // far past 3 days
	f.m.sweepRetention()
	if _, err := os.Stat(op.HandoffPath); err != nil {
		t.Fatalf("a fresh half older than 3 days lost its file: %v", err)
	}
	if got := f.op("half-1"); got.Pruned {
		t.Fatalf("pruned while fresh: %+v", got)
	}
	if err := f.m.store.DeleteFreshHalf("sid-1", "", f.m.now()); err != nil {
		t.Fatal(err)
	}
	f.m.sweepRetention() // the row went just now
	if _, err := os.Stat(op.HandoffPath); err != nil {
		t.Fatalf("the file went the moment the row did: %v", err)
	}
	f.clock.Store(10*day + 3*day - 1)
	f.m.sweepRetention()
	if _, err := os.Stat(op.HandoffPath); err != nil {
		t.Fatalf("the file went before 3 days after the row: %v", err)
	}
	f.clock.Store(10*day + 3*day)
	f.m.sweepRetention()
	if _, err := os.Stat(op.HandoffPath); !os.IsNotExist(err) {
		t.Fatalf("the file stayed 3 days after the row went: %v", err)
	}
	if got := f.op("half-1"); !got.Pruned {
		t.Fatalf("not pruned: %+v", got)
	}
}

// The claim that ends a fresh half (its row deleted inside the report's transaction) starts the same 3 days.
func TestRetention_AClaimStartsTheThreeDaysToo(t *testing.T) {
	f := newFixture(t)
	half := f.freshHalfWithFile("sid-1")
	f.clock.Store(10 * day)
	f.m.sweepRetention()
	r := f.begin("sid-1")
	if c, _ := f.decide(r.RequestID, "approve"); c != http.StatusOK {
		t.Fatalf("approve: %d", c)
	}
	if got := f.op(half.ID); got.UpdatedAt != 10*day {
		t.Fatalf("half op updated_at = %d, want the claim's time %d", got.UpdatedAt, 10*day)
	}
}

// A reuse op points at the half's file: pruning it removes nothing and logs nothing; another mismatch still logs.
// Mutation gates: the reuse op removes the half's file → red; the log line for a reuse op → red; no log for a plain mismatch → red.
func TestRetention_AReuseOpRemovesNothingAndLogsNothing(t *testing.T) {
	f := newFixture(t)
	half := f.halfOnDisk("half-1", "sid-1", 1*day)
	reuse := doneOp("reuse-1", "sid-1", 1*day)
	reuse.HandoffPath = half.HandoffPath
	if err := f.m.store.CreateRelayOp(reuse); err != nil {
		t.Fatal(err)
	}
	odd := doneOp("odd-1", "sid-2", 1*day)
	odd.HandoffPath = filepath.Join(f.core.Cfg.DataDir, "elsewhere.md")
	if err := f.m.store.CreateRelayOp(odd); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var logs []string
	f.m.logf = func(format string, a ...any) { mu.Lock(); logs = append(logs, fmt.Sprintf(format, a...)); mu.Unlock() }
	f.clock.Store(30 * day)
	f.m.sweepRetention()
	if _, err := os.Stat(half.HandoffPath); err != nil {
		t.Fatalf("the reuse op's pruning removed the half's file: %v", err)
	}
	if got := f.op("reuse-1"); !got.Pruned {
		t.Fatalf("reuse op not pruned: %+v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, l := range logs {
		if strings.Contains(l, "reuse-1") {
			t.Fatalf("a reuse op was logged: %s", l)
		}
	}
	var oddLogged bool
	for _, l := range logs {
		if strings.Contains(l, "odd-1") && strings.Contains(l, "not <relay dir>/<op id>.md") {
			oddLogged = true
		}
	}
	if !oddLogged {
		t.Fatalf("the plain mismatch lost its log line: %v", logs)
	}
}

// Every staleness path tells the stream and the roster: a compaction drops the half and the changed follows.
// Mutation gate: no signal after the compaction's delete → no changed (red).
func TestFreshSurface_Compact_TellsTheStream(t *testing.T) {
	f := newFixture(t)
	f.freshHalfWithFile("sid-1")
	w := f.watchFresh()
	w.drain()
	f.compacted("sid-1", "auto")
	evs := w.drain()
	if len(evs) != 1 || evs[0].Op != "changed" || len(evs[0].Sessions) != 0 {
		t.Fatalf("after a compaction: %+v, want one changed with no sessions", evs)
	}
}

// No sessions is [] and not null, in the view and in the event (a client iterates it).
// Mutation gate: drop either MarshalJSON guard → red.
func TestFreshWire_NoSessionsIsAnEmptyListNotNull(t *testing.T) {
	for name, v := range map[string]any{"view": team.RelayFreshView{}, "event": team.RelayFreshEventValue{Op: "changed"}} {
		raw, err := json.Marshal(v)
		if err != nil || !strings.Contains(string(raw), `"sessions":[]`) {
			t.Fatalf("%s: %s %v, want sessions:[]", name, raw, err)
		}
	}
}
