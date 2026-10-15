package watch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeHosts is a set of hosts that answer the locator's three questions.
type fakeHosts struct {
	fmu    sync.Mutex              // the locator asks the targets side by side
	hold   map[string]HolderAnswer // by alias; a missing alias is unreachable
	oldPer map[string]bool         // hosts that are an older daemon
	asked  []string
	woken  []string
	start  []string
	// startOK / wakeOK: whether those succeed on that host
	startFail, wakeFail map[string]bool
}

func (f *fakeHosts) Holder(_ context.Context, a string) (HolderAnswer, error) {
	f.fmu.Lock()
	defer f.fmu.Unlock()
	f.asked = append(f.asked, a)
	if f.oldPer[a] {
		return HolderAnswer{}, ErrNoWatchSupport
	}
	h, ok := f.hold[a]
	if !ok {
		return HolderAnswer{}, errors.New("unreachable")
	}
	return h, nil
}

func (f *fakeHosts) Wake(_ context.Context, a string) (HolderAnswer, error) {
	f.fmu.Lock()
	defer f.fmu.Unlock()
	f.woken = append(f.woken, a)
	if f.wakeFail[a] {
		return HolderAnswer{}, ErrNoSession
	}
	h := f.hold[a]
	h.State = HolderAwake
	f.hold[a] = h
	return h, nil
}

func (f *fakeHosts) Start(_ context.Context, a string) (HolderAnswer, error) {
	f.fmu.Lock()
	defer f.fmu.Unlock()
	f.start = append(f.start, a)
	if f.startFail[a] {
		return HolderAnswer{}, ErrNoSession
	}
	h := f.hold[a]
	h.State, h.SessionID = HolderAwake, "new-on-"+a
	f.hold[a] = h
	return h, nil
}

func hostAns(id string, st HolderState, sess string, targets ...string) HolderAnswer {
	return HolderAnswer{HostID: id, State: st, SessionID: sess, Targets: targets}
}

var targets = []string{"air26", "mlab"}

func newLoc(t *testing.T, f *fakeHosts) (*Locator, *[]string) {
	var warns []string
	l := NewLocator(filepath.Join(t.TempDir(), "remembered.json"), f, func(format string, a ...any) { warns = append(warns, fmt.Sprintf(format, a...)) })
	return l, &warns
}

func TestLocate_AWatcherAwakeElsewhereIsUsed(t *testing.T) {
	f := &fakeHosts{hold: map[string]HolderAnswer{
		"air26": hostAns("a:1", HolderNone, "", "air26", "mlab"),
		"mlab":  hostAns("m:1", HolderAwake, "s-mlab", "air26", "mlab"),
	}}
	l, _ := newLoc(t, f)
	w, err := l.Current(context.Background(), targets, true)
	require.NoError(t, err)
	require.Equal(t, "mlab", w.Alias)
	require.Equal(t, "s-mlab", w.SessionID)
	require.Empty(t, f.woken)
	require.Empty(t, f.start, "nothing is started when one is awake")
}

func TestLocate_TheFirstAwakeInTheOrderOfTargetsWins(t *testing.T) {
	f := &fakeHosts{hold: map[string]HolderAnswer{
		"air26": hostAns("a:1", HolderAwake, "s-a", "air26", "mlab"),
		"mlab":  hostAns("m:1", HolderAwake, "s-m", "air26", "mlab"),
	}}
	l, _ := newLoc(t, f)
	w, _ := l.Current(context.Background(), targets, true)
	require.Equal(t, "air26", w.Alias)
}

func TestLocate_ARestingWatcherIsWokenOnItsHost(t *testing.T) {
	f := &fakeHosts{hold: map[string]HolderAnswer{
		"air26": hostAns("a:1", HolderNone, "", "air26", "mlab"),
		"mlab":  hostAns("m:1", HolderResting, "s-rest", "air26", "mlab"),
	}}
	l, _ := newLoc(t, f)
	l.mem.Last = &Where{Alias: "mlab", HostID: "m:1", SessionID: "s-rest", State: HolderResting}
	w, err := l.Current(context.Background(), targets, true)
	require.NoError(t, err)
	require.Equal(t, []string{"mlab"}, f.woken)
	require.Empty(t, f.start, "woken, not started (the same session resumes)")
	require.Equal(t, "s-rest", w.SessionID)
	require.Equal(t, "mlab", w.Alias)
}

