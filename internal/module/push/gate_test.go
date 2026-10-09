package push

import (
	"fmt"
	"testing"
	"time"

	"github.com/wake/purdex/internal/push"
)

// PU-3 Task 3: the agent-event gate (spec §5.2), the Mac's shouldNotify ported as a table plus the daemon-only rows
// (freshness, presence, two devices and the error debounce).

func boolp(b bool) *bool { return &b }

func gdev(id string, agents map[string]push.AgentPrefs, tabs ...string) push.Device {
	return push.Device{DeviceID: id, Prefs: push.Prefs{Agents: agents, Tabs: tabs}}
}

func ev(status, name string) AgentEvent {
	return AgentEvent{AgentType: "cc", SessionCode: "c1", SessionName: "dev", EventName: name, Status: status, BroadcastTs: 100}
}

func decide(g *Gate, e AgentEvent, devs []push.Device, shows func(string) bool) int {
	return len(g.Decide(e, devs, shows))
}

func newTestGate() (*Gate, *fakeClock) {
	c := &fakeClock{now: time.Unix(2_000_000, 0)}
	return NewGate(c.Now), c
}

// The Mac's `shouldNotify` cases (hooks/useNotificationDispatcher.test.ts), one row each, with the device standing
// for the Mac's settings and "has a tab" for tab membership. The Mac's "window has focus and shows it" row is the
// daemon's presence rule, tested below.
func TestGate_MacShouldNotifyCases(t *testing.T) {
	on := map[string]push.AgentPrefs{"cc": {}}
	cases := []struct {
		name   string
		mut    func(*AgentEvent)
		agents map[string]push.AgentPrefs
		tabs   []string
		want   bool
	}{
		{"waiting with a tab", nil, on, []string{"c1"}, true},
		{"idle", func(e *AgentEvent) { e.Status = "idle"; e.EventName = "Stop" }, on, []string{"c1"}, true},
		{"notification_silent", func(e *AgentEvent) { e.Silent = true }, on, []string{"c1"}, false},
		{"running", func(e *AgentEvent) { e.Status = "running" }, on, []string{"c1"}, false},
		{"no tab, notify_without_tab off", nil, on, nil, false},
		{"no tab, notify_without_tab on", nil, map[string]push.AgentPrefs{"cc": {NotifyWithoutTab: true}}, nil, true},
		{"another session's tab", nil, on, []string{"other"}, false},
		{"agent disabled", nil, map[string]push.AgentPrefs{"cc": {Enabled: boolp(false)}}, []string{"c1"}, false},
		{"agent enabled explicitly", nil, map[string]push.AgentPrefs{"cc": {Enabled: boolp(true)}}, []string{"c1"}, true},
		{"event disabled", nil, map[string]push.AgentPrefs{"cc": {Events: map[string]bool{"PermissionRequest": false}}}, []string{"c1"}, false},
		{"event defaults to true when not in the map", nil, map[string]push.AgentPrefs{"cc": {Events: map[string]bool{"Stop": false}}}, []string{"c1"}, true},
		{"idle Notification is informational", func(e *AgentEvent) { e.Status = "idle"; e.EventName = "Notification" }, on, []string{"c1"}, false},
		{"waiting Notification", func(e *AgentEvent) { e.EventName = "Notification" }, on, []string{"c1"}, true},
		{"error StopFailure", func(e *AgentEvent) { e.Status = "error"; e.EventName = "StopFailure" }, on, []string{"c1"}, true},
		{"idle PdxNotification suppressed like Notification (W2)", func(e *AgentEvent) { e.Status = "idle"; e.EventName = "PdxNotification" }, on, []string{"c1"}, false},
		{"waiting PdxPermissionRequest like PermissionRequest (W2)", func(e *AgentEvent) { e.EventName = "PdxPermissionRequest" }, on, []string{"c1"}, true},
		{"error PdxStopFailure like StopFailure (W2)", func(e *AgentEvent) { e.Status = "error"; e.EventName = "PdxStopFailure" }, on, []string{"c1"}, true},
		{"a legacy events key disables the PdxXxx event (W2)", func(e *AgentEvent) { e.EventName = "PdxPermissionRequest" }, map[string]push.AgentPrefs{"cc": {Events: map[string]bool{"PermissionRequest": false}}}, []string{"c1"}, false},
		{"an agent the phone never mentioned has the defaults", func(e *AgentEvent) { e.AgentType = "codex" }, on, []string{"c1"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, _ := newTestGate()
			e := ev("waiting", "PermissionRequest")
			if c.mut != nil {
				c.mut(&e)
			}
			got := decide(g, e, []push.Device{gdev("d1", c.agents, c.tabs...)}, nil)
			if (got == 1) != c.want {
				t.Fatalf("recipients = %d, want pushed=%v", got, c.want)
			}
		})
	}
}

