package qualify

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

type fakeQueue struct {
	mu       sync.Mutex
	jobs     map[string]*wire.Job
	order    []wire.JobSubmission
	n        int
	failNth  int // the nth Submit fails (1-based); 0: none
	cancels  []string
	getError error
}

func newFakeQueue() *fakeQueue { return &fakeQueue{jobs: map[string]*wire.Job{}} }

func (q *fakeQueue) Submit(_ context.Context, s wire.JobSubmission) (wire.JobSubmissionResponse, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.failNth > 0 && len(q.order)+1 == q.failNth {
		q.order = append(q.order, s)
		return wire.JobSubmissionResponse{}, errors.New("queue is full")
	}
	q.n++
	id := fmt.Sprintf("j%d", q.n)
	q.order = append(q.order, s)
	q.jobs[id] = &wire.Job{ID: id, State: wire.StateQueued}
	return wire.JobSubmissionResponse{ID: id}, nil
}

func (q *fakeQueue) Get(_ context.Context, id string) (wire.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.getError != nil {
		return wire.Job{}, q.getError
	}
	j, ok := q.jobs[id]
	if !ok {
		return wire.Job{}, queue.ErrNotFound
	}
	return *j, nil
}

func (q *fakeQueue) Cancel(_ context.Context, id string) (wire.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.cancels = append(q.cancels, id)
	if j, ok := q.jobs[id]; ok && !j.State.Terminal() {
		j.State = wire.StateCancelled
	}
	return wire.Job{}, nil
}

func (q *fakeQueue) finish(id string, state wire.JobState, r *wire.Result) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.jobs[id].State, q.jobs[id].Result = state, r
}

func (q *fakeQueue) submissions() []wire.JobSubmission {
	q.mu.Lock()
	defer q.mu.Unlock()
	return slices.Clone(q.order)
}

type fakeStore struct {
	mu       sync.Mutex
	rows     []wire.EarnedLabel
	err      error
	attempts map[string]store.QualifyAttempt
	saveErr  error
}

func (s *fakeStore) QualifyAttempts() ([]store.QualifyAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.QualifyAttempt
	for _, a := range s.attempts {
		out = append(out, a)
	}
	return out, nil
}

func (s *fakeStore) SaveQualifyAttempt(a store.QualifyAttempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	if s.attempts == nil {
		s.attempts = map[string]store.QualifyAttempt{}
	}
	s.attempts[a.Label] = a
	return nil
}

func (s *fakeStore) DeleteQualifyAttempt(label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.attempts, label)
	return nil
}

func (s *fakeStore) EarnedLabels() ([]wire.EarnedLabel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.rows), s.err
}

func (s *fakeStore) SaveEarnedLabel(e wire.EarnedLabel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append(s.rows, e)
	return nil
}

type fakeSource struct {
	mu         sync.Mutex
	host       Host
	tasks      []Task
	tasksErr   error
	prepareErr error
	prepared   atomic.Int32
}

func (s *fakeSource) Host() Host {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.host
	h.Fingerprint = cloneMap(h.Fingerprint)
	return h
}

func (s *fakeSource) setFingerprint(fp map[string]string) {
	s.mu.Lock()
	s.host.Fingerprint = fp
	s.mu.Unlock()
}

