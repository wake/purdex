// internal/module/team/pause_release.go — POST /api/team/paused/{session_id}/release (unattended quota pause spec 2026-10-12 §3.4, UP-3b).
package teammod

import (
	"net/http"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// handlePausedRelease clears one session's pause: the resume group for that session — a lead takes the members it paused with it
// (their `lead` rows) — with the release text; unattended stays on. The body's client is an audit claim like PUT /api/team/roots's
// (kind "app", a label): told, not enforced. A session that holds no pause row is 404 not_paused (not 409: the resource does not
// exist, and a second click on 「解除」 has nothing left to do).
func (m *Module) handlePausedRelease(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	var req team.PauseReleaseRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Client.Kind) != "app" || strings.TrimSpace(req.Client.Label) == "" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, `client must be {"kind":"app","label":…}`, nil)
		return
	}
	sid := r.PathValue("session_id")
	m.createMu.Lock()
	defer m.createMu.Unlock()
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	held, err := m.store.PausesAmong([]string{sid})
	if err != nil {
		m.logf("[team] release %s: %v", sid, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if _, paused := held[sid]; !paused {
		m.writeErr(w, http.StatusNotFound, team.ErrNotPaused, "this session is not paused", nil)
		return
	}
	n, err := m.resumeOne(sid)
	if err != nil {
		m.logf("[team] release %s: %v", sid, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	m.logf("[team] pause of %s released by app %q from %s (%d session(s) resumed)", sid, strings.TrimSpace(req.Client.Label), r.RemoteAddr, n)
	m.writeJSON(w, http.StatusOK, team.PauseReleaseResponse{Released: n})
}
