package dev

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleDownload(t *testing.T) {
	dir := t.TempDir()

	// Create fake out/ structure
	outMain := filepath.Join(dir, "out", "main")
	outPreload := filepath.Join(dir, "out", "preload")
	os.MkdirAll(outMain, 0755)
	os.MkdirAll(outPreload, 0755)
	os.WriteFile(filepath.Join(outMain, "index.mjs"), []byte("// main"), 0644)
	os.WriteFile(filepath.Join(outPreload, "index.js"), []byte("// preload"), 0644)

	m := &DevModule{repoRoot: dir}

	req := httptest.NewRequest("GET", "/api/dev/update/download", nil)
	w := httptest.NewRecorder()
	m.handleDownload(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("content-type: want application/gzip, got %s", ct)
	}
	if w.Body.Len() == 0 {
		t.Error("body is empty")
	}
}

func TestHandleDownloadIncludesRenderer(t *testing.T) {
	dir := t.TempDir()

	// Create fake out/ structure with main, preload, renderer
	for _, sub := range []string{"main", "preload", "renderer"} {
		p := filepath.Join(dir, "out", sub)
		os.MkdirAll(p, 0755)
		os.WriteFile(filepath.Join(p, "index.js"), []byte("// "+sub), 0644)
	}
	// Also create a directory that should be excluded
	excluded := filepath.Join(dir, "out", "other")
	os.MkdirAll(excluded, 0755)
	os.WriteFile(filepath.Join(excluded, "skip.js"), []byte("// skip"), 0644)

	m := &DevModule{repoRoot: dir}

	req := httptest.NewRequest("GET", "/api/dev/update/download", nil)
	w := httptest.NewRecorder()
	m.handleDownload(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", w.Code)
	}

	// Extract tar and collect file names
	gr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)

	files := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected tar read error: %v", err)
		}
		files[hdr.Name] = true
	}

	for _, want := range []string{"main/index.js", "preload/index.js", "renderer/index.js"} {
		if !files[want] {
			t.Errorf("tar missing %s, got: %v", want, files)
		}
	}
	if files["other/skip.js"] {
		t.Error("tar should not contain other/skip.js")
	}
}

func TestHandleDownloadWalkError(t *testing.T) {
	dir := t.TempDir()

	// Create out/main/ but make a file unreadable to trigger walk error
	outMain := filepath.Join(dir, "out", "main")
	os.MkdirAll(outMain, 0755)
	badFile := filepath.Join(outMain, "index.mjs")
	os.WriteFile(badFile, []byte("// main"), 0644)
	// Remove read permission to trigger an error during io.Copy
	os.Chmod(badFile, 0000)
	defer os.Chmod(badFile, 0644) // cleanup

	m := &DevModule{repoRoot: dir}

	req := httptest.NewRequest("GET", "/api/dev/update/download", nil)
	w := httptest.NewRecorder()
	m.handleDownload(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status: want 500, got %d", w.Code)
	}
}

func TestHandleDownloadMissingOut(t *testing.T) {
	dir := t.TempDir() // no out/ directory

	m := &DevModule{repoRoot: dir}

	req := httptest.NewRequest("GET", "/api/dev/update/download", nil)
	w := httptest.NewRecorder()
	m.handleDownload(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status: want 404, got %d", w.Code)
	}
}

