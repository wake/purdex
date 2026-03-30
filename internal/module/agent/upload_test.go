package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/tmux-box/internal/core"
	"github.com/wake/tmux-box/internal/module/session"
	"github.com/wake/tmux-box/internal/tmux"
)

func TestDeduplicateFilename(t *testing.T) {
	dir := t.TempDir()

	// No conflict — returns original name.
	got := deduplicateFilename(dir, "photo.png")
	assert.Equal(t, "photo.png", got)

	// Create file to trigger conflict.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "photo.png"), []byte("x"), 0644))
	got = deduplicateFilename(dir, "photo.png")
	assert.Equal(t, "photo-1.png", got)

	// Second conflict.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "photo-1.png"), []byte("x"), 0644))
	got = deduplicateFilename(dir, "photo.png")
	assert.Equal(t, "photo-2.png", got)

	// No extension.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README"), []byte("x"), 0644))
	got = deduplicateFilename(dir, "README")
	assert.Equal(t, "README-1", got)
}

// --- fakeSessionProvider for upload handler tests ---

type fakeSessionProvider struct{}

func (f *fakeSessionProvider) ListSessions() ([]session.SessionInfo, error) {
	return []session.SessionInfo{{Code: "my-sess", Name: "my-sess"}}, nil
}
func (f *fakeSessionProvider) GetSession(code string) (*session.SessionInfo, error) {
	if code == "my-sess" {
		return &session.SessionInfo{Code: "my-sess", Name: "my-sess"}, nil
	}
	return nil, fmt.Errorf("not found")
}
func (f *fakeSessionProvider) UpdateMeta(code string, update session.MetaUpdate) error { return nil }
func (f *fakeSessionProvider) HandleTerminalWS(w http.ResponseWriter, r *http.Request, code string) {
}

// newUploadTestModule creates a Module with a fake session provider and tmux executor for upload tests.
func newUploadTestModule(t *testing.T) (*Module, *tmux.FakeExecutor) {
	t.Helper()
	fake := tmux.NewFakeExecutor()
	fake.AddSession("my-sess", "/tmp")

	c := &core.Core{Tmux: fake}

	m := &Module{core: c}
	m.uploadDir = t.TempDir()
	m.sessions = &fakeSessionProvider{}
	return m, fake
}

func TestHandleUpload_Success(t *testing.T) {
	m, fake := newUploadTestModule(t)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("session", "my-sess")
	fw, _ := w.CreateFormFile("file", "test.png")
	fw.Write([]byte("fake image data"))
	w.Close()

	req := httptest.NewRequest("POST", "/api/agent/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()

	m.handleUpload(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "test.png", resp["filename"])
	assert.Equal(t, true, resp["injected"])

	// File should exist on disk.
	_, err := os.Stat(filepath.Join(m.uploadDir, "my-sess", "test.png"))
	assert.NoError(t, err)

	// send-keys should have been called with quoted path + literal flag.
	calls := fake.RawKeysSent()
	require.Len(t, calls, 1)
	assert.Equal(t, "my-sess", calls[0].Target)
	assert.Contains(t, calls[0].Keys, "-l")
	// Path should be quoted and space-prefixed.
	injected := calls[0].Keys[len(calls[0].Keys)-1]
	assert.True(t, injected[0] == ' ', "should start with space")
	assert.Contains(t, injected, `"`)

}

func TestHandleUpload_PathTraversal(t *testing.T) {
	m, _ := newUploadTestModule(t)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("session", "my-sess")
	fw, _ := w.CreateFormFile("file", "../../etc/passwd")
	fw.Write([]byte("malicious"))
	w.Close()

	req := httptest.NewRequest("POST", "/api/agent/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()

	m.handleUpload(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	// File should be saved with base name only, not traversing.
	_, err := os.Stat(filepath.Join(m.uploadDir, "my-sess", "passwd"))
	assert.NoError(t, err)

	// Should NOT exist outside upload dir.
	_, err = os.Stat(filepath.Join(m.uploadDir, "..", "..", "etc", "passwd"))
	assert.True(t, os.IsNotExist(err))
}

func TestHandleUpload_MissingSession(t *testing.T) {
	m, _ := newUploadTestModule(t)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "test.png")
	fw.Write([]byte("data"))
	w.Close()

	req := httptest.NewRequest("POST", "/api/agent/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()

	m.handleUpload(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleUpload_SessionNotFound(t *testing.T) {
	m, _ := newUploadTestModule(t)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("session", "nonexistent")
	fw, _ := w.CreateFormFile("file", "test.png")
	fw.Write([]byte("data"))
	w.Close()

	req := httptest.NewRequest("POST", "/api/agent/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()

	m.handleUpload(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
