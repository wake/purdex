package core

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Startup timing lines are pure observation (#1767): they never change what
// runs or in which order, and carry only module names and durations.

const (
	// slowModuleThreshold is the single-module duration at or above which an
	// extra warning line is logged.
	slowModuleThreshold = time.Second
	// foldBelow is the duration under which a module is folded into "others".
	foldBelow = 5 * time.Millisecond
)

type moduleTiming struct {
	Name string
	Dur  time.Duration
}

// StepTiming is one named step of a module's Start, in report order.
type StepTiming struct {
	Name string
	Dur  time.Duration
}

func fmtMs(d time.Duration) string { return fmt.Sprintf("%dms", d.Milliseconds()) }

// formatModuleTimings renders "N modules in Xms: a=1ms b=2ms others=3ms":
// descending by duration, modules under foldBelow summed into others.
func formatModuleTimings(durs []moduleTiming, total time.Duration) string {
	sorted := append([]moduleTiming(nil), durs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Dur > sorted[j].Dur })
	var parts []string
	var others time.Duration
	folded := false
	for _, t := range sorted {
		if t.Dur < foldBelow {
			others += t.Dur
			folded = true
			continue
		}
		parts = append(parts, t.Name+"="+fmtMs(t.Dur))
	}
	if folded {
		parts = append(parts, "others="+fmtMs(others))
	}
	return fmt.Sprintf("%d modules in %s: %s", len(durs), fmtMs(total), strings.Join(parts, " "))
}

// FormatStepTimings renders steps in the given order as "a=1ms b=2ms".
func FormatStepTimings(steps []StepTiming) string {
	parts := make([]string, len(steps))
	for i, s := range steps {
		parts[i] = s.Name + "=" + fmtMs(s.Dur)
	}
	return strings.Join(parts, " ")
}

// clock / logger tolerate a Core built as a literal (nil now / logf).
func (c *Core) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *Core) logLine(format string, args ...any) {
	if c.logf != nil {
		c.logf(format, args...)
	}
}

// logPhaseTimings emits the per-phase summary (and slow-module warnings).
// failedAt != "" marks a phase that stopped at that module.
func (c *Core) logPhaseTimings(phase string, durs []moduleTiming, total time.Duration, failedAt string) {
	if len(durs) == 0 {
		return
	}
	for _, t := range durs {
		if t.Dur >= slowModuleThreshold {
			c.logLine("startup: slow module %s: %s took %s", phase, t.Name, fmtMs(t.Dur))
		}
	}
	if failedAt != "" {
		c.logLine("startup: %s failed at %s, %s", phase, failedAt, formatModuleTimings(durs, total))
		return
	}
	c.logLine("startup: %s %s", phase, formatModuleTimings(durs, total))
}
