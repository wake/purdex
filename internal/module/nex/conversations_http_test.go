package nex

// GET /api/nex/conversations (Task 38; spec §13.5, §13.6, §13.9): the
// endpoint over a fixture projects root, the 5 s snapshot reuse, the single
// flight, the schedule and Stop.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"lab.protype.tw/wake/nexen/store"

	"github.com/wake/purdex/internal/conversations"
	"github.com/wake/purdex/internal/module/agent"
	pstore "github.com/wake/purdex/internal/store"
)

// --- fixtures ---

const (
	ceS1   = "c0000000-0000-4000-8000-0000000000e1"
	ceS2   = "c0000000-0000-4000-8000-0000000000e2"
	ceS3   = "c0000000-0000-4000-8000-0000000000e3"
	ceSlug = "-work-app"
)

// ceSID is the i-th generated session id.
func ceSID(i int) string { return fmt.Sprintf("d0000000-0000-4000-8000-%012d", i) }

// fakeConvIndex is an in-memory conversations.Index.
type fakeConvIndex struct {
	mu     sync.Mutex
	rows   map[string]pstore.ConversationIndexRow
	allErr error
}

func newFakeConvIndex() *fakeConvIndex {
	return &fakeConvIndex{rows: map[string]pstore.ConversationIndexRow{}}
}

func (f *fakeConvIndex) All(context.Context) ([]pstore.ConversationIndexRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.allErr != nil {
		return nil, f.allErr
	}
	out := make([]pstore.ConversationIndexRow, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out, nil
}

func (f *fakeConvIndex) UpsertBatch(_ context.Context, rows []pstore.ConversationIndexRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range rows {
		f.rows[r.SessionID] = r
	}
	return nil
}

func (f *fakeConvIndex) put(rows ...pstore.ConversationIndexRow) {
	_ = f.UpsertBatch(context.Background(), rows)
}

func (f *fakeConvIndex) setAllErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.allErr = err
}

var _ conversations.Index = (*fakeConvIndex)(nil)

// convClock is a settable clock for convNow.
type convClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *convClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *convClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// convEnv is a handoffEnv module with the conversation listing wired over a
// fixture projects root: New()'s seams (the real Scan, Lstat and Stat), an
// in-memory index, a settable clock, and a count of convScan calls.
type convEnv struct {
	*handoffEnv
	home, root string
	idx        *fakeConvIndex
	clock      *convClock
	logs       *logSink
	baseScan   convScanFunc // New()'s convScan, uncounted
	scans      atomic.Int32 // convScan calls
}

func newConvEnv(t *testing.T) *convEnv {
	t.Helper()
	he := newHandoffEnv(t)
	env := &convEnv{handoffEnv: he, idx: newFakeConvIndex(), clock: &convClock{t: time.UnixMilli(1_759_800_000_000)}}
	env.home = t.TempDir()
	env.root = filepath.Join(env.home, ".claude", "projects")
	require.NoError(t, os.MkdirAll(env.root, 0o755))
	env.logs = captureLogs(he)

	m := he.m
	// newHandoffEnv builds the module as a literal: take New()'s seams, so a
	// seam New() forgot to set fails every test here.
	d := New()
	m.convNow, m.convIsRegular, m.convDirExists, m.convScan = d.convNow, d.convIsRegular, d.convDirExists, d.convScan
	require.Same(t, m, m.WithConversationIndex(env.idx))
	m.convRoot, m.convHome = env.root, env.home
	m.convNow = env.clock.Now
	env.baseScan = m.convScan
	m.convScan = func(ctx context.Context, root string, idx conversations.Index, now func() time.Time) (conversations.ScanResult, error) {
		env.scans.Add(1)
		return env.baseScan(ctx, root, idx, now)
	}
	m.convCtx, m.convCancel = context.WithCancel(context.Background())
	t.Cleanup(func() { m.stopConversations(context.Background()) })
	return env
}

// gateScan parks every convScan call until gate closes, ignoring its ctx
// (as the real Scan does between two file boundaries); entered closes on the
// first call, which is counted on entry. The parked call then answers
// result(now), or runs the real Scan when result is nil.
func (e *convEnv) gateScan(result func(now func() time.Time) (conversations.ScanResult, error)) (entered, gate chan struct{}) {
	entered, gate = make(chan struct{}), make(chan struct{})
	var once sync.Once
	e.m.convScan = func(ctx context.Context, root string, idx conversations.Index, now func() time.Time) (conversations.ScanResult, error) {
		e.scans.Add(1)
		once.Do(func() { close(entered) })
		<-gate
		if result != nil {
			return result(now)
		}
		return e.baseScan(ctx, root, idx, now)
	}
	return entered, gate
}

