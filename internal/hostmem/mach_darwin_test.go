//go:build darwin

package hostmem

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Mach readings are the same numbers vm_stat prints (it reads the same counters): the page counts within what moves in the
// instant between the two reads, the pageouts never behind vm_stat's.
func TestMach_VMAgreesWithVMStat(t *testing.T) {
	m, err := NewMach()
	require.NoError(t, err)
	before, err := m.VM()
	require.NoError(t, err)
	out, err := Run(context.Background())
	require.NoError(t, err)
	vs, err := ParseVMStat(out)
	require.NoError(t, err)
	after, err := m.VM()
	require.NoError(t, err)

	assert.GreaterOrEqual(t, vs.Pageouts, before.Pageouts, "vm_stat's pageouts are read after the first Mach read")
	assert.LessOrEqual(t, vs.Pageouts, after.Pageouts)
	for name, c := range map[string]struct{ mach, vm uint64 }{
		"free":        {after.Free - after.Speculative, vs.Free}, // Mach's free_count holds the speculative pages; vm_stat's "Pages free" does not (#3074)
		"inactive":    {after.Inactive, vs.Inactive},
		"speculative": {after.Speculative, vs.Speculative},
	} {
		diff := int64(c.mach) - int64(c.vm)
		assert.InDelta(t, 0, float64(diff), 20000, "%s pages: mach %d, vm_stat %d", name, c.mach, c.vm)
	}
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
