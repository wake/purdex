// internal/module/team/host_roots_reconciler.go — delivering the root changes of this lead host to the member hosts (cross-host roots
// spec 2026-10-12 §3.2, XR-2a). A coordinator wakes on a new change and every 60 s and starts one worker per host with pending
// changes; a host has at most one call in flight, so an offline host (HostCaller.Call can wait ~12 s) never delays another. The
// changes of a host go in rowid order, one call each; a head that cannot be delivered stops its host for the pass.
package teammod

import (
	"encoding/json"
	"slices"
	"sync"
	"time"

	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// rootsEvery is the reconciler's own period; a new change wakes it at once.
const rootsEvery = 60 * time.Second

// What the last contact with a host said (spec §3.2), in memory: "unknown" before the first.
const (
	RootsStatusUnknown      = "unknown"
	RootsStatusOK           = "ok"
	RootsStatusOffline      = "offline"      // transport, 5xx, 429, timeout, an unreadable answer, capabilities that cannot be read
	RootsStatusConsentOff   = "consent_off"  // 403 host_not_allowed: the member host has not allowed this lead host
	RootsStatusUnsupported  = "unsupported"  // no roots_change capability, or a 404
	RootsStatusUnauthorized = "unauthorized" // 401
)

// rootsState is the reconciler's in-memory state; the changes, the cache and the 401 stamps are in team.db.
type rootsState struct {
	mu      sync.Mutex
	running map[string]bool
	status  map[string]string
	caps    map[string]cachedCaps
}

// RootsHostStatus is the status class of the last contact with the host.
func (m *Module) RootsHostStatus(hostID string) string {
	m.roots.mu.Lock()
	defer m.roots.mu.Unlock()
	if s, ok := m.roots.status[hostID]; ok {
		return s
	}
	return RootsStatusUnknown
}

func (m *Module) setRootsStatus(hostID, s string) {
	m.roots.mu.Lock()
	defer m.roots.mu.Unlock()
	if m.roots.status == nil {
		m.roots.status = map[string]string{}
	}
	m.roots.status[hostID] = s
}

// kickRoots wakes the coordinator; it never blocks.
func (m *Module) kickRoots() {
	select {
	case m.rootsSig <- struct{}{}:
	default:
	}
}

// startRootsReconciler starts the coordinator when the host caller is there (Init).
func (m *Module) startRootsReconciler() {
	if m.cmdCaller == nil {
		return
	}
	m.sweepWG.Add(1)
	go m.runRootsReconciler()
}

func (m *Module) runRootsReconciler() {
	defer m.sweepWG.Done()
	t := time.NewTicker(rootsEvery)
	defer t.Stop()
	for {
		m.rootsPass()
		select {
		case <-m.stopCtx.Done():
			return
		case <-m.rootsSig:
		case <-t.C:
		}
	}
}

// rootsPass starts a worker for every host with pending changes that has none running.
func (m *Module) rootsPass() {
	if m.cmdCaller == nil || m.stopping() {
		return
	}
	hosts, err := m.store.PendingRootChangeHosts()
	if err != nil {
		m.logf("[team] host roots: %v", err)
		return
	}
	for _, h := range hosts {
		m.roots.mu.Lock()
		if m.roots.running[h] || m.stopping() {
			m.roots.mu.Unlock()
			continue
		}
		if m.roots.running == nil {
			m.roots.running = map[string]bool{}
		}
		m.roots.running[h] = true
		m.roots.mu.Unlock()
		m.sweepWG.Add(1) // the coordinator's own goroutine is counted: this Add cannot race Stop's Wait
		go func(h string) {
			defer m.sweepWG.Done()
			m.rootsWorker(h)
		}(h)
	}
}

// rootsWorker is one host's worker: it drains the host, then gives the host back. A change inserted (and the coordinator woken)
// after the drain's last look but before the host was given back found it "running" and was skipped: when the drain ended because
// nothing was pending, look once more after giving the host back and wake the coordinator if something is (a host that stopped on a
// failure is NOT woken again at once — it waits for the next period).
func (m *Module) rootsWorker(h string) {
	drained := m.drainRootsHost(h)
	if m.rootsAfterDrainEmpty != nil {
		m.rootsAfterDrainEmpty() // test seam: a change arrives in the gap
	}
	m.roots.mu.Lock()
	delete(m.roots.running, h)
	m.roots.mu.Unlock()
	if drained && !m.stopping() {
		if _, ok, _ := m.store.HeadRootChange(h); ok {
			m.kickRoots()
		}
	}
}

// rootsCapsOf is the host's capabilities, reused for capsTTL.
func (m *Module) rootsCapsOf(hostID string) (ipeers.TeamCaps, error) {
	m.roots.mu.Lock()
	c, ok := m.roots.caps[hostID]
	m.roots.mu.Unlock()
	if ok && m.now()-c.at < capsTTL {
		return c.caps, nil
	}
	caps, err := m.cmdCaller.TeamCaps(m.stopCtx, hostID)
	if err != nil {
		return ipeers.TeamCaps{}, err
	}
	m.roots.mu.Lock()
	if m.roots.caps == nil {
		m.roots.caps = map[string]cachedCaps{}
	}
	m.roots.caps[hostID] = cachedCaps{caps: caps, at: m.now()}
	m.roots.mu.Unlock()
	return caps, nil
}

// drainRootsHost delivers the host's pending changes in order until one has to wait. It reports whether it ended because the host
// has nothing pending (true) rather than on a wait, a failure or the stop (false).
func (m *Module) drainRootsHost(hostID string) (drained bool) {
	for !m.stopping() {
		head, ok, err := m.store.HeadRootChange(hostID)
		if err != nil {
			m.logf("[team] host roots %s: %v", hostID, err)
			return false
		}
		if !ok {
			return true
		}
		if !m.cmdCaller.Paired(hostID) {
			// the relation is gone (spec §3.2): the host's pending changes end with it, in unpairHost's transaction
			if err := m.unpairHost(hostID, "unpaired"); err != nil {
				m.logf("[team] host roots %s: unpair: %v", hostID, err)
			}
			return false
		}
		caps, err := m.rootsCapsOf(hostID)
		if err != nil {
			m.setRootsStatus(hostID, RootsStatusOffline)
			return false
		}
		if !caps.RootsChange {
			m.setRootsStatus(hostID, RootsStatusUnsupported)
			return false
		}
		if !m.attemptRootChange(head) {
			return false
		}
	}
	return false
}

// attemptRootChange makes the one call for the head change and acts on its class; true means the change was settled (done or
// refused) and the host's next one may go at once.
func (m *Module) attemptRootChange(c HostRootChange) bool {
	res := m.cmdCaller.Call(m.stopCtx, c.HostID, team.RootsChangeRoute, team.RootsChangeRequest{ID: c.ID, ToHostID: c.HostID, Op: c.Op, Path: c.Path})
	now := m.now()
	if res.Class != peersmod.ClassUnauthorized {
		// a run of 401s ends with any other answer
		if err := m.store.Clear401OfHead(c.HostID); err != nil {
			m.logf("[team] host roots %s: %v", c.HostID, err)
		}
	}
	switch res.Class {
	case peersmod.ClassDone:
		ans, why := completeRootsAnswer(res.Body, c)
		if why != "" {
			// not a whole answer to this request (a broken or newer peer): nothing is settled and nothing cached; the request
			// converges when it is sent again
			m.setRootsStatus(c.HostID, RootsStatusOffline)
			m.logf("[team] host roots %s: the answer to %s is not usable (%s); kept pending", c.HostID, c.ID, why)
			return false
		}
		if err := m.store.SettleRootChangeDone(c.ID, ans.Path, *ans.TeamRoots, *ans.TeamRootsRev, now); err != nil {
			m.logf("[team] host roots %s: %v", c.HostID, err)
			return false
		}
		m.setRootsStatus(c.HostID, RootsStatusOK)
		m.rosterChanged()
		return true
	case peersmod.ClassRefused, peersmod.ClassWrongHost:
		code := res.Code
		if code == "" && res.Class == peersmod.ClassWrongHost {
			code = "wrong_host"
		}
		if err := m.store.RefuseRootChange(c.ID, code, res.Detail, now); err != nil {
			m.logf("[team] host roots %s: %v", c.HostID, err)
			return false
		}
		if code == errHostNotAllowed {
			m.setRootsStatus(c.HostID, RootsStatusConsentOff)
		} else {
			m.setRootsStatus(c.HostID, RootsStatusOK) // it answered: the host is reachable and authorised, it only refused this change
		}
		m.rosterChanged()
		return true
	case peersmod.ClassUnauthorized:
		m.setRootsStatus(c.HostID, RootsStatusUnauthorized)
		first, err := m.store.Stamp401OfHead(c.HostID, now)
		if err != nil {
			m.logf("[team] host roots %s: %v", c.HostID, err)
			return false
		}
		if first != 0 && peersmod.Escalate401(time.UnixMilli(first), time.UnixMilli(now)) == peersmod.ClassUnpairedByPeer {
			// the same escalation as the commands pump (base §3.1-6): the peer has not known our token for ten minutes
			m.logf("[team] host roots %s: 401 for %s; ending the relation on this side", c.HostID, time.Duration(now-first)*time.Millisecond)
			if err := m.unpairHost(c.HostID, "unpaired_by_peer"); err != nil {
				m.logf("[team] host roots %s: unpair: %v", c.HostID, err)
			}
		}
		return false
	case peersmod.ClassUnsupported:
		m.setRootsStatus(c.HostID, RootsStatusUnsupported)
		return false
	case peersmod.ClassUnpaired, peersmod.ClassUnpairedByPeer:
		if err := m.unpairHost(c.HostID, string(res.Class)); err != nil {
			m.logf("[team] host roots %s: unpair: %v", c.HostID, err)
		}
		return false
	default: // transient and anything unexpected
		m.setRootsStatus(c.HostID, RootsStatusOffline)
		return false
	}
}

// rootsAnswer is the 200 body with the fields that must be there as pointers: a body that lacks them is not an answer.
type rootsAnswer struct {
	ID           string    `json:"id"`
	HostID       string    `json:"host_id"`
	Op           string    `json:"op"`
	Result       string    `json:"result"`
	Path         string    `json:"path"`
	TeamRoots    *[]string `json:"team_roots"`
	TeamRootsRev *int64    `json:"team_roots_rev"`
}

// completeRootsAnswer decodes the 200 body and checks it is the whole answer to c: its id, its host, its op, a known result and the
// grant after the request (team_roots and team_roots_rev both present). why names what is wrong, "" when nothing is.
func completeRootsAnswer(body []byte, c HostRootChange) (rootsAnswer, string) {
	var a rootsAnswer
	if err := json.Unmarshal(body, &a); err != nil {
		return a, "unreadable"
	}
	switch {
	case a.ID != c.ID:
		return a, "another request's id"
	case a.HostID != c.HostID:
		return a, "another host's id"
	case a.Op != c.Op:
		return a, "another op"
	case a.Result != team.RootsResultAdded && a.Result != team.RootsResultRemoved && a.Result != team.RootsResultUnchanged:
		return a, "unknown result"
	case a.TeamRoots == nil || a.TeamRootsRev == nil:
		return a, "no team_roots / team_roots_rev"
	}
	// the result must fit the op and the grant it reports: an add leaves its canonical path in the roots; a remove that removed
	// leaves its matched root out of them (a body that says otherwise did not apply the change)
	has := func(p string) bool { return p != "" && slices.Contains(*a.TeamRoots, p) }
	switch c.Op {
	case team.RootsOpAdd:
		if a.Result == team.RootsResultRemoved {
			return a, "an add answered removed"
		}
		if !has(a.Path) {
			return a, "an add whose path is not in the roots it reports"
		}
	case team.RootsOpRemove:
		if a.Result == team.RootsResultAdded {
			return a, "a remove answered added"
		}
		if a.Result == team.RootsResultRemoved && (a.Path == "" || has(a.Path)) {
			return a, "a remove that is still in the roots it reports"
		}
	}
	return a, ""
}
