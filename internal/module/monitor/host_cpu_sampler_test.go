package monitor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #2013: macOS 26 has no kern.cp_time. The darwin host CPU is the real utilisation, read from `iostat -c 2 -w 1` by a
// background sampler: started by the first snapshot request, stopped when nobody asks for a while, never waited for by a
// snapshot, never leaked past Stop.

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

const iostatSample = "testdata/iostat_darwin_c2_w1.txt"

func TestParseIostatCPU_RealSampleTakesTheSecondLine(t *testing.T) {
	raw, err := os.ReadFile(iostatSample)
	require.NoError(t, err)
	got, err := parseIostatCPU(string(raw))
	require.NoError(t, err)
	// second data line: us 12 sy 14 id 73 → 100 - id. The first line (since boot: id 80) must not be used.
	assert.Equal(t, 27.0, got)
}

func TestParseIostatCPU_Rejects(t *testing.T) {
	header := "              disk0       cpu    load average\n    KB/t  tps  MB/s  us sy id   1m   5m   15m\n"
	for name, raw := range map[string]string{
		"empty":                   "",
		"no header":               "   16.74  387  6.32  11  9 80  4.05 4.46 5.05\n   42.04   94  3.86  12 14 73  4.05 4.46 5.05\n",
		"only the since-boot row": header + "   16.74  387  6.32  11  9 80  4.05 4.46 5.05\n",
		"row shorter than header": header + "   16.74  387  6.32  11  9 80  4.05 4.46 5.05\n   42.04   94\n",
		"id not a number":         header + "   16.74  387  6.32  11  9 80  4.05 4.46 5.05\n   42.04   94  3.86  12 14 xx  4.05 4.46 5.05\n",
	} {
		_, err := parseIostatCPU(raw)
		assert.Error(t, err, name)
	}
}

func TestParseIostatCPU_ClampsTo0To100(t *testing.T) {
	header := "              disk0       cpu    load average\n    KB/t  tps  MB/s  us sy id   1m   5m   15m\n"
	over := header + "1 1 1 1 1 1 1 1 1\n1 1 1 0 0 -3 1 1 1\n"
	got, err := parseIostatCPU(over)
	require.NoError(t, err)
	assert.Equal(t, 100.0, got)
	under := header + "1 1 1 1 1 1 1 1 1\n1 1 1 0 0 140 1 1 1\n"
	got, err = parseIostatCPU(under)
	require.NoError(t, err)
	assert.Equal(t, 0.0, got)
}

type fakeIostat struct {
	runs  atomic.Int64
	mu    sync.Mutex
	out   func(n int64) (string, error)
	block chan struct{} // when non-nil the run waits for it or its context
	live  atomic.Int64  // runs in flight
}

func (f *fakeIostat) run(ctx context.Context) (string, error) {
	n := f.runs.Add(1)
	f.live.Add(1)
	defer f.live.Add(-1)
	f.mu.Lock()
	block, out := f.block, f.out
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if out != nil {
		return out(n)
	}
	return iostatWith(25), nil
}

func iostatWith(busy int) string {
	return fmt.Sprintf("              disk0       cpu    load average\n    KB/t  tps  MB/s  us sy id   1m   5m   15m\n"+
		"   16.74  387  6.32  11  9 80  4.05 4.46 5.05\n   42.04   94  3.86  %d 0 %d  4.05 4.46 5.05\n", busy, 100-busy)
}

type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (l *logSink) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logSink) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.lines)
}

func newTestSampler(f *fakeIostat, sink *logSink) *cpuSampler {
	s := newCPUSampler(f.run, sink.logf)
	s.idleAfter = time.Hour
	s.timeout = time.Second
	return s
}

