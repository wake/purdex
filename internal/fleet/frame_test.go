package fleet

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readFrames splits a captured loop output into frames.
func readFrames(t *testing.T, name string) []string {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	require.NoError(t, err)
	defer f.Close()
	fr := NewFrameReader(f)
	var out []string
	for {
		s, err := fr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		require.NoError(t, err)
		out = append(out, s)
	}
}

func TestFixturesParseEveryFrame(t *testing.T) {
	for _, c := range []struct {
		file, os string
		frames   int
		level    bool
	}{
		{"loop-darwin-mlab.txt", "Darwin", 11, true},
		{"loop-darwin-air26.txt", "Darwin", 11, true},
		{"loop-darwin-air19.txt", "Darwin", 21, true},
		{"loop-linux-mserv.txt", "Linux", 21, true},
	} {
		t.Run(c.file, func(t *testing.T) {
			frames := readFrames(t, c.file)
			require.Len(t, frames, c.frames)
			var win PageWindow
			for i, fr := range frames {
				at := t0.Add(time.Duration(i) * time.Second)
				f, err := ParseFast(fr, at, &win)
				require.NoError(t, err, "frame %d", i)
				assert.Equal(t, c.os, f.OS)
				assert.Equal(t, at, f.At, "the receive time, not the remote clock")
				assert.Positive(t, f.NCPU)
				assert.InDelta(t, 50, f.CPU, 50)
				assert.InDelta(t, 50, f.Mem, 50)
				assert.Positive(t, f.Mem)
				assert.InDelta(t, 50, f.DiskPct, 50)
				assert.Positive(t, f.DiskFreeGB)
				assert.Equal(t, c.level, f.LevelKnown)
				assert.Contains(t, []int{1, 2, 4}, f.Level)
				assert.Equal(t, i > 0, f.RateKnown, "the page rate needs a second reading")
				s, ok := ParseSlow(fr, at)
				assert.Equal(t, i%SlowEvery == 0, ok, "every fifth frame carries the slow part, the first included")
				if ok {
					assert.Len(t, s.TopCPU, 5)
					assert.Len(t, s.TopMem, 5)
					assert.False(t, s.GateBad)
				}
			}
		})
	}
}

func TestFixtureGateSections(t *testing.T) {
	mlab := readFrames(t, "loop-darwin-mlab.txt")
	s, ok := ParseSlow(mlab[0], t0)
	require.True(t, ok)
	require.NotNil(t, s.Gate, "mlab has pdx: the gate status parses")
	assert.False(t, s.GateNone)
	assert.NotEmpty(t, s.Gate.Settings)
	for _, h := range append(s.Gate.Heavy, s.Gate.Light...) {
		assert.Empty(t, h.Token, "the slot token is a secret of the remote host")
	}
	mserv := readFrames(t, "loop-linux-mserv.txt")
	s, ok = ParseSlow(mserv[0], t0)
	require.True(t, ok)
	assert.Nil(t, s.Gate)
	assert.True(t, s.GateNone, "no pdx: no queue, not an error")
	assert.False(t, s.GateBad)
}

const linuxFast = "@@f\n@@os\nLinux\n@@ncpu\n4\n@@load\n0.50\n@@cpu\n10 5 85\n@@level\n%s@@vm\nMemTotal: 1000\nMemAvailable: 250\npswpin 10\npswpout 5\n@@disk\nFilesystem 1024-blocks Used Available Capacity Mounted on\n/dev/x 1000000 400000 600000 40%% /\n"

func linuxFrame(psi, extra string) string {
	return fmt.Sprintf(linuxFast, psi) + extra + "@@end\n"
}

func TestParseFastLinuxValues(t *testing.T) {
	f, err := ParseFast(linuxFrame("", ""), t0, nil)
	require.NoError(t, err)
	assert.InDelta(t, 15, f.CPU, 0.001)
	assert.InDelta(t, 75, f.Mem, 0.001)
	assert.InDelta(t, 0.5, f.Load1, 0.001)
	assert.Equal(t, 4, f.NCPU)
	assert.InDelta(t, 40, f.DiskPct, 0.001)
	assert.InDelta(t, 600000.0/(1<<20), f.DiskFreeGB, 0.0001)
	assert.False(t, f.RateKnown, "no window given")
}

