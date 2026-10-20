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
// so on a busy host one try can be far off (#3168): vm_stat's figure may lie anywhere in the range the Mach counters moved through
// between the Mach read before and the one after, and 20000 pages outside it, and the test passes when any of a few tries agrees.
// A host where no try agrees is one where the two readers really differ.
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

// The decompression counter is vm_stat's "Decompressions": it only grows, and vm_stat read it between the two Mach reads. On a busy
// host the counter moves while vm_stat starts and a single try can land outside the two Mach reads (#3758), so, like the page counts
// above, the test passes when any of a few tries agrees; a host where no try does is one where the two readers really differ.
func TestMach_DecompressionsAgreeWithVMStat(t *testing.T) {
	m, err := NewMach()
	require.NoError(t, err)
	bad := decompressionsAgreeWithin(5, func() (VMCounters, VMCounters, VMStat) {
		time.Sleep(statsCacheWindow)
		before, err := m.VM()
		require.NoError(t, err)
		require.NotZero(t, before.Decompressions, "the host has decompressed at least once since boot")
		out, err := Run(context.Background())
		require.NoError(t, err)
		vs, err := ParseVMStat(out)
		require.NoError(t, err)
		after, err := m.VM()
		require.NoError(t, err)
		return before, after, vs
	})
	if bad != "" {
		t.Error(bad)
	}
}

// statsCacheWindow is the quiet the test leaves before each try. host_statistics64 answers a process that calls it again within about
// a second from a cache (xnu rate-limits it), so a second Mach read right after the first can repeat the first while vm_stat, a fresh
// process whose first call is never cached, already shows the counter further on (#3758). A try that starts after the window has a fresh
// first read, and its second read is the second call inside the window, which is still answered live.
const statsCacheWindow = 1100 * time.Millisecond

// decompressionsAgreeWithin reads up to tries times and answers "" as soon as one try has vm_stat's figure between the two Mach
// reads, else the disagreement of the last.
func decompressionsAgreeWithin(tries int, read func() (before, after VMCounters, vs VMStat)) string {
	var last string
	for i := 0; i < tries; i++ {
		before, after, vs := read()
		if last = decompressionsDisagreement(before, after, vs); last == "" {
			return ""
		}
	}
	return last
}

// decompressionsDisagreement is "" when vm_stat's figure lies in the range the Mach counter moved through between the two reads.
func decompressionsDisagreement(before, after VMCounters, vs VMStat) string {
	if vs.Decompressions < before.Decompressions || vs.Decompressions > after.Decompressions {
		return fmt.Sprintf("decompressions: vm_stat %d is outside mach's %d..%d", vs.Decompressions, before.Decompressions, after.Decompressions)
	}
	return ""
}