func TestLocate_NoneStartsOnTheFirstReachableTarget(t *testing.T) {
	f := &fakeHosts{hold: map[string]HolderAnswer{
		"air26": hostAns("a:1", HolderNone, "", "air26", "mlab"),
		"mlab":  hostAns("m:1", HolderNone, "", "air26", "mlab"),
	}}
	l, _ := newLoc(t, f)
	w, err := l.Current(context.Background(), targets, true)
	require.NoError(t, err)
	require.Equal(t, []string{"air26"}, f.start, "air26 first")
	require.Equal(t, "new-on-air26", w.SessionID)
}

func TestLocate_TheFirstTargetDownMeansTheSecond(t *testing.T) {
	f := &fakeHosts{hold: map[string]HolderAnswer{"mlab": hostAns("m:1", HolderNone, "", "air26", "mlab")}} // air26 does not answer
	l, _ := newLoc(t, f)
	w, err := l.Current(context.Background(), targets, true)
	require.NoError(t, err)
	require.Equal(t, []string{"mlab"}, f.start)
	require.Equal(t, "mlab", w.Alias)
}

func TestLocate_AFailedStartTriesTheNextTarget(t *testing.T) {
	f := &fakeHosts{
		hold: map[string]HolderAnswer{
			"air26": hostAns("a:1", HolderNone, "", "air26", "mlab"),
			"mlab":  hostAns("m:1", HolderNone, "", "air26", "mlab"),
		},
		startFail: map[string]bool{"air26": true},
	}
	l, _ := newLoc(t, f)
	w, err := l.Current(context.Background(), targets, true)
	require.NoError(t, err)
	require.Equal(t, []string{"air26", "mlab"}, f.start)
	require.Equal(t, "mlab", w.Alias)
}

func TestLocate_AnOldDaemonIsNeitherAWatcherNorAPlaceToStartOne(t *testing.T) {
	f := &fakeHosts{
		hold:   map[string]HolderAnswer{"mlab": hostAns("m:1", HolderNone, "", "air26", "mlab")},
		oldPer: map[string]bool{"air26": true},
	}
	l, warns := newLoc(t, f)
	w, err := l.Current(context.Background(), targets, true)
	require.NoError(t, err)
	require.Equal(t, "mlab", w.Alias)
	require.Equal(t, []string{"mlab"}, f.start)
	require.Empty(t, *warns)
}

func TestLocate_WithoutAutoWakeNothingIsWokenOrStarted(t *testing.T) {
	f := &fakeHosts{hold: map[string]HolderAnswer{
		"air26": hostAns("a:1", HolderResting, "s", "air26", "mlab"),
		"mlab":  hostAns("m:1", HolderNone, "", "air26", "mlab"),
	}}
	l, _ := newLoc(t, f)
	l.mem.Last = &Where{Alias: "air26", SessionID: "s"}
	_, err := l.Current(context.Background(), targets, false)
	require.ErrorIs(t, err, ErrNoWatcher)
	require.Empty(t, f.woken)
	require.Empty(t, f.start)
	// but an awake one is still found
	f.hold["mlab"] = hostAns("m:1", HolderAwake, "s-m", "air26", "mlab")
	w, err := l.Current(context.Background(), targets, false)
	require.NoError(t, err)
	require.Equal(t, "mlab", w.Alias)
}

func TestLocate_NobodyReachableIsNoWatcher(t *testing.T) {
	l, _ := newLoc(t, &fakeHosts{hold: map[string]HolderAnswer{}})
	_, err := l.Current(context.Background(), targets, true)
	require.ErrorIs(t, err, ErrNoWatcher)
}

func TestLocate_TheCacheIsNotAskedAgainWhileDeliveriesSucceed(t *testing.T) {
	f := &fakeHosts{hold: map[string]HolderAnswer{
		"air26": hostAns("a:1", HolderAwake, "s-a", "air26", "mlab"),
		"mlab":  hostAns("m:1", HolderNone, "", "air26", "mlab"),
	}}
	l, _ := newLoc(t, f)
	var w Where
	for i := 0; i < 5; i++ {
		var err error
		w, err = l.Current(context.Background(), targets, true)
		require.NoError(t, err)
		require.Equal(t, "air26", w.Alias)
	}
	require.Len(t, f.asked, 2, "one search of two hosts, then the cache")

	// a failed delivery: look again; the watcher has moved to mlab (no switching back is decided here, only what is awake)
	l.Failed(w)
	f.hold["air26"] = hostAns("a:1", HolderNone, "", "air26", "mlab")
	f.hold["mlab"] = hostAns("m:1", HolderAwake, "s-m", "air26", "mlab")
	w, _ = l.Current(context.Background(), targets, true)
	require.Equal(t, "mlab", w.Alias)
	require.Len(t, f.asked, 4)
}

