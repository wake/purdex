package poolmod

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

func wwdOf(m *Module) (map[string]string, bool) {
	v, ok := m.Status()["wwd"].(map[string]string)
	return v, ok
}

// Once the watch has read the local wwd's pool, the module knows who that wwd is: the first peer is this host (wwd's own entry comes
// first), and its host_id and alias are what Status reports, whatever the Purdex daemon's own host_id is.
func TestIdentity_TheFirstPeerOfTheLocalPoolIsThisWwd(t *testing.T) {
	lp := newLivePool(t)
	m, _ := watching(t, fakeWWD(t, lp.handler()))
	require.Eventually(t, func() bool { _, ok := wwdOf(m); return ok }, 3*time.Second, testPoll)
	got, _ := wwdOf(m)
	assert.Equal(t, lp.get().Peers[0].Info.HostID, got["host_id"])
	assert.Equal(t, "mlab", got["alias"])
}

// Before the first read, and while wwd cannot be read, there is no identity at all: the key is missing, not empty strings.
func TestIdentity_NoneBeforeTheFirstReadNorWhileWwdIsAway(t *testing.T) {
	var up atomic.Bool
	lp := newLivePool(t)
	h := lp.handler()
	m, _ := watching(t, fakeWWD(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		h(w, r)
	})))
	time.Sleep(5 * testPoll)
	_, ok := wwdOf(m)
	assert.False(t, ok, "nothing read yet")

	up.Store(true)
	require.Eventually(t, func() bool { _, ok := wwdOf(m); return ok }, 3*time.Second, testPoll)

	up.Store(false)
	require.Eventually(t, func() bool { _, ok := wwdOf(m); return !ok }, 3*time.Second, testPoll, "wwd went away: the identity is dropped")
}

// A wwd that is replaced by another host's (the socket now answers as someone else) changes the identity at the next read.
func TestIdentity_FollowsTheLocalWwd(t *testing.T) {
	lp := newLivePool(t)
	m, _ := watching(t, fakeWWD(t, lp.handler()))
	require.Eventually(t, func() bool { _, ok := wwdOf(m); return ok }, 3*time.Second, testPoll)
	lp.change(func(p *wire.Pool) { p.Peers[0].Info.HostID, p.Peers[0].Info.Alias = "h-new", "newname" })
	require.Eventually(t, func() bool { got, _ := wwdOf(m); return got["host_id"] == "h-new" && got["alias"] == "newname" }, 3*time.Second, testPoll)
}

// A wwd that hangs does not hold Status (what /api/info reads) up: the identity comes from memory, and Status answers at once. The last
// read stays until a read fails; a hung call has not failed yet.
func TestIdentity_StatusDoesNotWaitOnAHungWwd(t *testing.T) {
	lp := newLivePool(t)
	h := lp.handler()
	var hang atomic.Bool
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	m, _ := watching(t, fakeWWD(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hang.Load() {
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		h(w, r)
	})))
	require.Eventually(t, func() bool { _, ok := wwdOf(m); return ok }, 3*time.Second, testPoll)
	hang.Store(true)
	time.Sleep(5 * testPoll) // the watch is now inside a call that does not return

	done := make(chan struct{})
	go func() { _ = m.Status(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Status waited on wwd")
	}
}

// An empty pool (no peers) names no one.
func TestIdentity_AnEmptyPoolHasNoIdentity(t *testing.T) {
	lp := newLivePool(t)
	lp.change(func(p *wire.Pool) { p.Peers = nil })
	m, _ := watching(t, fakeWWD(t, lp.handler()))
	time.Sleep(5 * testPoll)
	_, ok := wwdOf(m)
	assert.False(t, ok)
}
