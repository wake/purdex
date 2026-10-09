package conversation

import (
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
	Conversation convmodel.Conversation `json:"conversation"`
	Header       headerJSON             `json:"header"`
	Window       windowJSON             `json:"window"`
	Cursor       string                 `json:"cursor"`
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
	if q.Has("after") || q.Has("around") {
		writeError(w, http.StatusNotImplemented, "not_implemented") // increments and around: the next PR of this line
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

	// A file that shrinks under a read is retried once against the fresh resolution.
	var src convfeed.Source
	for attempt := 0; ; attempt++ {
		src, err = m.resolver.Resolve(r.Context(), sid)
		if err != nil {
			if errors.Is(err, convfeed.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found")
			} else if r.Context().Err() == nil {
				writeError(w, http.StatusInternalServerError, "resolve_failed")
			}
			return
		}
		_, err = entry.Refresh(r.Context(), src)
		src.Closer.Close()
		if err == nil {
			break
		}
		if errors.Is(err, convfeed.ErrFileChanged) && attempt == 0 {
			continue
		}
		switch {
		case r.Context().Err() != nil:
		case errors.Is(err, convfeed.ErrFileChanged):
			writeError(w, http.StatusServiceUnavailable, "file_changed")
		default:
			writeError(w, http.StatusInternalServerError, "read_failed")
		}
		return
	}

	m.core.CfgMu.RLock()
	hostID := m.core.Cfg.HostID
	m.core.CfgMu.RUnlock()

	build := func(h convfeed.Header, cursor string, turnList []convmodel.Turn, w convfeed.WindowResult) snapshotJSON {
		var u *usageJSON
		var cu *convmodel.Usage
		if h.Usage != nil {
			u = &usageJSON{Model: h.Usage.Model, Effort: h.Usage.Effort}
			cu = &convmodel.Usage{Model: h.Usage.Model, Effort: h.Usage.Effort}
		}
		return snapshotJSON{
			Conversation: convmodel.Conversation{
				Key:      convmodel.Key{HostID: hostID, Provider: "claude", SessionID: sid},
				Provider: "claude", Backend: src.Backend, Title: h.Title, Status: src.Status, Usage: cu, Turns: turnList,
			},
			Header: headerJSON{Title: h.Title, Status: src.Status, Backend: src.Backend, Usage: u, Live: src.Live},
			Window: windowJSON{FirstIndex: w.FirstIndex, LastIndex: w.LastIndex, TotalTurns: w.TotalTurns, HasMoreBefore: w.HasMoreBefore},
			Cursor: cursor,
		}
	}
	view := entry.View(turns, before, func(h convfeed.Header, cursor string) func([]byte) bool {
		empty, _ := json.Marshal(build(h, cursor, []convmodel.Turn{}, convfeed.WindowResult{}))
		return func(turnArray []byte) bool { // the array replaces the "[]" of the empty body
			return len(empty)-2+len(turnArray)+envelopeSlack <= m.maxBody
		}
	})
	if view.OverBudget {
		writeError(w, http.StatusInternalServerError, "too_large")
		return
	}
	if view.Turns == nil {
		view.Turns = []convmodel.Turn{}
	}
	body, err := json.Marshal(build(view.Header, view.Cursor, view.Turns, view.WindowResult))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encode_failed")
		return
	}
	writeJSON(w, http.StatusOK, body)
}
