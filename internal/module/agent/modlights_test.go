package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/modevents"
	modeventsmod "github.com/wake/purdex/internal/module/modevents"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

const (
	modSID1 = "11111111-1111-4111-8111-111111111111"
	modSID2 = "22222222-2222-4222-8222-222222222222"
	modStrm = "stream-test-0001"
)

var modT0 = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// modClock is a settable clock for the overlay's Live(now) checks.
type modClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *modClock) Now() time.Time    { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *modClock) Set(t time.Time)   { c.mu.Lock(); c.now = t; c.mu.Unlock() }
func useModClock(m *Module) *modClock { c := &modClock{now: modT0}; m.modNow = c.Now; return c }
func modEv(sid, typ, data string) modevents.Event {
	return modevents.Event{SID: sid, Type: typ, Data: json.RawMessage(data)}
}

// feedMod drives the subscriber directly (the test-only path: a-3a has no
// worker, so nothing emits on a mod event by itself).
func feedMod(m *Module, stream string, evs ...modevents.Event) {
	for i, ev := range evs {
		if ev.Seq == 0 {
			ev.Seq = int64(i + 1)
		}
		if ev.At == 0 {
			ev.At = ev.Seq * 1000
		}
		m.onModEvent(modevents.StreamInfo{Stream: stream, SID: ev.SID}, ev)
	}
}

func modDirtySIDs(m *Module) map[string]bool {
	m.modMu.Lock()
	defer m.modMu.Unlock()
	out := map[string]bool{}
	for sid := range m.modDirty {
		out[sid] = true
	}
	return out
}

// TestModSubscriber_NeverBlocks: the subscriber runs inside Registry.Apply
// under the stream's order mutex, so it must return while the frame store
// and m.mu are both stuck.
func TestModSubscriber_NeverBlocks(t *testing.T) {
	m := newTestModule(t)
	useModClock(m)
	release := make(chan struct{})
	m.listFramesFn = func() ([]store.Frame, error) { <-release; return nil, nil }
	m.mu.Lock()
	defer func() {
		close(release)
		m.mu.Unlock()
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 1; i <= 1000; i++ {
			typ := modevents.TypeTurnStart
			if i%2 == 0 {
				typ = modevents.TypeTurnComplete
			}
			ev := modEv(modSID1, typ, `{"turn_id":"t","reason":"answer"}`)
			ev.Seq = int64(i)
			m.onModEvent(modevents.StreamInfo{Stream: modStrm, SID: modSID1}, ev)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber blocked on the frame store or m.mu")
	}
	if !modDirtySIDs(m)[modSID1] {
		t.Fatal("the sid of a changed stream is not marked dirty")
	}
	select {
	case <-m.modKick:
	default:
		t.Fatal("the subscriber did not kick the worker")
	}
}

// TestModSubscriber_SwitchMarksBothSids: a /clear moves the stream to a new
// sid; the old sid's panes lose the overlay and the new sid's gain it.
func TestModSubscriber_SwitchMarksBothSids(t *testing.T) {
	m := newTestModule(t)
	useModClock(m)
	feedMod(m, modStrm, modEv(modSID1, modevents.TypeSessionStart, `{"cwd":"/w"}`))
	m.modMu.Lock()
	clear(m.modDirty)
	m.modMu.Unlock()

	sw := modEv(modSID2, modevents.TypeSessionSwitch, `{"prev_sid":"`+modSID1+`","source":"clear"}`)
	sw.Seq = 2
	feedMod(m, modStrm, sw)

	if d := modDirtySIDs(m); !d[modSID1] || !d[modSID2] {
		t.Fatalf("dirty = %v, want both sids", d)
	}
	m.modMu.Lock()
	defer m.modMu.Unlock()
	if _, ok := m.modBySID[modSID1]; ok {
		t.Fatal("the old sid still points at the stream")
	}
	if m.modBySID[modSID2] != modStrm {
		t.Fatalf("modBySID[new] = %q, want %q", m.modBySID[modSID2], modStrm)
	}
}

// TestModSubscriber_NoRegistryReentry wires the subscriber into a real
// registry and applies batches on several streams while projections are
// read concurrently (run under -race). A subscriber that led back into
// Apply would deadlock on the stream's order mutex.
func TestModSubscriber_NoRegistryReentry(t *testing.T) {
	m := newTestModule(t)
	useModClock(m)
	m.modReg = modevents.NewRegistry(func() time.Time { return modT0 })
	m.startModLights()
	t.Cleanup(m.stopModLights)
	seedIdentityFrame(t, m, "%7", "cc", 4100, "s4100", 10, modSID1, "/w")

	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for s := 0; s < 4; s++ {
			wg.Add(1)
			go func(s int) {
				defer wg.Done()
				stream := fmt.Sprintf("stream-race-%04d", s)
				for i := 1; i <= 50; i++ {
					typ := modevents.TypeTurnStart
					if i%2 == 0 {
						typ = modevents.TypeHeartbeat
					}
					ev := modEv(modSID1, typ, `{"turn_id":"t"}`)
					ev.Seq, ev.At = int64(i), int64(i)
					if _, err := m.modReg.Apply(modevents.Batch{V: 1, Stream: stream, Agent: "cc", Events: []modevents.Event{ev}}); err != nil {
						t.Errorf("Apply: %v", err)
						return
					}
				}
			}(s)
		}
		for r := 0; r < 2; r++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 50; i++ {
					if _, err := m.liveFrameProjections(); err != nil {
						t.Errorf("liveFrameProjections: %v", err)
						return
					}
					if _, err := m.projectPane("%7"); err != nil {
						t.Errorf("projectPane: %v", err)
						return
					}
				}
			}()
		}
		wg.Wait()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock: registry Apply with the agent subscriber did not finish")
	}
	m.modMu.Lock()
	defer m.modMu.Unlock()
	if len(m.modStreams) != 4 || m.modBySID[modSID1] == "" {
		t.Fatalf("streams = %d, bySID = %v; want 4 streams indexed under the sid", len(m.modStreams), m.modBySID)
	}
}

