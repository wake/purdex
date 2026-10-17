package fleetmod

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	leaf "github.com/wake/purdex/internal/fleet"
	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/wwd/wire"
)

// fakePool is the pool source of a test: whatever the test last set.
type fakePool struct {
	mu   sync.Mutex
	pool wire.Pool
	err  error
}

func (f *fakePool) get(context.Context) (wire.Pool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pool, f.err
}

func (f *fakePool) set(p wire.Pool, err error) {
	f.mu.Lock()
	f.pool, f.err = p, err
	f.mu.Unlock()
}

func ptr[T any](v T) *T { return &v }

func factsOf(alias string, at time.Time, cpuIdle float64, pressure int, zone wire.Zone) *wire.Facts {
	return &wire.Facts{
		V: wire.Proto, HostID: alias + "-id", Alias: alias, At: at, Platform: "darwin",
		CPU:     wire.CPU{NCPU: 10, Load1: 2, IdlePct: cpuIdle},
		Mem:     wire.Mem{TotalMB: 16000, AvailableMB: 4000, Pressure: pressure, PageOutRate: ptr(3.0)},
		Disk:    []wire.Disk{{Mount: "/", FreeGB: 120, UsedPct: 40}},
		Procs:   []wire.Proc{{PID: 1, Name: "a", CPUPct: 10, MemMB: 900}, {PID: 2, Name: "b", CPUPct: 80, MemMB: 100}},
		Derived: wire.Derived{Zone: zone},
	}
}

func peerOK(alias string, at time.Time, cpuIdle float64, pressure int, zone wire.Zone) wire.Peer {
	return wire.Peer{Info: wire.Info{HostID: alias + "-id", Alias: alias}, Facts: factsOf(alias, at, cpuIdle, pressure, zone), Reachable: true}
}

func newPoolRig(t *testing.T) (*mrig, *fakePool) {
	t.Helper()
	r := newMRig(t, silent)
	fp := &fakePool{}
	r.m.d.Pool = fp.get
	r.setSource(hostconfig.FleetSourcePool)
	return r, fp
}

// setSource flips the fleet row's source the way a PUT would: the row changes and its revision moves.
func (r *mrig) setSource(src string) {
	r.mu.Lock()
	r.fleet.Source = src
	r.mu.Unlock()
	r.fleetRv.Add(1)
}

func (r *mrig) hasSession(key string) bool {
	_, ok := r.m.sessions[key]
	return ok
}

// With the pool as the source the rows keep the shape GET /api/fleet always had: id, kind, alias, status, fast and slow come
// from the peer's facts, the zone is wwd's own, and no SSH session is started for the peer.
func TestPoolSource_RowsFromFacts(t *testing.T) {
	r, fp := newPoolRig(t)
	now := r.fc.Now()
	fp.set(wire.Pool{Peers: []wire.Peer{
		peerOK("mlab", now, 5, 2, wire.ZoneWarning),
		peerOK("air26", now, 90, 1, wire.ZoneNormal),
	}}, nil)
	r.start()
	r.tick()

	self := r.host("mlab:abc123")
	require.NotNil(t, self)
	require.NotNil(t, self.Fast)
	assert.Equal(t, "ok", self.Status)
	assert.Equal(t, 95.0, self.Fast.CPU, "cpu is 100 - idle")
	assert.Equal(t, 75.0, self.Fast.Mem, "mem is the used share")
	assert.Equal(t, 2, self.Fast.Level)
	assert.Equal(t, 3.0, self.Fast.PageRate)
	assert.Equal(t, 40.0, self.Fast.Disk)
	assert.Equal(t, 120.0, self.Fast.DiskFreeGB)
	assert.Equal(t, "warning", self.Fast.Zone, "the zone is wwd's, not a second latch")
	assert.Equal(t, 10, self.Fast.NCPU)
	require.NotNil(t, self.Slow)
	require.Len(t, self.Slow.TopCPU, 2)
	assert.Equal(t, "b", self.Slow.TopCPU[0].Name, "ranked by cpu")
	assert.Equal(t, "a", self.Slow.TopMem[0].Name, "ranked by memory")
	assert.Nil(t, self.Slow.Queue, "the queue comes with the jobs API (2e)")

	peer := r.host("air26:def456")
	require.NotNil(t, peer)
	assert.Equal(t, "ok", peer.Status)
	require.NotNil(t, peer.Fast)
	assert.Equal(t, 10.0, peer.Fast.CPU)
	assert.Empty(t, r.m.sessions, "no ssh session in pool mode")
	assert.Zero(t, startsOf(r.sshDir), "and none was started and stopped again on the way (a host the pool lists is never probed)")
}

