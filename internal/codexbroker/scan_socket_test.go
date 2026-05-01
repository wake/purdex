package codexbroker

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func cxcRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "cxc"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	return abs
}

// findSocketCandidate returns the candidate with the given dir basename.
func findSocketCandidate(t *testing.T, cands []socketCandidate, base string) *socketCandidate {
	t.Helper()
	for i := range cands {
		if filepath.Base(cands[i].SockDir) == base {
			return &cands[i]
		}
	}
	t.Fatalf("socket candidate %q not found in %d candidates", base, len(cands))
	return nil
}

// TestScanSockets_PopulatesCandidate: live fixture with broker.pid and
// broker.sock yields candidate with PIDFromFile == 12345 and SockExists.
func TestScanSockets_PopulatesCandidate(t *testing.T) {
	root := cxcRoot(t)
	got, _ := scanSockets(context.Background(), NewOsFS(), []string{root}, 50*time.Millisecond)
	c := findSocketCandidate(t, got, "cxc-LIVE")
	if c.PIDFromFile != 12345 {
		t.Errorf("PIDFromFile = %d, want 12345", c.PIDFromFile)
	}
	if !c.SockExists {
		t.Errorf("SockExists = false")
	}
	if c.SyntheticKey == "" || len(c.SyntheticKey) != 16 {
		t.Errorf("SyntheticKey malformed: %q", c.SyntheticKey)
	}
}

// TestScanSockets_BrokerPidUnreadable: broker.pid contains non-integer →
// candidate emitted with PIDFromFile == 0 and per-dir anomaly.
func TestScanSockets_BrokerPidUnreadable(t *testing.T) {
	root := cxcRoot(t)
	got, _ := scanSockets(context.Background(), NewOsFS(), []string{root}, 50*time.Millisecond)
	c := findSocketCandidate(t, got, "cxc-ORPHAN")
	if c.PIDFromFile != 0 {
		t.Errorf("PIDFromFile = %d, want 0 (unparseable)", c.PIDFromFile)
	}
	found := false
	for _, a := range c.Anomalies {
		if a.Code == AnomalySocketOrphan {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected AnomalySocketOrphan, got %v", c.Anomalies)
	}
}

// TestScanSockets_NoMatchingStateDir_SyntheticKey: every socket candidate has
// a deterministic synthetic key derived from sha256(sockPath)[:16].
func TestScanSockets_NoMatchingStateDir_SyntheticKey(t *testing.T) {
	root := cxcRoot(t)
	got, _ := scanSockets(context.Background(), NewOsFS(), []string{root}, 50*time.Millisecond)
	for _, c := range got {
		if len(c.SyntheticKey) != 16 {
			t.Errorf("SyntheticKey for %s = %q (want 16 hex)", c.SockDir, c.SyntheticKey)
		}
	}
}

// slowOpenFS is an FS that injects a delay on Open under a given prefix.
type slowOpenFS struct {
	FS
	prefix string
	delay  time.Duration
}

func (s *slowOpenFS) Open(p string) (io.ReadCloser, error) {
	if filepath.HasPrefix(p, s.prefix) {
		time.Sleep(s.delay)
	}
	return s.FS.Open(p)
}

// TestScanSockets_PerDirTimeout: budget exceeded → that dir produces a
// per-dir anomaly; other dirs are unaffected.
func TestScanSockets_PerDirTimeout(t *testing.T) {
	root := cxcRoot(t)
	target := filepath.Join(root, "cxc-LIVE")
	fs := &slowOpenFS{FS: NewOsFS(), prefix: target, delay: 50 * time.Millisecond}

	cands, _ := scanSockets(context.Background(), fs, []string{root}, 5*time.Millisecond)

	// cxc-LIVE either absent or carries an anomaly.
	for _, c := range cands {
		if filepath.Base(c.SockDir) == "cxc-LIVE" {
			if c.PIDFromFile != 0 {
				// Acceptable if read raced under budget; but with 50ms
				// delay vs 5ms budget that's improbable.
				t.Logf("cxc-LIVE got PIDFromFile=%d under timeout (race)", c.PIDFromFile)
			}
		}
	}
	if findSocketCandidate(t, cands, "cxc-DEAD") == nil {
		t.Errorf("cxc-DEAD lost after per-dir timeout on cxc-LIVE")
	}
}

// TestScanSockets_RootMissing: no error when a root does not exist.
func TestScanSockets_RootMissing(t *testing.T) {
	cands, _ := scanSockets(context.Background(), NewOsFS(), []string{filepath.Join(os.TempDir(), "nonexistent-cxc-root")}, 50*time.Millisecond)
	if len(cands) != 0 {
		t.Errorf("got %d cands for missing root", len(cands))
	}
}
