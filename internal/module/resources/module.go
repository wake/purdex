// Package resourcesmod measures the host's load and each agent session's
// share of it, serves the latest reading on GET /api/resources
// (host-resource-lease spec §3.1, §4 D-1; plan P0-2) and keeps the lease
// queue (spec D-2 to D-5; plan P1).
//
// Sampling forks, so it runs on this module's own ticker in one goroutine
// and never on a request or hook path: the handlers only read the last
// snapshot (#1777, #1794).
package resourcesmod

import (
	"context"
	"log"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	iagent "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/core"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/resources"
)

// failuresBeforeUnavailable is how many ticks in a row must fail before the
// snapshot says sample_failed: one flaky fork does not blank the App.
const failuresBeforeUnavailable = 3

// sampleBudget bounds one tick's reads (the sampler's forks and the process
// table), well inside the 5 s interval.
const sampleBudget = 3 * time.Second

// Module owns the sampler loop, the latest Snapshot and the lease queue.
type Module struct {
	core *core.Core

	sampler  resources.Sampler
	roots    resources.RootSource // nil: no sessions are attributed
	interval time.Duration
	now      func() time.Time
	logf     func(format string, args ...any)
	// procSnapshot reads the process table once per tick, for the roots'
	// liveness; a seam so tests fork nothing.
	procSnapshot func(ctx context.Context) (*iagent.ProcessSnapshot, error)

	// latest is the last published snapshot; nil until the first tick ends.
	// A published snapshot is never modified.
	latest atomic.Pointer[resources.Snapshot]

	// Loop state, touched only by the sampler goroutine (or a test calling
	// tick directly).
	fails     int    // consecutive failed ticks
	degraded  bool   // the failing log line has been written for this run of failures
	rootsNote string // the last roots problem logged, so a standing one logs once
	// latch turns the stateless full reading into the hysteresis the
	// snapshot publishes; mode is the setting's mode as of the last tick.
	latch resources.FullLatch
	mode  string

	// Lease state. store is nil when resources.db could not be opened (or
	// there is no data dir): the module then runs measure-only. stateMu is
	// the one serialization boundary of every read-decide-write on lease
	// rows (plan Task 1.5); gen is the wake-up channel of the long-polls,
	// closed and replaced under it on every state transition.
	store       *leaseStore
	settingsSrc resources.SettingsReader // nil: settings read as measure
	admit       Admitter
	stateMu     sync.Mutex
	gen         chan struct{}
	newID       func() string
	// onGranted is called under stateMu for each row a pass grants; P1-2b
	// hangs the baseline capture on it.
	onGranted func(row leaseRow)
	// afterRead is a test seam: it runs in a long-poll after the row was
	// read and stateMu released, before the wait begins. nil in production.
	afterRead func(id string)

	noteMu       sync.Mutex
	settingsNote string // the settings problem last logged, so a standing one logs once

	// runCtx ends when Stop is called: it stops the loops and wakes the
	// long-polls.
	runCtx    context.Context
	stopRun   context.CancelFunc
	wg        sync.WaitGroup
	startMu   sync.Mutex
	started   bool
	closeOnce sync.Once
}

// New returns the module with production defaults; Init installs the platform
// sampler and finds the root source.
func New() *Module {
	runCtx, stopRun := context.WithCancel(context.Background())
	return &Module{
		interval:     resources.SampleInterval,
		now:          time.Now,
		logf:         log.Printf,
		procSnapshot: iagent.SnapshotProcesses,
		admit:        additiveAdmitter{},
		gen:          make(chan struct{}),
		newID:        uuid.NewString,
		onGranted:    func(leaseRow) {},
		runCtx:       runCtx,
		stopRun:      stopRun,
	}
}

func (m *Module) Name() string           { return "resources" }
func (m *Module) Dependencies() []string { return []string{"peers", "hostconfig"} }

// Init installs the platform sampler (unless one was set), opens resources.db,
// looks up the peers module's origin resolver as the session root source and
// the hostconfig module as the settings reader. None of them is fatal: a
// missing root source leaves the per-session list empty, and a database that
// does not open (or a missing settings reader) leaves the module measuring
// only, with every lease request granted at once.
func (m *Module) Init(c *core.Core) error {
	m.core = c
	if m.sampler == nil {
		m.sampler = resources.NewSampler()
	}
	m.openStore(c)
	m.findSettings(c)
	if m.roots != nil {
		return nil
	}
	svc, ok := c.Registry.Get(peersmod.OriginResolverKey)
	if !ok {
		m.logf("[resources] service %q not registered: no sessions will be attributed", peersmod.OriginResolverKey)
		return nil
	}
	src, ok := svc.(resources.RootSource)
	if !ok {
		m.logf("[resources] service %q is %T, not a RootSource: no sessions will be attributed", peersmod.OriginResolverKey, svc)
		return nil
	}
	m.roots = src
	return nil
}

// openStore opens resources.db in the data dir. A failure is logged and
// leaves m.store nil (measure-only); the daemon never fails for it.
func (m *Module) openStore(c *core.Core) {
	if m.store != nil {
		return
	}
	if c.Cfg == nil || c.Cfg.DataDir == "" {
		m.logf("[resources] no data dir: lease requests are granted without being recorded")
		return
	}
	path := filepath.Join(c.Cfg.DataDir, "resources.db")
	st, err := openLeaseStore(path)
	if err != nil {
		m.logf("[resources] %v: lease requests are granted without being recorded", err)
		return
	}
	m.store = st
}

func (m *Module) findSettings(c *core.Core) {
	if m.settingsSrc != nil {
		return
	}
	svc, ok := c.Registry.Get(resources.SettingsKey)
	if !ok {
		m.logf("[resources] service %q not registered: settings read as mode measure", resources.SettingsKey)
		return
	}
	rd, ok := svc.(resources.SettingsReader)
	if !ok {
		m.logf("[resources] service %q is %T, not a SettingsReader: settings read as mode measure", resources.SettingsKey, svc)
		return
	}
	m.settingsSrc = rd
}

func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/resources", m.handleGet)
	mux.HandleFunc("POST /api/resources/leases", m.handleLeasePost)
	mux.HandleFunc("GET /api/resources/leases/{id}", m.handleLeaseGet)
	mux.HandleFunc("DELETE /api/resources/leases/{id}", m.handleLeaseDelete)
	mux.HandleFunc("DELETE /api/resources/leases", m.handleLeaseDeleteByClient)
}

// Start runs the boot reconcile, then launches the sampler goroutine: one
// tick at once, then one per interval, until Stop.
func (m *Module) Start(context.Context) error {
	m.startMu.Lock()
	defer m.startMu.Unlock()
	if m.started {
		return nil
	}
	m.started = true
	m.boot()
	m.wg.Add(1)
	go m.run(m.runCtx)
	return nil
}

// Stop cancels the loops and the long-polls, waits for the goroutines as far
// as ctx (the shared shutdown budget) allows, then closes the database. It
// may be called again after a timeout and finishes the job; it is idempotent.
func (m *Module) Stop(ctx context.Context) error {
	m.stopRun()
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		m.closeStore()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// closeStore closes resources.db once, after the loops have been joined.
func (m *Module) closeStore() {
	m.closeOnce.Do(func() {
		if m.store != nil {
			if err := m.store.Close(); err != nil {
				m.logf("[resources] close resources.db: %v", err)
			}
		}
	})
}