func TestLocate_AFailedDeliveryKeepsTheLastWatcherForAWake(t *testing.T) {
	f := &fakeHosts{hold: map[string]HolderAnswer{
		"air26": hostAns("a:1", HolderAwake, "s-a", "air26", "mlab"),
		"mlab":  hostAns("m:1", HolderNone, "", "air26", "mlab"),
	}}
	l, _ := newLoc(t, f)
	w0, _ := l.Current(context.Background(), targets, true)
	l.Failed(w0)
	cur, last := l.Remembered()
	require.Nil(t, cur)
	require.NotNil(t, last)
	require.Equal(t, "air26", last.Alias)
	// air26 rested meanwhile: step 2 wakes it, nothing is started on mlab
	f.hold["air26"] = hostAns("a:1", HolderResting, "s-a", "air26", "mlab")
	w, err := l.Current(context.Background(), targets, true)
	require.NoError(t, err)
	require.Equal(t, []string{"air26"}, f.woken)
	require.Empty(t, f.start)
	require.Equal(t, "s-a", w.SessionID)
}

func TestLocate_TheRememberedWatcherSurvivesARestart(t *testing.T) {
	f := &fakeHosts{hold: map[string]HolderAnswer{"air26": hostAns("a:1", HolderAwake, "s-a", "air26", "mlab")}}
	path := filepath.Join(t.TempDir(), "remembered.json")
	l := NewLocator(path, f, nil)
	_, _ = l.Current(context.Background(), targets, true)
	n := len(f.asked)
	l2 := NewLocator(path, f, nil)
	w, err := l2.Current(context.Background(), targets, true)
	require.NoError(t, err)
	require.Equal(t, "s-a", w.SessionID)
	require.Len(t, f.asked, n, "a restart does not search again")
}

func TestLocate_MismatchedTargetsWarnOncePerChange(t *testing.T) {
	f := &fakeHosts{hold: map[string]HolderAnswer{
		"air26": hostAns("a:1", HolderNone, "", "mlab", "air26"), // the other order
		"mlab":  hostAns("m:1", HolderNone, "", "air26", "mlab"), // the same
	}}
	l, warns := newLoc(t, f)
	for i := 0; i < 3; i++ {
		l.mem = remembered{}
		_, _ = l.Current(context.Background(), targets, false)
	}
	require.Len(t, *warns, 1, "the same difference is not repeated: %v", *warns)
	require.True(t, strings.Contains((*warns)[0], "air26") && strings.Contains((*warns)[0], "differ"))
	// it changes again → warns again; then it agrees → quiet, and a later difference warns anew
	f.hold["air26"] = hostAns("a:1", HolderNone, "", "air26")
	_, _ = l.Current(context.Background(), targets, false)
	require.Len(t, *warns, 2)
	f.hold["air26"] = hostAns("a:1", HolderNone, "", "air26", "mlab")
	_, _ = l.Current(context.Background(), targets, false)
	require.Len(t, *warns, 2)
	f.hold["air26"] = hostAns("a:1", HolderNone, "", "mlab", "air26")
	_, _ = l.Current(context.Background(), targets, false)
	require.Len(t, *warns, 3)
	// a host that does not say its targets is not compared
	f.hold["air26"] = hostAns("a:1", HolderNone, "")
	_, _ = l.Current(context.Background(), targets, false)
	require.Len(t, *warns, 3)
}

func TestLocate_ACorruptRememberedFileIsSetAside(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remembered.json")
	require.NoError(t, writeFileForTest(path, "{nope"))
	l := NewLocator(path, &fakeHosts{hold: map[string]HolderAnswer{}}, nil)
	cur, last := l.Remembered()
	require.Nil(t, cur)
	require.Nil(t, last)
	require.FileExists(t, path+".corrupt")
}

// One search at a time: callers that all miss the cache together start one watcher, and the others get it.
func TestLocate_ConcurrentCallersStartOneWatcher(t *testing.T) {
	f := &lockedHosts{fakeHosts: fakeHosts{hold: map[string]HolderAnswer{
		"air26": hostAns("a:1", HolderNone, "", "air26", "mlab"),
		"mlab":  hostAns("m:1", HolderNone, "", "air26", "mlab"),
	}}}
	l := NewLocator("", f, nil)
	var wg sync.WaitGroup
	got := make([]Where, 8)
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w, err := l.Current(context.Background(), targets, true)
			require.NoError(t, err)
			got[i] = w
		}(i)
	}
	wg.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Equal(t, []string{"air26"}, f.start, "one start, not one per caller")
	for _, w := range got {
		require.Equal(t, "new-on-air26", w.SessionID)
	}
}

