package workbook

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/wake/purdex/internal/team"
)

// Page sizes of the list routes.
const (
	defaultLimit = 50
	maxLimit     = 200
)

// entryJSON is an entry on the wire (spec §6, clarified by the plan): every time is unix milliseconds.
type entryJSON struct {
	ID          int64  `json:"id"`
	ConvKey     string `json:"conv_key"`
	HostID      string `json:"host_id"`
	Provider    string `json:"provider"`
	SessionID   string `json:"session_id"`
	TurnID      string `json:"turn_id"`
	TurnAt      int64  `json:"turn_at"`
	TurnSeq     int64  `json:"turn_seq"`
	State       string `json:"state"`
	Reason      string `json:"reason"`
	Thing       string `json:"thing"`
	Push        string `json:"push"`
	Entry       string `json:"entry"`
	ThingDone   bool   `json:"thing_done"`
	PushReadyAt int64  `json:"push_ready_at"`
	TeamID      string `json:"team_id"`
	Role        string `json:"role"`
	Ref         string `json:"ref"`
	PromptVer   int    `json:"prompt_ver"`
	LatencyMS   int64  `json:"latency_ms"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

func entryWire(e Entry) entryJSON {
	return entryJSON{ID: e.ID, ConvKey: e.ConvKey, HostID: e.HostID, Provider: e.Provider, SessionID: e.SessionID, TurnID: e.TurnID,
		TurnAt: e.TurnAt, TurnSeq: e.TurnSeq, State: e.State, Reason: e.Reason, Thing: e.Thing, Push: e.Push, Entry: e.Entry,
		ThingDone: e.ThingDone, PushReadyAt: e.PushReadyAt, TeamID: e.TeamID, Role: e.Role, Ref: e.Ref, PromptVer: e.PromptVer,
		LatencyMS: e.LatencyMS, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt}
}

func entriesWire(rows []Entry) []entryJSON {
	out := make([]entryJSON, 0, len(rows))
	for _, e := range rows {
		out = append(out, entryWire(e))
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		status, b = http.StatusInternalServerError, []byte(`{"error":"internal"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(b)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

// RegisterRoutes mounts the read API (capability workbook.v1).
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/workbook/conversations/{provider}/{session_id}", m.handleConversation)
	mux.HandleFunc("GET /api/workbook/entries", m.handleEntries)
}

// intQuery reads an optional integer query value in [min, max]; ok is false after it has answered 400.
func intQuery(w http.ResponseWriter, r *http.Request, name string, def, min, max int64) (int64, bool) {
	if !r.URL.Query().Has(name) {
		return def, true
	}
	n, err := strconv.ParseInt(r.URL.Query().Get(name), 10, 64)
	if err != nil || n < min || n > max {
		writeError(w, http.StatusBadRequest, "bad_request")
		return 0, false
	}
	return n, true
}

// handleConversation: GET /api/workbook/conversations/{provider}/{session_id}?limit=&before=. The session may be any
// session of the conversation: the relay chain's root is its key (spec §4.1).
func (m *Module) handleConversation(w http.ResponseWriter, r *http.Request) {
	st := m.live()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	if r.PathValue("provider") != "claude" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	sid := r.PathValue("session_id")
	limit, ok := intQuery(w, r, "limit", defaultLimit, 1, maxLimit)
	if !ok {
		return
	}
	before, ok := intQuery(w, r, "before", 0, 1, 1<<62)
	if !ok {
		return
	}
	conv, err := m.convKeyOf(sid)
	if err != nil {
		log.Printf("[workbook] resolve conversation: %v", err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	rows, err := st.Conversation(conv, int(limit), before)
	if err != nil {
		log.Printf("[workbook] read conversation: %v", err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	status, haveStatus, err := st.Status(conv)
	if err != nil {
		log.Printf("[workbook] read status: %v", err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	if len(rows) == 0 && !haveStatus && before == 0 {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"conv_key":  conv,
		"status":    status.Status,
		"status_at": status.UpdatedAt,
		"entries":   entriesWire(rows),
	})
}

// handleEntries: GET /api/workbook/entries?since=&until=&thing_done=1&limit= — across the conversations of this host.
func (m *Module) handleEntries(w http.ResponseWriter, r *http.Request) {
	st := m.live()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	limit, ok := intQuery(w, r, "limit", defaultLimit, 1, maxLimit)
	if !ok {
		return
	}
	since, ok := intQuery(w, r, "since", 0, 0, 1<<62)
	if !ok {
		return
	}
	until, ok := intQuery(w, r, "until", 0, 0, 1<<62)
	if !ok {
		return
	}
	thingDone := false
	if r.URL.Query().Has("thing_done") {
		if r.URL.Query().Get("thing_done") != "1" {
			writeError(w, http.StatusBadRequest, "bad_request")
			return
		}
		thingDone = true
	}
	rows, err := st.Entries(since, until, thingDone, int(limit))
	if err != nil {
		log.Printf("[workbook] read entries: %v", err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entriesWire(rows)})
}

// convKeyOf is the conversation key of a session: the root of its relay chain. Without the team module's resolver (a
// test) the session is its own root.
func (m *Module) convKeyOf(sessionID string) (string, error) {
	if m.core == nil || m.core.Registry == nil {
		return sessionID, nil
	}
	svc, ok := m.core.Registry.Get(team.LineageRootKey)
	if !ok {
		return sessionID, nil
	}
	res, ok := svc.(team.LineageRootResolver)
	if !ok {
		return sessionID, nil
	}
	return res.RootSessionOf(sessionID)
}

// ---- events ----

type entryEventJSON struct {
	ConvKey   string    `json:"conv_key"`
	SessionID string    `json:"session_id"`
	Entry     entryJSON `json:"entry"`
}

type statusEventJSON struct {
	ConvKey   string `json:"conv_key"`
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	UpdatedAt int64  `json:"updated_at"`
}

// announce turns a store event into a host event: the value is a JSON string, like every host event.
func (m *Module) announce(e Event) {
	m.mu.Lock()
	send := m.broadcast
	m.mu.Unlock()
	if send == nil {
		return
	}
	var typ string
	var v any
	switch e.Kind {
	case EventEntry:
		typ, v = "workbook.entry", entryEventJSON{ConvKey: e.ConvKey, SessionID: e.SessionID, Entry: entryWire(e.Entry)}
	case EventStatus:
		typ, v = "workbook.status", statusEventJSON{ConvKey: e.ConvKey, SessionID: e.SessionID, Status: e.Status.Status, UpdatedAt: e.Status.UpdatedAt}
	default:
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		log.Printf("[workbook] event: %v", err)
		return
	}
	send(typ, string(b))
}
