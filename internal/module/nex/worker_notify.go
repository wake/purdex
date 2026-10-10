package nex

import (
	"encoding/json"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// WorkerNotifyKey is the service-registry key under which the nex module publishes its WorkerNotifyFeed (the push
// module will read it in PW-2; nex does not import push).
const WorkerNotifyKey = "nex.worker-notify-feed"

// workerNotifyFieldRunes caps each free-text field of an event, so a full queue holds a bounded amount.
const workerNotifyFieldRunes = 256

// clipRunes cuts s to workerNotifyFieldRunes runes, never inside a rune and with no ellipsis.
func clipRunes(s string) string {
	if len(s) <= workerNotifyFieldRunes { // bytes >= runes: short enough either way
		return s
	}
	if r := []rune(s); len(r) > workerNotifyFieldRunes {
		return string(r[:workerNotifyFieldRunes])
	}
	return s
}

// WorkerNotifyEvent is one worker status transition worth telling a person about. Every field comes from the row
// the projector just pushed. Not filled here, left to PW-2: the title fallback (exec-<6 chars>), Markdown/length
// normalisation, the provider→agent-type map, the exec-<id> code, collapse id and the user's preferences.
type WorkerNotifyEvent struct {
	ExecID    string
	Status    string // "waiting" | "idle" | "error"
	DedupKey  string // see classifyWorker
	RequestID string // waiting only: the pending permission request
	ToolName  string // waiting only: pending_permission.tool_name
	Title     string // session_title.text, raw ("" when none)
	Brief     string // the row's brief, raw: PW-2's title fallback
	Provider  string
	Reason    string // error only: last_turn_reason || terminal_reason || reject_reason || state
	TurnCount int64
	Stamp     int64 // unix milliseconds when the transition was seen
}

// WorkerNotifyFeed is the narrow view of the nex module a consumer of worker transitions gets.
type WorkerNotifyFeed interface {
	SubscribeWorkerNotify(fn func(WorkerNotifyEvent)) (unsubscribe func())
}

// SubscribeWorkerNotify registers fn for every worker transition, under workerNotifyHub's delivery contract. fn
// runs on the subscriber's own goroutine and must not block: a stuck fn fills its queue (events are then dropped)
// and holds up Stop for up to workerNotifyCloseWait.
func (m *Module) SubscribeWorkerNotify(fn func(WorkerNotifyEvent)) func() {
	return m.workerHub.subscribe(fn)
}

// workerNotifySubBuffer is the capacity of each subscriber's queue.
const workerNotifySubBuffer = 64

// workerNotifyHub is agent.notifyHub's shape for worker transitions: every subscriber owns a fixed-capacity channel
// and one recovered consumer goroutine; publish never waits (a full queue drops the event, counted) because it runs
// inside the read slot. A subscriber's fn must not block: close waits for it only workerNotifyCloseWait. close ends every consumer; after it publish and subscribe do nothing.
type workerNotifyHub struct {
	mu      sync.Mutex
	next    int
	subs    map[int]chan WorkerNotifyEvent
	closed  bool
	wg      sync.WaitGroup
	dropped atomic.Int64
	logging atomic.Bool
	// closeWait bounds close's wait for the consumers; zero means workerNotifyCloseWait.
	closeWait time.Duration
	gaveUp    bool // a close ran out its wait: later closes return at once
}

// workerNotifyCloseWait is how long close waits for subscriber goroutines (projectorStopWait's order of magnitude).
const workerNotifyCloseWait = 5 * time.Second

func (h *workerNotifyHub) subscribe(fn func(WorkerNotifyEvent)) func() {
	ch := make(chan WorkerNotifyEvent, workerNotifySubBuffer)
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return func() {}
	}
	if h.subs == nil {
		h.subs = map[int]chan WorkerNotifyEvent{}
	}
	id := h.next
	h.next++
	h.subs[id] = ch
	h.wg.Add(1)
	h.mu.Unlock()
	go func() {
		defer h.wg.Done()
		for ev := range ch {
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("[nex] worker notify subscriber panic: %v", r)
					}
				}()
				fn(ev)
			}()
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			if c, ok := h.subs[id]; ok {
				delete(h.subs, id)
				close(c)
			}
			h.mu.Unlock()
		})
	}
}

