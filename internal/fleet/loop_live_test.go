//go:build darwin || linux

package fleet

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Run the loop on this host for three frames: the one live proof that the script parses and prints whole frames, and that it
// ends by itself when its reader goes away.
func TestLoopLiveOnThisHost(t *testing.T) {
	if testing.Short() {
		t.Skip("live loop")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	body := strings.TrimSuffix(strings.TrimPrefix(Loop, "exec sh -c '"), "'")
	cmd := exec.CommandContext(ctx, "sh", "-c", body)
	// its own process group: the loop's trap does `kill 0`, which must not reach this test
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	fr := NewFrameReader(out)
	var frames []string
	for len(frames) < 3 {
		f, e := fr.Next()
		require.NoError(t, e)
		frames = append(frames, f)
	}
	_ = out.Close() // the reader goes away; the loop must end by itself
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		t.Fatal("the loop did not end after its reader closed")
	}
	want := map[string]string{"darwin": "Darwin", "linux": "Linux"}[runtime.GOOS]
	var win PageWindow
	for i, f := range frames {
		fast, err := ParseFast(f, t0.Add(time.Duration(i)*time.Second), &win)
		require.NoError(t, err, f)
		assert.Equal(t, want, fast.OS)
	}
	_, ok := ParseSlow(frames[0], t0)
	assert.True(t, ok, "the first frame carries the slow part")
	_, ok = ParseSlow(frames[1], t0)
	assert.False(t, ok)
}
