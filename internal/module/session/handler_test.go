package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// --- Provider method tests ---

func TestListSessionsMergesMeta(t *testing.T) {
	mod, meta, fake := newTestModule(t)

	fake.AddSession("dev", "/home/dev")
	fake.AddSession("prod", "/home/prod")

	// Set meta for first session only
	require.NoError(t, meta.SetMeta("$0", store.SessionMeta{
		TmuxID: "$0",
		Mode:   "terminal",
	}))

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	assert.Len(t, sessions, 2)

	// First session should have merged meta
	assert.Equal(t, "dev", sessions[0].Name)
	assert.Equal(t, "terminal", sessions[0].Mode)
	assert.NotEmpty(t, sessions[0].Code)

	// Second session should have default mode
	assert.Equal(t, "prod", sessions[1].Name)
	assert.Equal(t, "terminal", sessions[1].Mode)
	assert.NotEmpty(t, sessions[1].Code)
}

func TestListSessionsIncludesPaneTitleMetadata(t *testing.T) {
	mod, _, fake := newTestModule(t)

	fake.AddSession("dev", "/home/dev")
	fake.SetActivePaneMetadata("dev", tmux.TmuxPaneMetadata{
		SessionID:          "$0",
		SessionName:        "dev",
		WindowID:           "@1",
		PaneID:             "%2",
		PaneTitle:          "Planning",
		WindowName:         "editor",
		PaneCurrentCommand: "claude",
	})

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, "Planning", sessions[0].PaneTitle)
	assert.Equal(t, "editor", sessions[0].WindowName)
	assert.Equal(t, "claude", sessions[0].CurrentCommand)
}

func TestListSessionsContinuesWhenPaneMetadataFails(t *testing.T) {
	mod, _, fake := newTestModule(t)

	fake.AddSession("dev", "/home/dev")
	fake.SetActivePaneMetadataError("dev", errors.New("display-message failed"))

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, "dev", sessions[0].Name)
	assert.Empty(t, sessions[0].PaneTitle)
	assert.Empty(t, sessions[0].WindowName)
	assert.Empty(t, sessions[0].CurrentCommand)
}

func TestListSessionsUsesActivePaneTitleForMultiPaneSession(t *testing.T) {
	mod, _, fake := newTestModule(t)

	fake.AddSession("multi", "/workspace")
	fake.SetPaneCommand("multi:0.0", "wrong-pane-command")
	fake.SetActivePaneMetadata("multi", tmux.TmuxPaneMetadata{
		SessionID:          "$0",
		SessionName:        "multi",
		WindowID:           "@active",
		PaneID:             "%active",
		PaneTitle:          "active pane title",
		WindowName:         "active window",
		PaneCurrentCommand: "active-command",
	})

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, "active pane title", sessions[0].PaneTitle)
	assert.Equal(t, "active window", sessions[0].WindowName)
	assert.Equal(t, "active-command", sessions[0].CurrentCommand)
	assert.Zero(t, fake.PaneCommandCallCount("multi:0.0"), "session list should not merge arbitrary list-panes data")
}

func TestListSessionsCleansOrphans(t *testing.T) {
	mod, meta, fake := newTestModule(t)

	fake.AddSession("alive", "/tmp")

	// Create orphan meta for a session that doesn't exist in tmux
	require.NoError(t, meta.SetMeta("$99", store.SessionMeta{
		TmuxID: "$99",
		Mode:   "terminal",
	}))

	// ListSessions triggers orphan cleanup
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	assert.Len(t, sessions, 1)

	// Orphan should be cleaned
	orphan, err := meta.GetMeta("$99")
	require.NoError(t, err)
	assert.Nil(t, orphan, "orphan meta should be deleted")
}

func TestGetSessionByCode(t *testing.T) {
	mod, _, fake := newTestModule(t)

	fake.AddSession("my-session", "/home/test")

	// Get the code from ListSessions
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	code := sessions[0].Code

	info, err := mod.GetSession(code)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "my-session", info.Name)
	assert.Equal(t, code, info.Code)
}

func TestGetSessionNotFound(t *testing.T) {
	mod, _, _ := newTestModule(t)

	info, err := mod.GetSession("zzzzzz")
	require.NoError(t, err)
	assert.Nil(t, info)
}