// TestModLights_InitAndStartWireTheRegistry: the module depends on
// modevents, finds its registry at Init, subscribes at Start and cancels at
// Stop; without the registry the mod path stays off.
func TestModLights_InitAndStartWireTheRegistry(t *testing.T) {
	m := newTestModule(t)
	if !slices.Contains(m.Dependencies(), modeventsmod.ServiceName) {
		t.Fatalf("Dependencies() = %v, want %q", m.Dependencies(), modeventsmod.ServiceName)
	}
	reg := modevents.NewRegistry(time.Now)
	c := &core.Core{Registry: core.NewServiceRegistry()}
	c.Registry.Register(modeventsmod.ServiceName, reg)
	m.initModLights(c)
	if m.modReg != reg {
		t.Fatal("Init did not pick up the registry")
	}
	m.startModLights()
	apply := func(seq int64) {
		ev := modEv(modSID1, modevents.TypeSessionStart, `{"cwd":"/w"}`)
		ev.Seq = seq
		if _, err := reg.Apply(modevents.Batch{V: 1, Stream: modStrm, Agent: "cc", Events: []modevents.Event{ev}}); err != nil {
			t.Fatal(err)
		}
	}
	countStreams := func() int {
		m.modMu.Lock()
		defer m.modMu.Unlock()
		return len(m.modStreams)
	}
	apply(1)
	if n := countStreams(); n != 1 {
		t.Fatalf("streams after Start = %d, want 1", n)
	}
	m.stopModLights()
	m.modMu.Lock()
	clear(m.modStreams)
	m.modMu.Unlock()
	apply(2)
	if n := countStreams(); n != 0 {
		t.Fatal("the subscriber still runs after Stop")
	}

	off := newTestModule(t)
	off.initModLights(&core.Core{Registry: core.NewServiceRegistry()})
	off.startModLights()
	if off.modReg != nil || off.modCancel != nil {
		t.Fatal("without a registry the mod path must stay off")
	}
}

