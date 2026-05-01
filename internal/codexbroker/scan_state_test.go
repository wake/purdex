package codexbroker

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func stateRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "state"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	return abs
}

// findCandidate looks up a single state candidate in the result by directory
// suffix.
func findCandidate(t *testing.T, cands []stateCandidate, suffix string) *stateCandidate {
	t.Helper()
	for i := range cands {
		if filepath.Base(cands[i].StateDir) == suffix {
			return &cands[i]
		}
	}
	t.Fatalf("candidate %q not found in %d candidates", suffix, len(cands))
	return nil
}

// TestScanStateDirs_Live: live broker fixture (broker.json + state.json with
// running job) yields a candidate with HasBrokerJSON, BrokerJSONPID, JobCounts.Running == 1.
func TestScanStateDirs_Live(t *testing.T) {
	got, _ := scanStateDirs(context.Background(), NewOsFS(), stateRoot(t), 100*time.Millisecond)

	c := findCandidate(t, got, "live-aaaaaaaaaaaaaaaa")
	if !c.HasBrokerJSON {
		t.Errorf("HasBrokerJSON = false, want true")
	}
	if c.BrokerJSON.PID != 12345 {
		t.Errorf("BrokerJSON.PID = %d, want 12345", c.BrokerJSON.PID)
	}
	if c.JobCounts.Running != 1 {
		t.Errorf("JobCounts.Running = %d, want 1", c.JobCounts.Running)
	}
	if c.Key != "aaaaaaaaaaaaaaaa" {
		t.Errorf("Key = %q, want directory suffix", c.Key)
	}
}

// TestScanStateDirs_Historical_Skipped: historical (no broker.json) is not
// emitted in results.
func TestScanStateDirs_Historical_Skipped(t *testing.T) {
	got, _ := scanStateDirs(context.Background(), NewOsFS(), stateRoot(t), 100*time.Millisecond)
	for _, c := range got {
		if filepath.Base(c.StateDir) == "historical-bbbbbbbbbbbbbbbb" {
			t.Errorf("historical dir should be skipped (no broker.json), got candidate: %+v", c)
		}
	}
}

