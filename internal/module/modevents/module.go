// Package modeventsmod mounts the mod event channel (internal/modevents)
// in the daemon: it publishes the stream registry in the core
// ServiceRegistry, listens on <data_dir>/mod.sock with the channel's own
// HTTP server, evicts old streams on a ticker, and serves the read API on
// the daemon's TCP mux (api.go). A channel that cannot listen is
// reported, never fatal.
package modeventsmod

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/modevents"
	"github.com/wake/purdex/internal/promptq"
	"github.com/wake/purdex/internal/team"
)

// ServiceName is the core ServiceRegistry key of the *modevents.Registry.
const ServiceName = "modevents"

// evictEvery is the eviction ticker's period.
const evictEvery = time.Minute

// Module is the modevents daemon module.
type Module struct {
	core  *core.Core
	reg   *modevents.Registry
	queue *promptq.Queue // the prompt queue (U3-0b): the conversation API fills it, the mod socket drains it
	path  string

	logf func(string, ...any)
	now  func() time.Time // the registry's clock

	mu     sync.Mutex
	status modevents.Status
	ln     net.Listener
	srv    *http.Server
	cancel context.CancelFunc
	// stopPolls is closed by the first Stop, before the server is shut down: the mods' parked long polls answer at once.
	stopPolls chan struct{}
	// stopDone is made by the first Stop and closed when its cleanup ends;
	// every other Stop waits on it. Non-nil also keeps a later Start from
	// listening again.
	stopDone chan struct{}

	wg      sync.WaitGroup
	running atomic.Int32 // goroutines started by Start and not yet returned
}

// New returns the module; Init creates its registry.
func New() *Module { return &Module{logf: log.Printf, now: time.Now} }

func (m *Module) Name() string           { return ServiceName }
func (m *Module) Dependencies() []string { return nil }

// Init creates the registry, publishes it as ServiceName and works out the
// socket path from the data dir: the resolved path Listen binds at, which
// pdx.json names too. Whether it fits is Listen's call.
func (m *Module) Init(c *core.Core) error {
	m.core = c
	m.reg = modevents.NewRegistry(m.now)
	c.Registry.Register(ServiceName, m.reg)
	m.queue = promptq.New(streamOwners{m.reg})
	c.Registry.Register(promptq.Key, m.queue)
	c.CfgMu.RLock()
	dataDir := c.Cfg.DataDir
	c.CfgMu.RUnlock()
	m.path, _ = modevents.ResolveSocketPath(dataDir)
	return nil
}

// RegisterRoutes adds the read API (spec §6.6) to the daemon's TCP mux,
// behind its TokenAuth like every /api route. The channel's ingest has
// its own server on the socket.
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/mod/streams", m.handleStreams)
	mux.HandleFunc("GET /api/mod/streams/{stream}/events", m.handleEvents)
}

// Start listens and serves the channel, and starts the eviction ticker.
// It never fails: a channel that cannot listen is logged and reported by
// Status.
func (m *Module) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopDone != nil || m.ln != nil {
		return nil
	}
	m.ln, m.status = modevents.Listen(m.path)
	if !m.status.Enabled {
		m.logf("[modevents] disabled: %s", m.status.Reason)
		return nil
	}

	m.stopPolls = make(chan struct{})
	m.srv = modevents.NewServer(modevents.NewHandler(m.reg, modevents.WithTeamReader(m.teamRead), modevents.WithWorkbook(m.workbookService), modevents.WithPrompt(m.promptService), modevents.WithStop(m.stopPolls)))
	srv, ln := m.srv, m.ln
	m.spawn(func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			m.logf("[modevents] serve: %v", err)
		}
	})
	tickCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.spawn(func() {
		t := time.NewTicker(evictEvery)
		defer t.Stop()
		for {
			select {
			case <-tickCtx.Done():
				return
			case <-t.C:
				m.reg.Evict()
			}
		}
	})
	m.logf("[modevents] socket %s", m.path)
	return nil
}

// teamRead asks the team module, looked up at request time so this module needs no dependency on it (team depends on
// peers and others; the socket may be up before the team module has registered).
func (m *Module) teamRead(sessionID string) (modevents.TeamRead, error) {
	svc, ok := m.core.Registry.Get(team.ModReadKey)
	if !ok {
		return modevents.TeamRead{}, modevents.ErrTeamUnavailable
	}
	rd, ok := svc.(team.ModReader)
	if !ok {
		return modevents.TeamRead{}, modevents.ErrTeamUnavailable
	}
	got, err := rd.ModTeamRead(sessionID)
	if err != nil {
		return modevents.TeamRead{}, err
	}
	return modevents.TeamRead{Role: got.Role, Members: got.Members, TeamLabel: got.TeamLabel}, nil
}

// workbookJobsKey is the workbook module's service name for the job routes (workbook.JobsKey); a string here so the
// socket module does not depend on the workbook module. Looked up per request: the workbook module may be absent or late.
const workbookJobsKey = "workbook.jobs"

func (m *Module) workbookService() modevents.WorkbookService {
	svc, ok := m.core.Registry.Get(workbookJobsKey)
	if !ok {
		return nil
	}
	ws, ok := svc.(modevents.WorkbookService)
	if !ok {
		return nil
	}
	// A module that is registered but off (its store would not open) is "unavailable", not "no work".
	if r, has := svc.(interface{ Ready() bool }); has && !r.Ready() {
		return nil
	}
	return ws
}

func (m *Module) spawn(f func()) {
	m.wg.Add(1)
	m.running.Add(1)
	go func() {
		defer m.wg.Done()
		defer m.running.Add(-1)
		f()
	}()
}

// Stop, in this order (spec §6.1): closes the listener, which stops
// accepts and unlinks the socket; shuts the server down within ctx (the
// daemon's shared shutdown budget), closing what is still open when ctx
// expires; stops the ticker and waits for both goroutines. It returns only
// after all three, is idempotent, and is a no-op for a disabled channel.
// The first call does the work; every other call, concurrent or later,
// waits for it to finish, but if its own ctx ends first it closes the
// server's connections (which ends the first call's Shutdown) and then
// waits. Nothing in it can fail, so every call returns nil.
func (m *Module) Stop(ctx context.Context) error {
	m.mu.Lock()
	if done := m.stopDone; done != nil {
		srv := m.srv
		m.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			if srv != nil {
				m.logf("[modevents] stop: %v; closing connections", ctx.Err())
				_ = srv.Close()
			}
			<-done
		}
		return nil
	}
	done := make(chan struct{})
	m.stopDone = done
	ln, srv, cancel := m.ln, m.srv, m.cancel
	if m.stopPolls != nil {
		close(m.stopPolls) // under m.mu and behind the stopDone gate: closed once
	}
	m.mu.Unlock()
	defer close(done)

	if ln != nil {
		_ = ln.Close()
	}
	if srv != nil {
		if err := srv.Shutdown(ctx); err != nil {
			m.logf("[modevents] shutdown: %v; closing connections", err)
			_ = srv.Close()
		}
	}
	if cancel != nil {
		cancel()
	}
	m.wg.Wait()
	return nil
}

// Status reports whether the channel listens and, when not, why.
func (m *Module) Status() modevents.Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// SocketPathForInfo is the resolved socket path the channel binds at (or
// would), also when the channel is disabled.
func (m *Module) SocketPathForInfo() string { return m.path }