// ---- A3-3: the overlay ----

var (
	modStart     = modEv(modSID1, modevents.TypeSessionStart, `{"cwd":"/w"}`)
	modTurnStart = modEv(modSID1, modevents.TypeTurnStart, `{"turn_id":"t1"}`)
)

// overlayModule is a module with a frame store and the mod clock at modT0;
// panes are seeded per test. It turns the overlay switch on: production
// leaves it off until the a-3b worker runs.
func overlayModule(t *testing.T) (*Module, *modClock) {
	t.Helper()
	m := newTestModule(t)
	m.modOverlayOn.Store(true)
	return m, useModClock(m)
}

func paneProjection(t *testing.T, m *Module, pane string) *SessionProjection {
	t.Helper()
	p, err := m.projectPane(pane)
	if err != nil || p == nil || p.TopFrame == nil {
		t.Fatalf("projectPane(%s) = %+v, %v", pane, p, err)
	}
	return p
}

func liveProjectionByPane(t *testing.T, m *Module) map[string]SessionProjection {
	t.Helper()
	ps, err := m.liveFrameProjections()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]SessionProjection{}
	for _, p := range ps {
		out[p.PaneID] = p
	}
	return out
}

func wantLight(t *testing.T, what string, p SessionProjection, status agentpkg.Status, source string) {
	t.Helper()
	if p.Status != status || p.Source != source {
		t.Fatalf("%s: status %q source %q, want %q %q", what, p.Status, p.Source, status, source)
	}
}

// TestModOverlay_OffByDefault pins the deployed state of a-3a: Init and
// Start subscribe to the registry and the module keeps the per-stream
// state, but nothing turns the overlay on until the re-emit worker (a-3b)
// runs, so the lights stay the hook lights. Without the worker a mod change
// after a hook emit is never re-sent: the Stop hook beats the mod's
// 150 ms-batched turn.complete and the light would stay running.
func TestModOverlay_OffByDefault(t *testing.T) {
	m := newTestModule(t) // not overlayModule: that one turns the switch on
	useModClock(m)
	reg := modevents.NewRegistry(time.Now)
	c := &core.Core{Registry: core.NewServiceRegistry()}
	c.Registry.Register(modeventsmod.ServiceName, reg)
	m.initModLights(c)
	m.startModLights()
	t.Cleanup(m.stopModLights)
	seedIdentityFrame(t, m, "%5", "cc", 501, "s501", 10, modSID1, "/w") // hook status: idle

	for i, ev := range []modevents.Event{modStart, modTurnStart} {
		ev.Seq = int64(i + 1)
		ev.At = ev.Seq * 1000
		if _, err := reg.Apply(modevents.Batch{V: 1, Stream: modStrm, Agent: "cc", Events: []modevents.Event{ev}}); err != nil {
			t.Fatal(err)
		}
	}

	// The subscriber still records the stream; only the overlay is off.
	m.modMu.Lock()
	st := m.modStreams[modStrm]
	live := st != nil && st.Live(m.modClock()) && st.Status() == agentpkg.StatusRunning
	m.modMu.Unlock()
	if !live {
		t.Fatal("the subscriber did not record a live running stream")
	}
	if !modDirtySIDs(m)[modSID1] {
		t.Fatal("the sid was not marked dirty")
	}

	pane := *paneProjection(t, m, "%5")
	wantLight(t, "projectPane", pane, agentpkg.StatusIdle, "hook")
	all := liveProjectionByPane(t, m)
	wantLight(t, "liveFrameProjections", all["%5"], agentpkg.StatusIdle, "hook")
	if pane.Background != "" || all["%5"].Background != "" {
		t.Fatalf("background = %q / %q, want empty", pane.Background, all["%5"].Background)
	}
}

