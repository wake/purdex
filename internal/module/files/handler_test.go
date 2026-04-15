package files

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestModuleInterface(t *testing.T) {
	m := New()

	if got := m.Name(); got != "files" {
		t.Errorf("Name() = %q, want %q", got, "files")
	}

	if deps := m.Dependencies(); deps != nil {
		t.Errorf("Dependencies() = %v, want nil", deps)
	}
}

func TestHandleList_NormalDirectory(t *testing.T) {
	dir := t.TempDir()

	os.MkdirAll(filepath.Join(dir, "alpha"), 0o755)
	os.MkdirAll(filepath.Join(dir, "beta"), 0o755)
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("hello"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("abc"), 0o644)

	m := New()
	r := httptest.NewRequest(http.MethodGet, "/api/files?path="+dir, nil)
	w := httptest.NewRecorder()

	m.handleList(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var body struct {
		Path    string      `json:"path"`
		Entries []FileEntry `json:"entries"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("json decode: %v", err)
	}

	if body.Path != dir {
		t.Errorf("path = %q, want %q", body.Path, dir)
	}

	if len(body.Entries) != 4 {
		t.Fatalf("entries count = %d, want 4", len(body.Entries))
	}

	// Expected order: dirs first (alpha, beta), then files (a.txt, c.txt)
	expected := []struct {
		name  string
		isDir bool
		size  int64
	}{
		{"alpha", true, 0},
		{"beta", true, 0},
		{"a.txt", false, 3},
		{"c.txt", false, 5},
	}

	for i, exp := range expected {
		e := body.Entries[i]
		if e.Name != exp.name {
			t.Errorf("entries[%d].Name = %q, want %q", i, e.Name, exp.name)
		}
		if e.IsDir != exp.isDir {
			t.Errorf("entries[%d].IsDir = %v, want %v", i, e.IsDir, exp.isDir)
		}
		if !exp.isDir && e.Size != exp.size {
			t.Errorf("entries[%d].Size = %d, want %d", i, e.Size, exp.size)
		}
	}
}

func TestHandleList_DefaultHome(t *testing.T) {
	m := New()
	r := httptest.NewRequest(http.MethodGet, "/api/files", nil)
	w := httptest.NewRecorder()

	m.handleList(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("json decode: %v", err)
	}

	if body.Path == "" {
		t.Error("path is empty, expected non-empty home directory")
	}
}

func TestHandleList_RelativePath(t *testing.T) {
	m := New()
	r := httptest.NewRequest(http.MethodGet, "/api/files?path=relative/path", nil)
	w := httptest.NewRecorder()

	m.handleList(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleList_NonExistentDir(t *testing.T) {
	m := New()
	r := httptest.NewRequest(http.MethodGet, "/api/files?path=/tmp/nonexistent_purdex_test_xxx", nil)
	w := httptest.NewRecorder()

	m.handleList(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHandleList_HiddenFilesFiltered(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, ".hidden"), []byte("h"), 0o644)
	os.WriteFile(filepath.Join(dir, ".config"), []byte("c"), 0o644)
	os.WriteFile(filepath.Join(dir, "visible.txt"), []byte("v"), 0o644)

	m := New()
	r := httptest.NewRequest(http.MethodGet, "/api/files?path="+dir, nil)
	w := httptest.NewRecorder()

	m.handleList(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var body struct {
		Entries []FileEntry `json:"entries"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("json decode: %v", err)
	}

	if len(body.Entries) != 1 {
		t.Fatalf("entries count = %d, want 1", len(body.Entries))
	}

	if body.Entries[0].Name != "visible.txt" {
		t.Errorf("entries[0].Name = %q, want %q", body.Entries[0].Name, "visible.txt")
	}
}

func TestHandleList_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	m := New()
	r := httptest.NewRequest(http.MethodGet, "/api/files?path="+dir, nil)
	w := httptest.NewRecorder()

	m.handleList(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	// Decode into raw map to verify entries is [] not null
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
		t.Fatalf("json decode: %v", err)
	}

	entriesRaw := string(raw["entries"])
	if entriesRaw != "[]" {
		t.Errorf("entries = %s, want []", entriesRaw)
	}
}

func TestHandleList_PathIsFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "somefile.txt")
	os.WriteFile(filePath, []byte("data"), 0o644)

	m := New()
	r := httptest.NewRequest(http.MethodGet, "/api/files?path="+filePath, nil)
	w := httptest.NewRecorder()

	m.handleList(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHandleList_BrokenSymlink(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "normal.txt"), []byte("ok"), 0o644)
	// Broken symlink: lstat succeeds so Info() won't error; verify no crash.
	os.Symlink(filepath.Join(dir, "nonexistent_target"), filepath.Join(dir, "broken_link"))

	m := New()
	r := httptest.NewRequest(http.MethodGet, "/api/files?path="+dir, nil)
	w := httptest.NewRecorder()

	m.handleList(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var body struct {
		Entries []FileEntry `json:"entries"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("json decode: %v", err)
	}

	found := map[string]bool{}
	for _, e := range body.Entries {
		found[e.Name] = true
	}
	if !found["normal.txt"] {
		t.Error("normal.txt not found in entries")
	}
	if !found["broken_link"] {
		t.Error("broken_link not found in entries")
	}
}
