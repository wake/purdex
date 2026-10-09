package workbook

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/wake/purdex/internal/team"
)

// Page sizes of the list routes.
const (
	defaultLimit = 50
	maxLimit     = 200
	doneRecordN  = 20 // the done todos the conversation answer carries (spec §9)
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

	// v2 (capability workbook.v2)
	Kind        string          `json:"kind"`
	Usage       usageJSON       `json:"usage"`
	TodoChanges todoChangesJSON `json:"todo_changes"`
}

type usageJSON struct {
	In        int64 `json:"in"`
	Out       int64 `json:"out"`
	CacheRead int64 `json:"cache_read"`
}

type idTitleJSON struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

type todoChangesJSON struct {
	Added   []idTitleJSON `json:"added"`
	Done    []idTitleJSON `json:"done"`
	Dropped []idTitleJSON `json:"dropped"`
}

// todoJSON is a todo on the wire (spec §9): times are unix ms; closed_* are 0 while the todo is open.
type todoJSON struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	Detail        string `json:"detail"`
	State         string `json:"state"`
	ClosedBy      string `json:"closed_by"`
	CreatedAt     int64  `json:"created_at"`
	ClosedAt      int64  `json:"closed_at"`
	AddedEntryID  int64  `json:"added_entry_id"`
	ClosedEntryID int64  `json:"closed_entry_id"`
}

// todoListsJSON is the conversation answer's todos: the open list oldest first, the done record newest first.
type todoListsJSON struct {
	Open []todoJSON `json:"open"`
	Done []todoJSON `json:"done"`
}

func todoWire(t Todo) todoJSON {
	return todoJSON{ID: t.ID, Title: t.Title, Detail: t.Detail, State: t.State, ClosedBy: t.ClosedBy, CreatedAt: t.CreatedAt,
		ClosedAt: t.ClosedAt, AddedEntryID: t.AddedEntryID, ClosedEntryID: t.ClosedEntryID}
}

func todosWire(ts []Todo) []todoJSON {
	out := make([]todoJSON, 0, len(ts))
	for _, t := range ts {
		out = append(out, todoWire(t))
	}
	return out
}

func idTitles(ts []Todo) []idTitleJSON {
	out := make([]idTitleJSON, 0, len(ts))
	for _, t := range ts {
		out = append(out, idTitleJSON{ID: t.ID, Title: t.Title})
	}
	return out
}

func changesWire(c EntryTodoChanges) todoChangesJSON {
	return todoChangesJSON{Added: idTitles(c.Added), Done: idTitles(c.Done), Dropped: idTitles(c.Dropped)}
}

func entryWire(e Entry, ch EntryTodoChanges) entryJSON {
	return entryJSON{ID: e.ID, ConvKey: e.ConvKey, HostID: e.HostID, Provider: e.Provider, SessionID: e.SessionID, TurnID: e.TurnID,
		TurnAt: e.TurnAt, TurnSeq: e.TurnSeq, State: e.State, Reason: e.Reason, Thing: e.Thing, Push: e.Push, Entry: e.Entry,
		ThingDone: e.ThingDone, PushReadyAt: e.PushReadyAt, TeamID: e.TeamID, Role: e.Role, Ref: e.Ref, PromptVer: e.PromptVer,
		LatencyMS: e.LatencyMS, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
		Kind: e.Kind, Usage: usageJSON{In: e.UsageIn, Out: e.UsageOut, CacheRead: e.UsageCacheRead}, TodoChanges: changesWire(ch)}
}

// entriesWire is the rows with what each did to the todo list (one read for the page).
func (m *Module) entriesWire(st *Store, rows []Entry) ([]entryJSON, error) {
	ids := make([]int64, 0, len(rows))
	for _, e := range rows {
		ids = append(ids, e.ID)
	}
	changes, err := st.TodoChangesByEntry(ids)
	if err != nil {
		return nil, err
	}
	out := make([]entryJSON, 0, len(rows))
	for _, e := range rows {
		out = append(out, entryWire(e, changes[e.ID]))
	}
	return out, nil
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

// RegisterRoutes mounts the read API (capabilities workbook.v1 and workbook.v2).
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/workbook/conversations/{provider}/{session_id}", m.handleConversation)
	mux.HandleFunc("GET /api/workbook/entries", m.handleEntries)
	mux.HandleFunc("GET /api/workbook/conversations/{provider}/{session_id}/todos", m.handleTodos)
	mux.HandleFunc("POST /api/workbook/conversations/{provider}/{session_id}/refresh", m.handleRefresh) // the Mac App's; not a device route
}

