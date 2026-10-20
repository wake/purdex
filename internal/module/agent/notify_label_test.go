package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/notifylabel"
	"github.com/wake/purdex/internal/store"
)

// notify-title spec §3.1, §3.4: the event carries the label the daemon worked out, on the live frame and on the snapshot,
// from memory only.

type labelSources struct {
	mu      sync.Mutex
	gate    chan struct{} // when set, a read waits for it
	started chan struct{} // a read signals here when it begins (if set)
}

func (s *labelSources) sources() notifylabel.Sources {
	wait := func(ctx context.Context) {
		if s.started != nil {
			select {
			case s.started <- struct{}{}:
			default:
			}
		}
		if s.gate != nil {
			select {
			case <-s.gate:
			case <-ctx.Done():
			}
		}
	}
	return notifylabel.Sources{
		Code: func(ctx context.Context, sid string) (string, bool, error) {
			wait(ctx)
			if sid == modSID1 {
				return "mei", true, nil
			}
			return "", false, nil
		},
		Group: func(ctx context.Context, sid string) (string, error) {
			wait(ctx)
			if sid == modSID1 {
				return "Purdex A team", nil
			}
			return "", nil
		},
		Display: func(code string) (string, bool) {
			if code == "mei" {
				return "Mei", true
			}
			return "", false
		},
	}
}

// useLabeler gives the module a labeler on the fake sources.
func useLabeler(t *testing.T, m *Module, s *labelSources) *notifylabel.Labeler {
	t.Helper()
	l := notifylabel.New(s.sources())
	l.Start()
	t.Cleanup(l.Stop)
	m.labeler = l
	return l
}

// hookSIDBody is hookStopBody with the agent session id in the raw event, which is where the provider reads it from.
const hookSIDBody = `{"tmux_session":"work","tmux_pane_id":"%5",` + nonTmuxTail + `,"raw_event":{"session_id":"` + modSID1 + `"}}`

// registerIdentifying makes the cc provider answer every hook with idle and name the session its raw event carries.
func registerIdentifying(r *workerRig) {
	p := &fakeIdentifyingAgentProvider{}
	p.typeName = "cc"
	p.derive = func(string, json.RawMessage) agentpkg.DeriveResult {
		return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
	}
	r.m.registry.Register(p)
}

func hookEvents(t *testing.T, frames []emitted) []agentpkg.NormalizedEvent {
	t.Helper()
	var out []agentpkg.NormalizedEvent
	for _, f := range frames {
		out = append(out, f.Ev)
	}
	return out
}

// A warm label rides on the live hook frame and on the NotifyEvent the push module reads: one source, two consumers.
func TestNotifyLabel_LiveFrameCarriesItAndSoDoesTheNotifyEvent(t *testing.T) {
	r := newWorkerRig(t)
	registerIdentifying(r)
	seedIdentityFrame(t, r.m, "%5", "cc", 200, "Sun Apr 20 01:30:00 2026", 10, modSID1, "/w")
	l := useLabeler(t, r.m, &labelSources{})
	l.Label(modSID1, "work")
	if !l.WaitIdle(2 * time.Second) {
		t.Fatal("the background read did not finish")
	}
	got := make(chan NotifyEvent, 4)
	t.Cleanup(r.m.SubscribeNotify(func(ev NotifyEvent) { got <- ev }))

	if w := postEvent(r.m, hookSIDBody); w.Code != http.StatusOK {
		t.Fatalf("hook: %d", w.Code)
	}
	want := &notifylabel.Label{Name: "Mei", Group: "Purdex A team", Tmux: "work"}
	evs := hookEvents(t, r.drain(t))
	if len(evs) != 1 || evs[0].NotifyLabel == nil || *evs[0].NotifyLabel != *want {
		t.Fatalf("broadcast = %+v, want notify_label %+v", evs, want)
	}
	if ev := wantNotify(t, got); ev.Event.NotifyLabel == nil || *ev.Event.NotifyLabel != *want {
		t.Fatalf("notify event label = %+v", ev.Event.NotifyLabel)
	}
}

// A cold cache sends no label (the title falls back to today's), and asks for one read in the background.
func TestNotifyLabel_ColdCacheSendsNone(t *testing.T) {
	r := newWorkerRig(t)
	registerIdentifying(r)
	seedIdentityFrame(t, r.m, "%5", "cc", 200, "Sun Apr 20 01:30:00 2026", 10, modSID1, "/w")
	l := useLabeler(t, r.m, &labelSources{})
	if w := postEvent(r.m, hookSIDBody); w.Code != http.StatusOK {
		t.Fatalf("hook: %d", w.Code)
	}
	evs := hookEvents(t, r.drain(t))
	if len(evs) != 1 || evs[0].NotifyLabel != nil {
		t.Fatalf("cold broadcast = %+v, want no notify_label", evs)
	}
	if !l.WaitIdle(2 * time.Second) {
		t.Fatal("the background read did not finish")
	}
	if got := l.Label(modSID1, "work"); got.Name != "Mei" {
		t.Fatalf("the read did not land: %+v", got)
	}
}

