package gate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The record carries the command tree's usage read from its wait: CPU seconds, the largest process, major faults.
func TestDecision_CarriesTheUsageOfTheCommand(t *testing.T) {
	g, _ := testGate(t)
	runSync(t, g, Options{Argv: sh("i=0; while [ $i -lt 40000 ]; do i=$((i+1)); done"), Class: ClassHeavy})
	recs := readDecisions(t, g)
	require.Len(t, recs, 1)
	assert.Greater(t, recs[0]["cpu_s"], 0.0)
	assert.Greater(t, recs[0]["max_rss_mb"], 0.0)
	assert.NotContains(t, recs[0], "disk_read_mb", "no exact byte count is had for a waited tree (PR note)")
	assert.NotContains(t, recs[0], "disk_write_mb")
}

// A command that was never started has no usage fields.
func TestDecision_ARefusedCommandHasNoUsage(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Admission.DiskMinGB = 3 })
	g.DiskFree = func(string) (int64, error) { return 1 << 30, nil }
	r := runSync(t, g, Options{Argv: sh("true"), Class: ClassHeavy})
	require.Equal(t, ExitRefused, r.code)
	rec := readDecisions(t, g)[0]
	for _, k := range []string{"cpu_s", "max_rss_mb", "major_faults"} {
		assert.NotContains(t, rec, k)
	}
}

// Maxrss is bytes on darwin and KiB elsewhere.
func TestMaxRSSMB_UnitsPerOS(t *testing.T) {
	for _, c := range []struct {
		goos string
		raw  int64
		want float64
	}{
		{"darwin", 52428800, 50},
		{"linux", 51200, 50},
		{"freebsd", 51200, 50},
		{"darwin", 0, 0},
	} {
		assert.InDelta(t, c.want, maxRSSMB(c.raw, c.goos), 0.01, c.goos)
	}
}
