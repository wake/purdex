package resourcesmod

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

// The shadow counts the real held rows as "running": in the warning zone with
// the usage rising it holds the next request, and lets it in once the trend
// falls. (M: running always false and b is granted at once.)
func TestShadow_RunningComesFromTheRealHeldRows(t *testing.T) {
	f := newShadowFix(t)
	f.setZone("warning", "rising")
	f.queue("a", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "")
	assert.Equal(t, int64(1000), f.shadowOf("a").WaitMS, "nothing was running: granted")
	f.advance(40 * time.Second)
	f.queue("b", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "")
	assert.Equal(t, int64(-1), f.shadowOf("b").WaitMS, "a is held: warning and rising observes")
	f.setZone("warning", "falling")
	f.m.admissionPass(context.Background(), "")
	assert.NotEqual(t, int64(-1), f.shadowOf("b").WaitMS)
}

// A request the real pass could not grant in its first round (a session-new
// waiter whose baseline is not ready) was still granted by the shadow in that
// pass: when the real grant comes in a later round, would_wait stays false
// (codex R1). (M: judge by the later pass alone and it reads true.)
func TestShadow_WouldWaitSurvivesACutRound(t *testing.T) {
	f := newShadowFix(t)
	r := baseRow("s", "c-s")
	r.Scope, r.HolderPID = resources.ScopeSessionNew, 500
	r.CreatedAt, r.DeadlineAt, r.LeaseUntil = f.nowMS()-1000, f.nowMS()+300000, f.nowMS()+30000
	mustCreate(f.t, f.m.store, r)
	f.m.passOnce(f.m.settings(), "s", map[string]string{}) // no baseline yet: cut in the real pass
	f.m.flushShadow()
	require.Equal(t, "waiting", f.state("s"))
	require.Equal(t, int64(1000), f.shadowOf("s").WaitMS, "the shadow granted it in the request's own pass")
	f.m.passOnce(f.m.settings(), "", map[string]string{"s": "[]"}) // the baseline arrives
	f.m.flushShadow()
	require.Equal(t, "held", f.state("s"))
	assert.Equal(t, 0, f.shadowOf("s").WouldWait)
	assert.Empty(t, f.m.shadow.atOnce, "the note is dropped with the grant")
}

// Switching away from advise clears the shadow's last grant too, even with its
// queue empty, so a later advise does not start inside a stale gap (codex R1).
func TestShadow_SwitchClearsTheGapEvenWhenEmpty(t *testing.T) {
	f := newShadowFix(t)
	f.queue("a", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "")
	require.Zero(t, f.m.shadowLen.Load(), "the shadow granted its only request")
	require.NotZero(t, f.m.shadowLastAtMS.Load())
	f.set.set(resources.Settings{Mode: resources.ModeLease})
	f.m.admissionPass(context.Background(), "")
	assert.Zero(t, f.m.shadowLastAtMS.Load())
	f.set.set(resources.Settings{Mode: resources.ModeAdvise})
	f.queue("b", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "")
	assert.Equal(t, int64(1000), f.shadowOf("b").WaitMS, "no stale gap: granted by the shadow at once")
	assert.Equal(t, 0, f.shadowOf("b").WouldWait)
}

// A row's shadow verdict is written once.
func TestShadow_RecordedOnce(t *testing.T) {
	f := newShadowFix(t)
	f.heldLease("h", resources.ScopeProcess, 100)
	require.NoError(t, f.m.store.SetShadow("h", 1234, false, `{"gap":1}`))
	require.NoError(t, f.m.store.SetShadow("h", 99, true, `{"danger":9}`))
	r := f.shadowOf("h")
	assert.Equal(t, int64(1234), r.WaitMS)
	assert.Equal(t, 0, r.Open)
	assert.Equal(t, `{"gap":1}`, r.Reasons)
}
