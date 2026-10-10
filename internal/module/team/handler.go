package teammod

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

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
