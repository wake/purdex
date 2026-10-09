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
	mu     sync.Mutex
	calls  int
	own    []convfeed.Owner
	err    error
	onCall func() // runs inside every lookup (tests cancel the request there)
}

func (f *fakeOwners) LiveSessions(context.Context, string) ([]convfeed.Owner, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.onCall != nil {
		f.onCall()
	}
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
	m := &Module{core: c, maxBody: maxBody, subSem: make(chan struct{}, maxSubagentReads),
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
		{"/api/conversations/claude/" + sid + "?after=junk", 400, "bad_cursor"},
		{"/api/conversations/claude/" + sid + "?after=:5", 400, "bad_cursor"},
		{"/api/conversations/claude/" + sid + "?after=abc:x", 400, "bad_cursor"},
		{"/api/conversations/claude/" + sid + "?after=abc:1&turns=5", 400, "turns_and_after"},
		{"/api/conversations/claude/" + sid + "?after=abc:1&before=5", 400, "bad_query"},
		{"/api/conversations/claude/" + sid + "?after=abc:1&around=x", 400, "bad_query"},
		{"/api/conversations/claude/" + sid + "?around=x&before=3", 400, "before_and_around"},
		{"/api/conversations/claude/" + sid + "?around=", 400, "bad_around"},
		{"/api/conversations/claude/" + sid + "?around=" + strings.Repeat("a", 257), 400, "bad_around"},
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

// ---- subagents ----

const childID = "a7a639d97d57c6f43"

func (e *env) subagentFile(id, content string) string {
	e.t.Helper()
	p := filepath.Join(e.home, ".claude", "projects", "-work-x", sid, "subagents", "agent-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func childFixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../../testdata/conversation/v1/cc-transcript/subagent/children/" + childID + ".input.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type subagentResp struct {
	Items   []json.RawMessage `json:"items"`
	Partial bool              `json:"partial"`
}

func TestSubagent_ItemsOfTheChildFile(t *testing.T) {
	e := newEnv(t)
	e.transcript(idleTurns(1))
	e.subagentFile(childID, childFixture(t))
	w := e.get("/api/conversations/claude/" + sid + "/subagents/" + childID)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var got subagentResp
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../../testdata/conversation/v1/cc-transcript/subagent/children/" + childID + ".expected.json")
	if err != nil {
		t.Fatal(err)
	}
	var want struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) == 0 || len(got.Items) != len(want.Items) || got.Partial {
		t.Fatalf("items %d (want %d) partial %v", len(got.Items), len(want.Items), got.Partial)
	}
}

func TestSubagent_ValidationBeforeAnyAccessAndIdLengths(t *testing.T) {
	e := newEnv(t)
	e.transcript(idleTurns(1))
	long128 := "a" + strings.Repeat("b", 127)
	e.subagentFile(long128, "")
	for _, c := range []struct {
		url    string
		status int
		code   string
	}{
		{"/api/conversations/codex/" + sid + "/subagents/" + childID, 404, "provider_unsupported"},
		{"/api/conversations/claude/nope/subagents/" + childID, 400, "bad_session_id"},
		{"/api/conversations/claude/" + sid + "/subagents/" + long128 + "c", 400, "bad_agent_id"},
		{"/api/conversations/claude/" + sid + "/subagents/-a", 400, "bad_agent_id"},
		{"/api/conversations/claude/" + sid + "/subagents/a.b", 400, "bad_agent_id"},
		{"/api/conversations/claude/" + sid + "/subagents/..%2F..%2Fx", 400, "bad_agent_id"},
		{"/api/conversations/claude/" + sid + "/subagents/" + childID, 404, "not_found"}, // no such file
	} {
		if got := errCode(t, e.get(c.url), c.status); got != c.code {
			t.Errorf("%s: code %q, want %q", c.url, got, c.code)
		}
	}
	if e.owners.calls != 1 { // only the last, valid request got as far as the resolver
		t.Fatalf("%d owner lookups: invalid requests must not reach the resolver", e.owners.calls)
	}
	if w := e.get("/api/conversations/claude/" + sid + "/subagents/" + long128); w.Code != 200 {
		t.Fatalf("a 128-character id must be accepted: %d %s", w.Code, w.Body.String())
	}
}

// The agent file is opened by the descriptor-relative walk: a symlinked subagents directory is not followed.
func TestSubagent_SymlinkedDirectoryIsNotFollowed(t *testing.T) {
	e := newEnv(t)
	e.transcript(idleTurns(1))
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "agent-"+childID+".jsonl"), []byte(childFixture(t)), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(e.home, ".claude", "projects", "-work-x", sid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "subagents")); err != nil {
		t.Fatal(err)
	}
	if got := errCode(t, e.get("/api/conversations/claude/"+sid+"/subagents/"+childID), 404); got != "not_found" {
		t.Fatalf("code %q", got)
	}
}

