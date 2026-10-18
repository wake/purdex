package yield

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

func TestObserve_AHostWithoutAnOwnerRuleNeverYields(t *testing.T) {
	y := New(func() time.Time { return time.Unix(100, 0) })
	d, changed := y.Observe(nil) // facts.owner is null off air26
	assert.Equal(t, Decision{MayClaim: true}, d)
	assert.False(t, changed)
}

// The owner becomes active: claiming stops and the jobs that run are to be given back, for as long as it lasts; when the owner is away
// claiming goes on. Reclaim is a level, not an edge: a job claimed in the instant before the reading is still given back at the next one.
func TestObserve_YieldsWhileTheOwnerIsActiveAndResumesWhenAway(t *testing.T) {
	now := time.Unix(1000, 0)
	y := New(func() time.Time { return now })

	d, changed := y.Observe(&wire.OwnerState{Active: false, IdleS: 900})
	assert.Equal(t, Decision{MayClaim: true}, d)
	assert.False(t, changed)
	_, yielding := y.YieldedSince()
	assert.False(t, yielding)

	now = now.Add(time.Minute)
	d, changed = y.Observe(&wire.OwnerState{Active: true, IdleS: 1, Reason: "hid"})
	assert.Equal(t, Decision{MayClaim: false, Reclaim: true, Reason: "hid"}, d)
	assert.True(t, changed, "the first reading that says active")
	since, yielding := y.YieldedSince()
	assert.True(t, yielding)
	assert.Equal(t, now, since)

	now = now.Add(10 * time.Second)
	d, changed = y.Observe(&wire.OwnerState{Active: true, IdleS: 2, Reason: "build"})
	assert.Equal(t, Decision{MayClaim: false, Reclaim: true, Reason: "build"}, d, "still yielding, the reason follows the reading")
	assert.False(t, changed)
	since2, _ := y.YieldedSince()
	assert.Equal(t, since, since2, "since is when the yield began")

	now = now.Add(time.Hour)
	d, changed = y.Observe(&wire.OwnerState{Active: false, IdleS: 400})
	assert.Equal(t, Decision{MayClaim: true}, d)
	assert.True(t, changed, "the first reading that says away")
	_, yielding = y.YieldedSince()
	assert.False(t, yielding)
}

// A reading that goes missing (the facts have no owner for a moment: nil) while yielding is not "the owner left": yielding continues.
func TestObserve_ANilReadingDoesNotEndAYield(t *testing.T) {
	y := New(time.Now)
	y.Observe(&wire.OwnerState{Active: true, Reason: "hid"})
	d, changed := y.Observe(nil)
	assert.False(t, d.MayClaim)
	assert.True(t, d.Reclaim)
	assert.False(t, changed)
}

type fakeJob struct {
	id    string
	stop  func(context.Context) error
	order *[]string
	mu    *sync.Mutex
}

func (j fakeJob) ID() string { return j.id }
func (j fakeJob) Stop(ctx context.Context) error {
	err := error(nil)
	if j.stop != nil {
		err = j.stop(ctx)
	}
	j.mu.Lock()
	*j.order = append(*j.order, "stop:"+j.id)
	j.mu.Unlock()
	return err
}

type fakeReleaser struct {
	order *[]string
	mu    *sync.Mutex
	fail  map[string]error
	// ctxErr, when set, gets the error of the context each Release was called with
	ctxErr *[]error
}

func (r fakeReleaser) Release(ctx context.Context, id string) error {
	r.mu.Lock()
	*r.order = append(*r.order, "release:"+id)
	if r.ctxErr != nil {
		*r.ctxErr = append(*r.ctxErr, ctx.Err())
	}
	r.mu.Unlock()
	return r.fail[id]
}

func rig() (*[]string, *sync.Mutex) { return &[]string{}, &sync.Mutex{} }

// A job is stopped first, then released; every job is released, and the jobs are stopped at the same time (the owner is waiting).
func TestReclaimAll_StopsThenReleasesEveryJobInParallel(t *testing.T) {
	order, mu := rig()
	slow := func(context.Context) error { time.Sleep(150 * time.Millisecond); return nil }
	jobs := []Job{
		fakeJob{id: "a", stop: slow, order: order, mu: mu},
		fakeJob{id: "b", stop: slow, order: order, mu: mu},
		fakeJob{id: "c", stop: slow, order: order, mu: mu},
	}
	start := time.Now()
	require.NoError(t, ReclaimAll(context.Background(), jobs, fakeReleaser{order: order, mu: mu}))
	assert.Less(t, time.Since(start), 400*time.Millisecond, "three 150 ms stops took one, not three")
	for _, id := range []string{"a", "b", "c"} {
		stop, rel := indexOf(*order, "stop:"+id), indexOf(*order, "release:"+id)
		assert.True(t, stop >= 0 && rel > stop, "%s: stop %d before release %d", id, stop, rel)
	}
}

// A job that cannot be stopped is still given back (the lease would run out otherwise, and the owner is waiting); the errors are all
// returned, and one job's failure does not keep the others from being given back.
func TestReclaimAll_AFailureOfOneDoesNotStopTheOthers(t *testing.T) {
	order, mu := rig()
	boom := errors.New("stuck")
	nope := errors.New("origin down")
	jobs := []Job{
		fakeJob{id: "stuck", stop: func(context.Context) error { return boom }, order: order, mu: mu},
		fakeJob{id: "fine", order: order, mu: mu},
	}
	err := ReclaimAll(context.Background(), jobs, fakeReleaser{order: order, mu: mu, fail: map[string]error{"fine": nope}})
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.ErrorIs(t, err, nope)
	assert.GreaterOrEqual(t, indexOf(*order, "release:stuck"), 0, "released although its stop failed")
	assert.GreaterOrEqual(t, indexOf(*order, "release:fine"), 0)
}

func TestReclaimAll_NothingRunningIsNothing(t *testing.T) {
	assert.NoError(t, ReclaimAll(context.Background(), nil, fakeReleaser{}))
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// The stop ends because the context did (the owner cannot wait): the release is still made, with a context that is alive, or an HTTP
// release would abort at once and the job would only come back when its lease ran out.
func TestReclaimAll_TheReleaseOutlivesTheContextThatEndedTheStop(t *testing.T) {
	order, mu := rig()
	var ctxErrs []error
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	jobs := []Job{fakeJob{id: "a", order: order, mu: mu, stop: func(c context.Context) error { <-c.Done(); return c.Err() }}}
	err := ReclaimAll(ctx, jobs, fakeReleaser{order: order, mu: mu, ctxErr: &ctxErrs})
	require.ErrorIs(t, err, context.DeadlineExceeded, "the stop's error is reported")
	require.Len(t, ctxErrs, 1, "the release was made")
	assert.NoError(t, ctxErrs[0], "with a context that was alive")
}

type hangingReleaser struct{}

func (hangingReleaser) Release(ctx context.Context, _ string) error { <-ctx.Done(); return ctx.Err() }

// An origin that does not answer does not hold the reclaim for ever.
func TestReclaimAll_AReleaseThatHangsIsCutOff(t *testing.T) {
	old := releaseTimeout
	releaseTimeout = 80 * time.Millisecond
	t.Cleanup(func() { releaseTimeout = old })
	order, mu := rig()
	start := time.Now()
	err := ReclaimAll(context.Background(), []Job{fakeJob{id: "a", order: order, mu: mu}}, hangingReleaser{})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 2*time.Second)
}
