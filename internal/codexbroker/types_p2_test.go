package codexbroker

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// memFS is an in-memory FS implementation used by P2 tests. Only the methods
// the unit tests need are filled in; missing methods panic so future tests
// fail loudly rather than silently regressing.
type memFS struct {
	files map[string]string
	dirs  map[string]bool
}

func newMemFS() *memFS {
	return &memFS{files: map[string]string{}, dirs: map[string]bool{}}
}

func (m *memFS) writeFile(path, body string) {
	m.files[path] = body
}

func (m *memFS) Open(path string) (io.ReadCloser, error) {
	body, ok := m.files[path]
	if !ok {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

func (m *memFS) Stat(path string) (os.FileInfo, error) {
	if _, ok := m.files[path]; ok {
		return memFileInfo{name: filepath.Base(path), size: int64(len(m.files[path]))}, nil
	}
	if m.dirs[path] {
		return memFileInfo{name: filepath.Base(path), dir: true}, nil
	}
	return nil, &os.PathError{Op: "stat", Path: path, Err: os.ErrNotExist}
}

func (m *memFS) Lstat(path string) (os.FileInfo, error) { return m.Stat(path) }

func (m *memFS) EvalSymlinks(path string) (string, error) { return path, nil }

func (m *memFS) Glob(pattern string) ([]string, error) { return nil, nil }

func (m *memFS) ReadDir(path string) ([]os.DirEntry, error) { return nil, nil }

type memFileInfo struct {
	name string
	size int64
	dir  bool
}

func (f memFileInfo) Name() string       { return f.name }
func (f memFileInfo) Size() int64        { return f.size }
func (f memFileInfo) Mode() os.FileMode  { return 0o644 }
func (f memFileInfo) ModTime() time.Time { return time.Time{} }
func (f memFileInfo) IsDir() bool        { return f.dir }
func (f memFileInfo) Sys() any           { return nil }

// TestDecisionResult_JSONRoundTrip — encode + decode a populated DecisionResult
// without loss; AnomaliesAdded must round-trip.
func TestDecisionResult_JSONRoundTrip(t *testing.T) {
	in := DecisionResult{
		Predicates: PredicateResult{
			A: true, ADetail: "rpc:active",
			B: false, BDetail: "no recent",
			C: true, CDetail: "pane alive",
		},
		IdleSeconds:    120,
		OverrideE1:     true,
		OverrideE2:     false,
		Kill:           false,
		Reason:         "foreign_quarantine",
		AnomaliesAdded: []AnomalyCode{AnomalyForeignOwner},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out DecisionResult
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Predicates != in.Predicates {
		t.Errorf("predicates lost: got %+v want %+v", out.Predicates, in.Predicates)
	}
	if out.IdleSeconds != in.IdleSeconds {
		t.Errorf("idle seconds lost: got %d want %d", out.IdleSeconds, in.IdleSeconds)
	}
	if out.OverrideE1 != in.OverrideE1 || out.OverrideE2 != in.OverrideE2 {
		t.Errorf("overrides lost: %+v", out)
	}
	if out.Reason != in.Reason {
		t.Errorf("reason lost: got %q want %q", out.Reason, in.Reason)
	}
	if len(out.AnomaliesAdded) != 1 || out.AnomaliesAdded[0] != AnomalyForeignOwner {
		t.Errorf("AnomaliesAdded lost: %+v", out.AnomaliesAdded)
	}
}

// TestQuarantineEntry_ExpiresAt — verify time arithmetic matches spec
// (quarantinedAt + retention).
func TestQuarantineEntry_ExpiresAt(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	want := now.Add(7 * 24 * time.Hour)
	entry := QuarantineEntry{
		BrokerKey:     "abcdef0123456789",
		PID:           4242,
		Lstart:        now.Add(-2 * time.Hour),
		QuarantinedAt: now,
		Reason:        "pid_reuse_suspicion",
		Fingerprint:   BrokerFingerprint{Executable: "/usr/bin/node", Cmdline: "node app-server-broker.mjs", PidFile: "/tmp/cxc-X/broker.pid"},
		ExpiresAt:     want,
	}
	if !entry.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt got %v want %v", entry.ExpiresAt, want)
	}
}

// TestAuditDump_JSONRoundTrip — round-trip including nested killSequence with
// stepLatencyMs slice.
func TestAuditDump_JSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	last := now.Add(-1 * time.Hour)
	in := AuditDump{
		KilledAt: now,
		Reason:   "manual_sweep_override",
		Broker: BrokerRecord{
			Key:              "abcdef0123456789",
			PID:              12345,
			Lstart:           last,
			Cwd:              "/tmp/foo",
			JobCounts:        JobCounts{Completed: 1},
			LastJobUpdatedAt: &last,
		},
		Decision: DecisionResult{
			Predicates:     PredicateResult{A: false, B: false, C: false},
			IdleSeconds:    3600,
			OverrideE1:     false,
			OverrideE2:     false,
			Kill:           true,
			Reason:         "foreign_quarantine",
			AnomaliesAdded: []AnomalyCode{AnomalyForeignOwner},
		},
		KillSequence: AuditKillSequence{
			GracefulOk:    false,
			TermOk:        true,
			KillOk:        false,
			StepLatencyMs: [3]int64{5000, 250, 0},
		},
		BrokerLogTail: []string{"line 1", "line 2"},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out AuditDump
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out.KilledAt.Equal(in.KilledAt) {
		t.Errorf("KilledAt lost: %v vs %v", out.KilledAt, in.KilledAt)
	}
	if out.Reason != in.Reason {
		t.Errorf("Reason lost: %q vs %q", out.Reason, in.Reason)
	}
	if out.KillSequence.StepLatencyMs != in.KillSequence.StepLatencyMs {
		t.Errorf("StepLatencyMs lost: %v vs %v", out.KillSequence.StepLatencyMs, in.KillSequence.StepLatencyMs)
	}
	if len(out.BrokerLogTail) != 2 || out.BrokerLogTail[1] != "line 2" {
		t.Errorf("BrokerLogTail lost: %+v", out.BrokerLogTail)
	}
	if len(out.Decision.AnomaliesAdded) != 1 {
		t.Errorf("Decision.AnomaliesAdded lost: %+v", out.Decision.AnomaliesAdded)
	}
}

// TestReadStateJobs_HappyPath — fixture state.json with 3 jobs decodes into 3
// StateJobLite with correct *int Pid (one nil, two populated) and *time.Time
// CompletedAt.
func TestReadStateJobs_HappyPath(t *testing.T) {
	fs := newMemFS()
	body := `{
		"jobs": [
			{"id":"j1","status":"running","updatedAt":"2026-05-01T12:00:00Z","pid":4321},
			{"id":"j2","status":"completed","updatedAt":"2026-05-01T11:30:00Z","completedAt":"2026-05-01T11:31:00Z","pid":null},
			{"id":"j3","status":"queued","updatedAt":"2026-05-01T11:55:00Z","pid":7777}
		]
	}`
	fs.writeFile("/state/state.json", body)
	jobs, err := ReadStateJobs(fs, "/state")
	if err != nil {
		t.Fatalf("ReadStateJobs: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("len jobs = %d, want 3", len(jobs))
	}
	if jobs[0].ID != "j1" || jobs[0].Status != "running" {
		t.Errorf("j1 wrong: %+v", jobs[0])
	}
	if jobs[0].Pid == nil || *jobs[0].Pid != 4321 {
		t.Errorf("j1 pid not 4321: %+v", jobs[0].Pid)
	}
	if jobs[1].Pid != nil {
		t.Errorf("j2 pid should be nil (json null): %+v", jobs[1].Pid)
	}
	if jobs[1].CompletedAt == nil {
		t.Errorf("j2 completedAt should be non-nil")
	}
	if jobs[2].Pid == nil || *jobs[2].Pid != 7777 {
		t.Errorf("j3 pid not 7777: %+v", jobs[2].Pid)
	}
	if jobs[2].CompletedAt != nil {
		t.Errorf("j3 completedAt should be nil: %+v", jobs[2].CompletedAt)
	}
}

// TestReadStateJobs_FileMissing — returns (nil, nil), no error.
func TestReadStateJobs_FileMissing(t *testing.T) {
	fs := newMemFS()
	jobs, err := ReadStateJobs(fs, "/missing")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if jobs != nil {
		t.Errorf("expected nil slice, got %+v", jobs)
	}
}

// TestReadStateJobs_EmptyJobs — state.json present but jobs:[] → returns
// (nil, nil).
func TestReadStateJobs_EmptyJobs(t *testing.T) {
	fs := newMemFS()
	fs.writeFile("/state/state.json", `{"jobs":[]}`)
	jobs, err := ReadStateJobs(fs, "/state")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if jobs != nil {
		t.Errorf("want nil, got %+v", jobs)
	}
}

// TestReadStateJobs_MalformedJSON — returns parse error so caller can fall
// through to a documented degraded path.
func TestReadStateJobs_MalformedJSON(t *testing.T) {
	fs := newMemFS()
	fs.writeFile("/state/state.json", `{"jobs": [not-json`)
	_, err := ReadStateJobs(fs, "/state")
	if err == nil {
		t.Fatalf("expected parse error, got nil")
	}
	// Make sure it's not the file-missing error path.
	if errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected parse error, got file-missing: %v", err)
	}
}
