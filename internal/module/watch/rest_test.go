package watchmod

import (
	"context"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/watch"
	"github.com/wake/purdex/internal/watchsettings"
)

type fakeExit struct {
	mu sync.Mutex
	n  int
}

func (f *fakeExit) SendExit(context.Context) error { f.mu.Lock(); f.n++; f.mu.Unlock(); return nil }
func (f *fakeExit) count() int                     { f.mu.Lock(); defer f.mu.Unlock(); return f.n }

type sigRec struct {
	pid int
	sig syscall.Signal
}

// restRig is a module with an awake watcher (frame f1, registered at r.now) whose clock, transcript time and process table the test sets.
type restRig struct {
	*recRig
	note  *fakeNotifier
	exit  *fakeExit
	mtime time.Time
	start string
	sigs  []sigRec
	mode  string
	idle  int
}

func newRestRig(t *testing.T) *restRig {
	t.Helper()
	rr := &restRig{recRig: newRecRig(t), note: &fakeNotifier{}, exit: &fakeExit{}, start: "Fri Oct 16 12:00:00 2026", mode: watchsettings.ModePatrol, idle: 120}
	m := rr.m
	m.dlv.notifier, m.exit = rr.note, rr.exit
	m.mtimeOf = func(string) time.Time { return rr.mtime }
	m.procStart = func(int) (string, error) { return rr.start, nil }
	m.signalFn = func(pid int, sig syscall.Signal) error { rr.sigs = append(rr.sigs, sigRec{pid, sig}); return nil }
	m.settings = func() (watchsettings.Settings, error) {
		s := watchsettings.Default()
		s.Mode, s.PatrolIdleMin = rr.mode, rr.idle
		return s, nil
	}
	rr.owners.set(watcherOwner("f1"), true, nil)
	rr.step() // registers the frame at r.now
	return rr
}

func (r *restRig) step() { r.m.restStep(context.Background()) }

func (r *restRig) advance(d time.Duration) { r.now = r.now.Add(d); r.step() }

func (r *restRig) texts() []string { return r.note.texts }

func TestRest_AnIdleWatcherIsToldToRestOnceAndOnlyAfterTheIdleTime(t *testing.T) {
	r := newRestRig(t)
	r.advance(119 * time.Minute)
	require.Empty(t, r.texts())
	r.advance(2 * time.Minute)
	require.Equal(t, []string{restText}, r.texts())
	r.advance(time.Minute)
	require.Len(t, r.texts(), 1, "told once")
	require.Zero(t, r.exit.count(), "not exited before an answer or the wait")
}

func TestRest_ActivityPostponesIt(t *testing.T) {
	r := newRestRig(t)
	r.advance(110 * time.Minute)
	r.mtime = r.now // a turn wrote the transcript
	r.advance(15 * time.Minute)
	require.Empty(t, r.texts(), "125 minutes since the start, 15 since the last turn")
	require.NoError(t, r.m.holder.Update(func(h *watch.Holder) { h.LastDelivery = r.now }))
	r.advance(110 * time.Minute)
	require.Empty(t, r.texts(), "110 minutes since the last delivery, 125 since the last turn")
}

func TestRest_ExitGoesAfterTheAnswerOrAfterFiveMinutes(t *testing.T) {
	t.Run("answer", func(t *testing.T) {
		r := newRestRig(t)
		r.advance(121 * time.Minute)
		require.Len(t, r.texts(), 1)
		r.mtime = r.now.Add(20 * time.Second) // it is answering
		r.advance(60 * time.Second)
		require.Zero(t, r.exit.count(), "the turn is still going on")
		r.advance(60 * time.Second)
		require.Equal(t, 1, r.exit.count(), "the transcript has been still for 90 s")
	})
	t.Run("no answer", func(t *testing.T) {
		r := newRestRig(t)
		r.advance(121 * time.Minute)
		r.advance(4 * time.Minute)
		require.Zero(t, r.exit.count())
		r.advance(time.Minute + time.Second)
		require.Equal(t, 1, r.exit.count())
	})
}

func TestRest_AnEventAfterTheNoticeCancelsTheRest(t *testing.T) {
	r := newRestRig(t)
	r.advance(121 * time.Minute)
	require.NoError(t, r.m.holder.Update(func(h *watch.Holder) { h.LastDelivery = r.now.Add(time.Second) }))
	r.advance(6 * time.Minute)
	require.Zero(t, r.exit.count())
}