// Rule 0: only a BroadcastTs greater than the last seen for the session code pushes. Mutation gate: drop the check
// (or use >=) → red.
func TestGate_Freshness(t *testing.T) {
	g, _ := newTestGate()
	devs := []push.Device{gdev("d1", nil, "c1")}
	e := ev("waiting", "PermissionRequest")
	if decide(g, e, devs, nil) != 1 {
		t.Fatal("the first event did not push")
	}
	if decide(g, e, devs, nil) != 0 {
		t.Fatal("the same BroadcastTs pushed again (a sweep or probe re-emit)")
	}
	e.BroadcastTs = 99
	if decide(g, e, devs, nil) != 0 {
		t.Fatal("an older BroadcastTs pushed")
	}
	e.BroadcastTs = 101
	if decide(g, e, devs, nil) != 1 {
		t.Fatal("a newer BroadcastTs did not push")
	}
	other := e
	other.SessionCode, other.BroadcastTs = "c2", 5
	if decide(g, other, []push.Device{gdev("d1", nil, "c2")}, nil) != 1 {
		t.Fatal("freshness leaked across session codes")
	}
}

// The timestamp is recorded even when the event is not pushed, as the Mac's dedup does: a suppressed event, then the
// same frame again after the suppression ended, does not push.
func TestGate_FreshnessIsRecordedEvenWhenTheEventIsFilteredOut(t *testing.T) {
	g, _ := newTestGate()
	devs := []push.Device{gdev("d1", nil, "c1")}
	e := ev("waiting", "PermissionRequest")
	if decide(g, e, devs, func(string) bool { return true }) != 0 { // a present Mac shows it
		t.Fatal("pushed while a Mac shows the session")
	}
	if decide(g, e, devs, func(string) bool { return false }) != 0 {
		t.Fatal("the same frame pushed after the Mac went away")
	}
}

// Rule 4: a present Mac showing the session code suppresses the push; one showing another does not.
func TestGate_PresenceSuppressesByCode(t *testing.T) {
	g, _ := newTestGate()
	devs := []push.Device{gdev("d1", nil, "c1")}
	e := ev("waiting", "PermissionRequest")
	if decide(g, e, devs, func(code string) bool { return code == "c1" }) != 0 {
		t.Fatal("pushed while a present Mac shows c1")
	}
	e.BroadcastTs = 200
	if decide(g, e, devs, func(code string) bool { return code == "other" }) != 1 {
		t.Fatal("suppressed by a Mac that shows another session")
	}
}

// Two eligible devices get the same error event; the next identical error within 60 s reaches neither. Mutation
// gate: debounce per device (inside the device loop) → the second device is dropped → red.
func TestGate_TwoDevicesShareOneErrorDebounce(t *testing.T) {
	g, c := newTestGate()
	devs := []push.Device{gdev("d1", nil, "c1"), gdev("d2", nil, "c1")}
	e := ev("error", "StopFailure")
	e.ErrorString = "rate_limit"
	if got := decide(g, e, devs, nil); got != 2 {
		t.Fatalf("recipients = %d, want both devices on the first error", got)
	}
	c.advance(10 * time.Second)
	e.BroadcastTs = 101
	if got := decide(g, e, devs, nil); got != 0 {
		t.Fatalf("recipients = %d, want neither on the next identical error", got)
	}
}

// The window slides (trailing edge) and then ends; different keys are independent; waiting is never debounced.
func TestGate_ErrorDebounceWindow(t *testing.T) {
	devs := []push.Device{gdev("d1", nil, "c1")}
	errEv := func(ts int64, msg string) AgentEvent {
		e := ev("error", "StopFailure")
		e.BroadcastTs, e.ErrorString = ts, msg
		return e
	}
	g, c := newTestGate()
	if decide(g, errEv(1, "x"), devs, nil) != 1 {
		t.Fatal("first error")
	}
	c.advance(30 * time.Second)
	if decide(g, errEv(2, "x"), devs, nil) != 0 { // silenced, window now runs to t+90
		t.Fatal("second error within the window")
	}
	c.advance(35 * time.Second) // t+65: past the original window, inside the slid one
	if decide(g, errEv(3, "x"), devs, nil) != 0 {
		t.Fatal("the window did not slide")
	}
	c.advance(61 * time.Second)
	if decide(g, errEv(4, "x"), devs, nil) != 1 {
		t.Fatal("an error after the silence window")
	}
	if decide(g, errEv(5, "other error"), devs, nil) != 1 {
		t.Fatal("a different error string shares the debounce")
	}
	other := errEv(6, "x")
	other.EventName = "Stop"
	if decide(g, other, devs, nil) != 1 {
		t.Fatal("a different event name shares the debounce")
	}
	for i := int64(0); i < 5; i++ { // waiting is never debounced
		w := ev("waiting", "PermissionRequest")
		w.BroadcastTs = 1000 + i
		if decide(g, w, devs, nil) != 1 {
			t.Fatalf("waiting event %d was debounced", i)
		}
	}
}

