package hostmem

import (
	"errors"
	"fmt"
	"time"
)

// Fast is the one-second reading of the host's own load, taken with no fork: the kernel's pressure level, the swapouts counter
// and the load average from sysctl, the page counts, the pageouts and the CPU ticks from two Mach calls.
type Fast struct {
	At       time.Time
	CPU      float64 // real CPU use of the whole host since the previous reading, 0-100; meaningful when HaveCPU
	Mem      float64 // memory in use, 0-100 (what the sampler's vm_stat reading calls Mem)
	Level    int
	PageRate float64 // pageouts + swapouts per second over RateWindow
	Load1    float64
	NCPU     int
	// Missing names what this reading could not have and is therefore not to be believed: "cpu" (no Mach: the CPU figure is
	// absent, not 0), "pageouts" (the rate counts swapouts alone). Empty when the reading is whole.
	Missing []string
}

// MachReader is the two Mach calls (*Mach is the real one; a test supplies its own).
type MachReader interface {
	CPUTicks() (busy, total uint64, err error)
	VM() (VMCounters, error)
}

// FastSources are the reads a FastReader makes, as seams: the real ones are the sysctls and Mach.
type FastSources struct {
	Mach       MachReader // nil: loading libSystem failed (or not macOS)
	MachErr    error
	Level      func() (int, error)
	Swapouts   func() (uint64, error)
	Load1      func() (float64, error)
	NCPU       func() (int, error)
	PageSize   func() (uint64, error)
	MemBytes   func() (uint64, error)
	UsedMemPct func() (float64, error) // the kernel's own figure, for when the page counts cannot be had
}

// FastReader takes Fast readings. It keeps the previous CPU ticks and the recent paging counters, so one value belongs to one
// goroutine.
type FastReader struct {
	src      FastSources
	lastBusy uint64
	lastTot  uint64
	haveTick bool
	ring     []PageSample
	ringKeep time.Duration
}

// NewFastReader is a reader over the given sources.
func NewFastReader(src FastSources) *FastReader {
	return &FastReader{src: src, ringKeep: RateWindow + 2*time.Second}
}

// Read takes one reading at now. It errors only when nothing useful could be read (no level, no load, no ncpu); a reading
// that lacks the CPU or the pageouts is returned with Missing set.
func (r *FastReader) Read(now time.Time) (Fast, error) {
	f := Fast{At: now}
	var errs []error
	if v, err := r.src.Level(); err == nil {
		f.Level = v
	} else {
		errs = append(errs, err)
	}
	if v, err := r.src.Load1(); err == nil {
		f.Load1 = v
	} else {
		errs = append(errs, err)
	}
	if v, err := r.src.NCPU(); err == nil {
		f.NCPU = v
	} else {
		errs = append(errs, err)
	}
	if len(errs) == 3 {
		return Fast{}, fmt.Errorf("hostmem: nothing could be read: %w", errors.Join(errs...))
	}

	var pageouts uint64
	havePageouts := false
	haveMem := false
	if r.src.Mach != nil {
		if vm, err := r.src.Mach.VM(); err == nil {
			pageouts, havePageouts = vm.Pageouts, true
			// the same arithmetic as the sampler's Mem: (memory size - (free + inactive + speculative) pages) / memory size
			ps, perr := r.src.PageSize()
			total, terr := r.src.MemBytes()
			if perr == nil && terr == nil && ps > 0 && total > 0 {
				avail := float64(vm.Free+vm.Inactive+vm.Speculative) * float64(ps)
				f.Mem, haveMem = min(max(100*(1-avail/float64(total)), 0), 100), true
			}
		}
		if busy, total, err := r.src.Mach.CPUTicks(); err == nil {
			if r.haveTick && total > r.lastTot {
				f.CPU = 100 * float64(busy-r.lastBusy) / float64(total-r.lastTot)
				if f.CPU < 0 {
					f.CPU = 0
				}
			} else {
				f.Missing = append(f.Missing, "cpu") // the first reading has no earlier ticks to measure against
			}
			r.lastBusy, r.lastTot, r.haveTick = busy, total, true
		} else {
			f.Missing = append(f.Missing, "cpu")
		}
	} else {
		f.Missing = append(f.Missing, "cpu")
	}
	if !haveMem && r.src.UsedMemPct != nil {
		if v, err := r.src.UsedMemPct(); err == nil {
			f.Mem = v
		}
	}

	var swapouts uint64
	haveSwap := false
	if r.src.Swapouts != nil {
		if v, err := r.src.Swapouts(); err == nil {
			swapouts, haveSwap = v, true
		}
	}
	switch {
	case !havePageouts && !haveSwap:
		f.Missing = append(f.Missing, "pageouts")
	default:
		if !havePageouts {
			f.Missing = append(f.Missing, "pageouts") // swapouts alone
		}
		cur := PageSample{At: now, Total: pageouts + swapouts}
		f.PageRate = RateOver(r.ring, cur)
		keep := r.ring[:0]
		for _, s := range r.ring {
			if now.Sub(s.At) <= r.ringKeep && !s.At.After(now) {
				keep = append(keep, s)
			}
		}
		r.ring = append(keep, cur)
	}
	return f, nil
}
