package localruns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/locks"
)

const claim = "0123456789abcdef0123456789abcdef"

// fakeQueue records the runs it is asked for; each gets the next id.
type fakeQueue struct {
	mu       sync.Mutex
	n        int
	calls    []wire.LocalRunRequest
	workers  []string
	err      error
	badClaim bool
	// during runs inside RecordLocal, before it answers (a job event faster than the answer).
	during func(id string)
	ended  map[string]bool
	endErr error
	asked  [][]string
	// running is what Running answers (the jobs the queue has as not final); runErr makes it fail
	running   []wire.Job
	runErr    error
	afterList func()
}

func (q *fakeQueue) RecordLocal(_ context.Context, req wire.LocalRunRequest, worker string) (wire.LocalRunResponse, error) {
	q.mu.Lock()
	q.calls = append(q.calls, req)
	q.workers = append(q.workers, worker)
	q.n++
	id := "j_local" + string(rune('0'+q.n))
	err, bad, during := q.err, q.badClaim, q.during
	q.mu.Unlock()
	if err != nil {
		return wire.LocalRunResponse{}, err
	}
	if during != nil {
		during(id)
	}
	if bad {
		return wire.LocalRunResponse{ID: id}, nil
	}
	return wire.LocalRunResponse{ID: id, ClaimID: claim, LeaseS: 60, RenewS: 15}, nil
}

func (q *fakeQueue) Running(context.Context) ([]wire.Job, error) {
	q.mu.Lock()
	jobs, err, hook := q.running, q.runErr, q.afterList
	q.mu.Unlock()
	if hook != nil {
		hook() // runs after the queue answered and before the caller has installed anything
	}
	return jobs, err
}

func (q *fakeQueue) Ended(_ context.Context, ids []string) (map[string]bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.asked = append(q.asked, append([]string(nil), ids...))
	if q.endErr != nil {
		return nil, q.endErr
	}
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = q.ended[id]
	}
	return out, nil
}

func request(t *testing.T, keys ...string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "wire", "testdata", "local_run_request.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m["locks"] = append([]string{}, keys...)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wire.Decode[wire.LocalRunRequest](out); err != nil {
		t.Fatalf("the test request is not valid: %v", err)
	}
	return out
}

func post(s *Service, body []byte) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/local-runs", bytes.NewReader(body)))
	return rec
}

func newService(q Queue) (*Service, *locks.Table) {
	tb := locks.New()
	s := New(tb, q, "mlab")
	s.Logf = func(string, ...any) {}
	if err := s.Recover(context.Background()); err != nil {
		panic(err)
	}
	return s, tb
}

