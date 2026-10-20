package dev

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/buildinfo"
	"github.com/wake/purdex/internal/core"
)

// repoWithCommit is a directory that is a git repository with one commit, and that commit's short hash as `git log -1 --format=%h` prints it.
// The check is about whatever repository the daemon runs from, not about the one the test runs in: a test's working directory is a git
// worktree on a developer's machine, but a wwd job's can be a tar of the commit with no .git at all (the worker's clone did not have the
// job's base yet, #3729), where `git log -1` has nothing to read.
func repoWithCommit(t *testing.T) (dir, short string) {
	t.Helper()
	dir = t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "m"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	cmd := exec.Command("git", "log", "-1", "--format=%h")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return dir, strings.TrimSpace(string(out))
}

func TestHandleDaemonCheck_ReturnsHashes(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	repo, short := repoWithCommit(t)
	m := New(repo)
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/dev/daemon/check")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status %d", resp.StatusCode)
	}
	var body daemonCheckResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.LatestHash == "" {
		t.Error("latest_hash empty — expected a git commit short hash")
	}
	if body.LatestHash != short {
		t.Errorf("latest_hash = %q, want the repository's own %q", body.LatestHash, short)
	}
}

// A directory that is not a repository (a tar workspace has no .git) is "no latest hash", not an error, and the reason is logged, not lost.
func TestHandleDaemonCheck_NotARepositoryIsEmptyAndSaysWhy(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir)) // git looks no higher than the directory: whatever is above the temp dir is no repository of ours
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)

	m := New(dir)
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/dev/daemon/check")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body daemonCheckResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.LatestHash != "" || body.Available {
		t.Errorf("no repository, no latest hash: %+v", body)
	}
	if got := logs.String(); !strings.Contains(got, "git log -1") || !strings.Contains(got, "not a git repository") {
		t.Errorf("the reason git gave is in the log: %q", got)
	}
}

func TestHandleDaemonRebuild_BuildsInTempRepo(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	dir := t.TempDir()
	// Minimal go module that builds at ./cmd/pdx.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "pdx"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "pdx", "main.go"), []byte("package main\nfunc main(){}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := &DevModule{repoRoot: dir}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/dev/daemon/rebuild", m.handleDaemonRebuild)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/dev/daemon/rebuild", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	got := string(body)
	if !strings.Contains(got, `"type":"success"`) {
		t.Errorf("expected success event, got:\n%s", got)
	}
	if !strings.Contains(got, "data: ") {
		t.Errorf("expected SSE framing, got:\n%s", got)
	}
	// After Task 5: rename succeeded, so bin/pdx should exist (pdx.new is gone).
	if _, err := os.Stat(filepath.Join(dir, "bin", "pdx")); err != nil {
		t.Errorf("bin/pdx not present after rename: %v", err)
	}
}

// writeTinyRepo makes a module that builds at ./cmd/pdx.
func writeTinyRepo(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "pdx"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "pdx", "main.go"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

type restartCall struct{ reason, execPath string }

func postRebuild(t *testing.T, m *DevModule) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/dev/daemon/rebuild", m.handleDaemonRebuild)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/api/dev/daemon/rebuild", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
	return string(body)
}

// #3572: the rebuild hands the swap to core's restart flow (ordered shutdown,
// logs, relay-aware) instead of exec'ing itself, and asks for the binary it
// just built, not the one the daemon booted from.
func TestHandleDaemonRebuild_RequestsCoreRestartWithRebuiltBinary(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	dir := writeTinyRepo(t, "package main\nfunc main(){}\n")

	var calls []restartCall
	m := &DevModule{
		repoRoot: dir,
		restartFn: func(reason, execPath string, _ func()) error {
			calls = append(calls, restartCall{reason, execPath})
			return nil
		},
	}
	body := postRebuild(t, m)

	if len(calls) != 1 {
		t.Fatalf("restart calls: want 1, got %d", len(calls))
	}
	if calls[0].execPath != filepath.Join(dir, "bin", "pdx") {
		t.Fatalf("exec path: want rebuilt binary, got %q", calls[0].execPath)
	}
	if !strings.Contains(calls[0].reason, "dev rebuild") {
		t.Errorf("reason should name the dev rebuild, got %q", calls[0].reason)
	}
	if !strings.Contains(body, `"type":"restarting"`) {
		t.Errorf("expected restarting event, got:\n%s", body)
	}
}

