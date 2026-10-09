package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// #2137: Stop returns only after the `tmux wait-for` child the cancel killed has been reaped (exec.Cmd.Run's Wait,
// in the watcher goroutine), because a restart execs in place right after Stop: a child nobody reaped by then is a
// zombie of the daemon's pid for as long as it lives.
//
// The fake tmux on PATH blocks like `tmux wait-for` does, so no real tmux server is touched. The seam holds the
// goroutine just after Run returned (the child is dead and waited, the goroutine not finished): Stop must wait for it.
// Mutation gate: drop joinWatchers from Stop → Stop returns while the goroutine is held → red.
func TestStop_JoinsTheWaitForGoroutine(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tmux"), []byte("#!/bin/sh\nexec sleep 30\n"), 0o755))
	t.Setenv("PATH", dir+":/bin:/usr/bin")

	mod, _, _ := newTestModule(t)
	entered, release := make(chan struct{}, 1), make(chan struct{})
	mod.afterWaitForRun = func() {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	}
	require.NoError(t, mod.Start(context.Background()))

	stopped := make(chan struct{})
	go func() {
		_ = mod.Stop(context.Background())
		close(stopped)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the wait-for child never ended after the cancel")
	}
	select {
	case <-stopped:
		t.Fatal("Stop returned while the wait-for goroutine had not finished")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after the goroutine finished")
	}
}