// emptyScan is a successful scan of an empty root, whatever its ctx.
func emptyScan(now func() time.Time) (conversations.ScanResult, error) {
	return conversations.ScanResult{Present: map[string]conversations.Entry{}, ScannedAt: now().UnixMilli()}, nil
}

// waitFlightWaiters waits until the flight in progress has n waiters.
func (e *convEnv) waitFlightWaiters(t *testing.T, n int) {
	t.Helper()
	ceWaitFor(t, func() bool {
		e.m.convMu.Lock()
		defer e.m.convMu.Unlock()
		return e.m.convFlight != nil && e.m.convFlight.waiters >= n
	}, fmt.Sprintf("%d waiters on the flight", n))
}

// waitIdle waits until no flight is in progress.
func (e *convEnv) waitIdle(t *testing.T) {
	t.Helper()
	ceWaitFor(t, func() bool {
		e.m.convMu.Lock()
		defer e.m.convMu.Unlock()
		return e.m.convFlight == nil
	}, "the flight to end")
}

func (e *convEnv) cached() *convSnapshot {
	e.m.convMu.Lock()
	defer e.m.convMu.Unlock()
	return e.m.convCached
}

func (e *convEnv) setTerminalsErr(err error) {
	e.terminals.mu.Lock()
	defer e.terminals.mu.Unlock()
	e.terminals.err = err
}

func ceWaitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// setConvScanInterval shortens the schedule for one test.
func setConvScanInterval(t *testing.T, d time.Duration) {
	t.Helper()
	old := convScanInterval
	convScanInterval = d
	t.Cleanup(func() { convScanInterval = old })
}

// find returns every captured line containing sub.
func (s *logSink) find(sub string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, l := range s.lines {
		if strings.Contains(l, sub) {
			out = append(out, l)
		}
	}
	return out
}

// convResponse is the endpoint's body, a success or an error.
type convResponse struct {
	State         string            `json:"state"`
	ScannedAt     int64             `json:"scanned_at"`
	RootError     string            `json:"root_error"`
	Home          string            `json:"home"`
	Total         int               `json:"total"`
	Truncated     bool              `json:"truncated"`
	UnknownOwner  int               `json:"unknown_owner"`
	Conversations []conversationRow `json:"conversations"`
	Code          string            `json:"code"`
	Error         string            `json:"error"`

	raw map[string]json.RawMessage
}

// fetchConversations is GET RoutePrefix/conversations<query>, safe to call
// off the test goroutine.
func fetchConversations(base, query string) (int, convResponse, error) {
	resp, err := http.Get(base + RoutePrefix + "/conversations" + query)
	if err != nil {
		return 0, convResponse{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, convResponse{}, err
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		return 0, convResponse{}, fmt.Errorf("Content-Type = %q", ct)
	}
	var out convResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return 0, convResponse{}, err
	}
	if err := json.Unmarshal(body, &out.raw); err != nil {
		return 0, convResponse{}, err
	}
	return resp.StatusCode, out, nil
}

func getConversations(t *testing.T, base, query string) (int, convResponse) {
	t.Helper()
	status, res, err := fetchConversations(base, query)
	require.NoError(t, err)
	return status, res
}

func (e *convEnv) get(t *testing.T, query string) (int, convResponse) {
	t.Helper()
	return getConversations(t, e.srv.URL, query)
}

// ceLine encodes v as one JSONL line, '<' and '>' literal.
func ceLine(t *testing.T, v any) []byte {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	require.NoError(t, enc.Encode(v))
	return b.Bytes()
}

// cePrompt is a human prompt typed in an interactive Claude Code at cwd.
func cePrompt(cwd, text string) map[string]any {
	return map[string]any{"type": "user", "cwd": cwd, "entrypoint": "cli",
		"message": map[string]any{"role": "user", "content": text}}
}

func ceAITitle(sid, title string) map[string]any {
	return map[string]any{"type": "ai-title", "aiTitle": title, "sessionId": sid}
}

// cePadding is at least n bytes of assistant lines carrying nothing the
// scan looks for.
func cePadding(n int) []byte {
	line := `{"type":"assistant","pad":"` + strings.Repeat("x", 1000) + "\"}\n"
	var b bytes.Buffer
	for b.Len() < n {
		b.WriteString(line)
	}
	return b.Bytes()
}

// writeTranscript writes <root>/<ceSlug>/<sid>.jsonl with mtime at.
func (e *convEnv) writeTranscript(t *testing.T, sid string, at time.Time, parts ...[]byte) string {
	t.Helper()
	p := filepath.Join(e.root, ceSlug, sid+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, bytes.Join(parts, nil), 0o644))
	require.NoError(t, os.Chtimes(p, at, at))
	return p
}

