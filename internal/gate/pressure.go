package gate

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// PressureReader reads the host's pressure; a seam so tests inject a level and a rate.
type PressureReader interface {
	Read() (Pressure, error)
}

// sampleReuse is how long a sample in the cache file is reused by other gates; rateWindow how far back the rate looks.
const (
	sampleReuse = 5 * time.Second
	rateWindow  = 10 * time.Second
)

// HostReader reads the real host: `sysctl kern.memorystatus_vm_pressure_level` and the pageout and swapout counters
// of `vm_stat`. Samples are shared between concurrent gates through <dir>/pressure.json, so a queue of waiters does
// not fork vm_stat once each per poll.
type HostReader struct {
	Dir   string
	Now   func() time.Time
	Level func() (int, error)
	Pages func() (pageouts, swapouts uint64, err error)
}

// NewHostReader is the reader of this host.
func NewHostReader(dir string) *HostReader {
	return &HostReader{Dir: dir, Now: time.Now, Level: sysctlLevel, Pages: vmStatCounters}
}

type cachedSample struct {
	At       int64  `json:"at"` // unix ms
	Level    int    `json:"level"`
	Pageouts uint64 `json:"pageouts"`
	Swapouts uint64 `json:"swapouts"`
}

type sampleFile struct {
	Samples []cachedSample `json:"samples"`
}

func (r *HostReader) path() string { return filepath.Join(r.Dir, "pressure.json") }

func (r *HostReader) load() sampleFile {
	var f sampleFile
	if b, err := os.ReadFile(r.path()); err == nil {
		_ = json.Unmarshal(b, &f)
	}
	return f
}

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

// lockSampler takes the one-sampler lock without waiting; got is false when another gate is sampling.
func (r *HostReader) lockSampler() (unlock func(), got bool) {
	f, err := os.OpenFile(filepath.Join(r.Dir, "pressure.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return func() {}, true // no lock to be had (a directory the gate cannot write): sample alone
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		_ = f.Close()
		return func() {}, false
	}
	return func() { _ = f.Close() }, true
}

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
		for i := 0; i < 6; i++ {
			time.Sleep(50 * time.Millisecond)
			if p, ok := r.fresh(); ok {
				return p, nil
			}
		}
	} else if p, ok := r.fresh(); ok {
		return p, nil // another gate sampled while this one took the lock
	}
	now := r.Now()
	f := r.load()
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
	keep := f.Samples[:0:0]
	for _, s := range f.Samples {
		if now.Sub(time.UnixMilli(s.At)) <= 3*rateWindow && s.At <= cur.At {
			keep = append(keep, s)
		}
	}
	keep = append(keep, cur)
	if len(keep) > 16 {
		keep = keep[len(keep)-16:]
	}
	if b, err := json.Marshal(sampleFile{Samples: keep}); err == nil {
		tmp := r.path() + "." + strconv.Itoa(os.Getpid())
		if os.WriteFile(tmp, b, 0o644) == nil {
			if os.Rename(tmp, r.path()) != nil {
				_ = os.Remove(tmp)
			}
		}
	}
	return pressureOf(keep, cur), nil
}

// pressureOf is the level of cur and the rate against the newest sample that is at least a second older and within
// the window (the oldest in the window when none is a second older: a short baseline beats none).
func pressureOf(samples []cachedSample, cur cachedSample) Pressure {
	p := Pressure{Level: cur.Level}
	var base *cachedSample
	for i := range samples {
		s := &samples[i]
		age := time.UnixMilli(cur.At).Sub(time.UnixMilli(s.At))
		if age < time.Second || age > rateWindow {
			continue
		}
		if base == nil || s.At < base.At {
			base = s // the oldest inside the window: the longest baseline
		}
	}
	if base == nil {
		return p // no baseline yet: the rate is unknown, taken as 0 (the level still holds)
	}
	dt := float64(cur.At-base.At) / 1000
	if dt <= 0 {
		return p
	}
	d := float64(cur.Pageouts+cur.Swapouts) - float64(base.Pageouts+base.Swapouts)
	if d < 0 {
		d = 0
	}
	p.PageRate = d / dt
	return p
}

func sysctlLevel() (int, error) {
	v, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level")
	return int(v), err
}

// vmStatCounters reads Pageouts and Swapouts from vm_stat.
func vmStatCounters() (uint64, uint64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/vm_stat").Output()
	if err != nil {
		return 0, 0, err
	}
	return parseVMStat(out)
}

// parseVMStat takes the "Pageouts:" and "Swapouts:" lines of vm_stat's output.
func parseVMStat(out []byte) (pageouts, swapouts uint64, err error) {
	var gotP, gotS bool
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		name, val, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		name = strings.Trim(name, `" `)
		if name != "Pageouts" && name != "Swapouts" {
			continue
		}
		n, perr := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(val), "."), 10, 64)
		if perr != nil {
			return 0, 0, perr
		}
		if name == "Pageouts" {
			pageouts, gotP = n, true
		} else {
			swapouts, gotS = n, true
		}
	}
	if !gotP || !gotS {
		return 0, 0, errors.New("vm_stat has no Pageouts/Swapouts line")
	}
	return pageouts, swapouts, nil
}
