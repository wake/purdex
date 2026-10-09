package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/tmux"
)

func paneOnlyModule(t *testing.T) (*Module, *tmux.FakeExecutor) {
	t.Helper()
	m := newTestModule(t)
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(string, json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
		},
	})
	fake := tmux.NewFakeExecutor()
	fake.SetPaneSessionName("%9", "dev")
	m.tmux = fake
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fake}
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "dev-code", Name: "dev"}}}
	return m, fake
}

// hookSession is the session name the accepted event was broadcast under.
func hookSession(t *testing.T, m *Module, body string) string {
	t.Helper()
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)
	w := postEvent(m, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	select {
	case msg := <-sub.SendCh():
		var env struct{ Type, Session string }
		if err := json.Unmarshal(msg, &env); err != nil || env.Type != "hook" {
			t.Fatalf("message = %s (%v)", msg, err)
		}
		return env.Session
	case <-time.After(300 * time.Millisecond):
		t.Fatal("no hook broadcast")
		return ""
	}
}

const paneOnlyTail = `"sender_pid":99,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"PdxStop","raw_event":{},"agent_type":"cc"`

// #2123: a hook started where `tmux` is not on PATH (a pane that an ssh login without a login shell started) cannot name
// its tmux session; it still knows its pane. The event used to be refused as schema_invalid (and the hook swallows
// that), so the session never appeared. The daemon names the session from the pane.
func TestHandleEvent_PaneWithoutSessionNameIsNamedByTheDaemon(t *testing.T) {
	m, _ := paneOnlyModule(t)
	if got := hookSession(t, m, `{"tmux_session":"","tmux_pane_id":"%9",`+paneOnlyTail+`}`); got != "dev-code" {
		t.Fatalf("broadcast under %q, want the code of the pane's session dev", got)
	}
	if frames, err := m.frames.ListByPane("%9"); err != nil || len(frames) != 1 {
		t.Fatalf("frames = %+v err %v, want the pane's frame registered", frames, err)
	}
}

// A name the hook did send is trusted as before (the daemon does not ask tmux, whatever tmux would say).
func TestHandleEvent_ASessionNameFromTheHookIsNotOverruled(t *testing.T) {
	m, fake := paneOnlyModule(t)
	fake.SetPaneSessionName("%9", "something-else")
	if got := hookSession(t, m, `{"tmux_session":"dev","tmux_pane_id":"%9",`+paneOnlyTail+`}`); got != "dev-code" {
		t.Fatalf("broadcast under %q, want the code of the hook's own dev", got)
	}
}

// A pane the daemon cannot place stays refused: no invented identity.
func TestHandleEvent_AnUnplaceablePaneIsStillRefused(t *testing.T) {
	m, _ := paneOnlyModule(t)
	w := postEvent(m, `{"tmux_session":"","tmux_pane_id":"%404",`+paneOnlyTail+`}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "schema_invalid") {
		t.Fatalf("status = %d body=%s, want 400 schema_invalid", w.Code, w.Body.String())
	}
}

// A pane linked into several sessions cannot be told apart from the pane alone: refused, not guessed.
func TestHandleEvent_ALinkedPaneIsNotGuessed(t *testing.T) {
	m, fake := paneOnlyModule(t)
	fake.SetPaneAmbiguous("%9", true)
	w := postEvent(m, `{"tmux_session":"","tmux_pane_id":"%9",`+paneOnlyTail+`}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", w.Code, w.Body.String())
	}
}

// stuckPaneListing never answers the pane listing until the caller gives up.
type stuckPaneListing struct{ *tmux.FakeExecutor }

func (stuckPaneListing) ListPanePlacements(ctx context.Context) (map[string]tmux.PanePlacement, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// A stuck tmux must not hold the hook's request for ever.
func TestHandleEvent_AStuckTmuxDoesNotHoldTheRequest(t *testing.T) {
	m, fake := paneOnlyModule(t)
	m.tmux = stuckPaneListing{fake}
	done := make(chan int, 1)
	go func() { done <- postEvent(m, `{"tmux_session":"","tmux_pane_id":"%9",`+paneOnlyTail+`}`).Code }()
	select {
	case code := <-done:
		if code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", code)
		}
	case <-time.After(paneNameTimeout + 3*time.Second):
		t.Fatal("the request hung on tmux")
	}
}

// listingCounter counts the pane listings it is asked for.
type listingCounter struct {
	*tmux.FakeExecutor
	listings *atomic.Int32
}

func (c listingCounter) ListPanePlacements(ctx context.Context) (map[string]tmux.PanePlacement, error) {
	c.listings.Add(1)
	return c.FakeExecutor.ListPanePlacements(ctx)
}

// tmux is asked only for a request that already passes the cheap checks: no sender pid, no agent / event name, no start
// time and not uncertain -> no `list-panes -a`.
func TestHandleEvent_PaneNamingIsNotTriedForAnObviouslyBadRequest(t *testing.T) {
	m, fake := paneOnlyModule(t)
	var n atomic.Int32
	m.tmux = listingCounter{fake, &n}
	for name, body := range map[string]string{
		"no sender pid":     `{"tmux_session":"","tmux_pane_id":"%9","sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"PdxStop","raw_event":{},"agent_type":"cc"}`,
		"no event name":     `{"tmux_session":"","tmux_pane_id":"%9","sender_pid":99,"sender_start_time":"Sun Apr 20 01:30:00 2026","raw_event":{},"agent_type":"cc"}`,
		"no agent type":     `{"tmux_session":"","tmux_pane_id":"%9","sender_pid":99,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"PdxStop","raw_event":{}}`,
		"no start, certain": `{"tmux_session":"","tmux_pane_id":"%9","sender_pid":99,"purdex_name":"PdxStop","raw_event":{},"agent_type":"cc"}`,
	} {
		if w := postEvent(m, body); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", name, w.Code)
		}
	}
	if n.Load() != 0 {
		t.Fatalf("tmux was listed %d times for requests that fail the cheap checks", n.Load())
	}
}

func fillPaneNameSlots(t *testing.T) (release func()) {
	t.Helper()
	for i := 0; i < cap(paneNameSlots); i++ {
		paneNameSlots <- struct{}{}
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			for i := 0; i < cap(paneNameSlots); i++ {
				<-paneNameSlots
			}
		})
	}
	t.Cleanup(release)
	return release
}

// At most paneNameSlots listings at once: with every slot taken for longer than the wait, a further event is refused
// without asking tmux.
func TestHandleEvent_PaneNamingIsCapped(t *testing.T) {
	m, fake := paneOnlyModule(t)
	var n atomic.Int32
	m.tmux = listingCounter{fake, &n}
	orig := paneNameWait
	paneNameWait = 50 * time.Millisecond
	defer func() { paneNameWait = orig }()
	fillPaneNameSlots(t)
	if w := postEvent(m, `{"tmux_session":"","tmux_pane_id":"%9",`+paneOnlyTail+`}`); w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if n.Load() != 0 {
		t.Fatalf("tmux was listed %d times with every slot taken", n.Load())
	}
}

// ... but a burst that clears within the wait loses nothing: the event waits for a slot and is processed.
func TestHandleEvent_AShortBurstWaitsForASlotInsteadOfLosingTheEvent(t *testing.T) {
	m, _ := paneOnlyModule(t)
	release := fillPaneNameSlots(t)
	time.AfterFunc(100*time.Millisecond, release)
	if got := hookSession(t, m, `{"tmux_session":"","tmux_pane_id":"%9",`+paneOnlyTail+`}`); got != "dev-code" {
		t.Fatalf("broadcast under %q, want dev-code", got)
	}
}
