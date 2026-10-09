package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/convfeed"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent"
)

const sid = "7e7f214b-c4e3-48cd-ab15-62a3471bd4fd"

// ---- transcript rows (the CC 2.1.292 shapes the convfeed tests use) ----

type obj = map[string]any

func at(s float64) string {
	return time.UnixMilli(1791378000000 + int64(s*1000)).UTC().Format("2006-01-02T15:04:05.000Z")
}

func row(o obj) string {
	b, _ := json.Marshal(o)
	return string(b)
}

func common(typ, uuid string, sec float64) obj {
	return obj{"type": typ, "uuid": uuid, "timestamp": at(sec), "isSidechain": false, "userType": "external",
		"entrypoint": "cli", "cwd": "/work/x", "sessionId": sid, "version": "2.1.292", "gitBranch": "HEAD"}
}

func userRow(uuid string, sec float64, text string) string {
	o := common("user", uuid, sec)
	o["message"] = obj{"role": "user", "content": text}
	o["origin"] = obj{"kind": "human"}
	o["promptSource"] = "typed"
	o["turnOrigin"] = "human"
	o["turnPosition"] = obj{"promptIndex": 0, "turnIndex": 0}
	return row(o)
}

func assistantRow(uuid string, sec float64, block obj) string {
	o := common("assistant", uuid, sec)
	o["message"] = obj{"model": "claude-opus-5-5", "id": "msg_x", "type": "message", "role": "assistant",
		"content": []obj{block}, "stop_reason": "end_turn"}
	o["apiBlockIndex"] = 0
	o["effort"] = "medium"
	o["perTurnEffort"] = "medium"
	return row(o)
}

func toolResultRow(uuid string, sec float64, toolUseID, text string) string {
	o := common("user", uuid, sec)
	o["message"] = obj{"role": "user", "content": []obj{{"type": "tool_result", "tool_use_id": toolUseID, "content": text}}}
	return row(o)
}

// idleTurns is n finished turns.
func idleTurns(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(userRow(fmt.Sprintf("u%d", i), float64(i*2), fmt.Sprintf("question %d", i)) + "\n")
		b.WriteString(assistantRow(fmt.Sprintf("a%d", i), float64(i*2+1), obj{"type": "text", "text": fmt.Sprintf("answer %d", i)}) + "\n")
	}
	return b.String()
}

// stepsTurn is one turn with n tool steps of `pad` bytes of output each.
func stepsTurn(n, pad int) string {
	var b strings.Builder
	b.WriteString(userRow("us", 0, "do many things") + "\n")
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("toolu_%03d", i)
		b.WriteString(assistantRow(fmt.Sprintf("t%d", i), float64(1+i*2), obj{"type": "tool_use", "id": id, "name": "Bash", "input": obj{"command": "echo " + id}}) + "\n")
		b.WriteString(toolResultRow(fmt.Sprintf("r%d", i), float64(2+i*2), id, strings.Repeat("x", pad)) + "\n")
	}
	b.WriteString(assistantRow("end", 999, obj{"type": "text", "text": "done"}) + "\n")
	return b.String()
}

// ---- fixtures ----

type fakeOwners struct {
	mu    sync.Mutex
	calls int
	own   []convfeed.Owner
	err   error
}

func (f *fakeOwners) LiveSessions(context.Context, string) ([]convfeed.Owner, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.own, f.err
}

type env struct {
	t      *testing.T
	home   string
	owners *fakeOwners
	mod    *Module
	mux    *http.ServeMux
}

func newEnv(t *testing.T) *env {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	owners := &fakeOwners{}
	c := core.New(core.CoreDeps{Config: &config.Config{HostID: "h1:abc"}})
	m := &Module{core: c, maxBody: maxBody,
		cache:    convfeed.NewCache(convfeed.CacheOptions{}),
		resolver: &convfeed.Resolver{Home: home, Owners: owners}}
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	return &env{t: t, home: home, owners: owners, mod: m, mux: mux}
}