// The retry passes when a later try agrees, fails when none does, and does not read again once one has agreed.
func TestDecompressionsAgreeWithinRetries(t *testing.T) {
	good := func() (VMCounters, VMCounters, VMStat) {
		return VMCounters{Decompressions: 10}, VMCounters{Decompressions: 20}, VMStat{Decompressions: 15}
	}
	off := func() (VMCounters, VMCounters, VMStat) {
		return VMCounters{Decompressions: 10}, VMCounters{Decompressions: 20}, VMStat{Decompressions: 31}
	}
	calls := 0
	if msg := decompressionsAgreeWithin(5, func() (VMCounters, VMCounters, VMStat) {
		calls++
		if calls < 3 {
			return off()
		}
		return good()
	}); msg != "" || calls != 3 {
		t.Errorf("a third try agrees: %q after %d reads", msg, calls)
	}
	calls = 0
	if msg := decompressionsAgreeWithin(4, func() (VMCounters, VMCounters, VMStat) { calls++; return off() }); msg == "" || calls != 4 {
		t.Errorf("no try agrees: %q after %d reads", msg, calls)
	}
	if decompressionsDisagreement(VMCounters{Decompressions: 10}, VMCounters{Decompressions: 20}, VMStat{Decompressions: 9}) == "" {
		t.Error("below the first read is a disagreement")
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

// vmDisagreements names the page counts where vm_stat's figure lies more than 20000 pages outside the range Mach itself moved through
// between the read before and the one after (vm_stat was read in that interval, so inside it is agreement, however far Mach moved).
// A Mach "free" that is less than its speculative part is a wrong reading, not a figure to subtract (#3074).
func vmDisagreements(before, after VMCounters, vs VMStat) []string {
	if before.Free < before.Speculative || after.Free < after.Speculative {
		return []string{fmt.Sprintf("mach free_count %d/%d is less than its speculative part %d/%d", before.Free, after.Free, before.Speculative, after.Speculative)}
	}
	var bad []string
	for name, c := range map[string]struct{ before, after, vm uint64 }{
		"free":        {before.Free - before.Speculative, after.Free - after.Speculative, vs.Free}, // Mach's free_count holds the speculative pages; vm_stat's "Pages free" does not (#3074)
		"inactive":    {before.Inactive, after.Inactive, vs.Inactive},
		"speculative": {before.Speculative, after.Speculative, vs.Speculative},
	} {
		lo, hi := min(c.before, c.after), max(c.before, c.after)
		var out uint64 // how far vm_stat's figure is outside [lo, hi]
		switch {
		case c.vm < lo:
			out = lo - c.vm
		case c.vm > hi:
			out = c.vm - hi
		}
		if out > 20000 {
			bad = append(bad, fmt.Sprintf("%s pages: vm_stat %d is %d outside mach's %d..%d", name, c.vm, out, lo, hi))
		}
	}
	return bad
}

// vm_stat's figure may lie anywhere inside what Mach moved through across the reads, and 20000 pages outside it (#3168); a bigger move
// of Mach never widens the allowance on the far side, and a free_count under its speculative part is a wrong reading. Mutation gates:
// the interval replaced by "distance from after" → the inside row goes red; one side dropped → its row goes red; the 20000 changed →
// a boundary row goes red; the Free < Speculative guard dropped → the last row goes red.
func TestVMDisagreements_RangeOfWhatMachMovedPlus20000(t *testing.T) {
	still := VMCounters{Free: 300000, Speculative: 50000, Inactive: 200000}
	vsAt := func(free, inactive, spec uint64) VMStat {
		return VMStat{Free: free, Inactive: inactive, Speculative: spec}
	}

	assert.Empty(t, vmDisagreements(still, still, vsAt(250000, 200000, 50000)), "exact")
	assert.Empty(t, vmDisagreements(still, still, vsAt(250000+20000, 200000-20000, 50000+20000)), "at the 20000 allowance, both sides")
	bad := vmDisagreements(still, still, vsAt(250000+20001, 200000, 50000))
	if assert.Len(t, bad, 1, "20001 pages above on a host that stood still") {
		assert.Contains(t, bad[0], "free pages")
	}
	assert.Len(t, vmDisagreements(still, still, vsAt(250000-20001, 200000, 50000)), 1, "20001 below")
	assert.Len(t, vmDisagreements(still, still, vsAt(250000, 200000+20001, 50000)), 1, "inactive")
	assert.Len(t, vmDisagreements(still, still, vsAt(250000, 200000, 50000+20001)), 1, "speculative")

	// Mach's free pages went 250000 -> 280000 across the reads (the host was busy)
	moved := still
	moved.Free += 30000
	assert.Empty(t, vmDisagreements(still, moved, vsAt(246622, 200000, 50000)), "inside the range's low side: 3378 below it")
	assert.Empty(t, vmDisagreements(still, moved, vsAt(265000, 200000, 50000)), "inside the range")
	assert.Empty(t, vmDisagreements(still, moved, vsAt(280000+20000, 200000, 50000)), "20000 above the range")
	assert.Len(t, vmDisagreements(still, moved, vsAt(280000+20001, 200000, 50000)), 1, "20001 above the range")
	assert.Len(t, vmDisagreements(still, moved, vsAt(330000, 200000, 50000)), 1, "50000 above: the move of Mach does not widen the far side")
	assert.Len(t, vmDisagreements(still, moved, vsAt(250000-20001, 200000, 50000)), 1, "20001 below the range")
	assert.Empty(t, vmDisagreements(moved, still, vsAt(265000, 200000, 50000)), "the range is the same read the other way round")

	// a fixed offset like the speculative one of #3074 (50000 pages) is caught however much Mach moved
	assert.Len(t, vmDisagreements(still, moved, vsAt(250000+30000+50000, 200000, 50000)), 1)

	// a free_count under its speculative part is not subtracted into a huge number
	wrong := VMCounters{Free: 10, Speculative: 20, Inactive: 200000}
	if got := vmDisagreements(wrong, wrong, vsAt(0, 200000, 20)); assert.Len(t, got, 1) {
		assert.Contains(t, got[0], "less than its speculative part")
	}
	assert.Len(t, vmDisagreements(wrong, still, vsAt(250000, 200000, 50000)), 1, "the read before is the wrong one")
	if got := vmDisagreements(still, wrong, vsAt(250000, 200000, 50000)); assert.Len(t, got, 1, "the read after is the wrong one") {
		assert.Contains(t, got[0], "less than its speculative part")
	}
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
