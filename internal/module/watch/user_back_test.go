package watchmod

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	ifleet "github.com/wake/purdex/internal/fleet"
	fleetmod "github.com/wake/purdex/internal/module/fleet"
	"github.com/wake/purdex/internal/push"
)

// WK-3b, user_back (watcher wake settings spec 6.5): the person comes back to the computer after being away for at least away_min. Only the
// fleet primary watches; presence has three values (at, away, not known); the first reading is only a baseline; a change of role starts over.

type ubRig struct {
	*recRig
	mu      sync.Mutex
	primary bool
	term    int64
	at      bool
	known   bool
	awayMin int
	calls   [][2]string
	failing bool
}

func newUBRig(t *testing.T) *ubRig {
	t.Helper()
	u := &ubRig{recRig: newRecRig(t), primary: true, known: true, at: true, awayMin: 30}
	u.m.ubsrc = &userBackSources{
		primary:  func() bool { u.mu.Lock(); defer u.mu.Unlock(); return u.primary },
		presence: func() (bool, bool) { u.mu.Lock(); defer u.mu.Unlock(); return u.at, u.known },
		awayMin:  func() int { u.mu.Lock(); defer u.mu.Unlock(); return u.awayMin },
		term:     func() int64 { u.mu.Lock(); defer u.mu.Unlock(); return u.term },
	}
	u.m.wake = func(_ context.Context, reason, detail string) error {
		u.mu.Lock()
		defer u.mu.Unlock()
		u.calls = append(u.calls, [2]string{reason, detail})
		if u.failing {
			return errNoWatcherHere
		}
		return nil
	}
	return u
}

// step moves the clock and takes one reading, with the person at the computer (at) or not.
func (u *ubRig) step(after time.Duration, at bool) {
	u.mu.Lock()
	u.at, u.known = at, true
	u.mu.Unlock()
	u.now = u.now.Add(after)
	u.m.userBackStep(context.Background())
}

func (u *ubRig) woke() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.calls)
}

func TestUserBack_TheFirstReadingIsOnlyABaseline(t *testing.T) {
	u := newUBRig(t)
	u.step(0, true)
	require.Zero(t, u.woke(), "at the computer at the first reading: nothing came back")
	u2 := newUBRig(t)
	u2.step(0, false)
	u2.step(time.Hour, true)
	require.Equal(t, 1, u2.woke(), "away from the first reading on, and back after an hour: the time away is counted from the baseline")
}

func TestUserBack_BackAfterAwayMinWakesOnceAndNotBeforeIt(t *testing.T) {
	for name, c := range map[string]struct {
		away time.Duration
		want int
	}{
		"a minute short":   {29 * time.Minute, 0},
		"exactly away_min": {30 * time.Minute, 1},
		"much longer":      {5 * time.Hour, 1},
	} {
		u := newUBRig(t)
		u.step(0, true)            // baseline: at the computer
		u.step(time.Minute, false) // leaves: counted from this reading
		u.step(c.away, true)       // comes back
		require.Equal(t, c.want, u.woke(), name)
		if c.want == 1 {
			require.Equal(t, "user_back", u.calls[0][0])
			require.NotEmpty(t, u.calls[0][1], "the note says how long the person was away")
		}
	}
}

func TestUserBack_StayingAtTheComputerOrAwayWakesNothingAndComingBackTwiceWakesTwice(t *testing.T) {
	u := newUBRig(t)
	u.step(0, true)
	for range 5 {
		u.step(10*time.Minute, true)
	}
	require.Zero(t, u.woke(), "at the computer all along")
	for range 5 {
		u.step(time.Hour, false)
	}
	require.Zero(t, u.woke(), "away all along: nobody came back")
	u.step(time.Minute, true) // back
	require.Equal(t, 1, u.woke())
	u.step(time.Minute, true) // still there
	require.Equal(t, 1, u.woke(), "once per coming back")
	u.step(time.Minute, false)
	u.step(31*time.Minute, true)
	require.Equal(t, 2, u.woke(), "a second absence and return")
}

func TestUserBack_AwayMinIsTheSettingsAtTheMomentOfTheReturn(t *testing.T) {
	u := newUBRig(t)
	u.step(0, true)
	u.step(time.Minute, false)
	u.mu.Lock()
	u.awayMin = 60
	u.mu.Unlock()
	u.step(45*time.Minute, true)
	require.Zero(t, u.woke(), "45 minutes is under the 60 now asked")
}

// "Not known" is no reading: nobody is reporting, which is not the same as the person being away.
func TestUserBack_NotKnownIsNeitherAwayNorBackAndStartsTheBaselineOver(t *testing.T) {
	u := newUBRig(t)
	u.step(0, true)
	u.step(time.Minute, false) // away
	u.mu.Lock()
	u.known = false
	u.mu.Unlock()
	u.now = u.now.Add(2 * time.Hour)
	u.m.userBackStep(context.Background()) // nobody reports for two hours
	require.Zero(t, u.woke())
	// reports come back: the first of them is a baseline again, so a person who is at the computer then did not "come back"
	u.step(time.Minute, true)
	require.Zero(t, u.woke(), "the two hours nobody reported are not counted as away")
	// and one that is away then is counted from that reading
	u.mu.Lock()
	u.known = false
	u.mu.Unlock()
	u.m.userBackStep(context.Background())
	u.step(time.Minute, false)
	u.step(10*time.Minute, true)
	require.Zero(t, u.woke(), "10 minutes away from the baseline is under away_min")
}