func TestHandleCheck_WithBuildInfo(t *testing.T) {
	dir := t.TempDir()
	versionFile := filepath.Join(dir, "VERSION")
	os.WriteFile(versionFile, []byte("1.0.0-alpha.24\n"), 0644)

	// Create .build-info.json with matching hashes
	outDir := filepath.Join(dir, "out")
	os.MkdirAll(outDir, 0755)
	buildInfo := BuildInfo{
		Version:      "1.0.0-alpha.24",
		SPAHash:      "abc1234",
		ElectronHash: "def5678",
		BuiltAt:      "2026-03-29T12:00:00Z",
	}
	data, _ := json.Marshal(buildInfo)
	os.WriteFile(filepath.Join(outDir, ".build-info.json"), data, 0644)

	m := &DevModule{
		repoRoot:    dir,
		versionFile: versionFile,
		hashFn:      func(paths ...string) string { return "abc1234" },
		buildCmd:    func(*BuildSession) error { return nil },
	}

	// hashFn returns "abc1234" for all paths; SPA build hash is "abc1234" — matches SPA source
	// Electron build hash is "def5678" but hashFn returns "abc1234" — mismatch, but let's
	// use a smarter hashFn to make both match
	m.hashFn = func(paths ...string) string {
		if len(paths) > 0 && paths[0] == "spa/" {
			return "abc1234"
		}
		return "def5678"
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

	// Top-level hashes come from build info
	if resp.Version != "1.0.0-alpha.24" {
		t.Errorf("version: want 1.0.0-alpha.24, got %s", resp.Version)
	}
	if resp.SPAHash != "abc1234" {
		t.Errorf("spaHash: want abc1234, got %s", resp.SPAHash)
	}
	if resp.ElectronHash != "def5678" {
		t.Errorf("electronHash: want def5678, got %s", resp.ElectronHash)
	}

	// Source hashes come from hashFn
	if resp.Source.SPAHash != "abc1234" {
		t.Errorf("source.spaHash: want abc1234, got %s", resp.Source.SPAHash)
	}
	if resp.Source.ElectronHash != "def5678" {
		t.Errorf("source.electronHash: want def5678, got %s", resp.Source.ElectronHash)
	}

	// All match → no build triggered
	if resp.Building {
		t.Error("building: want false, got true")
	}
	if resp.BuildError != "" {
		t.Errorf("buildError: want empty, got %s", resp.BuildError)
	}
}

func TestHandleCheck_NoBuildInfo(t *testing.T) {
	dir := t.TempDir()
	versionFile := filepath.Join(dir, "VERSION")
	os.WriteFile(versionFile, []byte("1.0.0-alpha.24\n"), 0644)

	// No .build-info.json — no out/ directory at all

	m := &DevModule{
		repoRoot:    dir,
		versionFile: versionFile,
		hashFn:      func(paths ...string) string { return "abc1234" },
		buildCmd:    func(*BuildSession) error { return nil },
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

	// No build info → hashes are "unknown"
	if resp.SPAHash != "unknown" {
		t.Errorf("spaHash: want unknown, got %s", resp.SPAHash)
	}
	if resp.ElectronHash != "unknown" {
		t.Errorf("electronHash: want unknown, got %s", resp.ElectronHash)
	}

	// Source ≠ "unknown" → build triggered
	if !resp.Building {
		t.Error("building: want true, got false")
	}
}

func TestHandleCheck_StaleTriggersAutoBuild(t *testing.T) {
	dir := t.TempDir()
	versionFile := filepath.Join(dir, "VERSION")
	os.WriteFile(versionFile, []byte("1.0.0-alpha.24\n"), 0644)

	// Create .build-info.json with OLD hashes
	outDir := filepath.Join(dir, "out")
	os.MkdirAll(outDir, 0755)
	buildInfo := BuildInfo{
		Version:      "1.0.0-alpha.23",
		SPAHash:      "old1111",
		ElectronHash: "old2222",
		BuiltAt:      "2026-03-28T12:00:00Z",
	}
	data, _ := json.Marshal(buildInfo)
	os.WriteFile(filepath.Join(outDir, ".build-info.json"), data, 0644)

	m := &DevModule{
		repoRoot:    dir,
		versionFile: versionFile,
		hashFn:      func(paths ...string) string { return "new3333" },
		buildCmd:    func(*BuildSession) error { return nil },
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

	// Top-level hashes from build info (stale)
	if resp.SPAHash != "old1111" {
		t.Errorf("spaHash: want old1111, got %s", resp.SPAHash)
	}
	if resp.ElectronHash != "old2222" {
		t.Errorf("electronHash: want old2222, got %s", resp.ElectronHash)
	}

	// Source hashes are new
	if resp.Source.SPAHash != "new3333" {
		t.Errorf("source.spaHash: want new3333, got %s", resp.Source.SPAHash)
	}
	if resp.Source.ElectronHash != "new3333" {
		t.Errorf("source.electronHash: want new3333, got %s", resp.Source.ElectronHash)
	}

	// Stale → build triggered
	if !resp.Building {
		t.Error("building: want true, got false")
	}
}
