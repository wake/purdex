// Package modeventsmod mounts the mod event channel (internal/modevents)
// in the daemon: it publishes the stream registry in the core
// ServiceRegistry, listens on <data_dir>/mod.sock with the channel's own
// HTTP server, and evicts old streams on a ticker. A channel that cannot
// listen is reported, never fatal.
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
)

// ServiceName is the core ServiceRegistry key of the *modevents.Registry.
const ServiceName = "modevents"

// evictEvery is the eviction ticker's period.
const evictEvery = time.Minute

// Module is the modevents daemon module.
type Module struct {
	reg    *modevents.Registry
	path   string
	pathOK bool

	logf func(string, ...any)

	mu      sync.Mutex
	status  modevents.Status
	ln      net.Listener
	srv     *http.Server
	cancel  context.CancelFunc
	stopped bool

	wg      sync.WaitGroup
	running atomic.Int32 // goroutines started by Start and not yet returned
}

// New returns the module; Init creates its registry.
func New() *Module { return &Module{logf: log.Printf} }

func (m *Module) Name() string           { return ServiceName }
func (m *Module) Dependencies() []string { return nil }

// Init creates the registry, publishes it as ServiceName and works out the
// socket path from the data dir.
func (m *Module) Init(c *core.Core) error {
	m.reg = modevents.NewRegistry(time.Now)
	c.Registry.Register(ServiceName, m.reg)
	c.CfgMu.RLock()
	dataDir := c.Cfg.DataDir
	c.CfgMu.RUnlock()
	m.path, m.pathOK = modevents.SocketPath(dataDir)
	return nil
}

// RegisterRoutes adds nothing to the daemon's TCP mux: the channel has its
// own server on the socket.
func (m *Module) RegisterRoutes(*http.ServeMux) {}

// Start listens and serves the channel, and starts the eviction ticker.
// It never fails: a channel that cannot listen is logged and reported by
// Status.
func (m *Module) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped || m.ln != nil {
		return nil
	}
	if !m.pathOK {
		m.status = modevents.Status{Reason: modevents.ReasonPathTooLong}
	} else {
		var ln net.Listener
		ln, m.status = modevents.Listen(m.path)
		m.ln = ln
	}
	if !m.status.Enabled {
		m.logf("[modevents] disabled: %s", m.status.Reason)
		return nil
	}

	m.srv = modevents.NewServer(modevents.NewHandler(m.reg))
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
func (m *Module) Stop(ctx context.Context) error {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return nil
	}
	m.stopped = true
	ln, srv, cancel := m.ln, m.srv, m.cancel
	m.mu.Unlock()

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

// SocketPathForInfo is the socket path, also when the channel is disabled.
func (m *Module) SocketPathForInfo() string { return m.path }
