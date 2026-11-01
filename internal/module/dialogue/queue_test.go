package dialogue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/modevents"
)

// T8: the job channel. The base's queue_test.go / engine_test.go cases that still apply, rewritten for the per-session
// queue and the two job kinds.

type jcClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *jcClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *jcClock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type jcFinish struct {
	job           job
	state, reason string
}

// jcKind is the stand-in for T9's and T10's handlers: it records what the channel asks of a kind.
type jcKind struct {
	mu       sync.Mutex
	finishes []jcFinish
	applies  []string
	applyErr []error // consumed one per apply call; empty: applyAll (nil when unset)
	applyAll error   // returned by every apply once applyErr is used up
	buildErr error
	onApply  func()
	onBuild  func() // called before build returns (not under mu)
	afters   []job
	usages   []usage
}

func (k *jcKind) build(j job) (callInput, error) {
	if k.onBuild != nil {
		k.onBuild()
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.buildErr != nil {
		return callInput{}, k.buildErr
	}
	return callInput{System: "sys-" + j.kind, Prompt: fmt.Sprintf("prompt %v", j.payload)}, nil
}

func (k *jcKind) after(j job) {
	k.mu.Lock()
	k.afters = append(k.afters, j)
	k.mu.Unlock()
}

func (k *jcKind) afterCount() int { k.mu.Lock(); defer k.mu.Unlock(); return len(k.afters) }

func (k *jcKind) apply(j job, text string, u usage) error {
	if k.onApply != nil {
		k.onApply()
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.applies = append(k.applies, fmt.Sprintf("%s:%v:%d:%s", j.kind, j.payload, j.attempt, text))
	k.usages = append(k.usages, u)
	if len(k.applyErr) == 0 {
		return k.applyAll
	}
	err := k.applyErr[0]
	k.applyErr = k.applyErr[1:]
	return err
}

func (k *jcKind) finish(j job, state, reason string) {
	k.mu.Lock()
	k.finishes = append(k.finishes, jcFinish{j, state, reason})
	k.mu.Unlock()
}

func (k *jcKind) last() jcFinish {
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.finishes) == 0 {
		return jcFinish{}
	}
	return k.finishes[len(k.finishes)-1]
}

func (k *jcKind) count() int { k.mu.Lock(); defer k.mu.Unlock(); return len(k.finishes) }

type jcRig struct {
	t     *testing.T
	ch    *channel
	kind  *jcKind
	clock *jcClock
	capOK bool
	mu    sync.Mutex
}

const jcSID = "0f8e2c1a-1b2c-4d3e-8f90-a1b2c3d4e5f6"

func newJCRig(t *testing.T, cap int) *jcRig {
	t.Helper()
	r := &jcRig{t: t, kind: &jcKind{}, clock: &jcClock{t: time.Unix(1_800_000_000, 0)}, capOK: true}
	r.ch = newChannel(chanDeps{
		capable:  func(string) bool { r.mu.Lock(); defer r.mu.Unlock(); return r.capOK },
		handlers: map[string]kindHandler{kindTurnSummary: r.kind, kindRecap: r.kind},
		now:      r.clock.Now, logf: func(string, ...any) {}, callCap: cap,
	})
	return r
}

func (r *jcRig) setCapable(v bool) { r.mu.Lock(); r.capOK = v; r.mu.Unlock() }

func (r *jcRig) enq(sid, kind string, payload any) string {
	r.t.Helper()
	id, why := r.ch.enqueue(sid, kind, payload)
	if why != "" {
		r.t.Fatalf("enqueue %s: not queued (%s)", kind, why)
	}
	return id
}

func (r *jcRig) next(sid string) (wireJob, bool) {
	return r.ch.Next(context.Background(), "streamA", sid, 0)
}

// 1. both kinds are "turn" on the wire; the JSON is the base's shape.
func TestJobWire_BothKindsAreTurn(t *testing.T) {
	r := newJCRig(t, 0)
	r.enq(jcSID, kindTurnSummary, 1)
	r.enq(jcSID, kindRecap, 2)
	for _, want := range []struct{ kind, sys, prompt string }{{kindTurnSummary, "sys-turn-summary", "prompt 1"}, {kindRecap, "sys-recap", "prompt 2"}} {
		j, ok := r.next(jcSID)
		if !ok || j.Kind != want.kind {
			t.Fatalf("job %+v ok=%v, want kind %s", j, ok, want.kind)
		}
		b, err := json.Marshal(j)
		if err != nil {
			t.Fatal(err)
		}
		exp := fmt.Sprintf(`{"id":%q,"kind":"turn","complete":{"model":"haiku","system":[{"text":%q,"cache":true}],"prompt":%q,"max_tokens":4096,"effort":"low","timeout_ms":30000}}`,
			j.ID, want.sys, want.prompt)
		if string(b) != exp {
			t.Fatalf("wire\n got %s\nwant %s", b, exp)
		}
		if !strings.HasPrefix(j.ID, "dlg-") {
			t.Fatalf("job id %q", j.ID)
		}
		if _, err := r.ch.Result("streamA", j.ID, result{Answered: true, Text: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
}

// 2. the fourth waiting turn summary drops the oldest; a recap is never dropped and does not count.
func TestEnqueue_BacklogDropsOldestTurnSummaryOnly(t *testing.T) {
	r := newJCRig(t, 0)
	r.enq(jcSID, kindRecap, "r1")
	for i := 1; i <= 4; i++ {
		r.enq(jcSID, kindTurnSummary, i)
	}
	if r.kind.count() != 1 {
		t.Fatalf("finishes: %+v", r.kind.finishes)
	}
	f := r.kind.last()
	if f.job.kind != kindTurnSummary || f.job.payload != 1 || f.state != stateSkipped || f.reason != reasonBacklog {
		t.Fatalf("dropped %+v", f)
	}
	var got []string
	for {
		j, ok := r.next(jcSID)
		if !ok {
			break
		}
		got = append(got, j.Complete.Prompt)
		_, _ = r.ch.Result("streamA", j.ID, result{Answered: true, Text: "x"})
	}
	if strings.Join(got, ",") != "prompt r1,prompt 2,prompt 3,prompt 4" {
		t.Fatalf("handed out %v", got)
	}
}

// 2b. the queue is per session: another session's jobs neither count nor are handed out.
func TestEnqueue_QueuesArePerSession(t *testing.T) {
	r := newJCRig(t, 0)
	other := "11111111-1b2c-4d3e-8f90-a1b2c3d4e5f6"
	for i := 0; i < 3; i++ {
		r.enq(jcSID, kindTurnSummary, i)
	}
	r.enq(other, kindTurnSummary, "o")
	if r.kind.count() != 0 {
		t.Fatalf("a job was dropped: %+v", r.kind.finishes)
	}
	j, ok := r.next(other)
	if !ok || j.Complete.Prompt != "prompt o" {
		t.Fatalf("other got %+v %v", j, ok)
	}
}

// 3. the hourly cap: Next ends the job that would be the 3rd call as skipped(cap); a new hour admits again.
func TestNext_HourlyCapSkips(t *testing.T) {
	r := newJCRig(t, 2)
	for i := 0; i < 3; i++ {
		r.enq(jcSID, kindRecap, i)
	}
	for i := 0; i < 2; i++ {
		j, ok := r.next(jcSID)
		if !ok {
			t.Fatalf("call %d not handed out", i)
		}
		_, _ = r.ch.Result("streamA", j.ID, result{Answered: true, Text: "x"})
	}
	if _, ok := r.next(jcSID); ok {
		t.Fatal("a job past the cap was handed out")
	}
	if f := r.kind.last(); f.state != stateSkipped || f.reason != reasonCap || f.job.payload != 2 {
		t.Fatalf("capped %+v", f)
	}
	r.clock.Add(time.Hour)
	r.enq(jcSID, kindRecap, 3)
	if _, ok := r.next(jcSID); !ok {
		t.Fatal("the new hour did not admit")
	}
}

// 4. a result from another stream, or for an unknown job, or twice, is ErrNotLeased.
func TestResult_NotLeased(t *testing.T) {
	r := newJCRig(t, 0)
	r.enq(jcSID, kindRecap, 1)
	j, _ := r.next(jcSID)
	if _, err := r.ch.Result("streamB", j.ID, result{Answered: true, Text: "x"}); !errors.Is(err, modevents.ErrNotLeased) {
		t.Fatalf("other stream: %v", err)
	}
	if _, err := r.ch.Result("streamA", "dlg-nope", result{Answered: true}); !errors.Is(err, modevents.ErrNotLeased) {
		t.Fatalf("unknown job: %v", err)
	}
	if _, err := r.ch.Result("streamA", j.ID, result{Answered: true, Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ch.Result("streamA", j.ID, result{Answered: true, Text: "x"}); !errors.Is(err, modevents.ErrNotLeased) {
		t.Fatalf("second result: %v", err)
	}
	if len(r.kind.applies) != 1 {
		t.Fatalf("applies %v", r.kind.applies)
	}
}

// 5. a lease past timeout + 10 s is reaped: failed(lost), and the queue moves on to the next job.
func TestReap_ExpiredLeaseIsLost(t *testing.T) {
	r := newJCRig(t, 0)
	r.enq(jcSID, kindTurnSummary, 1)
	r.enq(jcSID, kindTurnSummary, 2)
	j, _ := r.next(jcSID)
	r.clock.Add(39 * time.Second)
	r.ch.reap()
	if r.kind.count() != 0 {
		t.Fatal("reaped before timeout + 10 s")
	}
	r.clock.Add(2 * time.Second) // 41 s > 30 s + 10 s
	r.ch.reap()
	if f := r.kind.last(); f.state != stateFailed || f.reason != reasonLost || f.job.payload != 1 {
		t.Fatalf("lost %+v", f)
	}
	if _, err := r.ch.Result("streamA", j.ID, result{Answered: true, Text: "late"}); !errors.Is(err, modevents.ErrNotLeased) {
		t.Fatalf("late result: %v", err)
	}
	if j2, ok := r.next(jcSID); !ok || j2.Complete.Prompt != "prompt 2" {
		t.Fatalf("the queue did not move on: %+v %v", j2, ok)
	}
}

// 6. an apply that cannot store is applied again by the reaper until it is taken; the queue is not stalled meanwhile.
func TestResult_ApplyFailureIsRetriedByTheReaper(t *testing.T) {
	r := newJCRig(t, 0)
	r.kind.applyErr = []error{errors.New("disk full"), errors.New("disk full")}
	r.enq(jcSID, kindRecap, 1)
	r.enq(jcSID, kindRecap, 2)
	j, _ := r.next(jcSID)
	if _, err := r.ch.Result("streamA", j.ID, result{Answered: true, Text: "answer"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.next(jcSID); !ok {
		t.Fatal("the queue stalled behind the unstored answer")
	}
	// Next does not retry (only the reaper does): the first apply is the only one so far.
	if len(r.kind.applies) != 1 || r.kind.count() != 0 {
		t.Fatalf("after Next: applies %v finishes %v", r.kind.applies, r.kind.finishes)
	}
	r.ch.reap() // fails again
	r.ch.reap() // taken
	r.ch.reap() // nothing left to apply
	if r.kind.afterCount() != 1 {
		t.Fatalf("after ran %d times", r.kind.afterCount())
	}
	if len(r.kind.applies) != 3 {
		t.Fatalf("applies %v", r.kind.applies)
	}
	for _, a := range r.kind.applies {
		if a != "recap:1:1:answer" {
			t.Fatalf("a different answer was applied: %s", a)
		}
	}
	if r.kind.count() != 0 {
		t.Fatalf("a finished row was ended again: %+v", r.kind.finishes)
	}
}

// 7. a session whose mod cannot run jobs gets nothing queued, nothing handed out, nothing announced.
func TestNotCapableSessionGetsNoJob(t *testing.T) {
	r := newJCRig(t, 0)
	r.enq(jcSID, kindRecap, 1)
	r.setCapable(false)
	if id, why := r.ch.enqueue(jcSID, kindRecap, 2); id != "" || why != "no_mod" {
		t.Fatalf("enqueue: %q %q", id, why)
	}
	if _, ok := r.next(jcSID); ok {
		t.Fatal("handed out to a session that is not capable")
	}
	if r.ch.JobWaiting(jcSID) {
		t.Fatal("announced a job to a session that is not capable")
	}
	r.setCapable(true)
	if !r.ch.JobWaiting(jcSID) {
		t.Fatal("not announced to a capable session")
	}
	if r.kind.count() != 0 {
		t.Fatalf("rows were written: %+v", r.kind.finishes)
	}
}

// build failure ends the job as failed(build) and moves on.
func TestNext_BuildFailureEndsTheJob(t *testing.T) {
	r := newJCRig(t, 0)
	r.kind.buildErr = errors.New("store down")
	r.enq(jcSID, kindRecap, 1)
	if _, ok := r.next(jcSID); ok {
		t.Fatal("handed out a job that could not be built")
	}
	if f := r.kind.last(); f.state != stateFailed || f.reason != reasonBuild {
		t.Fatalf("%+v", f)
	}
	if r.ch.JobWaiting(jcSID) {
		t.Fatal("the dead job is still queued")
	}
}

// 8. result classification.
func TestResult_Classification(t *testing.T) {
	// first call of each case runs as attempt 1 of a turn summary with a timeout of 30 000 ms
	cases := []struct {
		name       string
		res        result
		applyErr   error
		state      string // "" means: no finish, a retry is queued
		reason     string
		retryAfter bool
	}{
		{name: "empty-reply retries", res: result{Reason: "empty-reply"}, retryAfter: true},
		{name: "format error retries", res: result{Answered: true, Text: "{"}, applyErr: errFormat, retryAfter: true},
		{name: "wrapped format error retries", res: result{Answered: true, Text: "{"}, applyErr: fmt.Errorf("parse: %w", errFormat), retryAfter: true},
		{name: "auth", res: result{Reason: "api-error", Error: "authentication_failed", Status: 401}, state: stateFailed, reason: reasonAuth},
		{name: "other api error", res: result{Reason: "api-error", Error: "overloaded_error", Status: 529}, state: stateFailed, reason: reasonAPI},
		{name: "aborted at the timeout", res: result{Reason: "aborted", LatencyMS: 30000}, state: stateFailed, reason: reasonTimeout},
		{name: "aborted early", res: result{Reason: "aborted", LatencyMS: 1200}, state: stateFailed, reason: reasonStopped},
		{name: "refused", res: result{Reason: "refused"}, state: stateFailed, reason: reasonRefused},
		{name: "nothing-to-fork is unknown now", res: result{Reason: "nothing-to-fork"}, state: stateFailed, reason: reasonAPI},
		{name: "unknown reason", res: result{Reason: "weird"}, state: stateFailed, reason: reasonAPI},
		{name: "no reason", res: result{}, state: stateFailed, reason: reasonAPI},
	}
	for _, kind := range []string{kindTurnSummary, kindRecap} {
		for _, tc := range cases {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				r := newJCRig(t, 0)
				if tc.applyErr != nil {
					r.kind.applyErr = []error{tc.applyErr}
				}
				r.enq(jcSID, kind, "p")
				j, _ := r.next(jcSID)
				if _, err := r.ch.Result("streamA", j.ID, tc.res); err != nil {
					t.Fatal(err)
				}
				if tc.retryAfter {
					if r.kind.count() != 0 {
						t.Fatalf("ended instead of retrying: %+v", r.kind.finishes)
					}
					// the same kind comes back as attempt 2, at the head
					j2, ok := r.next(jcSID)
					if !ok || j2.Kind != kind || j2.ID == j.ID {
						t.Fatalf("retry %+v %v", j2, ok)
					}
					second := tc.res
					if tc.applyErr != nil {
						r.kind.applyErr = []error{tc.applyErr}
					}
					if _, err := r.ch.Result("streamA", j2.ID, second); err != nil {
						t.Fatal(err)
					}
					if f := r.kind.last(); f.state != stateFailed || f.reason != reasonFormat || f.job.attempt != 2 {
						t.Fatalf("second failure %+v", f)
					}
					if _, ok := r.next(jcSID); ok {
						t.Fatal("a third attempt was queued")
					}
					return
				}
				if f := r.kind.last(); f.state != tc.state || f.reason != tc.reason {
					t.Fatalf("got %+v want %s/%s", f, tc.state, tc.reason)
				}
				if _, ok := r.next(jcSID); ok {
					t.Fatal("a retry was queued")
				}
			})
		}
	}
}

// A retry keeps its place at the head of the session's queue and is not dropped for backlog.
func TestRetryKeepsItsPlaceAndIsNotBacklog(t *testing.T) {
	r := newJCRig(t, 0)
	r.enq(jcSID, kindTurnSummary, 1)
	j, _ := r.next(jcSID)
	for i := 2; i <= 5; i++ {
		r.enq(jcSID, kindTurnSummary, i)
	}
	if _, err := r.ch.Result("streamA", j.ID, result{Reason: "empty-reply"}); err != nil {
		t.Fatal(err)
	}
	j2, ok := r.next(jcSID)
	if !ok || j2.Complete.Prompt != "prompt 1" {
		t.Fatalf("retry not first: %+v", j2)
	}
}

// 9. Stop.
func TestStop_Converges(t *testing.T) {
	r := newJCRig(t, 0)
	r.enq(jcSID, kindTurnSummary, "leased")
	leased, _ := r.next(jcSID)
	r.enq(jcSID, kindRecap, "queued")
	r.ch.Stop()
	byPayload := map[any]jcFinish{}
	for _, f := range r.kind.finishes {
		byPayload[f.job.payload] = f
	}
	if f := byPayload["leased"]; f.state != stateFailed || f.reason != reasonStopped {
		t.Fatalf("leased: %+v", f)
	}
	if f := byPayload["queued"]; f.state != stateSkipped || f.reason != reasonStopped {
		t.Fatalf("queued: %+v", f)
	}
	if _, err := r.ch.Result("streamA", leased.ID, result{Answered: true, Text: "late"}); !errors.Is(err, modevents.ErrNotLeased) {
		t.Fatalf("late result: %v", err)
	}
	if len(r.kind.applies) != 0 {
		t.Fatalf("a late result was applied: %v", r.kind.applies)
	}
	start := time.Now()
	if _, ok := r.ch.Next(context.Background(), "streamA", jcSID, 5*time.Second); ok || time.Since(start) > time.Second {
		t.Fatalf("Next after Stop: ok=%v after %v", ok, time.Since(start))
	}
	if id, why := r.ch.enqueue(jcSID, kindRecap, "x"); id != "" || why != "stopped" {
		t.Fatalf("enqueue after Stop: %q %q", id, why)
	}
	if r.ch.JobWaiting(jcSID) {
		t.Fatal("announced after Stop")
	}
	n := r.kind.count()
	r.ch.reap()
	if r.kind.count() != n {
		t.Fatal("reap wrote after Stop")
	}
}

func TestStop_WakesWaitingPollsAtOnce(t *testing.T) {
	r := newJCRig(t, 0)
	var wg sync.WaitGroup
	got := make(chan bool, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok := r.ch.Next(context.Background(), "streamA", jcSID, 20*time.Second)
			got <- ok
		}()
	}
	time.Sleep(100 * time.Millisecond) // let them park
	start := time.Now()
	r.ch.Stop()
	wg.Wait()
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("polls woke after %v", d)
	}
	close(got)
	for ok := range got {
		if ok {
			t.Fatal("a poll got a job")
		}
	}
}

func TestStop_WaitsForAResultBeingApplied(t *testing.T) {
	r := newJCRig(t, 0)
	started, release := make(chan struct{}), make(chan struct{})
	r.kind.onApply = func() { close(started); <-release }
	r.enq(jcSID, kindRecap, 1)
	j, _ := r.next(jcSID)
	resDone := make(chan error, 1)
	go func() { _, err := r.ch.Result("streamA", j.ID, result{Answered: true, Text: "x"}); resDone <- err }()
	<-started
	stopDone := make(chan struct{})
	go func() { r.ch.Stop(); close(stopDone) }()
	select {
	case <-stopDone:
		t.Fatal("Stop returned while a result was being applied")
	case <-time.After(150 * time.Millisecond):
	}
	if r.kind.count() != 0 {
		t.Fatalf("the lease of a result being applied was ended: %+v", r.kind.finishes)
	}
	close(release)
	select {
	case <-stopDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return after the apply finished")
	}
	if err := <-resDone; err != nil {
		t.Fatal(err)
	}
	if len(r.kind.applies) != 1 {
		t.Fatalf("applies %v", r.kind.applies)
	}
}

// An answer still unstored at Stop ends its row as failed(stopped) and is not retried afterwards.
func TestStop_EndsUnstoredAnswers(t *testing.T) {
	r := newJCRig(t, 0)
	r.kind.applyErr = []error{errors.New("disk full")}
	r.enq(jcSID, kindRecap, 1)
	j, _ := r.next(jcSID)
	_, _ = r.ch.Result("streamA", j.ID, result{Answered: true, Text: "x"})
	r.ch.Stop()
	if f := r.kind.last(); f.state != stateFailed || f.reason != reasonStopped {
		t.Fatalf("%+v", f)
	}
	r.ch.reap()
	if len(r.kind.applies) != 1 {
		t.Fatalf("applied after Stop: %v", r.kind.applies)
	}
}

// The module's reaper goroutine and the channel leave nothing running (run with -race).
func TestRunReaper_EndsWithContext(t *testing.T) {
	r := newJCRig(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.ch.runReaper(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the reaper did not end")
	}
}

// Through the real socket handler: next -> result with the mod unchanged (it only ever sees kind "turn").
func TestJobs_ThroughTheModSocketHandler(t *testing.T) {
	const stream = "Ab3_-xyz09"
	reg := modevents.NewRegistry(time.Now)
	_, _ = reg.Apply(modevents.Batch{V: 1, Stream: stream, Agent: "cc", Caps: []string{modevents.CapWorkbookV2},
		Events: []modevents.Event{{Seq: 1, SID: jcSID, Type: "heartbeat", Data: json.RawMessage(`{}`)}}})
	kind := &jcKind{}
	ch := newChannel(chanDeps{
		capable:  func(sid string) bool { return reg.SessionCapable(sid, modevents.CapWorkbookV2, modevents.CapsFresh) },
		handlers: map[string]kindHandler{kindTurnSummary: kind, kindRecap: kind},
		logf:     func(string, ...any) {},
	})
	m := &Module{jobs: ch}
	svc := jobsService{m: m}
	h := modevents.NewHandler(reg, modevents.WithWorkbook(func() modevents.WorkbookService {
		if !svc.Ready() {
			return nil
		}
		return svc
	}))
	do := func(path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		return rec
	}
	next := fmt.Sprintf(`{"stream":%q,"session_id":%q,"wait_ms":0}`, stream, jcSID)

	if rec := do(modevents.WorkbookNextPath, next); rec.Code != http.StatusNoContent {
		t.Fatalf("empty queue: %d %s", rec.Code, rec.Body)
	}
	if id, why := ch.enqueue(jcSID, kindRecap, "p"); id == "" || why != "" {
		t.Fatalf("enqueue %q %q", id, why)
	}
	rec := do(modevents.WorkbookNextPath, next)
	if rec.Code != 200 {
		t.Fatalf("next: %d %s", rec.Code, rec.Body)
	}
	var env struct {
		Job struct {
			ID       string `json:"id"`
			Kind     string `json:"kind"`
			Complete struct {
				Model  string `json:"model"`
				Prompt string `json:"prompt"`
			} `json:"complete"`
		} `json:"job"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Job.Kind != "turn" || env.Job.Complete.Model != "haiku" || env.Job.Complete.Prompt != "prompt p" {
		t.Fatalf("job %+v", env.Job)
	}
	body := fmt.Sprintf(`{"stream":%q,"job_id":%q,"answered":true,"text":"hello","usage":{"input":1,"output":2,"cache_read":3},"latency_ms":9}`, stream, env.Job.ID)
	if rec := do(modevents.WorkbookResultPath, body); rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"more":false}` {
		t.Fatalf("result: %d %s", rec.Code, rec.Body)
	}
	if len(kind.applies) != 1 || kind.applies[0] != "recap:p:1:hello" {
		t.Fatalf("applies %v", kind.applies)
	}
	if rec := do(modevents.WorkbookResultPath, body); rec.Code != http.StatusConflict {
		t.Fatalf("second result: %d %s", rec.Code, rec.Body)
	}
	// a stopped module is "unavailable" to the socket
	m.mu.Lock()
	m.jobs = nil
	m.mu.Unlock()
	if rec := do(modevents.WorkbookNextPath, next); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("stopped: %d", rec.Code)
	}
}

// The module registers "workbook.jobs" at Init, the service is ready only while the module runs, and Stop stops the
// channel and the reaper.
func TestModule_RegistersJobsServiceAndStopsTheChannel(t *testing.T) {
	m, _, _, _ := newModuleEnv(t)
	svc, ok := m.core.Registry.Get(jobsKey)
	if !ok {
		t.Fatal("workbook.jobs is not registered")
	}
	js, ok := svc.(modevents.WorkbookService)
	if !ok || !svc.(interface{ Ready() bool }).Ready() {
		t.Fatalf("service %T not ready", svc)
	}
	m.mu.Lock()
	ch := m.jobs
	m.mu.Unlock()
	if ch == nil {
		t.Fatal("no channel after Start")
	}
	if _, got := js.NextJob(context.Background(), "streamA", jcSID, 0); got {
		t.Fatal("a job from an empty channel")
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if svc.(interface{ Ready() bool }).Ready() {
		t.Fatal("still ready after Stop")
	}
	if _, err := js.JobResult("streamA", modevents.WorkbookResult{JobID: "dlg-x"}); !errors.Is(err, modevents.ErrNotLeased) {
		t.Fatalf("result after Stop: %v", err)
	}
	if id, why := ch.enqueue(jcSID, kindRecap, 1); id != "" || why == "" {
		t.Fatalf("the stopped channel queued: %q %q", id, why)
	}
}
