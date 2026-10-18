package qos

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/wake/purdex/internal/wwd/wire"
)

// prioDarwinProcess is getpriority(2)'s PRIO_DARWIN_PROCESS: the background state of the process (1 when it is in the background).
const prioDarwinProcess = 4

// The test binary run again as a helper prints its own background state.
func TestMain(m *testing.M) {
	if os.Getenv("WWD_QOS_HELPER") == "print" {
		p, err := unix.Getpriority(prioDarwinProcess, 0)
		if err != nil {
			os.Stderr.WriteString(err.Error())
			os.Exit(2)
		}
		os.Stdout.WriteString(string(rune('0' + p)))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// bgState runs argv with the helper's environment and returns what the helper printed.
func bgState(t *testing.T, argv []string) string {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "WWD_QOS_HELPER=print")
	out, err := cmd.Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

// A command started through Background is in the background tier, and so is what it starts; one started without is not.
func TestBackground_PutsTheCommandAndItsChildrenInTheBackground(t *testing.T) {
	plain := []string{os.Args[0]}
	require.Equal(t, "0", bgState(t, plain), "a plain process is not in the background")

	wrapped, ok := Background(plain)
	require.True(t, ok)
	assert.Equal(t, []string{taskpolicyPath, "-b", os.Args[0]}, wrapped)
	assert.Equal(t, "1", bgState(t, wrapped), "the command is in the background")

	// a child of the command: sh runs the helper as its own child
	viaSh, ok := Background([]string{"/bin/sh", "-c", os.Args[0]})
	require.True(t, ok)
	assert.Equal(t, "1", bgState(t, viaSh), "and so is what it starts")
	assert.Equal(t, "0", bgState(t, []string{"/bin/sh", "-c", os.Args[0]}))
}

func TestFor_OnlyDevJobsOnAHostThatAsksForIt(t *testing.T) {
	argv := []string{"/bin/echo", "x"}
	w, bg := For(wire.ClassDev, true, argv)
	assert.True(t, bg)
	assert.Equal(t, []string{taskpolicyPath, "-b", "/bin/echo", "x"}, w)

	for _, c := range []struct {
		class wire.JobClass
		on    bool
	}{{wire.ClassDev, false}, {wire.ClassMerge, true}, {wire.ClassRelease, true}} {
		w, bg := For(c.class, c.on, argv)
		assert.False(t, bg, "%v %v", c.class, c.on)
		assert.Equal(t, argv, w)
	}
}

func TestBackground_EmptyArgvIsLeftAlone(t *testing.T) {
	w, ok := Background(nil)
	assert.False(t, ok)
	assert.Empty(t, w)
}