// TestModOverlay_LiveStreamWinsOverHookStatus: hooks leave the frame idle,
// the mod says a turn runs; the hook emit carries the mod's status.
func TestModOverlay_LiveStreamWinsOverHookStatus(t *testing.T) {
	m, _ := overlayModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "code-work", Name: "work"}}}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fakeTmux}
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(string, json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
		},
	})
	sub := m.core.Events.AddTestSubscriber()
	t.Cleanup(func() { m.core.Events.RemoveTestSubscriber(sub) })
	seedIdentityFrame(t, m, "%5", "cc", 200, "Sun Apr 20 01:30:00 2026", 10, modSID1, "/w") // the sender of nonTmuxTail
	feedMod(m, modStrm, modStart, modTurnStart)

	w := postEvent(m, `{"tmux_session":"work","tmux_pane_id":"%5",`+nonTmuxTail+`,"raw_event":{}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	select {
	case msg := <-sub.SendCh():
		var env struct{ Type, Session, Value string }
		if err := json.Unmarshal(msg, &env); err != nil {
			t.Fatal(err)
		}
		var ev agentpkg.NormalizedEvent
		if err := json.Unmarshal([]byte(env.Value), &ev); err != nil {
			t.Fatal(err)
		}
		if env.Session != "code-work" || ev.Status != "running" || ev.Source != "mod" {
			t.Fatalf("emit = %s, want running from mod on code-work", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("no hook emit")
	}
	m.mu.Lock()
	got := m.currentStatus["work"]
	m.mu.Unlock()
	if got != agentpkg.StatusRunning {
		t.Fatalf("currentStatus = %q, want the effective running", got)
	}
}

// TestModOverlay_StaleStreamFallsBackToFrameStatus: 30 s after its last
// event the stream still drives the pane; past that the frame does.
func TestModOverlay_StaleStreamFallsBackToFrameStatus(t *testing.T) {
	m, clock := overlayModule(t)
	seedIdentityFrame(t, m, "%5", "cc", 501, "s501", 10, modSID1, "/w")
	feedMod(m, modStrm, modStart, modTurnStart)

	clock.Set(modT0.Add(30 * time.Second))
	wantLight(t, "at 30 s", *paneProjection(t, m, "%5"), agentpkg.StatusRunning, "mod")
	clock.Set(modT0.Add(31 * time.Second))
	wantLight(t, "at 31 s (pane)", *paneProjection(t, m, "%5"), agentpkg.StatusIdle, "hook")
	wantLight(t, "at 31 s (all)", liveProjectionByPane(t, m)["%5"], agentpkg.StatusIdle, "hook")
}

// TestModOverlay_MatchesBySid: only the pane whose frame carries the
// stream's sid is overlaid.
func TestModOverlay_MatchesBySid(t *testing.T) {
	m, _ := overlayModule(t)
	seedIdentityFrame(t, m, "%5", "cc", 501, "s501", 10, modSID1, "/w")
	seedIdentityFrame(t, m, "%6", "cc", 601, "s601", 20, modSID2, "/w")
	feedMod(m, modStrm, modStart, modTurnStart)

	all := liveProjectionByPane(t, m)
	wantLight(t, "pane with the sid", all["%5"], agentpkg.StatusRunning, "mod")
	wantLight(t, "pane with another sid", all["%6"], agentpkg.StatusIdle, "hook")
	wantLight(t, "projectPane of the other pane", *paneProjection(t, m, "%6"), agentpkg.StatusIdle, "hook")
}

// TestModOverlay_FollowsClear: a /clear moves the stream to a new sid; the
// pane is overlaid again once the hook SessionStart{clear} has moved its
// frame to that sid, and not before.
func TestModOverlay_FollowsClear(t *testing.T) {
	m, _ := overlayModule(t)
	f := seedIdentityFrame(t, m, "%5", "cc", 501, "s501", 10, modSID1, "/w")
	feedMod(m, modStrm, modStart, modTurnStart)
	wantLight(t, "before /clear", *paneProjection(t, m, "%5"), agentpkg.StatusRunning, "mod")

	sw := modEv(modSID2, modevents.TypeSessionSwitch, `{"prev_sid":"`+modSID1+`","source":"clear"}`)
	sw.Seq = 3
	ts := modEv(modSID2, modevents.TypeTurnStart, `{"turn_id":"t2"}`)
	ts.Seq = 4
	feedMod(m, modStrm, sw, ts)
	wantLight(t, "frame still on the old sid", *paneProjection(t, m, "%5"), agentpkg.StatusIdle, "hook")

	if err := m.frames.UpdateSessionIdentity(f.FrameID, modSID2, "", 1<<40); err != nil {
		t.Fatal(err)
	}
	wantLight(t, "frame on the new sid", *paneProjection(t, m, "%5"), agentpkg.StatusRunning, "mod")
}

// TestModOverlay_DotsKeepDelegatingAndDropWorkflowAgents: while live, the
// dots are the mod's; a hook native ref for a mod dot keeps its fields,
// proxy refs stay, and a hook native ref the mod does not dot (a workflow
// agent) leaves the projection but not the frame row.
func TestModOverlay_DotsKeepDelegatingAndDropWorkflowAgents(t *testing.T) {
	m, _ := overlayModule(t)
	proxy := agentpkg.SubagentRef{ID: "proxy-1", Type: "codex", StartedAt: 5, SourcePID: 900, SourceStartTime: "s900", IsProxy: true}
	delegating := agentpkg.SubagentRef{ID: "agent-A", Type: "task", StartedAt: 111, Delegating: true, DelegatingToolUseIDs: []string{"toolu_1"}}
	workflow := agentpkg.SubagentRef{ID: "agent-W", Type: "task", StartedAt: 222}
	seedFrameWithSubagents(t, m, "%5", "cc", 501, "s501", 10, []agentpkg.SubagentRef{proxy, delegating, workflow})
	f, err := m.frames.GetByIdentity("%5", 501, "s501")
	if err != nil || f == nil {
		t.Fatalf("frame: %v", err)
	}
	if err := m.frames.UpdateSessionIdentity(f.FrameID, modSID1, "", 1<<40); err != nil {
		t.Fatal(err)
	}
	spawnA := modEv(modSID1, modevents.TypeAgentSpawn, `{"agent_id":"agent-A"}`)
	spawnA.Seq, spawnA.At = 3, 2000
	spawnB := modEv(modSID1, modevents.TypeAgentSpawn, `{"agent_id":"agent-B"}`)
	spawnB.Seq, spawnB.At = 4, 3000
	spawnW := modEv(modSID1, modevents.TypeAgentSpawn, `{"agent_id":"agent-W","workflow_run_id":"wf-1"}`)
	spawnW.Seq, spawnW.At = 5, 4000
	feedMod(m, modStrm, modStart, modTurnStart, spawnA, spawnB, spawnW)

	got := paneProjection(t, m, "%5").Subagents
	want := []agentpkg.SubagentRef{
		proxy,
		delegating,
		{ID: "agent-B", Type: "cc", StartedAt: 3000 * int64(time.Millisecond)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("subagents = %+v\nwant %+v", got, want)
	}
	row, err := m.frames.GetByIdentity("%5", 501, "s501")
	if err != nil || row == nil || len(row.Subagents) != 3 {
		t.Fatalf("the frame row must keep its refs: %+v, %v", row, err)
	}
}

// TestModOverlay_EndedStreamFallsBack: an ended stream never overlays; the
// pane shows its frame until the hook SessionEnd or the sweep removes it.
func TestModOverlay_EndedStreamFallsBack(t *testing.T) {
	m, _ := overlayModule(t)
	seedIdentityFrame(t, m, "%5", "cc", 501, "s501", 10, modSID1, "/w")
	end := modEv(modSID1, modevents.TypeSessionEnd, `{"reason":"prompt_input_exit"}`)
	end.Seq = 3
	feedMod(m, modStrm, modStart, modTurnStart)
	wantLight(t, "live", *paneProjection(t, m, "%5"), agentpkg.StatusRunning, "mod")
	feedMod(m, modStrm, end)
	wantLight(t, "ended", *paneProjection(t, m, "%5"), agentpkg.StatusIdle, "hook")
}

// TestModOverlay_BackgroundFromLiveStream: the background symbol rides on
// the projection and into the wire event.
func TestModOverlay_BackgroundFromLiveStream(t *testing.T) {
	m, _ := overlayModule(t)
	seedIdentityFrame(t, m, "%5", "cc", 501, "s501", 10, modSID1, "/w")
	bg := modEv(modSID1, modevents.TypeBackground, `{"tasks":[{"id":"b1","type":"monitor","status":"running"}],"crons":0}`)
	bg.Seq = 3
	feedMod(m, modStrm, modStart, modTurnStart, bg)
	p := paneProjection(t, m, "%5")
	ev := buildProjectionNormalized(p, "cc", "PdxStop", 1, agentpkg.DeriveResult{Status: agentpkg.StatusIdle})
	if p.Background != "monitor" || ev.Background != "monitor" || ev.Status != "running" || ev.Source != "mod" {
		t.Fatalf("projection bg %q, event %+v", p.Background, ev)
	}
}

// TestNormalized_BackgroundAlwaysPresent: "" clears the symbol, so the key
// is always on the wire; source is always set.
func TestNormalized_BackgroundAlwaysPresent(t *testing.T) {
	for name, ev := range map[string]agentpkg.NormalizedEvent{
		"zero":          {},
		"projection":    buildProjectionNormalized(&SessionProjection{PaneID: "%5", TopFrame: &store.Frame{Status: agentpkg.StatusIdle}}, "cc", "PdxStop", 1, agentpkg.DeriveResult{}),
		"no projection": buildProjectionNormalized(nil, "cc", "PdxStop", 1, agentpkg.DeriveResult{}),
	} {
		b, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `"background":""`) {
			t.Fatalf("%s: %s lacks \"background\":\"\"", name, b)
		}
		if name != "zero" && !strings.Contains(string(b), `"source":"hook"`) {
			t.Fatalf("%s: %s lacks \"source\":\"hook\"", name, b)
		}
	}
}

// TestNormalized_NonTmuxSourceIsHook: a non-tmux session has no frame and
// no overlay; its frames say source hook.
func TestNormalized_NonTmuxSourceIsHook(t *testing.T) {
	m, sub := nonTmuxModule(t)
	w := postEvent(m, `{"tmux_session":"","tmux_pane_id":"",`+nonTmuxTail+`,"raw_event":{"session_id":"abc-123"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	select {
	case msg := <-sub.SendCh():
		var env struct{ Value string }
		if err := json.Unmarshal(msg, &env); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(env.Value, `"source":"hook"`) || !strings.Contains(env.Value, `"background":""`) {
			t.Fatalf("value = %s", env.Value)
		}
	case <-time.After(time.Second):
		t.Fatal("no broadcast")
	}
	if ev := m.buildNormalized("work", "PdxStop", "cc", 1, agentpkg.DeriveResult{}); ev.Source != "hook" {
		t.Fatalf("legacy replay source = %q, want hook", ev.Source)
	}
}

// ---- the sid index: an ended stream never shadows a live one ----

func modIndex(m *Module, sid string) (string, bool) {
	m.modMu.Lock()
	defer m.modMu.Unlock()
	id, ok := m.modBySID[sid]
	return id, ok
}

func clearModDirty(m *Module) {
	m.modMu.Lock()
	clear(m.modDirty)
	m.modMu.Unlock()
}

func modSessionEnd() modevents.Event {
	end := modEv(modSID1, modevents.TypeSessionEnd, `{"reason":"prompt_input_exit"}`)
	end.Seq = 9
	return end
}

// TestModBySID_EndedStreamRepointsToLiveSibling: A runs, B reports the same
// sid later and then ends; the pane keeps A's overlay.
func TestModBySID_EndedStreamRepointsToLiveSibling(t *testing.T) {
	m, clock := overlayModule(t)
	seedIdentityFrame(t, m, "%5", "cc", 501, "s501", 10, modSID1, "/w")
	feedMod(m, "stream-A", modStart, modTurnStart)
	clock.Set(modT0.Add(time.Second))
	feedMod(m, "stream-B", modStart)
	if id, _ := modIndex(m, modSID1); id != "stream-B" {
		t.Fatalf("modBySID = %q, want the newest reporter stream-B", id)
	}
	clearModDirty(m)

	feedMod(m, "stream-B", modSessionEnd())

	if id, _ := modIndex(m, modSID1); id != "stream-A" {
		t.Fatalf("modBySID = %q, want stream-A (B ended)", id)
	}
	if !modDirtySIDs(m)[modSID1] {
		t.Fatal("the sid is not dirty after its index moved")
	}
	wantLight(t, "pane", *paneProjection(t, m, "%5"), agentpkg.StatusRunning, "mod")
}

// TestModBySID_EndedOnlyStreamDropsIndex: with no live sibling the index
// entry goes and the pane shows its frame.
func TestModBySID_EndedOnlyStreamDropsIndex(t *testing.T) {
	m, _ := overlayModule(t)
	seedIdentityFrame(t, m, "%5", "cc", 501, "s501", 10, modSID1, "/w")
	feedMod(m, modStrm, modStart, modTurnStart)
	clearModDirty(m)

	feedMod(m, modStrm, modSessionEnd())

	if id, ok := modIndex(m, modSID1); ok {
		t.Fatalf("modBySID still points at %q", id)
	}
	if !modDirtySIDs(m)[modSID1] {
		t.Fatal("the sid is not dirty after its stream ended")
	}
	wantLight(t, "pane", *paneProjection(t, m, "%5"), agentpkg.StatusIdle, "hook")
}

// TestModBySID_RepointSkipsEndedCandidates: stream A moves away from sid S
// while B (ended, heard from last) and C (live) also report S.
func TestModBySID_RepointSkipsEndedCandidates(t *testing.T) {
	m, clock := overlayModule(t)
	feedMod(m, "stream-C", modStart, modTurnStart)
	clock.Set(modT0.Add(time.Second))
	feedMod(m, "stream-A", modStart)
	clock.Set(modT0.Add(2 * time.Second))
	feedMod(m, "stream-B", modStart)
	clock.Set(modT0.Add(3 * time.Second))
	feedMod(m, "stream-B", modSessionEnd())
	if id, _ := modIndex(m, modSID1); id != "stream-A" {
		t.Fatalf("setup: modBySID = %q, want stream-A", id)
	}

	clock.Set(modT0.Add(4 * time.Second))
	sw := modEv(modSID2, modevents.TypeSessionSwitch, `{"prev_sid":"`+modSID1+`","source":"clear"}`)
	sw.Seq = 3
	feedMod(m, "stream-A", sw)

	if id, _ := modIndex(m, modSID1); id != "stream-C" {
		t.Fatalf("modBySID[old] = %q, want the live stream-C, not the ended stream-B", id)
	}
	if id, _ := modIndex(m, modSID2); id != "stream-A" {
		t.Fatalf("modBySID[new] = %q, want stream-A", id)
	}
}

// ---- bounding the mirror like the registry ----

func modStreamIDs(m *Module) map[string]bool {
	m.modMu.Lock()
	defer m.modMu.Unlock()
	out := map[string]bool{}
	for id := range m.modStreams {
		out[id] = true
	}
	return out
}

// TestModEviction_DropsIdleAndEndedStreams: the first event of a new stream
// drops what the registry would have dropped by now — a stream idle for
// IdleTTL and an ended one EndedTTL after its session.end — and keeps the
// rest.
func TestModEviction_DropsIdleAndEndedStreams(t *testing.T) {
	m, clock := overlayModule(t)
	tn := modT0.Add(modevents.IdleTTL)
	sidOf := func(id string) modevents.Event { return modEv("sid-"+id, modevents.TypeSessionStart, `{"cwd":"/w"}`) }
	endOf := func(id string) modevents.Event {
		end := modEv("sid-"+id, modevents.TypeSessionEnd, `{"reason":"prompt_input_exit"}`)
		end.Seq = 2
		return end
	}

	feedMod(m, "idle", sidOf("idle")) // at T0: IdleTTL old at tn
	clock.Set(tn.Add(-modevents.EndedTTL))
	feedMod(m, "ended", sidOf("ended"), endOf("ended")) // EndedTTL old at tn
	feedMod(m, "running", sidOf("running"))             // EndedTTL old but not ended: stays
	clock.Set(tn.Add(-time.Minute))
	feedMod(m, "fresh", sidOf("fresh"))
	if got := modStreamIDs(m); len(got) != 4 {
		t.Fatalf("setup: streams = %v, want 4 (nothing is old enough while they arrive)", got)
	}

	clock.Set(tn)
	feedMod(m, "newcomer", sidOf("newcomer"))

	got := modStreamIDs(m)
	want := map[string]bool{"running": true, "fresh": true, "newcomer": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("streams = %v, want %v", got, want)
	}
}

// TestModEviction_RepointsSidIndex: dropping the stream the sid index
// points at moves the index to the remaining live stream and marks the sid
// dirty. (The index follows the newest reporter, so the clock has to be
// bent for it to point at the older stream.)
func TestModEviction_RepointsSidIndex(t *testing.T) {
	m, clock := overlayModule(t)
	feedMod(m, "stream-A", modStart)
	feedMod(m, "stream-B", modStart)
	if id, _ := modIndex(m, modSID1); id != "stream-B" {
		t.Fatalf("setup: modBySID = %q, want stream-B", id)
	}
	m.modMu.Lock()
	m.modStreams["stream-B"].LastEvent = modT0.Add(-modevents.IdleTTL)
	m.modMu.Unlock()
	clearModDirty(m)

	clock.Set(modT0.Add(time.Second))
	feedMod(m, "newcomer", modEv(modSID2, modevents.TypeSessionStart, `{"cwd":"/w"}`))

	if got := modStreamIDs(m); got["stream-B"] || !got["stream-A"] {
		t.Fatalf("streams = %v, want stream-B dropped and stream-A kept", got)
	}
	if id, _ := modIndex(m, modSID1); id != "stream-A" {
		t.Fatalf("modBySID = %q, want stream-A", id)
	}
	if !modDirtySIDs(m)[modSID1] {
		t.Fatal("the sid is not dirty after its index moved")
	}
}

// TestModEviction_CapsAtMaxStreams: the mirror never holds more than the
// registry does; the stream heard from longest ago is the one to go.
func TestModEviction_CapsAtMaxStreams(t *testing.T) {
	m, clock := overlayModule(t)
	for i := 0; i <= modevents.MaxStreams; i++ {
		clock.Set(modT0.Add(time.Duration(i) * time.Second))
		id := fmt.Sprintf("stream-%03d", i)
		feedMod(m, id, modEv("sid-"+id, modevents.TypeSessionStart, `{"cwd":"/w"}`))
	}
	got := modStreamIDs(m)
	if len(got) != modevents.MaxStreams {
		t.Fatalf("streams = %d, want %d", len(got), modevents.MaxStreams)
	}
	if got["stream-000"] || !got["stream-001"] || !got[fmt.Sprintf("stream-%03d", modevents.MaxStreams)] {
		t.Fatal("the oldest stream was not the one dropped")
	}
	if _, ok := modIndex(m, "sid-stream-000"); ok {
		t.Fatal("the dropped stream's sid is still indexed")
	}
}
