package watchmod

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/team"
	"github.com/wake/purdex/internal/watch"
	"github.com/wake/purdex/internal/wwd/wire"
)

type patrolRig struct {
	*recRig
	mu                        sync.Mutex
	calls                     [][2]string
	present, unattended, busy bool
	freeGB                    float64
	failWake                  bool
}

func newPatrolRig(t *testing.T) *patrolRig { return newPatrolRigWith(t, true) }

// newPatrolRigWith is the rig with the watcher registered (awake) or this host holding none.
func newPatrolRigWith(t *testing.T, registered bool) *patrolRig {
	t.Helper()
	pr := &patrolRig{recRig: newRecRig(t), freeGB: 100}
	pr.m.psrc = &patrolSources{
		atComputer: func() bool { return pr.present },
		unattended: func() bool { return pr.unattended },
		busy:       func() bool { return pr.busy },
		diskFreeGB: func() (float64, bool) { return pr.freeGB, true },
	}
	pr.m.wake = func(_ context.Context, reason, detail string) error {
		pr.mu.Lock()
		defer pr.mu.Unlock()
		pr.calls = append(pr.calls, [2]string{reason, detail})
		if pr.failWake {
			return errNoWatcherHere
		}
		return nil
	}
	if registered {
		pr.owners.set(watcherOwner("f1"), true, nil)
		_, _, _ = pr.m.Reconcile(context.Background()) // the watcher registers at r.now
	}
	return pr
}

func (p *patrolRig) step(after time.Duration) {
	p.now = p.now.Add(after)
	p.m.patrolStep(context.Background())
}

func (p *patrolRig) reasons() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, c := range p.calls {
		out = append(out, c[0])
	}
	return out
}

func TestPatrol_TheIntervalFollowsTheSituation(t *testing.T) {
	cases := []struct {
		name                      string
		present, unattended, busy bool
		every                     time.Duration
	}{
		{"person at the computer", true, false, false, 30 * time.Minute},
		{"away, not in unattended mode", false, false, false, 30 * time.Minute},
		{"unattended and busy", false, true, true, 10 * time.Minute},
		{"unattended and calm", false, true, false, 15 * time.Minute},
		{"present beats unattended", true, true, true, 30 * time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newPatrolRig(t)
			r.present, r.unattended, r.busy = c.present, c.unattended, c.busy
			r.step(c.every - time.Second)
			require.Empty(t, r.reasons(), "not yet")
			r.step(2 * time.Second)
			require.Equal(t, []string{"patrol"}, r.reasons())
			r.step(c.every - 2*time.Second)
			require.Len(t, r.reasons(), 1, "counted from the last note")
			r.step(3 * time.Second)
			require.Len(t, r.reasons(), 2)
		})
	}
}

func TestPatrol_ARestingWatcherIsWokenEveryRestInterval(t *testing.T) {
	r := newPatrolRig(t)
	r.owners.set(watcherOwner("f1"), false, nil) // it rests
	r.step(40 * time.Second)                     // reconcile age; the holder turns resting
	_, _, _ = r.m.Reconcile(context.Background())
	require.Equal(t, watch.HolderResting, r.m.holder.Get().State)
	r.calls = nil
	r.step(9 * time.Minute)
	require.Empty(t, r.reasons())
	r.step(2 * time.Minute)
	require.Equal(t, []string{"patrol"}, r.reasons())
	require.Contains(t, r.calls[0][1], "休息中")
}

func TestPatrol_ALowDiskIsToldAtOnceOncePerTimeItRunsLow(t *testing.T) {
	r := newPatrolRig(t)
	r.freeGB = 9
	r.step(time.Minute)
	require.Equal(t, []string{"disk_low"}, r.reasons())
	require.Contains(t, r.calls[0][1], "剩 9.0 GB")
	r.step(time.Minute)
	require.Equal(t, []string{"disk_low"}, r.reasons(), "not again while it stays low")
	r.freeGB = 40
	r.step(time.Minute)
	r.freeGB = 8
	r.step(time.Minute)
	require.Equal(t, []string{"disk_low", "disk_low"}, r.reasons(), "again after it recovered and ran low again")
}

func TestPatrol_ThisHostWithoutAWatcherOrWithDetectionOffDoesNothing(t *testing.T) {
	r := newPatrolRigWith(t, false)
	r.freeGB = 1
	r.step(2 * time.Hour)
	require.Empty(t, r.reasons())
}

func TestPatrol_AFailedWakeIsTriedAgainAnIntervalLaterNotAtTheNextTick(t *testing.T) {
	r := newPatrolRig(t)
	r.failWake = true
	r.step(31 * time.Minute)
	require.Len(t, r.reasons(), 1)
	r.step(time.Minute)
	require.Len(t, r.reasons(), 1, "not hammered every tick")
	r.step(30 * time.Minute)
	require.Len(t, r.reasons(), 2)
}