// Mutation gate: the sampler starting at construction → runs before any request (red).
func TestCPUSampler_IsLazy(t *testing.T) {
	f := &fakeIostat{}
	s := newTestSampler(f, &logSink{})
	defer s.Close()
	time.Sleep(30 * time.Millisecond)
	assert.Equal(t, int64(0), f.runs.Load(), "no snapshot asked yet, so nothing is forked")

	_, err := s.CPUPercent(5 * time.Millisecond)
	assert.ErrorIs(t, err, errCPUPending, "the first answer is 'pending', not a wait")
	waitFor(t, "the first sample", func() bool { p, err := s.CPUPercent(5 * time.Millisecond); return err == nil && p == 25 })
}

// Mutation gate: CPUPercent waiting for the run → it blocks until the context/timeout (red, test hangs past 200 ms).
func TestCPUSampler_ARequestNeverWaitsForTheFork(t *testing.T) {
	f := &fakeIostat{block: make(chan struct{})}
	s := newTestSampler(f, &logSink{})
	defer s.Close()
	defer close(f.block)
	done := make(chan error, 1)
	go func() { _, err := s.CPUPercent(time.Millisecond); done <- err }()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, errCPUPending)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("CPUPercent waited for the running iostat")
	}
}

func TestCPUSampler_SamplesEveryIntervalWhileAsked(t *testing.T) {
	f := &fakeIostat{}
	s := newTestSampler(f, &logSink{})
	defer s.Close()
	_, _ = s.CPUPercent(5 * time.Millisecond)
	waitFor(t, "three runs", func() bool { _, _ = s.CPUPercent(5 * time.Millisecond); return f.runs.Load() >= 3 })
}

// Mutation gate: no idle check → the runs go on with nobody asking (red). A restart must not serve the old value.
func TestCPUSampler_StopsWhenIdleAndRestartsOnTheNextRequest(t *testing.T) {
	f := &fakeIostat{}
	s := newTestSampler(f, &logSink{})
	defer s.Close()
	s.idleAfter = 30 * time.Millisecond
	_, _ = s.CPUPercent(5 * time.Millisecond)
	waitFor(t, "a sample", func() bool { _, err := s.CPUPercent(5 * time.Millisecond); return err == nil })
	// stop asking
	waitFor(t, "the sampler to stop", func() bool {
		a := f.runs.Load()
		time.Sleep(60 * time.Millisecond)
		return f.runs.Load() == a
	})
	stopped := f.runs.Load()
	time.Sleep(40 * time.Millisecond)
	assert.Equal(t, stopped, f.runs.Load(), "an idle sampler forks nothing")

	f.mu.Lock()
	f.block = make(chan struct{}) // the restarted run is slow: the old value must not be served meanwhile
	f.mu.Unlock()
	_, err := s.CPUPercent(5 * time.Millisecond)
	assert.ErrorIs(t, err, errCPUPending, "after a stop the stale value is gone")
	waitFor(t, "the restart", func() bool { return f.runs.Load() > stopped })
	close(f.block)
}

// Mutation gate: Close not cancelling the run → Close waits for the 1 s timeout / the goroutine stays (red).
func TestCPUSampler_CloseKillsARunningForkAndNothingStartsAfter(t *testing.T) {
	f := &fakeIostat{block: make(chan struct{})}
	s := newTestSampler(f, &logSink{})
	_, _ = s.CPUPercent(time.Millisecond)
	waitFor(t, "a run in flight", func() bool { return f.live.Load() == 1 })

	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Close did not stop the running iostat")
	}
	assert.Equal(t, int64(0), f.live.Load(), "no fork left behind")

	runs := f.runs.Load()
	_, err := s.CPUPercent(time.Millisecond)
	assert.Error(t, err)
	assert.NotErrorIs(t, err, errCPUPending, "a closed sampler is not 'pending'")
	time.Sleep(30 * time.Millisecond)
	assert.Equal(t, runs, f.runs.Load(), "a request after Close starts nothing")
	s.Close() // twice is fine
}

