// Package slicecfg is the resource limits of wwd.slice, the systemd slice the worker's jobs run in on a Linux host (spec §5, #3549). The
// installer writes them as a drop-in; the daemon's config carries the same table so that an operator's override is a known key.
package slicecfg

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Limits are the slice's settings. A zero value is "the default"; a pointer is nil when unset (0 is not a valid weight or task count,
// but the strings and the pointers are treated the same way).
type Limits struct {
	CPUWeight     *int   `toml:"cpu_weight"`      // against the production services' default 100: under contention only
	CPUQuota      string `toml:"cpu_quota"`       // "300%": three cores
	MemoryMax     string `toml:"memory_max"`      // "6G", "512M", a byte count or "infinity"
	MemorySwapMax string `toml:"memory_swap_max"` // "0": a job that outgrows its memory is killed, not swapped
	TasksMax      *int   `toml:"tasks_max"`
}

// The defaults decided for mserv (4 cores, about 15 GB, about 2 GB of production services): jobs weigh half of them, take at most three
// cores and 6G, and never swap.
const (
	defaultCPUWeight     = 50
	defaultCPUQuota      = "300%"
	defaultMemoryMax     = "6G"
	defaultMemorySwapMax = "0"
	defaultTasksMax      = 4096
)

var (
	reQuota  = regexp.MustCompile(`^[1-9][0-9]{0,4}%$`)
	reMemory = regexp.MustCompile(`^([0-9]{1,12}[KMGT]?|infinity)$`)
)

// Validate refuses anything that is not a plain value: the file is read by systemd as root.
func (l Limits) Validate() error {
	if l.CPUWeight != nil && (*l.CPUWeight < 1 || *l.CPUWeight > 10000) {
		return fmt.Errorf("slice cpu_weight %d: 1 to 10000", *l.CPUWeight)
	}
	if l.CPUQuota != "" && !reQuota.MatchString(l.CPUQuota) {
		return fmt.Errorf("slice cpu_quota %q: a percentage such as 300%%", l.CPUQuota)
	}
	if l.MemoryMax != "" && !reMemory.MatchString(l.MemoryMax) {
		return fmt.Errorf("slice memory_max %q: a size such as 6G or 512M, a byte count or infinity", l.MemoryMax)
	}
	if l.MemorySwapMax != "" && !reMemory.MatchString(l.MemorySwapMax) {
		return fmt.Errorf("slice memory_swap_max %q: a size such as 0 or 1G, a byte count or infinity", l.MemorySwapMax)
	}
	if l.TasksMax != nil && (*l.TasksMax < 1 || *l.TasksMax > 4194304) {
		return fmt.Errorf("slice tasks_max %d: 1 to 4194304", *l.TasksMax)
	}
	return nil
}

// DropIn is the systemd drop-in text: the limits with the defaults filled in. Call Validate first.
func DropIn(l Limits) string {
	weight, tasks := defaultCPUWeight, defaultTasksMax
	if l.CPUWeight != nil {
		weight = *l.CPUWeight
	}
	if l.TasksMax != nil {
		tasks = *l.TasksMax
	}
	or := func(v, def string) string {
		if v == "" {
			return def
		}
		return v
	}
	var b strings.Builder
	b.WriteString("# Written by `wwd install`; the values come from [slice] in wwd's config.toml (defaults when absent). Edit the config, not this file.\n")
	b.WriteString("[Slice]\n")
	fmt.Fprintf(&b, "CPUWeight=%s\n", strconv.Itoa(weight))
	fmt.Fprintf(&b, "CPUQuota=%s\n", or(l.CPUQuota, defaultCPUQuota))
	fmt.Fprintf(&b, "MemoryMax=%s\n", or(l.MemoryMax, defaultMemoryMax))
	fmt.Fprintf(&b, "MemorySwapMax=%s\n", or(l.MemorySwapMax, defaultMemorySwapMax))
	fmt.Fprintf(&b, "TasksMax=%s\n", strconv.Itoa(tasks))
	return b.String()
}