func TestSubagent_AnswerIsCappedAndMarkedPartial(t *testing.T) {
	e := newEnv(t)
	e.mod.maxBody = 1000
	e.transcript(idleTurns(1))
	e.subagentFile(childID, childFixture(t))
	w := e.get("/api/conversations/claude/" + sid + "/subagents/" + childID)
	var got subagentResp
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || w.Body.Len() > e.mod.maxBody || !got.Partial {
		t.Fatalf("status %d body %d partial %v: the answer must fit the cap and say it is partial", w.Code, w.Body.Len(), got.Partial)
	}
}

func TestSubagent_BusyOnlyForAnOpenedFile(t *testing.T) {
	e := newEnv(t)
	e.transcript(idleTurns(1))
	e.subagentFile(childID, childFixture(t))
	for i := 0; i < maxSubagentReads; i++ {
		e.mod.subSem <- struct{}{}
	}
	// a session that does not exist is a plain 404: lookups never queue for the read slots
	if got := errCode(t, e.get("/api/conversations/claude/0e7f214b-c4e3-48cd-ab15-62a3471bd4fd/subagents/"+childID), 404); got != "not_found" {
		t.Fatalf("code %q", got)
	}
	if got := errCode(t, e.get("/api/conversations/claude/"+sid+"/subagents/"+childID), 503); got != "busy" {
		t.Fatalf("code %q", got)
	}
}

// The request ends while its file is being read: the parse stops, nothing is written, the slot is released.
func TestSubagent_CancelledDuringTheReadStopsAndReleasesItsSlot(t *testing.T) {
	e := newEnv(t)
	p := e.transcript(idleTurns(1))
	e.subagentFile(childID, childFixture(t)+strings.Repeat("{}\n", 40_000))
	ctx, cancel := context.WithCancel(context.Background())
	e.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "idle"}}
	e.owners.onCall = cancel // the pane lookup cancels the request: the file is found, then read on a dead context
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/conversations/claude/"+sid+"/subagents/"+childID, nil).WithContext(ctx))
	if w.Body.Len() != 0 {
		t.Fatalf("a cancelled request was answered: %d %s", w.Code, w.Body.String())
	}
	if len(e.mod.subSem) != 0 {
		t.Fatal("the read slot was not released")
	}
}

// An owner (or index) answer that names another transcript file must not choose the subagents directory.
func TestSubagent_TranscriptOfAnotherNameIsRefused(t *testing.T) {
	e := newEnv(t)
	other := filepath.Join(e.home, ".claude", "projects", "-work-y", "11111111-1111-4111-8111-111111111111.jsonl")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte(idleTurns(1)), 0o644); err != nil {
		t.Fatal(err)
	}
	// the subagent file exists under the *requested* session id in the other project's directory
	dir := filepath.Join(filepath.Dir(other), sid, "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent-"+childID+".jsonl"), []byte(childFixture(t)), 0o644); err != nil {
		t.Fatal(err)
	}
	e.owners.own = []convfeed.Owner{{TranscriptPath: other, Status: "idle"}}
	if got := errCode(t, e.get("/api/conversations/claude/"+sid+"/subagents/"+childID), 404); got != "not_found" {
		t.Fatalf("code %q", got)
	}
}

func TestCtxReader_StopsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := ctxReader{ctx: ctx, r: strings.NewReader("abcdef")}
	buf := make([]byte, 3)
	if n, err := r.Read(buf); n != 3 || err != nil {
		t.Fatalf("read before cancel: %d %v", n, err)
	}
	cancel()
	if n, err := r.Read(buf); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("read after cancel: %d %v, want context.Canceled", n, err)
	}
}

// ---- increments and around ----

type incResp struct {
	Reset   bool   `json:"reset"`
	Cursor  string `json:"cursor"`
	Changes []struct {
		Turn struct {
			ID    string `json:"id"`
			Index int    `json:"index"`
		} `json:"turn"`
		Items []map[string]any `json:"items"`
	} `json:"changes"`
	Header struct {
		Status string `json:"status"`
		Live   bool   `json:"live"`
	} `json:"header"`
	Conversation json.RawMessage `json:"conversation"`
}

func decodeInc(t *testing.T, w *httptest.ResponseRecorder) incResp {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var r incResp
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatalf("%v: %s", err, w.Body.String())
	}
	return r
}

func appendRows(t *testing.T, path string, rows ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, r := range rows {
		f.WriteString(r + "\n")
	}
}

