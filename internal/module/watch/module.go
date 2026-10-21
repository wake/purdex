// Package watchmod is the daemon's side of the load watcher's session (host-load-control spec §12.3): where the watcher is, and
// what this host holds of it. The detector runs in the resources module (it needs the sampler's tick); this module is the part
// that talks to other hosts. W2a: the holder state and its peer route. Discovery, delivery and the session itself come after.
package watchmod

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent"
	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/watch"
	"github.com/wake/purdex/internal/watchsettings"
)

// HolderKey is the service-registry key under which the module publishes its HolderStore: the session part (W3) records the
// watcher it starts, wakes and rests through it.
const HolderKey = "watch.holder"

// LocatorKey is the key of the module's Locator: the delivery part (W2b) asks it where to send events.
const LocatorKey = "watch.locator"

// maxHolderBody bounds the request of the holder route (it carries only the address).
const maxHolderBody = 4 << 10

// Module is the watch module.
type Module struct {
	core   *core.Core
	holder *watch.HolderStore
	logf   func(format string, args ...any)
	limit  *peersmod.HostLimiter
	now    func() time.Time
	// settings reads the watch setting (nil: the registry's reader is looked up at each use).
	settings func() (watchsettings.Settings, error)
	// localTZ names this host's time zone for the first wake settings document (nil: localZoneName); a test seam.
	localTZ func() string
	// beforeWakeCommit runs in a wake settings PUT after everything is checked and before the source is checked again; duringWakeCommit
	// runs right after the write. Test seams (nil in production).
	beforeWakeCommit, duringWakeCommit func()
	// caller reaches the other hosts (peers.HostCallerKey); nil: only this host can be asked.
	caller hostCaller
	// session starts and wakes the watcher on this host (W3); nil until then.
	session SessionControl
	loc     *watch.Locator

	// panes finds the pdx-watch tmux session; ownerSrc is the agent module's resolver (nil: looked up in the registry). rec is the
	// state of reconcile (reconcile.go); store is the holder as reconcile writes it (nil: holder; a test wraps it).
	panes    paneFinder
	ownerSrc ownerLookup
	rec      reconcileState
	store    holderStore
	// two is the state of the two-awake rule; standDowner delivers the notice to this host's own watcher (nil: logged, not sent).
	two         twoAwake
	standDowner StandDowner
	// chg is the last watch.changed payload; emit is a test seam (nil: the event stream of core).
	chg    changeState
	remote remoteState
	// dlv is the delivery of events to the watcher (delivery.go).
	dlv deliveryState
	// acks is this host's ack log (events.go).
	acks *watch.AckStore
	// evMu orders the counts and the sends of watch.events.
	evMu sync.Mutex
	// a count that failed is sent again after evRetryAfter (a test shortens it); evRetrying says one retry is waiting.
	evRetryAfter time.Duration
	evRetryMu    sync.Mutex
	evRetrying   bool
	// typer types into the pdx-watch pane for a start or wake (nil until Start); sess is session.json; the durations and statFile are
	// seams for the tests (zero: the real values).
	typer          paneTyper
	sess           *watch.SessionStore
	startGrace     time.Duration
	registerWithin time.Duration
	pollEvery      time.Duration
	statFile       func(string) bool
	// rst is the rest of the watcher (rest.go); exit sends /exit to its pane; the others are seams for the tests (nil: the real ones).
	rst restState
	// patrol is the daemon's patrol of the watcher (patrol.go); psrc, wake and patrolEvery are seams for the tests (nil / 0: the real ones).
	patrol      patrolState
	psrc        *patrolSources
	wake        func(ctx context.Context, reason, detail string) error
	patrolEvery time.Duration
	admit       sync.RWMutex // an event is admitted (read side) or the rest acts on its check (write side)
	activity    atomic.Int64 // events being delivered to the watcher: a rest step compares it
	exit        exiter
	mtimeOf     func(string) time.Time
	procStart   func(int) (string, error)
	signalFn    func(int, syscall.Signal) error
	restEvery   time.Duration
	// cooldowns are the failed starts that paired hosts reported.
	cooldowns cooldownBook
	// manualDelivery keeps Start from running the delivery loop: a test calls deliverPass itself.
	manualDelivery bool
	emit           func(core.HostEvent)
	// beforeChgLock is a test seam at the point a notifier is about to wait for the notice lock.
	beforeChgLock func()
	// tickEvery is the period of the safety-net ticker (a test shortens it); unsubscribe and stop undo Start.
	tickEvery   time.Duration
	unsubscribe func()
	stop        context.CancelFunc
	wg          sync.WaitGroup
	// runMu guards running and runCtx: a background reconcile starts only while running, and Stop waits for the ones in flight
	// (the SessionStart subscription may call back after unsubscribe returns).
	runMu   sync.Mutex
	running bool
	ready   bool // Init succeeded and Stop has not run: watch.v1 is announced
	runCtx  context.Context
}

