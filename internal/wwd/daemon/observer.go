package daemon

import (
	"errors"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

// maxUnsavedObservations bounds the finished observations kept for a store that cannot be written (the oldest go first).
const maxUnsavedObservations = 256

// ObservationStore keeps the finished observations (store.Store).
type ObservationStore interface {
	SaveObservation(wire.Observation) error
	LoadObservations(since time.Time) ([]wire.Observation, error)
}

// observeTracker is the part of facts.Tracker the Observer uses.
type observeTracker interface {
	Update(now time.Time, snap facts.ProcSnapshot, c facts.ProcContext) []wire.Observation
	Live() []wire.Observation
}

// Observer is the observation records of spec section 8.4 on this host: the sampler's process table goes in through Observe, the
// trees that ended over a threshold are stored, the ones still running are kept in memory, and the routes read both. Nothing here
// blocks or kills anything.
type Observer struct {
	tracker observeTracker
	store   ObservationStore

	mu        sync.RWMutex
	live      []wire.Observation // the running trees over a threshold, as of the last sample
	unsaved   []wire.Observation // ended and not yet in the store, oldest first
	lastStore string             // the last store error logged, to log a change only
}

// NewObserver watches this host's trees with cfg (the host is set from host). st may be nil: nothing is then kept past the memory
// bound.
func NewObserver(host string, cfg facts.ObserveConfig, st ObservationStore) *Observer {
	cfg.Host = host
	return newObserver(facts.NewTracker(cfg), st)
}

func newObserver(t observeTracker, st ObservationStore) *Observer {
	return &Observer{tracker: t, store: st}
}

// Observe is facts.Sources.Observe: it runs on the sampler's goroutine with every sample's process table.
func (o *Observer) Observe(now time.Time, snap facts.ProcSnapshot, c facts.ProcContext) {
	done := o.tracker.Update(now, snap, c)
	live := o.tracker.Live()

	o.mu.Lock()
	o.live = live
	o.unsaved = append(o.unsaved, done...)
	if over := len(o.unsaved) - maxUnsavedObservations; over > 0 {
		o.unsaved = o.unsaved[over:]
	}
	todo := append([]wire.Observation(nil), o.unsaved...)
	o.mu.Unlock()

	if o.store == nil {
		return
	}
	// outside the lock, so a slow disk does not hold up the routes; in order, and a record that failed is tried again on the next
	// sample before the ones after it
	saved := 0
	for _, r := range todo {
		if err := o.store.SaveObservation(r); err != nil {
			o.logStore("saving an observation", err)
			break
		}
		saved++
	}
	if saved > 0 {
		o.mu.Lock()
		o.unsaved = o.unsaved[min(saved, len(o.unsaved)):]
		o.mu.Unlock()
	}
}

func (o *Observer) logStore(what string, err error) {
	msg := what + ": " + err.Error()
	o.mu.Lock()
	changed := msg != o.lastStore
	o.lastStore = msg
	o.mu.Unlock()
	if changed {
		log.Printf("wwd: observations: %s", msg)
	}
}

// Observations are the stored, the waiting and the running observations that began at or after since, oldest first, each once. A
// store that cannot be read leaves the ones in memory.
func (o *Observer) Observations(since time.Time) ([]wire.Observation, error) {
	var stored []wire.Observation
	if o.store != nil {
		var err error
		stored, err = o.store.LoadObservations(since)
		if err != nil {
			o.logStore("reading the observations", err)
			var skipped *store.SkippedRowsError
			if !errors.As(err, &skipped) {
				stored = nil
			}
		}
	}
	o.mu.RLock()
	waiting := append([]wire.Observation(nil), o.unsaved...)
	running := append([]wire.Observation(nil), o.live...)
	o.mu.RUnlock()

	have := map[string]bool{}
	out := []wire.Observation{}
	for _, src := range [][]wire.Observation{stored, waiting, running} { // an ended record wins over its running form
		for _, r := range src {
			if r.StartedAt.Before(since) || have[r.ID] {
				continue
			}
			have[r.ID] = true
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].StartedAt.Before(out[j].StartedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Undeclared groups Observations(since) by command signature (wire.GroupUndeclared).
func (o *Observer) Undeclared(since time.Time) ([]wire.UndeclaredGroup, error) {
	obs, err := o.Observations(since)
	if err != nil {
		return nil, err
	}
	return wire.GroupUndeclared(obs), nil
}
