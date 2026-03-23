package cc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/tmux-box/internal/history"
	"github.com/wake/tmux-box/internal/module/session"
)

func TestGetHistory_EmptySessionID(t *testing.T) {
	mod := &CCModule{}
	messages, err := mod.GetHistory("/some/path", "")
	require.NoError(t, err)
	assert.Empty(t, messages)
}

func TestGetHistory_ValidJSONL(t *testing.T) {
	// Set up a temp directory as fake ~/.claude/projects/...
	home := t.TempDir()
	t.Setenv("HOME", home)

	cwd := "/Users/wake/Workspace/project"
	ccSessionID := "abc123"
	projectHash := history.CCProjectPath(cwd)

	dir := filepath.Join(home, ".claude", "projects", projectHash)
	require.NoError(t, os.MkdirAll(dir, 0o755))

	jsonl := `{"type":"user","message":{"role":"user","content":"hello"}}
{"type":"assistant","message":{"role":"assistant","model":"claude-3","content":"hi"}}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ccSessionID+".jsonl"), []byte(jsonl), 0o644))

	mod := &CCModule{}
	messages, err := mod.GetHistory(cwd, ccSessionID)
	require.NoError(t, err)
	assert.Len(t, messages, 2)
}

func TestGetHistory_MissingFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	mod := &CCModule{}
	messages, err := mod.GetHistory("/nonexistent/path", "nosuchid")
	require.NoError(t, err)
	assert.Empty(t, messages)
}

func TestHandleHistory_Success(t *testing.T) {
	// Set up fake home with a JSONL file
	home := t.TempDir()
	t.Setenv("HOME", home)

	cwd := "/Users/wake/project"
	ccSessionID := "sess42"
	projectHash := history.CCProjectPath(cwd)
	dir := filepath.Join(home, ".claude", "projects", projectHash)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	jsonl := `{"type":"user","message":{"role":"user","content":"test"}}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ccSessionID+".jsonl"), []byte(jsonl), 0o644))

	// Create a CCModule with a fake SessionProvider
	mod := &CCModule{
		sessions: &fakeSessionProvider{
			sessions: map[string]*session.SessionInfo{
				"abc123": {
					Code:        "abc123",
					Name:        "test-sess",
					Cwd:         cwd,
					CCSessionID: ccSessionID,
				},
			},
		},
	}

	// Use httptest with the new Go 1.22 pattern routing
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sessions/{code}/history", mod.handleHistory)

	req := httptest.NewRequest("GET", "/api/sessions/abc123/history", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var messages []map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&messages))
	assert.Len(t, messages, 1)
}

func TestHandleHistory_SessionNotFound(t *testing.T) {
	mod := &CCModule{
		sessions: &fakeSessionProvider{
			sessions: map[string]*session.SessionInfo{},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sessions/{code}/history", mod.handleHistory)

	req := httptest.NewRequest("GET", "/api/sessions/nope00/history", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// fakeSessionProvider implements session.SessionProvider for testing.
type fakeSessionProvider struct {
	sessions map[string]*session.SessionInfo
}

func (f *fakeSessionProvider) ListSessions() ([]session.SessionInfo, error) {
	var out []session.SessionInfo
	for _, s := range f.sessions {
		out = append(out, *s)
	}
	return out, nil
}

func (f *fakeSessionProvider) GetSession(code string) (*session.SessionInfo, error) {
	if s, ok := f.sessions[code]; ok {
		return s, nil
	}
	return nil, nil
}

func (f *fakeSessionProvider) UpdateMeta(code string, update session.MetaUpdate) error {
	return nil
}

func (f *fakeSessionProvider) HandleTerminalWS(w http.ResponseWriter, r *http.Request, code string) {
}
