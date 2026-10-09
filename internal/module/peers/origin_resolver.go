package peers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	iagent "github.com/wake/purdex/internal/agent"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/resources"
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

// ResolveOriginByRef is ResolveOriginBySession keyed by the conversation's ref ("_xxxxxx", ipeers.RefID of its
// session id): the live, non-proxy registry entry whose RefID is ref. `pdx adopt <ref>` names its target this way (adopt
// plan PL-1c). Same ok/err contract; two live entries with one ref answer the first in registry order. The ref is the
// CURRENT one: a ref a relay replaced is looked up through the team module's lineage, not here.
func (r *OriginResolver) ResolveOriginByRef(ref string) (team.Origin, bool, error) {
	if ref == "" {
		return team.Origin{}, false, nil
	}
	entries, _, err := ipeers.ReadRegistry(r.m.registryDir, r.m.liveness)
	if err != nil {
		r.m.logf("peers: origin resolver: read registry: %v", err)
		return team.Origin{}, false, fmt.Errorf("read registry: %w", err)
	}
	proxies := r.m.proxyPIDs()
	var hit *ipeers.Entry
	for i, e := range entries {
		if e.SessionID == "" || ipeers.RefID(e.SessionID) != ref || e.IsProxy || proxies[e.PID] {
			continue
		}
		if hit != nil && hit.SessionID != e.SessionID {
			// The ref is 6 base36 characters: two conversations can share one. Adopt acts on the answer, so a
			// guess by registry order is not made (the address resolver answers ambiguous for the same case).
			return team.Origin{}, false, ErrAmbiguousRef
		}
		if hit == nil {
			hit = &entries[i] // two processes of ONE session (a resume pair) are one conversation: the first
		}
	}
	if hit == nil {
		return team.Origin{}, false, nil
	}
	return r.originOf(*hit), true, nil
}

// ErrAmbiguousRef is ResolveOriginByRef's answer when two live conversations carry the same ref.
var ErrAmbiguousRef = errors.New("two live sessions carry this ref")

// InboxOf is the messaging socket of sessionID's live, non-proxy registry entry (the notice outbox sends from the
// lead's inbox, PL-1d1). ok is false (err nil) for an empty id and a session the registry does not list as live.
func (r *OriginResolver) InboxOf(sessionID string) (string, bool, error) {
	if sessionID == "" {
		return "", false, nil
	}
	entries, _, err := ipeers.ReadRegistry(r.m.registryDir, r.m.liveness)
	if err != nil {
		r.m.logf("peers: origin resolver: read registry: %v", err)
		return "", false, fmt.Errorf("read registry: %w", err)
	}
	proxies := r.m.proxyPIDs()
	for _, e := range entries {
		if e.SessionID == sessionID && !e.IsProxy && !proxies[e.PID] && e.Inbox != "" {
			return e.Inbox, true, nil
		}
	}
	return "", false, nil
}

// ResolveOriginsBySession is ResolveOriginBySession for many sessions with
// ONE registry read: the roster resolves a whole team set per build, and a
// read forks ps per entry. The filter is the single form's — a live,
// non-proxy entry, the first in registry order for a session id — and each
// match is rendered by originOf. Sessions the registry does not list are
// absent from the map (not an error); err is non-nil only when the registry
// could not be read. Empty ids are ignored, and no ids means no read.
func (r *OriginResolver) ResolveOriginsBySession(sessionIDs []string) (map[string]team.Origin, error) {
	want := make(map[string]struct{}, len(sessionIDs))
	for _, id := range sessionIDs {
		if id != "" {
			want[id] = struct{}{}
		}
	}
	out := make(map[string]team.Origin, len(want))
	if len(want) == 0 {
		return out, nil
	}
	entries, _, err := ipeers.ReadRegistry(r.m.registryDir, r.m.liveness)
	if err != nil {
		r.m.logf("peers: origin resolver: read registry: %v", err)
		return nil, fmt.Errorf("read registry: %w", err)
	}
	proxies := r.m.proxyPIDs()
	for _, e := range entries {
		if _, ok := want[e.SessionID]; !ok || e.IsProxy || proxies[e.PID] {
			continue
		}
		if _, done := out[e.SessionID]; done {
			continue
		}
		out[e.SessionID] = r.originOf(e)
	}
	return out, nil
}

// ListLiveOrigins is every live, non-proxy session of this host, one per session id, in registry order (the
// unattended panel's quota list). A registry read failure is the error; nothing is guessed.
func (r *OriginResolver) ListLiveOrigins() ([]team.Origin, error) {
	entries, _, err := ipeers.ReadRegistry(r.m.registryDir, r.m.liveness)
	if err != nil {
		r.m.logf("peers: origin resolver: read registry: %v", err)
		return nil, fmt.Errorf("read registry: %w", err)
	}
	proxies := r.m.proxyPIDs()
	seen := map[string]bool{}
	out := make([]team.Origin, 0, len(entries))
	for _, e := range entries {
		if e.IsProxy || proxies[e.PID] || e.SessionID == "" || seen[e.SessionID] {
			continue
		}
		seen[e.SessionID] = true
		out = append(out, r.originOf(e))
	}
	return out, nil
}