func TestIncrement_OnlyWhatChangedAfterTheCursor(t *testing.T) {
	e := newEnv(t)
	p := e.transcript(idleTurns(3))
	snap := decode(t, e.get("/api/conversations/claude/"+sid))

	same := decodeInc(t, e.get("/api/conversations/claude/"+sid+"?after="+snap.Cursor))
	if same.Reset || len(same.Changes) != 0 || same.Cursor != snap.Cursor {
		t.Fatalf("nothing happened: %+v", same)
	}

	appendRows(t, p, userRow("u9", 60, "a new question"))
	inc := decodeInc(t, e.get("/api/conversations/claude/"+sid+"?after="+snap.Cursor))
	if inc.Reset || inc.Cursor == snap.Cursor || len(inc.Changes) == 0 {
		t.Fatalf("an appended row: %+v", inc)
	}
	last := inc.Changes[len(inc.Changes)-1]
	if last.Turn.Index != 3 || len(last.Items) == 0 || inc.Header.Status != "ended" {
		t.Fatalf("last change = %+v header %+v", last, inc.Header)
	}
	for _, c := range inc.Changes {
		if c.Turn.Index < 2 {
			t.Fatalf("turn %d changed? only the last settled turn and the new one may be reported", c.Turn.Index)
		}
		if c.Items == nil {
			t.Fatalf("items must be a list, not null")
		}
	}
	// from the new cursor again: quiet
	if again := decodeInc(t, e.get("/api/conversations/claude/"+sid+"?after="+inc.Cursor)); len(again.Changes) != 0 || again.Reset {
		t.Fatalf("after catching up: %+v", again)
	}
}

func TestIncrement_AHeaderOnlyChangeStillAnswers(t *testing.T) {
	e := newEnv(t)
	p := e.transcript(idleTurns(1))
	snap := decode(t, e.get("/api/conversations/claude/"+sid))
	e.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "running", SeenAt: 1}}
	inc := decodeInc(t, e.get("/api/conversations/claude/"+sid+"?after="+snap.Cursor))
	if inc.Reset || inc.Cursor == snap.Cursor || inc.Header.Status != "running" || !inc.Header.Live {
		t.Fatalf("a pane appeared: %+v", inc)
	}
}

