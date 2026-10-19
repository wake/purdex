package qualify

import (
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/queue"
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
	mu   sync.Mutex
	rows []wire.EarnedLabel
	err  error
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

func (s *fakeSource) Tasks() []Task { return s.tasks }

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
		if s.Class != wire.ClassDev || s.Cacheable || !s.Interruptible || s.AdHoc || s.Submitter.Session != SessionName {
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

// 5b-2b is a library: nothing in the daemon's wiring or routes reaches the coordinator. 5b-2c is the PR that wires it and deletes this test
// (it must not go out before the passed-test digest writers, docs/plans WWD-5).
func TestNothingWiresTheCoordinatorYet(t *testing.T) {
	const self = "github.com/wake/purdex/internal/wwd/qualify"
	for _, dir := range []string{"../../../cmd/wwd", "../daemon", "../jobsapi", "../jobscmd", "../../../cmd/pdx"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range f.Imports {
				if strings.Trim(imp.Path.Value, `"`) == self {
					t.Errorf("%s imports the qualify package: 5b-2b must not wire the coordinator", filepath.Join(dir, name))
				}
			}
			src, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(src), "/v1/qualify") {
				t.Errorf("%s names a qualify route: 5b-2b must not add one", filepath.Join(dir, name))
			}
		}
	}
}
