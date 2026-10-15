package gate

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/hostmem"
)

// While the daemon writes the cache, the gate reuses a reading younger than 5 s whoever wrote it (no fork), and reads for
// itself once the reading is 5 s old: its freshness is the same with the daemon as without it.
func TestHostReader_ReusesTheDaemonsReadingForFiveSecondsAndNoLonger(t *testing.T) {
	dir := t.TempDir()
	f := &fakeHost{now: time.Unix(1000, 0), level: 1, po: 5000, so: 0}
	r := f.reader(dir)
	c := hostmem.Cache{Dir: dir}
	t0 := f.now

	// the daemon's readings, 5 s apart, as its ticker writes them
	require.True(t, c.Record(t0, hostmem.Sample{At: t0.UnixMilli(), Level: 2, Pageouts: 1000}))
	t1 := t0.Add(5 * time.Second)
	require.True(t, c.Record(t1, hostmem.Sample{At: t1.UnixMilli(), Level: 2, Pageouts: 1500}))

	f.now = t1.Add(4900 * time.Millisecond) // 4.9 s after the newest
	p, err := r.Read()
	require.NoError(t, err)
	assert.Equal(t, 0, f.reads, "the daemon's reading is reused: the gate did not read the host")
	assert.Equal(t, 2, p.Level)
	assert.InDelta(t, 100, p.PageRate, 0.001, "(1500 - 1000) pages over the daemon's 5 s")

	f.now = t1.Add(5 * time.Second) // 5 s: the window is closed
	p, err = r.Read()
	require.NoError(t, err)
	assert.Equal(t, 1, f.reads, "a reading 5 s old is not reused: the gate reads for itself")
	assert.Equal(t, 1, p.Level, "and sees the host as it is now, not the daemon's older level")
}

// A cache the daemon (or anyone) left torn or with no times in it is not a reading.
func TestHostReader_ACorruptOrTimelessCacheIsNotUsed(t *testing.T) {
	for name, raw := range map[string]string{
		"torn":     `{"samples":[{"at":1000000`,
		"no times": `{"samples":[{"level":4,"pageouts":1,"swapouts":1}]}`,
		"future":   `{"samples":[{"at":9999999999999,"level":4}]}`,
	} {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(hostmem.Cache{Dir: dir}.Path(), []byte(raw), 0o644))
		f := &fakeHost{now: time.Unix(1000, 0), level: 1, po: 10, so: 10}
		p, err := f.reader(dir).Read()
		require.NoError(t, err, name)
		assert.Equal(t, 1, f.reads, name+": the gate read the host")
		assert.Equal(t, 1, p.Level, name)
	}
}

// The gate and the daemon are two writers of one file under one lock: a gate that sampled leaves its reading, and the daemon's older
// one does not replace it.
func TestHostReader_TheDaemonDoesNotTakeBackAGatesNewerReading(t *testing.T) {
	dir := t.TempDir()
	f := &fakeHost{now: time.Unix(1000, 0), level: 4, po: 10, so: 10}
	r := f.reader(dir)
	_, err := r.Read() // the gate samples at 1000
	require.NoError(t, err)
	older := f.now.Add(-time.Second) // the daemon's tick began a second before
	assert.False(t, hostmem.Cache{Dir: dir}.Record(f.now, hostmem.Sample{At: older.UnixMilli(), Level: 1}))
	p, err := r.Read()
	require.NoError(t, err)
	assert.Equal(t, 4, p.Level)
	assert.Equal(t, 1, f.reads)
}