// ceStint is an archived, terminated stint for sid whose transcript is at path.
func ceStint(id, sid, path string, updated int64) store.Execution {
	return store.Execution{ID: id, SessionID: sid, State: store.StateTerminated, ArchivedAt: updated,
		CreatedAt: updated, UpdatedAt: updated, TranscriptPath: path, EffectiveProfile: "default"}
}

// --- the endpoint ---

func TestConversationsHTTP_EndedThenGone(t *testing.T) {
	env := newConvEnv(t)
	work := t.TempDir()
	t1, t2 := time.UnixMilli(1_759_700_000_000), time.UnixMilli(1_759_700_100_000)
	p1 := env.writeTranscript(t, ceS1, t1,
		ceLine(t, cePrompt(work, "fix the login bug\nwith details")),
		ceLine(t, ceAITitle(ceS1, "Login fix")))
	env.writeTranscript(t, ceS2, t2, ceLine(t, cePrompt(filepath.Join(work, "removed"), "second")))

	status, res := env.get(t, "?state=ended")
	require.Equal(t, http.StatusOK, status, res.Error)
	assert.Equal(t, "ended", res.State)
	assert.Equal(t, env.home, res.Home)
	assert.Equal(t, env.clock.Now().UnixMilli(), res.ScannedAt)
	assert.NotContains(t, res.raw, "root_error", "omitted when the root was listed")
	assert.Equal(t, 2, res.Total)
	assert.False(t, res.Truncated)
	assert.Equal(t, 0, res.UnknownOwner)
	require.Equal(t, []string{ceS2, ceS1}, cvIDs(res.Conversations), "newest first")
	assert.Equal(t, conversationRow{SessionID: ceS1, Title: "Login fix", TitleSource: "ai",
		FirstPrompt: "fix the login bug\nwith details", Cwd: work, CwdExists: true,
		LastActivityAt: t1.UnixMilli(), LastIn: "terminal", TranscriptPath: p1}, res.Conversations[1])
	assert.False(t, res.Conversations[0].CwdExists, "a cwd that is gone")

	status, res = env.get(t, "?state=gone")
	require.Equal(t, http.StatusOK, status, res.Error)
	assert.Equal(t, "gone", res.State)
	assert.Equal(t, 0, res.Total)
	assert.JSONEq(t, "[]", string(res.raw["conversations"]), "an empty list is [], not null")
	assert.EqualValues(t, 1, env.scans.Load(), "both states come from one snapshot")

	// S1's transcript removed: the index remembers S1, so the next scan
	// finds it gone; S2 stays ended.
	require.NoError(t, os.Remove(p1))
	env.clock.Advance(6 * time.Second)
	_, res = env.get(t, "?state=gone")
	assert.Equal(t, []string{ceS1}, cvIDs(res.Conversations))
	assert.Equal(t, 1, res.Total)
	_, res = env.get(t, "?state=ended")
	assert.Equal(t, []string{ceS2}, cvIDs(res.Conversations))
}

func TestConversationsHTTP_BadState(t *testing.T) {
	env := newConvEnv(t)
	for _, q := range []string{"", "?state=", "?state=ENDED", "?state=running", "?State=ended"} {
		status, res := env.get(t, q)
		assert.Equal(t, http.StatusBadRequest, status, q)
		assert.Equal(t, "bad_state", res.Code, q)
	}
	assert.Zero(t, env.scans.Load(), "a bad request scans nothing")
}

// §13.9 "a title only before the tail window → fallback", end to end.
func TestConversationsHTTP_TitleBeforeTheTailWindowFallsBackToThePrompt(t *testing.T) {
	env := newConvEnv(t)
	env.writeTranscript(t, ceS1, time.UnixMilli(1_759_700_000_000),
		ceLine(t, cePrompt("/work/app", "refactor the scanner\nand its tests")),
		ceLine(t, ceAITitle(ceS1, "Scanner refactor")),
		cePadding(conversations.TailWindow+4096))

	_, res := env.get(t, "?state=ended")
	row := cvOne(t, res.Conversations)
	assert.Equal(t, "prompt", row.TitleSource)
	assert.Equal(t, "refactor the scanner", row.Title)
}

// R-4-1: a root that cannot be listed marks nothing gone; the indexed S
// stays ended and the response carries the error.
func TestConversationsHTTP_RootErrorKeepsIndexedEndedAndNothingGone(t *testing.T) {
	env := newConvEnv(t)
	env.writeTranscript(t, ceS1, time.UnixMilli(1_759_700_000_000), ceLine(t, cePrompt("/work/app", "hello")))
	_, res := env.get(t, "?state=ended")
	require.Equal(t, []string{ceS1}, cvIDs(res.Conversations))

	require.NoError(t, os.RemoveAll(env.root))
	env.clock.Advance(6 * time.Second)
	status, res := env.get(t, "?state=ended")
	require.Equal(t, http.StatusOK, status, res.Error)
	assert.Contains(t, res.RootError, "list projects root")
	assert.Equal(t, env.clock.Now().UnixMilli(), res.ScannedAt)
	assert.Equal(t, []string{ceS1}, cvIDs(res.Conversations))

	status, res = env.get(t, "?state=gone")
	require.Equal(t, http.StatusOK, status, res.Error)
	assert.NotEmpty(t, res.RootError)
	assert.Equal(t, 0, res.Total)
	assert.Len(t, env.logs.find("root_error="), 1)
}

