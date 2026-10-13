package push

import (
	"sync"
	"time"

	"github.com/wake/purdex/internal/reads"
)

// Read sync on the push side (badge spec §7.2 "Pushes"). The reads module remembers the last read of each session and
// announces every accepted one; here, per push registration (DeviceID) of a phone that asked for it (prefs.read_sync), the
// reads since the last push to it are kept, in the order each code first became pending (a newer cursor for a pending code
// replaces the pending one in place). The sender attaches the oldest maxReads of them to every push it builds for that phone
// and, once APNs took the push, acknowledges exactly those versions. When nothing has been pushed to the phone for
// flushAfter, the sender is asked to send one push for the pending reads alone.
//
// Everything is in memory and lives and dies with the registration: it is dropped on every path that removes one.

// readsPerPush is how many pending reads one push takes (and how many the payload builder may carry).
const readsPerPush = 16

// readSync is the pending reads and the flush timers of every read_sync registration.
type readSync struct {
	now        func() time.Time
	after      func(d time.Duration, fn func()) (cancel func()) // time.AfterFunc; a test seam
	flushAfter time.Duration
	fire       func(deviceID string) // asks the sender for a flush push; must not block
	beforeAdd  func()                // a test seam: called at the start of Add, before the lock

	mu      sync.Mutex
	dev     map[string]*devReads
	stopped bool
}

type devReads struct {
	pending  map[string]reads.Cursor
	order    []string
	lastPush time.Time // the last push sent to the phone, of any kind; zero = not known
	cancel   func()
	armed    bool
	gen      uint64 // the arm a timer callback belongs to; Sent, Forget and every new arm invalidate the older ones
}

func newReadSync(now func() time.Time, flushAfter time.Duration, fire func(deviceID string)) *readSync {
	return &readSync{
		now: now, flushAfter: flushAfter, fire: fire, dev: map[string]*devReads{},
		after: func(d time.Duration, fn func()) func() {
			t := time.AfterFunc(d, fn)
			return func() { t.Stop() }
		},
	}
}

// Start opens a stopped readSync for a new run (the module's Start).
func (r *readSync) Start() {
	r.mu.Lock()
	r.stopped = false
	r.mu.Unlock()
}

// Stop cancels every flush timer and drops everything; until Start it takes no read.
func (r *readSync) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
	for _, d := range r.dev {
		if d.cancel != nil {
			d.cancel()
		}
	}
	r.dev = map[string]*devReads{}
}

// Add records a read for a registration. lastPush is when that phone was last pushed to, if the caller knows (a new entry
// takes it); the flush timer is armed if it is not already.
func (r *readSync) Add(deviceID string, rd reads.Read, lastPush time.Time) {
	if r.beforeAdd != nil {
		r.beforeAdd()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	d, ok := r.dev[deviceID]
	if !ok {
		d = &devReads{pending: map[string]reads.Cursor{}, lastPush: lastPush}
		r.dev[deviceID] = d
	}
	if _, pending := d.pending[rd.Code]; !pending {
		d.order = append(d.order, rd.Code)
	}
	d.pending[rd.Code] = rd.Through
	r.armLocked(deviceID, d)
}

// armLocked schedules the flush when none is: flushAfter after the last push (at once if that is already past), or after now
// when no push is known.
func (r *readSync) armLocked(deviceID string, d *devReads) {
	if d.armed || len(d.order) == 0 {
		return
	}
	delay := r.flushAfter
	if !d.lastPush.IsZero() {
		if delay = r.flushAfter - r.now().Sub(d.lastPush); delay < 0 {
			delay = 0
		}
	}
	d.armed = true
	d.gen++
	gen := d.gen
	d.cancel = r.after(delay, func() { r.flush(deviceID, d, gen) })
}

// flush is the timer's callback for the arm gen of d. A callback of a registration that has since been forgotten (or replaced
// under the same id), or of an arm that Sent or a newer arm has superseded (time.Timer.Stop cannot recall a callback that is
// already running), finds another entry or generation and does nothing.
func (r *readSync) flush(deviceID string, d *devReads, gen uint64) {
	r.mu.Lock()
	if r.stopped || r.dev[deviceID] != d || d.gen != gen {
		r.mu.Unlock()
		return
	}
	d.armed, d.cancel = false, nil
	if len(d.order) == 0 {
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	r.fire(deviceID) // outside the lock: it only enqueues
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dev[deviceID] != d || r.stopped {
		return
	}
	// A flush that did not clear the list (the send failed, or has not been made yet) is tried again one interval later; one
	// that worked has already called Sent, which cancelled this and left no timer for an empty list.
	if len(d.order) > 0 && !d.armed {
		d.lastPush = r.now()
		r.armLocked(deviceID, d)
	}
}

// Reserve is the oldest readsPerPush pending reads of a registration, in order. It removes nothing: they leave only through
// Sent, so a push that fails to build or to send carries them again next time.
func (r *readSync) Reserve(deviceID string) []reads.Read {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.dev[deviceID]
	if d == nil {
		return nil
	}
	n := len(d.order)
	if n > readsPerPush {
		n = readsPerPush
	}
	out := make([]reads.Read, 0, n)
	for _, code := range d.order[:n] {
		out = append(out, reads.Read{Code: code, Through: d.pending[code]})
	}
	return out
}

// Sent records that a push reached the phone at now, carrying sent: each (code, T) of it is removed only if the pending entry
// for that code still holds exactly that T (compare-and-delete), so a newer read that arrived while the push was in flight
// survives. The flush timer then counts from this push.
func (r *readSync) Sent(deviceID string, sent []reads.Read) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.dev[deviceID]
	if d == nil || r.stopped {
		return
	}
	d.lastPush = r.now()
	gone := map[string]bool{}
	for _, s := range sent {
		if cur, ok := d.pending[s.Code]; ok && cur == s.Through {
			delete(d.pending, s.Code)
			gone[s.Code] = true
		}
	}
	if len(gone) > 0 {
		kept := d.order[:0]
		for _, code := range d.order {
			if !gone[code] {
				kept = append(kept, code)
			}
		}
		d.order = kept
	}
	if d.cancel != nil {
		d.cancel()
		d.cancel, d.armed = nil, false
	}
	d.gen++ // a callback of the arm just cancelled may still be on its way
	r.armLocked(deviceID, d)
}

// Forget drops a registration's pending reads and its timer (the registration was removed or re-registered).
func (r *readSync) Forget(deviceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d := r.dev[deviceID]; d != nil {
		if d.cancel != nil {
			d.cancel()
		}
		delete(r.dev, deviceID)
	}
}

// Pending is how many reads a registration has waiting.
func (r *readSync) Pending(deviceID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d := r.dev[deviceID]; d != nil {
		return len(d.order)
	}
	return 0
}
