package fs

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// ── test helpers ─────────────────────────────────────────────────────────────

func setupTestModule(t *testing.T) (*FsModule, string) {
	t.Helper()
	return New(), t.TempDir()
}

func postJSON(handler http.HandlerFunc, body interface{}) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestHandleList(t *testing.T) {
	m, dir := setupTestModule(t)

	// Create a file and a subdirectory
	if err := os.WriteFile(filepath.Join(dir, "beta.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "alpha-dir"), 0o755); err != nil {
		t.Fatal(err)
	}

	w := postJSON(m.handleList, map[string]string{"path": dir})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Path    string      `json:"path"`
		Entries []fileEntry `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(resp.Entries))
	}

	// Directories should come first
	if !resp.Entries[0].IsDir {
		t.Errorf("expected first entry to be a directory, got %q", resp.Entries[0].Name)
	}
	if resp.Entries[0].Name != "alpha-dir" {
		t.Errorf("expected first entry name 'alpha-dir', got %q", resp.Entries[0].Name)
	}
	if resp.Entries[1].Name != "beta.txt" {
		t.Errorf("expected second entry name 'beta.txt', got %q", resp.Entries[1].Name)
	}
}

func TestHandleListRejectsRelativePath(t *testing.T) {
	m, _ := setupTestModule(t)

	w := postJSON(m.handleList, map[string]string{"path": "relative/path"})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleListSkipsHiddenFiles(t *testing.T) {
	m, dir := setupTestModule(t)

	// Create a visible file and a hidden file
	if err := os.WriteFile(filepath.Join(dir, "visible.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".hidden"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := postJSON(m.handleList, map[string]string{"path": dir})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Entries []fileEntry `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Entries) != 1 {
		t.Fatalf("expected 1 entry (hidden skipped), got %d", len(resp.Entries))
	}
	if resp.Entries[0].Name != "visible.txt" {
		t.Errorf("expected 'visible.txt', got %q", resp.Entries[0].Name)
	}
}

func TestHandleStat(t *testing.T) {
	m, dir := setupTestModule(t)

	content := []byte("test content")
	filePath := filepath.Join(dir, "stat-test.txt")
	if err := os.WriteFile(filePath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	w := postJSON(m.handleStat, map[string]string{"path": filePath})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Size        int64 `json:"size"`
		Mtime       int64 `json:"mtime"`
		IsDirectory bool  `json:"isDirectory"`
		IsFile      bool  `json:"isFile"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Size != int64(len(content)) {
		t.Errorf("expected size %d, got %d", len(content), resp.Size)
	}
	if !resp.IsFile {
		t.Error("expected isFile to be true")
	}
	if resp.IsDirectory {
		t.Error("expected isDirectory to be false")
	}
	if resp.Mtime == 0 {
		t.Error("expected non-zero mtime")
	}
}

func TestHandleReadWrite(t *testing.T) {
	m, dir := setupTestModule(t)

	filePath := filepath.Join(dir, "rw-test.txt")
	originalContent := "hello, purdex"
	encoded := base64.StdEncoding.EncodeToString([]byte(originalContent))

	// Write
	ww := postJSON(m.handleWrite, map[string]string{
		"path":    filePath,
		"content": encoded,
	})
	if ww.Code != http.StatusNoContent {
		t.Fatalf("write: expected 204, got %d: %s", ww.Code, ww.Body.String())
	}

	// Read back
	wr := postJSON(m.handleRead, map[string]string{"path": filePath})
	if wr.Code != http.StatusOK {
		t.Fatalf("read: expected 200, got %d: %s", wr.Code, wr.Body.String())
	}

	if got := wr.Body.String(); got != originalContent {
		t.Errorf("read content mismatch: expected %q, got %q", originalContent, got)
	}
}

func TestHandleDelete(t *testing.T) {
	m, dir := setupTestModule(t)

	filePath := filepath.Join(dir, "to-delete.txt")
	if err := os.WriteFile(filePath, []byte("bye"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := postJSON(m.handleDelete, map[string]string{"path": filePath})
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}

	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("expected file to be deleted, but it still exists")
	}
}

func TestHandleRename(t *testing.T) {
	m, dir := setupTestModule(t)

	srcPath := filepath.Join(dir, "original.txt")
	dstPath := filepath.Join(dir, "renamed.txt")
	content := []byte("rename me")
	if err := os.WriteFile(srcPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	w := postJSON(m.handleRename, map[string]string{
		"from": srcPath,
		"to":   dstPath,
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}

	if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
		t.Error("expected original file to be gone after rename")
	}

	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("failed to read renamed file: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("renamed file content mismatch: expected %q, got %q", content, got)
	}
}

func TestHandleMkdir(t *testing.T) {
	m, dir := setupTestModule(t)

	newDir := filepath.Join(dir, "new-directory")

	w := postJSON(m.handleMkdir, map[string]interface{}{
		"path":      newDir,
		"recursive": false,
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}

	info, err := os.Stat(newDir)
	if err != nil {
		t.Fatalf("expected directory to exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected created path to be a directory")
	}
}

func TestHandleReadRejectsLargeFile(t *testing.T) {
	m, dir := setupTestModule(t)

	filePath := filepath.Join(dir, "large.bin")
	// Create a file just over 10 MB
	f, err := os.Create(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(11 << 20); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	w := postJSON(m.handleRead, map[string]string{"path": filePath})
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleReadRejectsDirectory(t *testing.T) {
	m, dir := setupTestModule(t)

	w := postJSON(m.handleRead, map[string]string{"path": dir})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleDeleteRejectsShallowRecursive(t *testing.T) {
	m, _ := setupTestModule(t)

	w := postJSON(m.handleDelete, map[string]interface{}{
		"path":      "/tmp",
		"recursive": true,
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}
