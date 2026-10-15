package hostmem

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleAt(t0 time.Time, sec int, level int, po uint64) Sample {
	return Sample{At: t0.Add(time.Duration(sec) * time.Second).UnixMilli(), Level: level, Pageouts: po, Swapouts: po}
}

// A cache that is missing, torn, or full of readings with no time is no readings; it is never used as one.
func TestCache_LoadOfABadFileIsNothing(t *testing.T) {
	dir := t.TempDir()
	c := Cache{Dir: dir}
	assert.Empty(t, c.Load().Samples, "missing")
	for name, raw := range map[string]string{
		"torn":    `{"samples":[{"at":17000000000`,
		"garbage": "\x00\x01 not json",
		"wrong":   `[1,2,3]`,
		"empty":   ``,
	} {
		require.NoError(t, os.WriteFile(c.Path(), []byte(raw), 0o644))
		assert.Empty(t, c.Load().Samples, name)
	}
	require.NoError(t, os.WriteFile(c.Path(), []byte(`{"samples":[{"at":0,"level":4},{"at":-5,"level":4},{"at":1000,"level":1}]}`), 0o644))
	got := c.Load().Samples
	require.Len(t, got, 1, "a reading with no time cannot be told old from new")
	assert.EqualValues(t, 1000, got[0].At)
}

// Append keeps the recent readings in order, drops the old and the future ones, and at most KeepMax.
func TestCache_AppendKeepsTheRecentOnes(t *testing.T) {
	c := Cache{Dir: t.TempDir()}
	t0 := time.Unix(10_000, 0)
	for i := 0; i < 20; i++ {
		c.Append(t0.Add(time.Duration(i)*time.Second), sampleAt(t0, i, 1, uint64(i)))
	}
	got := c.Load().Samples
	require.Len(t, got, KeepMax)
	assert.Equal(t, sampleAt(t0, 19, 1, 19), got[len(got)-1])
	assert.Equal(t, sampleAt(t0, 4, 1, 4), got[0])
	// a reading from 40 s ago is older than KeepFor (30 s)
	now := t0.Add(60 * time.Second)
	out := c.Append(now, sampleAt(t0, 60, 2, 99))
	require.Len(t, out, 1)
	// a reading newer than the new one (the clock went back) is dropped
	c.Append(t0.Add(50*time.Second), sampleAt(t0, 50, 1, 1))
	assert.Equal(t, []Sample{sampleAt(t0, 50, 1, 1)}, c.Load().Samples)
}

// The lock is the gate's one-sampler lock: while someone holds it, Record gives its reading up and writes nothing.
func TestCache_RecordGivesWayToAWriterAndWritesAfter(t *testing.T) {
	dir := t.TempDir()
	c := Cache{Dir: dir}
	t0 := time.Unix(10_000, 0)
	unlock, got := c.TryLock()
	require.True(t, got)
	_, got2 := c.TryLock()
	assert.False(t, got2, "a second writer does not get the lock")
	assert.False(t, c.Record(t0, sampleAt(t0, 0, 1, 1)), "the daemon does not wait for a gate that is sampling")
	_, err := os.Stat(c.Path())
	assert.True(t, os.IsNotExist(err), "and writes nothing")
	unlock()
	assert.True(t, c.Record(t0, sampleAt(t0, 0, 1, 1)))
	assert.Len(t, c.Load().Samples, 1)
}

// The cache never goes back in time: a reading no newer than the newest in the file is not written.
func TestCache_RecordNeverReplacesANewerReading(t *testing.T) {
	c := Cache{Dir: t.TempDir()}
	t0 := time.Unix(10_000, 0)
	require.True(t, c.Record(t0, sampleAt(t0, 5, 2, 50))) // a gate sampled at +5
	assert.False(t, c.Record(t0.Add(6*time.Second), sampleAt(t0, 3, 1, 30)), "the daemon's reading began at +3: older")
	assert.False(t, c.Record(t0.Add(6*time.Second), sampleAt(t0, 5, 1, 30)), "as new is not newer")
	got := c.Load().Samples
	require.Len(t, got, 1)
	assert.Equal(t, 2, got[0].Level, "the gate's reading stands")
	assert.True(t, c.Record(t0.Add(11*time.Second), sampleAt(t0, 10, 1, 60)))
	assert.Len(t, c.Load().Samples, 2)
}

// Two writers (the daemon and a gate) never interleave: whatever they do at once, the file is one whole document in order, and no
// temp file is left behind.
func TestCache_TwoWritersNeverInterleave(t *testing.T) {
	dir := t.TempDir()
	c := Cache{Dir: dir}
	t0 := time.Unix(10_000, 0)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				at := t0.Add(time.Duration(w*1000+i) * time.Millisecond * 10)
				if w%2 == 0 {
					c.Record(at, Sample{At: at.UnixMilli(), Level: 1, Pageouts: uint64(i)})
					continue
				}
				if unlock, got := c.TryLock(); got { // what a gate does
					c.Append(at, Sample{At: at.UnixMilli(), Level: 2, Pageouts: uint64(i)})
					unlock()
				}
			}
		}(w)
	}
	wg.Wait()
	b, err := os.ReadFile(c.Path())
	require.NoError(t, err)
	var f File
	require.NoError(t, json.Unmarshal(b, &f), string(b))
	require.NotEmpty(t, f.Samples)
	for i := 1; i < len(f.Samples); i++ {
		assert.LessOrEqual(t, f.Samples[i-1].At, f.Samples[i].At, "in time order")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		assert.False(t, strings.HasPrefix(e.Name(), "pressure.json."), "no temp file is left: "+e.Name())
	}
}

// A directory that cannot be written is not an error that stops the caller.
func TestCache_RecordIntoAnUnwritableDirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file"), nil, 0o644))
	c := Cache{Dir: filepath.Join(dir, "file", "sub")} // a path below a file
	assert.False(t, c.Record(time.Now(), Sample{At: time.Now().UnixMilli(), Level: 1}))
}