func (e *env) transcript(content string) string {
	e.t.Helper()
	p := filepath.Join(e.home, ".claude", "projects", "-work-x", sid+".jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *env) get(url string) *httptest.ResponseRecorder {
	e.t.Helper()
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
	return w
}

type snapshot struct {
	Conversation struct {
		Key struct {
			HostID    string `json:"host_id"`
			Provider  string `json:"provider"`
			SessionID string `json:"session_id"`
		} `json:"key"`
		Title string `json:"title"`
		Turns []struct {
			Index        int `json:"index"`
			OmittedItems int `json:"omitted_items"`
			Items        []json.RawMessage
		} `json:"turns"`
	} `json:"conversation"`
	Header struct {
		Title   string `json:"title"`
		Status  string `json:"status"`
		Backend string `json:"backend"`
		Live    bool   `json:"live"`
		Usage   *struct {
			Model  string `json:"model"`
			Effort string `json:"effort"`
		} `json:"usage"`
	} `json:"header"`
	Window struct {
		FirstIndex    int  `json:"first_index"`
		LastIndex     int  `json:"last_index"`
		TotalTurns    int  `json:"total_turns"`
		HasMoreBefore bool `json:"has_more_before"`
	} `json:"window"`
	Cursor string `json:"cursor"`
}

func decode(t *testing.T, w *httptest.ResponseRecorder) snapshot {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var s snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatalf("%v: %s", err, w.Body.String())
	}
	return s
}

func errCode(t *testing.T, w *httptest.ResponseRecorder, status int) string {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body.String())
	}
	var e struct{ Error string }
	_ = json.Unmarshal(w.Body.Bytes(), &e)
	return e.Error
}

// ---- tests ----

func TestSnapshot_WindowHeaderCursorAndHostID(t *testing.T) {
	e := newEnv(t)
	e.transcript(idleTurns(5))
	s := decode(t, e.get("/api/conversations/claude/"+sid))
	if s.Conversation.Key.HostID != "h1:abc" || s.Conversation.Key.Provider != "claude" || s.Conversation.Key.SessionID != sid {
		t.Fatalf("key = %+v", s.Conversation.Key)
	}
	if len(s.Conversation.Turns) != 5 || s.Window.TotalTurns != 5 || s.Window.FirstIndex != 0 || s.Window.LastIndex != 4 || s.Window.HasMoreBefore {
		t.Fatalf("window = %+v turns %d", s.Window, len(s.Conversation.Turns))
	}
	if s.Cursor == "" || s.Header.Status != "ended" || s.Header.Live || s.Header.Backend != "" {
		t.Fatalf("header %+v cursor %q", s.Header, s.Cursor)
	}
	if s.Header.Usage == nil || s.Header.Usage.Model != "claude-opus-5-5" {
		t.Fatalf("usage = %+v", s.Header.Usage)
	}
	if s.Header.Title != s.Conversation.Title {
		t.Fatalf("title %q vs %q", s.Header.Title, s.Conversation.Title)
	}
	if _, rev, err := convfeed.ParseCursor(s.Cursor); err != nil || rev == 0 {
		t.Fatalf("cursor %q: rev %d err %v", s.Cursor, rev, err)
	}
}

