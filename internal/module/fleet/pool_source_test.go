package fleetmod

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
		Mem:     wire.Mem{TotalMB: 16000, AvailableMB: 8000, Pressure: pressure, PageOutRate: ptr(3.0)},
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
	return r, fp
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
	assert.Equal(t, 50.0, self.Fast.Mem, "mem is the used share")
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
	r.fc.Advance(ownStaleAfter + time.Second)
	r.tick()
	self := r.host("mlab:abc123")
	assert.Equal(t, "offline", self.Status)
	require.NotNil(t, self.Fast, "the last figures stay")
	assert.Equal(t, "unknown", self.Fast.Zone)

	fp.set(wire.Pool{Peers: []wire.Peer{peerOK("mlab", r.fc.Now(), 50, 1, wire.ZoneNormal)}}, nil)
	r.tick()
	assert.Equal(t, "ok", r.host("mlab:abc123").Status)
}