func TestConversationsHTTP_FailuresAnswer503AndAreNotCached(t *testing.T) {
	cases := map[string]struct {
		fail, clear func(*convEnv)
		want        string
	}{
		"nexen list": {
			func(e *convEnv) { fakeStore(e.handoffEnv).listErr = errors.New("store closed") },
			func(e *convEnv) { fakeStore(e.handoffEnv).listErr = nil },
			"store closed",
		},
		"live sessions": {
			func(e *convEnv) { e.setTerminalsErr(errors.New("frames unreadable")) },
			func(e *convEnv) { e.setTerminalsErr(nil) },
			"frames unreadable",
		},
		"index": {
			func(e *convEnv) { e.idx.setAllErr(errors.New("index locked")) },
			func(e *convEnv) { e.idx.setAllErr(nil) },
			"index locked",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env := newConvEnv(t)
			env.writeTranscript(t, ceS1, time.UnixMilli(1_759_700_000_000), ceLine(t, cePrompt("/work/app", "hello")))
			tc.fail(env)
			status, res := env.get(t, "?state=ended")
			assert.Equal(t, http.StatusServiceUnavailable, status)
			assert.Equal(t, "conversations_unavailable", res.Code)
			assert.Contains(t, res.Error, tc.want)
			assert.Nil(t, env.cached())

			tc.clear(env)
			status, res = env.get(t, "?state=ended") // same clock: a failure is never reused
			require.Equal(t, http.StatusOK, status, res.Error)
			assert.Equal(t, []string{ceS1}, cvIDs(res.Conversations))
			assert.EqualValues(t, 2, env.scans.Load())
		})
	}
}

// The flight runs on its own goroutine: a panic there is recovered into a 503
// (the daemon stays up), logged with its stack; the client gets no stack.
func TestConversationsHTTP_AScanPanicIs503AndLogsItsStack(t *testing.T) {
	env := newConvEnv(t)
	env.m.convScan = func(context.Context, string, conversations.Index, func() time.Time) (conversations.ScanResult, error) {
		panic("scan blew up")
	}

	status, res := env.get(t, "?state=ended")
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, "conversations_unavailable", res.Code)
	assert.Contains(t, res.Error, "panic: scan blew up")
	assert.NotContains(t, res.Error, "goroutine ", "the stack stays in the log")
	assert.Nil(t, env.cached())
	lines := env.logs.find("nex: conversations: snapshot panicked: scan blew up")
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "goroutine ")
	assert.Contains(t, lines[0], "collectConversations")
}

func TestConversationsHTTP_NoIndexWiredAnswers503(t *testing.T) {
	env := newHandoffEnv(t) // the module was never given an index
	status, res := getConversations(t, env.srv.URL, "?state=ended")
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, "conversations_unavailable", res.Code)
	assert.NotEmpty(t, res.Error)
}

// Purdex routes are mounted when Init soft-failed and answer for themselves.
func TestConversationsHTTP_MountedWhenTheEngineSoftFailed(t *testing.T) {
	m := New().WithConversationIndex(newFakeConvIndex())
	m.logf = discardLogf
	m.initErr = errors.New("nex: init: assembling engine: boom")
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	status, res := getConversations(t, srv.URL, "?state=ended")
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, "conversations_unavailable", res.Code)
	assert.Contains(t, res.Error, "boom")
}

// §13.6: every execution, archived included, paged to the end without a
// page cap.
func TestConversationsHTTP_ListPagesEveryExecution(t *testing.T) {
	env := newConvEnv(t)
	fs := fakeStore(env.handoffEnv)
	for i := 1; i <= 1203; i++ {
		// Never written, so each S is gone: 1,203 rows prove 1,203 joined.
		fs.listRows = append(fs.listRows, ceStint(fmt.Sprintf("E%04d", i), ceSID(i), filepath.Join(env.root, ceSlug, ceSID(i)+".jsonl"), int64(i)))
	}

	status, res := env.get(t, "?state=gone")
	require.Equal(t, http.StatusOK, status, res.Error)
	assert.Equal(t, 1203, res.Total)
	require.Len(t, fs.allListOpts, 3)
	for i, cursor := range []string{"", "E0500", "E1000"} {
		assert.Equal(t, store.ListOptions{IncludeArchived: true, Limit: 500, Cursor: cursor}, fs.allListOpts[i])
	}
}