// Rule 7 runs only when a device would be told: an error nobody wants leaves no debounce entry, so the first device
// to want it later is not silenced by it. Mutation gate: debounce before the device stage → red.
func TestGate_NoRecipientsLeavesNoDebounceEntry(t *testing.T) {
	g, _ := newTestGate()
	e := ev("error", "StopFailure")
	e.ErrorString = "x"
	if decide(g, e, []push.Device{gdev("d1", nil)}, nil) != 0 { // no tab, no notify_without_tab
		t.Fatal("pushed without a tab")
	}
	if g.DebounceLen() != 0 {
		t.Fatal("a debounce entry was made with no recipient")
	}
	e.BroadcastTs = 101
	if decide(g, e, []push.Device{gdev("d1", nil, "c1")}, nil) != 1 {
		t.Fatal("an error nobody wanted silenced the next one")
	}
}

// Mutation gate: key built by joining with a separator → the colliding pair is silenced → red.
func TestGate_DebounceKeyHasNoSeparatorCollision(t *testing.T) {
	if debounceKey("a|b", "c", "d") == debounceKey("a", "b|c", "d") {
		t.Fatal("keys collide")
	}
}

// At most 1000 debounce keys; the oldest is evicted. Mutation gate: no cap → red; evict the newest → red.
func TestGate_DebounceHoldsAtMost1000KeysAndEvictsTheOldest(t *testing.T) {
	g, _ := newTestGate()
	devs := []push.Device{gdev("d1", map[string]push.AgentPrefs{"cc": {NotifyWithoutTab: true}})}
	for i := 0; i < maxDebounceKeys+50; i++ {
		e := ev("error", "StopFailure")
		e.SessionCode = fmt.Sprintf("s%d", i)
		e.BroadcastTs = 1
		e.ErrorString = "x"
		if decide(g, e, devs, nil) != 1 {
			t.Fatalf("error %d did not push", i)
		}
	}
	if n := g.DebounceLen(); n != maxDebounceKeys {
		t.Fatalf("debounce keys = %d, want %d", n, maxDebounceKeys)
	}
	oldest := ev("error", "StopFailure")
	oldest.SessionCode, oldest.BroadcastTs, oldest.ErrorString = "s0", 2, "x"
	if decide(g, oldest, devs, nil) != 1 {
		t.Fatal("the oldest key was kept (its repeat was silenced)")
	}
	newest := ev("error", "StopFailure")
	newest.SessionCode, newest.BroadcastTs, newest.ErrorString = fmt.Sprintf("s%d", maxDebounceKeys+49), 2, "x"
	if decide(g, newest, devs, nil) != 0 {
		t.Fatal("the newest key was evicted")
	}
}

// Stale entries are swept (at most once per window), and Forget clears a session's state.
func TestGate_SweepAndForget(t *testing.T) {
	g, c := newTestGate()
	devs := []push.Device{gdev("d1", map[string]push.AgentPrefs{"cc": {NotifyWithoutTab: true}})}
	e := ev("error", "StopFailure")
	e.ErrorString = "x"
	decide(g, e, devs, nil)
	c.advance(7 * time.Minute) // older than five windows
	e2 := ev("error", "StopFailure")
	e2.SessionCode, e2.BroadcastTs, e2.ErrorString = "c2", 1, "y"
	decide(g, e2, devs, nil) // triggers the sweep
	if g.DebounceLen() != 1 {
		t.Fatalf("debounce keys = %d, want the stale one swept", g.DebounceLen())
	}
	g.Forget("c2")
	if g.DebounceLen() != 0 || g.SeenLen() != 1 {
		t.Fatalf("after Forget: debounce %d seen %d", g.DebounceLen(), g.SeenLen())
	}
	e2.BroadcastTs = 1 // same stamp as before the Forget
	if decide(g, e2, devs, nil) != 1 {
		t.Fatal("a forgotten session still remembers its timestamp")
	}
}

// The freshness table is bounded too. Mutation gate: no cap → red.
func TestGate_SeenTableIsBounded(t *testing.T) {
	g, _ := newTestGate()
	devs := []push.Device{gdev("d1", map[string]push.AgentPrefs{"cc": {NotifyWithoutTab: true}})}
	for i := 0; i < maxSeenSessions+10; i++ {
		e := ev("waiting", "PermissionRequest")
		e.SessionCode, e.BroadcastTs = fmt.Sprintf("s%d", i), int64(i+1)
		decide(g, e, devs, nil)
	}
	if g.SeenLen() != maxSeenSessions {
		t.Fatalf("seen = %d, want %d", g.SeenLen(), maxSeenSessions)
	}
}
