package agent

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/modevents"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/tmux"
)

// workerRig is a module wired to capture what it puts on the events bus:
// panes %5 and %7 live in tmux session "work" (code-work), %6 in "other"
// (code-other).
type workerRig struct {
	m     *Module
	clock *modClock
	sub   *core.EventSubscriber
}

type emitted struct {
	Session string
	Ev      agentpkg.NormalizedEvent
}

func newWorkerRig(t *testing.T) *workerRig {
	t.Helper()
	m, clock := overlayModule(t)
	fake := tmux.NewFakeExecutor()
	fake.SetPaneSessionName("%5", "work")
	fake.SetPaneSessionName("%7", "work")
	fake.SetPaneSessionName("%6", "other")
	m.tmux = fake
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{
		{Code: "code-work", Name: "work"},
		{Code: "code-other", Name: "other"},
	}}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fake}
	sub := m.core.Events.AddTestSubscriber()
	t.Cleanup(func() { m.core.Events.RemoveTestSubscriber(sub) })
	return &workerRig{m: m, clock: clock, sub: sub}
}

// round runs one worker round at the clock's now.
func (r *workerRig) round() { r.m.runModRound(r.clock.Now()) }

// drain returns every hook frame queued since the last call.
func (r *workerRig) drain(t *testing.T) []emitted {
	t.Helper()
	var out []emitted
	for {
		select {
		case msg := <-r.sub.SendCh():
			var env struct{ Type, Session, Value string }
			if err := json.Unmarshal(msg, &env); err != nil {
				t.Fatal(err)
			}
			if env.Type != "hook" {
				continue
			}
			var ev agentpkg.NormalizedEvent
			if err := json.Unmarshal([]byte(env.Value), &ev); err != nil {
				t.Fatal(err)
			}
			out = append(out, emitted{Session: env.Session, Ev: ev})
		default:
			return out
		}
	}
}

func modAsk(sid, toolUseID string, seq int64) modevents.Event {
	ev := modEv(sid, modevents.TypeToolCheck, `{"tool_use_id":"`+toolUseID+`","decision":"ask"}`)
	ev.Seq = seq
	return ev
}

func wantOneEmit(t *testing.T, what string, got []emitted, session, status, source string) emitted {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("%s: %d emits, want 1: %+v", what, len(got), got)
	}
	if got[0].Session != session || got[0].Ev.Status != status || got[0].Ev.Source != source {
		t.Fatalf("%s: emit = %s %s/%s, want %s %s/%s", what, got[0].Session, got[0].Ev.Status, got[0].Ev.Source, session, status, source)
	}
	return got[0]
}

// TestModWorker_EmitsOnChangeOnly: the SPA marks every idle / waiting /
// error frame unread, so a round whose light is the one already on the wire
// sends nothing — even when a mod event changed the stream's state.
func TestModWorker_EmitsOnChangeOnly(t *testing.T) {
	r := newWorkerRig(t)
	seedIdentityFrame(t, r.m, "%5", "cc", 501, "s501", 10, modSID1, "/w")
	feedMod(r.m, modStrm, modStart, modTurnStart)
	r.round()
	wantOneEmit(t, "first round", r.drain(t), "code-work", "running", "mod")

	// Dirty again with the same light: nothing goes out.
	r.m.modMu.Lock()
	r.m.modDirty[modSID1] = "heartbeat"
	r.m.modMu.Unlock()
	r.round()
	if got := r.drain(t); len(got) != 0 {
		t.Fatalf("same light re-sent: %+v", got)
	}

	// A new light is sent (the first ask) and a repeat of it is not, even
	// when the stream was marked dirty again.
	feedMod(r.m, modStrm, modAsk(modSID1, "toolu_1", 3))
	r.round()
	wantOneEmit(t, "first ask", r.drain(t), "code-work", "waiting", "mod")
	feedMod(r.m, modStrm, modAsk(modSID1, "toolu_2", 4))
	r.m.modMu.Lock()
	r.m.modDirty[modSID1] = modevents.TypeToolCheck
	r.m.modMu.Unlock()
	r.round()
	if got := r.drain(t); len(got) != 0 {
		t.Fatalf("second ask re-sent waiting: %+v", got)
	}
}

// TestModWorker_MarksBothSidsOnSwitch: a /clear moves the stream from one
// sid to another; the panes of both are re-emitted.
func TestModWorker_MarksBothSidsOnSwitch(t *testing.T) {
	r := newWorkerRig(t)
	seedIdentityFrame(t, r.m, "%5", "cc", 501, "s501", 10, modSID1, "/w")
	seedIdentityFrame(t, r.m, "%6", "cc", 601, "s601", 20, modSID2, "/w")
	feedMod(r.m, modStrm, modStart, modTurnStart)
	r.round()
	wantOneEmit(t, "before the switch", r.drain(t), "code-work", "running", "mod")

	sw := modEv(modSID2, modevents.TypeSessionSwitch, `{"prev_sid":"`+modSID1+`","source":"clear"}`)
	sw.Seq = 3
	feedMod(r.m, modStrm, sw)
	r.round()

	got := r.drain(t)
	bySession := map[string]emitted{}
	for _, e := range got {
		bySession[e.Session] = e
	}
	if len(got) != 2 {
		t.Fatalf("%d emits, want one per session: %+v", len(got), got)
	}
	if e := bySession["code-work"]; e.Ev.Status != "idle" || e.Ev.Source != "hook" {
		t.Fatalf("old sid's pane = %s/%s, want idle/hook (it lost the overlay)", e.Ev.Status, e.Ev.Source)
	}
	if e := bySession["code-other"]; e.Ev.Status != "idle" || e.Ev.Source != "mod" {
		t.Fatalf("new sid's pane = %s/%s, want idle/mod (it gained the overlay)", e.Ev.Status, e.Ev.Source)
	}
}