func TestUserBack_OnlyTheFleetPrimaryWatchesAndAChangeOfRoleStartsOver(t *testing.T) {
	u := newUBRig(t)
	u.mu.Lock()
	u.primary = false
	u.mu.Unlock()
	u.step(0, true)
	u.step(time.Minute, false)
	u.step(2*time.Hour, true)
	require.Zero(t, u.woke(), "a standby does not wake")

	// primary again: the first reading is a baseline, whatever the standby saw
	u.mu.Lock()
	u.primary = true
	u.mu.Unlock()
	u.step(time.Minute, true)
	require.Zero(t, u.woke())

	// away as the primary for an hour, then the role goes to the other host and comes back
	u.step(time.Minute, false)
	u.now = u.now.Add(time.Hour)
	u.mu.Lock()
	u.primary = false
	u.mu.Unlock()
	u.m.userBackStep(context.Background())
	u.mu.Lock()
	u.primary = true
	u.mu.Unlock()
	u.step(time.Minute, false) // baseline again: away from now
	u.step(5*time.Minute, true)
	require.Zero(t, u.woke(), "the hour before the role changed is not counted")
}

// A wake that fails is not sent again.
func TestUserBack_AFailedWakeIsNotRetried(t *testing.T) {
	u := newUBRig(t)
	u.step(0, true)
	u.step(time.Minute, false)
	u.mu.Lock()
	u.failing = true
	u.mu.Unlock()
	u.step(time.Hour, true)
	require.Equal(t, 1, u.woke())
	for range 5 {
		u.step(time.Minute, true)
	}
	require.Equal(t, 1, u.woke(), "the person is at the computer: nothing to retry")
}

// The values the real module reads: the push module's presence value, the fleet role view, both through the registry.
type presenceFake struct{ at, known bool }

func (p presenceFake) AtComputer() bool            { return p.at }
func (p presenceFake) PresenceState() (bool, bool) { return p.at, p.known }

type atComputerOnly bool

func (a atComputerOnly) AtComputer() bool { return bool(a) }

func TestUserBack_ReadsThePresenceStateAndTheFleetRoleFromTheRegistry(t *testing.T) {
	r := newRecRig(t)
	// nothing registered: not known, and not the primary
	at, known := r.m.presenceState()
	require.False(t, at || known)
	require.False(t, r.m.isFleetPrimary())

	// a value that is only an AtComputerReader says nothing about "not known"
	r.m.core.Registry.Register(push.AtComputerKey, push.AtComputerReader(atComputerOnly(true)))
	at, known = r.m.presenceState()
	require.False(t, at || known, "an AtComputerReader alone is not a reading")

	r.m.core.Registry.Register(push.AtComputerKey, push.AtComputerReader(presenceFake{at: true, known: true}))
	at, known = r.m.presenceState()
	require.True(t, at && known)

	role := ifleet.NewFakeRole(ifleet.RoleState{Role: ifleet.RoleStandby, Self: "mlab", Primary: "air26"})
	r.m.core.Registry.Register(fleetmod.RoleViewKey, ifleet.RoleView(role))
	require.False(t, r.m.isFleetPrimary(), "a standby")
	role.Set(ifleet.RoleState{Role: ifleet.RolePrimary, Term: 2, Self: "mlab"})
	require.True(t, r.m.isFleetPrimary())
	role.Set(ifleet.RoleState{Role: ifleet.RoleOff, Self: "mlab", Reason: "disabled"})
	require.False(t, r.m.isFleetPrimary(), "fleet off")
}

// The loop takes a reading every tick, ends with Stop, and a reading that is a return wakes through the module's own WakeWatcher seam.
func TestUserBack_TheLoopRunsEveryTickAndStopsWithTheModule(t *testing.T) {
	u := newUBRig(t)
	u.m.userBackEvery = 5 * time.Millisecond
	u.m.now = func() time.Time { u.mu.Lock(); defer u.mu.Unlock(); return u.now } // the loop reads the clock while the test moves it
	ctx, cancel := context.WithCancel(context.Background())
	u.m.wg.Add(1)
	go func() {
		defer u.m.wg.Done()
		u.m.userBackLoop(ctx)
	}()
	// away for long enough, as the clock the module reads says, then back
	u.mu.Lock()
	u.at = false
	u.mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	u.mu.Lock()
	u.now = u.now.Add(time.Hour)
	u.at = true
	u.mu.Unlock()
	require.Eventually(t, func() bool { return u.woke() >= 1 }, 3*time.Second, 5*time.Millisecond)
	cancel()
	done := make(chan struct{})
	go func() { u.m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the loop did not end with its context")
	}
}

// the role went primary -> standby -> primary between two ticks: no tick saw the standby, but the term moved, so the baseline starts over
func TestUserBack_ANewTermStartsOverEvenIfNoTickSawTheStandby(t *testing.T) {
	u := newUBRig(t)
	u.step(0, true)
	u.step(time.Minute, false)
	u.now = u.now.Add(time.Hour)
	u.mu.Lock()
	u.term++
	u.mu.Unlock()
	u.step(0, false) // baseline again: away from now
	u.step(5*time.Minute, true)
	require.Zero(t, u.woke(), "the hour before the term changed is not counted")
}