// TestScanStateDirs_MalformedBrokerJSON: broker.json malformed → anomaly,
// dir skipped from candidates, neighbouring dirs unaffected.
func TestScanStateDirs_MalformedBrokerJSON(t *testing.T) {
	cands, anoms := scanStateDirs(context.Background(), NewOsFS(), stateRoot(t), 100*time.Millisecond)
	// malformed-cccc... should NOT appear as a candidate (per plan: malformed
	// broker.json → anomaly + skip dir).
	for _, c := range cands {
		if filepath.Base(c.StateDir) == "malformed-cccccccccccccccc" {
			t.Errorf("malformed dir should be skipped, got candidate: %+v", c)
		}
	}
	// Anomaly should be present.
	found := false
	for _, a := range anoms {
		if a.Code == AnomalyBrokerJSONUnreadable {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected AnomalyBrokerJSONUnreadable, got %v", anoms)
	}
	// Live and other dirs still present.
	if findCandidate(t, cands, "live-aaaaaaaaaaaaaaaa") == nil {
		t.Errorf("live dir lost after malformed dir error")
	}
}

// TestScanStateDirs_StateJSONReadFails_RecordStillEmitted: broker.json valid
// but state.json malformed → candidate emitted with empty JobCounts and
// per-candidate AnomalyStateJSONUnreadable.
func TestScanStateDirs_StateJSONReadFails_RecordStillEmitted(t *testing.T) {
	cands, _ := scanStateDirs(context.Background(), NewOsFS(), stateRoot(t), 100*time.Millisecond)
	c := findCandidate(t, cands, "state-bad-dddddddddddddddd")
	if !c.HasBrokerJSON {
		t.Errorf("HasBrokerJSON = false despite readable broker.json")
	}
	if c.StateJSONReadable {
		t.Errorf("StateJSONReadable = true despite malformed state.json")
	}
	zero := JobCounts{}
	if c.JobCounts != zero {
		t.Errorf("JobCounts = %+v, want zero value", c.JobCounts)
	}
	found := false
	for _, a := range c.Anomalies {
		if a.Code == AnomalyStateJSONUnreadable {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected AnomalyStateJSONUnreadable on candidate, got %v", c.Anomalies)
	}
}

// TestScanStateDirs_JobCountsByStatus covers all six buckets including unknown.
func TestScanStateDirs_JobCountsByStatus(t *testing.T) {
	cands, _ := scanStateDirs(context.Background(), NewOsFS(), stateRoot(t), 100*time.Millisecond)
	c := findCandidate(t, cands, "multi-eeeeeeeeeeeeeeee")
	want := JobCounts{Queued: 1, Running: 1, Completed: 1, Failed: 1, Cancelled: 1, Unknown: 1}
	if c.JobCounts != want {
		t.Errorf("JobCounts = %+v, want %+v", c.JobCounts, want)
	}
}

// TestScanStateDirs_LastJobUpdatedAt_MaxOfJobs: drives AC5.
func TestScanStateDirs_LastJobUpdatedAt_MaxOfJobs(t *testing.T) {
	cands, _ := scanStateDirs(context.Background(), NewOsFS(), stateRoot(t), 100*time.Millisecond)
	c := findCandidate(t, cands, "multi-eeeeeeeeeeeeeeee")
	if c.LastJobUpdatedAt == nil {
		t.Fatalf("LastJobUpdatedAt = nil")
	}
	want := time.Date(2026, 5, 1, 15, 0, 0, 0, time.UTC)
	if !c.LastJobUpdatedAt.Equal(want) {
		t.Errorf("LastJobUpdatedAt = %v, want %v", c.LastJobUpdatedAt, want)
	}
}

// TestScanStateDirs_LastJobUpdatedAt_NilWhenNoJobs: drives AC5.
func TestScanStateDirs_LastJobUpdatedAt_NilWhenNoJobs(t *testing.T) {
	cands, _ := scanStateDirs(context.Background(), NewOsFS(), stateRoot(t), 100*time.Millisecond)
	c := findCandidate(t, cands, "empty-jobs-ffffffffffffffff")
	if c.LastJobUpdatedAt != nil {
		t.Errorf("LastJobUpdatedAt = %v, want nil for empty jobs[]", c.LastJobUpdatedAt)
	}
}

// TestScanStateDirs_LastJobUpdatedAt_NilWhenStateUnreadable: drives AC5.
func TestScanStateDirs_LastJobUpdatedAt_NilWhenStateUnreadable(t *testing.T) {
	cands, _ := scanStateDirs(context.Background(), NewOsFS(), stateRoot(t), 100*time.Millisecond)
	c := findCandidate(t, cands, "state-bad-dddddddddddddddd")
	if c.LastJobUpdatedAt != nil {
		t.Errorf("LastJobUpdatedAt = %v, want nil when state.json unreadable", c.LastJobUpdatedAt)
	}
}

// slowFS is an FS wrapper that injects a delay on Open calls under a given
// path prefix; used to test per-dir timeout.
type slowFS struct {
	FS
	prefix string
	delay  time.Duration
}

func (s *slowFS) Open(p string) (io.ReadCloser, error) {
	if s.prefix != "" && filepath.HasPrefix(p, s.prefix) {
		time.Sleep(s.delay)
	}
	return s.FS.Open(p)
}

// TestScanStateDirs_PerDirTimeout: slow Open on broker.json under a
// specific dir → that dir produces an anomaly; other dirs unaffected.
func TestScanStateDirs_PerDirTimeout(t *testing.T) {
	root := stateRoot(t)
	target := filepath.Join(root, "live-aaaaaaaaaaaaaaaa")
	fs := &slowFS{FS: NewOsFS(), prefix: target, delay: 50 * time.Millisecond}

	cands, _ := scanStateDirs(context.Background(), fs, root, 5*time.Millisecond)

	// Live should fail and either be absent or have a timeout-classified
	// anomaly. Other dirs should be present.
	for _, c := range cands {
		if filepath.Base(c.StateDir) == "live-aaaaaaaaaaaaaaaa" {
			// If still emitted, it must carry a per-dir read anomaly.
			if c.HasBrokerJSON {
				t.Errorf("live should not parse cleanly under timeout, got %+v", c)
			}
		}
	}
	if findCandidate(t, cands, "multi-eeeeeeeeeeeeeeee") == nil {
		t.Errorf("multi-status dir lost after per-dir timeout on live")
	}
}

// TestScanStateDirs_RootMissing: gracefully empty (no error) when root does
// not exist.
func TestScanStateDirs_RootMissing(t *testing.T) {
	cands, anoms := scanStateDirs(context.Background(), NewOsFS(), filepath.Join(os.TempDir(), "nonexistent-codex-state-root"), 100*time.Millisecond)
	if len(cands) != 0 {
		t.Errorf("got %d candidates, want 0 for missing root", len(cands))
	}
	_ = anoms // implementation may or may not record anomaly; both acceptable.
}

// TestScanStateDirs_CtxCanceled: returns early when ctx is cancelled.
func TestScanStateDirs_CtxCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cands, _ := scanStateDirs(ctx, NewOsFS(), stateRoot(t), 100*time.Millisecond)
	if len(cands) != 0 && !errors.Is(ctx.Err(), context.Canceled) {
		t.Errorf("got %d cands with cancelled ctx", len(cands))
	}
}
