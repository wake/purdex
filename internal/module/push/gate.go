package push

import (
	"crypto/sha256"
	"encoding/json"
	"sync"
	"time"

	"github.com/wake/purdex/internal/push"
)

// The agent-event gate (push spec §5.2): a mirror of the Mac's `shouldNotify` (spa/src/lib/notification-gate.ts), staged
// so that what is per event (0-4), what is per device (5-6) and what must happen once per event (7) are separate:
// a per-device debounce would silence every device after the first.
//
// Rules 8 (a waiting event that duplicates an AskUserQuestion push) and 9 (content exists) need the open hook_ask set
// and the content builder; the trigger applies them after Decide.

const (
	errorNotifyWindow = 60 * time.Second // ERROR_NOTIFY_WINDOW_MS
	maxDebounceKeys   = 1000             // MAX_DEBOUNCE_ENTRIES
	maxSeenSessions   = 4096             // freshness records kept; the oldest is evicted

	// clockStepBack: BroadcastTs is wall-clock nanoseconds. One that is older than the last seen by more than this is a
	// clock that was set back (NTP, resume from sleep, a manual change), not an out-of-order frame (those are
	// milliseconds apart); it is accepted, or every later event of the session would be dropped until the clock caught up.
	// The accepted trade-off: a frame that sat more than a minute between its hook's arrival and its broadcast would be
	// taken for a clock step too. A frame waits only for the serialised emit slot, so that is not expected; if the
	// emitter ever grows such a delay, freshness needs a generation from the producer instead of this inference.
	clockStepBack = int64(time.Minute)
)

// AgentEvent is one live tmux `hook` frame as the gate reads it.
type AgentEvent struct {
	AgentType   string
	SessionCode string
	SessionName string
	SessionID   string
	EventName   string // the frame's raw_event_name; PdxStop and Stop are the same event
	Status      string // the frame's derived status
	BroadcastTs int64
	Silent      bool   // detail.notification_silent == true
	ErrorString string // detail.error
}

// NormalizeEventName collapses the four user-facing notification events to their legacy form (one definition, shared
// with the content builder).
func NormalizeEventName(raw string) string { return push.NormalizeEventName(raw) }

// Gate holds the two pieces of state the rules need: the last BroadcastTs seen per session code (rule 0) and the error
// debounce (rule 7). Both are in memory; a daemon restart forgets them.
type Gate struct {
	mu        sync.Mutex
	now       func() time.Time
	seen      map[string]int64 // session code → last BroadcastTs
	debounce  map[string]debounceEntry
	nextSeq   uint64 // insertion order of debounce keys, so the oldest can be evicted at the cap
	lastSweep time.Time
}

type debounceEntry struct {
	until time.Time
	seq   uint64
	code  string // the session the key was made for (the key itself is a digest), for Forget
}

func NewGate(now func() time.Time) *Gate {
	if now == nil {
		now = time.Now
	}
	return &Gate{now: now, seen: map[string]int64{}, debounce: map[string]debounceEntry{}}
}

// Decide runs rules 0-7 and returns the devices to push to (none = no push). Rule 0 records the event's timestamp
// whatever the outcome, as the Mac does, so a re-emitted frame never pushes twice.
func (g *Gate) Decide(ev AgentEvent, devices []push.Device, shows func(code string) bool) []push.Device {
	ev.EventName = NormalizeEventName(ev.EventName)
	if !g.EventStage(ev, shows) {
		return nil
	}
	var recipients []push.Device
	for _, d := range devices {
		if DeviceWants(d, ev) {
			recipients = append(recipients, d)
		}
	}
	if len(recipients) == 0 {
		return nil // no recipients → no debounce entry (rule 7 runs only when someone would be told)
	}
	if !g.SendStage(ev) {
		return nil
	}
	return recipients
}