func cloneMap(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (s *fakeSource) Tasks() ([]Task, error) { return s.tasks, s.tasksErr }

func (s *fakeSource) Prepare(_ context.Context, t Task) (wire.JobSubmission, error) {
	s.prepared.Add(1)
	if s.prepareErr != nil {
		return wire.JobSubmission{}, s.prepareErr
	}
	return wire.JobSubmission{
		Task: t.Name, Args: t.Args, Class: wire.ClassMerge, Cacheable: true, Interruptible: true,
		Snapshot:      wire.Snapshot{Commit: "c1", Tree: "t1", Base: "b1"},
		Labels:        []string{"os:darwin", "arch:arm64", "repo:purdex", t.Label(), "tool:node24"},
		SupersedeKey:  "interactive",
		Submitter:     wire.Submitter{Session: "someone"},
		TimeoutS:      600,
		QueueTimeoutS: 7200,
	}, nil
}

type rig struct {
	c       *Coordinator
	q       *fakeQueue
	st      *fakeStore
	src     *fakeSource
	now     time.Time
	granted atomic.Int32
}

func newRig(t *testing.T, tasks ...Task) *rig {
	t.Helper()
	r := &rig{q: newFakeQueue(), st: &fakeStore{}, now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	r.src = &fakeSource{host: mserv(), tasks: tasks}
	r.c = New(Config{Queue: r.q, Store: r.st, Source: r.src, Now: func() time.Time { return r.now }, Granted: func() { r.granted.Add(1) }})
	return r
}

func ctx() context.Context { return context.Background() }

// finishPair ends both halves of the pair the rig started, with the given results.
func (r *rig) finishPair(cand, ref *wire.Result) {
	r.q.finish("j1", wire.StateSucceeded, cand)
	r.q.finish("j2", wire.StateSucceeded, ref)
}

func goodRes(host string) *wire.Result {
	zero := 0
	return &wire.Result{Exit: &zero, Host: host, Log: "l", Summary: sum(10, 0)}
}

func TestTickStartsAPairWithTheRightHalves(t *testing.T) {
	r := newRig(t, tsc)
	r.c.Tick(ctx())
	subs := r.q.submissions()
	if len(subs) != 2 {
		t.Fatalf("%d submissions", len(subs))
	}
	cand, ref := subs[0], subs[1]
	if cand.Snapshot != ref.Snapshot || cand.Snapshot.Tree != "t1" {
		t.Fatalf("the halves do not share one snapshot: %+v %+v", cand.Snapshot, ref.Snapshot)
	}
	for name, s := range map[string]wire.JobSubmission{"candidate": cand, "reference": ref} {
		// interruptible on purpose: a newer pair's supersede key must be able to stop an older pair that is still running
		if s.Class != wire.ClassMerge || s.Cacheable || !s.Interruptible || s.AdHoc || s.Submitter.Session != SessionName {
			t.Errorf("%s: %+v", name, s)
		}
	}
	// the candidate half asks for no os:/arch: and not the task's own label, and for its own host; the reference half keeps them all
	for _, l := range []string{"os:darwin", "arch:arm64", tsc.Label()} {
		if slices.Contains(cand.Labels, l) {
			t.Errorf("the candidate half asks for %s: %v", l, cand.Labels)
		}
		if !slices.Contains(ref.Labels, l) {
			t.Errorf("the reference half lost %s: %v", l, ref.Labels)
		}
	}
	for _, l := range []string{"repo:purdex", "tool:node24", "host:mserv"} {
		if !slices.Contains(cand.Labels, l) {
			t.Errorf("the candidate half lacks %s: %v", l, cand.Labels)
		}
	}
	if !slices.Contains(ref.Labels, "host:air26") || slices.Contains(ref.Labels, "host:mserv") {
		t.Errorf("the reference half is not pinned to the reference host: %v", ref.Labels)
	}
	if cand.SupersedeKey == ref.SupersedeKey || !strings.HasPrefix(cand.SupersedeKey, "qualify:mserv:") {
		t.Errorf("supersede keys: %q %q", cand.SupersedeKey, ref.SupersedeKey)
	}
	if st, _ := r.c.Status(); len(st) != 1 || st[0].Label != tsc.Label() {
		t.Fatalf("in flight: %+v", st)
	}
}

// Both halves keep the task's own class, capped at merge: a dev class pair starves behind merge work, a release class one does not need
// to outrank every merge.
func TestHalvesTakeTheTasksClassUpToMerge(t *testing.T) {
	for _, tc := range []struct{ base, want wire.JobClass }{
		{wire.ClassRelease, wire.ClassMerge}, {wire.ClassMerge, wire.ClassMerge}, {wire.ClassDev, wire.ClassDev},
		{"", wire.ClassDev}, {"bogus", wire.ClassDev},
	} {
		cand, ref := halves(wire.JobSubmission{Class: tc.base}, tsc, "mserv")
		if cand.Class != tc.want || ref.Class != tc.want {
			t.Errorf("base %q: halves %q and %q, want %q", tc.base, cand.Class, ref.Class, tc.want)
		}
	}
}

// Two automatic triggers racing start one pair, and a pair in flight is not started again.
func TestTwoTriggersRacingStartOnePair(t *testing.T) {
	r := newRig(t, tsc)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r.c.Tick(ctx()) }()
	}
	wg.Wait()
	r.c.Tick(ctx())
	if n := len(r.q.submissions()); n != 2 {
		t.Fatalf("%d submissions: more than one pair was started", n)
	}
	if _, err := r.c.Request(ctx(), "purdex/tsc"); !errors.Is(err, ErrInFlight) {
		t.Fatalf("a manual request while a pair runs: %v", err)
	}
}

