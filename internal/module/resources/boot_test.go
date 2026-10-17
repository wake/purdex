package resourcesmod

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/resources"
)

// The boot prunes host_minutes rows older than the retention and keeps the rest; it runs before the sampler's first sample.
// Mutation gate: drop PruneMinutes from boot -> the old row stays -> red.
func TestBoot_PrunesOldMinutesBeforeTheSampler(t *testing.T) {
	f := newStoreFix(t)
	now := f.nowMS()
	if err := f.m.store.InsertMinute(minuteRow{At: now - int64(retention/time.Millisecond) - 60000, NCPU: 10}); err != nil {
		t.Fatal(err)
	}
	if err := f.m.store.InsertMinute(minuteRow{At: now - 3600*1000, NCPU: 10}); err != nil {
		t.Fatal(err)
	}
	var seen atomic.Int64
	f.m.sampler = &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		if seen.Load() == 0 {
			rows, err := f.m.store.ReportMinutes(0, now+1)
			if err != nil {
				t.Error(err)
			}
			seen.Store(int64(len(rows)) + 1000) // the first sample sees the pruned table
		}
		return idleRaw(), nil, nil
	}}
	if err := f.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.m.Stop(context.Background()) })
	waitFor(t, "the first sample", func() bool { return seen.Load() != 0 })
	if got := seen.Load(); got != 1001 {
		t.Fatalf("the sampler saw %d minute rows before it, want only the recent one", got-1000)
	}
}
