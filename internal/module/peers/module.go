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
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	ipeers "github.com/wake/purdex/internal/peers"
)

// Module implements core.Module for GET /api/peers.
type Module struct {
	core        *core.Core
	sessions    session.SessionProvider
	owners      agent.OwnerResolver
	registryDir string          // default filepath.Join(home, ".claude", "sessions")
	liveness    ipeers.Liveness // default ipeers.DefaultLiveness()
	budget      time.Duration   // default 2 * time.Second
	now         func() time.Time
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
}

func (m *Module) Start(context.Context) error { return nil }
func (m *Module) Stop(context.Context) error  { return nil }

// response is the GET /api/peers envelope.
type response struct {
	HostID  string              `json:"host_id"`
	OK      bool                `json:"ok"`
	Error   string              `json:"error,omitempty"`
	Partial bool                `json:"partial"`
	Peers   []ipeers.PeerRecord `json:"peers"` // never null: []ipeers.PeerRecord{} when empty
}

// handlePeers serves GET /api/peers: this host's local inventory only
// (scope=all, cross-host fan-out, is not yet supported).
func (m *Module) handlePeers(w http.ResponseWriter, r *http.Request) {
	deadline := m.now().Add(m.budget)

	w.Header().Set("Content-Type", "application/json")

	if r.URL.Query().Get("scope") == "all" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "scope=all not supported yet"})
		return
	}

	m.core.CfgMu.RLock()
	hostID := m.core.Cfg.HostID
	alias := m.core.Cfg.PeerAlias()
	m.core.CfgMu.RUnlock()

	writeError := func(errMsg string) {
		json.NewEncoder(w).Encode(response{
			HostID:  hostID,
			OK:      false,
			Error:   errMsg,
			Partial: false,
			Peers:   []ipeers.PeerRecord{},
		})
	}

	sessions, err := m.sessions.ListSessions()
	if err != nil {
		writeError(err.Error())
		return
	}

	entries, _, err := ipeers.ReadRegistry(m.registryDir, m.liveness)
	if err != nil {
		writeError(err.Error())
		return
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

		owner, ok, err := m.owners.ResolveSessionOwner(r.Context(), s.Code)
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

	json.NewEncoder(w).Encode(response{
		HostID:  hostID,
		OK:      true,
		Partial: partial,
		Peers:   peerRecords,
	})
}