func TestAMatchEarnsTheLabelAndTellsTheDaemon(t *testing.T) {
	r := newRig(t, tsc)
	r.c.Tick(ctx())
	r.finishPair(goodRes("mserv"), goodRes("air26"))
	r.now = r.now.Add(3 * time.Minute)
	r.c.Tick(ctx())
	if len(r.st.rows) != 1 {
		t.Fatalf("rows %+v", r.st.rows)
	}
	row := r.st.rows[0]
	if row.Label != tsc.Label() || row.State != wire.EarnedLabelEarned || row.Snapshot != "t1" || row.Fingerprint["node"] != "24.14.0" || !row.GrantedAt.Equal(r.now) {
		t.Fatalf("%+v", row)
	}
	if r.granted.Load() != 1 {
		t.Fatalf("the daemon was told %d times", r.granted.Load())
	}
	if inf, rec := r.c.Status(); len(inf) != 0 || len(rec) != 1 || rec[0].Outcome != OutcomeMatch {
		t.Fatalf("%+v %+v", inf, rec)
	}
	// the label is held now: the next tick starts nothing
	r.st.rows[0].State = wire.EarnedLabelEarned
	before := len(r.q.submissions())
	r.c.Tick(ctx())
	if len(r.q.submissions()) != before {
		t.Fatal("a pair was started for a label that is held")
	}
}

// A mismatch earns nothing and the automatic retry waits for 24 h or a changed fingerprint; a person's request does not wait.
func TestAMismatchGrantsNothingAndTheRetryWaits(t *testing.T) {
	r := newRig(t, tsc)
	r.c.Tick(ctx())
	cs := sum(9, 0)
	cs.PassedDigest = digestOf(77) // other tests passed
	bad := goodRes("mserv")
	bad.Summary = cs
	r.finishPair(bad, goodRes("air26"))
	r.c.Tick(ctx()) // settles, and the retry is now waiting
	if len(r.st.rows) != 0 || r.granted.Load() != 0 {
		t.Fatalf("a mismatch granted: %+v", r.st.rows)
	}
	if _, rec := r.c.Status(); len(rec) != 1 || rec[0].Outcome != OutcomeMismatch {
		t.Fatalf("%+v", rec)
	}
	if n := len(r.q.submissions()); n != 2 {
		t.Fatalf("the automatic trigger did not wait: %d submissions", n)
	}
	r.now = r.now.Add(RetryAfter - time.Second)
	r.c.Tick(ctx())
	if n := len(r.q.submissions()); n != 2 {
		t.Fatalf("23:59:59 started a pair: %d", n)
	}
	r.now = r.now.Add(time.Second)
	r.c.Tick(ctx())
	if n := len(r.q.submissions()); n != 4 {
		t.Fatalf("24:00:00 did not start a pair: %d", n)
	}
}

func TestAChangedFingerprintEndsTheWaitAndManualDoesNotWait(t *testing.T) {
	r := newRig(t, tsc)
	r.c.Tick(ctx())
	r.finishPair(&wire.Result{Host: "mserv"}, goodRes("air26")) // the candidate half never ran to an exit: void
	r.c.Tick(ctx())
	if n := len(r.q.submissions()); n != 2 {
		t.Fatalf("%d", n)
	}
	got, err := r.c.Request(ctx(), "purdex/tsc") // by hand: no wait
	if err != nil || got.Label != tsc.Label() {
		t.Fatalf("%+v %v", got, err)
	}
	// and the new pair is in flight, so the automatic trigger leaves it alone; end it, then change the toolchain
	r.q.finish("j3", wire.StateFailed, &wire.Result{Host: "mserv"})
	r.q.finish("j4", wire.StateSucceeded, goodRes("air26"))
	r.c.Tick(ctx())
	n := len(r.q.submissions())
	r.src.setFingerprint(map[string]string{"node": "24.15.0"})
	r.c.Tick(ctx())
	if len(r.q.submissions()) != n+2 {
		t.Fatalf("a changed fingerprint did not end the wait: %d -> %d", n, len(r.q.submissions()))
	}
}