func TestUpdateMeta(t *testing.T) {
	mod, meta, fake := newTestModule(t)

	fake.AddSession("work", "/home/work")

	// Ensure meta exists first
	require.NoError(t, meta.SetMeta("$0", store.SessionMeta{
		TmuxID: "$0",
		Mode:   "terminal",
	}))

	// Get code
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	code := sessions[0].Code

	// Update meta via provider. Mode is always "terminal" since P-D.2, so
	// Cwd carries the distinguishing value that proves the partial update
	// actually reached the store.
	mode := "terminal"
	cwd := "/home/work/moved"
	err = mod.UpdateMeta(code, MetaUpdate{Mode: &mode, Cwd: &cwd})
	require.NoError(t, err)

	// Verify persisted
	stored, err := meta.GetMeta("$0")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "terminal", stored.Mode)
	assert.Equal(t, "/home/work/moved", stored.Cwd)
}

// --- HTTP handler tests ---

func TestHandlerListSessions(t *testing.T) {
	mod, meta, fake := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("alpha", "/tmp/alpha")
	fake.AddSession("beta", "/tmp/beta")

	// Set meta on first
	require.NoError(t, meta.SetMeta("$0", store.SessionMeta{
		TmuxID: "$0",
		Mode:   "terminal",
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var sessions []SessionInfo
	err := json.NewDecoder(w.Body).Decode(&sessions)
	require.NoError(t, err)
	assert.Len(t, sessions, 2)
	assert.Equal(t, "alpha", sessions[0].Name)
	assert.Equal(t, "terminal", sessions[0].Mode)
}

func TestHandlerListSessionsEmpty(t *testing.T) {
	mod, _, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Should be [], not null
	body := strings.TrimSpace(w.Body.String())
	assert.Equal(t, "[]", body)
}

// getList runs GET <path> through mux and returns the recorder.
func getList(t *testing.T, mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// getFresh runs GET /api/sessions?fresh=1 and decodes the envelope.
func getFresh(t *testing.T, mux *http.ServeMux) VersionedSessions {
	t.Helper()
	w := getList(t, mux, "/api/sessions?fresh=1")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var v VersionedSessions
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
	return v
}

func sessionNames(list []SessionInfo) []string {
	names := make([]string, 0, len(list))
	for _, s := range list {
		names = append(names, s.Name)
	}
	return names
}

func TestHandlerListSessionsFresh_Envelope(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	fake.AddSession("alpha", "/tmp/alpha")

	w := getList(t, mux, "/api/sessions?fresh=1")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	require.Contains(t, raw, "epoch")
	require.Contains(t, raw, "seq")
	require.Contains(t, raw, "sessions")

	var v VersionedSessions
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
	assert.Equal(t, mod.epoch, v.Epoch)
	assert.GreaterOrEqual(t, v.Seq, uint64(1))
	assert.Equal(t, []string{"alpha"}, sessionNames(v.Sessions))
}

func TestHandlerListSessionsFresh_EmptyIsArray(t *testing.T) {
	mod, _, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	w := getList(t, mux, "/api/sessions?fresh=1")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"sessions":[]`)
}

// A warm plain-GET cache must not answer ?fresh=1 (spec §3.1).
func TestHandlerListSessionsFresh_BypassesWarmCache(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	fake.AddSession("alpha", "/tmp")

	require.Equal(t, http.StatusOK, getList(t, mux, "/api/sessions").Code) // warm
	fake.AddSession("beta", "/tmp")                                         // inside the TTL

	v := getFresh(t, mux)
	assert.Equal(t, []string{"alpha", "beta"}, sessionNames(v.Sessions))
}

// Only the exact value fresh=1 selects the envelope; everything else keeps
// today's bare array.
func TestHandlerListSessionsFresh_OtherValuesStayArray(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	fake.AddSession("alpha", "/tmp")

	for _, path := range []string{"/api/sessions", "/api/sessions?fresh=0", "/api/sessions?fresh=true", "/api/sessions?fresh="} {
		w := getList(t, mux, path)
		require.Equal(t, http.StatusOK, w.Code, path)
		var arr []SessionInfo
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &arr), "%s must answer a bare array", path)
		assert.Len(t, arr, 1, path)
	}
}

func TestHandlerListSessionsFresh_TmuxErrorIs500(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmux = &listFailingExecutor{Executor: fake, err: errors.New("tmux list exploded")}
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	w := getList(t, mux, "/api/sessions?fresh=1")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "tmux list exploded")
}

func TestHandlerGetSession(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("target", "/tmp/target")

	// First get the code
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	code := sessions[0].Code

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+code, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var info SessionInfo
	err = json.NewDecoder(w.Body).Decode(&info)
	require.NoError(t, err)
	assert.Equal(t, "target", info.Name)
	assert.Equal(t, code, info.Code)
}

func TestHandlerGetSessionNotFound(t *testing.T) {
	mod, _, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/zzzzzz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandlerCreateSession(t *testing.T) {
	mod, _, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	body := `{"name": "new-session", "cwd": "/tmp"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var info SessionInfo
	err := json.NewDecoder(w.Body).Decode(&info)
	require.NoError(t, err)
	assert.Equal(t, "new-session", info.Name)
	assert.Equal(t, "terminal", info.Mode)
	assert.NotEmpty(t, info.Code)

	// Verify session exists via ListSessions
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
}

// TestHandlerCreateSession_StampsTmuxInstance guards the create response, which
// builds its SessionInfo by hand rather than going through ListSessions. The
// rebuild engine re-points a pane using the generation in this very response
// (spec v4 §4.8 step 4), so an unstamped create leaves the rebuilt pane with an
// unknown generation until the next sessions broadcast.
func TestHandlerCreateSession_StampsTmuxInstance(t *testing.T) {
	mod, _, _ := newTestModule(t)
	mod.tmuxInstanceFn = func() string { return "4471:1788740000" }
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	body := `{"name": "stamped", "cwd": "/tmp"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)

	var info SessionInfo
	require.NoError(t, json.NewDecoder(w.Body).Decode(&info))
	assert.Equal(t, "4471:1788740000", info.TmuxInstance)
}

// TestHandlerCreateSession_RecordsTmuxCwdNotRequestedCwd pins the create path
// to the truth rather than to the request.
//
// resolveCwd stats the directory, and then tmux is invoked — a window in which
// the directory can go away. tmux does not fail on an unusable -c, it silently
// starts the session in $HOME, so the cwd the session is really in can differ
// from the one that was asked for. `#{session_path}` is the only witness of
// which one it is, and both the stored meta and the response must carry it.
func TestHandlerCreateSession_RecordsTmuxCwdNotRequestedCwd(t *testing.T) {
	mod, meta, fake := newTestModule(t)
	// tmux ignored -c and started the session somewhere else.
	fake.ForceNewSessionCwd = "/"
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	body := `{"name": "raced", "cwd": "/tmp"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)

	var info SessionInfo
	require.NoError(t, json.NewDecoder(w.Body).Decode(&info))
	assert.Equal(t, "/", info.Cwd, "response must report the directory tmux used")

	stored, err := meta.GetMeta("$0")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "/", stored.Cwd, "stored meta must record the directory tmux used")
}

// TestHandlerCreateSession_RecordsRequestedCwdWhenTmuxAgrees is the other half:
// in the ordinary case tmux honours -c, so nothing about the recorded cwd moves.
func TestHandlerCreateSession_RecordsRequestedCwdWhenTmuxAgrees(t *testing.T) {
	mod, meta, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	body := `{"name": "honoured", "cwd": "/tmp"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)

	var info SessionInfo
	require.NoError(t, json.NewDecoder(w.Body).Decode(&info))
	assert.Equal(t, "/tmp", info.Cwd)

	stored, err := meta.GetMeta("$0")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "/tmp", stored.Cwd)
}

// captureStdLog points the standard logger at a buffer for the duration of the
// test and hands back a reader for whatever was written to it. The warning
// under test goes through the package-level logger, so this is the only seam.
func captureStdLog(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return buf.String
}

// TestHandlerCreateSession_SilentWhenCwdOnlyCanonicalised guards the warning
// against its own false positives.
//
// tmux's `#{session_path}` comes from getcwd(), which resolves symlinks and
// filesystem case, so a wholly successful create routinely reports a different
// *string* than was asked for (/tmp → /private/tmp on macOS, and any symlinked
// worktree the same way). Warning on those would bury the one case the warning
// exists for.
func TestHandlerCreateSession_SilentWhenCwdOnlyCanonicalised(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	require.NoError(t, os.Mkdir(realDir, 0o755))
	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(realDir, link))
	// The session was asked for via the symlink; getcwd() reports the target.
	fake.ForceNewSessionCwd = realDir

	logged := captureStdLog(t)

	body := fmt.Sprintf(`{"name": "canonical", "cwd": %q}`, link)
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)

	var info SessionInfo
	require.NoError(t, json.NewDecoder(w.Body).Decode(&info))
	assert.Equal(t, realDir, info.Cwd, "the canonical path is still what gets recorded")
	assert.Empty(t, logged(), "canonicalisation is not a divergence and must not warn")
}

// TestHandlerCreateSession_WarnsWhenCwdTrulyDiverges is the case the warning is
// for: tmux did not land in the requested directory at all.
func TestHandlerCreateSession_WarnsWhenCwdTrulyDiverges(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	root := t.TempDir()
	// The requested directory went away between resolveCwd and tmux, so tmux
	// silently started the session in a fallback directory instead.
	fake.ForceNewSessionCwd = "/"

	logged := captureStdLog(t)

	body := fmt.Sprintf(`{"name": "diverged", "cwd": %q}`, root)
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)

	out := logged()
	assert.Contains(t, out, "did not honour the requested directory")
	assert.Contains(t, out, root, "the warning names the requested path")
	assert.Contains(t, out, `"/"`, "the warning names the path tmux actually used")
}

// TestSameDirectory covers the branch the handler cannot reach from a test —
// the requested directory having been removed after resolveCwd stat'd it,
// which is the real TOCTOU the warning exists to surface.
func TestSameDirectory(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	require.NoError(t, os.Mkdir(realDir, 0o755))
	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(realDir, link))

	assert.True(t, sameDirectory(link, realDir), "a symlink and its target are one directory")
	assert.True(t, sameDirectory(realDir, realDir))
	assert.False(t, sameDirectory(realDir, root), "different directories are different")
	assert.False(t, sameDirectory(filepath.Join(root, "gone"), realDir),
		"a path that no longer exists is not the same directory as anything")
	assert.False(t, sameDirectory(realDir, filepath.Join(root, "gone")))
}

// TestCreate_CoercesLegacyStreamMode: P-D.2 narrowed `mode` to the single
// value `terminal`, but old workspace snapshots and device-state backups may
// still POST `mode: "stream"` (their SessionMeta.mode is forwarded verbatim
// until P-D.3 normalises it). The legacy value is accepted and stored as
// `terminal`, never rejected.
func TestCreate_CoercesLegacyStreamMode(t *testing.T) {
	mod, meta, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	body := `{"name": "legacy-session", "cwd": "/tmp", "mode": "stream"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var info SessionInfo
	err := json.NewDecoder(w.Body).Decode(&info)
	require.NoError(t, err)
	assert.Equal(t, "terminal", info.Mode)

	// Verify meta persisted with the coerced mode
	stored, err := meta.GetMeta("$0")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "terminal", stored.Mode)
}

func TestCreate_RejectsUnknownMode(t *testing.T) {
	mod, _, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	body := `{"name": "jsonl-session", "cwd": "/tmp", "mode": "jsonl"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid mode: must be terminal")
}

func TestHandlerCreateSessionInvalidMode(t *testing.T) {
	mod, _, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	body := `{"name": "bad-mode", "cwd": "/tmp", "mode": "invalid"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid mode")
}

func TestHandlerCreateSessionDuplicate(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("existing", "/tmp")

	body := `{"name": "existing", "cwd": "/tmp"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "session already exists")
}

// TestHandlerCreateSessionConcurrentSameName asserts that N simultaneous POSTs
// for the same session name result in exactly one 201 and N-1 409s, with no
// duplicate entry in the underlying store. Without createMu, the
// HasSession→NewSession TOCTOU window lets two (or more) creates slip past
// the duplicate check and FakeExecutor appends repeat entries to sessionOrder,
// which is deterministically visible via ListSessions length > 1.
func TestHandlerCreateSessionConcurrentSameName(t *testing.T) {
	mod, _, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	// N chosen generously: FakeExecutor serializes each individual tmux
	// op with its own mutex, so the handler-level TOCTOU window is just
	// the scheduler gap between HasSession and NewSession. N=100 keeps
	// the test reliably RED before the fix on modern multi-core hosts.
	const N = 100
	start := make(chan struct{})
	var wg sync.WaitGroup
	codes := make([]int, N)
	bodies := make([]string, N)

	for i := 0; i < N; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release all goroutines simultaneously
			req := httptest.NewRequest(
				http.MethodPost, "/api/sessions",
				strings.NewReader(`{"name":"dup","cwd":"/tmp"}`),
			)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			codes[i] = w.Code
			bodies[i] = w.Body.String()
		}()
	}
	close(start)
	wg.Wait()

	var created, conflict, other int
	for i, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflict++
		default:
			other++
			t.Logf("unexpected status %d body=%q", c, bodies[i])
		}
	}
	assert.Equal(t, 1, created, "exactly one request should succeed")
	assert.Equal(t, N-1, conflict, "other requests should return 409")
	assert.Equal(t, 0, other, "no unexpected statuses")

	// Underlying store must contain exactly one session named "dup".
	// FakeExecutor.NewSession appends to sessionOrder on every call (it
	// overwrites the sessions map but never dedupes the slice), so a
	// lost race is deterministically visible here as length > 1.
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	assert.Len(t, sessions, 1, "no duplicate session should exist in store")
	if len(sessions) == 1 {
		assert.Equal(t, "dup", sessions[0].Name)
	}
}

