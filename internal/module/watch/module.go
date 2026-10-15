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
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/watch"
	"github.com/wake/purdex/internal/watchsettings"
)

// HolderKey is the service-registry key under which the module publishes its HolderStore: the session part (W3) records the
// watcher it starts, wakes and rests through it.
const HolderKey = "watch.holder"

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
}

// New returns the module with production defaults.
func New() *Module {
	return &Module{logf: log.Printf, now: time.Now, limit: peersmod.NewHostLimiter(ipeers.HostRateLimit, ipeers.HostRateWindow, time.Now)}
}

func (m *Module) Name() string           { return "watch" }
func (m *Module) Dependencies() []string { return []string{"peers", "hostconfig"} }

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
	return nil
}

func (m *Module) Start(context.Context) error { return nil }
func (m *Module) Stop(context.Context) error  { return nil }

// RegisterRoutes wires the routes: the peers' holder question, and the local view the CLI reads.
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/peers/watch/holder", m.handlePeerHolder)
	mux.HandleFunc("GET /api/watch/holder", m.handleLocalHolder)
}

// HolderView is the answer about this host's holder: the wire form of both routes.
type HolderView struct {
	HostID       string    `json:"host_id"`
	State        string    `json:"state"`
	SessionID    string    `json:"session_id,omitempty"`
	Since        time.Time `json:"since,omitempty"`
	LastTurn     time.Time `json:"last_turn,omitempty"`
	LastDelivery time.Time `json:"last_delivery,omitempty"`
	// Targets is this host's setting, so a peer can warn when the two differ (nil when the setting cannot be read).
	Targets []string `json:"targets,omitempty"`
}

// view is this host's holder as the routes answer it.
func (m *Module) view() HolderView {
	h := m.holder.Get()
	v := HolderView{State: string(h.State), SessionID: h.SessionID, Since: h.Since, LastTurn: h.LastTurn, LastDelivery: h.LastDelivery}
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
	m.writeJSON(w, http.StatusOK, m.view())
}

// handleLocalHolder serves GET /api/watch/holder to the admin token (the CLI's view of this host).
func (m *Module) handleLocalHolder(w http.ResponseWriter, _ *http.Request) {
	m.writeJSON(w, http.StatusOK, m.view())
}

var errNoSettings = errors.New("the watch setting reader is not available")
