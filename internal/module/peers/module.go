// Package peers implements the "peers" daemon module: GET /api/peers, a
// local inventory of this host's tmux sessions joined with the agent module's
// owner resolution and the Claude Code session registry.
package peers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/middleware"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	ipeers "github.com/wake/purdex/internal/peers"
)

// fetchFunc is the fan-out seam: fetchRemote in production, a fake in tests.
type fetchFunc func(ctx context.Context, client *http.Client, baseURL, bearer string) (ipeers.Envelope, error)

// remoteFetchTimeout bounds each individual host fetch in a scope=all
// fan-out, independent of the shared http.Client's own timeout.
const remoteFetchTimeout = 3 * time.Second

// Module implements core.Module for GET /api/peers.
type Module struct {
	core        *core.Core
	sessions    session.SessionProvider
	owners      agent.OwnerResolver
	registryDir string          // default filepath.Join(home, ".claude", "sessions")
	liveness    ipeers.Liveness // default ipeers.DefaultLiveness()
	budget      time.Duration   // default 2 * time.Second
	now         func() time.Time
	client      *http.Client // default newRemoteClient(); shared across fan-out fetches
	fetch       fetchFunc    // default fetchRemote; test seam
}

// New constructs a peers Module with production defaults. Collaborators
// (sessions, owners) are wired in Init from the service registry.
func New() *Module {
	registryDir := filepath.Join(".claude", "sessions")
	if home, err := os.UserHomeDir(); err == nil {
		registryDir = filepath.Join(home, ".claude", "sessions")
	}
	return &Module{
		registryDir: registryDir,
		liveness:    ipeers.DefaultLiveness(),
		budget:      2 * time.Second,
		now:         time.Now,
		client:      newRemoteClient(),
		fetch:       fetchRemote,
	}
}

func (m *Module) Name() string           { return "peers" }
func (m *Module) Dependencies() []string { return []string{"session", "agent"} }

// Init wires the session provider and owner resolver from the service
// registry. Both are required: unlike some modules' soft-degrade Init, a
// missing dependency here is a hard error naming the missing registry key,
// since GET /api/peers has no meaningful behavior without either.
func (m *Module) Init(c *core.Core) error {
	m.core = c

	svc, ok := c.Registry.Get(session.RegistryKey)
	if !ok {
		return fmt.Errorf("peers: service %q not registered", session.RegistryKey)
	}
	sessions, ok := svc.(session.SessionProvider)
	if !ok {
		return fmt.Errorf("peers: service %q does not implement session.SessionProvider (%T)", session.RegistryKey, svc)
	}
	m.sessions = sessions

	svc, ok = c.Registry.Get(agent.OwnerResolverKey)
	if !ok {
		return fmt.Errorf("peers: service %q not registered", agent.OwnerResolverKey)
	}
	owners, ok := svc.(agent.OwnerResolver)
	if !ok {
		return fmt.Errorf("peers: service %q does not implement agent.OwnerResolver (%T)", agent.OwnerResolverKey, svc)
	}
	m.owners = owners

	return nil
}

func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/peers", m.handlePeers)
	mux.HandleFunc("GET /api/peers/hosts", m.handleListHosts)
	mux.HandleFunc("POST /api/peers/hosts", m.handleAddHost)
	mux.HandleFunc("PUT /api/peers/hosts/{alias}", m.handlePutHost)
	mux.HandleFunc("DELETE /api/peers/hosts/{alias}", m.handleDeleteHost)
}

func (m *Module) Start(context.Context) error { return nil }
func (m *Module) Stop(context.Context) error  { return nil }

// handlePeers serves GET /api/peers. scope unset/"local" returns this
// host's local inventory only; scope=all fans out to every configured peer
// host in parallel (admin principal only — see policy.go's
// HostRoutePolicy, enforced again here in depth); any other scope is 400.
func (m *Module) handlePeers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	scope := r.URL.Query().Get("scope")
	switch scope {
	case "", "local":
		// fine, snapshot below covers this path too.
	case "all":
		principal, ok := middleware.PrincipalFrom(r.Context())
		if !ok || principal.Kind != middleware.PrincipalAdmin {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
			return
		}
	default:
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "unknown scope"})
		return
	}

	// One config snapshot up front, used by every path below (local-only or
	// scope=all), so a concurrent config mutation cannot be observed
	// mid-request — in particular, so the local HostResult{Alias, HostID}
	// row can never disagree with the host/host_id embedded in its own
	// Peers records.
	m.core.CfgMu.RLock()
	hostID := m.core.Cfg.HostID
	alias := m.core.Cfg.PeerAlias()
	hosts := append([]config.PeerHost(nil), m.core.Cfg.Peers.Hosts...)
	m.core.CfgMu.RUnlock()

	if scope != "all" {
		json.NewEncoder(w).Encode(m.localEnvelope(r.Context(), hostID, alias))
		return
	}

	json.NewEncoder(w).Encode(m.allEnvelope(r.Context(), hostID, alias, hosts))
}

