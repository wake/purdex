package codexbroker

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newScannerFromFixtures(t *testing.T) *Scanner {
	t.Helper()
	stateAbs, _ := filepath.Abs(filepath.Join("testdata", "state"))
	cxcAbs, _ := filepath.Abs(filepath.Join("testdata", "cxc"))
	src := loadFixture(t, "one-broker.txt")
	lister := NewFakeProcessListerFromText(src)
	return NewScanner(ScannerOpts{
		FS:              NewOsFS(),
		Lister:          lister,
		PluginDataRoot:  stateAbs,
		SocketRoots:     []string{cxcAbs},
		TotalDeadline:   800 * time.Millisecond,
		StateDirBudget:  100 * time.Millisecond,
		SocketDirBudget: 50 * time.Millisecond,
	})
}

// TestScanner_HappyPath returns a Result with non-empty brokers and summary
// counts that align with the fixtures.
func TestScanner_HappyPath(t *testing.T) {
	s := newScannerFromFixtures(t)
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if res.Summary.Total == 0 {
		t.Errorf("Total = 0, want > 0")
	}
	if res.ScanDurationMs < 0 {
		t.Errorf("ScanDurationMs = %d", res.ScanDurationMs)
	}
	if res.DeadlineMs != 800 {
		t.Errorf("DeadlineMs = %d, want 800", res.DeadlineMs)
	}
}

// TestScanner_PartialOnTimeout: per-source delay > total deadline → Result
// has partial=true and scanSourceTimeouts populated.
func TestScanner_PartialOnTimeout(t *testing.T) {
	stateAbs, _ := filepath.Abs(filepath.Join("testdata", "state"))
	cxcAbs, _ := filepath.Abs(filepath.Join("testdata", "cxc"))
	src := loadFixture(t, "one-broker.txt")
	lister := NewFakeProcessListerFromText(src).WithDelay(500 * time.Millisecond)
	s := NewScanner(ScannerOpts{
		FS:              NewOsFS(),
		Lister:          lister,
		PluginDataRoot:  stateAbs,
		SocketRoots:     []string{cxcAbs},
		TotalDeadline:   50 * time.Millisecond,
		StateDirBudget:  100 * time.Millisecond,
		SocketDirBudget: 50 * time.Millisecond,
	})
	res, err := s.Scan(context.Background())
	if err != nil {
		// Acceptable: ps timed out → 503 sentinel
		if !errors.Is(err, ErrPsUnavailable) {
			t.Fatalf("Scan: unexpected err %v", err)
		}
		return
	}
	if !res.Partial {
		t.Errorf("expected Partial=true on timeout, got false")
	}
	if len(res.Summary.ScanSourceTimeouts) == 0 {
		t.Errorf("expected ScanSourceTimeouts to be populated, got %v", res.Summary.ScanSourceTimeouts)
	}
}

// TestScanner_PsFailureWithEmptyState_503: lister error AND no state/socket
// inventory → ErrPsUnavailable (HTTP 503).
//
// Per spec §4.3, 503 is only emitted when the entire inventory cannot be
// produced. With no PluginDataRoot/SocketRoots set, state-dir + socket
// scans return empty, so a ps failure is total failure.
func TestScanner_PsFailureWithEmptyState_503(t *testing.T) {
	emptyDir := t.TempDir() // empty plugin-data root
	lister := NewFakeProcessLister(nil).WithError(errors.New("ps not found"))
	s := NewScanner(ScannerOpts{
		FS:              NewOsFS(),
		Lister:          lister,
		PluginDataRoot:  emptyDir,
		SocketRoots:     []string{emptyDir},
		TotalDeadline:   800 * time.Millisecond,
		StateDirBudget:  100 * time.Millisecond,
		SocketDirBudget: 50 * time.Millisecond,
	})
	_, err := s.Scan(context.Background())
	if !errors.Is(err, ErrPsUnavailable) {
		t.Errorf("expected ErrPsUnavailable, got %v", err)
	}
}

// TestScanner_PsFailureWithStateData_200Partial: lister error AND state-dir
// scan produces inventory → 200 partial (not 503).
//
// Per spec §4.3 / R2 review API-7: a ps failure must NOT prevent returning
// whatever inventory state-dir / socket scans can still observe. The
// degradation surfaces via partial=true and "process" in scanSourceTimeouts.
func TestScanner_PsFailureWithStateData_200Partial(t *testing.T) {
	stateAbs, _ := filepath.Abs(filepath.Join("testdata", "state"))
	cxcAbs, _ := filepath.Abs(filepath.Join("testdata", "cxc"))
	lister := NewFakeProcessLister(nil).WithError(errors.New("ps not found"))
	s := NewScanner(ScannerOpts{
		FS:              NewOsFS(),
		Lister:          lister,
		PluginDataRoot:  stateAbs,
		SocketRoots:     []string{cxcAbs},
		TotalDeadline:   800 * time.Millisecond,
		StateDirBudget:  100 * time.Millisecond,
		SocketDirBudget: 50 * time.Millisecond,
	})
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatalf("expected nil error (200 partial), got %v", err)
	}
	if !res.Partial {
		t.Errorf("expected partial=true, got false")
	}
	foundProcessTimeout := false
	for _, t := range res.Summary.ScanSourceTimeouts {
		if t == "process" {
			foundProcessTimeout = true
			break
		}
	}
	if !foundProcessTimeout {
		t.Errorf("expected scanSourceTimeouts to contain \"process\", got %v",
			res.Summary.ScanSourceTimeouts)
	}
	if len(res.Brokers) == 0 {
		t.Errorf("expected non-empty brokers from state-dir/socket fallback, got 0")
	}
}

