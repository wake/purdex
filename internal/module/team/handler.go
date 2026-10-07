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
	"strconv"
	"strings"

	"github.com/google/uuid"

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
	// id is the CLI-generated UUID v4 (spec §6.1). Any form uuid.Parse
	// accepts is fine — hex case, braces or a urn: prefix — as long as it is
	// version 4 of the RFC 4122 variant; it is stored canonical (lowercase,
	// hyphenated), so a retry in another spelling is the same request.
	u, err := uuid.Parse(req.ID)
	if err != nil || u.Version() != 4 || u.Variant() != uuid.RFC4122 {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "id must be a UUID v4", nil)
		return
	}
	req.ID = u.String()
	switch req.Kind {
	case team.KindLead:
	case team.KindSelfRelay:
		// A self relay opens an op with its row; that is POST /api/relay/begin (P5a).
		m.writeErr(w, http.StatusBadRequest, team.ErrUnsupportedKind, "kind self_relay opens through POST /api/relay/begin", nil)
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
	origin, ok, err := m.origins.ResolveOrigin(req.OriginInbox)
	if err != nil {
		// The registry could not be read (already logged by the resolver):
		// not an unknown origin. 503 not_ready is what the restart-aware
		// CLI retries; origin_unknown it would give up on.
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "registry unavailable; retry", nil)
		return
	}
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

	// Everything from here to the insert is one critical section: Stop
	// cancels stopCtx under the same lock, so a create that passed the
	// entry check before Stop ran still sees stopping here and writes
	// nothing (no row, no event).
	m.createMu.Lock()
	defer m.createMu.Unlock()
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	// Idempotency on id comes first (spec §6.2 "Create (idempotent on id)"):
	// a retry answers for the row it created, whatever state that row is in
	// now and whatever else the origin has opened since. The request_open
	// rule applies to new ids only.
	existing, storedHash, err := m.store.getRow(req.ID)
	switch {
	case err == nil:
		if storedHash != hash {
			m.writeErr(w, http.StatusConflict, team.ErrIDConflict, "id already used by a different request", nil)
			return
		}
		m.writeJSON(w, http.StatusOK, existing)
		return
	case !errors.Is(err, ErrNoSuchApproval):
		m.logf("[team] create %s: %v", req.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if open, found, err := m.store.OpenByOrigin(origin.SessionID, req.Kind); err != nil {
		m.logf("[team] create %s: %v", req.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	} else if found {
		m.writeErr(w, http.StatusConflict, team.ErrRequestOpen, "this session already has an open lead request", &open)
		return
	}
	// One live team per lead (spec §6.2); an ended team does not count. The
	// approve inserts the team in the transaction that closes its row, so a
	// request that is no longer open has its team already.
	if t, found, err := m.store.LiveTeamByLead(origin.SessionID); err != nil {
		m.logf("[team] create %s: %v", req.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	} else if found {
		m.writeErr(w, http.StatusConflict, team.ErrAlreadyLead, "this session already leads team "+t.ID, nil)
		return
	}
	// No nested teams in v1 (spec §6.2): an active member of a live team
	// cannot lead; a member of an ended team is an ordinary session (D4).
	if _, t, found, err := m.store.ActiveMemberInLiveTeam(origin.SessionID); err != nil {
		m.logf("[team] create %s: %v", req.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	} else if found {
		m.writeErr(w, http.StatusConflict, team.ErrMemberCannotLead, "this session is a member of team "+t.ID+"; a member cannot lead", nil)
		return
	}
	now := m.now()
	stored, _, inserted, err := m.store.Create(team.Approval{
		ID: req.ID, Kind: req.Kind, HostID: m.hostID(), Origin: origin, Payload: payload, State: team.StateOpen,
		CreatedAt: now, DeadlineAt: now + int64(waitS)*1000, LeaseUntil: now + team.LeaseS*1000,
	}, hash)
	if err != nil {
		m.logf("[team] create %s: %v", req.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if !inserted {
		// Unreachable while create is the only inserter and runs under
		// createMu after the getRow above; kept so a second writer could
		// never make this path broadcast or 201 for a row it did not open.
		m.logf("[team] create %s: id appeared between check and insert", req.ID)
		m.writeErr(w, http.StatusConflict, team.ErrIDConflict, "id already used by a different request", nil)
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

// handleInflight is GET /api/team/inflight (spec §9.5): what a restart of
// this daemon would interrupt, for the App's restart confirm. Open requests
// survive a restart (boot lease grace), so the count informs, it does not
// block. relays_active counts ops not in done/failed/cancelled (P5a). Hook
// rows (P8a) are not counted: their pollers ride out a restart.
func (m *Module) handleInflight(w http.ResponseWriter, r *http.Request) {
	open, err := m.store.ListOpenNonHook()
	if err != nil {
		m.logf("[team] inflight: %v", err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	active, err := m.store.ListActiveRelayOps()
	if err != nil {
		m.logf("[team] inflight: %v", err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	m.writeJSON(w, http.StatusOK, team.InflightResponse{ApprovalsOpen: len(open), RelaysActive: len(active)})
}

// pollWait parses GET's ?wait= (seconds): "" is 0, a negative or non-numeric
// value is an error, and anything above MaxPollWaitS is capped to it, so a
// poll always returns well inside the 30 s lease the CLI renews with it.
func pollWait(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, errors.New("wait must be a non-negative number of seconds")
	}
	return min(n, team.MaxPollWaitS), nil
}

// handleGet is GET /api/team/approvals/{id}?wait=N (and P5a-2a's GET
// /api/relay/wait/{id}): the long-poll of pollRow (shared with GET
// /api/ask/wait/{id}), answering the row as it is. A poll cut by Stop
// therefore answers 200 with the row still open; the CLI re-polls and
// meets the restart. A poll whose renewal failed answers 503 not_ready
// instead of a 200 that would let the CLI believe the lease holds while
// the sweeper abandons it; the restart-aware CLI retries a 503.
func (m *Module) handleGet(w http.ResponseWriter, r *http.Request) {
	a, ok := m.pollRow(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	m.writeJSON(w, http.StatusOK, a)
}

// handleDelete is DELETE /api/team/approvals/{id}: the requester gives up.
// It answers the row as it now is — cancelled, or closed before as it was.
func (m *Module) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	after, won, err := m.closeAs(id, Close{State: team.StateCancelled, DecidedAt: m.now()})
	if errors.Is(err, ErrNoSuchApproval) {
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no such approval request", nil)
		return
	}
	if err != nil {
		m.logf("[team] delete %s: %v", id, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if won {
		m.logf("[team] approval %s cancelled by its requester", id)
	}
	m.writeJSON(w, http.StatusOK, after)
}

// handleDecide is POST /api/team/approvals/{id}/decide (spec §6.5): one
// click on any App; the client label and remote address are audit, and
// every decision is one daemon log line. A late decide answers 409
// already_decided carrying the closed row, so its client can say who
// handled it.
func (m *Module) handleDecide(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req team.DecideRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	var state team.State
	switch req.Decision {
	case "approve":
		state = team.StateApproved
	case "deny":
		state = team.StateDenied
	default:
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, `decision must be "approve" or "deny"`, nil)
		return
	}
	if strings.TrimSpace(req.Client.Kind) == "" || strings.TrimSpace(req.Client.Label) == "" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "client.kind and client.label are required", nil)
		return
	}
	client := req.Client
	client.Addr = r.RemoteAddr
	a, ok, err := m.store.Get(id)
	if err != nil {
		m.logf("[team] decide %s: %v", id, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if !ok {
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no such approval request", nil)
		return
	}
	if a.State != team.StateOpen {
		m.writeErr(w, http.StatusConflict, team.ErrAlreadyDecided, "this request is already closed", &a)
		return
	}
	if team.IsHookKind(a.Kind) {
		m.decideHook(w, a, req, state, client)
		return
	}
	now := m.now()
	var grant *team.Grant
	// A self_relay approval carries no grant (its payload is a
	// SelfRelayPayload); its op moves in afterClose.
	if state == team.StateApproved && a.Kind == team.KindLead {
		var payload team.LeadPayload
		if err := json.Unmarshal(a.Payload, &payload); err != nil {
			m.logf("[team] decide %s: decode payload: %v", id, err)
			m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
			return
		}
		// nil grant → the payload's values; an edit falls back to them
		// field by field (max_members 0, no roots).
		g := team.Grant{MaxMembers: payload.MaxMembers, Roots: payload.Roots}
		if req.Grant != nil {
			g.MaxMembers = normaliseMaxMembers(req.Grant.MaxMembers, payload.MaxMembers)
			if len(req.Grant.Roots) > 0 {
				roots, err := normaliseRoots(req.Grant.Roots, a.Origin.Cwd)
				if err != nil {
					m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, err.Error(), nil)
					return
				}
				g.Roots = roots
			}
		}
		grant = &g
	}
	c := Close{State: state, DecidedAt: now, DecidedBy: &client, Grant: grant}
	var after team.Approval
	var won bool
	if grant != nil {
		// Spec §6.2: the approval creates the team (§7.1) in the same
		// transaction. Its id is the request's id (plan v3 deviation 1).
		t := team.Team{ID: id, HostID: a.HostID, LeadSessionID: a.Origin.SessionID, LeadRef: a.Origin.Ref,
			Grant: *grant, RequestID: id, CreatedAt: now}
		after, won, err = m.closeWith(id, func() (team.Approval, bool, error) { return m.store.CloseLeadApproved(id, c, t) })
	} else {
		after, won, err = m.closeAs(id, c)
	}
	if errors.Is(err, ErrNoSuchApproval) {
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no such approval request", nil)
		return
	}
	if errors.Is(err, ErrLeadHasTeam) {
		// Both writes rolled back: the row is still open (the user may deny
		// it, or it times out). No approval in the body: a 409 carrying one
		// reads as "closed elsewhere" to the App.
		m.logf("[team] approval %s: approve by %s %q from %s refused: origin %s already leads a live team", id, client.Kind, client.Label, client.Addr, a.Origin.Ref)
		m.writeErr(w, http.StatusConflict, team.ErrAlreadyLead, "this session already leads a live team; the request stays open", nil)
		return
	}
	if errors.Is(err, ErrMemberCannotLead) { // as already_lead: rolled back, the row stays open
		m.logf("[team] approval %s: approve by %s %q refused: origin %s is a member of a live team", id, client.Kind, client.Label, a.Origin.Ref)
		m.writeErr(w, http.StatusConflict, team.ErrMemberCannotLead, "this session is a member of a live team; the request stays open", nil)
		return
	}
	if err != nil {
		m.logf("[team] decide %s: %v", id, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if !won {
		m.writeErr(w, http.StatusConflict, team.ErrAlreadyDecided, "this request was closed first by someone else", &after)
		return
	}
	teamNote := ""
	if grant != nil {
		teamNote = fmt.Sprintf("; team %s created (max_members %d, roots %v)", id, grant.MaxMembers, grant.Roots)
	}
	m.logf("[team] approval %s %s by %s %q from %s (origin %s)%s", id, after.State, client.Kind, client.Label, client.Addr, after.Origin.Ref, teamNote)
	m.writeJSON(w, http.StatusOK, after)
}