func TestHandlerCreateSessionInvalidName(t *testing.T) {
	mod, _, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	tests := []struct {
		name string
		body string
	}{
		{"empty name", `{"name": ""}`},
		{"spaces", `{"name": "has spaces"}`},
		{"special chars", `{"name": "bad@name"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

func TestHandlerRenameSessionDuplicate(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("alpha", "/tmp")
	fake.AddSession("beta", "/tmp")

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	code := sessions[0].Code // alpha

	body := `{"name": "beta"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+code, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "session already exists")
}

// TestRenameSessionAtomic_HardErrorNoAgentModule verifies that when the
// agent module is not registered, the rename helper returns a clear error
// (hard-fail) instead of silently falling back to a partial rename.
func TestRenameSessionAtomic_HardErrorNoAgentModule(t *testing.T) {
	mod, _, fake := newTestModule(t)
	fake.AddSession("alpha", "/tmp")

	// agent.module is deliberately NOT registered.
	err := mod.renameSessionAtomic("alpha", "beta")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent.module not registered")

	// tmux must NOT have been modified.
	assert.True(t, fake.HasSession("alpha"))
	assert.False(t, fake.HasSession("beta"))
}

// spyEventRenamer records Rename calls and can be configured to fail.
type spyEventRenamer struct {
	calls  [][2]string
	failOn string // if non-empty, Rename("old", "new") where old == failOn returns error
}

func (s *spyEventRenamer) Rename(oldName, newName string) error {
	s.calls = append(s.calls, [2]string{oldName, newName})
	if s.failOn != "" && oldName == s.failOn {
		return errors.New("simulated DB rename failure")
	}
	return nil
}

// stubAtomicRenamer implements atomicRenamer by immediately running doRename.
// No in-memory state to transfer in this test — we only care about doRename
// behavior and rollback semantics.
type stubAtomicRenamer struct{}

func (stubAtomicRenamer) RenameSessionAtomic(oldName, newName string, doRename func() error) error {
	return doRename()
}

// TestRenameSessionAtomic_RollbackOnTmuxFailure verifies that when tmux
// rename fails after DB rename succeeded, the DB rename is rolled back
// so all three layers (tmux, DB, in-memory) stay consistent.
func TestRenameSessionAtomic_RollbackOnTmuxFailure(t *testing.T) {
	mod, _, _ := newTestModule(t)
	// Note: deliberately do NOT add "alpha" to fake → tmux.RenameSession
	// will fail with ErrNoSession, triggering the rollback path.

	spy := &spyEventRenamer{}
	mod.core.Registry.Register("agent.events", spy)
	mod.core.Registry.Register("agent.module", stubAtomicRenamer{})

	err := mod.renameSessionAtomic("alpha", "beta")
	require.Error(t, err)

	// Expect two DB calls: forward rename + rollback
	require.Len(t, spy.calls, 2, "expected DB rename + rollback, got %v", spy.calls)
	assert.Equal(t, [2]string{"alpha", "beta"}, spy.calls[0], "first call should be forward rename")
	assert.Equal(t, [2]string{"beta", "alpha"}, spy.calls[1], "second call should be rollback")
}

// TestRenameSessionAtomic_HappyPath verifies DB rename is called exactly once
// when tmux rename succeeds (no rollback).
func TestRenameSessionAtomic_HappyPath(t *testing.T) {
	mod, _, fake := newTestModule(t)
	fake.AddSession("alpha", "/tmp")

	spy := &spyEventRenamer{}
	mod.core.Registry.Register("agent.events", spy)
	mod.core.Registry.Register("agent.module", stubAtomicRenamer{})

	err := mod.renameSessionAtomic("alpha", "beta")
	require.NoError(t, err)

	require.Len(t, spy.calls, 1, "expected single DB rename, got %v", spy.calls)
	assert.Equal(t, [2]string{"alpha", "beta"}, spy.calls[0])
	assert.True(t, fake.HasSession("beta"))
	assert.False(t, fake.HasSession("alpha"))
}

// TestRenameSessionAtomic_DBRenameFailsNoTmuxChange verifies that when DB
// rename fails first, tmux is never touched.
func TestRenameSessionAtomic_DBRenameFailsNoTmuxChange(t *testing.T) {
	mod, _, fake := newTestModule(t)
	fake.AddSession("alpha", "/tmp")

	spy := &spyEventRenamer{failOn: "alpha"}
	mod.core.Registry.Register("agent.events", spy)
	mod.core.Registry.Register("agent.module", stubAtomicRenamer{})

	err := mod.renameSessionAtomic("alpha", "beta")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulated DB rename failure")

	// Only the forward attempt, no rollback (nothing to roll back).
	require.Len(t, spy.calls, 1)
	// tmux unchanged.
	assert.True(t, fake.HasSession("alpha"))
	assert.False(t, fake.HasSession("beta"))
}

func TestHandlerDeleteSession(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("doomed", "/tmp/doomed")

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	code := sessions[0].Code

	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/"+code, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	// Verify session is gone
	sessions, err = mod.ListSessions()
	require.NoError(t, err)
	assert.Empty(t, sessions)
}

func TestHandlerDeleteSessionNotFound(t *testing.T) {
	mod, _, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/zzzzzz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestSwitchModeRouteGone: POST /api/sessions/{code}/mode was removed in
// P-D.2 together with the stream module; the session module's own mux must
// no longer know the route, even for a live session.
func TestSwitchModeRouteGone(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("mode-test", "/tmp")

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	code := sessions[0].Code

	body := `{"mode": "terminal"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+code+"/mode", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandlerSendKeys(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("target", "/tmp")

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	code := sessions[0].Code

	body := `{"keys":"echo hello\n"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+code+"/send-keys", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	// Verify keys were sent via SendKeysRaw
	calls := fake.RawKeysSent()
	require.Len(t, calls, 1)
	assert.Equal(t, "=target:", calls[0].Target)
	assert.Equal(t, []string{"echo hello\n"}, calls[0].Keys)
}

func TestHandlerSendKeysNotFound(t *testing.T) {
	mod, _, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	body := `{"keys":"echo hello\n"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/zzzzzz/send-keys", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- send-keys generation precondition (spec §4.6.2) ---

// sendKeysTo posts a send-keys request built from the given JSON body and
// returns the recorder, so each precondition case reads as one line.
func sendKeysTo(t *testing.T, mux *http.ServeMux, code, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+code+"/send-keys", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// A caller that states the generation it believes it is talking to gets that
// belief checked. Matching generation → the keys go through as before.
func TestHandlerSendKeys_ExpectedInstanceMatches_Sends(t *testing.T) {
	mod, _, fake := newTestModule(t)
	// The generation lives on the server that would receive the keys — that is
	// the only place a check about it can be authoritative — so the fake holds
	// it and the daemon reads the same value.
	mod.tmuxInstanceFn = fake.Instance
	fake.SetInstance("111:1000")
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("target", "/tmp")
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	code := sessions[0].Code

	w := sendKeysTo(t, mux, code, `{"keys":"echo hello\n","expected_tmux_instance":"111:1000"}`)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.Len(t, fake.RawKeysSent(), 1)
	// Targeted by session id: the conditional send resolves nothing by name.
	assert.Equal(t, "$0:", fake.RawKeysSent()[0].Target)
}

// The whole point: a session code is a reversible encoding of `$N`, so after a
// tmux restart the code the caller recorded can belong to a stranger. The
// daemon refuses and sends NOTHING.
func TestHandlerSendKeys_ExpectedInstanceMismatch_Refuses409(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = fake.Instance
	fake.SetInstance("222:2000")
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("target", "/tmp")
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	code := sessions[0].Code

	w := sendKeysTo(t, mux, code, `{"keys":"rm -rf /\n","expected_tmux_instance":"111:1000"}`)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Empty(t, fake.RawKeysSent(), "a refused send-keys must send nothing at all")
}

// Unknown never authorises a keystroke (spec §4.6.2). A daemon that cannot
// read its own generation cannot confirm the caller's expectation either.
func TestHandlerSendKeys_ExpectedInstanceAgainstUnknown_Refuses409(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = fake.Instance
	fake.SetInstance("")
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("target", "/tmp")
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	code := sessions[0].Code

	w := sendKeysTo(t, mux, code, `{"keys":"echo hello\n","expected_tmux_instance":"111:1000"}`)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Empty(t, fake.RawKeysSent())
}

// Absent means "no expectation", so Quick Commands and `executeCommand` —
// which post `{"keys":…}` and nothing else — are unaffected.
func TestHandlerSendKeys_NoExpectation_SendsWhateverTheGeneration(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = func() string { return "999:9000" }
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("target", "/tmp")
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	code := sessions[0].Code

	w := sendKeysTo(t, mux, code, `{"keys":"echo hello\n"}`)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.Len(t, fake.RawKeysSent(), 1)
}

// An explicitly empty expectation is the same as none: "" is the unknown
// value, and a caller cannot assert that a session's generation is unknown.
func TestHandlerSendKeys_EmptyExpectation_IsNoExpectation(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = func() string { return "999:9000" }
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("target", "/tmp")
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	code := sessions[0].Code

	w := sendKeysTo(t, mux, code, `{"keys":"echo hello\n","expected_tmux_instance":""}`)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.Len(t, fake.RawKeysSent(), 1)
}

func TestHandleList_CacheDebounce(t *testing.T) {
	mod, _, fake := newTestModule(t)

	fake.AddSession("test", "/tmp")

	handler := http.HandlerFunc(mod.handleList)

	// First call — fetches from tmux
	req1 := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first call: want 200, got %d", w1.Code)
	}
	// ListSessions is called once by handleList, but also internally by
	// ListSessions → listSessions which may call tmux.ListSessions.
	// We count tmux-level ListSessions calls via FakeExecutor.
	firstCount := fake.ListCallCount()
	if firstCount < 1 {
		t.Fatalf("first call: want ≥1 tmux ListSessions calls, got %d", firstCount)
	}

	// Second call within TTL — should use cache (no additional tmux calls)
	req2 := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if fake.ListCallCount() != firstCount {
		t.Fatalf("second call: want %d tmux calls (cached), got %d", firstCount, fake.ListCallCount())
	}

	// Verify both responses are identical
	if w1.Body.String() != w2.Body.String() {
		t.Error("cached response differs from original")
	}
}

func TestHandlerTerminalWSNotFound(t *testing.T) {
	// Setup module with no sessions
	mod, _, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	// Request with invalid code — session does not exist
	req := httptest.NewRequest("GET", "/ws/terminal/zzzzzz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestHandlerCreateSessionExpandsTildeCwd — tmux neither expands ~ nor fails
// on an unusable -c; it silently starts the session in $HOME. The daemon must
// therefore hand tmux an already-resolved absolute directory.
func TestHandlerCreateSessionExpandsTildeCwd(t *testing.T) {
	mod, meta, fake := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	// handleCreate resolves through os.UserHomeDir, which reads $HOME.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	want := filepath.Join(tmp, "sub")
	require.NoError(t, os.MkdirAll(want, 0o755))

	body := `{"name": "tilde", "cwd": "~/sub"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)

	var info SessionInfo
	require.NoError(t, json.NewDecoder(w.Body).Decode(&info))
	assert.Equal(t, want, info.Cwd, "response must carry the resolved path")

	sessions, err := fake.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, want, sessions[0].Cwd, "tmux must receive the expanded path")

	m, err := meta.GetMeta(sessions[0].ID)
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.Equal(t, want, m.Cwd, "stored meta must carry the resolved path")
}

// TestHandlerCreateSessionRejectsMissingCwd — a missing directory used to be
// swallowed by tmux, which created the session in $HOME anyway. It is now a
// 400, and nothing is created.
func TestHandlerCreateSessionRejectsMissingCwd(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	missing := filepath.Join(t.TempDir(), "nope")
	body := `{"name": "missing-cwd", "cwd": ` + strconv.Quote(missing) + `}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid cwd")
	assert.False(t, fake.HasSession("missing-cwd"), "nothing may be created")
}