// TestModWorker_EmitsWhenRepresentativeChangesWithEqualStatus: a newer pane
// of the same tmux session takes over as the representative with the very
// same light (running, mod, no dots); only the frame behind it differs, and
// that is still a change the SPA needs.
func TestModWorker_EmitsWhenRepresentativeChangesWithEqualStatus(t *testing.T) {
	r := newWorkerRig(t)
	seedIdentityFrame(t, r.m, "%5", "cc", 501, "s501", 10, modSID1, "/w")
	feedMod(r.m, modStrm, modStart, modTurnStart)
	r.round()
	wantOneEmit(t, "first pane", r.drain(t), "code-work", "running", "mod")

	seedIdentityFrame(t, r.m, "%7", "cc", 701, "s701", 20, modSID2, "/w")
	feedMod(r.m, "stream-test-0002",
		modEv(modSID2, modevents.TypeSessionStart, `{"cwd":"/w"}`),
		modEv(modSID2, modevents.TypeTurnStart, `{"turn_id":"t9"}`))
	r.round()

	wantOneEmit(t, "newer pane takes over", r.drain(t), "code-work", "running", "mod")
}

// TestModOverlay_StaleStreamEmitsOnceOnFlip: 30 s after its last event the
// stream still drives the pane; one tick later the pane is back on its hook
// status and the worker says so once.
func TestModOverlay_StaleStreamEmitsOnceOnFlip(t *testing.T) {
	r := newWorkerRig(t)
	seedIdentityFrame(t, r.m, "%5", "cc", 501, "s501", 10, modSID1, "/w")
	feedMod(r.m, modStrm, modStart, modTurnStart)
	r.round()
	wantOneEmit(t, "live", r.drain(t), "code-work", "running", "mod")

	r.clock.Set(modT0.Add(30 * time.Second))
	r.round()
	if got := r.drain(t); len(got) != 0 {
		t.Fatalf("emit while the stream is still live: %+v", got)
	}

	r.clock.Set(modT0.Add(31 * time.Second))
	r.round()
	e := wantOneEmit(t, "flip", r.drain(t), "code-work", "idle", "hook")
	if e.Ev.Detail["mod_event"] != "stale" {
		t.Fatalf("detail = %v, want mod_event stale", e.Ev.Detail)
	}

	r.clock.Set(modT0.Add(36 * time.Second))
	r.round()
	r.round()
	if got := r.drain(t); len(got) != 0 {
		t.Fatalf("the flip was sent again: %+v", got)
	}
}

// TestModWorker_RawEventNameIsMod: a worker frame is not a hook; its name is
// "mod" (no notification setting matches it) and detail says which mod event
// last touched the sid.
func TestModWorker_RawEventNameIsMod(t *testing.T) {
	r := newWorkerRig(t)
	seedIdentityFrame(t, r.m, "%5", "cc", 501, "s501", 10, modSID1, "/w")
	feedMod(r.m, modStrm, modStart, modTurnStart)
	r.round()

	e := wantOneEmit(t, "round", r.drain(t), "code-work", "running", "mod")
	if e.Ev.RawEventName != "mod" {
		t.Fatalf("raw_event_name = %q, want mod", e.Ev.RawEventName)
	}
	if e.Ev.Detail["mod_event"] != modevents.TypeTurnStart {
		t.Fatalf("detail = %v, want mod_event %s", e.Ev.Detail, modevents.TypeTurnStart)
	}
	if e.Ev.AgentType != "cc" {
		t.Fatalf("agent_type = %q, want cc", e.Ev.AgentType)
	}
}

// hookStopBody is the PdxStop of the pane %5 frame seeded with pid 200.
const hookStopBody = `{"tmux_session":"work","tmux_pane_id":"%5",` + nonTmuxTail + `,"raw_event":{}}`

// registerIdleHooks makes the cc provider answer every hook with idle.
func (r *workerRig) registerIdleHooks() {
	r.m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(string, json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
		},
	})
}

// TestModWorker_HookEmitIsTheBaseline: a hook emit records its light, so a
// round that finds the same light has nothing to say.
func TestModWorker_HookEmitIsTheBaseline(t *testing.T) {
	r := newWorkerRig(t)
	r.registerIdleHooks()
	seedIdentityFrame(t, r.m, "%5", "cc", 200, "Sun Apr 20 01:30:00 2026", 10, modSID1, "/w")
	if w := postEvent(r.m, hookStopBody); w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	wantOneEmit(t, "hook", r.drain(t), "code-work", "idle", "hook")

	r.m.modMu.Lock()
	r.m.modDirty[modSID1] = "heartbeat"
	r.m.modMu.Unlock()
	r.round()
	if got := r.drain(t); len(got) != 0 {
		t.Fatalf("the hook's light was re-sent: %+v", got)
	}
}

// TestModWorker_RenameMovesTheBaseline: a renamed session keeps the digest
// of what it last sent.
func TestModWorker_RenameMovesTheBaseline(t *testing.T) {
	r := newWorkerRig(t)
	seedIdentityFrame(t, r.m, "%5", "cc", 501, "s501", 10, modSID1, "/w")
	feedMod(r.m, modStrm, modStart, modTurnStart)
	r.round()
	r.drain(t)

	r.m.mu.Lock()
	r.m.renameSessionLocked("work", "renamed")
	_, old := r.m.lastEmittedLights["work"]
	_, moved := r.m.lastEmittedLights["renamed"]
	r.m.mu.Unlock()
	if old || !moved {
		t.Fatalf("digest after rename: old=%v new=%v, want it moved", old, moved)
	}
}
