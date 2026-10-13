package reads

import (
	"context"
	"testing"
	"time"

	"github.com/wake/purdex/internal/reads"
)

// Critic, second round on PR BG-4a.

// Before the module has seen its first hook frame it knows no epoch, yet the daemon's own epoch is public (/api/info
// boot_id): a cursor of this boot's epoch family (BootID, BootID-n) can only be legitimate up to what has been emitted, which
// at that point is nothing. Mutation gate: let it through when no epoch is known yet → red.
func TestRead_ABootEpochCursorBeforeAnyEventIsClampedToZero(t *testing.T) {
	e := newEnv(t)
	c := e.dial(t, "?reads=v1")
	c.next(t, "session.reads")
	boot := e.core.BootID
	if boot == "" {
		t.Fatal("the core has no boot id")
	}
	for code, epoch := range map[string]string{codeA: boot, codeB: boot + "-2"} {
		e.post(code, `{"through":{"epoch":"`+epoch+`","seq":18446744073709551615}}`)
		if got, _ := e.m.store.Get(code); got != (reads.Cursor{Epoch: epoch, Seq: 0}) {
			t.Fatalf("%s: lastRead = %+v, want seq 0 (nothing was emitted yet)", epoch, got)
		}
		c.next(t, "session.read")
	}
	e.post(codeC, `{"through":{"epoch":"another-boot","seq":77}}`) // a previous run's cursor: harmless, kept as sent
	if got, _ := e.m.store.Get(codeC); got != (reads.Cursor{Epoch: "another-boot", Seq: 77}) {
		t.Fatalf("lastRead = %+v", got)
	}
	// and once events flow, the epoch's own high-water mark applies
	e.notify.emit(codeA, boot, 12)
	time.Sleep(50 * time.Millisecond)
	e.post(codeA, `{"through":{"epoch":"`+boot+`","seq":500}}`)
	if got, _ := e.m.store.Get(codeA); got.Seq != 12 {
		t.Fatalf("lastRead = %+v, want the high-water mark 12", got)
	}
}

// Stop does not report success while a delivery is still running: it waits until the workers return or its context ends,
// and then says so. Mutation gate: return nil on timeout → red.
func TestFeed_StopReportsAWorkerThatDidNotReturn(t *testing.T) {
	f := newReadFeed(nil)
	release, entered := make(chan struct{}), make(chan struct{})
	f.subscribe(func(reads.Read) { close(entered); <-release })
	f.publish(reads.Read{Code: "a", Through: reads.Cursor{Epoch: "e", Seq: 1}})
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	if err := f.stop(ctx); err == nil {
		t.Fatal("stop returned nil while a delivery was blocked")
	}
	close(release)
	if err := f.stop(context.Background()); err != nil {
		t.Fatalf("stop after the delivery returned: %v", err)
	}
}
