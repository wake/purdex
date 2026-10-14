package fleet

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/wake/purdex/internal/resources"
)

// Reading is what one probe of a host measured.
type Reading struct {
	OS    string  // "Darwin" or "Linux"
	NCPU  int     // logical cpus
	Load1 float64 // one-minute load average
	CPU   float64 // load1 / ncpu, percent (may pass 100)
	Mem   float64 // memory in use, percent
	Disk  float64 // used percent of DiskMount's volume
}

// Parse reads the output of Script. Output without the @@end marker, a missing section, or a figure that does not parse is
// an error (the runner reports it as `parse`).
func Parse(out string) (Reading, error) {
	sections, err := splitSections(out)
	if err != nil {
		return Reading{}, err
	}
	var r Reading
	r.OS = strings.TrimSpace(sections["uname"])
	if r.OS != "Darwin" && r.OS != "Linux" {
		return Reading{}, fmt.Errorf("unsupported os %q", r.OS)
	}
	if r.NCPU, err = strconv.Atoi(strings.TrimSpace(sections["ncpu"])); err != nil || r.NCPU <= 0 {
		return Reading{}, errors.New("bad cpu count")
	}
	if r.Load1, err = parseLoad1(sections["load"]); err != nil {
		return Reading{}, err
	}
	r.CPU = 100 * r.Load1 / float64(r.NCPU)
	if r.OS == "Darwin" {
		r.Mem, err = darwinMem(sections["memsize"], sections["mem"], r.NCPU)
	} else {
		r.Mem, err = linuxMem(sections["mem"])
	}
	if err != nil {
		return Reading{}, err
	}
	if r.Disk, err = parseDF(sections["disk"]); err != nil {
		return Reading{}, err
	}
	return r, nil
}

// splitSections cuts the output at its `@@name` lines. The @@end marker has to be there.
func splitSections(out string) (map[string]string, error) {
	sections := map[string]string{}
	cur, ended := "", false
	var b strings.Builder
	flush := func() {
		if cur != "" {
			sections[cur] = b.String()
		}
		b.Reset()
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if name, ok := strings.CutPrefix(line, "@@"); ok {
			flush()
			if name == "end" {
				ended = true
				break
			}
			cur = name
			continue
		}
		if cur != "" {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !ended {
		return nil, errors.New("missing end marker")
	}
	return sections, nil
}

// parseLoad1 reads the first load figure of `sysctl -n vm.loadavg` ("{ 1.5 1.2 1.1 }") or /proc/loadavg ("1.5 1.2 1.1 ...").
func parseLoad1(s string) (float64, error) {
	f := strings.Fields(strings.Trim(strings.TrimSpace(s), "{}"))
	if len(f) == 0 {
		return 0, errors.New("missing load")
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0, errors.New("bad load")
	}
	return v, nil
}

// darwinMem is the in-use percentage exactly as the resources sampler computes it for the daemon's own host: it hands the
// vm_stat page counts to resources.ComputeHost (the vm_stat reading itself is unexported there, so it is ported here).
func darwinMem(memsize, vmstat string, ncpu int) (float64, error) {
	total, err := strconv.ParseUint(strings.TrimSpace(memsize), 10, 64)
	if err != nil || total == 0 {
		return 0, errors.New("bad memsize")
	}
	raw := resources.HostRaw{NCPU: ncpu, MemBytes: total}
	var sawFree, sawInactive, sawSpeculative bool
	for _, line := range strings.Split(vmstat, "\n") {
		if strings.Contains(line, "page size of") {
			f := strings.Fields(line)
			for i, w := range f {
				if w == "of" && i+1 < len(f) {
					raw.PageSize, _ = strconv.ParseUint(f[i+1], 10, 64)
				}
			}
			continue
		}
		name, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(val), "."), 10, 64)
		if err != nil {
			continue
		}
		switch name {
		case "Pages free":
			raw.Free, sawFree = n, true
		case "Pages inactive":
			raw.Inactive, sawInactive = n, true
		case "Pages speculative":
			raw.Speculative, sawSpeculative = n, true
		}
	}
	if raw.PageSize == 0 || !sawFree || !sawInactive || !sawSpeculative || !raw.Usable() {
		return 0, errors.New("bad vm_stat")
	}
	return resources.ComputeHost(raw).Mem, nil
}

// linuxMem is (MemTotal - MemAvailable) / MemTotal, percent.
func linuxMem(meminfo string) (float64, error) {
	var total, avail uint64
	var sawTotal, sawAvail bool
	for _, line := range strings.Split(meminfo, "\n") {
		name, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(val)
		if len(f) == 0 {
			continue
		}
		n, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		switch name {
		case "MemTotal":
			total, sawTotal = n, true
		case "MemAvailable":
			avail, sawAvail = n, true
		}
	}
	if !sawTotal || !sawAvail || total == 0 {
		return 0, errors.New("bad meminfo")
	}
	return math.Min(math.Max(float64(total-min(avail, total))*100/float64(total), 0), 100), nil
}

// parseDF reads the Capacity column ("NN%") of the last line of `df -k` / `df -kP`; it is the fifth field on both OSes.
func parseDF(s string) (float64, error) {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) < 2 {
		return 0, errors.New("bad df")
	}
	f := strings.Fields(lines[len(lines)-1])
	if len(f) < 5 || !strings.HasSuffix(f[4], "%") {
		return 0, errors.New("bad df")
	}
	v, err := strconv.ParseFloat(strings.TrimSuffix(f[4], "%"), 64)
	if err != nil || v < 0 || v > 100 {
		return 0, errors.New("bad df")
	}
	return v, nil
}
