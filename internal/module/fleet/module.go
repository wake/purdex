// Package fleetmod is the daemon's side of the fleet status (spec 2026-10-14-fleet-status-spec, rev 2): the primary / standby
// election and the two peer routes it uses. F-1b2: the module skeleton, state.json, the role engine and the routes. The
// measuring side (sessions, aggregator, GET /api/fleet) follows in F-1c2 and reads the role through fleet.RoleView.
package fleetmod

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/fleet"
	"github.com/wake/purdex/internal/module/hostconfig"
	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
)

// RoleViewKey is the registry key under which the module publishes its fleet.RoleView (the F-1c2 measuring side reads it).
const RoleViewKey = "fleet.role-view"

const (
	pathRole     = "/api/peers/fleet/role"
	pathAnnounce = "/api/peers/fleet/announce"
	maxBody      = 4 << 10
	maxTerm      = 1 << 40
)

// hostCaller is *peersmod.HostCaller as the module uses it (a test seam).
type hostCaller interface {
	Call(ctx context.Context, targetHostID, path string, body any) peersmod.CallResult
	HostIDOf(alias string) string
}

// Module is the fleet module.
type Module struct {
	core    *core.Core
	engine  *Engine
	measure *Measure // the measuring side (F-1c2): sessions, aggregator, GET /api/fleet, the push
	local   fleet.LocalSource
	caller  hostCaller
	limit   *peersmod.HostLimiter
	logf    func(format string, args ...any)
	now     func() time.Time

	// statePath overrides ~/.config/pdx/fleet/state.json (tests).
	statePath string
	// settings overrides the hostconfig-backed source (tests).
	settings SettingsSource

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func New() *Module {
	return &Module{logf: log.Printf, now: time.Now, limit: peersmod.NewHostLimiter(ipeers.HostRateLimit, ipeers.HostRateWindow, time.Now)}
}

func (m *Module) Name() string           { return "fleet" }
func (m *Module) Dependencies() []string { return []string{"peers", "hostconfig", "resources"} }

// Engine is the role engine (nil before Init).
func (m *Module) Engine() *Engine { return m.engine }

// Init resolves the keys the module depends on; a missing LocalSource is an empty FakeLocal, logged once.
func (m *Module) Init(c *core.Core) error {
	m.core = c
	if svc, ok := c.Registry.Get(peersmod.HostCallerKey); ok && m.caller == nil {
		if hc, ok := svc.(hostCaller); ok {
			m.caller = hc
		}
	}
	if svc, ok := c.Registry.Get(fleet.LocalSourceKey); ok {
		if ls, ok := svc.(fleet.LocalSource); ok {
			m.local = ls
		}
	}
	if m.local == nil {
		m.logf("[fleet] no local source registered: the own host reports nothing")
		m.local = fleet.NewFakeLocal()
	}
	if m.settings == nil {
		m.settings = &hostconfigSettings{m: m}
	}
	path := m.statePath
	if path == "" {
		if home, err := userConfigDir(); err == nil {
			path = filepath.Join(home, "pdx", "fleet", "state.json")
		}
	}
	m.engine = NewEngine(peerOf{m}, m.settings, m.now, m.logf, path, m.hostID())
	c.Registry.Register(RoleViewKey, fleet.RoleView(m.engine))
	m.measure = m.newMeasure()
	m.engine.SetNewestAge(m.measure.NewestAge)
	return nil
}

func (m *Module) hostID() string {
	m.core.CfgMu.RLock()
	defer m.core.CfgMu.RUnlock()
	return m.core.Cfg.HostID
}

func (m *Module) Start(ctx context.Context) error {
	ctx, m.cancel = context.WithCancel(ctx)
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.engine.Run(ctx)
	}()
	// OnChange does not replay the current state, so Start registers first and then reads it.
	return m.measure.Start(ctx)
}

func (m *Module) Stop(context.Context) error {
	// The engine goes first: its drain may still announce a primary, and the measure must be the last to hear it.
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
	if m.measure != nil {
		m.measure.Stop()
	}
	return nil
}

func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST "+pathRole, m.handleRole)
	mux.HandleFunc("POST "+pathAnnounce, m.handleAnnounce)
	m.measure.Routes(mux)
}

// ---- settings from hostconfig and the config ----

type hostconfigSettings struct{ m *Module }

func (h *hostconfigSettings) Rev() (int64, error) {
	svc, ok := h.m.core.Registry.Get(hostconfig.FleetConfigKey)
	if !ok {
		return 0, errNoConfig
	}
	if rr, ok := svc.(interface{ FleetRevision() (int64, error) }); ok {
		return rr.FleetRevision()
	}
	return 0, errNoConfig
}

func (h *hostconfigSettings) Read() (Settings, error) {
	svc, ok := h.m.core.Registry.Get(hostconfig.FleetConfigKey)
	if !ok {
		return Settings{}, errNoConfig
	}
	rd, ok := svc.(hostconfig.FleetConfigReader)
	if !ok {
		return Settings{}, errNoConfig
	}
	fc, _, err := rd.FleetConfig()
	if err != nil {
		return Settings{}, err
	}
	h.m.core.CfgMu.RLock()
	self := h.m.core.Cfg.Peers.Alias
	h.m.core.CfgMu.RUnlock()
	return Settings{Order: fc.Order, Enabled: fc.Enabled, Self: self}, nil
}

// ---- the Peer over HostCaller ----

type peerOf struct{ m *Module }

