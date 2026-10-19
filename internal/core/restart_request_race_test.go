package core

import (
	"runtime"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/config"
)

// A shutdown that sees an accepted restart (BeginShutdown true) must also see
// that restart's exec path: accepting and publishing the path are one step.
func TestRequestRestart_ShutdownSeesAcceptedRestartPath(t *testing.T) {
	for i := 0; i < 2000; i++ {
		c := New(CoreDeps{Config: &config.Config{}})
		c.SetRestartHook(func() {})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.RequestRestart("dev rebuild", "/repo/bin/pdx", nil)
		}()
		for c.life.Load() == lifeRunning {
			runtime.Gosched()
		}
		if c.BeginShutdown() {
			if got := c.RestartExecPath(); got != "/repo/bin/pdx" {
				t.Fatalf("iteration %d: restart accepted but exec path %q", i, got)
			}
		}
		wg.Wait()
	}
}
