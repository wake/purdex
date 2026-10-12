// internal/module/team/host_roots_handler.go — the lead host's routes for the directories it may use on other hosts (cross-host roots spec
// 2026-10-12 §3.2, XR-2b): POST /api/team/host-roots, GET /api/team/host-roots, POST /api/team/host-roots/dismiss. The token is the
// ordinary one; the App's claim is told, not enforced (as PUT /api/team/roots).
package teammod

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/wake/purdex/internal/config"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
)

// hostRootsReadBound is how long GET waits for the member hosts' answers, all together.
const hostRootsReadBound = 2 * time.Second

// pairedHostRef is a paired, verified host as the routes name it.
type pairedHostRef struct{ ID, Alias string }

// pairedHosts are the configured hosts that carry a verified host id and are paired, in config order.
func (m *Module) pairedHosts() []pairedHostRef {
	if m.cmdCaller == nil {
		return nil
	}
	m.core.CfgMu.RLock()
	hosts := append([]config.PeerHost(nil), m.core.Cfg.Peers.Hosts...)
	m.core.CfgMu.RUnlock()
	var out []pairedHostRef
	for _, h := range hosts {
		if h.HostID != "" && m.cmdCaller.Paired(h.HostID) {
			out = append(out, pairedHostRef{ID: h.HostID, Alias: h.Alias})
		}
	}
	return out
}

// resolvePairedHost is the name the App sent: an alias (case-insensitive) first, then a host id — as remote spawn and adopt resolve
// it — and the entry must be verified (carry a host id) and paired.
func (m *Module) resolvePairedHost(name string) (pairedHostRef, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return pairedHostRef{}, false
	}
	hosts := m.pairedHosts()
	for _, h := range hosts {
		if strings.EqualFold(h.Alias, name) {
			return h, true
		}
	}
	for _, h := range hosts {
		if h.ID == name {
			return h, true
		}
	}
	return pairedHostRef{}, false
}

