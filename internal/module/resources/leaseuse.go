package resourcesmod

import (
	"context"
	"encoding/json"
	"maps"
	"math"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
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
		trees[i] = resources.LeaseTree{
			ID: r.ID, Scope: r.Scope, Root: r.HolderPID, RootStartMS: holderStartMS(r.HolderStart),
			Baseline: parseBaseline(r.Baseline),
		}
	}
	usage := resources.ComputeLeaseUse(procs, trees, viewStartMS(view), raw.NCPU, raw.MemBytes)

	halfLife := m.settings().HalfLife()
	now := m.now()
	prev := m.leaseUseSnapshot()
	latest := make(map[string]resources.LeaseUsage, len(held))
	lastAt := make(map[string]time.Time, len(held))
	for _, r := range held {
		u := usage[r.ID]
		if u.Unverified {
			// Its pid is another process now (the sweeper ends the lease as
			// holder_gone): nothing is measured, and the figures it had stay.
			if p, ok := prev[r.ID]; ok {
				latest[r.ID] = p
			}
			if at, ok := m.useAt[r.ID]; ok {
				lastAt[r.ID] = at
			}
			continue
		}
		latest[r.ID] = u

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
			// The clock stays where the last figure that reached the
			// database left it, so the next good write weighs all of it.
			m.logf("[resources] lease %s: %v", r.ID, err)
			if at, ok := m.useAt[r.ID]; ok {
				lastAt[r.ID] = at
			}
			continue
		}
		lastAt[r.ID] = now
	}
	m.setLeaseUse(latest)
	m.useAt = lastAt
}

// holderStartMS reads a row's holder_start (the registry's text, second
// precision) as unix milliseconds; 0 when it is empty or does not parse.
func holderStartMS(s string) int64 {
	t, err := ipeers.ParseProcStart(s)
	if err != nil || t.IsZero() {
		return 0
	}
	return t.UnixMilli()
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

func (m *Module) setLeaseUse(use map[string]resources.LeaseUsage) {
	m.useMu.Lock()
	m.leaseUse = use
	m.useMu.Unlock()
}

// leaseUseSnapshot is the latest raw measurement of each held lease (not the
// average): the LeaseUse input of the admission pass. Use is each lease's own
// figure; to take the leases off the host, add CPU and Mem up over the leases
// separately (Use is a maximum and does not add up). The caller gets its own
// copy.
func (m *Module) leaseUseSnapshot() map[string]resources.LeaseUsage {
	m.useMu.Lock()
	defer m.useMu.Unlock()
	out := make(map[string]resources.LeaseUsage, len(m.leaseUse))
	maps.Copy(out, m.leaseUse)
	return out
}
