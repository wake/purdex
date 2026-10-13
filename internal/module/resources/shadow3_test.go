package resourcesmod

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

// The shadow's writes happen with stateMu released, so a busy database cannot
// hold the real pass (codex R2). (M: write inside shadowStep and TryLock fails.)
func TestShadow_WritesHappenOutsideTheLock(t *testing.T) {
	f := newShadowFix(t)
	var mu sync.Mutex
	var heldDuringWrite []bool
	f.m.shadowSet = func(id string, waitMS int64, open bool, reasons string) error {
		got := f.m.stateMu.TryLock()
		if got {
			f.m.stateMu.Unlock()
		}
		mu.Lock()
		heldDuringWrite = append(heldDuringWrite, !got)
		mu.Unlock()
		return f.m.store.SetShadow(id, waitMS, open, reasons)
	}
	f.queue("a", 35, 5*time.Minute)
	f.queue("b", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "")
	f.advance(10 * time.Second)
	_, err := f.m.store.End("b", resources.EndReleased, f.nowMS())
	require.NoError(t, err)
	f.m.admissionPass(context.Background(), "") // b's lower bound is settled here
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, heldDuringWrite, 2, "a's grant and b's lower bound")
	assert.Equal(t, []bool{false, false}, heldDuringWrite, "stateMu must be free while the shadow writes")
	assert.Equal(t, 1, f.shadowOf("b").Open)
}

// A write that fails is tried again at the next pass and the gap is not moved
// twice (codex R2). (M: drop the verdict after the first failure and the
// second assertion is red.)
func TestShadow_FailedWriteIsRetried(t *testing.T) {
	f := newShadowFix(t)
	fail := true
	var calls int
	f.m.shadowSet = func(id string, waitMS int64, open bool, reasons string) error {
		calls++
		if fail {
			return errors.New("database is locked")
		}
		return f.m.store.SetShadow(id, waitMS, open, reasons)
	}
	f.queue("a", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "")
	require.Equal(t, int64(-1), f.shadowOf("a").WaitMS, "the write failed")
	last := f.m.shadowLastAtMS.Load()
	fail = false
	f.advance(2 * time.Second)
	f.m.admissionPass(context.Background(), "")
	assert.Equal(t, int64(1000), f.shadowOf("a").WaitMS, "the retry wrote the verdict worked out the first time")
	assert.Equal(t, last, f.m.shadowLastAtMS.Load(), "the shadow's own gap was not moved by the retry")
	assert.Zero(t, f.m.shadowUnwritten.Load())

	// It gives up after a few tries rather than carrying a dead verdict forever.
	g := newShadowFix(t)
	g.m.shadowSet = func(string, int64, bool, string) error { return errors.New("disk full") }
	g.queue("x", 35, 5*time.Minute)
	for i := 0; i < shadowWriteAttempts+2; i++ {
		g.m.admissionPass(context.Background(), "")
		g.advance(time.Second)
	}
	assert.Zero(t, g.m.shadowUnwritten.Load())
}

// A flush that fails while the shadow is dropped (a switch away from advise)
// does not put the old state back (codex R1b). (M: drop the generation check
// and the verdict comes back.)
func TestShadow_DropDuringAFlushIsNotUndone(t *testing.T) {
	f := newShadowFix(t)
	f.m.shadowSet = func(string, int64, bool, string) error {
		f.m.dropShadow() // another pass switched away from advise while this one writes
		return errors.New("database is locked")
	}
	f.queue("a", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "")
	assert.Zero(t, f.m.shadowUnwritten.Load(), "the failed verdict was not carried over the drop")
	assert.Zero(t, f.m.shadowLastAtMS.Load())
	assert.False(t, f.m.shadowActive())
}

// A request that arrives while the shadow is in its gap records the gap left in
// dec_gap_ms (codex R2).
func TestShadow_DecGapMSIsTheShadowsGapLeft(t *testing.T) {
	f := newShadowFix(t)
	f.queue("a", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "") // the shadow grants a at t0
	f.advance(10 * time.Second)
	f.queue("c", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "")
	var gap int
	require.NoError(t, f.m.store.db.QueryRow(`SELECT dec_gap_ms FROM resource_leases WHERE id = 'c'`).Scan(&gap))
	assert.Equal(t, 20000, gap)
	require.NoError(t, f.m.store.db.QueryRow(`SELECT dec_gap_ms FROM resource_leases WHERE id = 'a'`).Scan(&gap))
	assert.Zero(t, gap, "a met no gap")
}

// In advise the status shows the shadow's reason for a request that is still
// waiting for real (cut for a baseline) (codex R2).
func TestShadow_StatusShowsTheShadowReason(t *testing.T) {
	f := newShadowFix(t)
	f.setZone("danger", "flat")
	r := baseRow("s", "c-s")
	r.Scope, r.HolderPID = resources.ScopeSessionNew, 500
	r.CreatedAt, r.DeadlineAt, r.LeaseUntil = f.nowMS()-1000, f.nowMS()+300000, f.nowMS()+30000
	mustCreate(f.t, f.m.store, r)
	f.m.passOnce(f.m.settings(), "s", map[string]string{}) // cut for a baseline; the shadow holds it in danger
	f.m.flushShadow()
	advise := resources.Settings{Mode: resources.ModeAdvise}.Effective()
	f.m.lastSettings.Store(&advise)
	snap := f.snapshot()
	require.Len(t, snap.Waiters, 1)
	assert.Equal(t, "danger", snap.Waiters[0].Reason)
	assert.Equal(t, f.nowMS(), snap.Waiters[0].ReasonSince)
}
