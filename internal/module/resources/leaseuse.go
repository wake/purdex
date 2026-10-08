package resourcesmod

import (
	"context"
	"encoding/json"
	"maps"
	"math"
	"time"

	"github.com/wake/purdex/internal/resources"
)

// measureLeases is the per-lease use step of a good sampler tick (plan Task
// 1.5): it measures the tree of every held lease on the sampler's process
// list, folds the figure into the lease's running average, peak and mean,
// stores them, and keeps the latest raw figure for admission (leaseUseSnapshot).
//
// It runs on the sampler goroutine after the snapshot is published. It takes
// no stateMu: the only writes are UpdateUse, whose UPDATE is guarded by
// state = held, so a lease that ended in between is simply not written. With
// no lease held it reads nothing, not even the process table. When the table
// cannot be read this tick changes nothing.
func (m *Module) measureLeases(ctx context.Context, procs []resources.Proc, raw resources.HostRaw) {
	if m.store == nil {
		return
	}
	held, err := m.store.Active()
	if err != nil {
		m.noteMeasure("list held leases: " + err.Error())
		return
	}
	if len(held) == 0 {
		m.noteMeasure("")
		m.setLeaseUse(nil)
		m.useAt = nil
		return
	}
	view, err := m.readView(ctx)
	if err != nil {
		m.noteMeasure("process table: " + err.Error())
		return
	}
	m.noteMeasure("")

	trees := make([]resources.LeaseTree, len(held))
	for i, r := range held {
		trees[i] = resources.LeaseTree{ID: r.ID, Scope: r.Scope, Root: r.HolderPID, Baseline: parseBaseline(r.Baseline)}
	}
	usage := resources.ComputeLeaseUse(procs, trees, viewStartMS(view), raw.NCPU, raw.MemBytes)

	halfLife := m.settings().HalfLife()
	now := m.now()
	latest := make(map[string]float64, len(held))
	lastAt := make(map[string]time.Time, len(held))
	for _, r := range held {
		u := usage[r.ID]
		latest[r.ID] = u.Use
		lastAt[r.ID] = now

		first := r.Samples == 0
		dt := m.interval // a lease resumed after a restart: one interval since its last figure
		if at, ok := m.useAt[r.ID]; ok {
			dt = now.Sub(at)
		}
		ewma := resources.UpdateEWMA(r.EWMA, u.Use, dt, halfLife, first)
		peak := math.Max(r.PeakUse, u.Use)
		samples := r.Samples + 1
		mean := r.MeanUse + (u.Use-r.MeanUse)/float64(samples)
		empty := 0
		if u.Empty {
			empty = r.EmptySamples + 1
		}
		if err := m.store.UpdateUse(r.ID, ewma, peak, mean, samples, empty); err != nil {
			m.logf("[resources] lease %s: %v", r.ID, err)
		}
	}
	m.setLeaseUse(latest)
	m.useAt = lastAt
}

// viewStartMS gives a pid's start time in unix milliseconds from a process
// table; ok is false for a pid it cannot vouch for.
func viewStartMS(view procView) func(pid int) (int64, bool) {
	return func(pid int) (int64, bool) {
		t, err := view.Start(pid)
		if err != nil || t.IsZero() {
			return 0, false
		}
		return t.UnixMilli(), true
	}
}

// parseBaseline reads a row's baseline column. Anything that is not a JSON
// array of entries (NULL, empty, damaged) is an empty baseline: it only
// charges more.
func parseBaseline(s string) []resources.BaselineEntry {
	if s == "" {
		return nil
	}
	var out []resources.BaselineEntry
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

// noteMeasure logs a measuring problem when it changes, and the return to
// normal. Sampler goroutine only.
func (m *Module) noteMeasure(problem string) {
	if problem == m.measureNote {
		return
	}
	if problem == "" {
		m.logf("[resources] lease measuring recovered")
	} else {
		m.logf("[resources] lease use not updated: %s", problem)
	}
	m.measureNote = problem
}

func (m *Module) setLeaseUse(use map[string]float64) {
	m.useMu.Lock()
	m.leaseUse = use
	m.useMu.Unlock()
}

// leaseUseSnapshot is the latest raw measured use of each held lease, host
// percent (not the average): the LeaseUse input of the admission pass. The
// caller gets its own copy.
func (m *Module) leaseUseSnapshot() map[string]float64 {
	m.useMu.Lock()
	defer m.useMu.Unlock()
	out := make(map[string]float64, len(m.leaseUse))
	maps.Copy(out, m.leaseUse)
	return out
}