// Mutation gate: no per-run timeout → a hung iostat is never reported (red).
func TestCPUSampler_AHungForkTimesOutIntoUnavailable(t *testing.T) {
	f := &fakeIostat{block: make(chan struct{})}
	defer close(f.block)
	s := newTestSampler(f, &logSink{})
	defer s.Close()
	s.timeout = 20 * time.Millisecond
	_, _ = s.CPUPercent(5 * time.Millisecond)
	waitFor(t, "the timeout to be reported", func() bool {
		_, err := s.CPUPercent(5 * time.Millisecond)
		return err != nil && !errors.Is(err, errCPUPending)
	})
}

// Mutation gate: logging every failure → many lines (red); never logging → none (red).
func TestCPUSampler_LogsOnlyTheTransitions(t *testing.T) {
	var phase atomic.Int64 // 0 ok, 1 failing, 2 ok, 3 failing
	f := &fakeIostat{out: func(int64) (string, error) {
		if p := phase.Load(); p == 1 || p == 3 {
			return "", errors.New("exec: iostat: not found")
		}
		return iostatWith(10), nil
	}}
	sink := &logSink{}
	s := newTestSampler(f, sink)
	defer s.Close()
	ask := func() (float64, error) { return s.CPUPercent(2 * time.Millisecond) }
	waitFor(t, "ok", func() bool { p, err := ask(); return err == nil && p == 10 })
	assert.Equal(t, 0, sink.count(), "a first success logs nothing")

	step := func(p int64, wantLines int, ok bool) {
		phase.Store(p)
		waitFor(t, fmt.Sprintf("phase %d", p), func() bool { _, err := ask(); return (err == nil) == ok && !errors.Is(err, errCPUPending) })
		start := f.runs.Load()
		waitFor(t, "more runs in the same state", func() bool { _, _ = ask(); return f.runs.Load() >= start+5 })
		assert.Equal(t, wantLines, sink.count(), "phase %d", p)
	}
	step(1, 1, false)
	step(2, 2, true)
	step(3, 3, false)
	assert.Contains(t, strings.Join(sink.lines, "\n"), "iostat")
}

// The host metrics read the sampler's percent; no counters are involved.
type fakePercentCollector struct {
	*fakeHostCollector
	percent float64
	err     error
	asked   []time.Duration
}

func (c *fakePercentCollector) CPUPercent(interval time.Duration) (float64, error) {
	c.asked = append(c.asked, interval)
	return c.percent, c.err
}

func TestHost_CPUFromAPercentSource(t *testing.T) {
	c := &fakePercentCollector{fakeHostCollector: newFakeHostCollector(), percent: 37.5}
	host := collectHostMetrics(context.Background(), NewHostMetricsState(c))
	require.NotNil(t, host.CPU.Percent)
	assert.Equal(t, 37.5, *host.CPU.Percent)
	assert.Equal(t, 0, c.cpuCalls, "the counter path is not used")

	c.err = errCPUPending
	host = collectHostMetrics(context.Background(), NewHostMetricsState(c))
	assert.Nil(t, host.CPU.Percent)
	assert.Equal(t, "pending", *host.CPU.UnavailableReason)

	c.err = errors.New("boom")
	host = collectHostMetrics(context.Background(), NewHostMetricsState(c))
	assert.Nil(t, host.CPU.Percent)
	assert.Equal(t, "host_cpu_unavailable", *host.CPU.UnavailableReason)
}

// The sampler is paced by the monitor's own refresh interval, and Stop closes it.
func TestModule_StopClosesTheHostCollector(t *testing.T) {
	c := &closableCollector{fakePercentCollector: &fakePercentCollector{fakeHostCollector: newFakeHostCollector()}}
	m := New(WithCollectors(Collectors{HostCollector: c, TmuxPaneLister: nil, ProcessTableCollector: nil}))
	require.NoError(t, m.Stop(context.Background()))
	assert.Equal(t, 1, c.closed)
}

type closableCollector struct {
	*fakePercentCollector
	closed int
}

func (c *closableCollector) Close() { c.closed++ }