func TestSnapshot_TurnsAndBeforePage(t *testing.T) {
	e := newEnv(t)
	e.transcript(idleTurns(10))
	s := decode(t, e.get("/api/conversations/claude/"+sid+"?turns=3"))
	if len(s.Conversation.Turns) != 3 || s.Window.FirstIndex != 7 || s.Window.LastIndex != 9 || !s.Window.HasMoreBefore {
		t.Fatalf("newest 3: %+v", s.Window)
	}
	s = decode(t, e.get("/api/conversations/claude/"+sid+"?turns=3&before=7"))
	if s.Window.FirstIndex != 4 || s.Window.LastIndex != 6 || !s.Window.HasMoreBefore {
		t.Fatalf("before 7: %+v", s.Window)
	}
	s = decode(t, e.get("/api/conversations/claude/"+sid+"?turns=3&before=2"))
	if s.Window.FirstIndex != 0 || s.Window.LastIndex != 1 || s.Window.HasMoreBefore {
		t.Fatalf("before 2: %+v", s.Window)
	}
	s = decode(t, e.get("/api/conversations/claude/"+sid+"?before=0"))
	if len(s.Conversation.Turns) != 0 || s.Conversation.Turns == nil {
		t.Fatalf("before 0 must be an empty list, got %+v", s.Conversation.Turns)
	}
	// the default is 20
	e.transcript(idleTurns(30))
	s = decode(t, e.get("/api/conversations/claude/"+sid))
	if len(s.Conversation.Turns) != 20 || s.Window.TotalTurns != 30 {
		t.Fatalf("default window = %d of %d", len(s.Conversation.Turns), s.Window.TotalTurns)
	}
}

// Every bad request is answered before the cache or any file is touched.
func TestValidation_BeforeAnyAccess(t *testing.T) {
	e := newEnv(t)
	e.transcript(idleTurns(2))
	cases := []struct {
		url    string
		status int
		code   string
	}{
		{"/api/conversations/codex/" + sid, 404, "provider_unsupported"},
		{"/api/conversations/claude/not-a-uuid", 400, "bad_session_id"},
		{"/api/conversations/claude/" + strings.ToUpper(sid), 400, "bad_session_id"},
		{"/api/conversations/claude/" + sid + "?turns=0", 400, "bad_turns"},
		{"/api/conversations/claude/" + sid + "?turns=201", 400, "bad_turns"},
		{"/api/conversations/claude/" + sid + "?turns=abc", 400, "bad_turns"},
		{"/api/conversations/claude/" + sid + "?before=-1", 400, "bad_before"},
		{"/api/conversations/claude/" + sid + "?before=x", 400, "bad_before"},
		{"/api/conversations/claude/" + sid + "?after=e:1", 501, "not_implemented"},
		{"/api/conversations/claude/" + sid + "?around=i1", 501, "not_implemented"},
	}
	for _, c := range cases {
		if got := errCode(t, e.get(c.url), c.status); got != c.code {
			t.Errorf("%s: code %q, want %q", c.url, got, c.code)
		}
	}
	if e.owners.calls != 0 || e.mod.cache.Len() != 0 {
		t.Fatalf("a rejected request reached the resolver (%d owner lookups) or the cache (%d entries)", e.owners.calls, e.mod.cache.Len())
	}
	if w := e.get("/api/conversations/claude/" + sid + "?turns=200"); w.Code != 200 {
		t.Fatalf("turns=200 must be accepted: %d", w.Code)
	}
}

func TestSnapshot_NotFound(t *testing.T) {
	e := newEnv(t)
	if got := errCode(t, e.get("/api/conversations/claude/"+sid), 404); got != "not_found" {
		t.Fatalf("code %q", got)
	}
}

func TestSnapshot_BusyWhenEveryEntryIsPinned(t *testing.T) {
	e := newEnv(t)
	e.transcript(idleTurns(1))
	e.mod.cache = convfeed.NewCache(convfeed.CacheOptions{Max: 1})
	_, release, err := e.mod.cache.Acquire(context.Background(), "someone-else")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if got := errCode(t, e.get("/api/conversations/claude/"+sid), 503); got != "busy" {
		t.Fatalf("code %q", got)
	}
}

