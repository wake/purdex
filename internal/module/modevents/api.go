package modeventsmod

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// The read API (spec §6.6), on the daemon's TCP mux: the channel's state
// and its streams, and a stream's recent events, for acceptance and
// fixture capture.

type socketJSON struct {
	Path    string `json:"path"` // resolved, also when disabled
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
}

type streamJSON struct {
	Stream       string           `json:"stream"`
	Agent        string           `json:"agent"`
	SID          string           `json:"sid"`
	CWD          string           `json:"cwd"`
	Interactive  bool             `json:"interactive"`
	CCVersion    string           `json:"cc_version"`
	ModVersion   string           `json:"mod_version"`
	FirstSeen    string           `json:"first_seen"` // RFC 3339, UTC
	LastSeen     string           `json:"last_seen"`  // RFC 3339, UTC
	LastSeq      int64            `json:"last_seq"`
	Gaps         int64            `json:"gaps"`
	DroppedTotal int64            `json:"dropped_total"`
	Rejected     int64            `json:"rejected"`
	Ended        bool             `json:"ended"`
	Counts       map[string]int64 `json:"counts"`
}

type streamsJSON struct {
	Socket  socketJSON   `json:"socket"`
	Streams []streamJSON `json:"streams"`
}

// eventJSON is an event as the mod sent it; data is embedded as is.
type eventJSON struct {
	Seq  int64           `json:"seq"`
	At   int64           `json:"at"` // the mod's Date.now(), ms
	SID  string          `json:"sid"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type eventsJSON struct {
	Events []eventJSON `json:"events"`
}

func rfc3339UTC(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// handleStreams: GET /api/mod/streams. Streams come most recently seen
// first, as the registry returns them.
func (m *Module) handleStreams(w http.ResponseWriter, _ *http.Request) {
	st := m.Status()
	out := streamsJSON{
		Socket:  socketJSON{Path: m.SocketPathForInfo(), Enabled: st.Enabled},
		Streams: []streamJSON{},
	}
	if !st.Enabled {
		out.Socket.Reason = st.Reason
	}
	for _, s := range m.reg.Streams() {
		counts := s.Counts
		if counts == nil {
			counts = map[string]int64{}
		}
		out.Streams = append(out.Streams, streamJSON{
			Stream: s.Stream, Agent: s.Agent, SID: s.SID, CWD: s.CWD, Interactive: s.Interactive,
			CCVersion: s.CCVersion, ModVersion: s.ModVersion,
			FirstSeen: rfc3339UTC(s.FirstSeen), LastSeen: rfc3339UTC(s.LastSeen),
			LastSeq: s.LastSeq, Gaps: s.Gaps, DroppedTotal: s.DroppedTotal, Rejected: s.Rejected,
			Ended: s.Ended, Counts: counts,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleEvents: GET /api/mod/streams/{stream}/events?after=<seq>, the
// stream's ring after seq (default 0), oldest first. A non-integer or
// negative after is 400 bad_after; an unknown stream is 404 no_stream.
func (m *Module) handleEvents(w http.ResponseWriter, r *http.Request) {
	var after int64
	if q := r.URL.Query().Get("after"); q != "" {
		n, err := strconv.ParseInt(q, 10, 64)
		if err != nil || n < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_after"})
			return
		}
		after = n
	}
	evs, ok := m.reg.Events(r.PathValue("stream"), after)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no_stream"})
		return
	}
	out := eventsJSON{Events: make([]eventJSON, 0, len(evs))}
	for _, e := range evs {
		out.Events = append(out.Events, eventJSON{Seq: e.Seq, At: e.At, SID: e.SID, Type: e.Type, Data: e.Data})
	}
	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		// Only a malformed Data could fail, and decoded events always hold
		// a JSON object.
		status, b = http.StatusInternalServerError, []byte(`{"error":"encode"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
