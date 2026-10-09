package push

import (
	"fmt"
	"sync"
	"time"

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/push"
)

// The agent-event trigger (push spec §5.2): the agent module's live tmux hook frames, through the gate, to the phones.

// waitingHold is how long a `waiting` event waits before its duplicate check (rule 8): the hook_ask `opened` of the
// same question comes through the team feed, which is not ordered with the agent hub.
const waitingHold = 2 * time.Second

// maxHolds bounds the waiting events held at once; one beyond it is pushed at once, without the duplicate check
// (a possible duplicate is better than a lost "your agent is waiting").
const maxHolds = 256

// onNotify runs on the agent hub's subscriber goroutine for this module, never under the emitter's lock. It must not
// wait: the hold is a timer, not a sleep.
func (m *Module) onNotify(ev agent.NotifyEvent) {
	snd := m.sender.Load()
	if snd == nil {
		return
	}
	arrival := m.asks.Now()               // the start of this event's window for rule 8
	windowEnd := arrival.Add(waitingHold) // its end is fixed now: a timer that runs late does not widen the window
	in := AgentEvent{
		AgentType: ev.Event.AgentType, SessionCode: ev.SessionCode, SessionName: ev.SessionName, SessionID: ev.SessionID,
		EventName: ev.Event.RawEventName, Status: ev.Event.Status, BroadcastTs: ev.Event.BroadcastTs,
		Silent: ev.Event.Detail["notification_silent"] == true, ErrorString: jsString(ev.Event.Detail["error"]),
	}
	content := push.AgentInput{SessionCode: ev.SessionCode, SessionID: ev.SessionID, SessionName: ev.SessionName, EventName: ev.Event.RawEventName, Detail: ev.Event.Detail}
	// Rules 0-7 first, for every frame: freshness (rule 0) records a probe's or sweep's newer timestamp too, so an older
	// frame that arrives late is not pushed behind it. Rule 9 (there is something to say) comes last, as in the spec.
	recipients := m.gate.Decide(in, m.snapshot(), m.presence.ShowsCode)
	if len(recipients) == 0 {
		return
	}
	if _, has := push.AgentContent(content, "en"); !has {
		return
	}
	ids := make([]string, len(recipients))
	for i, d := range recipients {
		ids[i] = d.DeviceID
	}
	// The sender of this run, not whatever is current when a hold ends: a held event of a run that was stopped must not
	// be delivered by the next run (enqueueing onto a stopped sender is harmless).
	send := func() {
		snd.Enqueue(Job{DeviceIDs: ids, Make: func(d push.Device) (push.Content, bool) {
			c := content
			c.HostLabel = d.HostLabel
			return push.AgentContent(c, d.Locale)
		}})
	}
	if in.Status != "waiting" {
		send()
		return
	}
	// Rule 8: a waiting event is held, then dropped if an AskUserQuestion of the same session was open at any moment
	// between its arrival and arrival + 2 s: that is the question this event is about, and it already pushed.
	// An ask that was opened and answered before the event arrived does not hide it.
	if !m.holds.after(m.holdFor, func() {
		if !m.asks.Overlaps(ev.SessionID, ev.SessionName, arrival, windowEnd) {
			send()
		}
	}) {
		send()
	}
}

// jsString mirrors the Mac's String(detail.error, defaulting to an empty text): "" for nothing, the text for a string,
// a printed value otherwise.
func jsString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

// holdSet is the timers of waiting events being held; stopAll cancels them at Stop.
type holdSet struct {
	mu      sync.Mutex
	timers  map[*time.Timer]struct{}
	stopped bool
}

// after runs fn after d on its own goroutine. False when it cannot hold (at the cap, or stopped): the caller decides.
func (h *holdSet) after(d time.Duration, fn func()) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopped || len(h.timers) >= maxHolds {
		return false
	}
	if h.timers == nil {
		h.timers = map[*time.Timer]struct{}{}
	}
	var t *time.Timer
	t = time.AfterFunc(d, func() {
		h.mu.Lock()
		delete(h.timers, t)
		stopped := h.stopped
		h.mu.Unlock()
		if !stopped {
			fn()
		}
	})
	h.timers[t] = struct{}{}
	return true
}

func (h *holdSet) reset() {
	h.mu.Lock()
	h.stopped = false
	h.mu.Unlock()
}

func (h *holdSet) stopAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopped = true
	for t := range h.timers {
		t.Stop()
	}
	h.timers = nil
}