// A session with neither a persona nor a team has no label at all (not an empty one).
func TestNotifyLabel_NothingKnownSendsNone(t *testing.T) {
	r := newWorkerRig(t)
	r.registerIdleHooks()
	seedIdentityFrame(t, r.m, "%5", "cc", 200, "Sun Apr 20 01:30:00 2026", 10, "unknown-sid", "/w")
	l := useLabeler(t, r.m, &labelSources{})
	l.Label("unknown-sid", "work")
	l.WaitIdle(2 * time.Second)
	postEvent(r.m, strings.Replace(hookSIDBody, modSID1, "unknown-sid", 1))
	for _, n := range hookEvents(t, r.drain(t)) {
		if n.NotifyLabel != nil {
			t.Fatalf("label = %+v", n.NotifyLabel)
		}
	}
}

// The emitter holds the emit slot while it labels; a source parked on I/O must not hold it (spec §3.1: no I/O in the
// emitter's lock). Frames keep going out while the read is stuck. Mutation gate: read the sources inside Label → red.
func TestNotifyLabel_SlowSourceDoesNotBlockTheBroadcast(t *testing.T) {
	r := newWorkerRig(t)
	registerIdentifying(r)
	seedIdentityFrame(t, r.m, "%5", "cc", 200, "Sun Apr 20 01:30:00 2026", 10, modSID1, "/w")
	src := &labelSources{gate: make(chan struct{}), started: make(chan struct{}, 1)}
	useLabeler(t, r.m, src)
	defer close(src.gate)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 20; i++ {
			postEvent(r.m, hookSIDBody)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the broadcast waited for a parked label read")
	}
	select {
	case <-src.started:
	case <-time.After(2 * time.Second):
		t.Fatal("no read was asked for")
	}
	for _, n := range hookEvents(t, r.drain(t)) {
		if n.NotifyLabel != nil {
			t.Fatalf("a label came out of a parked read: %+v", n.NotifyLabel)
		}
	}
}

// The subscribe-time snapshot carries the same label as the live frame, for a session with a frame projection.
func TestNotifyLabel_SnapshotCarriesItToo(t *testing.T) {
	r, sub := v2Rig(t)
	r.registerIdleHooks()
	seedIdentityFrame(t, r.m, "%5", "cc", 200, "Sun Apr 20 01:30:00 2026", 10, modSID1, "/w")
	seedIdentityFrame(t, r.m, "%6", "cc", 201, "Sun Apr 20 01:30:01 2026", 11, modSID2, "/o")
	l := useLabeler(t, r.m, &labelSources{})
	l.Label(modSID1, "work")
	l.Label(modSID2, "other")
	l.WaitIdle(2 * time.Second)

	r.m.sendSnapshot(sub)
	f, _ := oneSnapshot(t, sub)
	e, ok := entryFor(f, "code-work")
	if !ok || e.Event.NotifyLabel == nil || *e.Event.NotifyLabel != (notifylabel.Label{Name: "Mei", Group: "Purdex A team", Tmux: "work"}) {
		t.Fatalf("work entry = %+v ok=%v", e, ok)
	}
	if o, ok := entryFor(f, "code-other"); !ok || o.Event.NotifyLabel != nil {
		t.Fatalf("a session with nothing known carries no label: %+v", o)
	}
}

// A session id that only the stored legacy event knows (no frame projection) is read from the event's session_id.
func TestNotifyLabel_SnapshotLegacyRowUsesTheStoredSessionID(t *testing.T) {
	r, sub := v2Rig(t)
	r.registerIdleHooks()
	l := useLabeler(t, r.m, &labelSources{})
	l.Label(modSID1, "other")
	l.WaitIdle(2 * time.Second)
	raw := json.RawMessage(`{"session_id":"` + modSID1 + `"}`)
	if err := r.m.events.Set("other", "PdxStop", raw, "cc", 1); err != nil {
		t.Fatal(err)
	}
	r.m.sendSnapshot(sub)
	f, _ := oneSnapshot(t, sub)
	e, ok := entryFor(f, "code-other")
	if !ok || e.Event.NotifyLabel == nil || e.Event.NotifyLabel.Name != "Mei" || e.Event.NotifyLabel.Tmux != "other" {
		t.Fatalf("legacy entry = %+v ok=%v", e, ok)
	}
}

// The session id of a frame is one rule for the notify feed and the label: a mod / sweep / probe frame's projection
// (top frame, then primary), a hook's own id.
func TestNotifySessionID(t *testing.T) {
	proj := &SessionProjection{TopFrame: &store.Frame{SessionID: "top"}, PrimaryFrame: &store.Frame{SessionID: "primary"}}
	for _, c := range []struct {
		kind slotKind
		hook string
		p    *SessionProjection
		want string
	}{
		{kindSweep, "", proj, "top"},
		{kindProbe, "", &SessionProjection{PrimaryFrame: &store.Frame{SessionID: "primary"}}, "primary"},
		{kindWorker, "", nil, ""},
		{kindHook, "from-hook", proj, "from-hook"},
		{kindNonTmux, "x", proj, ""},
	} {
		if got := notifySessionID(c.kind, c.hook, c.p); got != c.want {
			t.Errorf("notifySessionID(%v, %q) = %q, want %q", c.kind, c.hook, got, c.want)
		}
	}
}
