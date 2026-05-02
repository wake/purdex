package codexbroker

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// auditFS is a tiny FS impl that wraps os/filepath for tests that need to
// read real files (broker.log fixtures).
type auditFS struct{}

func (auditFS) Open(path string) (io.ReadCloser, error) { return os.Open(path) }
func (auditFS) Stat(path string) (os.FileInfo, error)   { return os.Stat(path) }
func (auditFS) Lstat(path string) (os.FileInfo, error)  { return os.Lstat(path) }
func (auditFS) EvalSymlinks(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
func (auditFS) Glob(pattern string) ([]string, error)         { return filepath.Glob(pattern) }
func (auditFS) ReadDir(path string) ([]os.DirEntry, error)    { return os.ReadDir(path) }

// TestWritePreimage_HappyPath — writes file, reads back, validates JSON.
func TestWritePreimage_HappyPath(t *testing.T) {
	dir := t.TempDir()
	rec := BrokerRecord{Key: "abc123", PID: 4321}
	dec := DecisionResult{Predicates: PredicateResult{A: false}, Kill: true, Reason: "idle_timeout"}
	path, err := WritePreimage(dir, rec, dec, []string{"line a", "line b"})
	if err != nil {
		t.Fatalf("WritePreimage: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(path), "orphan-abc123-") {
		t.Errorf("filename pattern wrong: %s", filepath.Base(path))
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var dump AuditDump
	if err := json.Unmarshal(body, &dump); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if dump.Broker.Key != "abc123" {
		t.Errorf("broker key lost: %s", dump.Broker.Key)
	}
	if dump.Reason != "idle_timeout" {
		t.Errorf("reason lost: %s", dump.Reason)
	}
	if len(dump.BrokerLogTail) != 2 {
		t.Errorf("log tail lost")
	}
}

// TestWritePreimage_AtomicWrite — write to a parent that is itself a file
// (not a directory) → MkdirAll fails; no partial output.
func TestWritePreimage_AtomicWrite(t *testing.T) {
	parent := t.TempDir()
	// Create a regular file at the path we want to use as audit dir.
	blocker := filepath.Join(parent, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(blocker, "audit") // child of a regular file
	rec := BrokerRecord{Key: "abc"}
	dec := DecisionResult{}
	_, err := WritePreimage(bad, rec, dec, nil)
	if err == nil {
		t.Errorf("expected error writing under file-as-dir")
	}
	// No files left in parent that look like an orphan dump.
	entries, _ := os.ReadDir(parent)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "orphan-") {
			t.Errorf("partial file left: %s", e.Name())
		}
	}
}

// TestAppendPostscript_AddsKillSequence — write preimage then append
// postscript; resulting file has both sections.
func TestAppendPostscript_AddsKillSequence(t *testing.T) {
	dir := t.TempDir()
	rec := BrokerRecord{Key: "abc"}
	dec := DecisionResult{Kill: true}
	path, err := WritePreimage(dir, rec, dec, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := KillResult{
		GracefulOk:    false,
		TermOk:        true,
		KillOk:        false,
		StepLatencyMs: [3]int64{5000, 230, 0},
		CleanedUp:     true,
	}
	if err := AppendPostscript(path, result); err != nil {
		t.Fatalf("AppendPostscript: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var dump AuditDump
	if err := json.Unmarshal(body, &dump); err != nil {
		t.Fatal(err)
	}
	if !dump.KillSequence.TermOk {
		t.Errorf("killSequence.TermOk lost")
	}
	if dump.KillSequence.StepLatencyMs[1] != 230 {
		t.Errorf("step latency lost: %v", dump.KillSequence.StepLatencyMs)
	}
}

// TestReadBrokerLogTail_LastNLines — fixture file with 300 lines → returns
// last 200.
func TestReadBrokerLogTail_LastNLines(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "broker.log")
	var sb strings.Builder
	for i := 1; i <= 300; i++ {
		sb.WriteString("line ")
		sb.WriteString(itoaInt(i))
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(logPath, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	tail := ReadBrokerLogTail(dir, auditFS{}, 200)
	if len(tail) != 200 {
		t.Fatalf("len = %d, want 200", len(tail))
	}
	if tail[len(tail)-1] != "line 300" {
		t.Errorf("last line = %q, want line 300", tail[len(tail)-1])
	}
	if tail[0] != "line 101" {
		t.Errorf("first kept line = %q, want line 101", tail[0])
	}
}

// TestReadBrokerLogTail_FileMissing — returns empty, no error.
func TestReadBrokerLogTail_FileMissing(t *testing.T) {
	dir := t.TempDir()
	tail := ReadBrokerLogTail(dir, auditFS{}, 200)
	if len(tail) != 0 {
		t.Errorf("expected empty, got %v", tail)
	}
}

// TestKillSequence_AbortIfPreimageWriteFails — audit dir unwritable → Run
// returns error, no signal sent.
func TestKillSequence_AbortIfPreimageWriteFails(t *testing.T) {
	// Plant a regular file where the audit directory should be so MkdirAll
	// can't create the path.
	parent := t.TempDir()
	blocker := filepath.Join(parent, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(blocker, "audit")
	ks := &KillSequence{
		Rec:      BrokerRecord{Key: "abc", PID: 4321},
		Lister:   NewFakeProcessLister([]RawProcess{{PID: 4321, Cmdline: "node app-server-broker.mjs"}}),
		AuditDir: bad,
	}
	out, err := ks.runStep1(context.Background(), DecisionResult{})
	if err == nil {
		t.Errorf("expected error, got nil")
	}
	if out != "" {
		t.Errorf("expected empty path on error, got %q", out)
	}
}

func itoaInt(n int) string { return itoa(n) }