func TestSnapshot_StatusLivePaneEndedAndUnknown(t *testing.T) {
	e := newEnv(t)
	p := e.transcript(idleTurns(1))

	e.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "running", SeenAt: 1}}
	s := decode(t, e.get("/api/conversations/claude/"+sid))
	if s.Header.Status != "running" || !s.Header.Live || s.Header.Backend != "terminal" {
		t.Fatalf("live pane: %+v", s.Header)
	}

	e.owners.own = nil
	s = decode(t, e.get("/api/conversations/claude/"+sid))
	if s.Header.Status != "ended" || s.Header.Live || s.Header.Backend != "" {
		t.Fatalf("no pane: %+v", s.Header)
	}

	e.owners.err = errors.New("tmux unreachable")
	s = decode(t, e.get("/api/conversations/claude/"+sid))
	if s.Header.Status != "unknown" || s.Header.Live || len(s.Conversation.Turns) != 1 {
		t.Fatalf("owner lookup failed: %+v (content must still be served)", s.Header)
	}
}

// The cap is on the encoded body: older turns go first, then the oldest items of the one turn that remains.
func TestSnapshot_BodyCapDropsTurnsThenItems(t *testing.T) {
	e := newEnv(t)
	e.mod.maxBody = 6000
	e.transcript(idleTurns(40))
	w := e.get("/api/conversations/claude/" + sid + "?turns=200")
	s := decode(t, w)
	if w.Body.Len() > e.mod.maxBody {
		t.Fatalf("body %d bytes over the cap %d", w.Body.Len(), e.mod.maxBody)
	}
	if len(s.Conversation.Turns) == 0 || len(s.Conversation.Turns) >= 40 || !s.Window.HasMoreBefore || s.Window.LastIndex != 39 {
		t.Fatalf("expected the newest turns only: %d turns, %+v", len(s.Conversation.Turns), s.Window)
	}

	e.transcript(stepsTurn(40, 300))
	w = e.get("/api/conversations/claude/" + sid)
	s = decode(t, w)
	if w.Body.Len() > e.mod.maxBody {
		t.Fatalf("one big turn: body %d bytes over the cap %d", w.Body.Len(), e.mod.maxBody)
	}
	if len(s.Conversation.Turns) != 1 || s.Conversation.Turns[0].OmittedItems == 0 {
		t.Fatalf("one turn with omitted_items expected: %+v", s.Conversation.Turns)
	}
}

func TestSnapshot_TooLargeEvenForOneEmptyTurnIsAnError(t *testing.T) {
	e := newEnv(t)
	e.mod.maxBody = 300 // smaller than the envelope itself
	e.transcript(idleTurns(2))
	if got := errCode(t, e.get("/api/conversations/claude/"+sid), 500); got != "too_large" {
		t.Fatalf("code %q", got)
	}
}

// A second request sees an appended row through the same cached entry: the cursor moves and the window grows.
func TestSnapshot_FollowsTheFileAndKeepsTheEpoch(t *testing.T) {
	e := newEnv(t)
	p := e.transcript(idleTurns(2))
	a := decode(t, e.get("/api/conversations/claude/"+sid))
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(userRow("u9", 50, "one more") + "\n")
	f.Close()
	b := decode(t, e.get("/api/conversations/claude/"+sid))
	if len(b.Conversation.Turns) != 3 || b.Window.TotalTurns != 3 {
		t.Fatalf("appended row not seen: %+v", b.Window)
	}
	ea, ra, _ := convfeed.ParseCursor(a.Cursor)
	eb, rb, _ := convfeed.ParseCursor(b.Cursor)
	if ea != eb || rb <= ra {
		t.Fatalf("cursors %q -> %q: same epoch and a later revision expected", a.Cursor, b.Cursor)
	}
}

func TestSnapshot_ConcurrentRequestsShareOneEntry(t *testing.T) {
	e := newEnv(t)
	e.transcript(idleTurns(20))
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := e.get("/api/conversations/claude/" + sid)
			if w.Code != 200 {
				t.Errorf("status %d", w.Code)
			}
		}()
	}
	wg.Wait()
	if e.mod.cache.Len() != 1 {
		t.Fatalf("cache holds %d entries, want 1", e.mod.cache.Len())
	}
}