func (h *workerNotifyHub) publish(ev WorkerNotifyEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subs {
		select {
		case ch <- ev:
		default:
			if n := h.dropped.Add(1); (n == 1 || n%100 == 0) && h.logging.CompareAndSwap(false, true) {
				go func() {
					log.Printf("[nex] worker notify subscriber queue full (cap %d); %d event(s) dropped so far", workerNotifySubBuffer, h.dropped.Load())
					h.logging.Store(false)
				}()
			}
		}
	}
}

// has reports whether anyone listens, so a publisher can skip building an event.
func (h *workerNotifyHub) has() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs) > 0
}

// close ends every subscriber and waits for their goroutines (queued events are still delivered first).
func (h *workerNotifyHub) close() {
	h.mu.Lock()
	h.closed = true
	for id, ch := range h.subs {
		delete(h.subs, id)
		close(ch)
	}
	wait := h.closeWait
	gaveUp := h.gaveUp
	h.mu.Unlock()
	if gaveUp { // an earlier close already waited out the budget: do not wait a second time
		return
	}
	if wait <= 0 {
		wait = workerNotifyCloseWait
	}
	done := make(chan struct{})
	go func() { h.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(wait):
		h.mu.Lock()
		h.gaveUp = true
		h.mu.Unlock()
		log.Printf("[nex] worker notify: a subscriber is still running %v after close; not waiting for it", wait)
	}
}

// Dropped is how many (subscriber, event) deliveries were lost to a full queue.
func (h *workerNotifyHub) Dropped() int64 { return h.dropped.Load() }

// notifyWorker runs the classifier for one pushed read and publishes a transition. It runs inside the read slot:
// memory work only. No hub, no subscriber, a removed row, or no earlier entry (a baseline) publish nothing. The
// seed never comes through here, so the rows it records are the baseline the first real read is compared with.
func (p *projector) notifyWorker(id string, prev pushedRow, had bool, rec pushedRow, row json.RawMessage) {
	if p.notify == nil || rec.removed || !p.notify.has() {
		return
	}
	var before *rowDigest
	if had && !prev.removed && !prev.unparsed {
		before = &prev.digest
	}
	status, key, ok := classifyWorker(id, before, rec.digest)
	if !ok {
		return
	}
	var r struct {
		PendingPermission *struct {
			ToolName string `json:"tool_name"`
		} `json:"pending_permission"`
		SessionTitle *struct {
			Text string `json:"text"`
		} `json:"session_title"`
		Brief        string `json:"brief"`
		Provider     string `json:"provider"`
		RejectReason string `json:"reject_reason"`
	}
	_ = json.Unmarshal(row, &r) // the digest already parsed this row; a failure leaves the optional fields blank
	d := rec.digest
	ev := WorkerNotifyEvent{ExecID: id, Status: string(status), DedupKey: key, Brief: clipRunes(r.Brief), Provider: clipRunes(r.Provider),
		TurnCount: d.TurnCount, Stamp: p.now().UnixMilli()}
	if r.SessionTitle != nil {
		ev.Title = clipRunes(r.SessionTitle.Text)
	}
	switch status {
	case workerWaiting:
		ev.RequestID = d.PermissionRequest
		if r.PendingPermission != nil {
			ev.ToolName = clipRunes(r.PendingPermission.ToolName)
		}
	case workerError:
		for _, s := range []string{d.LastTurnReason, d.TerminalReason, r.RejectReason, d.State} {
			if s != "" {
				ev.Reason = clipRunes(s)
				break
			}
		}
	}
	p.notify.publish(ev)
}