// lockedHosts is fakeHosts safe for concurrent use, and slow enough that callers overlap.
type lockedHosts struct {
	mu sync.Mutex
	fakeHosts
}

func (l *lockedHosts) Holder(ctx context.Context, a string) (HolderAnswer, error) {
	time.Sleep(10 * time.Millisecond)
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fakeHosts.Holder(ctx, a)
}
func (l *lockedHosts) Start(ctx context.Context, a string) (HolderAnswer, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fakeHosts.Start(ctx, a)
}
func (l *lockedHosts) Wake(ctx context.Context, a string) (HolderAnswer, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fakeHosts.Wake(ctx, a)
}

// ---- the second round (codex attack) ----

func TestLocate_ASlowTargetCostsOneBudgetNotMoreAndTheOthersAreStillUsed(t *testing.T) {
	old := probeBudget
	probeBudget = 60 * time.Millisecond
	defer func() { probeBudget = old }()
	f := &slowHosts{fakeHosts: fakeHosts{hold: map[string]HolderAnswer{
		"air26": hostAns("a:1", HolderAwake, "s-a", "air26", "mlab"),
		"mlab":  hostAns("m:1", HolderAwake, "s-m", "air26", "mlab"),
	}}, slow: map[string]bool{"air26": true}}
	l := NewLocator("", f, nil)
	begin := time.Now()
	w, err := l.Current(context.Background(), targets, true)
	require.NoError(t, err)
	require.Less(t, time.Since(begin), 2*time.Second)
	require.Equal(t, "mlab", w.Alias, "air26 did not answer in its budget")
}

// slowHosts: a slow target does not answer before its context ends.
type slowHosts struct {
	fakeHosts
	slow map[string]bool
	mu   sync.Mutex
}

func (s *slowHosts) Holder(ctx context.Context, a string) (HolderAnswer, error) {
	if s.slow[a] {
		<-ctx.Done()
		return HolderAnswer{}, ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fakeHosts.Holder(ctx, a)
}

func TestLocate_AWaitingCallerGivesUpWhenItsContextEnds(t *testing.T) {
	block := make(chan struct{})
	f := &blockingHosts{fakeHosts: fakeHosts{hold: map[string]HolderAnswer{"air26": hostAns("a:1", HolderNone, "", "air26", "mlab")}}, block: block}
	l := NewLocator("", f, nil)
	done := make(chan struct{})
	go func() { _, _ = l.Current(context.Background(), targets, true); close(done) }()
	time.Sleep(30 * time.Millisecond) // the first caller is inside its search
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	begin := time.Now()
	_, err := l.Current(ctx, targets, true)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(begin), time.Second)
	close(block)
	<-done
}

type blockingHosts struct {
	fakeHosts
	block chan struct{}
}

func (b *blockingHosts) Holder(ctx context.Context, a string) (HolderAnswer, error) {
	<-b.block
	return b.fakeHosts.Holder(ctx, a)
}

// A wake whose answer was lost may have worked: the host is asked again, and nothing is started elsewhere on a guess.
func TestLocate_AWakeWithALostAnswerIsCheckedNotRepeatedNorReplaced(t *testing.T) {
	f := &lostHosts{fakeHosts: fakeHosts{hold: map[string]HolderAnswer{
		"air26": hostAns("a:1", HolderResting, "s-a", "air26", "mlab"),
		"mlab":  hostAns("m:1", HolderNone, "", "air26", "mlab"),
	}}, lose: map[string]bool{"air26": true}}
	l := NewLocator("", f, nil)
	l.mem.Last = &Where{Alias: "air26", SessionID: "s-a", State: HolderResting}
	w, err := l.Current(context.Background(), targets, true)
	require.NoError(t, err)
	require.Equal(t, "air26", w.Alias, "the host came up although its answer was lost")
	require.Equal(t, []string{"air26"}, f.woken, "woken once")
	require.Empty(t, f.start)
}