func TestPSILevels(t *testing.T) {
	psi := func(some, full string) string {
		return "some avg10=" + some + " avg60=0.00 avg300=0.00 total=1\nfull avg10=" + full + " avg60=0.00 avg300=0.00 total=1\n"
	}
	for _, c := range []struct {
		name, psi string
		level     int
		known     bool
	}{
		{"idle", psi("0.00", "0.00"), 1, true},
		{"some below 10", psi("9.99", "0.00"), 1, true},
		{"some at 10", psi("10.00", "0.00"), 2, true},
		{"full below 10, some high", psi("40.00", "9.99"), 2, true},
		{"full at 10", psi("10.00", "10.00"), 4, true},
		{"full high alone", psi("0.00", "55.0"), 4, true},
		{"no PSI", "", 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, err := ParseFast(linuxFrame(c.psi, ""), t0, nil)
			require.NoError(t, err)
			assert.Equal(t, c.level, f.Level)
			assert.Equal(t, c.known, f.LevelKnown)
		})
	}
}

func TestPageRateComesFromTheWindow(t *testing.T) {
	var win PageWindow
	counters := []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 100}
	var last Fast
	for i, c := range counters {
		fr := strings.Replace(linuxFrame("", ""), "pswpin 10\npswpout 5\n", fmt.Sprintf("pswpin %d\npswpout 0\n", c), 1)
		var err error
		last, err = ParseFast(fr, t0.Add(time.Duration(i)*time.Second), &win)
		require.NoError(t, err)
	}
	assert.True(t, last.RateKnown)
	assert.InDelta(t, 10.0, last.PageRate, 0.001, "100 pages over the 10 s window, not over the last second")
}

func TestParseFastRefusesBadFrames(t *testing.T) {
	good := linuxFrame("", "")
	_, err := ParseFast(good, t0, nil)
	require.NoError(t, err)
	for name, bad := range map[string]string{
		"no @@end":                      strings.TrimSuffix(good, "@@end\n"),
		"no @@end, every section whole": strings.Replace(good, "@@end", "@@enx", 1),
		"bad ncpu":                      strings.Replace(good, "@@ncpu\n4", "@@ncpu\nx", 1),
		"zero ncpu":                     strings.Replace(good, "@@ncpu\n4", "@@ncpu\n0", 1),
		"bad load":                      strings.Replace(good, "0.50", "nope", 1),
		"bad cpu":                       strings.Replace(good, "10 5 85", "10 5", 1),
		"cpu id over 100":               strings.Replace(good, "10 5 85", "10 5 150", 1),
		"no meminfo":                    strings.Replace(good, "MemAvailable: 250\n", "", 1),
		"no disk":                       strings.Replace(good, "40%", "??", 1),
		"unknown os":                    strings.Replace(good, "Linux", "Plan9", 1),
	} {
		_, err := ParseFast(bad, t0, nil)
		assert.Error(t, err, name)
	}
}

