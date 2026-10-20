package wwdalerts

import (
	"context"
	"testing"
	"time"

	"github.com/wake/purdex/internal/watch"
	"github.com/wake/purdex/internal/wwd/wire"
)

// finishingWaker succeeds only after the module has been asked to stop, as a call that wins the race with the shutdown does.
type finishingWaker struct {
	entered chan struct{}
	release chan struct{}
}

func (f *finishingWaker) WakeWatcher(ctx context.Context, reason, detail string) error {
	f.entered <- struct{}{}
	<-f.release
	return nil
}

// A call that succeeds while the module is stopping is recorded as told, so a restart does not wake for it again.
func TestASuccessAtShutdownIsRecorded(t *testing.T) {
	e := newEnv(t)
	w := &finishingWaker{entered: make(chan struct{}, 1), release: make(chan struct{})}
	e.m.wakeRule = func() bool { return true }
	e.m.waker = func() (watch.Waker, bool) { return w, true }
	ctx, cancel := context.WithCancel(context.Background())
	e.m.startWake(ctx)
	e.baseline(t)
	e.src.set("mlab", alertOf("k", wire.AlertThrash, wire.AlertDigest, t0, "x"))
	e.pass(t)
	<-w.entered
	cancel()
	close(w.release)
	e.m.wg.Wait()

	e.m = e.module(t) // restarted
	w2 := startWakeFor(t, e)
	e.now = e.now.Add(time.Hour)
	e.pass(t)
	e.settle(t)
	if n := len(w2.got()); n != 0 {
		t.Fatalf("woke again for a call that had succeeded: %v", w2.got())
	}
}