func TestLocate_WhenTheOutcomeCannotBeFoundOutNothingIsStartedElsewhere(t *testing.T) {
	f := &lostHosts{fakeHosts: fakeHosts{hold: map[string]HolderAnswer{
		"air26": hostAns("a:1", HolderResting, "s-a", "air26", "mlab"),
		"mlab":  hostAns("m:1", HolderNone, "", "air26", "mlab"),
	}}, lose: map[string]bool{"air26": true}, gone: map[string]bool{"air26": true}}
	l := NewLocator("", f, nil)
	l.mem.Last = &Where{Alias: "air26", SessionID: "s-a", State: HolderResting}
	_, err := l.Current(context.Background(), targets, true)
	require.ErrorIs(t, err, ErrNoWatcher)
	require.Empty(t, f.start, "air26 may be awake: no second watcher on mlab")
	require.Equal(t, []string{"air26"}, f.woken, "and no second wake")
}

func TestLocate_AStartWithALostAnswerIsCheckedToo(t *testing.T) {
	f := &lostHosts{fakeHosts: fakeHosts{hold: map[string]HolderAnswer{
		"air26": hostAns("a:1", HolderNone, "", "air26", "mlab"),
		"mlab":  hostAns("m:1", HolderNone, "", "air26", "mlab"),
	}}, lose: map[string]bool{"air26": true}}
	l := NewLocator("", f, nil)
	w, err := l.Current(context.Background(), targets, true)
	require.NoError(t, err)
	require.Equal(t, "air26", w.Alias)
	require.Equal(t, []string{"air26"}, f.start, "started once; mlab is not asked")
}

// lostHosts: wake and start work on the host but the answer does not come back (lose); gone: and the host cannot be asked again.
type lostHosts struct {
	fakeHosts
	lose, gone map[string]bool
	late       bool // set once an act was made: Holder then fails for the gone hosts
}

func (h *lostHosts) Holder(ctx context.Context, a string) (HolderAnswer, error) {
	if h.late && h.gone[a] {
		return HolderAnswer{}, errors.New("unreachable")
	}
	return h.fakeHosts.Holder(ctx, a)
}
func (h *lostHosts) Wake(ctx context.Context, a string) (HolderAnswer, error) {
	res, err := h.fakeHosts.Wake(ctx, a)
	h.late = true
	if h.lose[a] {
		return HolderAnswer{}, errors.New("timeout")
	}
	return res, err
}
func (h *lostHosts) Start(ctx context.Context, a string) (HolderAnswer, error) {
	res, err := h.fakeHosts.Start(ctx, a)
	h.late = true
	if h.lose[a] {
		return HolderAnswer{}, errors.New("timeout")
	}
	return res, err
}

// A late failure of an earlier delivery does not clear what a search found since.
func TestLocate_FailedNamesTheWatcherItIsAbout(t *testing.T) {
	f := &fakeHosts{hold: map[string]HolderAnswer{"air26": hostAns("a:1", HolderAwake, "s-new", "air26", "mlab")}}
	l, _ := newLoc(t, f)
	w, _ := l.Current(context.Background(), targets, true)
	l.Failed(Where{Alias: "air26", HostID: "a:1", SessionID: "s-old"})
	cur, _ := l.Remembered()
	require.NotNil(t, cur, "a failure of an older watcher is not about this one")
	l.Failed(w)
	cur, _ = l.Remembered()
	require.Nil(t, cur)
}

func TestLocate_ATamperedRememberedFileIsNotTrusted(t *testing.T) {
	for name, content := range map[string]string{
		"no session":     `{"current":{"alias":"air26","host_id":"a:1","state":"awake"}}`,
		"no alias":       `{"last":{"host_id":"a:1","session_id":"s","state":"resting"}}`,
		"a odd state":    `{"current":{"alias":"air26","host_id":"a:1","session_id":"s","state":"none"}}`,
		"an unknown one": `{"current":{"alias":"air26","session_id":"s","state":"dozing"}}`,
	} {
		path := filepath.Join(t.TempDir(), "remembered.json")
		require.NoError(t, writeFileForTest(path, content), name)
		l := NewLocator(path, &fakeHosts{}, nil)
		cur, last := l.Remembered()
		require.Nil(t, cur, name)
		require.Nil(t, last, name)
		require.FileExists(t, path+".corrupt", name)
	}
	path := filepath.Join(t.TempDir(), "remembered.json")
	require.NoError(t, writeFileForTest(path, `{"current":{"alias":"air26","host_id":"a:1","session_id":"s","state":"awake"}}`))
	cur, _ := NewLocator(path, &fakeHosts{}, nil).Remembered()
	require.NotNil(t, cur)
}

