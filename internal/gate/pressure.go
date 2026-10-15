package gate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wake/purdex/internal/hostmem"
)

// PressureReader reads the host's pressure; a seam so tests inject a level and a rate.
type PressureReader interface {
	Read() (Pressure, error)
}

// sampleReuse is how long a sample in the cache file is reused by other gates; rateWindow how far back the rate looks;
// staleLimit how old a cached sample may be and still stand in for a reading when the sampler is stuck.
const (
	sampleReuse = 5 * time.Second
	rateWindow  = hostmem.RateWindow
	staleLimit  = time.Minute
)

// HostReader reads the real host: `sysctl kern.memorystatus_vm_pressure_level` and the pageout and swapout counters
// of `vm_stat`. Samples are shared between concurrent gates through <dir>/pressure.json, so a queue of waiters does
// not fork vm_stat once each per poll.
type HostReader struct {
	Dir   string
	Now   func() time.Time
	Level func() (int, error)
	Pages func() (pageouts, swapouts uint64, err error)
	// Wait is how long a reader that did not get the sampler lock waits for the sampler's result (more than the 3 s a
	// vm_stat may take); a seam for tests.
	Wait time.Duration
}

// NewHostReader is the reader of this host.
func NewHostReader(dir string) *HostReader {
	return &HostReader{Dir: dir, Now: time.Now, Level: sysctlLevel, Pages: vmStatCounters, Wait: 4 * time.Second}
}

// The cache file and its writer's protocol live in hostmem, shared with the daemon, which writes the same file.
type (
	cachedSample = hostmem.Sample
	sampleFile   = hostmem.File
)

func (r *HostReader) cache() hostmem.Cache { return hostmem.Cache{Dir: r.Dir} }

func (r *HostReader) load() sampleFile { return r.cache().Load() }

// fresh is the pressure of the cached samples when the newest is younger than sampleReuse.
func (r *HostReader) fresh() (Pressure, bool) {
	f := r.load()
	n := len(f.Samples)
	if n == 0 {
		return Pressure{}, false
	}
	last := f.Samples[n-1]
	if age := r.Now().Sub(time.UnixMilli(last.At)); age >= 0 && age < sampleReuse {
		return pressureOf(f.Samples, last), true
	}
	return Pressure{}, false
}

// lockSampler takes the one-sampler lock without waiting; got is false when another gate (or the daemon) is sampling.
func (r *HostReader) lockSampler() (unlock func(), got bool) { return r.cache().TryLock() }

// Read gives the level and the rate. One gate at a time reads the host and writes the cache; the others wait a moment
// for its sample and read the host themselves only if none comes. A cache that is missing, torn or unwritable is
// just no baseline: the level alone decides until a second sample is there. The cache file is replaced by a rename,
// so a reader never sees half of one.
func (r *HostReader) Read() (Pressure, error) {
	if p, ok := r.fresh(); ok {
		return p, nil
	}
	unlock, got := r.lockSampler()
	defer unlock()
	if !got {
		// Another gate is sampling. Take nothing and write nothing without the lock: wait for its sample, and if it does
		// not come, give the last cached reading (older than the reuse time, but a reading) or an error.
		deadline := time.Now().Add(r.Wait)
		for time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
			if p, ok := r.fresh(); ok {
				return p, nil
			}
		}
		if f := r.load(); len(f.Samples) > 0 {
			last := f.Samples[len(f.Samples)-1]
			if age := r.Now().Sub(time.UnixMilli(last.At)); age >= 0 && age <= staleLimit {
				return pressureOf(f.Samples, last), nil
			}
		}
		// no recent reading: an unreadable pressure, which the caller counts (three in a row and it falls back)
		return Pressure{}, errors.New("another gate has been sampling the memory pressure for too long, and the cache is stale")
	}
	if p, ok := r.fresh(); ok {
		return p, nil // another gate sampled while this one took the lock
	}
	now := r.Now()
	level, err := r.Level()
	if err != nil {
		return Pressure{}, fmt.Errorf("pressure level: %w", err)
	}
	po, so, err := r.Pages()
	if err != nil {
		// The counters are unreadable: the level alone decides (no delta to compare), and nothing is cached, so a later
		// read tries the counters again.
		return Pressure{Level: level}, nil
	}
	cur := cachedSample{At: now.UnixMilli(), Level: level, Pageouts: po, Swapouts: so}
	keep := r.cache().Append(now, cur)
	return pressureOf(keep, cur), nil
}

// pressureOf is the level of cur and the rate against the cached samples.
func pressureOf(samples []cachedSample, cur cachedSample) Pressure {
	ps := make([]hostmem.PageSample, len(samples))
	for i, s := range samples {
		ps[i] = hostmem.PageSample{At: time.UnixMilli(s.At), Total: s.Pageouts + s.Swapouts}
	}
	return Pressure{Level: cur.Level, PageRate: hostmem.RateOver(ps, hostmem.PageSample{At: time.UnixMilli(cur.At), Total: cur.Pageouts + cur.Swapouts})}
}

func sysctlLevel() (int, error) { return hostmem.Level() }

// vmStatCounters reads Pageouts and Swapouts from vm_stat.
func vmStatCounters() (uint64, uint64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := hostmem.Run(ctx)
	if err != nil {
		return 0, 0, err
	}
	return parseVMStat(out)
}

// parseVMStat takes the "Pageouts:" and "Swapouts:" lines of vm_stat's output.
func parseVMStat(out []byte) (pageouts, swapouts uint64, err error) {
	v, err := hostmem.ParseVMStat(out)
	if err != nil {
		return 0, 0, err
	}
	if err := v.CountersErr(); err != nil {
		return 0, 0, err
	}
	return v.Pageouts, v.Swapouts, nil
}