// New returns the module with production defaults.
func New() *Module {
	return &Module{logf: log.Printf, now: time.Now, limit: peersmod.NewHostLimiter(ipeers.HostRateLimit, ipeers.HostRateWindow, time.Now)}
}

func (m *Module) Name() string           { return "watch" }
func (m *Module) Dependencies() []string { return []string{"peers", "hostconfig", "agent"} }

// Init opens holder.json in <data_dir>/watch. A file that cannot be read is set aside and the host holds none; the daemon never
// fails for it.
func (m *Module) Init(c *core.Core) error {
	m.core = c
	path := ""
	if c.Cfg != nil && c.Cfg.DataDir != "" {
		path = filepath.Join(c.Cfg.DataDir, "watch", "holder.json")
	}
	if path == "" {
		m.logf("[watch] no data dir: the holder state is kept in memory only")
		m.holder = watch.NewMemoryHolder()
	} else {
		h, err := watch.OpenHolder(path)
		if err != nil {
			m.logf("[watch] %v", err)
		}
		m.holder = h
	}
	c.Registry.Register(HolderKey, m.holder)
	if svc, ok := c.Registry.Get(peersmod.HostCallerKey); ok && m.caller == nil {
		if hc, ok := svc.(hostCaller); ok {
			m.caller = hc
		}
	}
	remembered := ""
	if c.Cfg != nil && c.Cfg.DataDir != "" {
		remembered = filepath.Join(c.Cfg.DataDir, "watch", "remembered.json")
	}
	m.loc = watch.NewLocator(remembered, asker{m}, m.logf)
	m.rec.registered = m.onRegistered
	m.baseline() // what the module starts with is not a change
	m.loc.SetOnChange(func() { m.notifyChanged(); m.wakeDelivery() })
	dir := ""
	if c.Cfg != nil && c.Cfg.DataDir != "" {
		dir = filepath.Join(c.Cfg.DataDir, "watch")
	}
	m.initDelivery(dir)
	sessPath := ""
	if dir != "" {
		sessPath = filepath.Join(dir, "session.json")
	}
	var serr error
	if m.sess, serr = watch.OpenSession(sessPath); serr != nil {
		m.logf("[watch] %v", serr)
	}
	c.Registry.Register(watch.EventSinkKey, watch.EventSink(m))
	m.runMu.Lock()
	m.ready = true
	m.runMu.Unlock()
	c.Registry.Register(LocatorKey, m.loc)
	c.Registry.Register(watch.WakerKey, watch.Waker(m))
	return nil
}

// Start reconciles once (a watcher that started before this daemon, such as the manual one of 10-16, is picked up without a
// restart of the watcher), then keeps reconciling: on each verified SessionStart from pdx-watch (and on an overflow of that
// subscription), and every 5 minutes as a safety net. Holder reads reconcile when the last one is older than 30 s.
func (m *Module) Start(context.Context) error {
	if m.panes == nil {
		m.panes = watch.NewPaneControl("")
	}
	if m.typer == nil {
		if pc, ok := m.panes.(*watch.PaneControl); ok {
			m.typer = pc
		}
	}
	if m.exit == nil {
		if pc, ok := m.panes.(*watch.PaneControl); ok {
			m.exit = pc
		}
	}
	if m.session == nil && m.typer != nil {
		m.session = &sessionCtl{m: m}
	}
	if m.standDowner == nil {
		m.standDowner = peerStandDowner{m}
	}
	if svc, ok := m.core.Registry.Get(agent.TerminalSessionsKey); ok {
		if ts, ok := svc.(agent.TerminalSessions); ok {
			m.unsubscribe = ts.SubscribeSessionStart(func(ev agent.SessionStartEvent) {
				if ev.Overflow || ev.TmuxSession == watch.PaneSession {
					m.reconcileBackground("session start")
				}
			})
		}
	} else {
		m.logf("[watch] service %q not registered: a watcher is noticed by the ticker and by holder reads only", agent.TerminalSessionsKey)
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.stop = cancel
	m.runMu.Lock()
	m.running, m.runCtx = true, ctx
	m.runMu.Unlock()
	m.reconcileBackground("start")
	if !m.manualDelivery {
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			m.deliveryLoop(ctx)
		}()
	}
	if !m.manualDelivery {
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			m.restLoop(ctx)
		}()
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			m.patrolLoop(ctx)
		}()
	}
	every := m.tickEvery
	if every <= 0 {
		every = safetyNet
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m.reconcileBackground("ticker")
				m.refreshRemoteBackground()
			}
		}
	}()
	return nil
}