// EventStage is rules 0-4, once per event. ev.EventName must be normalised.
func (g *Gate) EventStage(ev AgentEvent, shows func(code string) bool) bool {
	if !g.fresh(ev.SessionCode, ev.BroadcastTs) { // 0
		return false
	}
	if ev.Status != "waiting" && ev.Status != "idle" && ev.Status != "error" { // 1
		return false
	}
	if ev.Silent { // 2
		return false
	}
	if ev.Status == "idle" && ev.EventName == "Notification" { // 3: informational
		return false
	}
	if shows != nil && shows(ev.SessionCode) { // 4: a present Mac shows it
		return false
	}
	return true
}

// DeviceWants is rules 5-6, per device. ev.EventName must be normalised.
func DeviceWants(d push.Device, ev AgentEvent) bool {
	p := d.Prefs.Agents[ev.AgentType]    // an agent the phone never mentioned has the Mac's defaults: on, no events off, tabs only
	if p.Enabled != nil && !*p.Enabled { // 5
		return false
	}
	if on, set := p.Events[ev.EventName]; set && !on {
		return false
	}
	if !p.NotifyWithoutTab { // 6
		for _, code := range d.Prefs.Tabs {
			if code == ev.SessionCode {
				return true
			}
		}
		return false
	}
	return true
}

// SendStage is rule 7, once per event: an error passes the daemon-wide debounce (trailing-edge sliding 60 s on
// session code + event + error string); any other status always passes.
func (g *Gate) SendStage(ev AgentEvent) bool {
	if ev.Status != "error" {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if now.Sub(g.lastSweep) >= errorNotifyWindow {
		for k, e := range g.debounce {
			if e.until.Before(now.Add(-5 * errorNotifyWindow)) {
				delete(g.debounce, k)
			}
		}
		g.lastSweep = now
	}
	key := debounceKey(ev.SessionCode, ev.EventName, ev.ErrorString) // a fixed-size digest: the error string is not retained
	e, known := g.debounce[key]
	if known && now.Before(e.until) {
		e.until = now.Add(errorNotifyWindow) // within the window: slide it and stay silent
		g.debounce[key] = e
		return false
	}
	if !known {
		if len(g.debounce) >= maxDebounceKeys { // the cap: evict the oldest key
			var oldest string
			var oldestSeq uint64
			for k, v := range g.debounce {
				if oldest == "" || v.seq < oldestSeq {
					oldest, oldestSeq = k, v.seq
				}
			}
			delete(g.debounce, oldest)
		}
		g.nextSeq++
		e.seq, e.code = g.nextSeq, ev.SessionCode
	}
	e.until = now.Add(errorNotifyWindow)
	g.debounce[key] = e
	return true
}

// fresh: ts is greater than the last one seen for the code, which it then becomes.
func (g *Gate) fresh(code string, ts int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if last, ok := g.seen[code]; ok && ts <= last && last-ts <= clockStepBack {
		return false
	}
	if _, ok := g.seen[code]; !ok && len(g.seen) >= maxSeenSessions {
		var oldest string
		var oldestTs int64
		for c, t := range g.seen {
			if oldest == "" || t < oldestTs {
				oldest, oldestTs = c, t
			}
		}
		delete(g.seen, oldest)
	}
	g.seen[code] = ts
	return true
}

// debounceKey identifies an error bucket: the SHA-256 of a JSON array of the three parts (the array escapes the
// separators a joined string could be confused by). A digest, so a huge error string costs 32 bytes of key, not itself.
func debounceKey(code, event, errorString string) string {
	b, _ := json.Marshal([]string{code, event, errorString})
	sum := sha256.Sum256(b)
	return string(sum[:])
}

// Forget drops everything the gate knows about a session code (it ended), so a reused code starts clean.
func (g *Gate) Forget(code string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.seen, code)
	for k, e := range g.debounce {
		if e.code == code {
			delete(g.debounce, k)
		}
	}
}

// DebounceLen and SeenLen are for tests and metrics.
func (g *Gate) DebounceLen() int { g.mu.Lock(); defer g.mu.Unlock(); return len(g.debounce) }
func (g *Gate) SeenLen() int     { g.mu.Lock(); defer g.mu.Unlock(); return len(g.seen) }