// A half that ran on another host than it was meant for makes the pair void.
func TestAHalfOnTheWrongHostIsVoid(t *testing.T) {
	r := newRig(t, tsc)
	r.c.Tick(ctx())
	r.finishPair(goodRes("air26"), goodRes("air26")) // the reference host took the candidate half too
	r.c.Tick(ctx())
	if len(r.st.rows) != 0 {
		t.Fatal("granted on a pair that ran on the wrong host")
	}
	if _, rec := r.c.Status(); len(rec) != 1 || rec[0].Outcome != OutcomeVoid {
		t.Fatalf("%+v", rec)
	}
}

// A match is not trusted when the host's toolchain moved while the pair ran.
func TestAMatchAcrossAToolchainChangeIsVoid(t *testing.T) {
	r := newRig(t, tsc)
	r.c.Tick(ctx())
	r.finishPair(goodRes("mserv"), goodRes("air26"))
	r.src.setFingerprint(map[string]string{"node": "24.15.0"})
	r.c.Tick(ctx())
	if len(r.st.rows) != 0 || r.granted.Load() != 0 {
		t.Fatal("granted across a toolchain change")
	}
	if _, rec := r.c.Status(); len(rec) != 1 || rec[0].Outcome != OutcomeVoid {
		t.Fatalf("%+v", rec)
	}
}

// A result of an older definition never earns the label: while the pair ran the task's qualify args, its needs, its reference or its
// definition changed (the host's fingerprint did not), or the task is gone.
func TestAMatchForAnOlderDefinitionOfTheTaskIsVoid(t *testing.T) {
	for name, change := range map[string]func(*Task){
		"the qualify args": func(t *Task) { t.Args = []string{"another.test.ts"} },
		"the needs":        func(t *Task) { t.Needs = []string{"os:darwin", "tool:node25"} },
		"the reference":    func(t *Task) { t.Reference = "mlab" },
		"the definition":   func(t *Task) { t.DefHash = "d2" },
	} {
		task := tsc
		task.DefHash = "d1"
		r := newRig(t, task)
		r.c.Tick(ctx())
		changed := task
		changed.Needs = slices.Clone(task.Needs)
		change(&changed)
		r.src.tasks = []Task{changed}
		r.finishPair(goodRes("mserv"), goodRes("air26"))
		r.c.Tick(ctx())
		if len(r.st.rows) != 0 || r.granted.Load() != 0 {
			t.Errorf("%s changed and the old result earned the label", name)
		}
		if _, rec := r.c.Status(); len(rec) != 1 || rec[0].Outcome != OutcomeVoid {
			t.Errorf("%s: %+v", name, rec)
		}
	}
	// the task gone altogether
	r := newRig(t, tsc)
	r.c.Tick(ctx())
	r.src.tasks = nil
	r.finishPair(goodRes("mserv"), goodRes("air26"))
	r.c.Tick(ctx())
	if len(r.st.rows) != 0 {
		t.Fatal("a task that is gone earned a label")
	}
	// and the order of the needs is not a change
	task := tsc
	task.Needs = []string{"tool:node24", "os:darwin"}
	r = newRig(t, task)
	r.c.Tick(ctx())
	reordered := task
	reordered.Needs = []string{"os:darwin", "tool:node24"}
	r.src.tasks = []Task{reordered}
	r.finishPair(goodRes("mserv"), goodRes("air26"))
	r.c.Tick(ctx())
	if len(r.st.rows) != 1 {
		t.Fatal("reordered needs voided a match")
	}
}

// The retry memory follows the tasks that exist: it is not kept for one that is gone.
func TestTheRetryMemoryForgetsGoneTasks(t *testing.T) {
	r := newRig(t, tsc)
	r.c.Tick(ctx())
	r.finishPair(&wire.Result{Host: "mserv"}, goodRes("air26")) // void
	r.c.Tick(ctx())
	if len(r.c.last) != 1 {
		t.Fatalf("%v", r.c.last)
	}
	r.src.tasks = nil
	r.c.Tick(ctx())
	if len(r.c.last) != 0 {
		t.Fatalf("kept for a task that is gone: %v", r.c.last)
	}
}

