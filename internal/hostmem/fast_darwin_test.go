//go:build darwin

package hostmem

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The real reader on this host: a whole reading (nothing missing after the first), plausible figures, and the cost of one.
func TestFastReader_RealHost(t *testing.T) {
	src := SystemSources()
	require.NoError(t, src.MachErr)
	r := NewFastReader(src)
	_, err := r.Read(time.Now())
	require.NoError(t, err)
	time.Sleep(1100 * time.Millisecond)
	f, err := r.Read(time.Now())
	require.NoError(t, err)
	assert.Empty(t, f.Missing, "a whole reading on a macOS host")
	assert.InDelta(t, 50, f.CPU, 50)
	assert.Greater(t, f.Mem, 0.0)
	assert.LessOrEqual(t, f.Mem, 100.0)
	assert.Contains(t, []int{1, 2, 4}, f.Level)
	assert.Greater(t, f.NCPU, 0)
	assert.GreaterOrEqual(t, f.PageRate, 0.0)
}

// One reading costs well under a millisecond: the 1 s tick is not a load on the host.
func BenchmarkFastReaderRead(b *testing.B) {
	r := NewFastReader(SystemSources())
	now := time.Now()
	for i := 0; i < b.N; i++ {
		now = now.Add(time.Second)
		if _, err := r.Read(now); err != nil {
			b.Fatal(err)
		}
	}
}
