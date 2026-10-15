package gate

import (
	"runtime"
)

// HostFacts is what "auto" is worked out from.
type HostFacts struct {
	NCPU       int
	AvailMemMB int // memory the kernel would give a new process without paging; 0 = unknown
}

// ReadHostFacts reads this host: the cores, and the memory still available (hw.memsize × kern.memorystatus_level %).
func ReadHostFacts() HostFacts {
	f := HostFacts{NCPU: runtime.NumCPU()}
	if total, err := sysctlUint64("hw.memsize"); err == nil {
		if level, err := sysctlUint32("kern.memorystatus_level"); err == nil {
			f.AvailMemMB = availMemMB(total, level)
		}
	}
	return f
}

// availMemMB is the memory still available, in MB: the machine's bytes times the kernel's available percentage. 0 for a
// percentage that is not 1 to 100 (unknown). The shift is done first: Go's * / >> share one precedence, left to right.
func availMemMB(totalBytes uint64, levelPct uint32) int {
	if levelPct == 0 || levelPct > 100 {
		return 0
	}
	return int((totalBytes >> 20) * uint64(levelPct) / 100)
}

// Module 3, parallelism: how many cores one command may use. The result is capped by the memory its workers are
// estimated to need, so a full suite runs close to the core count and still leaves room on a loaded host.
type ParallelismConfig struct {
	Mode           string // "auto": a parameter set to auto is worked out from the host; "fixed": auto means the fixed defaults; "off": no cap
	ReserveCores   int    // auto: cores − reserve
	MemPerWorkerMB int    // auto: also at most available memory ÷ this (pnpm and anything else: 400)
	// Per tool, measured in G0 (2026-10-15): a go test package needs about 150 MB, a vitest worker about 700 MB.
	GoMemPerWorkerMB     int
	VitestMemPerWorkerMB int
	// VitestMaxWorkers caps the vitest auto: 8 workers ran 19 % faster than 3 but reached memory pressure level 2 and
	// 101 more tests timed out, and nothing between was measured, so the default is the measured 3.
	VitestMaxWorkers int
	GoP              Param
	VitestWorkers    Param
	PnpmWorkspace    Param
	// Class overrides ([gate.parallelism.heavy] / [gate.parallelism.light]): a parameter set there wins for that class.
	ByClass map[Class]ParallelismOverride
}

// ParallelismOverride is the part of the module a class sets for itself.
type ParallelismOverride struct {
	GoP, VitestWorkers, PnpmWorkspace *Param
}

const (
	fixedGoP, fixedVitest, fixedPnpm = 3, 3, 2 // what "auto" means in mode fixed
)

// pnpmAutoMax is the most that "auto" gives pnpm's --workspace-concurrency. pnpm runs several packages at once and each
// package's script has workers of its own (a vitest run alone is up to 3), so the product is what loads the host: four
// packages at a time was chosen as plenty for the workspaces here, not measured (G0 did not run pnpm -r); the memory
// division by mem_per_worker_mb still applies below it.
const pnpmAutoMax = 4

func parseParallelism(r raw) ParallelismConfig {
	c := ParallelismConfig{
		Mode:                 r.Enum("mode", "auto", "auto", "fixed", "off"),
		ReserveCores:         r.Int("reserve_cores", 2, 0, 64),
		MemPerWorkerMB:       r.Int("mem_per_worker_mb", 400, 50, 65536),
		GoMemPerWorkerMB:     r.Int("go_mem_per_worker_mb", 150, 50, 65536),
		VitestMemPerWorkerMB: r.Int("vitest_mem_per_worker_mb", 700, 50, 65536),
		VitestMaxWorkers:     r.Int("vitest_max_workers", 3, 1, 256),
		GoP:                  r.IntOrAuto("go_p", Param{Auto: true}, 1, 256),
		VitestWorkers:        r.IntOrAuto("vitest_workers", Param{Auto: true}, 1, 256),
		PnpmWorkspace:        r.IntOrAuto("pnpm_concurrency", Param{Auto: true}, 1, 256),
		ByClass:              map[Class]ParallelismOverride{},
	}
	for _, cl := range []Class{ClassHeavy, ClassLight} {
		sub := r.sub(string(cl))
		var o ParallelismOverride
		pick := func(key string) *Param {
			if !sub.has(key) {
				return nil
			}
			p := sub.IntOrAuto(key, Param{N: -1}, 1, 256) // N -1 is what a refused value leaves
			if !p.Auto && p.N == -1 {
				return nil // a value that was refused sets nothing: the base value stays
			}
			return &p
		}
		o.GoP, o.VitestWorkers, o.PnpmWorkspace = pick("go_p"), pick("vitest_workers"), pick("pnpm_concurrency")
		if o.GoP != nil || o.VitestWorkers != nil || o.PnpmWorkspace != nil {
			c.ByClass[cl] = o
		}
	}
	return c
}

// Limits are the caps a command gets; a zero is "no cap" (the person's own, or mode off).
type Limits struct {
	GoP, VitestWorkers, PnpmWorkspace int
	// Src is where each came from: SrcConfig for a number the person set, SrcAuto for one worked out, SrcDefault for
	// the fixed fallback.
	Src map[string]Source
}

// autoCount is cores − reserve, at most what the free memory holds at perWorkerMB each, at least 1.
func (c ParallelismConfig) autoCount(f HostFacts, perWorkerMB int) int {
	n := f.NCPU - c.ReserveCores
	if f.NCPU <= 0 {
		n = 1
	}
	if f.AvailMemMB > 0 {
		n = min(n, f.AvailMemMB/perWorkerMB)
	}
	return max(n, 1)
}

// Resolve works the caps out for a class on this host now.
func (c ParallelismConfig) Resolve(class Class, f HostFacts) Limits {
	l := Limits{Src: map[string]Source{}}
	if c.Mode == "off" {
		return l
	}
	o := c.ByClass[class]
	// pick: a number the person set is used as it is; an "auto" is worked out (cut to autoMax where one is given:
	// pnpm runs several packages at once, each with workers of its own, so a few are plenty), or in mode fixed is the
	// fixed default.
	pick := func(name string, base Param, over *Param, fixed, perWorkerMB, autoMax int) int {
		p := base
		if over != nil {
			p = *over
		}
		switch {
		case !p.Auto:
			l.Src[name] = SrcConfig
			return p.N
		case c.Mode == "fixed":
			l.Src[name] = SrcDefault
			return fixed
		}
		l.Src[name] = SrcAuto
		n := c.autoCount(f, perWorkerMB)
		if autoMax > 0 {
			n = min(n, autoMax)
		}
		return n
	}
	l.GoP = pick("go_p", c.GoP, o.GoP, fixedGoP, c.GoMemPerWorkerMB, 0)
	l.VitestWorkers = pick("vitest_workers", c.VitestWorkers, o.VitestWorkers, fixedVitest, c.VitestMemPerWorkerMB, c.VitestMaxWorkers)
	l.PnpmWorkspace = pick("pnpm_concurrency", c.PnpmWorkspace, o.PnpmWorkspace, fixedPnpm, c.MemPerWorkerMB, pnpmAutoMax)
	return l
}
