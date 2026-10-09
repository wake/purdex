package modevents

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"time"
)

// EventsPath is the channel's ingest route (POST).
const EventsPath = "/mod/v1/events"

// TeamPath is the one read the channel answers (GET, TI-5a): the team role of a session.
const TeamPath = "/mod/v1/team"

// maxSessionID bounds the session_id query value (a Claude Code session id is a 36-byte uuid).
const maxSessionID = 128

// TeamRead is the team module's answer for one session (JSON as the mod reads it).
type TeamRead struct {
	Role      string `json:"role"` // lead | member | none
	Members   int    `json:"members"`
	TeamLabel string `json:"team_label"`
}

// TeamReader answers for one session id. An error is "could not tell", never "none"; ErrTeamUnavailable (the team
// module is not there) is a 503, any other error a 500.
type TeamReader func(sessionID string) (TeamRead, error)

// ErrTeamUnavailable is what a TeamReader returns when there is no team module to ask.
var ErrTeamUnavailable = errors.New("modevents: team module unavailable")

// HandlerOption configures NewHandler.
type HandlerOption func(*handler)

// WithTeamReader enables GET /mod/v1/team. Without it the route answers 503 unavailable (the mod keeps its last good value).
func WithTeamReader(r TeamReader) HandlerOption { return func(h *handler) { h.team = r } }

type handler struct {
	reg      *Registry
	team     TeamReader
	workbook func() WorkbookService
	polls    pollGate
	// activePolls counts the long polls being served (hard cap maxPolls).
	activePolls atomic.Int32
	maxPolls    int
}

// withMaxPolls lowers the poll cap (tests).
func withMaxPolls(n int) HandlerOption { return func(h *handler) { h.maxPolls = n } }

// MaxBody caps a request body; larger bodies get 413.
const MaxBody = 1 << 20

// NewHandler serves POST /mod/v1/events into reg: 200 {"ack":N}, 400
// {"error":"<code>"} (counted on the stream when its id was valid), 413
// {"error":"too_large"}, 503 {"error":"registry_full"} when Apply refuses
// a new stream with ErrRegistryFull (the mod backs off and resends), and
// 500 {"error":"internal"} for any other Apply error. Any other path is
// 404, any other method 405.
func NewHandler(reg *Registry, opts ...HandlerOption) http.Handler {
	h := &handler{reg: reg, maxPolls: defaultMaxPolls}
	for _, o := range opts {
		o(h)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case EventsPath:
			h.events(w, r)
		case TeamPath:
			h.teamRead(w, r)
		case WorkbookNextPath:
			h.workbookNext(w, r)
		case WorkbookRefreshPath:
			h.workbookRefresh(w, r)
		case WorkbookResultPath:
			h.workbookResult(w, r)
		default:
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		}
	})
}

// teamRead serves GET /mod/v1/team?session_id=<sid>: 200 {"role","members","team_label"}, 400 bad_request (no, empty,
// repeated or over-long session_id), 405 for any other method, 503 unavailable without a team module, 500 internal.
func (h *handler) teamRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	ids := r.URL.Query()["session_id"]
	if len(ids) != 1 || ids[0] == "" || len(ids[0]) > maxSessionID {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	if h.team == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	got, err := h.team(ids[0])
	if errors.Is(err, ErrTeamUnavailable) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	writeJSON(w, http.StatusOK, got)
}

// events serves POST /mod/v1/events.
func (h *handler) events(w http.ResponseWriter, r *http.Request) {
	reg := h.reg
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	b, err := DecodeBatch(http.MaxBytesReader(w, r.Body, MaxBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "too_large"})
			return
		}
		code := CodeBadJSON
		var we *WireError
		if errors.As(err, &we) {
			code = we.Code
			if we.Stream != "" {
				reg.Reject(we.Stream)
			}
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": code})
		return
	}
	ack, err := reg.Apply(b)
	switch {
	case errors.Is(err, ErrRegistryFull):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "registry_full"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
	default:
		// `workbook: true` tells the mod a job of its session's conversation waits that nobody holds, so it asks `next`
		// (wire additive: an older mod ignores the field). Without a waiting job the answer is the bare ack.
		if svc, sid := h.service(), b.Events[len(b.Events)-1].SID; svc != nil && h.streamMayPoll(b.Stream, sid) && svc.JobWaiting(sid) {
			writeJSON(w, http.StatusOK, map[string]any{"ack": ack, "workbook": true})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int64{"ack": ack})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, _ := json.Marshal(v) // maps of strings and ints always marshal
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// NewServer is the channel's own server, with the timeouts of spec §6.1.
func NewServer(h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
}
