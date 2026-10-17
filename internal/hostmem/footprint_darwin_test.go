//go:build darwin

package hostmem

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The footprint of this very process grows with the memory it touches, by about as much, and is known to macOS's own `footprint` tool to
// the same order. Mutation gate: the wrong offset (the resident size's, @64, or any other field) fails the cross-check below (red).
func TestFootprint_OfThisProcessFollowsWhatItTouches(t *testing.T) {
	f, err := NewFootprinter()
	require.NoError(t, err)
	before, err := f.Of(os.Getpid())
	require.NoError(t, err)
	assert.Greater(t, before, uint64(1<<20), "a running test is more than a megabyte")

	block := make([]byte, 256<<20)
	for i := 0; i < len(block); i += 4096 {
		block[i] = 1
	}
	after, err := f.Of(os.Getpid())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, after-before, uint64(200<<20), "256 MB touched must show in the footprint (%d -> %d)", before, after)
	_ = block[len(block)-1]
}

// macOS's own tool says the same number: `footprint -p <pid>` prints "Footprint: <n> <unit>" for the process. Skipped where the tool is
// not there or prints another shape.
func TestFootprint_AgreesWithTheFootprintTool(t *testing.T) {
	path, err := exec.LookPath("footprint")
	if err != nil {
		t.Skip("no footprint tool")
	}
	f, err := NewFootprinter()
	require.NoError(t, err)
	block := make([]byte, 128<<20)
	for i := 0; i < len(block); i += 4096 {
		block[i] = 1
	}
	out, err := exec.Command(path, "-p", strconv.Itoa(os.Getpid())).CombinedOutput()
	if err != nil {
		t.Skipf("footprint tool: %v", err)
	}
	m := regexp.MustCompile(`Footprint:\s+([0-9.]+)\s+(KB|MB|GB)`).FindStringSubmatch(string(out))
	if m == nil {
		t.Skipf("footprint tool printed another shape: %q", string(out))
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	unit := map[string]float64{"KB": 1 << 10, "MB": 1 << 20, "GB": 1 << 30}[m[2]]
	want := n * unit
	got, err := f.Of(os.Getpid())
	require.NoError(t, err)
	assert.InEpsilon(t, want, float64(got), 0.25, "ours %d, the tool's %.0f", got, want)
	_ = block[len(block)-1]
}

// A pid that is not there, and pid 0 or a negative one, have no footprint; neither does a process of another user (launchd, pid 1).
func TestFootprint_NoneForWhatCannotBeAsked(t *testing.T) {
	f, err := NewFootprinter()
	require.NoError(t, err)
	for _, pid := range []int{0, -3, 99999999} {
		_, err := f.Of(pid)
		assert.Error(t, err, "pid %d", pid)
	}
	if os.Geteuid() != 0 {
		_, err := f.Of(1)
		assert.Error(t, err, "launchd belongs to root: EPERM for anyone else")
	}
}

func TestFootprints_LeavesOutWhatHasNone(t *testing.T) {
	f, err := NewFootprinter()
	require.NoError(t, err)
	got := Footprints(f, []int{os.Getpid(), 99999999})
	assert.Contains(t, got, os.Getpid())
	assert.NotContains(t, got, 99999999)
	assert.Empty(t, Footprints(nil, []int{os.Getpid()}), "no Footprinter: nothing")
}