func changeView(c HostRootChange) team.HostRootChange {
	return team.HostRootChange{ID: c.ID, HostID: c.HostID, Op: c.Op, Path: c.Path, State: c.State, Code: c.Code, Detail: c.Detail,
		Canonical: c.Canonical, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

// handleHostRootsPost records one change for a member host: 202 with the pending row. A remove of a path the host is not known to
// have is 400 not_a_root; one that members of this lead still run under is 409 roots_in_use with the uses (nothing is written).
func (m *Module) handleHostRootsPost(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	var req team.HostRootsPostRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	switch {
	case strings.TrimSpace(req.Client.Kind) != "app" || strings.TrimSpace(req.Client.Label) == "":
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, `client must be {"kind":"app","label":…}`, nil)
		return
	case req.Op != team.RootsOpAdd && req.Op != team.RootsOpRemove:
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "op must be add or remove", nil)
		return
	}
	host, ok := m.resolvePairedHost(req.Host)
	if !ok {
		m.writeErr(w, http.StatusBadRequest, team.ErrUnknownHost, "no paired host answers to "+boundText(req.Host), nil)
		return
	}
	switch {
	case !filepath.IsAbs(req.Path):
		m.writeErr(w, http.StatusBadRequest, team.ErrRootsBadRoot, "not_absolute", nil)
		return
	case filepath.Clean(req.Path) != req.Path || len(req.Path) > 4096 || strings.ContainsRune(req.Path, 0):
		m.writeErr(w, http.StatusBadRequest, team.ErrRootsBadRoot, "the path must be absolute and clean", nil)
		return
	}
	c := HostRootChange{ID: uuid.NewString(), HostID: host.ID, Op: req.Op, Path: req.Path, CreatedAt: m.now()}
	err := m.store.AddHostRootChange(c)
	var inUse *RootsInUseError
	switch {
	case errors.As(err, &inUse):
		m.writeJSON(w, http.StatusConflict, team.HostRootsInUse{Error: team.ErrRootsInUse,
			Detail: "這個資料夾還有 member 在用，先結束它們才能移除", Uses: inUse.Uses})
		return
	case errors.Is(err, ErrNotARoot):
		m.writeErr(w, http.StatusBadRequest, team.ErrNotARoot, "that path is not a root of "+host.Alias, nil)
		return
	case err != nil:
		m.logf("[team] host roots: add %s %s on %s: %v", req.Op, req.Path, host.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	m.logf("[team] host roots: %s %q on %s (%s) recorded by app %q from %s", req.Op, req.Path, host.Alias, host.ID, strings.TrimSpace(req.Client.Label), r.RemoteAddr)
	m.kickRoots()
	row, _, _ := m.store.RootChange(c.ID)
	m.writeJSON(w, http.StatusAccepted, changeView(row))
}

// handleHostRootsDismiss deletes a refused or dropped change; a pending one is 409 pending, an unknown one 404. No claim needed.
func (m *Module) handleHostRootsDismiss(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	var req team.HostRootsDismissRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "id is required", nil)
		return
	}
	switch err := m.store.DismissRootChange(req.ID); {
	case errors.Is(err, ErrNoSuchRootChange):
		m.writeErr(w, http.StatusNotFound, team.ErrNoSuchChange, "no such change", nil)
	case errors.Is(err, ErrRootChangePending):
		m.writeErr(w, http.StatusConflict, team.ErrChangePending, "the change is still pending; to undo it, send the inverse op", nil)
	case err != nil:
		m.logf("[team] host roots: dismiss %s: %v", req.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
	default:
		m.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// handleHostRootsGet answers for every paired host what it last said its roots are and what is still owed. Before it answers it asks
// every host to `read`, in parallel, for 2 s in all; a host that does not answer in time keeps its cache and last status.
func (m *Module) handleHostRootsGet(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	hosts := m.pairedHosts()
	m.readHostsRoots(r.Context(), hosts)
	out := team.HostRootsView{Hosts: make([]team.HostRootsHost, 0, len(hosts))}
	for _, h := range hosts {
		v := team.HostRootsHost{HostID: h.ID, Alias: h.Alias, Status: m.RootsHostStatus(h.ID), Roots: []string{}, Changes: []team.HostRootChange{}}
		seen, ok, err := m.store.HostRootsSeen(h.ID)
		if err != nil {
			m.logf("[team] host roots: cache of %s: %v", h.ID, err)
		} else if ok {
			v.Roots, v.Rev, v.SeenAt = seen.Roots, seen.Rev, seen.SeenAt
			if v.Roots == nil {
				v.Roots = []string{}
			}
		}
		rows, err := m.store.RootChangesToShow(h.ID)
		if err != nil {
			m.logf("[team] host roots: changes of %s: %v", h.ID, err)
		}
		for _, c := range rows {
			v.Changes = append(v.Changes, changeView(c))
		}
		out.Hosts = append(out.Hosts, v)
	}
	m.writeJSON(w, http.StatusOK, out)
}

// readHostsRoots sends one read to every host at once and waits for them all, but not beyond hostRootsReadBound. A read that comes
// back updates the cache and the status; one that is still out when the bound passes is abandoned (its answer is dropped).
func (m *Module) readHostsRoots(parent context.Context, hosts []pairedHostRef) {
	if len(hosts) == 0 || m.cmdCaller == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, hostRootsReadBound)
	defer cancel()
	var wg sync.WaitGroup
	for _, h := range hosts {
		wg.Add(1)
		go func(h pairedHostRef) {
			defer wg.Done()
			m.readHostRoots(ctx, h.ID)
		}(h)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// callRead is one `read` of the host: its status class and, for a whole answer, the roots and revision it reported.
func (m *Module) callRead(ctx context.Context, hostID string) (status string, ans rootsAnswer, ok bool) {
	id := uuid.NewString()
	res := m.cmdCaller.Call(ctx, hostID, team.RootsChangeRoute, team.RootsChangeRequest{ID: id, ToHostID: hostID, Op: team.RootsOpRead})
	if ctx.Err() != nil {
		return "", rootsAnswer{}, false // out of time: the answer, whatever it was, is not used
	}
	status = statusOfCall(res)
	if res.Class != peersmod.ClassDone {
		return status, rootsAnswer{}, false
	}
	a, why := completeRootsAnswer(res.Body, HostRootChange{ID: id, HostID: hostID, Op: team.RootsOpRead})
	if why != "" {
		m.logf("[team] host roots %s: the answer to a read is not usable (%s)", hostID, why)
		return RootsStatusOffline, rootsAnswer{}, false
	}
	return status, a, true
}

// readHostRoots reads one host and applies the answer to the cache. A rev lower than the cache's is read again at once: a late, older
// answer must not move the cache back, but two lower ones in a row mean the member host's relation with this host was rebuilt (its
// rev started over), and then the read's answer replaces the cache. Only this read path may do so; a settle stays monotonic.
func (m *Module) readHostRoots(ctx context.Context, hostID string) {
	status, a, ok := m.callRead(ctx, hostID)
	if status != "" {
		m.setRootsStatus(hostID, status)
	}
	if !ok {
		return
	}
	cached, have, err := m.store.HostRootsSeen(hostID)
	if err != nil {
		m.logf("[team] host roots: cache of %s: %v", hostID, err)
		return
	}
	now := m.now()
	if have && *a.TeamRootsRev < cached.Rev {
		st2, a2, ok2 := m.callRead(ctx, hostID)
		if st2 != "" {
			m.setRootsStatus(hostID, st2) // the confirming read is the latest word on the host
		}
		switch {
		case !ok2:
			return // could not confirm: keep the cache
		case *a2.TeamRootsRev < cached.Rev:
			// only while the cache is still the one judged by: a settle or an unpair that moved it meanwhile is not undone
			if _, err := m.store.OverwriteHostRootsSeenIf(hostID, cached, *a2.TeamRoots, *a2.TeamRootsRev, now); err != nil {
				m.logf("[team] host roots: %v", err)
			}
			return
		}
		a = a2 // the second answer is not lower: it is the fresher one, applied the ordinary way
	}
	if err := m.store.UpsertHostRootsSeen(hostID, *a.TeamRoots, *a.TeamRootsRev, now); err != nil {
		m.logf("[team] host roots: %v", err)
	}
}

// statusOfCall is the status class a call's result names.
func statusOfCall(res peersmod.CallResult) string {
	switch res.Class {
	case peersmod.ClassDone:
		return RootsStatusOK
	case peersmod.ClassRefused, peersmod.ClassWrongHost:
		if res.Code == errHostNotAllowed {
			return RootsStatusConsentOff
		}
		return RootsStatusOK // it answered: reachable and authorised, it only refused this request
	case peersmod.ClassUnauthorized:
		return RootsStatusUnauthorized
	case peersmod.ClassUnsupported:
		return RootsStatusUnsupported
	}
	return RootsStatusOffline
}
