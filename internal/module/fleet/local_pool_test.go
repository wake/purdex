package fleetmod

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	leaf "github.com/wake/purdex/internal/fleet"
	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/wwd/wire"
)

// C1: while the pool serves this host, the LocalSource's 1 s tick (the FastReader's sysctls) is not running.

func TestLocalSource_NotStartedWhilePoolServesThisHost(t *testing.T) {
	r, fp := newPoolRig(t)
	now := r.fc.Now()
	fp.set(wire.Pool{Peers: []wire.Peer{peerOK("mlab", now, 50, 1, wire.ZoneNormal), peerOK("air26", now, 50, 1, wire.ZoneNormal)}}, nil)
	r.start()
	assert.Zero(t, r.local.Starts(), "pool mode: nothing is known about the pool yet, and nothing is started")
	r.tick()
	r.tick()
	assert.Zero(t, r.local.Starts(), "the pool serves this host")
}

func TestLocalSource_StartsWhenThePoolCannotServeAndStopsWhenItCan(t *testing.T) {
	r, fp := newPoolRig(t)
	now := r.fc.Now()
	fp.set(wire.Pool{Peers: []wire.Peer{peerOK("mlab", now, 50, 1, wire.ZoneNormal)}}, nil)
	r.start()
	r.tick()
	require.Zero(t, r.local.Starts())

	fp.set(wire.Pool{}, errors.New("wwd down"))
	r.fc.Advance(poolStaleAfter * 2)
	r.setLocal(33, 1)
	r.tick()
	assert.Equal(t, 1, r.local.Starts(), "the pool gave up this host: the local source runs again")
	r.tick()
	assert.Equal(t, 1, r.local.Starts(), "and is started once")
	stops := r.local.Stops()

	fp.set(wire.Pool{Peers: []wire.Peer{peerOK("mlab", r.fc.Now(), 50, 1, wire.ZoneNormal)}}, nil)
	r.tick()
	assert.Equal(t, stops+1, r.local.Stops(), "the pool serves it again: the local source is stopped")
	r.tick()
	assert.Equal(t, stops+1, r.local.Stops())
	assert.Equal(t, 1, r.local.Starts())
}

// The ssh source is as it always was: started with the measuring, not on the first tick.
func TestLocalSource_SshSourceStartsWithTheMeasuring(t *testing.T) {
	r, _ := newPoolRig(t)
	r.setSource(hostconfig.FleetSourceSSH)
	r.start()
	assert.Equal(t, 1, r.local.Starts())
	r.tick()
	assert.Equal(t, 1, r.local.Starts())
}

// A reading the local source kept from before it was stopped is not taken for a fresh one when it is started again.
func TestLocalSource_AReadingFromBeforeTheRestartIsNotTaken(t *testing.T) {
	r, fp := newPoolRig(t)
	now := r.fc.Now()
	fp.set(wire.Pool{Peers: []wire.Peer{peerOK("mlab", now, 50, 1, wire.ZoneNormal)}}, nil)
	r.start()
	r.tick()
	r.local.SetFast(leaf.LocalFast{At: now.Add(-10 * time.Minute), CPU: 99, Mem: 50, Level: 1, NCPU: 10, Load1: 2}) // what the source kept from long ago

	fp.set(wire.Pool{}, errors.New("wwd down"))
	r.fc.Advance(poolStaleAfter * 2)
	r.tick()
	self := r.host("mlab:abc123")
	require.NotNil(t, self)
	if self.Fast != nil {
		assert.NotEqual(t, 99.0, self.Fast.CPU, "the old reading is not the own host's")
	}

	r.setLocal(33, 1) // a reading taken after the start
	r.tick()
	assert.Equal(t, 33.0, r.host("mlab:abc123").Fast.CPU)
}
