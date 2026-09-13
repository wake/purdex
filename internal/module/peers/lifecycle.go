package peers

// Module lifecycle: Start (the startup sweep and the idle-reap ticker),
// Stop (the fixed teardown order) and the reap loop between them. The
// reply workers Stop joins live in reply.go; the helper manager they and
// the reap loop drive lives in helpers.go.

import (
	"context"
	"time"
)

// helperReapInterval is how often idle helpers are reaped (HelperIdleReap
// is the idle threshold itself).
const helperReapInterval = time.Minute

// replyWorkerCap bounds the reply workers (Task 9) in flight at once.
const replyWorkerCap = 8

// Start sweeps proxies.json from a previous run (spec §4.5) — a sweep
// that cannot rewrite the ownership file is fatal, since the daemon must
// never run without one — and then starts the idle-reap ticker under
// stopCtx.
func (m *Module) Start(context.Context) error {
	if err := m.helpers.Sweep(); err != nil {
		return err
	}
	m.reapWG.Add(1)
	go m.reapLoop()
	return nil
}

// reapLoop releases idle helpers every helperReapInterval until Stop.
// ReapIdle runs synchronously on this goroutine, so once Stop has joined
// reapWG no Release from here can still begin.
func (m *Module) reapLoop() {
	defer m.reapWG.Done()
	ticker := time.NewTicker(helperReapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCtx.Done():
			return
		case <-ticker.C:
			m.helpers.ReapIdle()
		}
	}
}

// Stop tears down in a fixed order (R2-B1, R3-M2): cancel stopCtx (new
// deliveries answer 503 not_ready, in-flight socket writes abort, the
// reply semaphore wait gives up) → join the reap ticker (no further
// Release can start from it) → helpers.Stop (closes admission, joins
// every startup, pump and in-flight Release: after it no onFrame runs, so
// no reply worker can be added) → join the reply workers that were
// already running. Idempotent.
func (m *Module) Stop(context.Context) error {
	m.stopCancel()
	m.reapWG.Wait()
	if m.helpers != nil {
		m.helpers.Stop()
	}
	m.workers.Wait()
	return nil
}