func TestHandleDaemonRebuild_RestartRefusedReportsError(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	dir := writeTinyRepo(t, "package main\nfunc main(){}\n")

	n := 0
	m := &DevModule{
		repoRoot: dir,
		restartFn: func(string, string, func()) error {
			n++
			return core.ErrRestartInProgress
		},
	}
	body := postRebuild(t, m)

	if n != 1 {
		t.Fatalf("restart attempts: want 1, got %d", n)
	}
	if !strings.Contains(body, `"type":"error"`) || !strings.Contains(body, "restart_in_progress") {
		t.Errorf("expected error event naming the refusal, got:\n%s", body)
	}
	if strings.Contains(body, `"type":"restarting"`) {
		t.Errorf("must not announce a restart that was refused:\n%s", body)
	}
}

func TestHandleDaemonRebuild_BuildFailureDoesNotRestart(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	dir := writeTinyRepo(t, "package main\nfunc main(){ broken syntax }\n")

	n := 0
	m := &DevModule{
		repoRoot:  dir,
		restartFn: func(string, string, func()) error { n++; return nil },
	}
	body := postRebuild(t, m)
	if n != 0 {
		t.Fatalf("a failed build must not restart, got %d calls", n)
	}
	if !strings.Contains(body, `"type":"error"`) {
		t.Errorf("expected error event, got:\n%s", body)
	}
}

func TestHandleDaemonRebuild_ConcurrentReturns409(t *testing.T) {
	daemonRebuildMu.Lock()
	defer daemonRebuildMu.Unlock()

	m := &DevModule{repoRoot: "."}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/dev/daemon/rebuild", nil)
	m.handleDaemonRebuild(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("want 409, got %d", w.Code)
	}
}

func TestHandleDaemonRebuild_BuildFailureEmitsError(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "pdx"), 0755); err != nil {
		t.Fatal(err)
	}
	// Deliberate syntax error to trigger go build failure.
	if err := os.WriteFile(filepath.Join(dir, "cmd", "pdx", "main.go"), []byte("package main\nfunc main(){ broken syntax }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m := &DevModule{repoRoot: dir}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/dev/daemon/rebuild", nil)
	m.handleDaemonRebuild(w, req)
	body := w.Body.String()
	if !strings.Contains(body, `"type":"error"`) {
		t.Errorf("expected error event, got:\n%s", body)
	}
}

func TestHandleDaemonRebuild_RenameFailureEmitsError(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "pdx"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "pdx", "main.go"), []byte("package main\nfunc main(){}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Make bin/pdx a non-empty directory so rename(file -> dir) fails.
	if err := os.MkdirAll(filepath.Join(dir, "bin", "pdx"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "pdx", "blocker"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	m := &DevModule{repoRoot: dir}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/dev/daemon/rebuild", m.handleDaemonRebuild)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/dev/daemon/rebuild", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	got := string(body)
	if !strings.Contains(got, `"type":"error"`) {
		t.Errorf("expected error event after rename failure, got:\n%s", got)
	}
	if !strings.Contains(got, "rename failed") {
		t.Errorf("expected rename-failed message, got:\n%s", got)
	}
}

func TestHandleDaemonCheck_AvailableFlag(t *testing.T) {
	old := buildinfo.Hash
	defer func() { buildinfo.Hash = old }()

	buildinfo.Hash = "definitely-not-a-real-hash-0000"
	t.Setenv("PDX_DEV_MODE", "1")
	repo, _ := repoWithCommit(t)
	m := New(repo)
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/dev/daemon/check")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body daemonCheckResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available {
		t.Errorf("expected Available=true when buildinfo.Hash differs, got %+v", body)
	}
}

func TestReadVersionFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("1.2.3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m := &DevModule{repoRoot: dir, versionFile: filepath.Join(dir, "VERSION")}
	if got := m.readVersionFile(); got != "1.2.3" {
		t.Fatalf("readVersionFile = %q, want 1.2.3", got)
	}
	if got := (&DevModule{}).readVersionFile(); got != "unknown" {
		t.Fatalf("missing VERSION → %q, want unknown", got)
	}
}
