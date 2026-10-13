package resourcesmod

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/wake/purdex/internal/resources"
)

// waitReasons is the per-waiting-row account of why it is not granted (lease
// v2 spec D-7 "How wait_reasons is counted"). Each pass gives every waiter a
// reason; the time between two passes belongs to the reason that was in force
// during it. When the row is granted or ends, the totals go into the same
// UPDATE as the transition.
//
// Locking: the pass, the routes and the sweeper call it with stateMu held, so
// the lock order is stateMu -> mu, never the reverse. The GET route and the
// event goroutine call snapshot (a copy) holding mu only. It is lost on
// restart.
type waitReasons struct {
	mu sync.Mutex
	m  map[string]*reasonState
}

type reasonState struct {
	reason resources.V2Reason
	since  time.Time
	acc    map[resources.V2Reason]int64 // ms
}

// reasonView is a copy of one row's current reason, for status and for rule A5.
type reasonView struct {
	Reason resources.V2Reason
	Since  time.Time
}

// observe records that, at now, the row is held back for reason. The time
// since the previous observation is added to the previous reason first.
func (w *waitReasons) observe(id string, reason resources.V2Reason, now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.m == nil {
		w.m = map[string]*reasonState{}
	}
	st := w.m[id]
	if st == nil {
		st = &reasonState{acc: map[resources.V2Reason]int64{}}
		w.m[id] = st
	} else {
		st.acc[st.reason] += max(0, now.Sub(st.since).Milliseconds())
	}
	st.reason, st.since = reason, now
}

// peek is the JSON map of milliseconds by reason, with the open interval
// charged to the current reason up to now. It changes nothing: the caller
// writes it in the transition's UPDATE and calls drop when that UPDATE won.
// "" for a row that was never observed.
func (w *waitReasons) peek(id string, now time.Time) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	st := w.m[id]
	if st == nil {
		return ""
	}
	out := make(map[resources.V2Reason]int64, len(st.acc)+1)
	for r, ms := range st.acc {
		out[r] = ms
	}
	if st.reason != "" {
		out[st.reason] += max(0, now.Sub(st.since).Milliseconds())
	}
	for r, ms := range out {
		if ms == 0 {
			delete(out, r) // a reason that held the row for no time is not a reason
		}
	}
	b, _ := json.Marshal(out) // a map of strings to ints
	return string(b)
}

// current is the reason the last pass gave the row and when.
func (w *waitReasons) current(id string) (resources.V2Reason, time.Time, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	st := w.m[id]
	if st == nil {
		return "", time.Time{}, false
	}
	return st.reason, st.since, true
}

// drop forgets a row that was granted or ended.
func (w *waitReasons) drop(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.m, id)
}

// retain forgets every row not in live: a waiting row that ended some way no
// transition path reported must not stay forever.
func (w *waitReasons) retain(live map[string]bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id := range w.m {
		if !live[id] {
			delete(w.m, id)
		}
	}
}

// snapshot is a copy of every row's current reason.
func (w *waitReasons) snapshot() map[string]reasonView {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[string]reasonView, len(w.m))
	for id, st := range w.m {
		out[id] = reasonView{Reason: st.reason, Since: st.since}
	}
	return out
}
