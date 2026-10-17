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
}

func NewBroker() *Broker { return &Broker{subs: map[*subscriber]struct{}{}} }

// Subscribe returns the event channel and its cancel. peer marks a TCP reader.
// The channel starts with the latest facts event (if any), taken under the same
// lock that registers the subscriber, so a record published meanwhile arrives
// once: either as that first event or after it, never both.
func (b *Broker) Subscribe(peer bool) (<-chan wire.Event, func()) {
	s := &subscriber{ch: make(chan wire.Event, subscriberBuffer), peer: peer}
	b.mu.Lock()
	if b.latest != nil {
		s.ch <- *b.latest
	}
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s.ch, func() { b.drop(s) }
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
		b.latest = &e
		b.fanOutLocked(e, false)
	}
}

// PublishJob announces a job state change. It never blocks and must not call
// back into the queue (the queue calls it while it holds its own locks).
func (b *Broker) PublishJob(ev wire.JobEvent) {
	if e, ok := envelope(wire.EventJob, ev); ok {
		b.publish(e, ev.Job.AdHoc)
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
		ch, cancel := d.broker.Subscribe(peer)
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
