package teammod

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// bodyCap bounds request bodies; a request is never larger than a few KB.
const bodyCap = 1 << 20

// errStorage is the 500 body's error code: a team.db failure, outside the
// wire contract's codes (clients treat any unlisted code as a plain error).
const errStorage = "storage_error"

func (m *Module) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		m.logf("[team] encode response: %v", err)
	}
}

func (m *Module) writeErr(w http.ResponseWriter, status int, code, detail string, a *team.Approval) {
	m.writeJSON(w, status, team.APIError{Error: code, Detail: detail, Approval: a})
}

// decodeBody decodes a JSON body into v; false means a 400 was written.
func (m *Module) decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, bodyCap+1))
	if err != nil || len(body) > bodyCap {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "body unreadable or over 1 MiB", nil)
		return false
	}
	if err := json.Unmarshal(body, v); err != nil {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "invalid JSON: "+err.Error(), nil)
		return false
	}
	return true
}

// normaliseRoots cleans and absolutises roots against the origin's cwd;
// empty means [cwd]. An error names a root that cannot be made absolute.
func normaliseRoots(roots []string, cwd string) ([]string, error) {
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if !filepath.IsAbs(r) {
			r = filepath.Join(cwd, r)
		}
		if !filepath.IsAbs(r) {
			return nil, fmt.Errorf("root %q is not absolute and the origin has no cwd", r)
		}
		out = append(out, filepath.Clean(r))
	}
	if len(out) == 0 {
		if !filepath.IsAbs(cwd) {
			return nil, errors.New("roots are required: the origin has no cwd")
		}
		out = append(out, filepath.Clean(cwd))
	}
	return out, nil
}

// normaliseMaxMembers applies 0→3, cap 8 (spec §6.1); def is the fallback for 0.
func normaliseMaxMembers(n, def int) int {
	if n <= 0 {
		return def
	}
	if n > team.MaxMaxMembers {
		return team.MaxMaxMembers
	}
	return n
}

// requestHash is the idempotency key's fingerprint: the fields that make
// two requests "the same request" (PD, handoff notes).
func requestHash(kind team.Kind, sessionID string, waitS int, payload []byte) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%d\x00", kind, sessionID, waitS)
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}

// handleCreate is POST /api/team/approvals.
func (m *Module) handleCreate(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	var req team.CreateApprovalRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if req.ID == "" || len(req.ID) > 128 {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "id is required (UUID v4) and at most 128 bytes", nil)
		return
	}
	switch req.Kind {
	case team.KindLead:
	case team.KindSelfRelay:
		m.writeErr(w, http.StatusBadRequest, team.ErrUnsupportedKind, "kind self_relay is not supported by this daemon yet", nil)
		return
	default:
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "kind must be lead", nil)
		return
	}
	if req.WaitS < 0 || req.MaxMembers < 0 {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "wait_s and max_members must not be negative", nil)
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "reason is required", nil)
		return
	}
	origin, ok := m.origins.ResolveOrigin(req.OriginInbox)
	if !ok {
		m.writeErr(w, http.StatusBadRequest, team.ErrOriginUnknown, "origin_inbox is not a live Claude Code session on this host", nil)
		return
	}
	roots, err := normaliseRoots(req.Roots, origin.Cwd)
	if err != nil {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, err.Error(), nil)
		return
	}
	waitS := req.WaitS
	if waitS == 0 {
		waitS = team.DefaultWaitS
	}
	if waitS > team.MaxWaitS {
		waitS = team.MaxWaitS
	}
	payload, err := json.Marshal(team.LeadPayload{Reason: reason, MaxMembers: normaliseMaxMembers(req.MaxMembers, team.DefaultMaxMembers), Roots: roots})
	if err != nil {
		m.writeErr(w, http.StatusInternalServerError, errStorage, "encode payload: "+err.Error(), nil)
		return
	}
	hash := requestHash(req.Kind, origin.SessionID, waitS, payload)

	m.createMu.Lock()
	defer m.createMu.Unlock()
	if open, found, err := m.store.OpenByOrigin(origin.SessionID, req.Kind); err != nil {
		m.logf("[team] create %s: %v", req.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	} else if found && open.ID != req.ID {
		m.writeErr(w, http.StatusConflict, team.ErrRequestOpen, "this session already has an open lead request", &open)
		return
	}
	now := m.now()
	stored, storedHash, inserted, err := m.store.Create(team.Approval{
		ID: req.ID, Kind: req.Kind, HostID: m.hostID(), Origin: origin, Payload: payload, State: team.StateOpen,
		CreatedAt: now, DeadlineAt: now + int64(waitS)*1000, LeaseUntil: now + team.LeaseS*1000,
	}, hash)
	if err != nil {
		m.logf("[team] create %s: %v", req.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if !inserted {
		if storedHash != hash {
			m.writeErr(w, http.StatusConflict, team.ErrIDConflict, "id already used by a different request", nil)
			return
		}
		m.writeJSON(w, http.StatusOK, stored)
		return
	}
	m.logf("[team] approval %s opened: kind=%s origin=%s (%s) reason=%q", stored.ID, stored.Kind, origin.Ref, origin.SessionID, reason)
	m.broadcast("opened", &stored)
	m.writeJSON(w, http.StatusCreated, stored)
}

// handleList is GET /api/team/approvals?state=open.
func (m *Module) handleList(w http.ResponseWriter, r *http.Request) {
	if s := r.URL.Query().Get("state"); s != "" && s != string(team.StateOpen) {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "only state=open is supported", nil)
		return
	}
	open, err := m.store.ListOpen()
	if err != nil {
		m.logf("[team] list: %v", err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	m.writeJSON(w, http.StatusOK, map[string]any{"approvals": open})
}