// Past the owner scan's 20-page cap: the listing has none.
func TestConversationsHTTP_ListHasNoPageCap(t *testing.T) {
	env := newConvEnv(t)
	fs := fakeStore(env.handoffEnv)
	const n = 20*500 + 1
	for i := 1; i <= n; i++ {
		fs.listRows = append(fs.listRows, ceStint(fmt.Sprintf("E%05d", i), ceSID(i), "", int64(i)))
	}

	status, res := env.get(t, "?state=gone")
	require.Equal(t, http.StatusOK, status, res.Error)
	assert.Equal(t, n, res.Total)
	assert.True(t, res.Truncated)
	assert.Len(t, fs.allListOpts, 21)
}

// repeatCursorStore answers every List page with one new row and the same
// NextCursor "C"; past five calls it errs, so a walk that never stops fails
// instead of hanging.
type repeatCursorStore struct {
	mu      sync.Mutex
	cursors []string
}

func (s *repeatCursorStore) Get(context.Context, string) (store.Execution, error) {
	return store.Execution{}, store.ErrNotFound
}

func (s *repeatCursorStore) List(_ context.Context, opts store.ListOptions) (store.ListPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cursors = append(s.cursors, opts.Cursor)
	n := len(s.cursors)
	if n > 5 {
		return store.ListPage{}, errors.New("the walk did not stop")
	}
	return store.ListPage{Items: []store.Execution{ceStint(fmt.Sprintf("E%d", n), ceSID(n), "", int64(n))}, NextCursor: "C"}, nil
}

// R-4-2 (coordinator ruling, 2026-10-07): a repeated cursor means the walk
// cannot see every execution, and a live one it missed would list a running
// worker as ended. The walk stops at the repeat and the snapshot fails closed
// (503, not cached); it never serves the rows it did see.
func TestConversationsHTTP_ARepeatedCursorFailsClosed(t *testing.T) {
	env := newConvEnv(t)
	env.writeTranscript(t, ceS1, time.UnixMilli(1_759_700_000_000), ceLine(t, cePrompt("/work/app", "hello")))
	rs := &repeatCursorStore{}
	env.m.sys.store = rs

	status, res := env.get(t, "?state=gone")
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, "conversations_unavailable", res.Code)
	assert.Contains(t, res.Error, `repeated cursor "C"`)
	assert.Empty(t, res.Conversations, "no partial list")
	assert.Equal(t, []string{"", "C"}, rs.cursors, "the walk stops at the repeat")
	assert.Nil(t, env.cached())
	assert.Len(t, env.logs.find(`repeated cursor "C"`), 1)

	// Not cached: once the store pages properly, the next request (same
	// clock) scans again and answers.
	env.m.sys.store = &fakeNexStore{}
	status, res = env.get(t, "?state=ended")
	require.Equal(t, http.StatusOK, status, res.Error)
	assert.Equal(t, []string{ceS1}, cvIDs(res.Conversations))
	assert.EqualValues(t, 2, env.scans.Load())
}

func TestConversationsHTTP_CapsEachStateAt2000NewestFirst(t *testing.T) {
	for _, n := range []int{conversationRowCap, conversationRowCap + 1} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			env := newConvEnv(t)
			present := map[string]conversations.Entry{}
			for i := 1; i <= n; i++ {
				env.idx.put(pstore.ConversationIndexRow{SessionID: ceSID(i), FirstEntrypoint: "cli", MtimeMs: int64(i)})
				present[ceSID(i)] = conversations.Entry{SessionID: ceSID(i), MtimeMs: int64(i)}
			}
			env.m.convScan = func(_ context.Context, _ string, _ conversations.Index, now func() time.Time) (conversations.ScanResult, error) {
				return conversations.ScanResult{Present: present, Files: n, ScannedAt: now().UnixMilli()}, nil
			}

			status, res := env.get(t, "?state=ended")
			require.Equal(t, http.StatusOK, status, res.Error)
			assert.Equal(t, n, res.Total)
			assert.Equal(t, n > conversationRowCap, res.Truncated)
			require.Len(t, res.Conversations, conversationRowCap)
			for i, r := range res.Conversations {
				require.EqualValues(t, n-i, r.LastActivityAt, "newest first, the oldest cut")
			}
		})
	}
}

