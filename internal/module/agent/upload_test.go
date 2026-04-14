package agent

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/tmux"
)

func TestCreateDedupFile(t *testing.T) {
	dir := t.TempDir()

	// No conflict — returns original name and creates the file.
	f, got, err := createDedupFile(dir, "photo.png")
	require.NoError(t, err)
	f.Close()
	assert.Equal(t, "photo.png", got)

	// File already exists (created above) — should return "photo-1.png".
	f, got, err = createDedupFile(dir, "photo.png")
	require.NoError(t, err)
	f.Close()
	assert.Equal(t, "photo-1.png", got)

	// Second conflict — "photo-1.png" now exists too, expect "photo-2.png".
	f, got, err = createDedupFile(dir, "photo.png")
	require.NoError(t, err)
	f.Close()
	assert.Equal(t, "photo-2.png", got)

	// No extension.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README"), []byte("x"), 0644))
	f, got, err = createDedupFile(dir, "README")
	require.NoError(t, err)
	f.Close()
	assert.Equal(t, "README-1", got)
}

// --- fakeSessionProvider for tests ---
// Defined in handler_test.go (shared across test files in this package).

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

	// PasteText should have been called with the file path.
	pastes := fake.PastesSent()
	require.Len(t, pastes, 1)
	assert.Equal(t, "my-sess", pastes[0].Target)
	assert.Contains(t, pastes[0].Text, "test.png")

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

// TestHandleUpload_PasteTextFail verifies that when PasteText fails the
// uploaded file is removed from disk (no orphaned files).
func TestHandleUpload_PasteTextFail(t *testing.T) {
	m, fake := newUploadTestModule(t)
	fake.FailPasteText = true

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("session", "my-sess")
	fw, _ := mw.CreateFormFile("file", "inject.txt")
	fw.Write([]byte("content"))
	mw.Close()

	req := httptest.NewRequest("POST", "/api/agent/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()

	m.handleUpload(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "inject failed")

	// The uploaded file must not remain on disk.
	_, err := os.Stat(filepath.Join(m.uploadDir, "my-sess", "inject.txt"))
	assert.True(t, os.IsNotExist(err), "orphaned file should have been removed")
}