// Status is what /api/info reads to announce watch.v1: ready from a successful Init until Stop (the routes are there whether or not
// detection is switched on).
func (m *Module) Status() map[string]any {
	m.runMu.Lock()
	defer m.runMu.Unlock()
	return map[string]any{"ready": m.ready}
}

func (m *Module) Stop(context.Context) error {
	m.runMu.Lock()
	m.running = false
	m.ready = false
	m.runMu.Unlock()
	if m.unsubscribe != nil {
		m.unsubscribe()
	}
	if m.stop != nil {
		m.stop()
	}
	m.wg.Wait()
	return nil
}

// RegisterRoutes wires the routes: the peers' holder question, and the local view the CLI reads.
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/peers/watch/holder", m.handlePeerHolder)
	mux.HandleFunc("POST /api/peers/watch/announce", m.handlePeerAnnounce)
	mux.HandleFunc("POST /api/peers/watch/event", m.handlePeerEvent)
	mux.HandleFunc("GET /api/watch/holder", m.handleLocalHolder)
	mux.HandleFunc("GET /api/watch/status", m.handleStatus)
	mux.HandleFunc("GET /api/watch/unhandled", m.handleUnhandled)
	mux.HandleFunc("GET /api/watch/events", m.handleEvents)
	mux.HandleFunc("POST /api/watch/ack", m.handleAck)
	mux.HandleFunc("POST /api/peers/watch/ack", m.handlePeerAck)
	mux.HandleFunc("POST /api/peers/watch/note", m.handlePeerNote)
	mux.HandleFunc("POST /api/watch/start", m.handleStart)
	mux.HandleFunc("POST /api/watch/wake", m.handleWake)
	mux.HandleFunc("POST /api/peers/watch/start", m.handlePeerStart)
	mux.HandleFunc("POST /api/peers/watch/wake", m.handlePeerWake)
	mux.HandleFunc("POST /api/peers/watch/log", m.handlePeerLog)
	mux.HandleFunc("GET /api/watch/log", m.handleLog)
	m.registerWakeRoutes(mux)
}

// HolderView is the answer about this host's holder: the wire form of both routes.
type HolderView struct {
	HostID       string    `json:"host_id"`
	State        string    `json:"state"`
	SessionID    string    `json:"session_id,omitempty"`
	Since        time.Time `json:"since,omitempty"`
	LastTurn     time.Time `json:"last_turn,omitempty"`
	LastDelivery time.Time `json:"last_delivery,omitempty"`
	// The tmux ids of the watcher this host holds (empty while it rests): what a client needs to attach to pdx-watch.
	TmuxSessionID string `json:"tmux_session_id,omitempty"`
	PaneID        string `json:"pane_id,omitempty"`
	// Starting says a start or wake is in progress on this host; CooldownUntil is when a failed one lets the next be tried.
	Starting      bool       `json:"starting,omitempty"`
	CooldownUntil *time.Time `json:"cooldown_until,omitempty"`
	// Targets is this host's setting, so a peer can warn when the two differ (nil when the setting cannot be read).
	Targets []string `json:"targets,omitempty"`
}

// view is this host's holder as the routes answer it.
func (m *Module) view() HolderView { return m.viewOf(m.holder.Get()) }

// viewOf is the holder h as the routes answer it (this host's id and targets added).
func (m *Module) viewOf(h watch.Holder) HolderView {
	v := HolderView{State: string(h.State), SessionID: h.SessionID, Since: h.Since, LastTurn: h.LastTurn, LastDelivery: h.LastDelivery,
		TmuxSessionID: h.TmuxSessionID, PaneID: h.PaneID}
	if m.sess != nil {
		st := m.sess.Get()
		v.Starting = m.startingNow(st)
		if !st.CooldownUntil.IsZero() {
			until := st.CooldownUntil
			v.CooldownUntil = &until
		}
	}
	if m.core != nil && m.core.Cfg != nil {
		m.core.CfgMu.RLock()
		v.HostID = m.core.Cfg.HostID
		m.core.CfgMu.RUnlock()
	}
	if s, err := m.readSettings(); err == nil {
		v.Targets = s.Targets
	}
	return v
}