// localEnvelope builds this host's own inventory from a caller-supplied
// hostID/alias: the response body for scope unset/"local", and the local
// row's peers/ok/partial/error for scope=all. It never touches CfgMu itself
// — the caller (handlePeers) takes the one config snapshot for the whole
// request, so the local row's labels and the host/host_id embedded in its
// own Peers records are always built from the same values.
func (m *Module) localEnvelope(ctx context.Context, hostID, alias string) ipeers.Envelope {
	deadline := m.now().Add(m.budget)

	writeError := func(errMsg string) ipeers.Envelope {
		return ipeers.Envelope{
			HostID:  hostID,
			OK:      false,
			Error:   errMsg,
			Partial: false,
			Peers:   []ipeers.PeerRecord{},
		}
	}

	instance := m.sessions.TmuxInstance()

	sessions, err := m.sessions.ListSessions()
	if err != nil {
		return writeError(err.Error())
	}

	entries, _, err := ipeers.ReadRegistry(m.registryDir, m.liveness)
	if err != nil {
		return writeError(err.Error())
	}

	summaries := make([]ipeers.SessionSummary, 0, len(sessions))
	owners := make(map[string]ipeers.Owner, len(sessions))
	unresolved := make(map[string]bool)

	for _, s := range sessions {
		summaries = append(summaries, ipeers.SessionSummary{
			Code:         s.Code,
			Name:         s.Name,
			Cwd:          s.Cwd,
			TmuxInstance: s.TmuxInstance,
		})

		if !m.now().Before(deadline) {
			unresolved[s.Code] = true
			continue
		}

		owner, ok, err := m.owners.ResolveSessionOwner(ctx, s.Code)
		if err != nil {
			// The lookup itself failed (tmux read error, resolver timeout,
			// cancelled context) — this is not "no agent". Reporting it as
			// no_agent would tell the SPA a live session has none, so it is
			// reported the same way as a session whose owner lookup never
			// ran: agent:null, reason:"", partial:true (Item 1, #988).
			unresolved[s.Code] = true
			continue
		}
		if ok {
			owners[s.Code] = ipeers.Owner{
				AgentType:  owner.AgentType,
				SessionID:  owner.SessionID,
				Cwd:        owner.Cwd,
				TmuxPaneID: owner.TmuxPaneID,
				LastSeenAt: owner.LastSeenAt,
				Status:     owner.Status,
			}
		}
	}

	// Mirrors handleSessionProvenance (internal/module/agent): the tmux
	// generation is sampled on both sides of the (slow) owner-resolution
	// work. A tmux server that restarted mid-inventory would otherwise let
	// session/owner data straddle two different tmux generations into one
	// answer. Either sample being "" (unknown) means the check cannot fire,
	// and the response proceeds as if nothing had changed.
	if after := m.sessions.TmuxInstance(); instance != "" && after != "" && after != instance {
		return writeError("tmux server restarted during inventory")
	}

	partial := len(unresolved) > 0

	peerRecords := ipeers.Build(ipeers.BuildInput{
		HostID:     hostID,
		Alias:      alias,
		Sessions:   summaries,
		Owners:     owners,
		Unresolved: unresolved,
		Entries:    entries,
		ProxyPIDs:  map[int]bool{},
	})

	return ipeers.Envelope{
		HostID:  hostID,
		OK:      true,
		Partial: partial,
		Peers:   peerRecords,
	}
}

// allEnvelope builds a scope=all response: the local row first (from
// localEnvelope, labeled with the snapshot's alias/host_id), then every
// configured host in order, fetched in parallel — each write lands by index
// into a pre-sized slice so the result order matches config order
// regardless of which goroutine finishes first.
func (m *Module) allEnvelope(ctx context.Context, hostID, alias string, hosts []config.PeerHost) ipeers.AllEnvelope {
	results := make([]ipeers.HostResult, len(hosts)+1)

	local := m.localEnvelope(ctx, hostID, alias)
	results[0] = ipeers.HostResult{
		Alias:   alias,
		HostID:  hostID,
		OK:      local.OK,
		Error:   local.Error,
		Partial: local.Partial,
		Peers:   local.Peers,
	}

	var wg sync.WaitGroup
	for i, h := range hosts {
		i, h := i, h
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i+1] = m.fetchHostResult(ctx, h)
		}()
	}
	wg.Wait()

	return ipeers.AllEnvelope{Hosts: results}
}

// fetchHostResult fetches one configured peer host's inventory for a
// scope=all fan-out, translating every failure mode (no outbound token,
// transport/decode error, host_id mismatch) into a failed HostResult rather
// than propagating an error.
func (m *Module) fetchHostResult(ctx context.Context, h config.PeerHost) ipeers.HostResult {
	if h.Token == "" {
		return ipeers.HostResult{
			Alias:  h.Alias,
			HostID: h.HostID,
			OK:     false,
			Error:  "no outbound token",
			Peers:  []ipeers.PeerRecord{},
		}
	}

	fetchCtx, cancel := context.WithTimeout(ctx, remoteFetchTimeout)
	defer cancel()

	env, err := m.fetch(fetchCtx, m.client, h.URL, h.Token)
	if err != nil {
		return ipeers.HostResult{
			Alias:  h.Alias,
			HostID: h.HostID,
			OK:     false,
			Error:  err.Error(),
			Peers:  []ipeers.PeerRecord{},
		}
	}

	if h.HostID != "" && env.HostID != h.HostID {
		return ipeers.HostResult{
			Alias:  h.Alias,
			HostID: h.HostID,
			OK:     false,
			Error:  fmt.Sprintf("host_id mismatch: got %s", env.HostID),
			Peers:  []ipeers.PeerRecord{},
		}
	}

	resultHostID := h.HostID
	if resultHostID == "" {
		resultHostID = env.HostID
	}

	peers := env.Peers
	if peers == nil {
		peers = []ipeers.PeerRecord{}
	}

	return ipeers.HostResult{
		Alias:   h.Alias,
		HostID:  resultHostID,
		OK:      env.OK,
		Error:   env.Error,
		Partial: env.Partial,
		Peers:   peers,
	}
}
