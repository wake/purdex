package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"github.com/wake/purdex/internal/convfeed"
	"github.com/wake/purdex/internal/convmodel"
)

const (
	defaultTurns = 20
	maxTurns     = 200
	// envelopeSlack covers what varies in the envelope around the turns (digits of the indexes and counts).
	envelopeSlack = 512
	// maxItemID bounds the item id an `around` names (ids are short; nothing legitimate is longer).
	maxItemID = 256
)

var sessionIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type usageJSON struct {
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}

type headerJSON struct {
	Title   string     `json:"title"`
	Status  string     `json:"status"`
	Backend string     `json:"backend"`
	Usage   *usageJSON `json:"usage,omitempty"`
	Live    bool       `json:"live"`
}

type windowJSON struct {
	FirstIndex    int  `json:"first_index"`
	LastIndex     int  `json:"last_index"`
	TotalTurns    int  `json:"total_turns"`
	HasMoreBefore bool `json:"has_more_before"`
}

type snapshotJSON struct {
	Reset        bool             `json:"reset,omitempty"`
	Conversation conversationJSON `json:"conversation"`
	Header       headerJSON       `json:"header"`
	Window       windowJSON       `json:"window"`
	Cursor       string           `json:"cursor"`
}

func headerOf(h convfeed.Header) headerJSON {
	var u *usageJSON
	if h.Usage != nil {
		u = &usageJSON{Model: h.Usage.Model, Effort: h.Usage.Effort}
	}
	return headerJSON{Title: h.Title, Status: h.Status, Backend: h.Backend, Usage: u, Live: h.Live}
}

func writeJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}

func writeError(w http.ResponseWriter, status int, code string) {
	b, _ := json.Marshal(map[string]string{"error": code})
	writeJSON(w, status, b)
}

// intParam parses an optional non-negative integer query value.
func intParam(w http.ResponseWriter, r *http.Request, name, errCode string, def, min, max int) (int, bool) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		writeError(w, http.StatusBadRequest, errCode)
		return 0, false
	}
	return n, true
}