// Facts that stop moving go stale even though wwd keeps answering; facts of a peer wwd says is unreachable are not believed.
func TestPoolSource_StaleFactsAndUnreachableWithOldFacts(t *testing.T) {
	r, fp := newPoolRig(t)
	now := r.fc.Now()
	old := peerOK("air26", now, 50, 1, wire.ZoneNormal)
	old.Reachable = false // unreachable, but its last facts are still in the answer
	fp.set(wire.Pool{Peers: []wire.Peer{peerOK("mlab", now, 50, 1, wire.ZoneNormal), old}}, nil)
	r.start()
	r.tick()
	assert.Equal(t, "ok", r.host("mlab:abc123").Status)
	assert.Equal(t, "offline", r.host("air26:def456").Status, "an unreachable peer's facts are not used")

	r.fc.Advance(10 * time.Second) // wwd's 5 s sampling plus its 5 s peer poll: facts this old are healthy
	r.tick()
	assert.Equal(t, "ok", r.host("mlab:abc123").Status, "no flapping at the normal cadence")

	r.fc.Advance(poolStaleAfter + time.Second) // the same facts (same At) come back: nothing new was read
	r.tick()
	assert.Equal(t, "offline", r.host("mlab:abc123").Status, "an old reading is not ok")
}

// A host whose clock jumped into the future for one sample and then came back must not be locked out: the next facts, stamped
// earlier than the future one, are still new readings.
func TestPoolSource_AFutureStampDoesNotLockTheRow(t *testing.T) {
	r, fp := newPoolRig(t)
	now := r.fc.Now()
	fp.set(wire.Pool{Peers: []wire.Peer{peerOK("mlab", now.Add(24*time.Hour), 50, 1, wire.ZoneNormal)}}, nil)
	r.start()
	r.tick()
	assert.Equal(t, "ok", r.host("mlab:abc123").Status)

	r.fc.Advance(5 * time.Second)
	fp.set(wire.Pool{Peers: []wire.Peer{peerOK("mlab", r.fc.Now(), 10, 1, wire.ZoneNormal)}}, nil)
	r.tick()
	assert.Equal(t, 90.0, r.host("mlab:abc123").Fast.CPU, "the corrected clock's sample is taken")

	r.fc.Advance(poolStaleAfter - time.Second)
	fp.set(wire.Pool{Peers: []wire.Peer{peerOK("mlab", r.fc.Now(), 10, 1, wire.ZoneNormal)}}, nil)
	r.tick()
	assert.Equal(t, "ok", r.host("mlab:abc123").Status, "and it stays ok")
}

// A peer wwd cannot reach, a missing peer and a failing wwd all read offline; a returning wwd brings the row back.
func TestPoolSource_UnreachableAndWWDDown(t *testing.T) {
	r, fp := newPoolRig(t)
	now := r.fc.Now()
	fp.set(wire.Pool{Peers: []wire.Peer{
		peerOK("mlab", now, 50, 1, wire.ZoneNormal),
		{Info: wire.Info{HostID: "air26-id", Alias: "air26"}, Facts: nil, Reachable: false},
	}}, nil)
	r.start()
	r.tick()
	assert.Equal(t, "ok", r.host("mlab:abc123").Status)
	p := r.host("air26:def456")
	assert.Equal(t, "offline", p.Status)
	assert.Nil(t, p.Fast)

	fp.set(wire.Pool{}, errors.New("wwd down"))
	r.fc.Advance(poolStaleAfter + time.Second)
	r.tick()
	self := r.host("mlab:abc123")
	assert.Equal(t, "offline", self.Status)
	require.NotNil(t, self.Fast, "the last figures stay")
	assert.Equal(t, "unknown", self.Fast.Zone)

	fp.set(wire.Pool{Peers: []wire.Peer{peerOK("mlab", r.fc.Now(), 50, 1, wire.ZoneNormal)}}, nil)
	r.tick()
	assert.Equal(t, "ok", r.host("mlab:abc123").Status)
}