// A half that ended without running (cancelled, expired) voids the pair at once and the other half is cancelled.
func TestAHalfThatNeverRanCancelsTheOtherAndVoidsThePair(t *testing.T) {
	r := newRig(t, tsc)
	r.c.Tick(ctx())
	r.q.finish("j1", wire.StateExpired, nil) // the candidate half timed out in the queue; the reference half is still queued
	r.c.Tick(ctx())
	if !slices.Contains(r.q.cancels, "j2") {
		t.Fatalf("the other half was not cancelled: %v", r.q.cancels)
	}
	if inf, rec := r.c.Status(); len(inf) != 0 || len(rec) != 1 || rec[0].Outcome != OutcomeVoid {
		t.Fatalf("%+v %+v", inf, rec)
	}
}

func TestAHalfGoneFromTheQueueVoidsThePairAndAReadErrorWaits(t *testing.T) {
	r := newRig(t, tsc)
	r.c.Tick(ctx())
	r.q.getError = errors.New("database is locked")
	r.c.Tick(ctx())
	if inf, _ := r.c.Status(); len(inf) != 1 {
		t.Fatal("a read error ended the pair")
	}
	r.q.getError = nil
	delete(r.q.jobs, "j1")
	r.c.Tick(ctx())
	if inf, rec := r.c.Status(); len(inf) != 0 || len(rec) != 1 || rec[0].Outcome != OutcomeVoid {
		t.Fatalf("%+v %+v", inf, rec)
	}
	if !slices.Contains(r.q.cancels, "j2") || slices.Contains(r.q.cancels, "j1") {
		t.Fatalf("the half that is still there must be stopped, and the gone one not asked about: %v", r.q.cancels)
	}
}

// The second half failing to queue cancels the first (one half alone proves nothing) and the failure is held like any other.
func TestASecondSubmitFailureCancelsTheFirstHalf(t *testing.T) {
	r := newRig(t, tsc)
	r.q.failNth = 2
	r.c.Tick(ctx())
	if !slices.Contains(r.q.cancels, "j1") {
		t.Fatalf("the first half was left: %v", r.q.cancels)
	}
	if inf, rec := r.c.Status(); len(inf) != 0 || len(rec) != 1 || rec[0].Outcome != OutcomeVoid {
		t.Fatalf("%+v %+v", inf, rec)
	}
	r.q.failNth = 0
	r.c.Tick(ctx()) // within the wait: nothing new
	if n := len(r.q.submissions()); n != 2 {
		t.Fatalf("%d submissions after a failure inside the wait", n)
	}
}

func TestAPrepareFailureIsHeldAndNothingIsQueued(t *testing.T) {
	r := newRig(t, tsc)
	r.src.prepareErr = errors.New("the clone is not a git repository")
	r.c.Tick(ctx())
	r.c.Tick(ctx())
	if len(r.q.submissions()) != 0 {
		t.Fatal("something was queued")
	}
	if r.src.prepared.Load() != 1 {
		t.Fatalf("Prepare ran %d times: a failure inside the wait is not retried", r.src.prepared.Load())
	}
}

func TestRequestRefusesWhatCheckRefuses(t *testing.T) {
	r := newRig(t, tsc)
	r.src.host.Alias = "air26" // this host is the reference
	if _, err := r.c.Request(ctx(), "purdex/tsc"); !errors.Is(err, ErrReference) {
		t.Fatalf("%v", err)
	}
	if _, err := r.c.Request(ctx(), "purdex/none"); err == nil {
		t.Fatal("an unknown task")
	}
	if len(r.q.submissions()) != 0 {
		t.Fatal("a refused request queued something")
	}
}

func TestAnUnreadableEarnedStoreStartsNothing(t *testing.T) {
	r := newRig(t, tsc)
	r.st.err = errors.New("disk I/O error")
	r.c.Tick(ctx())
	if len(r.q.submissions()) != 0 {
		t.Fatal("a pair was started without knowing what is held")
	}
}

