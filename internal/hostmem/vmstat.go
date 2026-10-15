// Package hostmem is the one place that knows how the host's memory is read: the kernel's pressure level, the counters of
// `vm_stat`, and the page rate worked out from them. The gate, the daemon's resource sampler and the monitor module all
// read memory; they used to carry a copy of the vm_stat parser each.
package hostmem

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// VMStat is what the callers use of vm_stat's output. A line that is not in the output leaves its number 0; HaveMemory and
// HaveCounters say whether the lines a caller needs were all there (a 0 must not pass for a full host or a quiet one).
type VMStat struct {
	PageSize, Free, Inactive, Speculative uint64
	// Pageouts and Swapouts are the running counters of pages written out since boot.
	Pageouts, Swapouts uint64
	// HaveMemory: the page size and the free, inactive and speculative counts were there. HaveCounters: both counters were.
	HaveMemory, HaveCounters bool
}

// ParseVMStat reads vm_stat output. It fails only on an unreadable output or on a number it cannot read in a line it uses;
// what is missing is for MemoryErr and CountersErr to say.
func ParseVMStat(raw []byte) (VMStat, error) {
	var v VMStat
	var sawFree, sawInactive, sawSpeculative, sawPageouts, sawSwapouts bool
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "page size of") {
			fields := strings.Fields(line)
			for i, field := range fields {
				if field == "of" && i+1 < len(fields) {
					v.PageSize, _ = strconv.ParseUint(fields[i+1], 10, 64)
				}
			}
			continue
		}
		name, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		var dst *uint64
		var saw *bool
		switch strings.Trim(name, `" `) {
		case "Pages free":
			dst, saw = &v.Free, &sawFree
		case "Pages inactive":
			dst, saw = &v.Inactive, &sawInactive
		case "Pages speculative":
			dst, saw = &v.Speculative, &sawSpeculative
		case "Pageouts":
			dst, saw = &v.Pageouts, &sawPageouts
		case "Swapouts":
			dst, saw = &v.Swapouts, &sawSwapouts
		default:
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(val), "."), 10, 64)
		if err != nil {
			return VMStat{}, fmt.Errorf("vm_stat: %s: %w", strings.Trim(name, `" `), err)
		}
		*dst, *saw = n, true
	}
	if err := sc.Err(); err != nil {
		return VMStat{}, err
	}
	v.HaveMemory = v.PageSize > 0 && sawFree && sawInactive && sawSpeculative
	v.HaveCounters = sawPageouts && sawSwapouts
	return v, nil
}

// MemoryErr is nil when the page size and the free, inactive and speculative counts were all in the output.
func (v VMStat) MemoryErr() error {
	switch {
	case v.PageSize == 0:
		return errors.New("vm_stat: missing page size")
	case !v.HaveMemory:
		return errors.New("vm_stat: missing free, inactive or speculative pages")
	}
	return nil
}

// CountersErr is nil when both the Pageouts and the Swapouts line were in the output.
func (v VMStat) CountersErr() error {
	if !v.HaveCounters {
		return errors.New("vm_stat has no Pageouts/Swapouts line")
	}
	return nil
}

// Run forks vm_stat and returns its output; ctx bounds it.
func Run(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "/usr/bin/vm_stat").Output()
}