func TestIncrement_StaleCursorIsAResetWithASnapshot(t *testing.T) {
	e := newEnv(t)
	e.transcript(idleTurns(3))
	snap := decode(t, e.get("/api/conversations/claude/"+sid))
	_, rev, _ := convfeed.ParseCursor(snap.Cursor)
	for name, cur := range map[string]string{
		"foreign epoch":   "ffffffffffffffff:1",
		"future revision": fmt.Sprintf("%s:%d", strings.SplitN(snap.Cursor, ":", 2)[0], rev+100),
	} {
		w := e.get("/api/conversations/claude/" + sid + "?after=" + cur)
		var r struct {
			Reset        bool `json:"reset"`
			Conversation struct {
				Turns []any `json:"turns"`
			} `json:"conversation"`
			Cursor string `json:"cursor"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil || w.Code != 200 {
			t.Fatalf("%s: %d %v %s", name, w.Code, err, w.Body.String())
		}
		if !r.Reset || len(r.Conversation.Turns) != 3 || r.Cursor != snap.Cursor {
			t.Fatalf("%s: %+v", name, r)
		}
	}
	// a fresh snapshot is not marked reset
	var plain map[string]any
	_ = json.Unmarshal(e.get("/api/conversations/claude/"+sid).Body.Bytes(), &plain)
	if _, has := plain["reset"]; has {
		t.Fatal("a plain snapshot must not carry reset")
	}
}

// A rewritten file starts a new epoch: the old cursor is stale.
func TestIncrement_RewrittenFileMakesTheCursorStale(t *testing.T) {
	e := newEnv(t)
	e.transcript(idleTurns(3))
	snap := decode(t, e.get("/api/conversations/claude/"+sid))
	e.transcript(idleTurns(2)) // shorter: the file shrank
	w := e.get("/api/conversations/claude/" + sid + "?after=" + snap.Cursor)
	var r struct {
		Reset bool `json:"reset"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &r)
	if w.Code != 200 || !r.Reset {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

// A catch-up that would pass the cap is a reset with a snapshot instead.
func TestIncrement_TooBigACatchUpBecomesAReset(t *testing.T) {
	e := newEnv(t)
	p := e.transcript(idleTurns(1))
	snap := decode(t, e.get("/api/conversations/claude/"+sid))
	e.mod.maxBody = 4000
	var rows []string
	for i := 0; i < 30; i++ {
		rows = append(rows, userRow(fmt.Sprintf("n%d", i), float64(100+i*2), strings.Repeat("padding ", 20)))
		rows = append(rows, assistantRow(fmt.Sprintf("m%d", i), float64(101+i*2), obj{"type": "text", "text": strings.Repeat("reply ", 20)}))
	}
	appendRows(t, p, rows...)
	w := e.get("/api/conversations/claude/" + sid + "?after=" + snap.Cursor)
	var r struct {
		Reset        bool `json:"reset"`
		Conversation struct {
			Turns []any `json:"turns"`
		} `json:"conversation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil || w.Code != 200 || !r.Reset || len(r.Conversation.Turns) == 0 {
		t.Fatalf("%d %v: %.300s", w.Code, err, w.Body.String())
	}
	if w.Body.Len() > e.mod.maxBody {
		t.Fatalf("the reset snapshot is %d bytes, over the cap %d", w.Body.Len(), e.mod.maxBody)
	}
}

func itemIDs(t *testing.T, w *httptest.ResponseRecorder) (ids []string, s snapshot) {
	t.Helper()
	s = decode(t, w)
	for _, tr := range s.Conversation.Turns {
		for _, it := range tr.Items {
			var x struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(it, &x)
			ids = append(ids, x.ID)
		}
	}
	return ids, s
}

func TestAround_CentresOnTheItemsTurnAndClampsAtTheEnds(t *testing.T) {
	e := newEnv(t)
	e.transcript(idleTurns(20))
	// ids of turn 10's items
	_, mid := itemIDs(t, e.get("/api/conversations/claude/"+sid+"?turns=1&before=11"))
	var one struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(mid.Conversation.Turns[0].Items[0], &one)
	if one.ID == "" || mid.Conversation.Turns[0].Index != 10 {
		t.Fatalf("setup: turn %+v item %q", mid.Conversation.Turns[0].Index, one.ID)
	}
	s := decode(t, e.get("/api/conversations/claude/"+sid+"?turns=5&around="+one.ID))
	if s.Window.FirstIndex != 8 || s.Window.LastIndex != 12 || !s.Window.HasMoreBefore {
		t.Fatalf("centred on turn 10: %+v", s.Window)
	}
	// near the start and near the end the window slides but keeps its size
	_, first := itemIDs(t, e.get("/api/conversations/claude/"+sid+"?turns=1&before=1"))
	var f struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(first.Conversation.Turns[0].Items[0], &f)
	s = decode(t, e.get("/api/conversations/claude/"+sid+"?turns=5&around="+f.ID))
	if s.Window.FirstIndex != 0 || s.Window.LastIndex != 4 || s.Window.HasMoreBefore {
		t.Fatalf("at the start: %+v", s.Window)
	}
	_, lastS := itemIDs(t, e.get("/api/conversations/claude/"+sid+"?turns=1"))
	var l struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(lastS.Conversation.Turns[0].Items[0], &l)
	s = decode(t, e.get("/api/conversations/claude/"+sid+"?turns=5&around="+l.ID))
	if s.Window.FirstIndex != 15 || s.Window.LastIndex != 19 {
		t.Fatalf("at the end: %+v", s.Window)
	}
	// an unknown item
	if got := errCode(t, e.get("/api/conversations/claude/"+sid+"?around=nope"), 404); got != "item_not_found" {
		t.Fatalf("code %q", got)
	}
}

// Under the size cap the target turn is never the one that drops out.
func TestAround_TheTargetSurvivesTheCap(t *testing.T) {
	e := newEnv(t)
	e.mod.maxBody = 6000
	e.transcript(idleTurns(40))
	_, early := itemIDs(t, e.get("/api/conversations/claude/"+sid+"?turns=1&before=3"))
	var x struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(early.Conversation.Turns[0].Items[0], &x)
	w := e.get("/api/conversations/claude/" + sid + "?turns=200&around=" + x.ID)
	s := decode(t, w)
	if w.Body.Len() > e.mod.maxBody {
		t.Fatalf("body %d over cap %d", w.Body.Len(), e.mod.maxBody)
	}
	if s.Window.FirstIndex > 2 || s.Window.LastIndex < 2 {
		t.Fatalf("turn 2 is not in the window %+v", s.Window)
	}
}

// The item exists but its turn is over the cap and the item is among the dropped oldest: say so, never a window
// that silently lacks what was asked for.
func TestAround_AnItemDroppedByTheCapIsAnExplicitError(t *testing.T) {
	e := newEnv(t)
	e.mod.maxBody = 6000
	e.transcript(stepsTurn(40, 300))
	_, s := itemIDs(t, e.get("/api/conversations/claude/"+sid+"?turns=1"))
	if s.Conversation.Turns[0].OmittedItems == 0 {
		t.Fatal("setup: the turn must be over the cap")
	}
	// the very first item of the turn (the user message) is the oldest: it was dropped
	e.mod.maxBody = maxBody
	_, full := itemIDs(t, e.get("/api/conversations/claude/"+sid))
	var first struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(full.Conversation.Turns[0].Items[0], &first)
	e.mod.maxBody = 6000
	if got := errCode(t, e.get("/api/conversations/claude/"+sid+"?around="+first.ID), 422); got != "item_not_shown" {
		t.Fatalf("code %q", got)
	}
}
