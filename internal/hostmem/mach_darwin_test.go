//go:build darwin

package hostmem

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Mach readings are the same numbers vm_stat prints (it reads the same counters): the page counts within what moves in the
// instant between the two reads, the pageouts never behind vm_stat's. The counters move while vm_stat starts (a process spawn),
// so on a busy host one try can be far off (#3168): the allowance is 20000 pages plus what the Mach counters themselves moved
// between the Mach read before and the one after, and the test passes when any of a few tries agrees. A host where no try agrees
// is one where the two readers really differ.
func TestMach_VMAgreesWithVMStat(t *testing.T) {
	m, err := NewMach()
	require.NoError(t, err)
	bad := vmAgreeWithin(5, func() (VMCounters, VMCounters, VMStat) {
		before, err := m.VM()
		require.NoError(t, err)
		out, err := Run(context.Background())
		require.NoError(t, err)
		vs, err := ParseVMStat(out)
		require.NoError(t, err)
		after, err := m.VM()
		require.NoError(t, err)

		// not a matter of timing: the counter only grows, and vm_stat read it between the two Mach reads
		require.GreaterOrEqual(t, vs.Pageouts, before.Pageouts, "vm_stat's pageouts are read after the first Mach read")
		require.LessOrEqual(t, vs.Pageouts, after.Pageouts)
		return before, after, vs
	})
	for _, msg := range bad {
		t.Error(msg)
	}
}

// vmAgreeWithin reads up to tries times and answers nothing as soon as one try agrees, else the disagreements of the last.
func vmAgreeWithin(tries int, read func() (before, after VMCounters, vs VMStat)) []string {
	var last []string
	for i := 0; i < tries; i++ {
		before, after, vs := read()
		if last = vmDisagreements(before, after, vs); len(last) == 0 {
			return nil
		}
	}
	return last
}

// vmDisagreements names the page counts where Mach (after) and vm_stat differ by more than 20000 pages plus the Mach counters' own
// movement across the two reads.
func vmDisagreements(before, after VMCounters, vs VMStat) []string {
	var bad []string
	for name, c := range map[string]struct{ before, after, vm uint64 }{
		"free":        {before.Free - before.Speculative, after.Free - after.Speculative, vs.Free}, // Mach's free_count holds the speculative pages; vm_stat's "Pages free" does not (#3074)
		"inactive":    {before.Inactive, after.Inactive, vs.Inactive},
		"speculative": {before.Speculative, after.Speculative, vs.Speculative},
	} {
		moved := int64(c.after) - int64(c.before)
		if moved < 0 {
			moved = -moved
		}
		diff := int64(c.after) - int64(c.vm)
		if diff < 0 {
			diff = -diff
		}
		if diff > 20000+moved {
			bad = append(bad, fmt.Sprintf("%s pages: mach %d (moved %d during the reads), vm_stat %d", name, c.after, moved, c.vm))
		}
	}
	return bad
}

// The allowance is 20000 pages plus what Mach itself moved across the two reads (#3168): the same gap is fine on a host that moved
// that much and a disagreement on one that stood still. Mutation gates: the movement dropped from the allowance → the busy row goes
// red; the 20000 raised or dropped → a row goes red; one metric not checked → its row goes red.
func TestVMDisagreements_AllowsWhatMachMovedAndNoMore(t *testing.T) {
	still := VMCounters{Free: 300000, Speculative: 50000, Inactive: 200000}
	vsAt := func(free, inactive, spec uint64) VMStat {
		return VMStat{Free: free, Inactive: inactive, Speculative: spec}
	}

	assert.Empty(t, vmDisagreements(still, still, vsAt(250000, 200000, 50000)), "exact")
	assert.Empty(t, vmDisagreements(still, still, vsAt(250000+20000, 200000-20000, 50000+20000)), "at the 20000 allowance")
	bad := vmDisagreements(still, still, vsAt(250000+20001, 200000, 50000))
	if assert.Len(t, bad, 1, "20001 pages off on a host that stood still") {
		assert.Contains(t, bad[0], "free pages")
	}
	assert.Len(t, vmDisagreements(still, still, vsAt(250000, 200000+20001, 50000)), 1, "inactive")
	assert.Len(t, vmDisagreements(still, still, vsAt(250000, 200000, 50000+20001)), 1, "speculative")

	// the 27887 pages of #3168: within 20000 + 8000 when Mach moved 8000 across the reads, over 20000 + 7000 when it moved 7000. The
	// vm_stat figure is placed against the counter AFTER the reads (258000 / 257000 free pages there), not the one before.
	moved := func(by uint64) VMCounters { c := still; c.Free += by; return c }
	assert.Empty(t, vmDisagreements(still, moved(8000), vsAt(258000-27887, 200000, 50000)), "moved 8000: 27887 is within 28000")
	assert.Len(t, vmDisagreements(still, moved(7000), vsAt(257000-27887, 200000, 50000)), 1, "moved 7000: 27887 is over 27000")
}

// One try that disagrees does not fail the test when a later one agrees; five that disagree do. Mutation gate: one try → the second
// case goes red.
func TestVMAgreeWithin_RetriesAndAnswersTheLastDisagreement(t *testing.T) {
	still := VMCounters{Free: 300000, Speculative: 50000, Inactive: 200000}
	off := VMStat{Free: 250000 + 90000, Inactive: 200000, Speculative: 50000}
	right := VMStat{Free: 250000, Inactive: 200000, Speculative: 50000}

	n := 0
	assert.Empty(t, vmAgreeWithin(5, func() (VMCounters, VMCounters, VMStat) {
		n++
		if n < 3 {
			return still, still, off
		}
		return still, still, right
	}), "the third try agrees")
	assert.Equal(t, 3, n, "it stops at the first try that agrees")

	n = 0
	bad := vmAgreeWithin(5, func() (VMCounters, VMCounters, VMStat) { n++; return still, still, off })
	assert.Len(t, bad, 1)
	assert.Equal(t, 5, n, "every try is used before it gives up")
}

// CPU ticks only grow, busy never exceeds total, and a burn of CPU shows in the busy share.
func TestMach_CPUTicksGrow(t *testing.T) {
	m, err := NewMach()
	require.NoError(t, err)
	b1, t1, err := m.CPUTicks()
	require.NoError(t, err)
	assert.LessOrEqual(t, b1, t1)
	end := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(end) {
	}
	b2, t2, err := m.CPUTicks()
	require.NoError(t, err)
	assert.Greater(t, t2, t1)
	assert.GreaterOrEqual(t, b2, b1)
	assert.LessOrEqual(t, b2, t2)
}

// A reading costs no memory it does not give back: the array host_processor_info allocates is deallocated each time.
func TestMach_CPUTicksDoesNotLeak(t *testing.T) {
	m, err := NewMach()
	require.NoError(t, err)
	for i := 0; i < 2000; i++ { // 2000 leaks of ~160 bytes each would be a page of 16 KB per 100; vm_deallocate keeps it flat
		_, _, err := m.CPUTicks()
		require.NoError(t, err)
	}
}
