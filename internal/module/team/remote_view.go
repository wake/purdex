// internal/module/team/remote_view.go
package teammod

import (
	"context"
	"sync"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// What a lead host shows of a remote member's context and model (cross-host team spec §8) comes from the member host's
// GET /api/peers, read through the host caller (3 s per host). Two ways to ask, one cache:
//
//   - GET /api/team (an explicit `pdx team`) refreshes the stale hosts first and waits, bounded by that 3 s, all hosts at
//     once: the table is complete when it is the answer to a question;
//   - the roster (the App's event and GET /api/team/roster) NEVER waits on a remote call: it reads the cache and starts a
//     background refresh of the stale hosts, so a slow or dead member host cannot hold up a roster build.
//
// A host's reading is fresh for remoteReadingFreshMs. A host that did not answer is cached as failed for that long too
// (a dead host is not asked on every roster build); its members show blank context and `context_unavailable`.

const remoteReadingFreshMs = 10_000

// peerRecordReader is the host caller's PeerRecords; the module's cmdCaller offers it when it is the real one.
type peerRecordReader interface {
	PeerRecords(ctx context.Context, hostID string) ([]ipeers.PeerRecord, error)
}

// remoteReading is one host's last answer: the context of each session on it by CC session id, or failed.
type remoteReading struct {
	at     int64
	gen    uint64 // the order the reads started in
	failed bool
	ctx    map[string]*team.MemberContext
	// seen are the CC session ids the host listed at all (with or without a context reading): a listed session is live.
	seen map[string]bool
}

type remoteReadings struct {
	mu       sync.Mutex
	gen      uint64
	by       map[string]remoteReading
	inFlight map[string]bool
}

// remoteHostAlias is the alias this host calls hostID by; "" for its own host or one it does not know.
func (m *Module) remoteHostAlias(hostID string) string {
	if hostID == "" || hostID == m.hostID() || m.cmdCaller == nil {
		return ""
	}
	return m.cmdCaller.AliasOf(hostID)
}

// remoteHostsOf is the distinct remote host ids of rows.
func (m *Module) remoteHostsOf(rows []memberRow) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range rows {
		// Only members in play: a host that holds finished rows alone is not asked (a dead old host must not slow a view).
		if m.isRemoteRow(r) && remoteLiveState(r.State) && !seen[r.HostID] {
			seen[r.HostID] = true
			out = append(out, r.HostID)
		}
	}
	return out
}

func (m *Module) staleRemoteHosts(hostIDs []string) []string {
	now := m.now()
	m.remote.mu.Lock()
	defer m.remote.mu.Unlock()
	var out []string
	for _, h := range hostIDs {
		if r, ok := m.remote.by[h]; !ok || now-r.at >= remoteReadingFreshMs {
			out = append(out, h)
		}
	}
	return out
}

// readRemoteHost asks one host and stores the answer (or the failure).
func (m *Module) readRemoteHost(ctx context.Context, hostID string) {
	rr := remoteReading{at: m.now()}
	m.remote.mu.Lock()
	m.remote.gen++
	rr.gen = m.remote.gen
	m.remote.mu.Unlock()
	if m.peerRecords == nil {
		rr.failed = true
	} else if rows, err := m.peerRecords(ctx, hostID); err != nil {
		rr.failed = true
	} else {
		rr.ctx, rr.seen = map[string]*team.MemberContext{}, map[string]bool{}
		for _, row := range rows {
			a := row.Agent
			if a == nil || a.SessionID == "" {
				continue
			}
			rr.seen[a.SessionID] = true
			if a.Context == nil {
				continue
			}
			c := team.MemberContext{UsedPercentage: a.Context.UsedPercentage, Window: a.Context.Window,
				ModelID: a.Context.ModelID, Effort: a.Context.Effort, At: a.Context.At}
			rr.ctx[a.SessionID] = &c
		}
	}
	m.remote.mu.Lock()
	if m.remote.by == nil {
		m.remote.by = map[string]remoteReading{}
	}
	// Two reads of one host can overlap (a background refresh and a GET /api/team): the one that STARTED later wins,
	// whichever finishes last.
	if have, ok := m.remote.by[hostID]; !ok || have.gen < rr.gen {
		m.remote.by[hostID] = rr
	}
	m.remote.mu.Unlock()
}