// Any note the watcher is given (here through the real WakeWatcher path) restarts the count.
func TestPatrol_ANoteFromAnotherSourceRestartsTheCount(t *testing.T) {
	r := newSessionRig(t, "")
	r.owners.set(watcherOwner("f1"), true, nil)
	_, _, _ = r.m.Reconcile(context.Background())
	r.m.dlv.notifier = &fakeNotifier{}
	r.m.psrc = &patrolSources{atComputer: func() bool { return true }, unattended: func() bool { return false }, busy: func() bool { return false },
		diskFreeGB: func() (float64, bool) { return 100, true }}
	var reasons []string
	r.m.wake = func(_ context.Context, reason, _ string) error { reasons = append(reasons, reason); return nil }
	r.now = r.now.Add(25 * time.Minute)
	require.NoError(t, r.m.WakeWatcher(context.Background(), "unattended_changed", "x"))
	r.now = r.now.Add(10 * time.Minute) // 35 minutes after the watcher started, 10 after the note
	r.m.patrolStep(context.Background())
	require.Empty(t, reasons)
	r.now = r.now.Add(21 * time.Minute)
	r.m.patrolStep(context.Background())
	require.Equal(t, []string{"patrol"}, reasons)
}

// The real sources are the registry's services (what the modules publish), read at each tick.
func TestPatrol_TheSourcesAreTheRegistrysServices(t *testing.T) {
	r := newRig(t)
	require.False(t, r.m.atComputer())
	require.False(t, r.m.unattendedOn())
	require.False(t, r.m.hostBusy())
	reg := r.m.core.Registry
	reg.Register(push.AtComputerKey, atComputerFake(true))
	reg.Register(hostconfig.UnattendedKey, unattendedFake{team.UnattendedState{On: true}})
	reg.Register(resources.HostReaderKey, hostFake{resources.HostUse{Zone: "warning"}})
	require.True(t, r.m.atComputer())
	require.True(t, r.m.unattendedOn())
	require.True(t, r.m.hostBusy())
	reg.Register(resources.HostReaderKey, hostFake{resources.HostUse{Zone: "normal"}})
	require.False(t, r.m.hostBusy())
	_, ok := r.m.diskFreeGB()
	require.True(t, ok, "the data dir's free space is read")
}

type localFactsFake struct {
	f  wire.Facts
	ok bool
}

func (l localFactsFake) LatestLocalFacts() (wire.Facts, bool) { return l.f, l.ok }

// C1: with wwd's facts of this host the patrol reads the free disk from them: the lowest of the listed volumes (the home volume is
// one of them, and the mount names cannot be matched against a path), when the data directory is under home. Otherwise (no fresh
// facts, no disk listed, a data directory elsewhere) it takes its own statfs.
func TestPatrolDiskFreeFromFacts(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	r := newPatrolRigWith(t, true)
	r.m.psrc = nil
	reg := r.m.core.Registry
	r.m.core.Cfg.DataDir = home
	// the names macOS gives: the Data volume for /Users, then the sealed root
	two := wire.Facts{Disk: []wire.Disk{{Mount: "/System/Volumes/Data", FreeGB: 7.5}, {Mount: "/", FreeGB: 42.5}}}
	swapped := wire.Facts{Disk: []wire.Disk{{Mount: "/", FreeGB: 42.5}, {Mount: "/home", FreeGB: 7.5}}}
	one := wire.Facts{Disk: []wire.Disk{{Mount: "/", FreeGB: 42.5}}}

	for name, f := range map[string]wire.Facts{"data volume first": two, "home partition second": swapped} {
		reg.Register(resources.LocalFactsKey, localFactsFake{f, true})
		gb, ok := r.m.diskFreeGB()
		require.True(t, ok, name)
		require.Equal(t, 7.5, gb, name+": the lowest, which is the volume that runs low")
	}
	reg.Register(resources.LocalFactsKey, localFactsFake{one, true})
	gb, ok := r.m.diskFreeGB()
	require.True(t, ok)
	require.Equal(t, 42.5, gb)

	reg.Register(resources.LocalFactsKey, localFactsFake{two, false})
	gb, ok = r.m.diskFreeGB()
	require.True(t, ok)
	require.NotContains(t, []float64{42.5, 7.5}, gb, "no fresh facts: statfs")

	reg.Register(resources.LocalFactsKey, localFactsFake{wire.Facts{}, true})
	gb, ok = r.m.diskFreeGB()
	require.True(t, ok)
	require.NotContains(t, []float64{42.5, 7.5}, gb, "no disk listed: statfs")

	r.m.core.Cfg.DataDir = "/dev" // another device than home and the root: facts do not list its volume
	reg.Register(resources.LocalFactsKey, localFactsFake{two, true})
	gb, ok = r.m.diskFreeGB()
	require.True(t, ok)
	require.NotContains(t, []float64{42.5, 7.5}, gb, "a data directory outside home is statfs'd")
}

