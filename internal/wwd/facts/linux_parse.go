package facts

import (
	"bufio"
	"errors"
	"strconv"
	"strings"
)

// The parsers of the Linux /proc and /sys texts. They take text, not paths, and
// carry no build tag, so every platform's tests can feed them fixtures.

// ParsePSI reads a pressure file ("some avg10=0.00 avg60=0.00 avg300=0.00
// total=0" and the same for "full") into the three numbers the record keeps.
func ParsePSI(text string) (some10, some60, full10 float64, err error) {
	seen := false
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || (f[0] != "some" && f[0] != "full") {
			continue
		}
		for _, kv := range f[1:] {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || (k != "avg10" && k != "avg60") {
				continue
			}
			n, perr := strconv.ParseFloat(v, 64)
			if perr != nil {
				return 0, 0, 0, perr
			}
			seen = true
			switch {
			case f[0] == "some" && k == "avg10":
				some10 = n
			case f[0] == "some" && k == "avg60":
				some60 = n
			case f[0] == "full" && k == "avg10":
				full10 = n
			}
		}
	}
	if !seen {
		return 0, 0, 0, errors.New("pressure: no some/full lines")
	}
	return some10, some60, full10, nil
}

// Meminfo is the part of /proc/meminfo the record uses, in bytes.
type Meminfo struct {
	Total, Available, SwapTotal, SwapFree uint64
	HasAvailable                          bool
	// Active, Inactive and Cached (the file cache), each with whether the kernel gave the line.
	Active, Inactive, Cached          uint64
	HasActive, HasInactive, HasCached bool
}

func ParseMeminfo(text string) (Meminfo, error) {
	var m Meminfo
	haveTotal := false
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		k, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		kb, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		switch k {
		case "MemTotal":
			m.Total, haveTotal = kb<<10, true
		case "MemAvailable":
			m.Available, m.HasAvailable = kb<<10, true
		case "SwapTotal":
			m.SwapTotal = kb << 10
		case "SwapFree":
			m.SwapFree = kb << 10
		case "Active":
			m.Active, m.HasActive = kb<<10, true
		case "Inactive":
			m.Inactive, m.HasInactive = kb<<10, true
		case "Cached":
			m.Cached, m.HasCached = kb<<10, true
		}
	}
	if !haveTotal {
		return m, errors.New("meminfo: no MemTotal")
	}
	return m, nil
}

// ParseStatmRSS is the resident set in bytes from /proc/self/statm (size, resident, ... in pages).
func ParseStatmRSS(text string, pageSize uint64) (uint64, error) {
	f := strings.Fields(text)
	if len(f) < 2 {
		return 0, errors.New("statm: fewer than two fields")
	}
	pages, err := strconv.ParseUint(f[1], 10, 64)
	if err != nil {
		return 0, err
	}
	return pages * pageSize, nil
}

// rawMemFromMeminfo is the Linux memory reading of a parsed meminfo (the production-slice pressure is added by the caller). Swap used
// and total come from the same read; the breakdown is nil for a line the kernel did not give, and wired is always nil (no such figure).
func rawMemFromMeminfo(mi Meminfo, pageSize uint64) RawMem {
	m := RawMem{TotalBytes: mi.Total, PageSize: pageSize, Linux: true, AvailableBytes: mi.Available}
	used, total := mi.SwapTotal-min(mi.SwapFree, mi.SwapTotal), mi.SwapTotal
	m.SwapUsedBytes, m.SwapTotalBytes = &used, &total
	if mi.HasActive {
		m.ActiveBytes = &mi.Active
	}
	if mi.HasInactive {
		m.InactiveBytes = &mi.Inactive
	}
	if mi.HasCached {
		m.FileCacheBytes = &mi.Cached
	}
	return m
}

// ParseCPUStat reads the aggregate "cpu" line of /proc/stat: busy and total
// ticks, cumulative since boot. idle and iowait count as idle.
func ParseCPUStat(text string) (busy, total uint64, ncpu int, err error) {
	found := false
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if f[0] == "cpu" {
			var sum, idle uint64
			for i, s := range f[1:] {
				n, perr := strconv.ParseUint(s, 10, 64)
				if perr != nil {
					return 0, 0, 0, perr
				}
				// user nice system idle iowait irq softirq steal; guest and
				// guest_nice are already inside user and nice
				if i >= 8 {
					break
				}
				sum += n
				if i == 3 || i == 4 {
					idle += n
				}
			}
			busy, total, found = sum-idle, sum, true
		} else if strings.HasPrefix(f[0], "cpu") {
			ncpu++
		}
	}
	if !found {
		return 0, 0, 0, errors.New("stat: no cpu line")
	}
	return busy, total, ncpu, nil
}

func ParseLoadavg(text string) (l1, l5 float64, err error) {
	f := strings.Fields(text)
	if len(f) < 2 {
		return 0, 0, errors.New("loadavg: too few fields")
	}
	if l1, err = strconv.ParseFloat(f[0], 64); err != nil {
		return 0, 0, err
	}
	l5, err = strconv.ParseFloat(f[1], 64)
	return l1, l5, err
}

// ParseVmstat returns the cumulative pgpgout (KiB written to block devices, mostly
// file write-back) and pswpout (pages swapped out).
func ParseVmstat(text string) (pgpgoutKiB, pswpout uint64, err error) {
	var haveOut, haveSwap bool
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		switch k {
		case "pgpgout":
			if pgpgoutKiB, err = strconv.ParseUint(strings.TrimSpace(v), 10, 64); err != nil {
				return 0, 0, err
			}
			haveOut = true
		case "pswpout":
			if pswpout, err = strconv.ParseUint(strings.TrimSpace(v), 10, 64); err != nil {
				return 0, 0, err
			}
			haveSwap = true
		}
	}
	if !haveOut || !haveSwap {
		return 0, 0, errors.New("vmstat: no pgpgout or pswpout")
	}
	return pgpgoutKiB, pswpout, nil
}
