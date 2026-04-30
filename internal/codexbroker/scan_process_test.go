package codexbroker

import (
	"context"
	"os"
	"testing"
)

// TestScanProcesses_OneBroker: filters out non-broker rows and parses argv +
// brokerKey for the one matching row.
func TestScanProcesses_OneBroker(t *testing.T) {
	src := loadFixture(t, "one-broker.txt")
	lister := NewFakeProcessListerFromText(src)
	cwd := "/Users/wake/Workspace/wake/purdex"
	fs := &fakeFS{resolve: map[string]string{cwd: cwd}}

	got, err := scanProcesses(context.Background(), lister, fs, false)
	if err != nil {
		t.Fatalf("scanProcesses: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1: %+v", len(got), got)
	}
	c := got[0]
	if c.PID != 12345 {
		t.Errorf("PID = %d, want 12345", c.PID)
	}
	if c.RawCwd != cwd {
		t.Errorf("RawCwd = %q", c.RawCwd)
	}
	if c.CwdResolved != cwd {
		t.Errorf("CwdResolved = %q", c.CwdResolved)
	}
	if c.Endpoint != "unix:/var/folders/aa/bb/T/cxc-ABCDEF/broker.sock" {
		t.Errorf("Endpoint = %q", c.Endpoint)
	}
	if c.PidFile != "/var/folders/aa/bb/T/cxc-ABCDEF/broker.pid" {
		t.Errorf("PidFile = %q", c.PidFile)
	}
	if c.Key == "" || len(c.Key) != 16 {
		t.Errorf("Key = %q (length %d)", c.Key, len(c.Key))
	}
	if len(c.Anomalies) != 0 {
		t.Errorf("expected no anomalies, got %v", c.Anomalies)
	}
}

// TestScanProcesses_DuplicateRuntime returns two candidates with identical key.
func TestScanProcesses_DuplicateRuntime(t *testing.T) {
	src := loadFixture(t, "duplicate-runtime.txt")
	lister := NewFakeProcessListerFromText(src)
	fs := &fakeFS{resolve: map[string]string{"/tmp/dup": "/tmp/dup"}}

	got, err := scanProcesses(context.Background(), lister, fs, false)
	if err != nil {
		t.Fatalf("scanProcesses: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2", len(got))
	}
	if got[0].Key != got[1].Key {
		t.Errorf("keys differ for same cwd: %q vs %q", got[0].Key, got[1].Key)
	}
	if got[0].PID == got[1].PID {
		t.Errorf("same PID for two records: %d", got[0].PID)
	}
}

// TestScanProcesses_ArgvTruncated: candidate is emitted with anomaly, not skipped.
func TestScanProcesses_ArgvTruncated(t *testing.T) {
	src := loadFixture(t, "argv-truncated.txt")
	lister := NewFakeProcessListerFromText(src)
	fs := &fakeFS{}

	got, err := scanProcesses(context.Background(), lister, fs, false)
	if err != nil {
		t.Fatalf("scanProcesses: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1 (truncated must not be skipped)", len(got))
	}
	c := got[0]
	if c.PID != 88888 {
		t.Errorf("PID = %d, want 88888", c.PID)
	}
	found := false
	for _, a := range c.Anomalies {
		if a.Code == AnomalyArgvTruncated {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected AnomalyArgvTruncated, got %v", c.Anomalies)
	}
}

// TestScanProcesses_NoBrokers: empty result when no rows match the broker pattern.
func TestScanProcesses_NoBrokers(t *testing.T) {
	src := loadFixture(t, "no-broker.txt")
	lister := NewFakeProcessListerFromText(src)
	fs := &fakeFS{}

	got, err := scanProcesses(context.Background(), lister, fs, false)
	if err != nil {
		t.Fatalf("scanProcesses: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d candidates, want 0: %+v", len(got), got)
	}
}

// TestScanProcesses_PsFailureReturnsError: lister error propagates as a
// whole-scan failure (so handler can decide 503 vs partial).
func TestScanProcesses_PsFailureReturnsError(t *testing.T) {
	lister := NewFakeProcessLister(nil).WithError(os.ErrInvalid)
	_, err := scanProcesses(context.Background(), lister, &fakeFS{}, false)
	if err == nil {
		t.Fatalf("expected error from lister")
	}
}

// TestScanProcesses_CwdUnresolvableEmitsCandidate: EvalSymlinks failure → candidate
// still emitted with raw cwd + AnomalyCwdUnresolvable.
func TestScanProcesses_CwdUnresolvableEmitsCandidate(t *testing.T) {
	src := loadFixture(t, "one-broker.txt")
	lister := NewFakeProcessListerFromText(src)
	fs := &fakeFS{errors: map[string]error{
		"/Users/wake/Workspace/wake/purdex": os.ErrPermission,
	}}

	got, err := scanProcesses(context.Background(), lister, fs, false)
	if err != nil {
		t.Fatalf("scanProcesses: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d candidates", len(got))
	}
	c := got[0]
	if c.CwdResolved != "" {
		t.Errorf("CwdResolved should be empty on EvalSymlinks fail, got %q", c.CwdResolved)
	}
	found := false
	for _, a := range c.Anomalies {
		if a.Code == AnomalyCwdUnresolvable {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected AnomalyCwdUnresolvable, got %v", c.Anomalies)
	}
}
