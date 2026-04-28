package fs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// httpSearchBodyT mirrors the wire shape; defined locally so the test owns
// the JSON tags and never accidentally leaks the daemon-internal SearchRoot
// (whose Kind/Absolute fields are unexported on the wire).
type httpSearchBodyT struct {
	Mode    string                 `json:"mode"`
	Query   map[string]string      `json:"query"`
	Roots   []map[string]any       `json:"roots"`
	Limits  map[string]int         `json:"limits,omitempty"`
	Filters map[string]any         `json:"filters,omitempty"`
}

func newHTTPReq(t *testing.T, body httpSearchBodyT) *http.Request {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	return httptest.NewRequest("POST", "/api/fs/search", bytes.NewReader(b))
}

func setupSearchHandlerTest(t *testing.T, sessionCwd string) (*FsModule, string) {
	t.Helper()
	provider := newFakeSessionProvider()
	if sessionCwd != "" {
		provider.setCwd("sess1", sessionCwd)
	}
	m := newTestFsModule(t, provider)
	return m, sessionCwd
}

func TestHandleSearch_RejectsAbsoluteRootKind(t *testing.T) {
	dir := t.TempDir()
	m, _ := setupSearchHandlerTest(t, dir)
	body := httpSearchBodyT{
		Mode:  "basename",
		Query: map[string]string{"basename": "foo"},
		Roots: []map[string]any{{"kind": "absolute", "path": dir}},
	}
	w := httptest.NewRecorder()
	m.handleSearch(w, newHTTPReq(t, body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for kind=absolute, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleSearch_RejectsUnknownRootKind(t *testing.T) {
	dir := t.TempDir()
	m, _ := setupSearchHandlerTest(t, dir)
	body := httpSearchBodyT{
		Mode:  "basename",
		Query: map[string]string{"basename": "foo"},
		Roots: []map[string]any{{"kind": "made-up-kind", "sessionCode": "sess1"}},
	}
	w := httptest.NewRecorder()
	m.handleSearch(w, newHTTPReq(t, body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown kind, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleSearch_WorkspaceProjectPathReturns501(t *testing.T) {
	m, _ := setupSearchHandlerTest(t, "")
	body := httpSearchBodyT{
		Mode:  "basename",
		Query: map[string]string{"basename": "foo"},
		Roots: []map[string]any{{"kind": "workspace-projectPath", "workspaceId": "w1"}},
	}
	w := httptest.NewRecorder()
	m.handleSearch(w, newHTTPReq(t, body))
	if w.Code != http.StatusNotImplemented {
		t.Errorf("expected 501 for workspace-projectPath, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleSearch_RejectsSystemPaths(t *testing.T) {
	for _, sysPath := range []string{"/", "/etc", "/sys", "/Users"} {
		t.Run(sysPath, func(t *testing.T) {
			m, _ := setupSearchHandlerTest(t, sysPath)
			body := httpSearchBodyT{
				Mode:  "basename",
				Query: map[string]string{"basename": "foo"},
				Roots: []map[string]any{{"kind": "session-cwd", "sessionCode": "sess1"}},
			}
			w := httptest.NewRecorder()
			m.handleSearch(w, newHTTPReq(t, body))
			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400 for system path %s, got %d body=%s", sysPath, w.Code, w.Body.String())
			}
		})
	}
}

func TestHandleSearch_RejectsHomeDirRoot(t *testing.T) {
	home, _ := os.UserHomeDir()
	if home == "" {
		t.Skip("HOME not set")
	}
	m, _ := setupSearchHandlerTest(t, home)
	body := httpSearchBodyT{
		Mode:  "basename",
		Query: map[string]string{"basename": "foo"},
		Roots: []map[string]any{{"kind": "session-cwd", "sessionCode": "sess1"}},
	}
	w := httptest.NewRecorder()
	m.handleSearch(w, newHTTPReq(t, body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for $HOME root, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleSearch_AcceptsSessionCwd(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "src", "found.go"), "ok")
	m, _ := setupSearchHandlerTest(t, dir)
	body := httpSearchBodyT{
		Mode:  "basename",
		Query: map[string]string{"basename": "found.go"},
		Roots: []map[string]any{{"kind": "session-cwd", "sessionCode": "sess1"}},
	}
	w := httptest.NewRecorder()
	m.handleSearch(w, newHTTPReq(t, body))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp SearchResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	if len(resp.Matches) != 1 {
		t.Errorf("expected 1 match, got %+v", resp.Matches)
	}
}

func TestHandleSearch_MandatoryExcludesEnforcedWhenClientSendsEmpty(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "node_modules", "foo.go"), "skip")
	mustWrite(t, filepath.Join(dir, "src", "foo.go"), "ok")
	m, _ := setupSearchHandlerTest(t, dir)
	body := httpSearchBodyT{
		Mode:  "basename",
		Query: map[string]string{"basename": "foo.go"},
		Roots: []map[string]any{{"kind": "session-cwd", "sessionCode": "sess1"}},
		Filters: map[string]any{
			"excludeDirs":          []string{}, // explicit empty array
			"excludeBasenameGlobs": []string{},
		},
	}
	w := httptest.NewRecorder()
	m.handleSearch(w, newHTTPReq(t, body))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp SearchResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	if len(resp.Matches) != 1 {
		t.Errorf("expected mandatory excludes to filter out node_modules; got %+v", resp.Matches)
	}
}

func TestHandleSearch_RespectGitignoreDefaultTrue(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".gitignore"), "build/\n")
	mustWrite(t, filepath.Join(dir, "build", "foo.go"), "skip")
	mustWrite(t, filepath.Join(dir, "src", "foo.go"), "ok")
	m, _ := setupSearchHandlerTest(t, dir)
	body := httpSearchBodyT{
		Mode:  "basename",
		Query: map[string]string{"basename": "foo.go"},
		Roots: []map[string]any{{"kind": "session-cwd", "sessionCode": "sess1"}},
		// Filters omitted entirely — default true.
	}
	w := httptest.NewRecorder()
	m.handleSearch(w, newHTTPReq(t, body))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp SearchResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	if len(resp.Matches) != 1 {
		t.Errorf("expected build/ pruned by default gitignore, got %+v", resp.Matches)
	}
}

func TestHandleSearch_GitignoreParseError400(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".gitignore"), "[broken\n")
	m, _ := setupSearchHandlerTest(t, dir)
	body := httpSearchBodyT{
		Mode:  "basename",
		Query: map[string]string{"basename": "x"},
		Roots: []map[string]any{{"kind": "session-cwd", "sessionCode": "sess1"}},
	}
	w := httptest.NewRecorder()
	m.handleSearch(w, newHTTPReq(t, body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for gitignore parse error, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleSearch_MissingSessionReturns400(t *testing.T) {
	m := newTestFsModule(t, newFakeSessionProvider())
	body := httpSearchBodyT{
		Mode:  "basename",
		Query: map[string]string{"basename": "x"},
		Roots: []map[string]any{{"kind": "session-cwd", "sessionCode": "no-such-sess"}},
	}
	w := httptest.NewRecorder()
	m.handleSearch(w, newHTTPReq(t, body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing session, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleSearch_RejectsEmptyRoots(t *testing.T) {
	m, _ := setupSearchHandlerTest(t, t.TempDir())
	body := httpSearchBodyT{
		Mode:  "basename",
		Query: map[string]string{"basename": "x"},
		Roots: []map[string]any{},
	}
	w := httptest.NewRecorder()
	m.handleSearch(w, newHTTPReq(t, body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty roots, got %d", w.Code)
	}
}

func TestHandleSearch_RejectsUnsupportedMode(t *testing.T) {
	m, _ := setupSearchHandlerTest(t, t.TempDir())
	body := httpSearchBodyT{
		Mode:  "fuzzy",
		Query: map[string]string{"basename": "x"},
		Roots: []map[string]any{{"kind": "session-cwd", "sessionCode": "sess1"}},
	}
	w := httptest.NewRecorder()
	m.handleSearch(w, newHTTPReq(t, body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for fuzzy mode, got %d", w.Code)
	}
}

func TestHandleSearch_RejectsNonPOST(t *testing.T) {
	m, _ := setupSearchHandlerTest(t, t.TempDir())
	req := httptest.NewRequest("GET", "/api/fs/search", nil)
	w := httptest.NewRecorder()
	m.handleSearch(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// TestHandleSearch_SymlinkSessionCwdResolvedBeforeAllowlist — R2-H1: session
// cwd that lexically lives in /tmp but symlinks into $HOME (or another blocked
// area) must be rejected because EvalSymlinks runs before validateNotSystemPath.
func TestHandleSearch_SymlinkSessionCwdRejectedWhenResolvesToHomeDirect(t *testing.T) {
	tmp := t.TempDir()
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	link := filepath.Join(tmp, "home-link")
	require.NoError(t, os.Symlink(home, link))
	// Cwd is the symlink itself — resolves directly to $HOME (a system root).
	m, _ := setupSearchHandlerTest(t, link)
	body := httpSearchBodyT{
		Mode:  "basename",
		Query: map[string]string{"basename": "x"},
		Roots: []map[string]any{{"kind": "session-cwd", "sessionCode": "sess1"}},
	}
	w := httptest.NewRecorder()
	m.handleSearch(w, newHTTPReq(t, body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 (symlink resolves to $HOME); got %d body=%s", w.Code, w.Body.String())
	}
}

// TestHandleSearch_SymlinkResolvedDeepWorkspaceAllowed — control: a symlink
// inside /tmp pointing to another /tmp dir is fine; resolved path is still
// non-system.
func TestHandleSearch_SymlinkResolvedDeepWorkspaceAllowed(t *testing.T) {
	target := t.TempDir()
	mustWrite(t, filepath.Join(target, "found.go"), "ok")
	tmp := t.TempDir()
	link := filepath.Join(tmp, "ws-link")
	require.NoError(t, os.Symlink(target, link))
	m, _ := setupSearchHandlerTest(t, link)
	body := httpSearchBodyT{
		Mode:  "basename",
		Query: map[string]string{"basename": "found.go"},
		Roots: []map[string]any{{"kind": "session-cwd", "sessionCode": "sess1"}},
	}
	w := httptest.NewRecorder()
	m.handleSearch(w, newHTTPReq(t, body))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 (resolved into another /tmp dir is fine); got %d body=%s", w.Code, w.Body.String())
	}
}

// TestHandleSearch_SessionCwdNotExistRejected — non-existent cwd produces an
// EvalSymlinks error that the handler must surface as 400 (not 500).
func TestHandleSearch_SessionCwdNotExistRejected(t *testing.T) {
	m, _ := setupSearchHandlerTest(t, "/nonexistent-path-on-purpose-xyz")
	body := httpSearchBodyT{
		Mode:  "basename",
		Query: map[string]string{"basename": "x"},
		Roots: []map[string]any{{"kind": "session-cwd", "sessionCode": "sess1"}},
	}
	w := httptest.NewRecorder()
	m.handleSearch(w, newHTTPReq(t, body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-existent cwd, got %d", w.Code)
	}
}