// originOf renders a registry entry as a team.Origin: ref, address (the
// rule GET /api/peers uses, record.go applyIdentity) and title. Name stays
// the registry name; the address is the conversation's virtual name, from
// the same namer and store the listing reads, else its ref — so a lead or
// team notice built from it names the conversation as `pdx peers` does.
//
// The team module's callers carry no ctx (the interface predates naming),
// so the naming is bounded here by namerTimeout: a name store that does not
// answer costs the origin its virtual name (the ref form), never the caller.
func (r *OriginResolver) originOf(e ipeers.Entry) team.Origin {
	ref := ipeers.RefID(e.SessionID)
	alias := r.m.configSnapshot().alias
	addr := alias + "/" + ref
	ctx, cancel := context.WithTimeout(context.Background(), namerTimeout)
	defer cancel()
	if vn := r.m.virtualNamesOf(ctx, e)[e.SessionID]; ipeers.RoutableName(vn) {
		addr = alias + "/" + vn
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
// review H-3). In order:
//  1. a live, non-proxy registry entry has sessionID: live, in whatever
//     process;
//  2. the lead's own process, from the process table and never the
//     registry: no process recorded, or a start time that cannot be read,
//     is unknown; pid dead, or alive with another start time (reused), is
//     gone — whether or not the registry could be read;
//  3. that process alive: its own verified entry under another session
//     (a manual /clear began a new conversation) is gone; anything else —
//     no entry of its own (file missing, truncated, unreadable), or a
//     registry that is missing, empty or unlistable — is unknown.
//
// A relay's own /clear also leaves the pid in another conversation;
// EndTeam's relay guard and P4-3's cleared transaction cover that.
func (r *OriginResolver) LeadPresence(sessionID string, pid int, procStart string) Presence {
	// A missing dir reads as empty and an error leaves entries nil: either
	// way no entry, so only step 2 can say gone.
	entries, _, readErr := ipeers.ReadRegistry(r.m.registryDir, r.m.liveness)
	if readErr != nil {
		r.m.logf("peers: origin resolver: read registry: %v", readErr)
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
	if !got.Truncate(time.Second).Equal(want.Truncate(time.Second)) {
		return PresenceGone
	}
	if ownEntry {
		return PresenceGone
	}
	return PresenceUnknown
}

// SameProcess is LeadPresence's step 2 alone: pid is alive and was started at procStart (to the second, as
// LeadPresence compares). A dead or reused pid is false, nil; a procStart that does not parse or a start time
// that cannot be read is an error, so the caller never signals on a guess.
func (r *OriginResolver) SameProcess(pid int, procStart string) (bool, error) {
	want, err := ipeers.ParseProcStart(procStart)
	if pid <= 0 || err != nil {
		return false, fmt.Errorf("process %d: start time %q cannot be verified", pid, procStart)
	}
	if !r.m.liveness.PidAlive(pid) {
		return false, nil
	}
	got, ok := r.startTime(pid)
	if !ok {
		return false, fmt.Errorf("process %d: start time cannot be read", pid)
	}
	return got.Truncate(time.Second).Equal(want.Truncate(time.Second)), nil
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

// readRegistry is ipeers.ReadRegistry, a seam so a test can see the Liveness
// ProcessRoots builds.
var readRegistry = ipeers.ReadRegistry

// rootView is the slice of a process snapshot ProcessRoots reads. The
// embedded ProcessView is there so a test can prove ProcessRoots never calls
// Read (argv); *agent.ProcessSnapshot is the production value.
type rootView interface {
	iagent.ProcessView
	Start(pid int) (time.Time, error)
}

// ProcessRoots lists the live, non-proxy sessions of the registry as
// resources.Root, for the host resource sampler (host-resource-lease plan,
// Task 0.5). Liveness comes from snap alone: PidAlive and StartTime read the
// snapshot, Info stays nil (snap.Read reads argv and may fork ps) and Zombie
// stays nil, so the pass forks nothing. Proxy helpers are dropped by
// proxyPIDs, not by argv. A session whose start time the snapshot cannot
// read is classified unknown by the registry read and left out; a pid whose
// start time differs from the registry file's (reused) is left out too, so
// every Root names the process currently running under its pid.
func (r *OriginResolver) ProcessRoots(snap *iagent.ProcessSnapshot) ([]resources.Root, error) {
	if snap == nil {
		return nil, errors.New("process roots: nil process snapshot")
	}
	return r.processRoots(snap)
}

func (r *OriginResolver) processRoots(view rootView) ([]resources.Root, error) {
	live := ipeers.Liveness{
		Stat: func(path string) error {
			_, err := os.Stat(path)
			return err
		},
		PidAlive:  view.Alive,
		StartTime: view.Start,
	}
	entries, _, err := readRegistry(r.m.registryDir, live)
	if err != nil {
		r.m.logf("peers: origin resolver: read registry: %v", err)
		return nil, fmt.Errorf("read registry: %w", err)
	}
	proxies := r.m.proxyPIDs()
	roots := make([]resources.Root, 0, len(entries))
	// A session id is one root. After a resume the old process can linger
	// (a zombie keeps its registry file and inbox) next to the new one under
	// the same id; the snapshot has no process state to tell them apart, so
	// the process that started last is the session.
	at := map[string]int{} // session id -> index in roots
	began := map[string]time.Time{}
	for _, e := range entries {
		if e.SessionID == "" || e.IsProxy || proxies[e.PID] {
			continue
		}
		start, _ := view.Start(e.PID) // the registry read vouched for it already
		root := resources.Root{
			SessionID: e.SessionID,
			PID:       e.PID,
			ProcStart: e.ProcStart,
			Tmux:      e.Tmux,
			Cwd:       e.Cwd,
		}
		if i, twin := at[e.SessionID]; twin {
			if start.After(began[e.SessionID]) {
				roots[i], began[e.SessionID] = root, start
			}
			continue
		}
		at[e.SessionID], began[e.SessionID] = len(roots), start
		roots = append(roots, root)
	}
	return roots, nil
}