func (m *Module) readSettings() (watchsettings.Settings, error) {
	if m.settings != nil {
		return m.settings()
	}
	svc, ok := m.core.Registry.Get(watchsettings.Key)
	if !ok {
		return watchsettings.Settings{}, errNoSettings
	}
	rd, ok := svc.(watchsettings.Reader)
	if !ok {
		return watchsettings.Settings{}, errNoSettings
	}
	return rd.WatchSettings()
}

func (m *Module) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		m.logf("[watch] encode response: %v", err)
	}
}

func (m *Module) writeErr(w http.ResponseWriter, status int, code, detail string) {
	m.writeJSON(w, status, ipeers.APIError{Error: code, Detail: detail})
}

// peerEntry is the live peer entry of alias, with this host's id, from one config snapshot.
func (m *Module) peerEntry(alias string) (config.PeerHost, string, bool) {
	m.core.CfgMu.RLock()
	defer m.core.CfgMu.RUnlock()
	ours := m.core.Cfg.HostID
	if i := m.core.Cfg.Peers.FindPeerHostByAlias(alias); i >= 0 {
		return m.core.Cfg.Peers.Hosts[i], ours, true
	}
	return config.PeerHost{}, ours, false
}

// handlePeerHolder serves POST /api/peers/watch/holder: a paired host asks whether a watcher is awake or resting here. The order
// is the team routes' contract: the binding of the host principal to its live entry, the per-host rate limit and the body cap
// before the body is decoded, then to_host_id. It is POST because HostCaller only POSTs (the plan wrote GET).
func (m *Module) handlePeerHolder(w http.ResponseWriter, r *http.Request) {
	var entry config.PeerHost
	var ours string
	_, _, berr := peersmod.BindHostPrincipal(r, "watch holder", func(alias string) (config.PeerHost, bool) {
		var ok bool
		entry, ours, ok = m.peerEntry(alias)
		return entry, ok
	})
	if berr != nil {
		m.logf("[watch] holder question refused (%s): %s", berr.Code, berr.Detail)
		m.writeErr(w, berr.Status, berr.Code, berr.Detail)
		return
	}
	var req struct {
		ToHostID string `json:"to_host_id"`
	}
	switch st := peersmod.AdmitDecode(w, r, m.limit, entry.HostID, maxHolderBody, &req); st {
	case 0:
	case http.StatusTooManyRequests:
		m.writeErr(w, st, ipeers.ErrRateLimited, "host rate limit exceeded")
		return
	case http.StatusRequestEntityTooLarge:
		m.writeErr(w, st, ipeers.ErrBadRequest, "body over 4 KiB")
		return
	default:
		m.writeErr(w, st, ipeers.ErrBadRequest, "invalid JSON body")
		return
	}
	if req.ToHostID != ours {
		m.writeErr(w, http.StatusConflict, "wrong_host", "this request is addressed to another host")
		return
	}
	m.fresh(r.Context())
	m.writeJSON(w, http.StatusOK, m.view())
}

// handleLocalHolder serves GET /api/watch/holder to the admin token (the CLI's view of this host).
func (m *Module) handleLocalHolder(w http.ResponseWriter, r *http.Request) {
	m.fresh(r.Context())
	m.writeJSON(w, http.StatusOK, m.view())
}

var errNoSettings = errors.New("the watch setting reader is not available")

// Status is this host's view of the watcher (GET /api/watch/status, `pdx watch status`).
type Status struct {
	Self HolderView `json:"self"`
	// Current is the watcher events go to now; Last the last one there was (a forgotten Current leaves it for a wake).
	Current *watch.Where `json:"current,omitempty"`
	Last    *watch.Where `json:"last,omitempty"`
	// Watcher is the watcher events go to (nil: none): this host's own or another's, awake or resting (spec §12.6); the same
	// fields watch.changed carries.
	Watcher  *WatcherView `json:"watcher"`
	Enabled  bool         `json:"enabled"`
	AutoWake bool         `json:"auto_wake"`
	Mode     string       `json:"mode"`
	Targets  []string     `json:"targets"`
}

func (m *Module) handleStatus(w http.ResponseWriter, r *http.Request) {
	m.fresh(r.Context())
	st := Status{Self: m.view()}
	st.Current, st.Last = m.loc.Remembered()
	st.Watcher = m.watcherNow()
	if s, err := m.readSettings(); err == nil {
		st.Enabled, st.AutoWake, st.Mode, st.Targets = s.Enabled, s.AutoWake, s.Mode, s.Targets
	}
	m.writeJSON(w, http.StatusOK, st)
}

// Locator is the module's locator (W2b delivers through it).
func (m *Module) Locator() *watch.Locator { return m.loc }
