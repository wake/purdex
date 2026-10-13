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
// the usage flat it holds the next request, and lets it in once the trend
// falls. (M: running always false and b is granted at once.)
func TestShadow_RunningComesFromTheRealHeldRows(t *testing.T) {
	f := newShadowFix(t)
	f.setZone("warning", "flat")
	f.queue("a", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "")
	assert.Equal(t, int64(1000), f.shadowOf("a").WaitMS, "nothing was running: granted")
	f.advance(40 * time.Second)
	f.queue("b", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "")
	assert.Equal(t, int64(-1), f.shadowOf("b").WaitMS, "a is held: warning and flat observes")
	f.setZone("warning", "falling")
	f.m.admissionPass(context.Background(), "")
	assert.NotEqual(t, int64(-1), f.shadowOf("b").WaitMS)
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
