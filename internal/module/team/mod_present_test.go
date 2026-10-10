package teammod

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/modevents"
	modeventsmod "github.com/wake/purdex/internal/module/modevents"
	"github.com/wake/purdex/internal/team"
)

// presenceClock is the modevents registry's clock in these tests.
type presenceClock struct{ ns atomic.Int64 }

func (c *presenceClock) Now() time.Time          { return time.Unix(0, c.ns.Load()).UTC() }
func (c *presenceClock) Advance(d time.Duration) { c.ns.Add(int64(d)) }

// modStream registers a modevents registry in the fixture's core registry (the
// team module looks it up per call) and returns it with its clock.
func (f *fixture) modStream() (*modevents.Registry, *presenceClock) {
	f.t.Helper()
	clk := &presenceClock{}
	clk.ns.Store(time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC).UnixNano())
	reg := modevents.NewRegistry(clk.Now)
	f.core.Registry.Register(modeventsmod.ServiceName, reg)
	return reg, clk
}

// beat applies one batch of the stream: a heartbeat of sid, or a session.end.
func beat(t *testing.T, reg *modevents.Registry, stream, sid, typ string, seq int64) {
	t.Helper()
	b := modevents.Batch{V: 1, Stream: stream, Agent: "cc", CCVersion: "2.1.293", ModVersion: "1.0.0",
		Events: []modevents.Event{{Seq: seq, At: seq, SID: sid, Type: typ, Data: json.RawMessage(`{}`)}}}
	if _, err := reg.Apply(b); err != nil {
		t.Fatal(err)
	}
}

// "Mod present" is a fresh mod event stream of the session (spec §6.6), not the
// hello record. Mutation gate: read modSeen again, ignore Ended, the sid or the
// 30 s window → red.
func TestModPresent_IsAFreshStreamNotTheHello(t *testing.T) {
	f := newFixture(t)
	reg, clk := f.modStream()
	if f.m.modPresent("sid-1") || f.m.modPresent("") {
		t.Fatal("nothing yet")
	}
	// #1832: a hello alone (the mod said hello once, then the session came back
	// with --safe-mode) is not presence.
	if code, body := f.do(http.MethodPost, "/api/relay/hello", team.RelayHelloRequest{SessionID: "sid-1", ModVersion: "1", Agent: "cc"}); code != http.StatusOK {
		t.Fatalf("hello: %d %s", code, body)
	}
	if f.m.modPresent("sid-1") {
		t.Fatal("a hello without a stream must not count")
	}
	beat(t, reg, "stream-aaaa", "sid-1", "heartbeat", 1)
	if !f.m.modPresent("sid-1") || f.m.modPresent("sid-2") {
		t.Fatal("a fresh stream makes exactly its session present")
	}
	clk.Advance(29 * time.Second)
	if !f.m.modPresent("sid-1") {
		t.Fatal("still present at 29 s")
	}
	clk.Advance(2 * time.Second)
	if f.m.modPresent("sid-1") {
		t.Fatal("31 s without a batch: the mod is gone")
	}
	beat(t, reg, "stream-aaaa", "sid-1", "heartbeat", 2)
	beat(t, reg, "stream-aaaa", "sid-2", "heartbeat", 3) // /clear or resume moved the stream
	if f.m.modPresent("sid-1") || !f.m.modPresent("sid-2") {
		t.Fatal("presence follows the stream's current session")
	}
	beat(t, reg, "stream-aaaa", "sid-2", modevents.TypeSessionEnd, 4)
	if f.m.modPresent("sid-2") {
		t.Fatal("an ended stream is not present")
	}
}

// Without a modevents registry (the socket module absent) nobody is present.
func TestModPresent_NoRegistryMeansAbsent(t *testing.T) {
	f := newFixture(t)
	if f.m.modPresent("sid-1") {
		t.Fatal("no registry: no mod")
	}
}

// A session whose mod is gone gets its terminal-only row again (#1832): the
// hook path opens it once the stream is stale, and not while it is fresh.
func TestObserve_StaleStreamOpensTerminalOnly(t *testing.T) {
	f := newFixture(t)
	reg, clk := f.modStream()
	beat(t, reg, "stream-aaaa", "sid-1", "heartbeat", 1)
	f.m.observeHookEvent(preAsk("sid-1", "toolu_f"))
	if rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-1"); len(rows) != 0 {
		t.Fatalf("fresh stream: rows = %+v", rows)
	}
	clk.Advance(31 * time.Second)
	f.m.observeHookEvent(preAsk("sid-1", "toolu_f"))
	if rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-1"); len(rows) != 1 {
		t.Fatalf("stale stream: rows = %d, want 1", len(rows))
	}
}

// Right after a daemon restart the registry is empty: the hook opens a
// terminal-only row, and the mod's begin then takes it over, one card (spec
// §6.6, "wrong in the safe direction only").
func TestObserve_EmptyRegistryThenBeginIsOneCard(t *testing.T) {
	f := newFixture(t)
	f.modStream() // empty: the daemon just restarted
	f.m.observeHookEvent(preAsk("sid-1", "toolu_r"))
	rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-1")
	if len(rows) != 1 {
		t.Fatalf("terminal-only rows = %d, want 1", len(rows))
	}
	f.askBegin("toolu_r")
	if rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-1"); len(rows) != 0 {
		t.Fatalf("after begin: terminal-only rows = %+v", rows)
	}
	open, err := f.m.store.ListOpen()
	if err != nil || len(open) != 1 || isTerminalOnly(open[0]) {
		t.Fatalf("open rows = %+v err=%v, want one answerable card", open, err)
	}
}