// Activating in pool mode starts no session of its own: the first tick, which has asked the pool, starts the ones that are needed.
func TestPoolSource_ActivationStartsNoSessionBeforeThePoolIsAsked(t *testing.T) {
	r, fp := newPoolRig(t)
	now := r.fc.Now()
	fp.set(wire.Pool{Peers: []wire.Peer{peerOK("mlab", now, 50, 1, wire.ZoneNormal), peerOK("air26", now, 50, 1, wire.ZoneNormal)}}, nil)
	r.start()
	assert.False(t, r.hasSession("peer"), "nothing is probed before the pool has been read")
}

// A host the pool does not list (yet) is read over SSH as before; the moment the pool lists it the session goes and the pool's
// facts are the source; if the pool drops it again the session comes back.
func TestPoolSource_HostNotInThePoolFallsBackToSSHAndBack(t *testing.T) {
	r, fp := newPoolRig(t)
	now := r.fc.Now()
	fp.set(wire.Pool{Peers: []wire.Peer{peerOK("mlab", now, 50, 1, wire.ZoneNormal)}}, nil) // air26 is not in the pool
	r.start()
	r.tick()
	assert.True(t, r.hasSession("peer"), "air26 is not in the pool: it is probed over ssh")
	assert.False(t, r.hasSession("self"))
	assert.Equal(t, "ok", r.host("mlab:abc123").Status)

	fp.set(wire.Pool{Peers: []wire.Peer{peerOK("mlab", now, 50, 1, wire.ZoneNormal), peerOK("air26", now, 90, 1, wire.ZoneNormal)}}, nil)
	r.tick()
	assert.False(t, r.hasSession("peer"), "air26 is in the pool now: the session is stopped")
	assert.False(t, r.m.hosts["peer"].poolOwned, "the facts are taken only once the session is gone, so its last reading cannot overwrite them")
	r.tick()
	p := r.host("air26:def456")
	require.NotNil(t, p.Fast)
	assert.Equal(t, "ok", p.Status)
	assert.Equal(t, 10.0, p.Fast.CPU, "the pool's facts")

	fp.set(wire.Pool{Peers: []wire.Peer{peerOK("mlab", now, 50, 1, wire.ZoneNormal)}}, nil)
	r.tick()
	assert.True(t, r.hasSession("peer"), "air26 left the pool: ssh again")
	assert.Equal(t, "offline", r.host("air26:def456").Status, "and it reads offline until ssh reads it")
}

// A probe host takes the same path as the peer: the alias in the config is matched against the pool.
func TestPoolSource_ProbeHostFollowsThePool(t *testing.T) {
	r, fp := newPoolRig(t)
	r.setProbes(hostconfig.ProbeHost{ID: "dd1b70b2", SSHAlias: "mserv", Name: "mserv"})
	now := r.fc.Now()
	fp.set(wire.Pool{Peers: []wire.Peer{peerOK("mlab", now, 50, 1, wire.ZoneNormal), peerOK("air26", now, 50, 1, wire.ZoneNormal)}}, nil)
	r.start()
	r.tick()
	assert.True(t, r.hasSession("probe:dd1b70b2"), "mserv has no wwd yet")

	fp.set(wire.Pool{Peers: []wire.Peer{peerOK("mlab", now, 50, 1, wire.ZoneNormal), peerOK("air26", now, 50, 1, wire.ZoneNormal),
		peerOK("mserv", now, 20, 1, wire.ZoneNormal)}}, nil)
	r.tick()
	assert.False(t, r.hasSession("probe:dd1b70b2"))
	r.tick() // the facts are taken once the session is gone
	h := r.host("dd1b70b2")
	require.NotNil(t, h)
	assert.Equal(t, KindProbe, h.Kind)
	assert.Equal(t, "ok", h.Status)
	assert.Equal(t, 80.0, h.Fast.CPU)
}