// A pair a person started by hand is settled by Settle alone: no other request and no automatic tick is needed.
func TestSettleFinishesAManualPairWithoutATick(t *testing.T) {
	r := newRig(t, tsc)
	if _, err := r.c.Request(ctx(), "purdex/tsc"); err != nil {
		t.Fatal(err)
	}
	r.finishPair(goodRes("mserv"), goodRes("air26"))
	r.now = r.now.Add(3 * time.Minute)
	r.c.Settle(ctx())
	if inf, rec := r.c.Status(); len(inf) != 0 || len(rec) != 1 || rec[0].Outcome != OutcomeMatch {
		t.Fatalf("%+v %+v", inf, rec)
	}
	if r.granted.Load() != 1 {
		t.Fatalf("the daemon was told %d times", r.granted.Load())
	}
}

func TestRunSettleSettlesOnItsPeriodAndStopsWithItsContext(t *testing.T) {
	r := newRig(t, tsc)
	if _, err := r.c.Request(ctx(), "purdex/tsc"); err != nil {
		t.Fatal(err)
	}
	r.finishPair(goodRes("mserv"), goodRes("air26"))
	cctx, cancel := context.WithCancel(ctx())
	done := make(chan struct{})
	go func() { r.c.RunSettle(cctx, time.Millisecond); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for r.granted.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunSettle did not stop with its context")
	}
	if r.granted.Load() != 1 {
		t.Fatalf("settled %d times", r.granted.Load())
	}
}

// A manifest that cannot be read is not an unknown task: a request for a task that is not found while one is broken is a fault of the host
// (not the 404), and the retry memory of the tasks that cannot be seen is kept.
func TestABrokenManifestIsNotAnUnknownTask(t *testing.T) {
	r := newRig(t, tsc)
	r.src.tasksErr = errors.New("a: manifest does not parse")
	_, err := r.c.Request(ctx(), "purdex/nope")
	var unknown *UnknownTaskError
	if err == nil || errors.As(err, &unknown) || !strings.Contains(err.Error(), "does not parse") {
		t.Fatalf("%v", err)
	}
	r.src.tasksErr = nil
	if _, err := r.c.Request(ctx(), "purdex/nope"); !errors.As(err, &unknown) {
		t.Fatalf("%v", err)
	}
}

func TestABrokenManifestKeepsTheRetryMemory(t *testing.T) {
	r := newRig(t, tsc)
	r.c.last[tsc.Label()] = Attempt{At: r.now, Outcome: OutcomeMismatch}
	r.src.tasks, r.src.tasksErr = nil, errors.New("unreadable")
	r.c.Tick(ctx())
	if _, ok := r.c.last[tsc.Label()]; !ok {
		t.Fatal("the retry memory of a task hidden by a broken manifest was dropped")
	}
	r.src.tasksErr = nil
	r.c.Tick(ctx())
	if _, ok := r.c.last[tsc.Label()]; ok {
		t.Fatal("the retry memory of a task that is gone was kept")
	}
}

// A pair that matched is not voided by a manifest that cannot be read at the moment it is settled: it stays, and is settled (and earns the
// label) on the first look after the manifest can be read again.
func TestAMatchWaitsOutAManifestThatCannotBeReadJustNow(t *testing.T) {
	r := newRig(t, tsc)
	r.c.Tick(ctx())
	r.finishPair(goodRes("mserv"), goodRes("air26"))
	r.now = r.now.Add(3 * time.Minute)
	r.src.tasks, r.src.tasksErr = nil, errors.New("unreadable just now")
	r.c.Settle(ctx())
	if inf, rec := r.c.Status(); len(inf) != 1 || len(rec) != 0 || len(r.st.rows) != 0 {
		t.Fatalf("%+v %+v %+v", inf, rec, r.st.rows)
	}
	r.src.tasks, r.src.tasksErr = []Task{tsc}, nil
	r.c.Settle(ctx())
	if inf, rec := r.c.Status(); len(inf) != 0 || len(rec) != 1 || rec[0].Outcome != OutcomeMatch || len(r.st.rows) != 1 {
		t.Fatalf("%+v %+v %+v", inf, rec, r.st.rows)
	}
	// and a task that is known to be gone (the read worked) is still void
	r2 := newRig(t, tsc)
	r2.c.Tick(ctx())
	r2.finishPair(goodRes("mserv"), goodRes("air26"))
	r2.now = r2.now.Add(3 * time.Minute)
	r2.src.tasks = nil
	r2.c.Settle(ctx())
	if _, rec := r2.c.Status(); len(rec) != 1 || rec[0].Outcome != OutcomeVoid {
		t.Fatalf("%+v", rec)
	}
}