func (p peerOf) target() (hostID string, err error) {
	if p.m.caller == nil {
		return "", errNoCaller
	}
	s := p.m.engine.settingsSnapshot()
	alias := s.peerAlias()
	if alias == "" {
		return "", errNoPeer
	}
	if id := p.m.caller.HostIDOf(alias); id != "" {
		return id, nil
	}
	return "", errNoPeer
}

func (p peerOf) Role(ctx context.Context, req RoleRequest) (RoleAnswer, error) {
	id, err := p.target()
	if err != nil {
		return RoleAnswer{}, err
	}
	res := p.m.caller.Call(ctx, id, pathRole, map[string]any{"to_host_id": id, "from_alias": req.FromAlias, "role": req.Role, "term": req.Term, "order": req.Order})
	if res.Class != peersmod.ClassDone {
		return RoleAnswer{}, callErr(res)
	}
	var a RoleAnswer
	if err := json.Unmarshal(res.Body, &a); err != nil {
		return RoleAnswer{}, err
	}
	return a, nil
}

func (p peerOf) Announce(ctx context.Context, term int64) error {
	id, err := p.target()
	if err != nil {
		return err
	}
	s := p.m.engine.settingsSnapshot()
	res := p.m.caller.Call(ctx, id, pathAnnounce, map[string]any{"to_host_id": id, "from_alias": s.Self, "term": term})
	if res.Class != peersmod.ClassDone {
		return callErr(res)
	}
	return nil
}

// ---- routes ----

type roleBody struct {
	ToHostID  string `json:"to_host_id"`
	FromAlias string `json:"from_alias"`
	Role      string `json:"role"`
	Term      int64  `json:"term"`
	// Order is the caller's fleet order (role only).
	Order []string `json:"order"`
}

func (m *Module) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		m.logf("[fleet] encode response: %v", err)
	}
}

func (m *Module) writeErr(w http.ResponseWriter, status int, code, detail string) {
	m.writeJSON(w, status, ipeers.APIError{Error: code, Detail: detail})
}

// admit does the team routes' preamble: the binding of the host principal, the rate limit and body cap, the decode, then
// to_host_id. It reports whether the request may go on.
func (m *Module) admit(w http.ResponseWriter, r *http.Request, what string, v any, toHost, fromAlias func() string) bool {
	var entryHost string
	pr, _, berr := peersmod.BindHostPrincipal(r, what, func(alias string) (cfgPeer, bool) {
		e, ok := m.peerEntry(alias)
		entryHost = e.HostID
		return e, ok
	})
	if berr != nil {
		m.logf("[fleet] %s refused (%s): %s", what, berr.Code, berr.Detail)
		m.writeErr(w, berr.Status, berr.Code, berr.Detail)
		return false
	}
	switch st := peersmod.AdmitDecode(w, r, m.limit, entryHost, maxBody, v); st {
	case 0:
	case http.StatusTooManyRequests:
		m.writeErr(w, st, ipeers.ErrRateLimited, "host rate limit exceeded")
		return false
	case http.StatusRequestEntityTooLarge:
		m.writeErr(w, st, ipeers.ErrBadRequest, "body over 4 KiB")
		return false
	default:
		m.writeErr(w, st, ipeers.ErrBadRequest, "invalid JSON body")
		return false
	}
	if toHost() != m.hostID() {
		m.writeErr(w, http.StatusConflict, "wrong_host", "this request is addressed to another host")
		return false
	}
	// from_alias is the authenticated host's own, and one of the fleet's (a third paired host cannot steer the roles)
	if !strings.EqualFold(fromAlias(), pr.Alias) || !m.engine.InOrder(fromAlias()) {
		m.writeErr(w, http.StatusForbidden, ipeers.ErrForbidden, "from_alias is not the calling host's fleet alias")
		return false
	}
	return true
}

func (m *Module) handleRole(w http.ResponseWriter, r *http.Request) {
	var b roleBody
	if !m.admit(w, r, "fleet role", &b, func() string { return b.ToHostID }, func() string { return b.FromAlias }) {
		return
	}
	switch b.Role {
	case fleet.RolePrimary, fleet.RoleStandby, fleet.RoleOff, roleStarting:
	default:
		m.writeErr(w, http.StatusBadRequest, ipeers.ErrBadRequest, "unknown role")
		return
	}
	if strings.TrimSpace(b.FromAlias) == "" || b.Term < 0 || b.Term > maxTerm {
		m.writeErr(w, http.StatusBadRequest, ipeers.ErrBadRequest, "from_alias and term are required")
		return
	}
	m.writeJSON(w, http.StatusOK, m.engine.HandleRole(RoleRequest{FromAlias: b.FromAlias, Role: b.Role, Term: b.Term, Order: b.Order}))
}

func (m *Module) handleAnnounce(w http.ResponseWriter, r *http.Request) {
	var b roleBody
	if !m.admit(w, r, "fleet announce", &b, func() string { return b.ToHostID }, func() string { return b.FromAlias }) {
		return
	}
	if strings.TrimSpace(b.FromAlias) == "" || b.Term < 0 || b.Term > maxTerm {
		m.writeErr(w, http.StatusBadRequest, ipeers.ErrBadRequest, "from_alias and term are required")
		return
	}
	m.engine.HandleAnnounce(b.FromAlias, b.Term)
	m.writeJSON(w, http.StatusOK, struct {
		HostID string `json:"host_id"`
	}{m.hostID()})
}
