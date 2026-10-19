package poolmod

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

// A notice that has been let through when Stop comes is emitted before Stop returns, and one that comes after Stop is dropped: nothing
// goes out once Stop has returned.
func TestPoolChanged_StopWaitsForANoticeUnderWayAndDropsLaterOnes(t *testing.T) {
	m := New()
	m.mergeWithin = time.Hour
	m.watching = true
	entered, release := make(chan struct{}), make(chan struct{})
	n := &noticed{}
	m.emit = func(ev core.HostEvent) {
		close(entered)
		<-release
		n.emit(ev)
	}
	go m.notify("mserv") // nothing sent for it yet: at once
	<-entered

	stopped := make(chan struct{})
	go func() {
		_ = m.Stop(context.Background())
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("Stop returned while a notice was still being emitted")
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not return after the notice was emitted")
	}
	assert.Len(t, n.all(), 1, "the notice that had been let through is delivered")

	m.notify("air26") // reaches notify after Stop
	assert.Len(t, n.all(), 1, "nothing goes out after Stop")
}

// A timer callback that Stop and a Start overtook does nothing: the old window's notice does not leak into the new watch.
func TestPoolChanged_ATimerOfAnEarlierStartIsDropped(t *testing.T) {
	m := New()
	m.mergeWithin = 200 * time.Millisecond
	m.watching = true
	n := &noticed{}
	m.emit = n.emit
	m.sentAt["mserv"] = time.Now() // inside the window: the next notice waits
	m.notify("mserv")
	require.Len(t, m.pending, 1)
	require.NoError(t, m.Stop(context.Background()))
	m.mu.Lock()
	m.watching = true // a Start of the next generation
	m.mu.Unlock()
	time.Sleep(500 * time.Millisecond)
	assert.Empty(t, n.all(), "the old timer must not emit in the new generation")
}

// A module stopped and started again in the same core serves pool.v1 and announces pool.events.v1 again.
func TestInfo_PoolCapabilitiesComeBackAfterARestart(t *testing.T) {
	t.Setenv("PDX_WWD_SOCK", filepath.Join(t.TempDir(), "none.sock"))
	m := New()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}, Registry: core.NewServiceRegistry()})
	c.AddModule(m)
	require.NoError(t, m.Init(c))
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	has := func() (v1, events bool) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/info", nil))
		body := rec.Body.String()
		return strings.Contains(body, `"pool.v1"`), strings.Contains(body, `"pool.events.v1"`)
	}
	require.NoError(t, m.Start(context.Background()))
	v1, ev := has()
	require.True(t, v1 && ev)
	require.NoError(t, m.Stop(context.Background()))
	v1, ev = has()
	require.False(t, v1 || ev)
	require.NoError(t, m.Start(context.Background()))
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	v1, ev = has()
	assert.True(t, v1 && ev, "pool.v1 and pool.events.v1 are back after Start")
}