func TestRest_AFrameThatStaysAfterExitGetsSIGTERMThenSIGKILLOnlyWhileItIsTheSameProcess(t *testing.T) {
	r := newRestRig(t)
	r.advance(121 * time.Minute)
	r.advance(5*time.Minute + time.Second) // exit sent
	require.Equal(t, 1, r.exit.count())
	r.advance(4 * time.Minute)
	require.Empty(t, r.sigs, "not before 5 minutes after /exit")
	r.advance(time.Minute + time.Second)
	require.Equal(t, []sigRec{{4242, syscall.SIGTERM}}, r.sigs)
	r.advance(5 * time.Second)
	require.Len(t, r.sigs, 1, "SIGKILL waits 10 s")
	r.advance(6 * time.Second)
	require.Equal(t, []sigRec{{4242, syscall.SIGTERM}, {4242, syscall.SIGKILL}}, r.sigs)
}

func TestRest_NoSignalGoesToAPidWhoseStartTimeChanged(t *testing.T) {
	r := newRestRig(t)
	r.advance(121 * time.Minute)
	r.advance(5*time.Minute + time.Second)
	r.start = "Sat Oct 17 01:00:00 2026" // the pid was reused
	r.advance(6 * time.Minute)
	require.Empty(t, r.sigs)
}

func TestRest_TheWatcherThatExitsIsLeftAloneAndAResidentNeverRests(t *testing.T) {
	r := newRestRig(t)
	r.advance(121 * time.Minute)
	r.advance(5*time.Minute + time.Second)
	r.owners.set(watcherOwner("f1"), false, nil) // it exited
	r.advance(10 * time.Minute)
	require.Empty(t, r.sigs)
	require.Equal(t, watch.HolderResting, r.m.holder.Get().State)

	q := newRestRig(t)
	q.mode = watchsettings.ModeResident
	q.advance(24 * time.Hour)
	require.Empty(t, q.texts())
	require.Zero(t, q.exit.count())
}

func TestRest_ANewFrameStartsTheCountAgain(t *testing.T) {
	r := newRestRig(t)
	r.advance(121 * time.Minute)
	require.Len(t, r.texts(), 1)
	r.owners.set(watcherOwner("f2"), true, nil)
	r.advance(time.Minute) // registers f2
	r.advance(10 * time.Minute)
	require.Len(t, r.texts(), 1, "f2 is new: not idle yet")
}

func TestRest_AFrameThatReplacesTheOneAskedIsNotExitedOnTheOldOnesClock(t *testing.T) {
	r := newRestRig(t)
	r.advance(121 * time.Minute)
	require.Len(t, r.texts(), 1)
	r.owners.set(watcherOwner("f2"), true, nil)
	r.advance(6 * time.Minute)
	require.Zero(t, r.exit.count(), "/exit goes to the frame that was asked, not to a new one")
}

func TestRest_AFailedSIGKILLIsTriedAgainAtTheNextStepNotRestartedAsANewRest(t *testing.T) {
	r := newRestRig(t)
	r.advance(121 * time.Minute)
	r.advance(5*time.Minute + time.Second)
	r.advance(5*time.Minute + time.Second) // SIGTERM
	fail := true
	r.m.signalFn = func(pid int, sig syscall.Signal) error {
		r.sigs = append(r.sigs, sigRec{pid, sig})
		if sig == syscall.SIGKILL && fail {
			return syscall.EPERM
		}
		return nil
	}
	r.advance(11 * time.Second) // SIGKILL fails
	fail = false
	r.advance(10 * time.Second)
	require.Equal(t, syscall.SIGKILL, r.sigs[len(r.sigs)-1].sig)
	require.Len(t, r.texts(), 1, "no second notice: the rest went on")
	require.Len(t, r.sigs, 3)
}

func TestRest_TheNoticesOwnWriteToTheTranscriptIsNotAnAnswer(t *testing.T) {
	r := newRestRig(t)
	r.advance(121 * time.Minute)
	r.mtime = r.now.Add(5 * time.Second) // the notice was written to the transcript
	r.advance(3 * time.Minute)
	require.Zero(t, r.exit.count(), "90 s of quiet after the notice's own write is not an answer")
	r.advance(2*time.Minute + time.Second)
	require.Equal(t, 1, r.exit.count(), "the five minutes are the limit")
}

func TestRest_AnEventAfterTheExitStillCancelsTheSignals(t *testing.T) {
	r := newRestRig(t)
	r.advance(121 * time.Minute)
	r.advance(5*time.Minute + time.Second) // exit sent
	require.NoError(t, r.m.holder.Update(func(h *watch.Holder) { h.LastDelivery = r.now.Add(time.Second) }))
	r.advance(6 * time.Minute)
	require.Empty(t, r.sigs, "it is working on an event: no signal")
}