// refreshRemoteReadings brings the stale hosts' readings up to date and waits for them (all at once).
func (m *Module) refreshRemoteReadings(ctx context.Context, hostIDs []string) {
	stale := m.staleRemoteHosts(hostIDs)
	var wg sync.WaitGroup
	for _, h := range stale {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.readRemoteHost(ctx, h)
		}()
	}
	wg.Wait()
}

// kickRemoteReadings starts a background refresh of each stale host that has none running; it never waits.
func (m *Module) kickRemoteReadings(hostIDs []string) {
	for _, h := range m.staleRemoteHosts(hostIDs) {
		m.remote.mu.Lock()
		if m.remote.inFlight == nil {
			m.remote.inFlight = map[string]bool{}
		}
		busy := m.remote.inFlight[h]
		m.remote.inFlight[h] = true
		m.remote.mu.Unlock()
		if busy {
			continue
		}
		if !m.goTracked(func() {
			defer func() {
				m.remote.mu.Lock()
				delete(m.remote.inFlight, h)
				m.remote.mu.Unlock()
			}()
			m.readRemoteHost(m.stopCtx, h)
			m.rosterChanged() // the numbers just arrived: the App's roster shows them
		}) {
			m.remote.mu.Lock()
			delete(m.remote.inFlight, h)
			m.remote.mu.Unlock()
		}
	}
}

// remoteListed says whether the host's last answer listed the session (live on its host).
func (m *Module) remoteListed(hostID, sessionID string) bool {
	m.remote.mu.Lock()
	defer m.remote.mu.Unlock()
	return m.remote.by[hostID].seen[sessionID]
}

// remoteContextOf is a remote member's cached context, and whether its host failed to answer (then blank).
// Not yet read: nil, false.
func (m *Module) remoteContextOf(hostID, sessionID string) (c *team.MemberContext, unavailable bool) {
	m.remote.mu.Lock()
	defer m.remote.mu.Unlock()
	r, ok := m.remote.by[hostID]
	if !ok {
		return nil, false
	}
	if r.failed {
		return nil, true
	}
	return r.ctx[sessionID], false
}

// remoteLiveState: a remote member row the roster shows (spec §4.2: the states with a seat).
func remoteLiveState(s team.MemberState) bool {
	switch s {
	case team.MemberActive, team.MemberJoining, team.MemberReleasing, team.MemberKilling:
		return true
	}
	return false
}

// remoteRosterMember is a remote member's roster entry: addressed by its host's alias, its state as the lead host holds
// it (joining / releasing / killing show as such), context and model from the cache. It is not "live" in the registry
// sense (the registry is this host's); Live follows whether its host answered with the session.
func (m *Module) remoteRosterMember(mr memberRow, quota team.RelayQuota, open map[string][]TaskRow, teamID string) team.RosterMember {
	alias := m.remoteHostAlias(mr.HostID)
	s := team.RosterSession{SessionID: mr.SessionID, Ref: mr.Ref, Address: firstNonEmpty(alias, mr.HostID) + "/" + mr.Ref,
		Title: mr.Title, TmuxSession: mr.TmuxSession, HostID: mr.HostID, HostAlias: alias, RelayQuota: quota}
	s.Context, s.ContextUnavailable = m.remoteContextOf(mr.HostID, mr.SessionID)
	s.Live = m.remoteListed(mr.HostID, mr.SessionID)
	s.Model, s.Effort = mr.Model, mr.Effort
	if s.Context != nil {
		s.Model, s.Effort = firstNonEmpty(s.Model, s.Context.ModelID), firstNonEmpty(s.Effort, s.Context.Effort)
	}
	rm := team.RosterMember{RosterSession: s, State: mr.State, Origin: rosterOriginOf(mr), JoinedAt: mr.CreatedAt}
	if mr.SpawnOp != "" {
		if cur, ok := currentTaskOf(open[mr.SpawnOp]); ok {
			rm.Task = &team.RosterTask{ID: team.TaskDisplayID(teamID, cur.Seq), Subject: cur.Subject, Status: cur.Status}
		}
	}
	return rm
}
