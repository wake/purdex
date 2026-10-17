package workbook

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/journal"
)

// Transition: pages still carry pinned[] (same entries as confirms[]) and every entry a pinned flag (true exactly for the
// confirms), so the SPA before JV-3 still shows the 待確認 band; PUT /api/journal/pin answers 410 with the text the old App shows.
// Mutation gate: drop pinned[] from the page -> red; flag every entry pinned=false -> red; the pin route answers 404 -> red.
func TestJournalAPI_PinnedMirrorsConfirmsAndPinRouteIsGone(t *testing.T) {
	j := newJournalAPI(t)
	u := j.unit("team:t1", "日誌")
	j.write(u,
		journal.Entry{ID: "ask", Kind: journal.KindConfirm, At: tm(time.October, 5, 1), Actor: "A", Items: []journal.Item{{Text: "甲?"}}},
		journal.Entry{ID: "done", Kind: journal.KindConfirm, At: tm(time.October, 6, 1), Actor: "A", Items: []journal.Item{{Text: "乙?", Done: true}}},
		journal.Entry{ID: "x", Kind: journal.KindExec, At: tm(time.October, 7, 1), Actor: "A", Text: "t"},
	)
	j.seats.seats["m1"] = teamSeat("t1")
	_, p, raw := j.page("/api/journal?session_id=m1")
	if !strings.Contains(raw, `"pinned":[`) {
		t.Fatalf("no pinned[] on the page: %s", raw)
	}
	if !slices.Equal(ids(p.Pinned), ids(p.Confirms)) || !slices.Equal(ids(p.Confirms), []string{"ask"}) {
		t.Errorf("pinned = %v, confirms = %v", ids(p.Pinned), ids(p.Confirms))
	}
	for _, e := range p.Entries {
		if e.Pinned != (e.ID == "ask") {
			t.Errorf("entry %s pinned = %v", e.ID, e.Pinned)
		}
	}
	for _, e := range append(p.Pinned, p.Confirms...) {
		if !e.Pinned {
			t.Errorf("band entry %s has pinned=false", e.ID)
		}
	}
	w := httptest.NewRecorder()
	j.mux.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/journal/pin", strings.NewReader(`{"session_id":"m1","entry_id":"ask","pinned":true}`)))
	var got map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != http.StatusGone || got["error"] != "pin_removed" || got["detail"] != "釘選已移除，待確認會自動列在最上面" {
		t.Errorf("pin route: code %d body %s", w.Code, w.Body.String())
	}
}
