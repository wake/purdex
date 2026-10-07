package peers

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

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

// Presence is what the registry and the process table say about a team's
// lead, for a decision that cannot be undone (lead-team spec §7.1: a team
// ends when its lead's conversation ends). Unlike LiveSession's bool it
// keeps "could not tell" apart from "gone".
type Presence int

const (
	PresenceUnknown Presence = iota // nothing proves the conversation ended, nor that it lives
	PresenceLive                    // a live, non-proxy entry has the lead's session id
	PresenceGone                    // the lead's process is dead or reused, or alive in another conversation
)

// LeadPresence is the presence of the lead whose conversation is sessionID
// and whose process is pid, started at procStart, as its lead request
// recorded them. A relay's /clear keeps the process, so they still name the
// lead after its session id moves. Gone comes only from that process —
// never from the registry alone, never from another session's file (P4-2
// review H-3):
//   - a live, non-proxy entry has sessionID: live, in whatever process;
//   - pid is dead, or alive with another start time (reused): gone;
//   - pid alive with that start, and its own verified entry names another
//     session (a manual /clear began a new conversation): gone;
//   - anything else is unknown: the registry dir is missing or cannot be
//     listed, no process was recorded, a start time cannot be read, or the
//     live pid has no verified entry of its own (its file missing,
//     truncated or unreadable).
//
// A relay's own /clear also leaves the pid in another conversation;
// EndTeam's relay guard and P4-3's cleared transaction cover that.
func (r *OriginResolver) LeadPresence(sessionID string, pid int, procStart string) Presence {
	if _, err := os.Stat(r.m.registryDir); errors.Is(err, fs.ErrNotExist) {
		return PresenceUnknown
	}
	entries, _, err := ipeers.ReadRegistry(r.m.registryDir, r.m.liveness)
	if err != nil {
		r.m.logf("peers: origin resolver: read registry: %v", err)
		return PresenceUnknown
	}
	proxies := r.m.proxyPIDs()
	ownEntry := false // pid's own verified entry, under another session id
	for _, e := range entries {
		if e.IsProxy || proxies[e.PID] {
			continue
		}
		if sessionID != "" && e.SessionID == sessionID {
			return PresenceLive
		}
		ownEntry = ownEntry || e.PID == pid
	}
	want, err := ipeers.ParseProcStart(procStart)
	if pid <= 0 || err != nil {
		return PresenceUnknown
	}
	if !r.m.liveness.PidAlive(pid) {
		return PresenceGone
	}
	got, ok := r.startTime(pid)
	if !ok {
		return PresenceUnknown
	}
	if !got.Truncate(time.Second).Equal(want.Truncate(time.Second)) || ownEntry {
		return PresenceGone
	}
	return PresenceUnknown
}

// startTime reads pid's start time as ReadRegistryDiag does (Info when set,
// else StartTime); ok is false when it cannot be read.
func (r *OriginResolver) startTime(pid int) (time.Time, bool) {
	live := r.m.liveness
	var t time.Time
	var err error
	switch {
	case live.Info != nil:
		info, infoErr := live.Info(pid)
		t, err = info.StartTime, infoErr
	case live.StartTime != nil:
		t, err = live.StartTime(pid)
	}
	return t, err == nil && !t.IsZero()
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