// A wake that was refused at step 2 is not asked again at step 3, and the watcher is started on the next target.
func TestLocate_AHostThatRefusedTheWakeIsNotWokenAgainInTheStartStep(t *testing.T) {
	f := &fakeHosts{
		hold: map[string]HolderAnswer{
			"air26": hostAns("a:1", HolderResting, "s-a", "air26", "mlab"),
			"mlab":  hostAns("m:1", HolderNone, "", "air26", "mlab"),
		},
		wakeFail: map[string]bool{"air26": true},
	}
	l := NewLocator("", f, nil)
	l.mem.Last = &Where{Alias: "air26", SessionID: "s-a", State: HolderResting}
	w, err := l.Current(context.Background(), targets, true)
	require.NoError(t, err)
	require.Equal(t, []string{"air26"}, f.woken, "woken once, at step 2")
	require.Equal(t, []string{"mlab"}, f.start)
	require.Equal(t, "mlab", w.Alias)
}

// An outcome that could not be found out blocks a start elsewhere across searches (a partition outlasts one Current), until the
// host answers or the time passes.
func TestLocate_AnUncertainHostBlocksAStartElsewhereUntilItAnswersOrTheTimePasses(t *testing.T) {
	mk := func() (*lostHosts, *Locator) {
		f := &lostHosts{fakeHosts: fakeHosts{hold: map[string]HolderAnswer{
			"air26": hostAns("a:1", HolderNone, "", "air26", "mlab"),
			"mlab":  hostAns("m:1", HolderNone, "", "air26", "mlab"),
		}}, lose: map[string]bool{"air26": true}, gone: map[string]bool{"air26": true}}
		return f, NewLocator(filepath.Join(t.TempDir(), "remembered.json"), f, nil)
	}
	ctx := context.Background()

	f, l := mk()
	_, err := l.Current(ctx, targets, true) // air26: Start lost its answer and the question after it got none
	require.ErrorIs(t, err, ErrNoWatcher)
	require.Equal(t, []string{"air26"}, f.start)
	// the next searches: air26 still does not answer → mlab is not started on a guess
	for i := 0; i < 3; i++ {
		_, err = l.Current(ctx, targets, true)
		require.ErrorIs(t, err, ErrNoWatcher)
	}
	require.Equal(t, []string{"air26"}, f.start, "no start on mlab while air26 is unaccounted for")

	// the block is kept across a restart
	l2 := NewLocator(l.path, f, nil)
	_, err = l2.Current(ctx, targets, true)
	require.ErrorIs(t, err, ErrNoWatcher)
	require.Equal(t, []string{"air26"}, f.start)

	// air26 comes back and holds the watcher the start made: it is found, the block is gone
	f.late, f.gone["air26"] = false, false
	f.hold["air26"] = hostAns("a:1", HolderAwake, "new-on-air26", "air26", "mlab")
	w, err := l.Current(ctx, targets, true)
	require.NoError(t, err)
	require.Equal(t, "air26", w.Alias)
	_, _ = l.Remembered()
	require.Nil(t, l.mem.Uncertain)

	// air26 never comes back: after the time the block ends and mlab starts one
	f, l = mk()
	_, _ = l.Current(ctx, targets, true)
	now := time.Now()
	l.now = func() time.Time { return now.Add(uncertainTTL + time.Minute) }
	w, err = l.Current(ctx, targets, true)
	require.NoError(t, err)
	require.Equal(t, "mlab", w.Alias)
	require.Equal(t, []string{"air26", "mlab"}, f.start)
}

func TestLocate_AnUncertainHostThatAnswersNoneEndsTheBlockAndTheNormalStepsGoOn(t *testing.T) {
	f := &lostHosts{fakeHosts: fakeHosts{hold: map[string]HolderAnswer{
		"air26": hostAns("a:1", HolderNone, "", "air26", "mlab"),
		"mlab":  hostAns("m:1", HolderNone, "", "air26", "mlab"),
	}}, lose: map[string]bool{"air26": true}, gone: map[string]bool{"air26": true}}
	l := NewLocator("", f, nil)
	_, _ = l.Current(context.Background(), targets, true)
	f.late, f.gone["air26"] = false, false // reachable again, and it holds none: the start never happened
	f.lose["air26"] = false
	w, err := l.Current(context.Background(), targets, true)
	require.NoError(t, err)
	require.Equal(t, "air26", w.Alias, "started on the first target again, now with an answer")
}
