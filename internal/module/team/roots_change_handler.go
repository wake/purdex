// internal/module/team/roots_change_handler.go — POST /api/peers/team/roots-change (cross-host roots spec 2026-10-12 §3.1).
package teammod

import (
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/middleware"
	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// maxRootPath bounds the path a roots-change request carries.
const maxRootPath = 4096

// maxRootsDetail is what a JSON refusal's detail keeps on the wire (the transport cuts it at 200 bytes).
const maxRootsDetail = 200

// rootsHTTPError is a refusal found under the config lock: its status and wire code.
type rootsHTTPError struct {
	status       int
	code, detail string
}

func (e *rootsHTTPError) Error() string { return e.code }

// rootsDone carries the answer out of the UpdateConfig closure when the request changed nothing: returning it aborts the
// write (no file write, rev unchanged) and the handler answers it as a success.
type rootsDone struct{ resp team.RootsChangeResponse }

func (e *rootsDone) Error() string { return "roots unchanged" }

// handleRootsChange serves the route. The order is the contract, as for commands: the binding of the host principal, the
// per-host rate limit and the body cap before the body is decoded, then shape, to_host_id; consent; then (remove) the
// best-effort in-use pre-check — outside UpdateConfig, since the closure must touch config only — and one UpdateConfig
// closure that reads and writes the entry's roots.
func (m *Module) handleRootsChange(w http.ResponseWriter, r *http.Request) {
	var entry config.PeerHost
	var ourHostID string
	principal, _, berr := peersmod.BindHostPrincipal(r, "roots change", func(alias string) (config.PeerHost, bool) {
		var ok bool
		entry, ourHostID, ok = m.peerEntry(alias)
		return entry, ok
	})
	if berr != nil {
		m.logf("[team] roots change refused (%s): %s", berr.Code, berr.Detail)
		m.writeCommandErr(w, berr.Status, berr.Code, berr.Detail)
		return
	}
	if m.stopCtx.Err() != nil {
		m.writeCommandErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping")
		return
	}
	var req team.RootsChangeRequest
	switch st := peersmod.AdmitDecode(w, r, m.rootsLimit, entry.HostID, maxCommandBody, &req); st {
	case 0:
	case http.StatusTooManyRequests:
		m.writeCommandErr(w, st, ipeers.ErrRateLimited, "host rate limit exceeded")
		return
	case http.StatusRequestEntityTooLarge:
		m.writeCommandErr(w, st, team.ErrCommandBadRequest, "body over 64 KiB")
		return
	default:
		m.writeCommandErr(w, st, team.ErrCommandBadRequest, "invalid JSON body")
		return
	}
	if msg := validateRootsChange(req); msg != "" {
		m.writeCommandErr(w, http.StatusBadRequest, team.ErrCommandBadRequest, msg)
		return
	}
	if req.ToHostID != ourHostID {
		m.writeCommandErr(w, http.StatusConflict, team.ErrCommandWrongHost, "this request is addressed to another host")
		return
	}
	// Consent is looked at first, from the entry the binding returned; the closure looks again under the lock.
	if !entry.AllowTeam {
		m.writeCommandErr(w, http.StatusForbidden, team.ErrCommandHostNotAllowed, "this host does not accept team requests from the lead host")
		return
	}

	if req.Op == team.RootsOpRead {
		m.core.CfgMu.RLock()
		h, ok := findRootsEntry(m.core.Cfg.Peers.Hosts, principal.Alias, principal.HostID)
		resp := rootsResponse(req, ourHostID, team.RootsResultUnchanged, "", h)
		m.core.CfgMu.RUnlock()
		if !ok || !h.AllowTeam {
			m.writeCommandErr(w, http.StatusForbidden, team.ErrCommandHostNotAllowed, "this host does not accept team requests from the lead host")
			return
		}
		m.writeJSON(w, http.StatusOK, resp)
		return
	}

	var canon string // add: the canonical path, computed before the lock (it touches the file system)
	var candidates []string
	if req.Op == team.RootsOpAdd {
		roots, err := config.CanonicalTeamRoots([]string{req.Path})
		if err != nil {
			m.writeBadRoot(w, err)
			return
		}
		canon = roots[0]
	} else {
		var err error
		if candidates, err = config.RemovalCandidates([]string{req.Path}); err != nil {
			m.writeBadRoot(w, err)
			return
		}
	}

	// A removal judges "kept" from the roots the pre-check read, so the write must apply to that same set: the closure
	// refuses when the revision moved since (another request, an admin), and the attempt starts over with a fresh read and a
	// fresh pre-check. Bounded; past it the caller gets 503 not_ready — a transient class (5xx) for HostCaller, so the lead host keeps the row pending and sends it again, where a JSON 4xx would settle it as refused.
	var resp team.RootsChangeResponse
	var err error
	for attempt := 0; attempt < maxRootsAttempts; attempt++ {
		expectRev := int64(-1) // -1: no revision precondition (add)
		if req.Op == team.RootsOpRemove {
			cur, _, ok := m.peerEntry(principal.Alias)
			if !ok || cur.HostID != principal.HostID || !cur.AllowTeam {
				m.writeCommandErr(w, http.StatusForbidden, team.ErrCommandHostNotAllowed, "this host does not accept team requests from the lead host")
				return
			}
			refs, perr := m.rootsInUse(cur, candidates)
			if perr != nil {
				m.logf("[team] roots change %s: in-use check: %v", req.ID, perr)
				m.writeCommandErr(w, http.StatusInternalServerError, "storage_error", "team.db failed; see the daemon log")
				return
			}
			if len(refs) > 0 {
				m.writeCommandErr(w, http.StatusConflict, team.ErrRootsInUse, boundDetail(strings.Join(refs, ",")))
				return
			}
			expectRev = cur.TeamRootsRev
		}
		err = m.applyRootsChange(req, principal, ourHostID, canon, candidates, expectRev, &resp)
		var stale *rootsStale
		if !errors.As(err, &stale) {
			break
		}
		err = &rootsHTTPError{http.StatusServiceUnavailable, team.ErrNotReady, "the roots changed while the request was checked; retry"}
	}
	var done *rootsDone
	var herr *rootsHTTPError
	switch {
	case errors.As(err, &done):
		m.writeJSON(w, http.StatusOK, done.resp)
	case errors.As(err, &herr):
		m.writeCommandErr(w, herr.status, herr.code, herr.detail)
	case err != nil:
		m.logf("[team] roots change %s: %v", req.ID, err)
		m.writeCommandErr(w, http.StatusInternalServerError, "storage_error", "config could not be written; see the daemon log")
	default:
		m.logf("[team] team roots: %s (%s) %s %s, rev %d", principal.Alias, principal.HostID, resp.Result, resp.Path, resp.TeamRootsRev)
		m.writeJSON(w, http.StatusOK, resp)
	}
}

// maxRootsAttempts bounds how often a removal re-reads and re-checks when the roots move under it.
const maxRootsAttempts = 3

// rootsStale is the closure's refusal when the revision is not the one the pre-check read.
type rootsStale struct{}

func (e *rootsStale) Error() string { return "roots moved" }

// applyRootsChange is the one UpdateConfig closure: it reads and writes the caller's entry in config and nothing else.
// expectRev >= 0 is the revision the in-use pre-check judged; a different one is rootsStale and nothing is written.
func (m *Module) applyRootsChange(req team.RootsChangeRequest, principal middleware.Principal, ourHostID, canon string, candidates []string, expectRev int64, resp *team.RootsChangeResponse) error {
	return m.core.UpdateConfig(func(cfg *config.Config) error {
		h, ok := findRootsEntryPtr(cfg.Peers.Hosts, principal.Alias, principal.HostID)
		if !ok || !h.AllowTeam {
			return &rootsHTTPError{http.StatusForbidden, team.ErrCommandHostNotAllowed, "this host does not accept team requests from the lead host"}
		}
		if expectRev >= 0 && h.TeamRootsRev != expectRev {
			return &rootsStale{}
		}
		var (
			next   []string
			result = team.RootsResultUnchanged
			path   string
			err    error
		)
		if req.Op == team.RootsOpRemove {
			next, err = config.ApplyRootsChange(h.TeamRoots, nil, nil, candidates, nil)
			for _, have := range h.TeamRoots {
				if slices.Contains(candidates, have) {
					path = have
					break
				}
			}
		} else {
			next, err = config.ApplyRootsChange(h.TeamRoots, nil, nil, nil, []string{canon})
			path = canon
		}
		if err != nil {
			return &rootsHTTPError{http.StatusBadRequest, team.ErrRootsBadRoot, rootReason(err)}
		}
		if slices.Equal(h.TeamRoots, next) {
			return &rootsDone{rootsResponse(req, ourHostID, team.RootsResultUnchanged, path, *h)}
		}
		if req.Op == team.RootsOpRemove {
			result = team.RootsResultRemoved
		} else {
			result = team.RootsResultAdded
		}
		h.TeamRoots = next
		h.TeamRootsRev++
		*resp = rootsResponse(req, ourHostID, result, path, *h)
		return nil
	})
}

// validateRootsChange checks the shape: a UUID v4 id, a known op, and a path that is absolute and clean for add / remove
// and empty for read. "" means valid.
func validateRootsChange(req team.RootsChangeRequest) string {
	if !uuidV4.MatchString(req.ID) {
		return "id must be a UUID v4"
	}
	for _, s := range []string{req.ToHostID, req.Op, req.Path} {
		if len(s) > maxRootPath || !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0 {
			return "a field is too long, not UTF-8, or holds a control character"
		}
	}
	switch req.Op {
	case team.RootsOpRead:
		if req.Path != "" {
			return "read takes no path"
		}
	case team.RootsOpAdd, team.RootsOpRemove:
		if req.Path == "" || !filepath.IsAbs(req.Path) || filepath.Clean(req.Path) != req.Path {
			return req.Op + " needs an absolute, clean path"
		}
	default:
		return "op must be add, remove or read"
	}
	return ""
}

func findRootsEntry(hosts []config.PeerHost, alias, hostID string) (config.PeerHost, bool) {
	for _, h := range hosts {
		if h.Alias == alias && h.HostID == hostID && hostID != "" {
			return h, true
		}
	}
	return config.PeerHost{}, false
}

func findRootsEntryPtr(hosts []config.PeerHost, alias, hostID string) (*config.PeerHost, bool) {
	for i := range hosts {
		if hosts[i].Alias == alias && hosts[i].HostID == hostID && hostID != "" {
			return &hosts[i], true
		}
	}
	return nil, false
}

func rootsResponse(req team.RootsChangeRequest, hostID, result, path string, h config.PeerHost) team.RootsChangeResponse {
	return team.RootsChangeResponse{ID: req.ID, HostID: hostID, Op: req.Op, Result: result, Path: path,
		TeamRoots: append([]string{}, h.TeamRoots...), TeamRootsRev: h.TeamRootsRev}
}

func rootReason(err error) string {
	var re *config.RootError
	if errors.As(err, &re) {
		return re.Reason
	}
	return err.Error()
}

func (m *Module) writeBadRoot(w http.ResponseWriter, err error) {
	m.writeCommandErr(w, http.StatusBadRequest, team.ErrRootsBadRoot, rootReason(err))
}

// boundDetail cuts s to the 200 bytes a JSON refusal's detail keeps on the wire, at a comma boundary when it can.
func boundDetail(s string) string {
	if len(s) <= maxRootsDetail {
		return s
	}
	cut := s[:maxRootsDetail]
	if i := strings.LastIndex(cut, ","); i > 0 {
		cut = cut[:i]
	}
	return cut
}

// rootsInUse is the best-effort pre-check of a removal (never inside UpdateConfig: it reads team.db). The roots the
// candidates match are the ones being revoked; a member of THIS lead host — an active remote member, or a running forwarded
// spawn — whose real cwd is within one of them and within none of the roots the entry keeps is in use. refs are the member
// refs and spawn op ids. It is not atomic with the write: the lead host's own check is the authoritative one, and a spawn
// accepted in the gap is still stopped at create / launch by underOpRoots.
func (m *Module) rootsInUse(entry config.PeerHost, candidates []string) (refs []string, err error) {
	var removed, kept []string
	for _, have := range entry.TeamRoots {
		if slices.Contains(candidates, have) {
			removed = append(removed, have)
		} else {
			kept = append(kept, have)
		}
	}
	if len(removed) == 0 {
		return nil, nil
	}
	if m.rootsPrecheckHook != nil {
		m.rootsPrecheckHook()
	}
	uses, err := m.store.LeadHostCwds(entry.HostID)
	if err != nil {
		return nil, err
	}
	for _, u := range uses {
		real := u.Cwd
		if r, err := filepath.EvalSymlinks(u.Cwd); err == nil {
			real = r
		}
		if !slices.ContainsFunc(removed, func(root string) bool { return within(real, root) }) {
			continue
		}
		if slices.ContainsFunc(kept, func(root string) bool { return within(real, root) }) {
			continue
		}
		refs = append(refs, u.Ref)
	}
	return refs, nil
}
