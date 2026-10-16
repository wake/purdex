package fleet

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoopArgsExactArgv(t *testing.T) {
	got, err := LoopArgs("mserv")
	require.NoError(t, err)
	want := []string{
		"-T",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=5",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "RemoteCommand=none",
		"-o", "ControlMaster=no",
		"-o", "ControlPath=none",
		"-o", "ServerAliveInterval=5",
		"-o", "ServerAliveCountMax=2",
		"--", "mserv", Loop,
	}
	assert.Equal(t, want, got)
}

func TestLoopArgsOptionsComeBeforeDoubleDash(t *testing.T) {
	got, err := LoopArgs("a-1.b")
	require.NoError(t, err)
	dd := slices.Index(got, "--")
	require.Greater(t, dd, 0)
	assert.Equal(t, []string{"a-1.b", Loop}, got[dd+1:], "only the alias and the loop follow --")
	for _, a := range got[dd+1:] {
		assert.NotContains(t, got[:dd], a)
	}
}

func TestLoopArgsRefusesBadAlias(t *testing.T) {
	for _, a := range []string{"a;rm", "", "a b", "a$(x)", "a`x`", "a\nb", strings.Repeat("x", 65), "a'b"} {
		_, err := LoopArgs(a)
		assert.Error(t, err, "%q", a)
	}
}

func TestLoopIsOneConstantUnderShC(t *testing.T) {
	require.True(t, strings.HasPrefix(Loop, "exec sh -c '"))
	require.True(t, strings.HasSuffix(Loop, "'"))
	inner := strings.TrimSuffix(strings.TrimPrefix(Loop, "exec sh -c '"), "'")
	assert.NotContains(t, inner, "'", "the loop is wrapped in single quotes, so it has none")
	assert.NotContains(t, inner, "\\\\")
	for _, m := range []string{"@@f", "@@end", "@@s", "@@top_cpu", "@@top_mem", "@@gate", "@@gate_end", "@@gate none", "iostat -n0 -w1", "/proc/stat", ".local/bin/pdx", ".config/pdx/bin/pdx", "kill 0", "head -c 32768"} {
		assert.Contains(t, inner, m)
	}
}

// The loop only reads: no file is written on the remote host, nothing is changed.
func TestLoopWritesNothingOnTheRemoteHost(t *testing.T) {
	for _, bad := range []string{" > /", ">>", "tee ", "rm ", "mkdir", "mktemp", "mkfifo", "touch ", "chmod", "sudo", "curl", "nc "} {
		assert.NotContains(t, Loop, bad)
	}
}
