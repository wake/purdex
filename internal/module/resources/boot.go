package resourcesmod

import "time"

// retention is how long host_minutes rows are kept (spec D-8.2).
const retention = 14 * 24 * time.Hour

// boot runs before the sampler starts: it prunes the host_minutes rows older than the retention. A failure is logged and the boot
// goes on. (It used to reconcile the lease and position rows too; those tables are no longer read or written, WWD-0a.)
func (m *Module) boot() {
	if m.store == nil {
		return
	}
	now := m.now()
	if _, err := m.store.PruneMinutes(now.Add(-retention).UnixMilli()); err != nil {
		m.logf("[resources] boot prune minutes: %v", err)
	}
}
