// Package resourcesmod measures the host's load and each agent session's
// share of it, and serves the latest reading on GET /api/resources
// (host-resource-lease spec §3.1, §4 D-1; plan P0-2). It keeps the host_minutes timeline in resources.db and follows the host
// setting `resources`. (The lease queue it once kept is gone: the gate replaced it, WWD-0a.)
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
	"github.com/wake/purdex/internal/fleet"
	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/hostmem"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/watchsettings"
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

	// watch is the load watcher's state (watch.go).
	watch watchState

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
	// ringMu guards ring: the sampler goroutine adds to it, and a request
	// (GET ?samples=1) reads a copy. zoneLatch, lastZone and zoneSince are the
	// sampler goroutine's own.
	ringMu sync.Mutex
	ring   resources.Ring
	// zoneLatch is the daemon's zone: the memory pressure level and the page rate, the gate's own signal. pageRing holds the
	// recent paging counters the rate is worked out from (hostmem.RateOver, the gate's arithmetic), and rateLimit the rate at
	// which the gate holds new work (its config, read every tick).
	zoneLatch resources.PressureZoneLatch
	pageRing  []hostmem.PageSample
	rateLimit float64
	haveRate  bool // the last reading carried the paging counters
	lastZone  resources.Zone
	zoneSince time.Time
	// gateConfig reads the gate's config; gateCounts the slot records (see gate_input.go). nil: a module that is not wired to the
	// host (Init wires both).
	// pageFrac is the part of a page the facts' rate added since the last whole one (sampler goroutine only; see cachePressureFromFacts).
	pageFrac float64
	// procsFailing says the process table read failed on the last pool tick (logged once per run).
	procsFailing bool
	// fromPool is whether the last tick's host figures were wwd's (sampler goroutine only).
	fromPool bool

	gateConfig func() gate.Config
	gateCounts func() (heavy, light, waiting int, ok bool)
	// minute is the host timeline's open minute (D-8.2).
	minute minuteAgg
	// mode is the setting's mode as of the last tick (the sampler goroutine's
	// own); empty before the first one.
	mode string

	// store is resources.db; nil when it could not be opened (or there is no
	// data dir), and the module then runs measure-only. settingsSrc is the
	// hostconfig module's reader; nil reads as mode measure.
	store           *resourcesStore
	settingsSrc     resources.SettingsReader
	noteMu          sync.Mutex
	settingsFailing bool // a run of settings read failures is under way and has been logged

	// pool and fleetSrc: while the fleet row says source = "pool", LatestHost / LatestHostDetail answer from wwd's facts of this
	// host (poolhost.go) and fall back to the sample below when wwd gives none. Both nil (a test module) keeps the own sample.
	pool     *poolHost
	fleetSrc *fleetSource

	// lastSettings is the settings the last tick read: the snapshot route
	// uses them instead of reading the settings store per request.
	lastSettings atomic.Pointer[resources.Settings]
	// lastProcs is the process list of the last good sample, shared read-only (the fleet's local source reads it).
	lastProcs atomic.Pointer[[]resources.Proc]
	lastPrune time.Time
	// newGate makes the gate the fleet summary reads (a test seam: nil is gate.New).
	newGate func(gate.Config) *gate.Gate

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
		watch:        watchState{env: defaultWatchEnv()},
	}
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
	c.Registry.Register(resources.HostReaderKey, resources.HostReader(m))
	c.Registry.Register(fleet.LocalSourceKey, fleet.LocalSource(newLocalSource(m)))
	m.findPoolHost(c)
	c.Registry.Register(resources.LocalFactsKey, resources.LocalFactsReader(m))
	if m.sampler == nil {
		m.sampler = resources.NewSampler()
		if m.gateConfig == nil {
			m.gateConfig = hostGateConfig() // production wiring (a module with an injected sampler is a test's): the reading of the gate's config and slot records
		}
	}
	m.openStore(c)
	m.findSettings(c)
	m.findWatchSettings(c)
	if c.Events != nil {
		c.Events.OnSubscribe(m.sendEventSnapshot)
	}
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

// LatestHost is the host part of the last published snapshot (resources.HostReader); ok is false before a usable one.
func (m *Module) LatestHost() (resources.HostUse, bool) {
	if hd, ok := m.poolDetail(); ok {
		return hd.Host, true
	}
	snap := m.latest.Load()
	if snap == nil || !snap.Available {
		return resources.HostUse{}, false
	}
	return snap.Host, true
}

// LatestHostDetail is LatestHost with the sample time (resources.HostDetailReader). ok is false before a usable sample, as
// LatestHost.
func (m *Module) LatestHostDetail() (resources.HostDetail, bool) {
	if hd, ok := m.poolDetail(); ok {
		return hd, true
	}
	snap := m.latest.Load()
	if snap == nil || !snap.Available {
		return resources.HostDetail{}, false
	}
	return resources.HostDetail{Host: snap.Host, SampledAt: snap.SampledAt.UnixMilli()}, true
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
	st, err := openResourcesStore(filepath.Join(c.Cfg.DataDir, "resources.db"))
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
	mux.HandleFunc("GET /api/resources/report", m.handleReport)
}

// Start runs the boot reconcile, then launches the sampler goroutine (one
// tick at once, then one per interval) until Stop.
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
	m.boot() // before the sampler
	m.wg.Add(1)
	go m.run(m.runCtx)
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

// findWatchSettings looks up the hostconfig module's watch setting reader; without one the watcher stays off.
func (m *Module) findWatchSettings(c *core.Core) {
	if m.watch.src != nil {
		return
	}
	svc, ok := c.Registry.Get(watchsettings.Key)
	if !ok {
		m.logf("[watch] service %q not registered: the watcher stays off", watchsettings.Key)
		return
	}
	rd, ok := svc.(watchsettings.Reader)
	if !ok {
		m.logf("[watch] service %q is %T, not a Reader: the watcher stays off", watchsettings.Key, svc)
		return
	}
	m.watch.src = rd
}
