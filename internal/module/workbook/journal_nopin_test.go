package workbook

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/journal"
)

// Pinning is gone from the wire (#3155): a page carries confirms[] only, no entry carries a pinned flag, and the pin route is
// not registered. A file written while pins existed (`"pinned":true` in an entry's meta) still parses, and its entry is served
// like any other, without the field.
// Mutation gate: put pinned[] back on the page -> red; put the flag back on an entry -> red; register any handler on the pin
// route -> red; make the parser reject unknown meta fields -> red.
func TestJournalAPI_NoPinnedOnTheWireAndPinRouteIsNotRegistered(t *testing.T) {
	j := newJournalAPI(t)
	withLead(j, "t1", "lead-1")
	u := j.unit("team:t1", "日誌")
	j.write(u,
		journal.Entry{ID: "ask", Kind: journal.KindConfirm, At: tm(time.October, 5, 1), Actor: "A", Items: []journal.Item{{Text: "甲?"}}},
		journal.Entry{ID: "x", Kind: journal.KindExec, At: tm(time.October, 7, 1), Actor: "A", Text: "t"},
	)
	// A file from before pins were removed: the meta line carries "pinned":true.
	oldAt := tm(time.October, 1, 1)
	oldFile := journal.MonthFile(unitHome(u).journalDir, oldAt)
	old := "## 2026-10-01\n\n### 10:00 · 待確認 📌\n- [ ] q\n" +
		`<!-- j {"id":"old","kind":"confirm","at":"` + oldAt.UTC().Format(time.RFC3339) + `","items":[{"text":"q"}],"pinned":true} -->` + "\n"
	if prev, err := os.ReadFile(oldFile); err == nil {
		old = string(prev) + "\n" + old
	}
	if err := os.MkdirAll(filepath.Dir(oldFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldFile, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	j.seats.seats["m1"] = teamSeat("t1")
	_, p, raw := j.page("/api/journal?session_id=m1")
	if strings.Contains(raw, `"pinned"`) {
		t.Errorf("pinned on the wire: %s", raw)
	}
	if !slices.Contains(ids(p.Confirms), "old") || !slices.Contains(ids(p.Confirms), "ask") {
		t.Errorf("confirms = %v, want both the new entry and the one from the old file", ids(p.Confirms))
	}
	if code, out := j.request(http.MethodPut, "/api/journal/pin", map[string]any{"session_id": "m1", "entry_id": "ask", "pinned": true}); code != http.StatusNotFound {
		t.Errorf("pin route: code %d body %s, want 404", code, out)
	}
}
