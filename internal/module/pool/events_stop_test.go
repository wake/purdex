package poolmod

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

// A notice that is already being sent when Stop comes finishes before Stop returns, and one that begins after Stop is dropped: nothing goes
// out once Stop has returned.
func TestPoolChanged_StopWaitsForASendUnderWayAndDropsLaterOnes(t *testing.T) {
	m := New()
	m.watching = true
	entered, release := make(chan struct{}), make(chan struct{})
	n := &noticed{}
	m.emit = func(ev core.HostEvent) {
		close(entered)
		<-release
		n.emit(ev)
	}
	go m.send("mserv")
	<-entered

	stopped := make(chan struct{})
	go func() {
		_ = m.Stop(context.Background())
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("Stop returned while a send was still under way")
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not return after the send ended")
	}
	assert.Len(t, n.all(), 1, "the send that had begun is delivered")

	m.send("air26") // a notice past its window that reaches send after Stop
	assert.Len(t, n.all(), 1, "nothing goes out after Stop")
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
		return contains(body, `"pool.v1"`), contains(body, `"pool.events.v1"`)
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

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