// handleRefresh: POST …/refresh → 202 {entry_id}; 409 not_live (no live session of the conversation whose mod announced
// workbook.refresh) or refresh_pending (one is under way).
func (m *Module) handleRefresh(w http.ResponseWriter, r *http.Request) {
	eng := m.currentEngine()
	if eng == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	if r.PathValue("provider") != "claude" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	id, err := eng.RequestRefresh(r.PathValue("session_id"), "")
	switch {
	case errors.Is(err, ErrNotLive):
		writeError(w, http.StatusConflict, "not_live")
	case errors.Is(err, ErrRefreshPending):
		writeError(w, http.StatusConflict, "refresh_pending")
	case err != nil:
		log.Printf("[workbook] request a refresh: %v", err)
		writeError(w, http.StatusInternalServerError, "internal")
	default:
		writeJSON(w, http.StatusAccepted, map[string]int64{"entry_id": id})
	}
}

// refreshAvailable: now, some session of the conversation can run a refresh (computed on read).
func (m *Module) refreshAvailable(conv string) bool {
	eng := m.currentEngine()
	return eng != nil && eng.RefreshAvailable(conv)
}

func (m *Module) currentEngine() *Engine {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.engine
}

type refreshAvailableEventJSON struct {
	ConvKey   string `json:"conv_key"`
	Available bool   `json:"available"`
}

// announceAvailability sends workbook.refresh_available for each conversation whose value changed (plan D11).
func (m *Module) announceAvailability(changes []AvailabilityChange) {
	m.mu.Lock()
	send := m.broadcast
	m.mu.Unlock()
	if send == nil {
		return
	}
	for _, c := range changes {
		b, err := json.Marshal(refreshAvailableEventJSON{ConvKey: c.ConvKey, Available: c.Available})
		if err != nil {
			continue
		}
		send("workbook.refresh_available", string(b))
	}
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
	if len(rows) == 0 && !haveStatus {
		// an exhausted page of a conversation that exists is an empty 200; a conversation that was never written is 404
		known, err := st.HasEntries(conv)
		if err != nil {
			log.Printf("[workbook] read conversation: %v", err)
			writeError(w, http.StatusInternalServerError, "internal")
			return
		}
		if !known {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
	}
	entries, err := m.entriesWire(st, rows)
	if err == nil {
		var open, done []Todo
		if open, err = st.OpenTodos(conv, maxOpenTodos); err == nil {
			if done, err = st.Todos(conv, TodoDone, doneRecordN, 0); err == nil {
				writeJSON(w, http.StatusOK, map[string]any{
					"conv_key":          conv,
					"status":            status.Status,
					"status_at":         status.UpdatedAt,
					"entries":           entries,
					"todos":             todoListsJSON{Open: todosWire(open), Done: todosWire(done)},
					"refresh_available": m.refreshAvailable(conv),
				})
				return
			}
		}
	}
	log.Printf("[workbook] read the conversation's v2 parts: %v", err)
	writeError(w, http.StatusInternalServerError, "internal")
}

// handleTodos: GET …/todos?state=open|done|dropped&limit=&before= — the list or the done record, paged by todo id, newest
// first.
func (m *Module) handleTodos(w http.ResponseWriter, r *http.Request) {
	st := m.live()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	if r.PathValue("provider") != "claude" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	state := TodoOpen
	if r.URL.Query().Has("state") {
		state = r.URL.Query().Get("state")
	}
	if state != TodoOpen && state != TodoDone && state != TodoDropped {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	limit, ok := intQuery(w, r, "limit", defaultLimit, 1, maxLimit)
	if !ok {
		return
	}
	before, ok := intQuery(w, r, "before", 0, 1, 1<<62)
	if !ok {
		return
	}
	conv, err := m.convKeyOf(r.PathValue("session_id"))
	if err != nil {
		log.Printf("[workbook] resolve conversation: %v", err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	known, err := st.Known(conv)
	if err != nil {
		log.Printf("[workbook] read conversation: %v", err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	if !known {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	rows, err := st.Todos(conv, state, int(limit), before)
	if err != nil {
		log.Printf("[workbook] read todos: %v", err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"todos": todosWire(rows)})
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
	entries, err := m.entriesWire(st, rows)
	if err != nil {
		log.Printf("[workbook] read todo changes: %v", err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
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

type todosEventJSON struct {
	ConvKey   string     `json:"conv_key"`
	SessionID string     `json:"session_id"`
	Todos     []todoJSON `json:"todos"`
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
		var ch EntryTodoChanges
		if st := m.live(); st != nil { // the observer runs holding no store lock: it may read back
			got, err := st.TodoChangesByEntry([]int64{e.Entry.ID})
			if err != nil {
				log.Printf("[workbook] event todo changes: %v", err)
			}
			ch = got[e.Entry.ID]
		}
		typ, v = "workbook.entry", entryEventJSON{ConvKey: e.ConvKey, SessionID: e.SessionID, Entry: entryWire(e.Entry, ch)}
	case EventTodos:
		typ, v = "workbook.todos", todosEventJSON{ConvKey: e.ConvKey, SessionID: e.SessionID, Todos: todosWire(e.Todos)}
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
