package dialogue

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
)

// Page sizes of the read routes.
const (
	recapsDefaultLimit = 20
	apiMaxLimit        = 100
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		status, b = http.StatusInternalServerError, []byte(`{"error":"internal"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

// RegisterRoutes mounts the read API and the recap request (capability dialogue.v1).
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/dialogue/{session_id}/recaps", m.handleRecaps)
	mux.HandleFunc("GET /api/dialogue/{session_id}/turn-summaries", m.handleTurnSummaries)
	mux.HandleFunc("POST /api/dialogue/{session_id}/recap", m.handleRequestRecap)
}

// live is the open store (nil once the module is stopped or never opened) and the job channel (nil unless running).
func (m *Module) live() (*Store, *channel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ready || m.store == nil {
		return nil, nil
	}
	return m.store, m.jobs
}

// sessionOf answers 400 bad_session / 503 unavailable itself; ok is false then.
func (m *Module) sessionOf(w http.ResponseWriter, r *http.Request) (sid string, st *Store, ch *channel, ok bool) {
	sid = r.PathValue("session_id")
	if !validSessionID(sid) {
		writeError(w, http.StatusBadRequest, "bad_session")
		return "", nil, nil, false
	}
	st, ch = m.live()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return "", nil, nil, false
	}
	return sid, st, ch, true
}

// intParam reads an optional integer query value >= min; present is false when absent, ok is false when malformed.
func intParam(r *http.Request, name string, min int) (n int, present, ok bool) {
	q := r.URL.Query()
	if !q.Has(name) {
		return 0, false, true
	}
	v, err := strconv.Atoi(q.Get(name))
	if err != nil || v < min {
		return 0, true, false
	}
	return v, true, true
}

type recapJSON struct {
	ID       int64    `json:"id"`
	At       int64    `json:"at"`
	FromNo   int      `json:"from_no"`
	ToNo     int      `json:"to_no"`
	Trigger  string   `json:"trigger"`
	Headline string   `json:"headline"`
	Done     []string `json:"done"`
	Now      string   `json:"now"`
	Decided  []string `json:"decided"`
	Waiting  []string `json:"waiting"`
	Next     string   `json:"next"`
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// handleRecaps: GET /api/dialogue/{session_id}/recaps?limit=
func (m *Module) handleRecaps(w http.ResponseWriter, r *http.Request) {
	sid, st, ch, ok := m.sessionOf(w, r)
	if !ok {
		return
	}
	limit, present, ok := intParam(r, "limit", 1)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_limit")
		return
	}
	if !present {
		limit = recapsDefaultLimit
	}
	if limit > apiMaxLimit {
		limit = apiMaxLimit
	}
	rows, err := st.ListRecaps(sid, limit)
	var newest []SummaryRow
	var lastTo int
	if err == nil {
		newest, err = st.SummariesRange(sid, 0, 1<<30, 1)
	}
	if err == nil {
		lastTo, err = st.LatestRecapToNo(sid)
	}
	if err != nil {
		log.Printf("[dialogue] read recaps: %v", err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	out := make([]recapJSON, 0, len(rows))
	for _, row := range rows {
		body, err := recapOfRow(row)
		if err != nil {
			log.Printf("[dialogue] recap %d has an unreadable body: %v", row.ID, err)
			continue
		}
		out = append(out, recapJSON{ID: row.ID, At: row.At, FromNo: row.FromNo, ToNo: row.ToNo, Trigger: row.Trigger,
			Headline: row.Headline, Done: nonNil(body.Done), Now: body.Now, Decided: nonNil(body.Decided), Waiting: nonNil(body.Waiting), Next: body.Next})
	}
	newestNo := 0
	if len(newest) > 0 {
		newestNo = newest[0].TurnNo
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"recaps":           out,
		"can_generate":     ch != nil && ch.capable(sid),
		"newest_turn_no":   newestNo,
		"last_recap_to_no": lastTo,
	})
}

type turnSummaryJSON struct {
	TurnNo  int    `json:"turn_no"`
	At      int64  `json:"at"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Push    string `json:"push"`
}

// handleTurnSummaries: GET /api/dialogue/{session_id}/turn-summaries?from=&to=&limit= — done summaries, oldest first.
func (m *Module) handleTurnSummaries(w http.ResponseWriter, r *http.Request) {
	sid, st, _, ok := m.sessionOf(w, r)
	if !ok {
		return
	}
	from, _, okFrom := intParam(r, "from", 0)
	to, hasTo, okTo := intParam(r, "to", 0)
	limit, hasLimit, okLimit := intParam(r, "limit", 1)
	if !okFrom || !okTo || !okLimit {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if !hasTo {
		to = 1 << 30
	}
	if from > to {
		writeError(w, http.StatusBadRequest, "bad_range")
		return
	}
	if !hasLimit || limit > apiMaxLimit {
		limit = apiMaxLimit
	}
	rows, err := st.SummariesRange(sid, from, to, limit)
	if err != nil {
		log.Printf("[dialogue] read turn summaries: %v", err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	out := make([]turnSummaryJSON, 0, len(rows))
	for _, x := range rows {
		out = append(out, turnSummaryJSON{TurnNo: x.TurnNo, At: x.TurnAt, Title: x.Title, Summary: x.Summary, Push: x.Push})
	}
	writeJSON(w, http.StatusOK, map[string]any{"turn_summaries": out})
}

// handleRequestRecap: POST /api/dialogue/{session_id}/recap {"trigger":"open"|"manual"} → {"state":…}.
func (m *Module) handleRequestRecap(w http.ResponseWriter, r *http.Request) {
	sid, _, _, ok := m.sessionOf(w, r)
	if !ok {
		return
	}
	var body struct {
		Trigger string `json:"trigger"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	if len(strings.TrimSpace(string(raw))) == 0 {
		body.Trigger = "manual" // no body: a plain request
	} else if err := json.Unmarshal(raw, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_trigger")
		return
	}
	if body.Trigger != "open" && body.Trigger != "manual" {
		writeError(w, http.StatusBadRequest, "bad_trigger")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"state": m.requestRecap(sid, body.Trigger)})
}
