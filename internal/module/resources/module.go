// Package resourcesmod measures the host's load and each agent session's
// share of it, and serves the latest reading on GET /api/resources
// (host-resource-lease spec §3.1, §4 D-1; plan P0-2). It keeps the lease
// queue in resources.db and follows the host setting `resources` (plan P1).
//
// Sampling forks, so it runs on this module's own ticker in one goroutine
// and never on a request or hook path: the handler only reads the last
// snapshot (#1777, #1794).
package resourcesmod

import (
	"context"
	"errors"
	"log"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

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

// Module owns the sampler loop and the latest Snapshot.
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
	// fullLatch turns each reading into the published host.full with a
	// hysteresis; a failed tick leaves it where it was.
	fullLatch resources.FullLatch
	// minute is the host timeline's open minute (D-8.2).
	minute minuteAgg
	// mode is the setting's mode as of the last tick (the sampler goroutine's
	// own); empty before the first one.
	mode string

	// store is resources.db; nil when it could not be opened (or there is no
	// data dir), and the module then runs measure-only. settingsSrc is the
	// hostconfig module's reader; nil reads as mode measure.
	store           *leaseStore
	settingsSrc     resources.SettingsReader
	noteMu          sync.Mutex
	settingsFailing bool // a run of settings read failures is under way and has been logged
	listFailing     bool // the lease listing is failing and has been logged

	// stateMu is the one serialization boundary of the lease rows: every
	// read-decide-write on them (the sweeper's ends today; the admission pass,
	// create, delete and the per-tick use update later) holds it from the read
	// to the last write, so no transition lands between the rows a decision was
	// made on and the writes that follow (plan Task 1.5, review #4). The writes
	// under it are single statements, and a long poll never holds it while it
	// waits.
	stateMu sync.Mutex
	// gen is the generation channel: wake closes it and installs a fresh one on
	// every state transition, so a poller that took genChan before it read the
	// rows is woken by any change after that read. genMu guards the swap only.
	genMu sync.Mutex
	gen   chan struct{}

	// Sweeper state (sweeper.go), touched by the sweeper goroutine or a test
	// calling sweepOnce, under stateMu. sweepView replaces the process table
	// the holders are judged against (a seam: a populated ProcessSnapshot
	// cannot be built outside package agent; nil reads procSnapshot);
	// sweepHook runs before each end the sweeper attempts, for tests to race a
	// writer in.
	sweepEvery time.Duration
	sweepView  func(ctx context.Context) (procView, error)
	sweepHook  func(r leaseRow)
	// passHook runs inside the admission pass, after the rows were read and
	// before the first grant: a seam for tests to race a writer in.
	passHook func()
	// beforeLockHook runs between the unlocked baseline work and the locked
	// part of a round: a seam for a request that arrives in between.
	beforeLockHook func()
	// baselineFailing: the "no baseline" problem has been logged (the unlocked part
	// of the pass runs on the sampler and the sweeper goroutine).
	baselineFailing atomic.Bool
	// pollHook runs in a long poll after it read the row and released stateMu,
	// before it waits: a seam for tests to land a transition in that window.
	pollHook func()
	// lastSettings is the settings the last tick read: the snapshot route
	// uses them (for the leases' charge) instead of reading the settings store
	// per request.
	lastSettings atomic.Pointer[resources.Settings]
	// skipPass makes admissionPass a no-op (a test seam).
	skipPass    bool
	lastPrune   time.Time
	viewFailing bool

	// Per-lease use (leaseuse.go). leaseUse is the latest raw measured use of
	// each held lease in host percent, guarded by useMu; useAt, measureNote
	// and lastProcs' writer are the sampler goroutine's own. lastProcs is the
	// process list of the last good sample, shared read-only.
	useMu       sync.Mutex
	leaseUse    map[string]resources.LeaseUsage
	useAt       map[string]time.Time
	measureNote string
	lastProcs   atomic.Pointer[[]resources.Proc]

	// runCtx ends when Stop is called; it is made in New so that a Stop
	// before Start still keeps a later Start from running.
	runCtx    context.Context
	stopRun   context.CancelFunc
	wg        sync.WaitGroup
	startMu   sync.Mutex
	started   bool
	stopped   bool // Stop was called: the module cannot be started again
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
		runCtx:       runCtx,
		stopRun:      stopRun,
		gen:          make(chan struct{}),
		sweepEvery:   sweepInterval,
	}
}

