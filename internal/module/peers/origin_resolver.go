package peers

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// OriginResolverKey is the service-registry key under which Init publishes
// the module's *OriginResolver, for the team module (lead-team spec §6.2:
// "the existing findOrigin attributes it by inbox").
const OriginResolverKey = "peers.origin-resolver"

// OriginResolver attributes an approval request's caller to a live Claude
// Code session from the registry. It is a thin view over the peers module
// — the same registry dir, liveness and proxy-pid set GET /api/peers uses
// — and deliberately not m.origin(): that one is a self-route verb that
// fails when the title store is nil.
type OriginResolver struct{ m *Module }

// ResolveOrigin returns the live, non-proxy registry entry whose inbox is
// inbox, as a team.Origin. ok is false (and err nil) for an empty inbox, an
// unknown or dead one and a proxy helper: the registry was read and none of
// those is a session. err is non-nil only when the registry could not be
// read (logged) — the caller must not report that as an unknown origin; the
// team handler answers 503 not_ready so the CLI retries.
func (r *OriginResolver) ResolveOrigin(inbox string) (team.Origin, bool, error) {
	if inbox == "" {
		return team.Origin{}, false, nil
	}
	entries, _, err := ipeers.ReadRegistry(r.m.registryDir, r.m.liveness)
	if err != nil {
		r.m.logf("peers: origin resolver: read registry: %v", err)
		return team.Origin{}, false, fmt.Errorf("read registry: %w", err)
	}
	e, found := findOriginEntry(entries, r.m.proxyPIDs(), inbox)
	if !found {
		return team.Origin{}, false, nil
	}
	return r.originOf(e), true, nil
}

// ResolveOriginBySession is ResolveOrigin keyed by the CC session id the
// relay mod reports (lead-team-relay spec §8.3): the live, non-proxy
// registry entry with that session id. Two live entries for one session id
// (a process pair mid-resume) answer the first in registry order; both
// describe the same conversation, and the Origin fields that differ (pid,
// inbox) are display and liveness only.
func (r *OriginResolver) ResolveOriginBySession(sessionID string) (team.Origin, bool, error) {
	if sessionID == "" {
		return team.Origin{}, false, nil
	}
	entries, _, err := ipeers.ReadRegistry(r.m.registryDir, r.m.liveness)
	if err != nil {
		r.m.logf("peers: origin resolver: read registry: %v", err)
		return team.Origin{}, false, fmt.Errorf("read registry: %w", err)
	}
	proxies := r.m.proxyPIDs()
	for _, e := range entries {
		if e.SessionID == sessionID && !e.IsProxy && !proxies[e.PID] {
			return r.originOf(e), true, nil
		}
	}
	return team.Origin{}, false, nil
}

// originOf renders a registry entry as a team.Origin: ref, address (the
// rule GET /api/peers uses, record.go applyIdentity) and title.
func (r *OriginResolver) originOf(e ipeers.Entry) team.Origin {
	ref := ipeers.RefID(e.SessionID)
	alias := r.m.configSnapshot().alias
	addr := alias + "/" + ref
	if ipeers.RoutableName(e.Name) { // the rule GET /api/peers uses (internal/peers/record.go applyIdentity)
		addr = alias + "/" + e.Name
	}
	return team.Origin{
		SessionID: e.SessionID,
		Ref:       ref,
		Name:      e.Name,
		PID:       e.PID,
		ProcStart: e.ProcStart,
		Cwd:       e.Cwd,
		Tmux:      e.Tmux,
		Title:     r.titleOf(e.SessionID),
		Address:   addr,
	}
}

// titleOf is the session's title from the title store, or "" when there is
// no store, the read fails, or the session has none. The dialog shows the
// title or falls back to the name, so a missing title is never an error.
func (r *OriginResolver) titleOf(sessionID string) string {
	if r.m.titles == nil {
		return ""
	}
	rows, err := r.m.titles.Snapshot()
	if err != nil {
		return ""
	}
	for _, row := range rows {
		if row.SessionID == sessionID {
			return row.Label
		}
	}
	return ""
}

// Presence is what the registry says about one CC session, for a decision
// that cannot be undone (lead-team spec §7.1: a team ends when its lead's
// conversation ends). Unlike LiveSession's bool it keeps "could not tell"
// apart from "gone".
type Presence int

const (
	// PresenceUnknown: the registry could not be listed, its dir does not
	// exist, or it holds a file whose (filename) pid is alive but whose
	// contents could not be verified (BlockingUnknown) — that file may be
	// this session.
	PresenceUnknown Presence = iota
	// PresenceLive: a live, non-proxy entry has this session id.
	PresenceLive
	// PresenceGone: the registry was read, nothing in it blocks, and no
	// live non-proxy entry has this session id.
	PresenceGone
)

// SessionPresence is LiveSession in three states (P4-2 review): the team
// sweeper ends a team only on PresenceGone. ReadRegistry folds an
// unverifiable file into its skipped count and reads a missing dir as an
// empty registry, both with a nil error; here both are PresenceUnknown.
func (r *OriginResolver) SessionPresence(sessionID string) Presence {
	if sessionID == "" {
		return PresenceGone
	}
	if _, err := os.Stat(r.m.registryDir); errors.Is(err, fs.ErrNotExist) {
		return PresenceUnknown
	}
	entries, diag, err := ipeers.ReadRegistryDiag(r.m.registryDir, r.m.liveness)
	if err != nil {
		r.m.logf("peers: origin resolver: read registry: %v", err)
		return PresenceUnknown
	}
	proxies := r.m.proxyPIDs()
	for _, e := range entries {
		if e.SessionID == sessionID && !e.IsProxy && !proxies[e.PID] {
			return PresenceLive
		}
	}
	if len(diag.BlockingUnknown()) > 0 {
		return PresenceUnknown
	}
	return PresenceGone
}

// LiveSession reports whether a live, non-proxy registry entry has this
// CC session id. A registry read error answers true: "unknown" must never
// abandon an open request (PD5), only a registry that was read and does
// not list the session does.
func (r *OriginResolver) LiveSession(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	entries, _, err := ipeers.ReadRegistry(r.m.registryDir, r.m.liveness)
	if err != nil {
		r.m.logf("peers: origin resolver: read registry: %v", err)
		return true
	}
	proxies := r.m.proxyPIDs()
	for _, e := range entries {
		if e.SessionID == sessionID && !e.IsProxy && !proxies[e.PID] {
			return true
		}
	}
	return false
}
