package monitor

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// errCPUPending is the answer while the sampler has no sample yet (just started, or restarted after an idle stop).
var errCPUPending = errors.New("host cpu: no sample yet")

var errCPUClosed = errors.New("host cpu: sampler closed")

const (
	// cpuSamplerIdleAfter: with no snapshot asking for this long the sampler stops forking; the next request restarts it.
	cpuSamplerIdleAfter = 60 * time.Second
	// cpuSamplerRunTimeout bounds one `iostat -c 2 -w 1` (it takes about a second).
	cpuSamplerRunTimeout = 3 * time.Second
)

// cpuSampler keeps the host's CPU utilisation fresh in the background, for hosts that have no cheap counters (macOS 26 has
// no kern.cp_time, #2013). It starts at the first request, repeats every interval the requests carry, stops when nobody
// has asked for idleAfter, and is stopped for good by Close, which also kills a run in flight. A request only reads the last
// result: it never waits for the fork.
type cpuSampler struct {
	run  func(context.Context) (string, error)
	logf func(string, ...any)
	// idleAfter and timeout are set before the first request.
	idleAfter time.Duration
	timeout   time.Duration

	mu        sync.Mutex
	closed    bool
	running   bool
	lastAsk   time.Time
	interval  time.Duration
	have      bool
	percent   float64
	err       error // the last run's failure, nil after a success
	reported  bool  // a state has been reported at least once (the first success is not news)
	failing   bool  // the state last reported
	wg        sync.WaitGroup
	baseCtx   context.Context
	baseStopf context.CancelFunc
}

func newCPUSampler(run func(context.Context) (string, error), logf func(string, ...any)) *cpuSampler {
	base, stop := context.WithCancel(context.Background())
	return &cpuSampler{run: run, logf: logf, idleAfter: cpuSamplerIdleAfter, timeout: cpuSamplerRunTimeout, baseCtx: base, baseStopf: stop}
}

// CPUPercent is the last sample's utilisation (0–100), errCPUPending before the first, or the last run's error.
func (s *cpuSampler) CPUPercent(interval time.Duration) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, errCPUClosed
	}
	s.lastAsk = time.Now()
	s.interval = interval
	if !s.running {
		s.running = true
		s.wg.Add(1)
		go s.loop()
	}
	switch {
	case s.err != nil:
		return 0, s.err
	case !s.have:
		return 0, errCPUPending
	}
	return s.percent, nil
}

// Close stops the sampler for good and waits for its goroutine; a run in flight is killed (its context is cancelled).
func (s *cpuSampler) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.baseStopf()
	s.wg.Wait()
}

func (s *cpuSampler) loop() {
	defer s.wg.Done()
	for {
		ctx, cancel := context.WithTimeout(s.baseCtx, s.timeout)
		out, err := s.run(ctx)
		cancel()
		if s.baseCtx.Err() != nil {
			return
		}
		var pct float64
		if err == nil {
			pct, err = parseIostatCPU(out)
		}
		interval := s.record(pct, err)

		timer := time.NewTimer(interval)
		select {
		case <-s.baseCtx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		// The idle decision and the "running" flag change under the same lock a request takes, so a request cannot
		// slip between "decided to stop" and "stopped" and be left without a sampler.
		s.mu.Lock()
		if time.Since(s.lastAsk) > s.idleAfter {
			s.running = false
			s.have, s.err = false, nil // a restart must not serve what was measured before the pause
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
	}
}

// record stores a run's result and logs the change of state, not the run. It returns the wait before the next run.
func (s *cpuSampler) record(pct float64, err error) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.have, s.err = false, err
	} else {
		s.have, s.percent, s.err = true, pct, nil
	}
	failing := err != nil
	if s.reported && failing != s.failing || !s.reported && failing {
		if failing {
			s.logf("[monitor] host cpu unavailable: %v", err)
		} else {
			s.logf("[monitor] host cpu available again")
		}
	}
	s.reported, s.failing = true, failing
	return s.interval
}

// parseIostatCPU reads the CPU utilisation out of `LC_ALL=C iostat -c 2 -w 1`: the header's second line names the columns,
// the first data line is the average since boot (not used), the second is the last second. The result is 100 - id,
// clamped to 0–100.
func parseIostatCPU(raw string) (float64, error) {
	var header []string
	var rows [][]string
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if header == nil {
			if indexOf(fields, "us") >= 0 && indexOf(fields, "sy") >= 0 && indexOf(fields, "id") >= 0 {
				header = fields
			}
			continue
		}
		rows = append(rows, fields)
	}
	if header == nil {
		return 0, errors.New("iostat: no us/sy/id header")
	}
	if len(rows) < 2 {
		return 0, fmt.Errorf("iostat: %d data rows, want 2", len(rows))
	}
	row := rows[len(rows)-1]
	idx := indexOf(header, "id")
	if len(row) != len(header) {
		return 0, fmt.Errorf("iostat: row has %d columns, header %d", len(row), len(header))
	}
	idle, err := strconv.ParseFloat(row[idx], 64)
	if err != nil {
		return 0, fmt.Errorf("iostat: id %q: %w", row[idx], err)
	}
	busy := 100 - idle
	if busy < 0 {
		busy = 0
	}
	if busy > 100 {
		busy = 100
	}
	return busy, nil
}

func indexOf(fields []string, want string) int {
	for i, f := range fields {
		if f == want {
			return i
		}
	}
	return -1
}
