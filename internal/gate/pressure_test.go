package gate

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

const vmStatSample = `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                                     5545.
Pageins:                                    301199558.
Pageouts:                                    1453835.
Swapins:                                     3630369.
Swapouts:                                    3985854.
"Translation faults":                    16047757703.
`

func TestParseVMStat(t *testing.T) {
	po, so, err := parseVMStat([]byte(vmStatSample))
	require.NoError(t, err)
	assert.Equal(t, uint64(1453835), po)
	assert.Equal(t, uint64(3985854), so)
	_, _, err = parseVMStat([]byte("Pages free: 5.\n"))
	assert.Error(t, err)
	_, _, err = parseVMStat([]byte("Pageouts: abc.\nSwapouts: 1.\n"))
	assert.Error(t, err)
}

type fakeHost struct {
	now      time.Time
	level    int
	po, so   uint64
	levelErr error
	reads    int
}

func (f *fakeHost) reader(dir string) *HostReader {
	return &HostReader{Dir: dir, Now: func() time.Time { return f.now },
		Level: func() (int, error) { f.reads++; return f.level, f.levelErr },
		Pages: func() (uint64, uint64, error) { return f.po, f.so, nil }}
}

// The rate is pages per second against an older sample in the window; the first read has none and counts as 0.
func TestHostReader_RateComesFromTheSamplesOfTheLastSeconds(t *testing.T) {
	f := &fakeHost{now: time.Unix(1000, 0), level: 1, po: 1000, so: 1000}
	r := f.reader(t.TempDir())
	p, err := r.Read()
	require.NoError(t, err)
	assert.Equal(t, Pressure{Level: 1}, p, "no baseline yet")

	f.now = f.now.Add(8 * time.Second)
	f.po, f.so = 1400, 1400 // +800 pages in 8 s
	p, err = r.Read()
	require.NoError(t, err)
	assert.InDelta(t, 100.0, p.PageRate, 0.001)
	assert.Equal(t, 1, p.Level)

	f.now = f.now.Add(8 * time.Second)
	f.po, f.so, f.level = 1400, 1400, 2 // quiet again; the baseline is the 8 s old sample
	p, err = r.Read()
	require.NoError(t, err)
	assert.InDelta(t, 0.0, p.PageRate, 0.001, "the first sample (16 s ago) is out of the window; the 8 s old one is the baseline and nothing moved since")
	assert.Equal(t, 2, p.Level)
}

// A sample younger than 5 s is shared with the other gates: the host is not read again.
func TestHostReader_ReusesAFreshSample(t *testing.T) {
	f := &fakeHost{now: time.Unix(1000, 0), level: 1}
	dir := t.TempDir()
	_, err := f.reader(dir).Read()
	require.NoError(t, err)
	f.now = f.now.Add(2 * time.Second)
	f.level = 4
	p, err := f.reader(dir).Read() // another gate
	require.NoError(t, err)
	assert.Equal(t, 1, p.Level, "the cached sample, not the host")
	assert.Equal(t, 1, f.reads)
	f.now = f.now.Add(4 * time.Second)
	p, err = f.reader(dir).Read()
	require.NoError(t, err)
	assert.Equal(t, 4, p.Level, "older than 5 s: read again")
}

func TestHostReader_CounterGoingBackwardsIsNoNegativeRate(t *testing.T) {
	f := &fakeHost{now: time.Unix(1000, 0), level: 1, po: 5000, so: 5000}
	r := f.reader(t.TempDir())
	_, _ = r.Read()
	f.now = f.now.Add(6 * time.Second)
	f.po, f.so = 10, 10 // a reboot or a wrap
	p, err := r.Read()
	require.NoError(t, err)
	assert.Zero(t, p.PageRate)
}

func TestHostReader_UnreadableIsAnError(t *testing.T) {
	f := &fakeHost{now: time.Unix(1000, 0), levelErr: errors.New("no such sysctl")}
	_, err := f.reader(t.TempDir()).Read()
	assert.Error(t, err)
}

func TestHostReader_ACacheItCannotWriteStillReads(t *testing.T) {
	f := &fakeHost{now: time.Unix(1000, 0), level: 2}
	r := f.reader("/proc/does/not/exist")
	p, err := r.Read()
	require.NoError(t, err)
	assert.Equal(t, 2, p.Level)
	_, err = os.Stat("/proc/does")
	assert.Error(t, err)
}