func TestARunIsRecordedAndHoldsItsKeysUnderTheJobId(t *testing.T) {
	q := &fakeQueue{}
	s, tb := newService(q)
	rec := post(s, request(t, "tmux", "port:7860"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var resp wire.LocalRunResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.ID != "j_local1" || resp.ClaimID != claim || resp.LeaseS != 60 || resp.RenewS != 15 {
		t.Fatalf("answer %+v err %v", resp, err)
	}
	if len(q.calls) != 1 || q.workers[0] != "mlab" || len(q.calls[0].Locks) != 2 {
		t.Fatalf("the queue was asked %+v for %v", q.calls, q.workers)
	}
	if o, _ := tb.Holder("tmux"); o != "j_local1" {
		t.Fatalf("holder of tmux = %q", o)
	}
	if got := s.Held(); len(got) != 1 || got[0] != "j_local1" {
		t.Fatalf("held %v", got)
	}
}

func TestAHeldKeyIsA409WithTheKeysAndTheQueueIsNotAsked(t *testing.T) {
	q := &fakeQueue{}
	s, tb := newService(q)
	if _, err := tb.Acquire("j_remote", []string{"tmux", "db"}, nil); err != nil {
		t.Fatal(err)
	}
	rec := post(s, request(t, "tmux", "other", "db"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	busy, err := wire.Decode[wire.LocksBusy](rec.Body.Bytes())
	if err != nil || len(busy.Keys) != 2 || busy.Keys[0] != "db" || busy.Keys[1] != "tmux" ||
		len(busy.Holders) != 2 || busy.Holders[0] != (wire.LockHolder{Key: "db", Job: "j_remote"}) || busy.Holders[1] != (wire.LockHolder{Key: "tmux", Job: "j_remote"}) {
		t.Fatalf("body %s err %v", rec.Body, err)
	}
	if len(q.calls) != 0 {
		t.Fatal("the queue recorded a run whose keys were held")
	}
	if _, ok := tb.Holder("other"); ok {
		t.Fatal("a refused run kept the key that was free")
	}
}

func TestAQueueThatCannotRecordReleasesTheKeysAndAnswers503(t *testing.T) {
	q := &fakeQueue{err: errors.New("database is locked")}
	s, tb := newService(q)
	rec := post(s, request(t, "tmux"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if _, ok := tb.Holder("tmux"); ok || len(s.Held()) != 0 {
		t.Fatal("keys stayed held for a run that was not recorded")
	}
}

func TestAnUnusableClaimReleasesTheKeys(t *testing.T) {
	q := &fakeQueue{badClaim: true}
	s, tb := newService(q)
	rec := post(s, request(t, "tmux"))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if _, ok := tb.Holder("tmux"); ok {
		t.Fatal("keys stayed held")
	}
}

func TestABadBodyIsA400AndTouchesNothing(t *testing.T) {
	q := &fakeQueue{}
	s, tb := newService(q)
	for name, body := range map[string][]byte{
		"not json":      []byte("{"),
		"unknown field": []byte(`{"nope":1}`),
		"empty":         nil,
	} {
		if rec := post(s, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", name, rec.Code)
		}
	}
	var m map[string]any
	_ = json.Unmarshal(request(t), &m)
	m["cacheable"] = true // a fast-lane run is never cacheable
	b, _ := json.Marshal(m)
	if rec := post(s, b); rec.Code != http.StatusBadRequest {
		t.Errorf("cacheable: status %d", rec.Code)
	}
	if len(q.calls) != 0 || len(tb.Held()) != 0 {
		t.Fatal("a refused request recorded or held something")
	}
}

func TestMalformedLockKeysAreA400NotAnOutage(t *testing.T) {
	q := &fakeQueue{}
	s, tb := newService(q)
	for name, keys := range map[string][]string{"empty": {""}, "newline": {"a\nb"}, "repeated": {"k", "k"}} {
		b := request(t)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		m["locks"] = keys
		b, _ = json.Marshal(m)
		if rec := post(s, b); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d: %s", name, rec.Code, rec.Body)
		}
	}
	if len(q.calls) != 0 || len(tb.Held()) != 0 {
		t.Fatal("a malformed request recorded or held something")
	}
}

func TestATerminalLocalEventReleasesTheKeysAndOthersDoNot(t *testing.T) {
	q := &fakeQueue{}
	s, tb := newService(q)
	post(s, request(t, "tmux"))
	ev := func(local bool, st wire.JobState) wire.JobEvent {
		return wire.JobEvent{Job: wire.Job{ID: "j_local1", Local: local, State: st}}
	}
	s.OnJobEvent(ev(true, wire.StateRunning))
	s.OnJobEvent(ev(false, wire.StateFailed)) // a remote job of the same id is not this hold
	if _, ok := tb.Holder("tmux"); !ok {
		t.Fatal("keys were released by an event that does not end the run")
	}
	for _, st := range []wire.JobState{wire.StateSucceeded} {
		s.OnJobEvent(ev(true, st))
	}
	if _, ok := tb.Holder("tmux"); ok || len(s.Held()) != 0 {
		t.Fatal("the end of the run did not release its keys")
	}
	s.OnJobEvent(ev(true, wire.StateSucceeded)) // twice is harmless
}

func TestEveryFinalStateReleases(t *testing.T) {
	for _, st := range []wire.JobState{wire.StateSucceeded, wire.StateFailed, wire.StateCancelled, wire.StateSuperseded, wire.StateExpired} {
		q := &fakeQueue{}
		s, tb := newService(q)
		post(s, request(t, "tmux"))
		s.OnJobEvent(wire.JobEvent{Job: wire.Job{ID: "j_local1", Local: true, State: st}})
		if _, ok := tb.Holder("tmux"); ok {
			t.Errorf("%s did not release", st)
		}
	}
}

func TestAnEventFasterThanTheAnswerReleasesAtOnce(t *testing.T) {
	q := &fakeQueue{}
	s, tb := newService(q)
	q.during = func(id string) { // the job is recorded, ends and is announced before RecordLocal returns
		s.OnJobEvent(wire.JobEvent{Job: wire.Job{ID: id, Local: true, State: wire.StateFailed}})
	}
	rec := post(s, request(t, "tmux"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if _, ok := tb.Holder("tmux"); ok || len(s.Held()) != 0 {
		t.Fatal("keys of a job that had already ended stayed held")
	}
}

func TestReconcileReleasesTheJobsTheQueueSaysAreOver(t *testing.T) {
	q := &fakeQueue{}
	s, tb := newService(q)
	post(s, request(t, "a"))
	post(s, request(t, "b"))
	post(s, request(t, "c"))
	q.ended = map[string]bool{"j_local1": true, "j_local3": true}
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := tb.Holder("a"); ok {
		t.Error("a: not released")
	}
	if _, ok := tb.Holder("b"); !ok {
		t.Error("b: released although the job goes on")
	}
	if _, ok := tb.Holder("c"); ok {
		t.Error("c: not released")
	}
	if got := s.Held(); len(got) != 1 || got[0] != "j_local2" {
		t.Fatalf("held %v", got)
	}
}

func TestReconcileThatCannotAskKeepsEverything(t *testing.T) {
	q := &fakeQueue{}
	s, tb := newService(q)
	post(s, request(t, "a"))
	q.endErr = errors.New("queue busy")
	if err := s.Reconcile(context.Background()); err == nil {
		t.Fatal("the failure was not reported")
	}
	if _, ok := tb.Holder("a"); !ok {
		t.Fatal("keys were released on a failed comparison")
	}
}

func TestReconcileWithNothingHeldDoesNotAskTheQueue(t *testing.T) {
	q := &fakeQueue{}
	s, _ := newService(q)
	if err := s.Reconcile(context.Background()); err != nil || len(q.asked) != 0 {
		t.Fatalf("err %v asked %v", err, q.asked)
	}
}

func TestTwoRunsForOneKeyAtOnceAreOneRecordAndOneRefusal(t *testing.T) {
	q := &fakeQueue{}
	s, _ := newService(q)
	var ok, busy atomic.Int32
	var wg sync.WaitGroup
	body := request(t, "tmux")
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch post(s, body).Code {
			case http.StatusOK:
				ok.Add(1)
			case http.StatusConflict:
				busy.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 || busy.Load() != 7 || len(q.calls) != 1 {
		t.Fatalf("recorded %d, refused %d, queue asked %d times", ok.Load(), busy.Load(), len(q.calls))
	}
}

func TestARunWithoutKeysIsRecordedAndHoldsNothingBack(t *testing.T) {
	q := &fakeQueue{}
	s, _ := newService(q)
	if rec := post(s, request(t)); rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if rec := post(s, request(t)); rec.Code != http.StatusOK {
		t.Fatalf("a second run with no keys: %d", rec.Code)
	}
	s.OnJobEvent(wire.JobEvent{Job: wire.Job{ID: "j_local1", Local: true, State: wire.StateSucceeded}})
	if got := s.Held(); len(got) != 1 || got[0] != "j_local2" {
		t.Fatalf("held %v", got)
	}
}

func TestRegistrationsWaitForTheLocksToBeRebuiltAfterAStart(t *testing.T) {
	q := &fakeQueue{runErr: errors.New("queue not up")}
	tb := locks.New()
	s := New(tb, q, "mlab")
	s.Logf = func(string, ...any) {}
	if rec := post(s, request(t, "tmux")); rec.Code != http.StatusServiceUnavailable || len(q.calls) != 0 {
		t.Fatalf("before the rebuild: status %d, queue asked %d times", rec.Code, len(q.calls))
	}
	if err := s.Recover(context.Background()); err == nil {
		t.Fatal("a failed rebuild was reported as done")
	}
	if rec := post(s, request(t, "tmux")); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("after a failed rebuild: status %d", rec.Code)
	}
	q.runErr = nil
	if err := s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec := post(s, request(t, "tmux")); rec.Code != http.StatusOK {
		t.Fatalf("after the rebuild: status %d: %s", rec.Code, rec.Body)
	}
}

func TestARunningJobKeepsItsKeysAcrossARestart(t *testing.T) {
	// the queue still has j_old running; the daemon starts again with an empty table
	q := &fakeQueue{running: []wire.Job{
		{ID: "j_old", Local: true, State: wire.StateRunning, Locks: []string{"tmux"}},
		{ID: "j_done", Local: true, State: wire.StateSucceeded, Locks: []string{"db"}},
		{ID: "j_remote", Local: false, State: wire.StateRunning, Locks: []string{"net"}},
	}}
	s, tb := newService(q)
	if o, _ := tb.Holder("tmux"); o != "j_old" {
		t.Fatalf("holder of tmux = %q", o)
	}
	if _, ok := tb.Holder("db"); ok {
		t.Error("a job that is over holds keys")
	}
	if _, ok := tb.Holder("net"); ok {
		t.Error("a job that is not a fast-lane run was taken into the fast lane's table")
	}
	if rec := post(s, request(t, "tmux")); rec.Code != http.StatusConflict {
		t.Fatalf("a run for a key of the job that is still going: %d", rec.Code)
	}
	s.OnJobEvent(wire.JobEvent{Job: wire.Job{ID: "j_old", Local: true, State: wire.StateFailed}})
	if rec := post(s, request(t, "tmux")); rec.Code != http.StatusOK {
		t.Fatalf("after its end: %d", rec.Code)
	}
	q.mu.Lock()
	q.running = nil // j_old is over for the queue too
	q.mu.Unlock()
	if err := s.Recover(context.Background()); err != nil { // a second rebuild is harmless
		t.Fatal(err)
	}
}

func TestAnEndWithNoRegistrationInFlightIsNotRemembered(t *testing.T) {
	q := &fakeQueue{}
	s, _ := newService(q)
	s.OnJobEvent(wire.JobEvent{Job: wire.Job{ID: "j_other", Local: true, State: wire.StateFailed}})
	s.mu.Lock()
	n := len(s.early)
	s.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d ends remembered with nobody waiting for them", n)
	}
}

func TestAnEndAnnouncedWhileTheRegistrationIsInFlightSurvivesAReconcile(t *testing.T) {
	q := &fakeQueue{}
	s, tb := newService(q)
	q.during = func(id string) {
		s.OnJobEvent(wire.JobEvent{Job: wire.Job{ID: id, Local: true, State: wire.StateFailed}})
		_ = s.Reconcile(context.Background()) // a comparison in the middle must not forget it
	}
	if rec := post(s, request(t, "tmux")); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if _, ok := tb.Holder("tmux"); ok {
		t.Fatal("the keys of a job that had ended stayed held")
	}
}

func TestRecoverThatCannotAccountForAJobKeepsTheLaneClosed(t *testing.T) {
	// two running jobs share a key (an older start): only one can hold it
	q := &fakeQueue{running: []wire.Job{
		{ID: "j_a", Local: true, State: wire.StateRunning, Locks: []string{"tmux"}},
		{ID: "j_b", Local: true, State: wire.StateRunning, Locks: []string{"tmux", "db"}},
	}}
	tb := locks.New()
	s := New(tb, q, "mlab")
	s.Logf = func(string, ...any) {}
	if err := s.Recover(context.Background()); err == nil {
		t.Fatal("a job whose keys could not be taken was passed over")
	}
	if rec := post(s, request(t, "other")); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("the lane opened with a job unaccounted for: %d", rec.Code)
	}
	// j_a's lease lapses and the queue marks it over: the next rebuild accounts for every running job
	q.mu.Lock()
	q.running = q.running[1:]
	q.mu.Unlock()
	s.OnJobEvent(wire.JobEvent{Job: wire.Job{ID: "j_a", Local: true, State: wire.StateFailed}})
	if err := s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec := post(s, request(t, "tmux")); rec.Code != http.StatusConflict {
		t.Fatalf("j_b's keys must be held: %d", rec.Code)
	}
	if rec := post(s, request(t, "other")); rec.Code != http.StatusOK {
		t.Fatalf("the lane did not open: %d", rec.Code)
	}
}

func TestRecoverDoesNotTakeTheKeysOfAJobThatEndedWhileItListed(t *testing.T) {
	q := &fakeQueue{running: []wire.Job{{ID: "j_old", Local: true, State: wire.StateRunning, Locks: []string{"tmux"}}}}
	tb := locks.New()
	s := New(tb, q, "mlab")
	s.Logf = func(string, ...any) {}
	q.afterList = func() { // the job ends after the queue listed it and before its keys are taken
		s.OnJobEvent(wire.JobEvent{Job: wire.Job{ID: "j_old", Local: true, State: wire.StateSucceeded}})
	}
	if err := s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := tb.Holder("tmux"); ok || len(s.Held()) != 0 {
		t.Fatal("the keys of a job that had ended were rebuilt")
	}
}

func TestARepeatedRebuildThatFailsClosesTheLaneAgain(t *testing.T) {
	q := &fakeQueue{}
	s, tb := newService(q) // the lane is open
	if _, err := tb.Acquire("j_worker", []string{"tmux"}, nil); err != nil {
		t.Fatal(err)
	}
	q.mu.Lock()
	q.running = []wire.Job{{ID: "j_new", Local: true, State: wire.StateRunning, Locks: []string{"tmux"}}}
	q.mu.Unlock()
	if err := s.Recover(context.Background()); err == nil {
		t.Fatal("a job that could not be accounted for was passed over")
	}
	if rec := post(s, request(t, "other")); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("the lane stayed open: %d", rec.Code)
	}
}