// A finished request (any outcome) leaves nothing pinned.
func TestSnapshot_ReleasesItsPin(t *testing.T) {
	e := newEnv(t)
	e.transcript(idleTurns(1))
	e.mod.cache = convfeed.NewCache(convfeed.CacheOptions{Max: 1})
	for _, url := range []string{
		"/api/conversations/claude/" + sid,                               // served
		"/api/conversations/claude/" + sid + "?before=0",                 // served, empty window
		"/api/conversations/claude/0e7f214b-c4e3-48cd-ab15-62a3471bd4fd", // not found
	} {
		e.get(url)
		_, release, err := e.mod.cache.Acquire(context.Background(), "someone-else")
		if err != nil {
			t.Fatalf("after %s the only cache slot is still pinned: %v", url, err)
		}
		release()
	}
}

// A request queued behind a long read holds no file and leaves with its caller.
func TestSnapshot_QueuedRequestHoldsNothingAndLeavesWithItsContext(t *testing.T) {
	e := newEnv(t)
	e.transcript(idleTurns(1))
	entry, release, err := e.mod.cache.Acquire(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	held := make(chan struct{})
	unblock := make(chan struct{})
	go entry.Exclusive(context.Background(), func() error { close(held); <-unblock; return nil })
	<-held
	defer close(unblock)

	ctx, cancel := context.WithCancel(context.Background())
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		req := httptest.NewRequest("GET", "/api/conversations/claude/"+sid, nil).WithContext(ctx)
		e.mux.ServeHTTP(w, req)
		close(done)
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the queued request did not leave when its context ended")
	}
	if e.owners.calls != 0 {
		t.Fatalf("the queued request resolved the transcript (%d owner lookups): it would hold an open file while it waits", e.owners.calls)
	}
}

func TestModule_StartIsIdempotentAndStopWaitsForTheSweeper(t *testing.T) {
	e := newEnv(t)
	if err := e.mod.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.mod.Start(context.Background()); err != nil { // no second sweeper, no lost cancel
		t.Fatal(err)
	}
	e.mod.mu.Lock()
	done := e.mod.done
	e.mod.mu.Unlock()
	if err := e.mod.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	default:
		t.Fatal("Stop returned before the sweeper did")
	}
	if err := e.mod.Stop(context.Background()); err != nil { // stopping twice is fine
		t.Fatal(err)
	}
}

type fakePanes struct {
	panes []agent.PaneOwner
	err   error
}

func (f fakePanes) ConfirmedOwners(context.Context, string) ([]agent.PaneOwner, error) {
	return f.panes, f.err
}

func TestOwnerAdapter_MapsPanesToOwnersAndPassesErrors(t *testing.T) {
	a := ownerAdapter{fakePanes{panes: []agent.PaneOwner{{TranscriptPath: "/p/a.jsonl", Status: "waiting", LastSeenAt: 7, SessionID: sid}}}}
	got, err := a.LiveSessions(context.Background(), sid)
	if err != nil || len(got) != 1 || got[0] != (convfeed.Owner{TranscriptPath: "/p/a.jsonl", Status: "waiting", SeenAt: 7}) {
		t.Fatalf("got %+v err %v", got, err)
	}
	boom := errors.New("boom")
	if _, err := (ownerAdapter{fakePanes{err: boom}}).LiveSessions(context.Background(), sid); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

// A Stop that gave up waiting (its context ended) must not leave the module unable to start again.
func TestModule_StartWorksAfterATimedOutStop(t *testing.T) {
	e := newEnv(t)
	if err := e.mod.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = e.mod.Stop(ctx) // may return before the sweeper has (the context is already done)
	deadline := time.Now().Add(2 * time.Second)
	for {
		e.mod.mu.Lock()
		running := e.mod.cancel != nil
		e.mod.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the module still counts as running after its sweeper returned")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := e.mod.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.mod.mu.Lock()
	running := e.mod.cancel != nil
	e.mod.mu.Unlock()
	if !running {
		t.Fatal("Start after a timed-out Stop did not start the sweeper")
	}
	if err := e.mod.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
