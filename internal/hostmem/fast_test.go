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
