// internal/module/team/proxy_handler.go
package teammod

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/wake/purdex/internal/config"
	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// The proxy adapter on the lead host (cross-host team spec §7, plan X6-1): POST /api/peers/team/proxy.
//
// A remote member's report and task calls arrive from its member host. They are NOT handed to the existing handlers: those
// resolve the caller from an origin_inbox in the request, and a paired host could put the lead's there. The adapter
//
//   - binds the host principal first, as the commands and facts routes do (§6.1), with its rate limit and body cap;
//   - refuses (400) any origin_inbox in the forwarded body, whatever its spelling, or in the query;
//   - builds the actor ONLY from {host_id == the principal's, mk}: the member's row, active, in a live team, found by those
//     two keys and by nothing the request names (a session id, a ref, an address);
//   - runs only an allow-list of owner-scoped operations — list my tasks, change the status of a task I own, post a report
//     as myself — through the same caller-taking cores the member routes use, which re-check the member's right inside
//     their own transactions (liveMemberIn) and never touch a lead operation;
//   - answers the call's status and body unchanged inside the envelope.

// proxyTaskStatusPath is the one parameterised route of the allow-list.
var proxyTaskStatusPath = regexp.MustCompile(`^/api/team/tasks/([^/?#]+)/status$`)

// maxProxyField bounds the mk.
const maxProxyField = 256

// captureWriter records what a core writes, so the adapter can wrap it.
type captureWriter struct {
	h      http.Header
	status int
	body   []byte
}

func newCapture() *captureWriter { return &captureWriter{h: http.Header{}} }

func (c *captureWriter) Header() http.Header { return c.h }
func (c *captureWriter) WriteHeader(s int) {
	if c.status == 0 {
		c.status = s
	}
}
func (c *captureWriter) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	c.body = append(c.body, b...)
	return len(b), nil
}

