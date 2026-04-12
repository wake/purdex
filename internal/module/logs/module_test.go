package logs

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleDaemonLog(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")
	os.MkdirAll(logsDir, 0700)
	os.WriteFile(filepath.Join(logsDir, "pdx.log"), []byte("line1\nline2\nline3\nline4\nline5\n"), 0644)

	m := &LogsModule{logsDir: logsDir}

	req := httptest.NewRequest("GET", "/api/logs/daemon?tail=3", nil)
	w := httptest.NewRecorder()
	m.handleDaemonLog(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 3 {
		t.Errorf("got %d lines, want 3: %q", len(lines), body)
	}
}

func TestHandleDaemonLogNoFile(t *testing.T) {
	m := &LogsModule{logsDir: t.TempDir()}

	req := httptest.NewRequest("GET", "/api/logs/daemon", nil)
	w := httptest.NewRecorder()
	m.handleDaemonLog(w, req)

	if w.Code != 204 {
		t.Fatalf("status = %d, want 204", w.Code)
	}
}

func TestHandleCrashLog(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")
	os.MkdirAll(logsDir, 0700)
	os.WriteFile(filepath.Join(logsDir, "crash-20260412-041136.log"), []byte("crash content"), 0644)

	m := &LogsModule{logsDir: logsDir}

	req := httptest.NewRequest("GET", "/api/logs/crash", nil)
	w := httptest.NewRecorder()
	m.handleCrashLog(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "crash content") {
		t.Error("missing crash content")
	}
}

func TestHandleCrashLogNone(t *testing.T) {
	m := &LogsModule{logsDir: t.TempDir()}

	req := httptest.NewRequest("GET", "/api/logs/crash", nil)
	w := httptest.NewRecorder()
	m.handleCrashLog(w, req)

	if w.Code != 204 {
		t.Fatalf("status = %d, want 204", w.Code)
	}
}

func TestHandleCrashLogPicksLatest(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")
	os.MkdirAll(logsDir, 0700)
	os.WriteFile(filepath.Join(logsDir, "crash-20260410-120000.log"), []byte("older"), 0644)
	os.WriteFile(filepath.Join(logsDir, "crash-20260412-120000.log"), []byte("newer"), 0644)

	m := &LogsModule{logsDir: logsDir}

	req := httptest.NewRequest("GET", "/api/logs/crash", nil)
	w := httptest.NewRecorder()
	m.handleCrashLog(w, req)

	if !strings.Contains(w.Body.String(), "newer") {
		t.Errorf("expected latest crash log, got: %s", w.Body.String())
	}
}