// R-4-9: an S held only by an unverified frame is listed nowhere and counted.
func TestConversationsHTTP_UnknownOwnerIsCountedNotListed(t *testing.T) {
	env := newConvEnv(t)
	at := time.UnixMilli(1_759_700_000_000)
	env.writeTranscript(t, ceS1, at, ceLine(t, cePrompt("/work/app", "one")))
	env.writeTranscript(t, ceS2, at, ceLine(t, cePrompt("/work/app", "two")))
	env.writeTranscript(t, ceS3, at, ceLine(t, cePrompt("/work/app", "three")))
	env.terminals.live = map[string][]agent.TerminalSession{
		ceS1: {{FrameID: "F1", SessionID: ceS1, AgentType: "cc", Verified: false}},
		ceS2: {{FrameID: "F2", SessionID: ceS2, AgentType: "cc", Verified: true}},
	}

	_, res := env.get(t, "?state=ended")
	assert.Equal(t, []string{ceS3}, cvIDs(res.Conversations))
	assert.Equal(t, 1, res.UnknownOwner)
	_, res = env.get(t, "?state=gone")
	assert.Equal(t, 0, res.Total)
	assert.Equal(t, 1, res.UnknownOwner)
	assert.Len(t, env.logs.find("unknown_owner=1"), 1)
}

// The Addition: IsRegular is Lstat (a regular file, never a symlink) and
// DirExists is Stat, through New()'s defaults.
func TestConversationsHTTP_NewWiresTheStatSeams(t *testing.T) {
	d := New()
	assert.Nil(t, d.convIdx, "the index is off until wired")
	assert.NotNil(t, d.convScan)
	assert.NotNil(t, d.convIsRegular)
	assert.NotNil(t, d.convDirExists)
	require.NotNil(t, d.convNow)
	assert.WithinDuration(t, time.Now(), d.convNow(), time.Minute)

	env := newConvEnv(t)
	outside := t.TempDir()
	regular := filepath.Join(outside, "regular.jsonl")
	require.NoError(t, os.WriteFile(regular, []byte("{}\n"), 0o644))
	link := filepath.Join(outside, "link.jsonl")
	require.NoError(t, os.Symlink(regular, link))
	dir := filepath.Join(outside, "dir.jsonl")
	require.NoError(t, os.Mkdir(dir, 0o755))
	fakeStore(env.handoffEnv).listRows = []store.Execution{
		ceStint("E1", ceS1, regular, 3),
		ceStint("E2", ceS2, link, 2),
		ceStint("E3", ceS3, dir, 1),
	}

	_, res := env.get(t, "?state=ended")
	assert.Equal(t, []string{ceS1}, cvIDs(res.Conversations), "R-4-8: a stint's transcript outside the listing")
	_, res = env.get(t, "?state=gone")
	assert.Equal(t, []string{ceS2, ceS3}, cvIDs(res.Conversations), "a symlink or a dir is not a transcript")
}

// The Addition: one line per scan with files, re-read, bytes read, the
// unreadable dirs and the duration.
func TestConversationsHTTP_LogsEachScan(t *testing.T) {
	env := newConvEnv(t)
	at := time.UnixMilli(1_759_700_000_000)
	env.writeTranscript(t, ceS1, at, ceLine(t, cePrompt("/work/app", "one")))
	env.writeTranscript(t, ceS2, at, ceLine(t, cePrompt("/work/app", "two")))
	locked := filepath.Join(env.root, "-locked")
	require.NoError(t, os.Mkdir(locked, 0o755))
	require.NoError(t, os.Chmod(locked, 0))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	env.get(t, "?state=ended")
	env.clock.Advance(6 * time.Second)
	env.get(t, "?state=ended")

	lines := env.logs.find("nex: conversations: scan ")
	require.Len(t, lines, 2)
	dirs := regexp.QuoteMeta(fmt.Sprintf("%q", []string{locked}))
	first := regexp.MustCompile(`files=2 reread=2 bytes_read=(\d+) unreadable_dirs=1 ` + dirs + ` duration=\d`)
	m := first.FindStringSubmatch(lines[0])
	require.NotNil(t, m, lines[0])
	n, _ := strconv.Atoi(m[1])
	assert.Positive(t, n)
	assert.Regexp(t, `files=2 reread=0 bytes_read=0 unreadable_dirs=1 `+dirs+` duration=\d`, lines[1], "an unchanged file is not re-read")
	assert.NotContains(t, lines[0], "root_error")
}

// --- the snapshot ---

func TestConversationsHTTP_ReusesASnapshotUpTo5s(t *testing.T) {
	env := newConvEnv(t)
	env.writeTranscript(t, ceS1, time.UnixMilli(1_759_700_000_000), ceLine(t, cePrompt("/work/app", "hello")))

	_, first := env.get(t, "?state=ended")
	env.clock.Advance(4999 * time.Millisecond)
	_, again := env.get(t, "?state=gone")
	assert.EqualValues(t, 1, env.scans.Load(), "reused within 5 s")
	assert.Equal(t, first.ScannedAt, again.ScannedAt)

	env.clock.Advance(1001 * time.Millisecond)
	_, fresh := env.get(t, "?state=ended")
	assert.EqualValues(t, 2, env.scans.Load(), "rescanned after 6 s")
	assert.Equal(t, env.clock.Now().UnixMilli(), fresh.ScannedAt)
}