// A restart does not bring a retry forward: a second coordinator on the same store (and the same queue) waits out the 24 h the first one
// started, and starts a pair at 24:00:00.
func TestARestartKeepsTheRetryWait(t *testing.T) {
	r := newRig(t, tsc)
	r.c.Tick(ctx())
	bad := goodRes("mserv")
	bad.Summary = sum(9, 0)
	bad.Summary.PassedDigest = digestOf(77)
	r.finishPair(bad, goodRes("air26"))
	r.c.Tick(ctx()) // the mismatch is settled and saved
	if len(r.st.attempts) != 1 {
		t.Fatalf("the attempt was not saved: %+v", r.st.attempts)
	}
	before := len(r.q.submissions())

	r.now = r.now.Add(time.Hour)
	restarted := New(Config{Queue: r.q, Store: r.st, Source: r.src, Now: func() time.Time { return r.now }, Granted: func() {}})
	restarted.Tick(ctx())
	if n := len(r.q.submissions()); n != before {
		t.Fatalf("the first record after a restart started a pair inside the 24 h: %d submissions", n)
	}
	r.now = r.now.Add(RetryAfter - time.Hour)
	restarted.Tick(ctx())
	if n := len(r.q.submissions()); n != before+2 {
		t.Fatalf("24:00:00 after the failure did not start a pair: %d submissions", n)
	}
}

// An earned label ends the memory of the failures before it, in the store as well; a task that is gone ends it too.
func TestEarningTheLabelOrLosingTheTaskForgetsTheSavedAttempt(t *testing.T) {
	r := newRig(t, tsc)
	r.st.attempts = map[string]store.QualifyAttempt{tsc.Label(): {Label: tsc.Label(), Outcome: string(OutcomeVoid), At: r.now.Add(-48 * time.Hour)}}
	r.c = New(Config{Queue: r.q, Store: r.st, Source: r.src, Now: func() time.Time { return r.now }, Granted: func() { r.granted.Add(1) }})
	r.c.Tick(ctx()) // 48 h after the failure: a pair starts
	r.finishPair(goodRes("mserv"), goodRes("air26"))
	r.now = r.now.Add(3 * time.Minute)
	r.c.Tick(ctx())
	if len(r.st.attempts) != 0 || len(r.st.rows) != 1 {
		t.Fatalf("attempts %+v rows %+v", r.st.attempts, r.st.rows)
	}
	r.st.attempts = map[string]store.QualifyAttempt{"task:purdex/gone": {Label: "task:purdex/gone", Outcome: string(OutcomeVoid), At: r.now}}
	r.c = New(Config{Queue: r.q, Store: r.st, Source: r.src, Now: func() time.Time { return r.now }, Granted: func() {}})
	r.c.Tick(ctx())
	if len(r.st.attempts) != 0 {
		t.Fatalf("the attempt of a task that no longer exists was kept: %+v", r.st.attempts)
	}
}

// A store that cannot save the attempt does not stop the coordinator: the wait still holds in memory for this run.
func TestAFailedSaveOfTheAttemptStillWaitsInMemory(t *testing.T) {
	r := newRig(t, tsc)
	r.st.saveErr = errors.New("disk full")
	r.c.Tick(ctx())
	bad := goodRes("mserv")
	bad.Summary = sum(9, 0)
	bad.Summary.PassedDigest = digestOf(77)
	r.finishPair(bad, goodRes("air26"))
	r.c.Tick(ctx())
	before := len(r.q.submissions())
	r.c.Tick(ctx())
	if n := len(r.q.submissions()); n != before {
		t.Fatalf("a failed save lost the wait in this run: %d", n)
	}
}