// wake tells every poller holding the current generation channel that a lease
// row changed state: it closes the channel and installs a fresh one. A writer
// calls it after the write, normally still under stateMu.
func (m *Module) wake() {
	m.genMu.Lock()
	defer m.genMu.Unlock()
	close(m.gen)
	m.gen = make(chan struct{})
}

// genChan is the channel the next wake closes. A poller takes it before it
// reads the rows, so a change after that read cannot be missed.
func (m *Module) genChan() <-chan struct{} {
	m.genMu.Lock()
	defer m.genMu.Unlock()
	return m.gen
}

func (m *Module) Name() string           { return "resources" }
func (m *Module) Dependencies() []string { return []string{"peers", "hostconfig"} }

// Init installs the platform sampler (unless one was set), opens
// resources.db, looks up the hostconfig module as the settings reader and the
// peers module's origin resolver as the session root source. None of them is
// fatal: a missing root source leaves the per-session list empty, and a
// database that does not open (or a missing settings reader) leaves the module
// measuring only.
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
		m.logf("[resources] no data dir: resources.db is not opened, measuring only")
		return
	}
	st, err := openLeaseStore(filepath.Join(c.Cfg.DataDir, "resources.db"))
	if err != nil {
		m.logf("[resources] resources.db: %v: measuring only", err)
		return
	}
	m.store = st
}

// findSettings looks up the hostconfig module's SettingsReader.
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
	mux.HandleFunc("POST /api/resources/leases", m.handleLeaseCreate)
	mux.HandleFunc("DELETE /api/resources/leases", m.handleLeaseDeleteByClient)
	mux.HandleFunc("GET /api/resources/leases/{id}", m.handleLeaseGet)
	mux.HandleFunc("DELETE /api/resources/leases/{id}", m.handleLeaseDelete)
}

// Start runs the boot reconcile, then launches the sampler goroutine (one
// tick at once, then one per interval) and the lease sweeper, until Stop.
func (m *Module) Start(context.Context) error {
	m.startMu.Lock()
	defer m.startMu.Unlock()
	if m.stopped {
		return errStopped
	}
	if m.started {
		return nil
	}
	m.started = true
	m.boot() // before the sampler: its first tick sees the reconciled rows
	m.wg.Add(1)
	go m.run(m.runCtx)
	if m.store != nil {
		m.wg.Add(1)
		go m.runSweeper(m.runCtx) // after the boot reconcile and the sampler
	}
	return nil
}

// errStopped is what Start answers after Stop: a stopped module is finished,
// and a Start that returned nil would leave a daemon serving a frozen snapshot.
var errStopped = errors.New("resources module: Start after Stop")

// Stop cancels the loop and waits for the goroutine as far as ctx (the shared
// shutdown budget) allows. It leaves resources.db open: the daemon stops
// modules before the HTTP server drains (cmd/pdx/shutdown.go), so a request
// still in flight needs the database; Close, which runs after the server is
// down, closes it. Stop may be called again after a timeout and finishes the
// job; it is idempotent, and it also keeps a later Start from running.
func (m *Module) Stop(ctx context.Context) error {
	m.markStopped()
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// markStopped ends the module for good, atomically with Start: after it a
// Start answers errStopped, whichever of Stop and Close came first, and the
// sampler's context is cancelled.
func (m *Module) markStopped() {
	m.startMu.Lock()
	m.stopped = true
	m.startMu.Unlock()
	m.stopRun()
}

// Close closes resources.db once. The daemon calls it after the HTTP server
// has shut down (core.Closer); the loop was joined by Stop, and a Close that
// comes first stops it too, so it never closes the database under a sampler.
func (m *Module) Close() error {
	m.markStopped()
	m.wg.Wait()
	m.closeOnce.Do(func() {
		if m.store == nil {
			return
		}
		if err := m.store.Close(); err != nil {
			m.logf("[resources] close resources.db: %v", err)
		}
	})
	return nil
}