func TestConversationsHTTP_ConcurrentRequestsShareOneScan(t *testing.T) {
	env := newConvEnv(t)
	env.writeTranscript(t, ceS1, time.UnixMilli(1_759_700_000_000), ceLine(t, cePrompt("/work/app", "hello")))
	entered, gate := env.gateScan(nil)

	type answer struct {
		status int
		res    convResponse
		err    error
	}
	answers := make(chan answer, 2)
	for _, q := range []string{"?state=ended", "?state=gone"} {
		go func() {
			status, res, err := fetchConversations(env.srv.URL, q)
			answers <- answer{status, res, err}
		}()
	}
	waitClosed(t, entered, "the scan")
	env.waitFlightWaiters(t, 2)
	close(gate)

	var scannedAt []int64
	for range 2 {
		select {
		case a := <-answers:
			require.NoError(t, a.err)
			require.Equal(t, http.StatusOK, a.status, a.res.Error)
			scannedAt = append(scannedAt, a.res.ScannedAt)
		case <-time.After(3 * time.Second):
			t.Fatal("a request did not return")
		}
	}
	assert.EqualValues(t, 1, env.scans.Load())
	assert.Equal(t, scannedAt[0], scannedAt[1])
}

func TestConversationsHTTP_AWaiterThatGivesUpLeavesTheFlightRunning(t *testing.T) {
	env := newConvEnv(t)
	env.writeTranscript(t, ceS1, time.UnixMilli(1_759_700_000_000), ceLine(t, cePrompt("/work/app", "hello")))
	entered, gate := env.gateScan(nil)

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := env.m.conversationSnapshot(ctx)
		errc <- err
	}()
	waitClosed(t, entered, "the scan")
	cancel()
	select {
	case err := <-errc:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("the waiter did not return when its ctx ended")
	}

	close(gate)
	env.waitIdle(t)
	require.NotNil(t, env.cached(), "the flight completed and was cached")
	status, res := env.get(t, "?state=ended")
	require.Equal(t, http.StatusOK, status, res.Error)
	assert.Equal(t, []string{ceS1}, cvIDs(res.Conversations))
	assert.EqualValues(t, 1, env.scans.Load())
}

// --- the schedule and Stop ---

func TestConversationsHTTP_ScheduleScansAtStartThenOnEveryTick(t *testing.T) {
	env := newConvEnv(t)
	setConvScanInterval(t, 20*time.Millisecond)
	// Every reading of the clock is 10 s after the last, so no tick reuses
	// the previous snapshot.
	var reads atomic.Int64
	base := env.clock.Now()
	env.m.convNow = func() time.Time { return base.Add(time.Duration(reads.Add(1)) * 10 * time.Second) }

	require.NoError(t, env.m.Start(context.Background()))
	ceWaitFor(t, func() bool { return env.scans.Load() >= 3 }, "three scans")
	require.NoError(t, env.m.Stop(context.Background()))
	n := env.scans.Load()
	time.Sleep(60 * time.Millisecond)
	assert.Equal(t, n, env.scans.Load(), "no scan after Stop")
}

func TestConversationsHTTP_ScheduleScansOnceAtStart(t *testing.T) {
	env := newConvEnv(t)
	setConvScanInterval(t, time.Hour)
	require.NoError(t, env.m.Start(context.Background()))
	ceWaitFor(t, func() bool { return env.scans.Load() == 1 }, "the scan at start")
	env.waitIdle(t)
	require.NotNil(t, env.cached())
	time.Sleep(50 * time.Millisecond)
	assert.EqualValues(t, 1, env.scans.Load(), "the next one is 1 h away")
}

func TestConversationsHTTP_NoScheduleWithoutAnIndex(t *testing.T) {
	env := newConvEnv(t)
	env.m.convIdx = nil
	setConvScanInterval(t, 20*time.Millisecond)
	require.NoError(t, env.m.Start(context.Background()))
	time.Sleep(60 * time.Millisecond)
	assert.Zero(t, env.scans.Load())
}

