package monitor

import (
	"context"
	"errors"
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
	err       error         // the last run's failure, nil after a success
	reported  bool          // a state has been reported at least once (the first success is not news)
	failing   bool          // the state last reported
	wake      chan struct{} // a request asked for a shorter interval than the one the loop is waiting out
	wg        sync.WaitGroup
	baseCtx   context.Context
	baseStopf context.CancelFunc
}

func newCPUSampler(run func(context.Context) (string, error), logf func(string, ...any)) *cpuSampler {
	base, stop := context.WithCancel(context.Background())
	return &cpuSampler{run: run, logf: logf, idleAfter: cpuSamplerIdleAfter, timeout: cpuSamplerRunTimeout, baseCtx: base, baseStopf: stop, wake: make(chan struct{}, 1)}
}

// CPUPercent is the last sample's utilisation (0–100), errCPUPending before the first, or the last run's error.
func (s *cpuSampler) CPUPercent(interval time.Duration) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, errCPUClosed
	}
	s.lastAsk = time.Now()
	shorter := s.interval > 0 && interval < s.interval
	s.interval = interval
	if shorter {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
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
		started := time.Now()
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
		s.record(pct, err)

		// The interval is the period between two starts, not the pause after a run (a run takes about a second), and it
		// is read again whenever a request asks for a shorter one, so a setting change does not wait out the old wait.
	wait:
		for {
			s.mu.Lock()
			d := s.interval - time.Since(started)
			s.mu.Unlock()
			timer := time.NewTimer(max(d, 0))
			select {
			case <-s.baseCtx.Done():
				timer.Stop()
				return
			case <-s.wake:
				timer.Stop()
			case <-timer.C:
				break wait
			}
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

// record stores a run's result and logs the change of state, not the run.
func (s *cpuSampler) record(pct float64, err error) {
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
}