// ActiveRemoteMember is the active member row of the lead host's team that the member host hostID holds under the member
// key mk, with its team, when that team is live. Found by those two keys only.
func (s *Store) ActiveRemoteMember(hostID, mk string) (memberRow, team.Team, bool, error) {
	var mr memberRow
	var t team.Team
	var grantJSON string
	err := s.db.QueryRow(`SELECT `+qualify("m", memberCols)+`, `+qualify("t", teamCols)+`
		FROM team_members m JOIN teams t ON t.id = m.team_id
		WHERE m.host_id = ? AND m.mk = ? AND m.state = 'active' AND t.ended_at = 0`, hostID, mk).
		Scan(append(mr.dest(), teamDest(&t, &grantJSON)...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return memberRow{}, team.Team{}, false, nil
	}
	if err == nil {
		err = decodeTeamGrant(&t, grantJSON)
	}
	if err != nil {
		return memberRow{}, team.Team{}, false, fmt.Errorf("active remote member %s/%s: %w", hostID, mk, err)
	}
	return mr, t, true, nil
}

func (m *Module) writeProxyErr(w http.ResponseWriter, status int, code, detail string) {
	m.writeJSON(w, status, team.CommandRefusal{Error: code, Detail: detail})
}

// handleTeamProxy serves POST /api/peers/team/proxy.
func (m *Module) handleTeamProxy(w http.ResponseWriter, r *http.Request) {
	var entry config.PeerHost
	var ourHostID string
	principal, _, berr := peersmod.BindHostPrincipal(r, "team proxy", func(alias string) (config.PeerHost, bool) {
		var ok bool
		entry, ourHostID, ok = m.peerEntry(alias)
		return entry, ok
	})
	if berr != nil {
		m.logf("[team] proxy refused (%s): %s", berr.Code, berr.Detail)
		m.writeProxyErr(w, berr.Status, berr.Code, berr.Detail)
		return
	}
	if m.stopCtx.Err() != nil {
		m.writeProxyErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping")
		return
	}
	var req team.ProxyRequest
	switch st := peersmod.AdmitDecode(w, r, m.proxyLimit, entry.HostID, maxCommandBody, &req); st {
	case 0:
	case http.StatusTooManyRequests:
		m.writeProxyErr(w, st, ipeers.ErrRateLimited, "host rate limit exceeded")
		return
	case http.StatusRequestEntityTooLarge:
		m.writeProxyErr(w, st, team.ErrProxyBadRequest, "body over 64 KiB")
		return
	default:
		m.writeProxyErr(w, st, team.ErrProxyBadRequest, "invalid JSON body")
		return
	}
	if req.ToHostID != ourHostID {
		m.writeProxyErr(w, http.StatusConflict, team.ErrCommandWrongHost, "this request is addressed to another host")
		return
	}
	if req.MK == "" || len(req.MK) > maxProxyField || strings.IndexFunc(req.MK, func(r rune) bool { return !unicode.IsGraphic(r) }) >= 0 {
		m.writeProxyErr(w, http.StatusBadRequest, team.ErrProxyBadRequest, "mk is required (at most 256 bytes, printable characters only)")
		return
	}
	u, err := url.ParseRequestURI(req.Path)
	if err != nil || u.Fragment != "" || u.Host != "" || u.Scheme != "" {
		m.writeProxyErr(w, http.StatusBadRequest, team.ErrProxyBadRequest, "path must be an absolute path with an optional query")
		return
	}
	q := u.Query()
	for k := range q {
		if strings.Contains(strings.ToLower(k), "origin") {
			m.writeProxyErr(w, http.StatusBadRequest, team.ErrProxyOriginInbox, "the forwarded query must not name a caller")
			return
		}
	}
	if strings.Contains(strings.ToLower(req.Path), "origin_inbox") {
		m.writeProxyErr(w, http.StatusBadRequest, team.ErrProxyOriginInbox, "the forwarded path must not name a caller")
		return
	}
	if len(req.Body) > 0 {
		var keys map[string]json.RawMessage
		if json.Unmarshal(req.Body, &keys) == nil {
			for k := range keys {
				if strings.Contains(strings.ToLower(k), "origin") { // Go's decoder matches field names case-insensitively
					m.writeProxyErr(w, http.StatusBadRequest, team.ErrProxyOriginInbox, "the forwarded body must not name a caller")
					return
				}
			}
		}
	}
	// the route first, so a refused call never reads the member
	run, ok := m.proxyRoute(req, u.Path, q)
	if !ok {
		m.writeProxyErr(w, http.StatusForbidden, team.ErrProxyForbidden, "this method and path are not served through the proxy")
		return
	}
	mr, t, found, err := m.store.ActiveRemoteMember(entry.HostID, req.MK)
	if err != nil {
		m.logf("[team] proxy %s %s: %v", entry.HostID, req.MK, err)
		m.writeProxyErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log")
		return
	}
	// fresh binding: the alias may have been re-created for another host while the member was read
	if fresh, _, ok := m.peerEntry(principal.Alias); !ok || fresh.HostID != entry.HostID {
		m.writeProxyErr(w, http.StatusForbidden, ipeers.ErrHostUnverified, "host entry no longer matches the authenticated host")
		return
	}
	cw := newCapture()
	if !found {
		m.notMember(cw)
	} else {
		run(cw, taskCaller{team: t, member: &mr})
	}
	body := cw.body
	if len(body) == 0 {
		body = []byte("null")
	}
	status := cw.status
	if status == 0 {
		status = http.StatusOK
	}
	m.writeJSON(w, http.StatusOK, team.ProxyAnswer{HostID: ourHostID, Status: status, Body: body})
}

// proxyRoute is the allow-list: the call as a function of the actor, or false for anything else. The query (and the body)
// carry no caller by now; each core reads only what it needs from them.
func (m *Module) proxyRoute(req team.ProxyRequest, path string, q url.Values) (func(http.ResponseWriter, taskCaller), bool) {
	switch {
	case req.Method == http.MethodPost && path == "/api/team/reports":
		var body team.CreateReportRequest
		if json.Unmarshal(req.Body, &body) != nil || body.OriginInbox != "" {
			return func(w http.ResponseWriter, _ taskCaller) {
				m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "invalid report request", nil)
			}, true
		}
		return func(w http.ResponseWriter, c taskCaller) { m.createReportAs(w, c, body) }, true
	case req.Method == http.MethodGet && path == "/api/team/tasks":
		return func(w http.ResponseWriter, c taskCaller) { m.listTasksAs(w, c, q) }, true
	case req.Method == http.MethodPost && proxyTaskStatusPath.MatchString(path):
		id := proxyTaskStatusPath.FindStringSubmatch(path)[1]
		var body team.TaskStatusRequest
		if json.Unmarshal(req.Body, &body) != nil || body.OriginInbox != "" {
			return func(w http.ResponseWriter, _ taskCaller) {
				m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "invalid status request", nil)
			}, true
		}
		return func(w http.ResponseWriter, c taskCaller) { m.setTaskStatusAs(w, c, id, body) }, true
	}
	return nil, false
}