// handleSnapshot answers GET /api/conversations/{provider}/{session_id}?turns=N&before=I. Every validation happens
// before the cache is touched (and so before any file is opened).
func (m *Module) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("provider") != "claude" {
		writeError(w, http.StatusNotFound, "provider_unsupported")
		return
	}
	sid := r.PathValue("session_id")
	if !sessionIDRe.MatchString(sid) {
		writeError(w, http.StatusBadRequest, "bad_session_id")
		return
	}
	q := r.URL.Query()
	if q.Has("after") && q.Has("turns") {
		writeError(w, http.StatusBadRequest, "turns_and_after") // an increment has no window to shape
		return
	}
	if q.Has("after") && (q.Has("before") || q.Has("around")) {
		writeError(w, http.StatusBadRequest, "bad_query")
		return
	}
	if q.Has("around") && q.Has("before") {
		writeError(w, http.StatusBadRequest, "before_and_around")
		return
	}
	var afterEpoch string
	var afterRev uint64
	if q.Has("after") {
		var err error
		if afterEpoch, afterRev, err = convfeed.ParseCursor(q.Get("after")); err != nil {
			writeError(w, http.StatusBadRequest, "bad_cursor")
			return
		}
	}
	around := q.Get("around")
	if q.Has("around") && (around == "" || len(around) > maxItemID) {
		writeError(w, http.StatusBadRequest, "bad_around")
		return
	}
	turns, ok := intParam(w, r, "turns", "bad_turns", defaultTurns, 1, maxTurns)
	if !ok {
		return
	}
	before, ok := intParam(w, r, "before", "bad_before", -1, 0, int(^uint32(0)>>1))
	if !ok {
		return
	}

	entry, release, err := m.cache.Acquire(r.Context(), sid)
	if err != nil {
		if errors.Is(err, convfeed.ErrBusy) {
			writeError(w, http.StatusServiceUnavailable, "busy")
		}
		return // a cancelled request has nobody to answer
	}
	defer release()

	if err := entry.Exclusive(r.Context(), func() error { return m.refresh(r.Context(), entry, sid) }); err != nil {
		m.writeRefreshError(w, r, err)
		return
	}

	hostID := m.hostID()
	reset := false
	if q.Has("after") {
		inc := entry.Increment(afterEpoch, afterRev)
		if !inc.Stale {
			if body, ok := m.encodeIncrement(inc, hostID, 0); ok {
				writeJSON(w, http.StatusOK, body)
				return
			}
		}
		reset = true // a cursor of another epoch, or a catch-up too big for one answer: a fresh snapshot instead
	}
	body, _, status, code := m.snapshotBody(entry, sid, hostID, turns, before, around, q.Has("around"), reset)
	if code != "" {
		writeError(w, status, code)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// writeRefreshError answers a failed resolve-and-refresh (nothing for a request that is already gone).
func (m *Module) writeRefreshError(w http.ResponseWriter, r *http.Request, err error) {
	var re resolveError
	switch {
	case r.Context().Err() != nil: // the request is gone: nobody to answer
	case errors.Is(err, convfeed.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, convfeed.ErrFileChanged):
		writeError(w, http.StatusServiceUnavailable, "file_changed")
	case errors.As(err, &re):
		writeError(w, http.StatusInternalServerError, "resolve_failed")
	default:
		writeError(w, http.StatusInternalServerError, "read_failed")
	}
}

func (m *Module) hostID() string {
	m.core.CfgMu.RLock()
	defer m.core.CfgMu.RUnlock()
	return m.core.Cfg.HostID
}

// snapshotBody builds the snapshot answer (the same body for HTTP and for a WebSocket's snapshot frame): the window
// under the 4 MiB cap, the header and the cursor of one instant. On failure it returns the HTTP status and error code.
func (m *Module) snapshotBody(entry *convfeed.Entry, sid, hostID string, turns, before int, around string, hasAround, reset bool) (body []byte, cursor string, status int, code string) {
	build := func(h convfeed.Header, cursor string, turnList []convmodel.Turn, win convfeed.WindowResult, reset bool) snapshotJSON {
		var cu *convmodel.Usage
		if h.Usage != nil {
			cu = &convmodel.Usage{Model: h.Usage.Model, Effort: h.Usage.Effort}
		}
		return snapshotJSON{
			Reset: reset,
			Conversation: conversationJSON{
				Key:      convmodel.Key{HostID: hostID, Provider: "claude", SessionID: sid},
				Provider: "claude", Backend: h.Backend, Title: h.Title, Status: h.Status, Usage: cu, Turns: apiTurns(turnList),
			},
			Header: headerOf(h),
			Window: windowJSON{FirstIndex: win.FirstIndex, LastIndex: win.LastIndex, TotalTurns: win.TotalTurns, HasMoreBefore: win.HasMoreBefore},
			Cursor: cursor,
		}
	}
	envelope := func(h convfeed.Header, cursor string) func([]byte) bool {
		empty, _ := json.Marshal(build(h, cursor, []convmodel.Turn{}, convfeed.WindowResult{}, reset))
		return func(turnArray []byte) bool { // the array replaces the "[]" of the empty body
			return len(empty)-2+len(turnArray)+envelopeSlack <= m.maxBody
		}
	}
	var view convfeed.View
	if hasAround {
		var found, shown bool
		if view, found, shown = entry.ViewAround(turns, around, envelope, encodeAPITurn); !found {
			return nil, "", http.StatusNotFound, "item_not_found"
		} else if !shown && !view.OverBudget {
			return nil, "", http.StatusUnprocessableEntity, "item_not_shown" // its turn is over the cap and the item was dropped
		}
	} else {
		view = entry.View(turns, before, envelope, encodeAPITurn)
	}
	if view.OverBudget {
		return nil, "", http.StatusInternalServerError, "too_large"
	}
	if view.Turns == nil {
		view.Turns = []convmodel.Turn{}
	}
	body, err := json.Marshal(build(view.Header, view.Cursor, view.Turns, view.WindowResult, reset))
	if err != nil {
		return nil, "", http.StatusInternalServerError, "encode_failed"
	}
	return body, view.Cursor, http.StatusOK, ""
}

type turnHeaderJSON struct {
	ID           string               `json:"id"`
	Index        int                  `json:"index"`
	StartedAt    int64                `json:"started_at"`
	EndedAt      *int64               `json:"ended_at,omitempty"`
	Outcome      convmodel.Outcome    `json:"outcome"`
	Error        *convmodel.TurnError `json:"error,omitempty"`
	OmittedItems int                  `json:"omitted_items,omitempty"`
}

type changeJSON struct {
	Turn  turnHeaderJSON `json:"turn"`
	Items []indexedItem  `json:"items"`
}

type incrementJSON struct {
	Changes []changeJSON `json:"changes"`
	Header  headerJSON   `json:"header"`
	Cursor  string       `json:"cursor"`
}

// encodeIncrement is the answer to a valid cursor; ok is false when it would pass the body cap (the caller then
// answers a reset with a snapshot, which has its own way to fit).
func (m *Module) encodeIncrement(inc convfeed.Increment, hostID string, overhead int) (body []byte, ok bool) {
	resp := incrementJSON{Changes: make([]changeJSON, 0, len(inc.Changes)), Header: headerOf(inc.Header), Cursor: inc.Cursor}
	for _, c := range inc.Changes {
		t := c.Turn
		items := indexedItemsAt(c.Items, c.Indexes)
		resp.Changes = append(resp.Changes, changeJSON{
			Turn:  turnHeaderJSON{ID: t.ID, Index: t.Index, StartedAt: t.StartedAt, EndedAt: t.EndedAt, Outcome: t.Outcome, Error: t.Error, OmittedItems: t.OmittedItems},
			Items: items,
		})
	}
	body, err := json.Marshal(resp)
	if err != nil || len(body)+overhead > m.maxBody { // overhead: what a WebSocket frame adds around the body
		return nil, false
	}
	return body, true
}

// resolveError marks a resolver failure that is not "not found".
type resolveError struct{ error }

// refresh resolves the transcript and feeds the entry from it. A file that shrinks under a read is retried once
// against a fresh resolution. Every opened file is closed on every path, panics included.
func (m *Module) refresh(ctx context.Context, entry *convfeed.Entry, sid string) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if err = m.refreshOnce(ctx, entry, sid); !errors.Is(err, convfeed.ErrFileChanged) {
			return err
		}
	}
	return err
}

func (m *Module) refreshOnce(ctx context.Context, entry *convfeed.Entry, sid string) error {
	src, err := m.resolver.Resolve(ctx, sid)
	if err != nil {
		if errors.Is(err, convfeed.ErrNotFound) || ctx.Err() != nil {
			return err
		}
		return resolveError{err}
	}
	defer src.Closer.Close()
	_, err = entry.Refresh(ctx, src)
	return err
}
