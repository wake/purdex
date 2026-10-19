package hostmem

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const vmStatSample = `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                                    68039.
Pages active:                                 332323.
Pages inactive:                               318361.
Pages speculative:                             13661.
Pages throttled:                                   0.
Pages wired down:                             137688.
"Translation faults":                    61269535748.
Pageouts:                                      123456.
Swapouts:                                       78901.
Decompressions:                          13935860366.
`

func TestParseVMStat(t *testing.T) {
	v, err := ParseVMStat([]byte(vmStatSample))
	require.NoError(t, err)
	assert.Equal(t, VMStat{PageSize: 16384, Free: 68039, Inactive: 318361, Speculative: 13661, Pageouts: 123456, Swapouts: 78901,
		Decompressions: 13935860366, HaveMemory: true, HaveCounters: true}, v)
	assert.NoError(t, v.MemoryErr())
	assert.NoError(t, v.CountersErr())
}

// What is missing is said by the checks, so each caller asks for what it needs (a zero must not pass for a full or a quiet host).
func TestParseVMStat_MissingLines(t *testing.T) {
	for name, c := range map[string]struct {
		raw              string
		memoryOK, ctrsOK bool
	}{
		"no page size":   {strings.Replace(vmStatSample, "page size of 16384", "page size", 1), false, true},
		"no free":        {strings.Replace(vmStatSample, "Pages free:", "Pages gone:", 1), false, true},
		"no inactive":    {strings.Replace(vmStatSample, "Pages inactive:", "Pages gone:", 1), false, true},
		"no speculative": {strings.Replace(vmStatSample, "Pages speculative:", "Pages gone:", 1), false, true},
		"no pageouts":    {strings.Replace(vmStatSample, "Pageouts:", "Gone:", 1), true, false},
		"no swapouts":    {strings.Replace(vmStatSample, "Swapouts:", "Gone:", 1), true, false},
		"empty":          {"", false, false},
		"not vm_stat":    {"hello\nworld\n", false, false},
	} {
		v, err := ParseVMStat([]byte(c.raw))
		require.NoError(t, err, name)
		assert.Equal(t, c.memoryOK, v.MemoryErr() == nil, name+": memory")
		assert.Equal(t, c.memoryOK, v.HaveMemory, name+": HaveMemory")
		assert.Equal(t, c.ctrsOK, v.HaveCounters, name+": HaveCounters")
		assert.Equal(t, c.ctrsOK, v.CountersErr() == nil, name+": counters")
	}
}

// A number that is not one, in a line a caller uses, is an error rather than a 0.
func TestParseVMStat_BadNumber(t *testing.T) {
	for _, raw := range []string{"Pageouts: abc.\nSwapouts: 1.\n", "Pages free: -4.\n", "Swapouts: 1.5.\n"} {
		_, err := ParseVMStat([]byte(raw))
		assert.Error(t, err, raw)
	}
	// a line of no interest may say anything
	_, err := ParseVMStat([]byte("Pages purgeable: n/a.\n"))
	assert.NoError(t, err)
}

// RateOver is the arithmetic of the gate's admission rate, shared with the daemon's zone.
func TestRateOver(t *testing.T) {
	t0 := time.Unix(1000, 0)
	at := func(s float64) time.Time { return t0.Add(time.Duration(s * float64(time.Second))) }
	cur := PageSample{At: at(10), Total: 1000}

	// the oldest sample inside the 10 s window, at least a second old, is the baseline
	assert.InDelta(t, 50, RateOver([]PageSample{{At: at(0), Total: 500}, {At: at(5), Total: 800}}, cur), 0.001)
	// nothing a second or more behind, or nothing inside the window: unknown, taken as 0
	assert.Equal(t, 0.0, RateOver(nil, cur))
	assert.Equal(t, 0.0, RateOver([]PageSample{{At: at(9.5), Total: 0}}, cur))
	assert.Equal(t, 0.0, RateOver([]PageSample{{At: at(-5), Total: 0}}, cur))
	// a counter that went back (a reboot between samples) is 0, not a huge unsigned wrap
	assert.Equal(t, 0.0, RateOver([]PageSample{{At: at(2), Total: 5000}}, cur))
	// the sample of the same instant is not a baseline
	assert.Equal(t, 0.0, RateOver([]PageSample{cur}, cur))
}