func TestConversationsHTTP_StopIsBoundedAndNoEngineCallFollowsIt(t *testing.T) {
	env := newConvEnv(t)
	env.m.convStopCap = 50 * time.Millisecond
	fs := fakeStore(env.handoffEnv)
	fs.listRows = []store.Execution{ceStint("E1", ceS1, "", 1)}
	// The scan has read its last file when Stop cancels: it returns success,
	// and the flight must stop at its next boundary, before any List.
	entered, gate := env.gateScan(emptyScan)

	pending := make(chan int, 1)
	go func() {
		status, _, _ := fetchConversations(env.srv.URL, "?state=ended")
		pending <- status
	}()
	waitClosed(t, entered, "the scan")

	start := time.Now()
	require.NoError(t, env.m.Stop(context.Background()))
	assert.Less(t, time.Since(start), time.Second, "Stop waits for a running scan only up to its bound")
	assert.Len(t, env.logs.find("conversation scan still running after"), 1)

	status, res := env.get(t, "?state=ended")
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, "conversations_unavailable", res.Code)

	close(gate)
	env.waitIdle(t)
	assert.Zero(t, fs.ListCalls(), "no List after Stop returned")
	assert.Nil(t, env.cached())
	select {
	case status := <-pending:
		assert.Equal(t, http.StatusServiceUnavailable, status)
	case <-time.After(3 * time.Second):
		t.Fatal("the request waiting on the stopped flight did not return")
	}
}

// A Stop that lands while the walk is on a page: that page is the last (each
// page runs detached, so the walk itself must look at ctx between pages).
func TestConversationsHTTP_StopEndsTheListWalkAtAPageBoundary(t *testing.T) {
	env := newConvEnv(t)
	env.m.convStopCap = 50 * time.Millisecond
	fs := fakeStore(env.handoffEnv)
	for i := 1; i <= 600; i++ { // two pages
		fs.listRows = append(fs.listRows, ceStint(fmt.Sprintf("E%04d", i), ceSID(i), "", int64(i)))
	}
	entered := make(chan struct{})
	fs.listGate, fs.listEntered = make(chan struct{}), entered
	go func() { _, _ = env.m.conversationSnapshot(context.Background()) }()
	waitClosed(t, entered, "the first List page")

	require.NoError(t, env.m.Stop(context.Background()))
	close(fs.listGate)
	env.waitIdle(t)
	assert.Equal(t, 1, fs.ListCalls(), "no page starts after Stop")
	assert.Nil(t, env.cached())
}

func TestConversationsHTTP_StopIsBoundedByItsCtx(t *testing.T) {
	env := newConvEnv(t)
	entered, gate := env.gateScan(emptyScan)
	go func() { _, _ = env.m.conversationSnapshot(context.Background()) }()
	waitClosed(t, entered, "the scan")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	require.NoError(t, env.m.Stop(ctx))
	assert.Less(t, time.Since(start), time.Second)
	assert.Len(t, env.logs.find("conversation scan still running (context deadline exceeded)"), 1)
	close(gate)
	env.waitIdle(t)
}

// A Stop ctx that already expired (Q1's wait used it up) must not report a
// scan that is not running: select picks among ready cases at random.
func TestConversationsHTTP_StopWithNothingRunningLogsNothing(t *testing.T) {
	env := newConvEnv(t)
	setConvScanInterval(t, time.Hour)
	require.NoError(t, env.m.Start(context.Background()))
	ceWaitFor(t, func() bool { return env.scans.Load() == 1 }, "the scan at start")
	env.waitIdle(t) // the start scan is done; only the schedule goroutine is left
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range 20 {
		env.m.stopConversations(ctx)
	}
	assert.Empty(t, env.logs.find("conversation scan still running"))
}

// --- Init ---

func TestInit_ConversationsRootFromHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", launchdPath)
	cfg := baseConfig(t)
	m := New().WithConversationIndex(newFakeConvIndex())
	m.assemble = newFakeAssemble(&fakeAssembleRecord{}, noopEngine(), nil)
	m.logf = discardLogf

	require.NoError(t, m.Init(newTestCore(&cfg)))
	assert.Equal(t, filepath.Join(home, ".claude", "projects"), m.convRoot)
	assert.Equal(t, home, m.convHome)
	require.NotNil(t, m.convCtx)
	assert.NoError(t, m.convCtx.Err())
	require.NoError(t, m.Stop(context.Background()))
	assert.Error(t, m.convCtx.Err(), "Stop cancels the scans' context")
}

func TestInit_NoHomeLeavesConversationsOff(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("PATH", launchdPath)
	if _, err := os.UserHomeDir(); err == nil {
		t.Skip("os.UserHomeDir succeeds with HOME empty on this platform")
	}
	cfg := baseConfig(t)
	eng := noopEngine()
	eng.store = &fakeNexStore{}
	m := New().WithConversationIndex(newFakeConvIndex())
	m.assemble = newFakeAssemble(&fakeAssembleRecord{}, eng, nil)
	sink := &logSink{}
	m.logf = func(f string, a ...any) {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		sink.lines = append(sink.lines, fmt.Sprintf(f, a...))
	}

	require.NoError(t, m.Init(newTestCore(&cfg)))
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	assert.Empty(t, m.convRoot)
	assert.Len(t, sink.find("nex: conversations: no home directory"), 1)

	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	status, res := getConversations(t, srv.URL, "?state=ended")
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, "conversations_unavailable", res.Code)
	assert.Contains(t, res.Error, "home")
}