// A wwd that cannot be asked for longer than a fact may stand still hands every row back to the old sources; the own host is
// read from the LocalSource again.
func TestPoolSource_WWDGoneForGoodFallsBackToTheOldSources(t *testing.T) {
	r, fp := newPoolRig(t)
	now := r.fc.Now()
	fp.set(wire.Pool{Peers: []wire.Peer{peerOK("mlab", now, 50, 1, wire.ZoneNormal), peerOK("air26", now, 50, 1, wire.ZoneNormal)}}, nil)
	r.start()
	r.tick()
	assert.False(t, r.hasSession("peer"))

	fp.set(wire.Pool{}, errors.New("wwd down"))
	r.fc.Advance(poolStaleAfter / 2)
	r.tick()
	assert.False(t, r.hasSession("peer"), "a short outage does not change the sources")

	r.fc.Advance(poolStaleAfter)
	r.local.SetFast(leaf.LocalFast{At: r.fc.Now(), CPU: 33, Mem: 50, Level: 1, NCPU: 10, Load1: 2})
	r.tick()
	assert.True(t, r.hasSession("peer"), "a long one does")
	self := r.host("mlab:abc123")
	require.NotNil(t, self.Fast)
	assert.Equal(t, 33.0, self.Fast.CPU, "the own host is read from the local source again")
	assert.Equal(t, "ok", self.Status)
}

// source "ssh" (or nothing) never asks the pool; flipping the row needs no restart in either direction.
func TestPoolSource_TheRowFlipsTheSourceWithoutARestart(t *testing.T) {
	r, fp := newPoolRig(t)
	r.setSource(hostconfig.FleetSourceSSH)
	now := r.fc.Now()
	fp.set(wire.Pool{Peers: []wire.Peer{peerOK("mlab", now, 50, 1, wire.ZoneNormal), peerOK("air26", now, 50, 1, wire.ZoneNormal)}}, nil)
	r.start()
	r.setLocal(11, 1)
	r.tick()
	assert.True(t, r.hasSession("peer"), "ssh source: the peer is probed")
	assert.Equal(t, 11.0, r.host("mlab:abc123").Fast.CPU, "and the own host is the local source's")

	r.setSource(hostconfig.FleetSourcePool)
	r.tick()
	assert.False(t, r.hasSession("peer"), "pool source: air26 is in the pool")
	assert.Equal(t, 50.0, r.host("mlab:abc123").Fast.CPU)

	r.setSource(hostconfig.FleetSourceSSH)
	r.tick()
	assert.True(t, r.hasSession("peer"), "back to ssh")
	assert.False(t, r.m.hosts["peer"].poolOwned, "the pool no longer owns the row")
	assert.NotEqual(t, "ok", r.host("air26:def456").Status, "and it does not keep the pool's figures as ok")
}

// The old source's error (an ssh host-key refusal, say) does not outlive its session: a listed host the pool has nothing for
// reads offline, not "error".
func TestPoolSource_ListedWithoutFactsDropsAnOldSSHError(t *testing.T) {
	r, fp := newPoolRig(t)
	fp.set(wire.Pool{Peers: []wire.Peer{peerOK("mlab", r.fc.Now(), 50, 1, wire.ZoneNormal)}}, nil) // air26 not listed: ssh
	r.start()
	r.tick()
	require.True(t, r.hasSession("peer"))
	r.m.mu.Lock()
	r.m.hosts["peer"].status, r.m.hosts["peer"].code = leaf.StatusError, leaf.CodeHostKey
	r.m.mu.Unlock()

	fp.set(wire.Pool{Peers: []wire.Peer{peerOK("mlab", r.fc.Now(), 50, 1, wire.ZoneNormal),
		{Info: wire.Info{HostID: "air26-id", Alias: "air26"}, Facts: nil, Reachable: false}}}, nil)
	r.tick() // the session is stopped
	r.tick() // the pool owns the row
	h := r.host("air26:def456")
	assert.Equal(t, "offline", h.Status)
	assert.Equal(t, "timeout", h.Error)
}

// A host that was read by another source and is now listed by the pool without usable facts (unreachable) must not keep the old
// source's ok.
func TestPoolSource_ListedWithoutFactsIsOfflineNotTheOldOk(t *testing.T) {
	r, fp := newPoolRig(t)
	fp.set(wire.Pool{}, nil) // the pool lists nobody: the own host is the local source's
	r.start()
	r.setLocal(10, 1)
	r.tick()
	require.Equal(t, "ok", r.host("mlab:abc123").Status)

	fp.set(wire.Pool{Peers: []wire.Peer{{Info: wire.Info{HostID: "mlab-id", Alias: "mlab"}, Facts: nil, Reachable: false}}}, nil)
	r.tick()
	assert.Equal(t, "offline", r.host("mlab:abc123").Status)
}
