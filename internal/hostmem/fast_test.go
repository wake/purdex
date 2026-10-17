package hostmem

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fakeSources() FastSources {
	return FastSources{
		Level:      func() (int, error) { return 2, nil },
		Load1:      func() (float64, error) { return 3.5, nil },
		NCPU:       func() (int, error) { return 10, nil },
		PageSize:   func() (uint64, error) { return 16384, nil },
		MemBytes:   func() (uint64, error) { return 16 << 30, nil },
		UsedMemPct: func() (float64, error) { return 61, nil },
	}
}

// Without Mach: no CPU figure (named, not 0), the kernel's own memory figure, the rate from swapouts alone, and the reading still
// carries the level, the load and the cpu count.
func TestFastReader_WithoutMachItSaysWhatIsMissing(t *testing.T) {
	var swaps uint64 = 1000
	src := fakeSources()
	src.MachErr = errors.New("no libSystem")
	src.Swapouts = func() (uint64, error) { return swaps, nil }
	r := NewFastReader(src)
	t0 := time.Unix(10_000, 0)

	f, err := r.Read(t0)
	require.NoError(t, err)
	assert.Equal(t, 2, f.Level)
	assert.Equal(t, 3.5, f.Load1)
	assert.Equal(t, 10, f.NCPU)
	assert.Equal(t, 61.0, f.Mem, "the kernel's figure stands in")
	assert.ElementsMatch(t, []string{"cpu", "pageouts"}, f.Missing)
	assert.Equal(t, 0.0, f.PageRate, "no baseline yet")

	swaps += 300
	f, err = r.Read(t0.Add(3 * time.Second))
	require.NoError(t, err)
	assert.InDelta(t, 100, f.PageRate, 0.001, "300 pages in 3 s, from swapouts alone")
	assert.Contains(t, f.Missing, "pageouts")
}

func TestFastReader_NothingReadableIsAnError(t *testing.T) {
	no := errors.New("no")
	r := NewFastReader(FastSources{
		Level: func() (int, error) { return 0, no }, Load1: func() (float64, error) { return 0, no }, NCPU: func() (int, error) { return 0, no },
	})
	_, err := r.Read(time.Now())
	assert.Error(t, err)
}

// A part that fails leaves the others in the reading.
func TestFastReader_OnePartFailingKeepsTheRest(t *testing.T) {
	src := fakeSources()
	src.Level = func() (int, error) { return 0, errors.New("no level") }
	f, err := NewFastReader(src).Read(time.Now())
	require.NoError(t, err)
	assert.Equal(t, 0, f.Level)
	assert.Equal(t, 3.5, f.Load1)
}

type fakeMach struct {
	busy, total uint64
	vm          VMCounters
	cpuErr      error
	vmErr       error
}

func (f *fakeMach) CPUTicks() (uint64, uint64, error) { return f.busy, f.total, f.cpuErr }
func (f *fakeMach) VM() (VMCounters, error)           { return f.vm, f.vmErr }

// CPU is the busy share of the ticks since the previous reading (not since boot); the first reading has no earlier ticks and says
// so rather than reporting the boot-long average.
func TestFastReader_CPUIsTheShareSinceThePreviousReading(t *testing.T) {
	m := &fakeMach{busy: 1000, total: 4000, vm: VMCounters{Free: 100, Pageouts: 10}}
	src := fakeSources()
	src.Mach = m
	r := NewFastReader(src)
	t0 := time.Unix(10_000, 0)

	f, err := r.Read(t0)
	require.NoError(t, err)
	assert.Contains(t, f.Missing, "cpu", "the first reading has nothing to measure against")

	m.busy, m.total = 1300, 4400 // +300 busy of +400 ticks
	f, err = r.Read(t0.Add(time.Second))
	require.NoError(t, err)
	assert.InDelta(t, 75, f.CPU, 0.001)
	assert.NotContains(t, f.Missing, "cpu")

	m.busy, m.total = 1300, 4400 // no ticks went by: no new figure, not a division by zero
	f, err = r.Read(t0.Add(2 * time.Second))
	require.NoError(t, err)
	assert.Contains(t, f.Missing, "cpu")

	m.cpuErr = errors.New("kern_return 4")
	f, err = r.Read(t0.Add(3 * time.Second))
	require.NoError(t, err)
	assert.Contains(t, f.Missing, "cpu")
}

// Mem is the sampler's own arithmetic: (memory size - (free + inactive + speculative) pages x page size) / memory size, where the
// sampler's free is vm_stat's (without the speculative pages) and Mach's Free holds them already: Free + Inactive, no second Speculative.
func TestFastReader_MemIsTheSamplersArithmetic(t *testing.T) {
	m := &fakeMach{vm: VMCounters{Free: 124_288, Inactive: 200_000, Speculative: 24_288, Pageouts: 0}} // 324288 pages x 16 KiB ~ 4.95 GiB
	src := fakeSources()
	src.Mach = m
	f, err := NewFastReader(src).Read(time.Unix(1, 0))
	require.NoError(t, err)
	avail := float64(324_288) * 16384
	assert.InDelta(t, 100*(1-avail/float64(16<<30)), f.Mem, 0.001)
	assert.NotEqual(t, 61.0, f.Mem, "the page counts, not the kernel's fallback figure")
}

// The rate counts pageouts and swapouts together, over the window, and the swapouts-only reading names what it lacks.
func TestFastReader_PageRateIsPageoutsPlusSwapouts(t *testing.T) {
	m := &fakeMach{vm: VMCounters{Pageouts: 1000}}
	var swaps uint64 = 5000
	src := fakeSources()
	src.Mach = m
	src.Swapouts = func() (uint64, error) { return swaps, nil }
	r := NewFastReader(src)
	t0 := time.Unix(10_000, 0)
	f, _ := r.Read(t0)
	assert.NotContains(t, f.Missing, "pageouts")
	m.vm.Pageouts += 200
	swaps += 100
	f, _ = r.Read(t0.Add(3 * time.Second))
	assert.InDelta(t, 100, f.PageRate, 0.001, "(200 + 100) pages in 3 s")
	// a counter that went back (a reboot) is 0, not a huge wrap
	m.vm.Pageouts = 5
	f, _ = r.Read(t0.Add(6 * time.Second))
	assert.Equal(t, 0.0, f.PageRate)
}
