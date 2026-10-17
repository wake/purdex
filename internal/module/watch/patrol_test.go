package watchmod

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/watch"
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
