package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// subscriberBuffer is how many events a subscriber may fall behind before it is
// dropped; Publish never waits for anyone.
const subscriberBuffer = 64

// maxSubscribers bounds the open event streams (local readers and peers
// together); one more is refused with 503. A host has a handful of readers: the
// Purdex daemon, the CLI, the other wwd's.
const maxSubscribers = 64

// keepAliveEvery is the SSE comment interval that keeps idle connections open.
const keepAliveEvery = 15 * time.Second

type subscriber struct {
	ch   chan wire.Event
	peer bool // a TCP reader: never sees ad-hoc job events
}

// Broker fans events out to the SSE streams. Publishing never blocks: a
// subscriber that has fallen subscriberBuffer events behind is closed (it
// reconnects and starts from the current facts).
type Broker struct {
	mu     sync.Mutex
	subs   map[*subscriber]struct{}
	latest *wire.Event // the last facts event: a new subscriber starts with it
	// latestFacts is the record behind latest, and shape (set by the daemon from the sampler) the gate's view of it: a stream that
	// opens after the proto 2 gate shut must not start with the v 2 record cached before.
	latestFacts wire.Facts
	shape       func(wire.Facts) wire.Facts
	// latestLabels is the last labels event, sent right after the facts: a stream
	// that opens or reopens later still learns the current labels, which would
	// otherwise reach it only with the next change.
	latestLabels *wire.Event
}

func NewBroker() *Broker { return &Broker{subs: map[*subscriber]struct{}{}} }

// SetShape sets the function a stream's first facts event goes through (Sampler.Shaped); nil: the cached record as published.
func (b *Broker) SetShape(fn func(wire.Facts) wire.Facts) {
	b.mu.Lock()
	b.shape = fn
	b.mu.Unlock()
}

// Subscribe returns the event channel and its cancel. peer marks a TCP reader.
// The channel starts with the latest facts event (if any), taken under the same
// lock that registers the subscriber, so a record published meanwhile arrives
// once: either as that first event or after it, never both.
func (b *Broker) Subscribe(peer bool) (<-chan wire.Event, func()) {
	ch, cancel, _ := b.subscribe(peer, false)
	return ch, cancel
}

// TrySubscribe is Subscribe with the cap of maxSubscribers streams: ok is false
// (and nothing is registered) when it is reached.
func (b *Broker) TrySubscribe(peer bool) (<-chan wire.Event, func(), bool) {
	return b.subscribe(peer, true)
}

func (b *Broker) subscribe(peer, capped bool) (<-chan wire.Event, func(), bool) {
	s := &subscriber{ch: make(chan wire.Event, subscriberBuffer), peer: peer}
	b.mu.Lock()
	if capped && len(b.subs) >= maxSubscribers {
		b.mu.Unlock()
		return nil, nil, false
	}
	if b.latest != nil {
		first := *b.latest
		if b.shape != nil {
			if e, ok := envelope(wire.EventFacts, b.shape(b.latestFacts)); ok {
				first = e
			}
		}
		s.ch <- first
	}
	if b.latestLabels != nil {
		s.ch <- *b.latestLabels
	}
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s.ch, func() { b.drop(s) }, true
}

func (b *Broker) drop(s *subscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subs[s]; ok {
		delete(b.subs, s)
		close(s.ch)
	}
}

func (b *Broker) publish(e wire.Event, adHoc bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fanOutLocked(e, adHoc)
}

func (b *Broker) fanOutLocked(e wire.Event, adHoc bool) {
	for s := range b.subs {
		if adHoc && s.peer {
			continue // spec §4.4, §8.3, §11: an ad-hoc job is local only, its event too
		}
		select {
		case s.ch <- e:
		default:
			delete(b.subs, s)
			close(s.ch)
		}
	}
}

func envelope(t wire.EventType, v any) (wire.Event, bool) {
	b, err := json.Marshal(v)
	if err != nil {
		return wire.Event{}, false
	}
	return wire.Event{Type: t, Data: b}, true
}

// PublishFacts announces a new facts record.
func (b *Broker) PublishFacts(f wire.Facts) {
	if e, ok := envelope(wire.EventFacts, f); ok {
		b.mu.Lock() // remembering it and fanning it out are one step, or a subscriber could get it twice
		defer b.mu.Unlock()
		b.latest, b.latestFacts = &e, f
		b.fanOutLocked(e, false)
	}
}

// PublishJob announces a job state change. It never blocks and must not call
// back into the queue (the queue calls it while it holds its own locks).
func (b *Broker) PublishJob(ev wire.JobEvent) {
	if e, ok := envelope(wire.EventJob, ev); ok {
		// an ad-hoc job and a fast-lane (local) job never leave this host: their events go to local subscribers only
		b.publish(e, ev.Job.AdHoc || ev.Job.Local)
	}
}

// PublishLabels announces that this host's labels changed.
func (b *Broker) PublishLabels(ev wire.LabelsEvent) {
	if e, ok := envelope(wire.EventLabels, ev); ok {
		b.mu.Lock() // remembering it and fanning it out are one step, as for facts
		defer b.mu.Unlock()
		b.latestLabels = &e
		b.fanOutLocked(e, false)
	}
}

// PublishException and PublishWorker are for WWD-1f.
func (b *Broker) PublishException(ev wire.ExceptionEvent) {
	if e, ok := envelope(wire.EventException, ev); ok {
		b.publish(e, false)
	}
}

func (b *Broker) PublishWorker(ev wire.WorkerEvent) {
	if e, ok := envelope(wire.EventWorker, ev); ok {
		b.publish(e, false)
	}
}

// writeFrame writes one SSE frame: `event:` the type, `data:` the Event JSON.
func writeFrame(w http.ResponseWriter, e wire.Event) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, b)
	return err
}

// handleEvents serves GET /v1/events. The first frame is the latest facts (the
// broker puts it in the new subscription) so a reader does not wait for the next
// sample.
func (d *Daemon) handleEvents(peer bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		ch, cancel, ok := d.broker.TrySubscribe(peer)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "too many event streams"})
			return
		}
		defer cancel()
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		fl.Flush()
		tick := time.NewTicker(keepAliveEvery)
		defer tick.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-tick.C:
				if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
					return
				}
				fl.Flush()
			case e, open := <-ch:
				if !open {
					return // dropped for falling behind: the client reconnects
				}
				if err := writeFrame(w, e); err != nil {
					return
				}
				fl.Flush()
			}
		}
	}
}