func TestSameDevice(t *testing.T) {
	dir := t.TempDir()
	require.True(t, sameDevice(dir, dir))
	require.True(t, sameDevice(dir, filepath.Join(dir, ".")))
	require.False(t, sameDevice(dir, filepath.Join(dir, "missing")), "a path that is not there is on no device")
	require.False(t, sameDevice(dir, "/dev"), "devfs is its own device")
}

type atComputerFake bool

func (a atComputerFake) AtComputer() bool { return bool(a) }

type unattendedFake struct{ st team.UnattendedState }

func (u unattendedFake) Unattended() (team.UnattendedState, error) { return u.st, nil }

type hostFake struct{ h resources.HostUse }

func (h hostFake) LatestHost() (resources.HostUse, bool) { return h.h, true }

// A low-disk note that could not be delivered is made again at the next tick, not forgotten until the disk recovers and runs low again.
func TestPatrol_AFailedLowDiskNoteIsRetriedAtTheNextTick(t *testing.T) {
	r := newPatrolRig(t)
	r.freeGB = 5
	r.failWake = true
	r.step(time.Minute)
	r.step(time.Minute)
	require.Equal(t, []string{"disk_low", "disk_low"}, r.reasons(), "tried at each tick while it fails")
	r.failWake = false
	r.step(time.Minute)
	r.step(time.Minute)
	require.Equal(t, []string{"disk_low", "disk_low", "disk_low"}, r.reasons(), "and once it went through, not again")
}

// A note given by another host restarts the count on the host that holds the watcher (the receiving side of the peer route).
func TestPatrol_ANoteReceivedFromAnotherHostRestartsTheCount(t *testing.T) {
	r := newSessionRig(t, "")
	r.owners.set(watcherOwner("f1"), true, nil)
	_, _, _ = r.m.Reconcile(context.Background())
	r.m.dlv.notifier = &fakeNotifier{}
	r.now = r.now.Add(29 * time.Minute)
	body := `{"to_host_id":"` + ourID + `","reason":"unattended_changed","detail":"d"}`
	require.Equal(t, 200, r.serve(hostCtx(), "/api/peers/watch/note", body).Code)
	r.m.patrol.mu.Lock()
	last := r.m.patrol.last
	r.m.patrol.mu.Unlock()
	require.True(t, last.Equal(r.now), "the count restarts when the note was told")
}

// After a restart the count starts again from the start of the daemon, not from when the watcher registered long ago.
func TestPatrol_AfterARestartTheCountStartsFromTheDaemonsStart(t *testing.T) {
	r := newPatrolRig(t)
	r.now = r.now.Add(5 * time.Hour) // the watcher has been there long
	r.m.patrol.started = r.now
	r.step(10 * time.Minute)
	require.Empty(t, r.reasons(), "ten minutes after the daemon began: not yet")
	r.step(21 * time.Minute)
	require.Equal(t, []string{"patrol"}, r.reasons())
}

func TestPatrol_AClockThatWentBackCountsFromNowNotFromTheFuture(t *testing.T) {
	r := newPatrolRig(t)
	r.m.patrol.last = r.now.Add(3 * time.Hour) // a note stamped by a clock that was ahead
	r.step(29 * time.Minute)                   // the first look: the stamp is replaced by now
	require.Empty(t, r.reasons())
	r.step(29 * time.Minute)
	require.Empty(t, r.reasons(), "an interval has not passed since the first look")
	r.step(2 * time.Minute)
	require.Equal(t, []string{"patrol"}, r.reasons(), "one interval from the first look, not three hours")
}

// A rested holder here does not patrol while another host's watcher is the remembered one.
func TestPatrol_ARestedHolderHereStaysQuietWhileAnotherHostsWatcherIsTheRememberedOne(t *testing.T) {
	r, fc := askerRig(t)
	fc.reply[pathHolder] = done(HolderView{HostID: peerID, State: "awake", SessionID: "s-26", Targets: []string{"air26", "mlab"}})
	_, err := r.m.loc.Current(context.Background(), []string{"air26", "mlab"}, false) // remembers the awake one on air26
	require.NoError(t, err)
	require.NoError(t, r.m.holder.Update(func(h *watch.Holder) { h.State, h.SessionID = watch.HolderResting, "old" }))
	var reasons []string
	r.m.wake = func(_ context.Context, reason, _ string) error { reasons = append(reasons, reason); return nil }
	r.m.psrc = &patrolSources{diskFreeGB: func() (float64, bool) { return 1, true }}
	r.m.patrolStep(context.Background())
	require.Empty(t, reasons, "not the disk alarm either: the watcher is elsewhere")
}

// The daemon's own start ahead of the clock (it went back while running) is brought to now once, not every tick.
func TestPatrol_AStartStampAheadOfTheClockIsBroughtToNowOnce(t *testing.T) {
	r := newPatrolRig(t)
	r.m.patrol.started = r.now.Add(2 * time.Hour)
	r.step(time.Minute)
	r.step(29 * time.Minute)
	require.Empty(t, r.reasons())
	r.step(2 * time.Minute)
	require.Equal(t, []string{"patrol"}, r.reasons(), "one interval after the first look, not when the clock catches up")
}
