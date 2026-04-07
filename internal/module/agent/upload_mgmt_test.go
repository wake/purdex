package agent

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMgmtTestModule creates a Module with a temp uploadDir for management tests.
func newMgmtTestModule(t *testing.T) *Module {
	t.Helper()
	m := &Module{}
	m.uploadDir = t.TempDir()
	return m
}

// TestHandleDeleteUploadFile_NotFound verifies that deleting a non-existent file
// returns 404 without a TOCTOU race (os.Remove is called directly).
func TestHandleDeleteUploadFile_NotFound(t *testing.T) {
	m := newMgmtTestModule(t)

	// Create the session subdirectory so path traversal check passes,
	// but do NOT create the target file.
	require.NoError(t, os.MkdirAll(filepath.Join(m.uploadDir, "sess1"), 0755))

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/upload/files/{session}/{filename}", m.handleDeleteUploadFile)

	req := httptest.NewRequest("DELETE", "/api/upload/files/sess1/ghost.txt", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "file not found")
}

// TestHandleDeleteUploadFile_Success verifies that deleting an existing file
// returns 204 and removes the file from disk.
func TestHandleDeleteUploadFile_Success(t *testing.T) {
	m := newMgmtTestModule(t)

	dir := filepath.Join(m.uploadDir, "sess1")
	require.NoError(t, os.MkdirAll(dir, 0755))
	target := filepath.Join(dir, "real.txt")
	require.NoError(t, os.WriteFile(target, []byte("data"), 0644))

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/upload/files/{session}/{filename}", m.handleDeleteUploadFile)

	req := httptest.NewRequest("DELETE", "/api/upload/files/sess1/real.txt", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)

	_, err := os.Stat(target)
	assert.True(t, os.IsNotExist(err), "file should have been deleted")
}