// Qualification reads the coordinator's own memory: the pair in flight with its ids, then the wait after a mismatch with the why of the
// record, and the repos whose manifest cannot be read. It starts and settles nothing.
func TestQualificationFollowsThePairThroughAMismatch(t *testing.T) {
	r := newRig(t, tsc)
	r.c.Tick(ctx())
	v, err := r.c.Qualification()
	if err != nil || len(v.Tasks) != 1 || v.Tasks[0].Progress != wire.QualProgressPairing || v.Tasks[0].Pair == nil || v.Tasks[0].Pair.Candidate != "j1" || v.Tasks[0].Pair.Reference != "j2" {
		t.Fatalf("%+v %v", v, err)
	}
	cs := sum(9, 0)
	cs.PassedDigest = digestOf(77)
	bad := goodRes("mserv")
	bad.Summary = cs
	r.finishPair(bad, goodRes("air26"))
	r.c.Tick(ctx())
	v, _ = r.c.Qualification()
	tk := v.Tasks[0]
	if tk.Progress != wire.QualProgressWaiting || tk.Last == nil || tk.Last.Outcome != "mismatch" || tk.Last.Why == "" || tk.RetryAt == nil || !tk.RetryAt.Equal(r.now.Add(RetryAfter)) || len(v.Recent) != 1 {
		t.Fatalf("%+v", v)
	}
	if n := len(r.q.submissions()); n != 2 {
		t.Fatalf("reading started a pair: %d", n)
	}
	r.src.tasksErr = &ManifestError{Repo: "purdex", Err: errors.New("bad")}
	r.src.tasks = nil
	if v, _ = r.c.Qualification(); len(v.Unreadable) != 1 || v.Unreadable[0] != "purdex" {
		t.Fatalf("%+v", v)
	}
}

// Reading the qualification does its disk reads outside the coordinator's lock: a Tasks that hangs must not stop Settle and Tick.
func TestQualificationDoesNotHoldTheLockWhileItReadsTheManifests(t *testing.T) {
	r := newRig(t, tsc)
	hang := &blockingSource{fakeSource: r.src, entered: make(chan struct{}), release: make(chan struct{})}
	r.c.cfg.Source = hang
	done := make(chan struct{})
	go func() { _, _ = r.c.Qualification(); close(done) }()
	<-hang.entered
	settled := make(chan struct{})
	go func() { r.c.Settle(ctx()); close(settled) }()
	select {
	case <-settled:
	case <-time.After(5 * time.Second):
		t.Fatal("Settle waits for a Qualification that is reading the manifests")
	}
	close(hang.release)
	<-done
}

// A pair that settles (and earns the label) between the memory copy and the rows read must not read as "no pair, no label, ready": the old
// pair next to the new earned row is held, idle.
func TestQualificationAPairSettlingMidReadIsNeverReady(t *testing.T) {
	for _, after := range []bool{false, true} { // the settle comes before the rows are read, or right after
		r := newRig(t, tsc)
		r.c.Tick(ctx())
		r.finishPair(goodRes("mserv"), goodRes("air26"))
		r.c.cfg.Store = &hookStore{fakeStore: r.st, after: after, onRead: func() { r.c.Settle(ctx()) }}
		v, err := r.c.Qualification()
		if err != nil || len(v.Tasks) != 1 {
			t.Fatalf("after=%v: %+v %v", after, v, err)
		}
		tk := v.Tasks[0]
		held := tk.Hold == wire.QualHoldEarned && tk.Progress == wire.QualProgressIdle
		stillPairing := tk.Hold == wire.QualHoldNone && tk.Progress == wire.QualProgressPairing
		if !held && !stillPairing {
			t.Fatalf("after=%v: a finished pair read as neither earned nor pairing: %+v", after, tk)
		}
	}
}

type hookStore struct {
	*fakeStore
	after  bool // run onRead after the rows were read (the rows returned are then the older ones)
	onRead func()
}

func (h *hookStore) EarnedLabels() ([]wire.EarnedLabel, error) {
	if !h.after {
		h.onRead()
	}
	rows, err := h.fakeStore.EarnedLabels()
	if h.after {
		h.onRead()
	}
	return rows, err
}

type blockingSource struct {
	*fakeSource
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingSource) Tasks() ([]Task, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return b.fakeSource.Tasks()
}
