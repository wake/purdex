package dev

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleCheck(t *testing.T) {
	dir := t.TempDir()
	versionFile := filepath.Join(dir, "VERSION")
	os.WriteFile(versionFile, []byte("1.0.0-alpha.21\n"), 0644)

	m := &DevModule{
		repoRoot:    dir,
		versionFile: versionFile,
		hashFn:      func(paths ...string) string { return "abc1234" },
	}

	req := httptest.NewRequest("GET", "/api/dev/update/check", nil)
	w := httptest.NewRecorder()
	m.handleCheck(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", w.Code)
	}

	var resp UpdateCheckResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Version != "1.0.0-alpha.21" {
		t.Errorf("version: want 1.0.0-alpha.21, got %s", resp.Version)
	}
	if resp.ElectronHash != "abc1234" {
		t.Errorf("electronHash: want abc1234, got %s", resp.ElectronHash)
	}
	if resp.SPAHash != "abc1234" {
		t.Errorf("spaHash: want abc1234, got %s", resp.SPAHash)
	}
}
