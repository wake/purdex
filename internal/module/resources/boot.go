package resourcesmod

import "time"

const (
	// bootGrace is how long a waiting row survives its poller being gone at
	// boot: the client needs the restart time to come back (spec D-4).
	bootGrace = 30 * time.Second
	// retention is how long ended rows are kept.
	retention = 7 * 24 * time.Hour
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
	if _, err := m.store.ExtendWaiting(now.Add(bootGrace).UnixMilli()); err != nil {
		m.logf("[resources] boot grace: %v", err)
	}
	if _, err := m.store.Prune(now.Add(-retention).UnixMilli()); err != nil {
		m.logf("[resources] boot prune: %v", err)
	}
}
