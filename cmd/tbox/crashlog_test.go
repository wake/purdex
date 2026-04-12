package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteCrashLog(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")

	writeCrashLog(logsDir, "test panic", []byte("goroutine 1 [running]:\nmain.main()\n"))

	entries, err := filepath.Glob(filepath.Join(logsDir, "crash-*.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 crash log, got %d", len(entries))
	}

	data, _ := os.ReadFile(entries[0])
	content := string(data)

	if !strings.Contains(content, "Panic: test panic") {
		t.Error("missing panic value")
	}
	if !strings.Contains(content, "goroutine 1") {
		t.Error("missing stack trace")
	}
}

func TestWriteCrashLogRedaction(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")

	setRedactTokens([]string{"supersecret123"})
	defer setRedactTokens(nil)

	panicVal := "Authorization: Bearer tok_abc123\ntoken=purdex_xyz789\nvalue=supersecret123"
	writeCrashLog(logsDir, panicVal, []byte("stack with supersecret123 inside"))

	entries, _ := filepath.Glob(filepath.Join(logsDir, "crash-*.log"))
	data, _ := os.ReadFile(entries[0])
	content := string(data)

	if strings.Contains(content, "tok_abc123") {
		t.Error("Authorization header value not redacted")
	}
	if strings.Contains(content, "purdex_xyz789") {
		t.Error("purdex_ token not redacted")
	}
	if strings.Contains(content, "supersecret123") {
		t.Error("cfg.Token value not redacted")
	}
	if !strings.Contains(content, "[REDACTED]") {
		t.Error("redaction marker missing")
	}
}
