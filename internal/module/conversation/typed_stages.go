package conversation

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"

	"github.com/wake/purdex/internal/convfeed"
)

// How the typed routes feed the message stage ledger (stages.go). /submit writes received after the session and the owner are
// resolved, filled when the mod filled the composer, and, when the handler returns, the stage its answer stands for:
// handed -> entered, refused -> failed, unknown -> unknown. A text left in the terminal is a refusal that does not know: unknown.

// stageWriter remembers the typed answer the handler wrote, so the stage can follow it without every refusal site knowing.
type stageWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

const maxStageAnswer = 4 << 10 // an answer is a few dozen bytes

func (w *stageWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *stageWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.body.Len() < maxStageAnswer {
		w.body.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// stageOfAnswer is the stage an answer of the submit route stands for ("" for none).
func stageOfAnswer(status int, body []byte) (stage, reason, mode string) {
	var a typedAnswer
	if json.Unmarshal(body, &a) != nil {
		return "", "", ""
	}
	switch {
	case a.Outcome == "handed":
		return stageEntered, "", a.Mode
	case a.Outcome == "unknown":
		reason = a.Reason
		if reason == "" {
			reason = "unknown"
		}
		return stageUnknown, reason, a.Mode
	case a.Reason == "text_left_in_terminal": // keys mode: typed, the Enter did not go cleanly
		return stageUnknown, a.Reason, "keys"
	case a.Outcome == "refused" || status >= 400:
		reason = a.Reason
		if reason == "" {
			reason = a.Error
		}
		if reason == "" {
			reason = "internal"
		}
		return stageFailed, reason, ""
	}
	return "", "", ""
}

// recordAnswer moves the id to the stage its answer stands for.
func (m *Module) recordAnswer(sid, id string, tok uint64, w *stageWriter, now time.Time) {
	if w.status == 0 {
		return
	}
	if stage, reason, mode := stageOfAnswer(w.status, w.body.Bytes()); stage != "" {
		m.stages.setAttempt(sid, id, tok, stage, reason, mode, now)
	}
}

// observeStages lets the ledger compare the session's queue and user items with the ids still in flight. It reads the entry
// only while some id could still change (a conversation of thousands of items is not walked for nothing).
func (m *Module) observeStages(sid string, entry *convfeed.Entry) {
	if entry == nil || !m.stages.inFlight(sid) {
		return
	}
	var items []stageItem
	for _, u := range entry.UserItems() {
		items = append(items, stageItem{ID: u.ID, Text: u.Text, At: u.At})
	}
	m.stages.observe(sid, entry.Header().Queued, items, m.tuning().now())
}
