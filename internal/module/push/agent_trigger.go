package push

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/workbooklines"
	"github.com/wake/purdex/internal/workbooksettings"
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
	sendWith := func(base push.AgentInput) {
		snd.Enqueue(Job{DeviceIDs: ids, Make: func(d push.Device) (push.Content, bool) {
			c := base
			c.HostLabel = d.HostLabel
			return push.AgentContent(c, d.Locale)
		}})
	}
	send := func() { sendWith(content) }
	if in.Status != "waiting" {
		// A Stop push is held for the session workbook's line of that turn (plan WB-3), when there is a workbook to ask.
		if m.holdForWorkbook(ev, content, sendWith) {
			return
		}
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

// maxWBHolds bounds the Stop pushes held for the workbook at once; one beyond it goes out at once, as today.
const maxWBHolds = 256

// holdForWorkbook holds a Stop / StopFailure push for the workbook's line of that turn: false (the caller sends now)
// for any other event, with no wait configured, with no workbook in the registry (looked up now, so start order does not
// matter), at the cap, or once the module is stopping. The hold is a goroutine of the wbHolds set; it sends with the line,
// or without it when the line does not come (deadline, a failed entry), and sends nothing once the module is stopped.
func (m *Module) holdForWorkbook(ev agent.NotifyEvent, content push.AgentInput, sendWith func(push.AgentInput)) bool {
	name := push.NormalizeEventName(ev.Event.RawEventName)
	if name != "Stop" && name != "StopFailure" {
		return false
	}
	wait := m.workbookWait()
	if wait <= 0 {
		return false
	}
	lines := m.workbookLines()
	if lines == nil {
		return false
	}
	sid := ev.SessionID
	since := ev.Event.BroadcastTs / int64(time.Millisecond) // the frame's stamp is wall-clock nanoseconds; the entry's turn_at is ms
	return m.wbHolds.start(func(ctx context.Context) {
		line, ok := lines.Await(ctx, sid, since, time.Now().Add(wait))
		if ctx.Err() != nil {
			return // stopped: nothing is enqueued
		}
		c := content
		if ok {
			c.Workbook = &push.WorkbookLine{Thing: line.Thing, Push: line.Push, ConvKey: line.ConvKey, EntryID: line.EntryID}
		}
		sendWith(c)
	})
}

// workbookWait is the host setting push_wait_s as a duration; 0 (no hold) without the setting or when it cannot be read.
func (m *Module) workbookWait() time.Duration {
	if m.wbWait != nil {
		return m.wbWait()
	}
	if m.core == nil || m.core.Registry == nil {
		return 0
	}
	svc, ok := m.core.Registry.Get(workbooksettings.Key)
	if !ok {
		return 0
	}
	r, ok := svc.(workbooksettings.Reader)
	if !ok {
		return 0
	}
	s, err := r.WorkbookSettings()
	if err != nil {
		return 0
	}
	return time.Duration(s.PushWaitS) * time.Second
}

// workbookLines is the workbook module's waiter, or nil when there is none (yet).
func (m *Module) workbookLines() workbooklines.Lines {
	if m.wbLines != nil {
		return m.wbLines()
	}
	if m.core == nil || m.core.Registry == nil {
		return nil
	}
	svc, ok := m.core.Registry.Get(workbooklines.Key)
	if !ok {
		return nil
	}
	l, _ := svc.(workbooklines.Lines)
	return l
}

// wbHolds is the set of goroutines holding Stop pushes for the workbook. Admission and stop are linearised by mu, so no
// goroutine starts after stop() has begun waiting, and none outlives stop().
type wbHoldSet struct {
	mu      sync.Mutex
	stopped bool
	n       int
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// reset opens the set for a new run (Start).
func (h *wbHoldSet) reset() {
	h.mu.Lock()
	h.stopped, h.n = false, 0
	h.ctx, h.cancel = context.WithCancel(context.Background())
	h.mu.Unlock()
}

// start runs fn on its own goroutine; false when stopped, never opened, or at the cap.
func (h *wbHoldSet) start(fn func(ctx context.Context)) bool {
	h.mu.Lock()
	if h.stopped || h.ctx == nil || h.n >= maxWBHolds {
		h.mu.Unlock()
		return false
	}
	h.n++
	h.wg.Add(1)
	ctx := h.ctx
	h.mu.Unlock()
	go func() {
		defer func() {
			h.mu.Lock()
			h.n--
			h.mu.Unlock()
			h.wg.Done()
		}()
		fn(ctx)
	}()
	return true
}

// stop cancels every hold and waits for them: nothing is enqueued by a hold after it returns.
func (h *wbHoldSet) stop() {
	h.mu.Lock()
	h.stopped = true
	cancel := h.cancel
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	h.wg.Wait()
}
