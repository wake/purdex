package workbook

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// WB-2b-i (b): the Mac's refresh route, refresh_available on the conversation answer and its host event.

func (e *apiEnv) post(url string) *httptest.ResponseRecorder {
	e.t.Helper()
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, url, nil))
	return w
}

// withEngine gives the module an engine whose refresh-capable sessions are the ones listed (the module's own Start needs
// the whole daemon).
func (e *apiEnv) withEngine(refresh *[]CapSession) *Engine {
	e.t.Helper()
	eng := NewEngine(Deps{Store: e.store, Lineage: e.roots, HostID: "h1", Logf: func(string, ...any) {},
		RefreshSessions: func() []CapSession { return *refresh }})
	e.m.mu.Lock()
	e.m.engine = eng
	e.m.mu.Unlock()
	return eng
}

func TestAPI_RefreshRoute(t *testing.T) {
	e := newAPI(t)
	var live []CapSession
	e.withEngine(&live)
	url := "/api/workbook/conversations/claude/s1/refresh"
	errCode(t, e.post(url), 409, "not_live")
	errCode(t, e.post("/api/workbook/conversations/codex/s1/refresh"), 404, "not_found")

	live = []CapSession{{SID: "s1", At: time.Now()}}
	w := e.post(url)
	var body struct {
		EntryID int64 `json:"entry_id"`
	}
	if w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.EntryID == 0 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if en, err := e.store.Entry(body.EntryID); err != nil || en.Kind != KindRefresh || en.State != StatePending {
		t.Fatalf("entry = %+v %v", en, err)
	}
	errCode(t, e.post(url), 409, "refresh_pending")
	// the row travels as a workbook.entry event
	found := false
	for _, ev := range e.sent() {
		if ev.typ == "workbook.entry" && strings.Contains(ev.value, `"kind":"refresh"`) {
			found = true
		}
	}
	if !found {
		t.Fatal("no workbook.entry event for the refresh row")
	}
}

// With the module off (no engine) the route is a 503; a GET is not allowed on it.
func TestAPI_RefreshRouteWithoutAnEngine(t *testing.T) {
	e := newAPI(t)
	errCode(t, e.post("/api/workbook/conversations/claude/s1/refresh"), 503, "unavailable")
	if w := e.get("/api/workbook/conversations/claude/s1/refresh"); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", w.Code)
	}
}

// The conversation answer carries refresh_available, computed on read, per conversation.
func TestAPI_ConversationRefreshAvailable(t *testing.T) {
	e := newAPI(t)
	e.roots.root["s1"] = "c"
	e.roots.root["s2"] = "c"
	mustInsert(t, e.store, pending("c", "s1", "a", 1))
	var live []CapSession
	e.withEngine(&live)
	avail := func() bool {
		var b struct {
			Avail *bool `json:"refresh_available"`
		}
		w := e.get("/api/workbook/conversations/claude/s1")
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil || b.Avail == nil {
			t.Fatalf("%v %s", err, w.Body.String())
		}
		return *b.Avail
	}
	if avail() {
		t.Fatal("available with no live session")
	}
	live = []CapSession{{SID: "s2", At: time.Now()}} // another session of the same conversation
	if !avail() {
		t.Fatal("not available with a live session of the conversation")
	}
	live = []CapSession{{SID: "elsewhere", At: time.Now()}}
	if avail() {
		t.Fatal("another conversation's session counted")
	}
}

// workbook.refresh_available goes out once per change, as a JSON string value.
func TestAPI_RefreshAvailableEvent(t *testing.T) {
	e := newAPI(t)
	e.m.announceAvailability([]AvailabilityChange{{ConvKey: "c", Available: true}, {ConvKey: "d", Available: false}})
	got := e.sent()
	if len(got) != 2 || got[0].typ != "workbook.refresh_available" || got[0].value != `{"conv_key":"c","available":true}` ||
		got[1].value != `{"conv_key":"d","available":false}` {
		t.Fatalf("events = %+v", got)
	}
}
