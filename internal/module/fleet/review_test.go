package fleetmod

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/fleet"
	"github.com/wake/purdex/internal/middleware"
)

func TestPartition_DifferentOrders_ConvergeBySmallerAlias(t *testing.T) {
	p := newPair(t)
	p.bs.change(func(s *Settings) { s.Order = []string{"mlab", "air26"} }) // B disagrees about the order
	p.ab.setDown(true)
	p.ba.setDown(true)
	p.a.Startup(ctx())
	p.b.Startup(ctx())
	require.Equal(t, int64(1), p.a.State().Term)
	require.Equal(t, int64(1), p.b.State().Term)
	p.ab.setDown(false)
	p.ba.setDown(false)
	p.a.Beat(ctx())
	p.b.Beat(ctx())
	require.Equal(t, fleet.RolePrimary, p.a.State().Role, "air26 < mlab: the same answer on both sides")
	require.Equal(t, fleet.RoleStandby, p.b.State().Role)
}

// blockPeer blocks until the context ends, as a call to a peer that does not answer.
type blockPeer struct{ called chan struct{} }

func (b *blockPeer) Role(c context.Context, _ RoleRequest) (RoleAnswer, error) {
	close(b.called)
	<-c.Done()
	return RoleAnswer{}, c.Err()
}
func (b *blockPeer) Announce(c context.Context, _ int64) error { <-c.Done(); return c.Err() }

func TestStop_MidHeartbeat_NoTakeoverNoStateWriteReturnsFast(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	set := &fakeSettings{set: Settings{Order: []string{"air26", "mlab"}, Enabled: true, Self: "mlab"}}
	bp := &blockPeer{called: make(chan struct{})}
	e := NewEngine(bp, set, newClock().Now, nil, path, "h")
	e.refresh()
	c, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.Beat(c); close(done) }()
	<-bp.called
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Beat did not return within 1 s of the stop")
	}
	require.Equal(t, fleet.RoleStandby, e.State().Role, "a stop is not the other being dead")
	_, err := os.Stat(path)
	require.True(t, os.IsNotExist(err), "state.json not written")
}

func TestStop_MidStartup_BecomesNothing(t *testing.T) {
	set := &fakeSettings{set: Settings{Order: []string{"air26", "mlab"}, Enabled: true, Self: "air26"}}
	bp := &blockPeer{called: make(chan struct{})}
	e := NewEngine(bp, set, newClock().Now, nil, "", "h")
	c, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.Startup(c); close(done) }()
	<-bp.called
	cancel()
	<-done
	require.Equal(t, fleet.RoleStandby, e.State().Role, "no primary from an unreachable that was only a stop")
}

type failSettings struct {
	reads int
	err   error
}

func (f *failSettings) Rev() (int64, error)     { return 0, f.err }
func (f *failSettings) Read() (Settings, error) { f.reads++; return Settings{}, f.err }

func TestSettingsReadError_LoggedOnceWithBackoffCappedAtAMinute(t *testing.T) {
	clk := newClock()
	fs := &failSettings{err: errors.New("db locked")}
	var logs atomic.Int32
	e := NewEngine(&link{}, fs, clk.Now, func(string, ...any) { logs.Add(1) }, "", "h")
	for i := 0; i < 20; i++ { // many calls at one instant: one read
		e.refresh()
	}
	require.Equal(t, 1, fs.reads)
	for i := 0; i < 12; i++ {
		clk.Add(61 * time.Second)
		e.refresh()
	}
	require.Equal(t, 13, fs.reads, "at most once a minute, once per retry")
	require.Equal(t, int32(1), logs.Load(), "the same error is logged once")
	e.mu.Lock()
	require.LessOrEqual(t, e.backoff, time.Minute)
	e.mu.Unlock()
	fs.err = errors.New("another")
	clk.Add(61 * time.Second)
	e.refresh()
	require.Equal(t, int32(2), logs.Load(), "a changed error is logged")
}

func TestRoutes_FromAliasIsBoundToThePrincipal(t *testing.T) {
	r := newRig(t)
	r.m.core.Cfg.Peers.Hosts = append(r.m.core.Cfg.Peers.Hosts, config.PeerHost{Alias: "third", HostID: "third:cccccc", URL: "http://y", Token: "t", InboundToken: "j"})
	third := &middleware.Principal{Kind: middleware.PrincipalHost, Alias: "third", HostID: "third:cccccc"}
	body := func(from, term string) string {
		return `{"to_host_id":"` + ourID + `","from_alias":"` + from + `","role":"primary","term":` + term + `}`
	}
	// a third paired host claiming to be mlab, or using its own alias (not in the order)
	require.Equal(t, http.StatusForbidden, r.post(pathRole, third, body("mlab", "999")).Code)
	require.Equal(t, http.StatusForbidden, r.post(pathRole, third, body("third", "999")).Code)
	require.Equal(t, http.StatusForbidden, r.post(pathAnnounce, third, `{"to_host_id":"`+ourID+`","from_alias":"mlab","term":999}`).Code)
	require.Equal(t, int64(0), r.m.engine.State().Term, "role and term unchanged")
	// the real one, case-insensitively
	require.Equal(t, http.StatusOK, r.post(pathRole, hostP, body("MLAB", "3")).Code)
	// term bounds
	require.Equal(t, http.StatusBadRequest, r.post(pathRole, hostP, body("mlab", "1099511627777")).Code)
	require.Equal(t, http.StatusOK, r.post(pathRole, hostP, body("mlab", "1099511627776")).Code)
}
