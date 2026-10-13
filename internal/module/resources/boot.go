package resourcesmod

import "time"

const (
	// bootGrace is how long a waiting row survives its poller being gone at
	// boot: the client needs the restart time to come back (spec D-4).
	bootGrace = 30 * time.Second
	// retention is how long ended rows and host_minutes rows are kept (spec
	// D-8.2: one period for both, so a report never mixes a short and a long one).
	retention = 14 * 24 * time.Hour
)

// boot is the reconcile that runs before the sampler starts. Rows live in
// resources.db, so there is nothing to rebuild in memory: held rows keep their
// state and their persisted measurements, waiting rows get the boot grace on
// their lease, and ended rows older than the retention go. A failure is logged
// and the boot goes on. Nothing else touches the rows yet; it still takes
// stateMu, so every read-decide-write on the rows is under the one lock.
func (m *Module) boot() {
	if m.store == nil {
		return
	}
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	now := m.now()
	// Parking is gone: a waiting row an older daemon left parked has nobody
	// polling it.
	if n, err := m.store.EndParkedAtBoot(now.UnixMilli()); err != nil {
		m.logf("[resources] boot: end parked rows: %v", err)
	} else if n > 0 {
		m.logf("[resources] boot: %d parked rows ended as abandoned", n)
	}
	if _, err := m.store.ExtendWaiting(now.Add(bootGrace).UnixMilli()); err != nil {
		m.logf("[resources] boot grace: %v", err)
	}
	if _, err := m.store.Prune(now.Add(-retention).UnixMilli()); err != nil {
		m.logf("[resources] boot prune: %v", err)
	}
	if _, err := m.store.PruneMinutes(now.Add(-retention).UnixMilli()); err != nil {
		m.logf("[resources] boot prune minutes: %v", err)
	}
	if last, err := m.store.MaxOfferedAt(); err != nil {
		m.logf("[resources] boot last offer: %v", err)
	} else {
		m.lastOfferMS = last
	}
	if last, err := m.store.MaxGrantedAt(); err != nil {
		m.logf("[resources] boot last grant: %v", err)
	} else {
		m.lastGrantMS = last
		m.lastGrantAtMS.Store(last)
	}
}