func TestParseSlowGateVariants(t *testing.T) {
	procs := "@@s\n@@top_cpu\n 12  30.5 2048 Google Chrome\n 13 1.0 1024 sh\nnot a line\n@@top_mem\n 12 30.5 2048 Google Chrome\n"
	frame := func(gate string) string { return linuxFrame("", procs+gate) }
	t.Run("multi-line json", func(t *testing.T) {
		g := "@@gate\n{\n  \"Heavy\": [\n    {\"class\": \"heavy\", \"slot\": 1, \"pid\": 5, \"token\": \"secret\"}\n  ],\n  \"WaitHeavy\": 2\n}\n@@gate_end\n"
		s, ok := ParseSlow(frame(g), t0)
		require.True(t, ok)
		require.NotNil(t, s.Gate)
		assert.Equal(t, 2, s.Gate.WaitHeavy)
		assert.Equal(t, 5, s.Gate.Heavy[0].PID)
		assert.Empty(t, s.Gate.Heavy[0].Token)
		assert.Equal(t, []Proc{{PID: 12, Name: "Google Chrome", CPU: 30.5, RSSMB: 2}, {PID: 13, Name: "sh", CPU: 1, RSSMB: 1}}, s.TopCPU)
		assert.Len(t, s.TopMem, 1)
	})
	t.Run("none", func(t *testing.T) {
		s, ok := ParseSlow(frame("@@gate none\n"), t0)
		require.True(t, ok)
		assert.True(t, s.GateNone)
		assert.Nil(t, s.Gate)
		assert.False(t, s.GateBad)
	})
	t.Run("broken keeps the lists and the fast part", func(t *testing.T) {
		for name, g := range map[string]string{
			"cut":     "@@gate\n{\n  \"Heavy\": [\n@@gate_end\n",
			"empty":   "@@gate\n@@gate_end\n",
			"garbage": "@@gate\nCommand not found\n@@gate_end\n",
			"missing": "",
		} {
			fr := frame(g)
			s, ok := ParseSlow(fr, t0)
			require.True(t, ok, name)
			assert.True(t, s.GateBad, name)
			assert.Nil(t, s.Gate, name)
			assert.Len(t, s.TopCPU, 2, "the lists are kept: %s", name)
			_, err := ParseFast(fr, t0, nil)
			assert.NoError(t, err, "the fast part is untouched: %s", name)
		}
	})
	t.Run("no slow part", func(t *testing.T) {
		_, ok := ParseSlow(linuxFrame("", ""), t0)
		assert.False(t, ok)
	})
}

func TestFrameReaderSkipsNoiseAndSplitsFrames(t *testing.T) {
	in := "Welcome to somewhere\n" + linuxFrame("", "") + "noise\n" + linuxFrame("", "")
	fr := NewFrameReader(strings.NewReader(in))
	for i := 0; i < 2; i++ {
		s, err := fr.Next()
		require.NoError(t, err)
		assert.Equal(t, linuxFrame("", ""), s)
	}
	_, err := fr.Next()
	assert.ErrorIs(t, err, io.EOF)
}

func TestFrameWithoutEndIsDroppedAndTheNextOneRead(t *testing.T) {
	cut := strings.TrimSuffix(linuxFrame("", ""), "@@end\n")
	fr := NewFrameReader(strings.NewReader(cut + linuxFrame("", "")))
	_, err := fr.Next()
	assert.ErrorIs(t, err, ErrIncompleteFrame)
	s, err := fr.Next()
	require.NoError(t, err)
	assert.Equal(t, linuxFrame("", ""), s)
}

func TestFrameCutOffAtEOFIsNotAFrame(t *testing.T) {
	cut := strings.TrimSuffix(linuxFrame("", ""), "@@end\n")
	_, err := NewFrameReader(strings.NewReader(cut)).Next()
	assert.ErrorIs(t, err, io.EOF)
}

func TestFrameOver64KiBIsSkippedWhole(t *testing.T) {
	// many short lines, then one very long line (longer than any line buffer), then a good frame
	many := linuxFrame("", "@@s\n@@top_cpu\n"+strings.Repeat("1 0.0 1 x\n", 7000)) // ~70 KB
	long := strings.Replace(linuxFrame("", ""), "@@vm\n", "@@vm\n"+strings.Repeat("y", 300_000)+"\n", 1)
	for name, big := range map[string]string{"many lines": many, "one long line": long} {
		t.Run(name, func(t *testing.T) {
			fr := NewFrameReader(strings.NewReader(big + linuxFrame("", "")))
			_, err := fr.Next()
			assert.ErrorIs(t, err, ErrFrameTooBig)
			s, err := fr.Next()
			require.NoError(t, err)
			assert.Equal(t, linuxFrame("", ""), s)
		})
	}
}

func TestFrameJustUnderTheCapIsKept(t *testing.T) {
	pad := strings.Repeat("1 0.0 1 x\n", 6000) // 60 KB
	fr := NewFrameReader(strings.NewReader(linuxFrame("", "@@s\n@@top_cpu\n"+pad)))
	s, err := fr.Next()
	require.NoError(t, err)
	assert.Less(t, len(s), MaxFrame)
}
