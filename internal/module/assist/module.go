// Package assistmod is the daemon's side of the assist hosts (assist-dispatch plan X1a): the switches, the allowed list and the
// readiness of each host, kept in the gate directory's assist.json and assist-ready.json. It is the only writer of both files'
// shared parts (plan contract 8); the CLI and the App go through its admin-only routes.
package assistmod

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/gate"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
	"github.com/wake/purdex/internal/watch"
)

// origins is the part of the peers module's resolver this module reads.
type origins interface {
	ResolveOriginByRef(ref string) (team.Origin, bool, error)
	ResolveOriginBySession(sessionID string) (team.Origin, bool, error)
	ListLiveOrigins() ([]team.Origin, error)
}

// The peers module's resolver is what Init looks up.
var _ origins = (*peersmod.OriginResolver)(nil)

var (
	errBusy   = errors.New("busy")
	errNoHost = errors.New("no_such_host")
)

// timeouts are how long each script may run (plan contract 4); at the timeout its process group is ended.
type timeouts struct{ on, off, check time.Duration }

// Module is the assist module.
type Module struct {
	dir      string
	logf     func(format string, args ...any)
	now      func() time.Time
	origins  origins
	teams    team.AssistTeams
	seats    team.SeatReader
	timeouts timeouts
	// pollEvery is the period of the reconciler and the checks.
	pollEvery time.Duration

	// runners holds one mutex per host: held from claiming an Op to clearing it, so two switch scripts of one host never run at once.
	runMu   sync.Mutex
	runners map[string]*sync.Mutex

	// ready is the poller's copy of assist-ready.json (it is the file's only writer).
	readyMu sync.Mutex
	ready   map[string]gate.AssistReady
	// passMu serialises the poller's passes.
	passMu sync.Mutex
	kick   chan struct{}

	// notify sends a notice to a session on this host (nil: no notices); waker wakes the load watcher (nil: no wakes). nkick asks the
	// notifier goroutine for a pass; notifyMu serialises passes; wake folds the wakes; wakeWG joins them at Stop.
	notify   func(sessionID, text string) error
	waker    func(ctx context.Context, reason, detail string) error
	nkick    chan struct{}
	notifyMu sync.Mutex
	wake     wakeState
	wakeWG   sync.WaitGroup

	// ctxMu guards ctx, the parent of every script: Stop cancels it, so a script in flight does not hold the shutdown.
	ctxMu   sync.Mutex
	ctx     context.Context
	stop    context.CancelFunc
	stopped bool // Stop has begun: no new wake starts
	wg      sync.WaitGroup
}

// New returns the module with production defaults.
func New() *Module {
	return &Module{logf: log.Printf, now: time.Now, dir: gate.DefaultDir(), pollEvery: 60 * time.Second,
		timeouts: timeouts{on: 120 * time.Second, off: 90 * time.Second, check: 30 * time.Second},
		runners:  map[string]*sync.Mutex{}, ready: map[string]gate.AssistReady{}, kick: make(chan struct{}, 1), nkick: make(chan struct{}, 1)}
}

func (m *Module) Name() string           { return "assist" }
func (m *Module) Dependencies() []string { return []string{"peers", "team"} }

// Init looks up the services it reads; a missing one only makes the routes that need it answer not_ready.
func (m *Module) Init(c *core.Core) error {
	if svc, ok := c.Registry.Get(peersmod.OriginResolverKey); ok {
		if o, ok := svc.(origins); ok {
			m.origins = o
		}
	}
	if svc, ok := c.Registry.Get(team.AssistTeamsKey); ok {
		m.teams, _ = svc.(team.AssistTeams)
	}
	if svc, ok := c.Registry.Get(team.SeatReaderKey); ok {
		m.seats, _ = svc.(team.SeatReader)
	}
	if r, err := gate.ReadReady(m.dir); err == nil {
		m.ready = r
	}
	if m.notify == nil {
		m.notify = func(sessionID, text string) error {
			return peersmod.NotifySelf(m.scriptCtx(), c, m.logf, sessionID, text)
		}
	}
	if m.waker == nil {
		m.waker = func(ctx context.Context, reason, detail string) error {
			svc, ok := c.Registry.Get(watch.WakerKey)
			if !ok {
				return nil // no watch module: nobody to wake
			}
			w, ok := svc.(watch.Waker)
			if !ok {
				return nil
			}
			return w.WakeWatcher(ctx, reason, detail)
		}
	}
	return nil
}

// Start runs the reconciler and the checks at once and then every pollEvery (and right after a switch).
func (m *Module) Start(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	m.ctxMu.Lock()
	m.ctx, m.stop = ctx, cancel
	m.ctxMu.Unlock()
	m.wg.Add(1)
	go func() { // the notifier: one pass per ask, however many asks came while it ran
		defer m.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-m.nkick:
				m.notifyPass()
			}
		}
	}()
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		t := time.NewTicker(m.pollEvery)
		defer t.Stop()
		for {
			m.pass()
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			case <-m.kick:
			}
		}
	}()
	return nil
}

func (m *Module) Stop(context.Context) error {
	m.ctxMu.Lock()
	m.stopped = true
	stop := m.stop
	if stop != nil {
		stop()
	}
	m.ctxMu.Unlock()
	m.wg.Wait()
	m.wakeWG.Wait()
	return nil
}

// Status is what /api/info reads: the module is up.
func (m *Module) Status() map[string]any { return map[string]any{"ready": true} }

func (m *Module) kickPoll() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

func (m *Module) runner(alias string) *sync.Mutex {
	m.runMu.Lock()
	defer m.runMu.Unlock()
	mu := m.runners[alias]
	if mu == nil {
		mu = &sync.Mutex{}
		m.runners[alias] = mu
	}
	return mu
}

// RegisterRoutes mounts the admin-only routes.
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/assist", m.handleStatus)
	mux.HandleFunc("POST /api/assist/host", m.handleHostSet)
	mux.HandleFunc("DELETE /api/assist/host/{alias}", m.handleHostRemove)
	mux.HandleFunc("POST /api/assist/switch", m.handleSwitch)
	mux.HandleFunc("POST /api/assist/reserve", m.handleReserve)
	mux.HandleFunc("POST /api/assist/allow", m.handleAllow)
	mux.HandleFunc("POST /api/assist/deny", m.handleDeny)
	mux.HandleFunc("GET /api/assist/hosts", m.handleHosts)
}

// setReady records alias's check (for rec.Gen) in memory and in assist-ready.json.
func (m *Module) setReady(alias string, rec gate.AssistReady) {
	m.readyMu.Lock()
	defer m.readyMu.Unlock()
	m.ready[alias] = rec
	if err := gate.WriteReady(m.dir, m.ready); err != nil {
		m.logf("[assist] write assist-ready.json: %v", err)
	}
}

func (m *Module) readyCopy() map[string]gate.AssistReady {
	m.readyMu.Lock()
	defer m.readyMu.Unlock()
	out := make(map[string]gate.AssistReady, len(m.ready))
	for k, v := range m.ready {
		out[k] = v
	}
	return out
}

// scriptCtx is the parent of a script's context: the module's, once started.
func (m *Module) scriptCtx() context.Context {
	m.ctxMu.Lock()
	defer m.ctxMu.Unlock()
	if m.ctx == nil {
		return context.Background()
	}
	return m.ctx
}
