// Package resourcesmod measures the host's load and each agent session's
// share of it, and serves the latest reading on GET /api/resources
// (host-resource-lease spec §3.1, §4 D-1; plan P0-2).
//
// Sampling forks, so it runs on this module's own ticker in one goroutine
// and never on a request or hook path: the handler only reads the last
// snapshot (#1777, #1794).
package resourcesmod

import (
	"context"
	"log"
	"net/http"
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
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	startMu   sync.Mutex
	started   bool
}

// New returns the module with production defaults; Init installs the platform
// sampler and finds the root source.
func New() *Module {
	return &Module{
		interval:     resources.SampleInterval,
		now:          time.Now,
		logf:         log.Printf,
		procSnapshot: iagent.SnapshotProcesses,
	}
}

func (m *Module) Name() string           { return "resources" }
func (m *Module) Dependencies() []string { return []string{"peers"} }

// Init installs the platform sampler (unless one was set) and looks up the
// peers module's origin resolver as the session root source. A missing or
// foreign service is not an error: the host figures still work, the
// per-session list is just empty.
func (m *Module) Init(c *core.Core) error {
	m.core = c
	if m.sampler == nil {
		m.sampler = resources.NewSampler()
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

func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	// The GET route arrives with the handler (Task 0.7).
	_ = mux
}

// Start launches the sampler goroutine: one tick at once, then one per
// interval, until Stop.
func (m *Module) Start(context.Context) error {
	m.startMu.Lock()
	defer m.startMu.Unlock()
	if m.started {
		return nil
	}
	m.started = true
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.wg.Add(1)
	go m.run(ctx)
	return nil
}

// Stop cancels the loop and waits for the goroutine, as far as ctx (the
// shared shutdown budget) allows.
func (m *Module) Stop(ctx context.Context) error {
	m.startMu.Lock()
	cancel := m.cancel
	m.startMu.Unlock()
	if cancel != nil {
		cancel()
	}
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