// TestScanner_ConcurrentScansAreIndependent: P1 deliberately does NOT
// coalesce concurrent scans (R1 review found that singleflight with a
// fixed key would expose later callers to the first caller's deadline,
// violating the per-request deadline contract). Each Scan call must
// drive its own ps invocation and honour its own context. P3 will add
// a TTL cache + singleflight at a higher layer if amplification
// becomes a problem.
func TestScanner_ConcurrentScansAreIndependent(t *testing.T) {
	stateAbs, _ := filepath.Abs(filepath.Join("testdata", "state"))
	cxcAbs, _ := filepath.Abs(filepath.Join("testdata", "cxc"))
	src := loadFixture(t, "one-broker.txt")
	var listerCalls atomic.Int64
	rawLister := NewFakeProcessListerFromText(src).WithDelay(20 * time.Millisecond)
	countingLister := &countingProcessLister{inner: rawLister, count: &listerCalls}
	s := NewScanner(ScannerOpts{
		FS:              NewOsFS(),
		Lister:          countingLister,
		PluginDataRoot:  stateAbs,
		SocketRoots:     []string{cxcAbs},
		TotalDeadline:   800 * time.Millisecond,
		StateDirBudget:  100 * time.Millisecond,
		SocketDirBudget: 50 * time.Millisecond,
	})

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.Scan(context.Background())
		}()
	}
	wg.Wait()
	if c := listerCalls.Load(); c != 5 {
		t.Errorf("concurrent independence broken: lister called %d times for 5 concurrent Scans, want 5", c)
	}
}

type countingProcessLister struct {
	inner ProcessLister
	count *atomic.Int64
}

func (c *countingProcessLister) List(ctx context.Context) ([]RawProcess, error) {
	c.count.Add(1)
	return c.inner.List(ctx)
}

// TestScanner_DeadlineRespected: total wall-clock < deadline + tolerance.
func TestScanner_DeadlineRespected(t *testing.T) {
	stateAbs, _ := filepath.Abs(filepath.Join("testdata", "state"))
	cxcAbs, _ := filepath.Abs(filepath.Join("testdata", "cxc"))
	src := loadFixture(t, "one-broker.txt")
	// Inject heavy state-dir delay.
	delayedFS := &slowFS{FS: NewOsFS(), prefix: stateAbs, delay: 500 * time.Millisecond}
	s := NewScanner(ScannerOpts{
		FS:              delayedFS,
		Lister:          NewFakeProcessListerFromText(src),
		PluginDataRoot:  stateAbs,
		SocketRoots:     []string{cxcAbs},
		TotalDeadline:   100 * time.Millisecond,
		StateDirBudget:  100 * time.Millisecond,
		SocketDirBudget: 50 * time.Millisecond,
	})
	start := time.Now()
	_, err := s.Scan(context.Background())
	elapsed := time.Since(start)
	if err != nil && !errors.Is(err, ErrPsUnavailable) {
		t.Fatalf("Scan: %v", err)
	}
	if elapsed > 300*time.Millisecond {
		t.Errorf("Scan exceeded deadline + tolerance: %v", elapsed)
	}
}

// TestScanner_P95UnderDeadline drives AC9: 50 successive scans against
// fixtures complete with p95 ≤ deadline (800 ms). Use a small fixture set
// (single ps row, ~6 state dirs, ~3 cxc dirs) — much smaller than mlab live
// (60 dirs, 123 procs) but sufficient for orchestration timing sanity.
//
// The main AC9 verification is on mlab live during PR-A live verification
// (Task O). This test guards against regressions in the orchestration loop.
func TestScanner_P95UnderDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip on windows; sleep granularity differs")
	}
	s := newScannerFromFixtures(t)
	const N = 50
	durs := make([]time.Duration, 0, N)
	for i := 0; i < N; i++ {
		start := time.Now()
		_, err := s.Scan(context.Background())
		if err != nil {
			t.Fatalf("Scan[%d]: %v", i, err)
		}
		durs = append(durs, time.Since(start))
	}
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	idx := N * 95 / 100
	if idx >= N {
		idx = N - 1
	}
	p95 := durs[idx]
	if p95 > 800*time.Millisecond {
		t.Errorf("p95 = %v, want ≤ 800ms", p95)
	}
}
