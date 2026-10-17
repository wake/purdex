package workbook

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/journal"
)

// The confirm body must be exactly one JSON object: trailing tokens, garbage, or a tail past the body limit are a 400 and
// nothing is written.
// Mutation gate: drop the second Decode / EOF check -> the trailing-garbage cases write the file (red).
func TestJournalAPI_ConfirmBodyMustBeOneObject(t *testing.T) {
	j := newJournalAPI(t)
	u := j.unit("team:t1", "日誌")
	j.write(u, journal.Entry{ID: "ask", Kind: journal.KindConfirm, At: tm(time.October, 5, 1), Actor: "A", Items: []journal.Item{{Text: "甲?"}}})
	j.seats.seats["m1"] = teamSeat("t1")
	ok := `{"session_id":"m1","entry_id":"ask","idx":0,"done":true}`
	for name, body := range map[string]string{
		"garbage":      ok + " garbage",
		"second value": ok + ` {"a":1}`,
		"big tail":     ok + strings.Repeat(" ", 70<<10) + "x",
		"big spaces":   ok + strings.Repeat(" ", 70<<10),
	} {
		w := httptest.NewRecorder()
		j.mux.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/journal/confirm", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: code %d", name, w.Code)
		}
		es, _, _ := journal.ReadUnit(unitHome(u).journalDir)
		if es[0].Items[0].Done || es[0].Items[0].Manual {
			t.Fatalf("%s: the file was written", name)
		}
	}
	w := httptest.NewRecorder()
	j.mux.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/journal/confirm", strings.NewReader(ok+"\n  \n")))
	if w.Code != http.StatusNoContent {
		t.Errorf("trailing whitespace: code %d", w.Code)
	}
}
