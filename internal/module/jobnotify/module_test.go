package jobnotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/wire"
)

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func job(id string, state wire.JobState, finished time.Time) wire.Job {
	b, err := os.ReadFile(filepath.Join("..", "..", "wwd", "wire", "testdata", "job_queued.json"))
	if err != nil {
		panic(err)
	}
	j, err := wire.Decode[wire.Job](b)
	if err != nil {
		panic(err)
	}
	j.ID, j.State, j.Task, j.Submitter.Session = id, state, "lint", "sess-1"
	if state.Terminal() {
		code := 0
		if state == wire.StateFailed {
			code = 1
		}
		j.Claim = nil
		j.Result = &wire.Result{Log: id + "/log", Host: "mlab", StartedAt: &finished, FinishedAt: &finished,
			Summary: &wire.Summary{Failures: []wire.Failure{}, Tail: []string{}}}
		if state == wire.StateSucceeded || state == wire.StateFailed {
			j.Result.Exit = &code
		}
	}
	return j
}

func event(t *testing.T, j wire.Job) wire.Event {
	t.Helper()
	b, err := json.Marshal(wire.JobEvent{Host: "mlab", Job: j})
	if err != nil {
		t.Fatal(err)
	}
	return wire.Event{Type: wire.EventJob, Data: b}
}

type fakeSource struct {
	events []wire.Event
	jobs   map[wire.JobState][]wire.Job
	opened chan struct{}
}

func (f *fakeSource) Watch(ctx context.Context, ready chan<- struct{}, fn func(wire.Event) bool) error {
	if ready != nil {
		close(ready)
	}
	for _, ev := range f.events {
		if fn(ev) {
			return nil
		}
	}
	if f.opened != nil {
		close(f.opened)
	}
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeSource) Jobs(_ context.Context, q client.JobsQuery) ([]wire.Job, error) {
	return f.jobs[q.State], nil
}

type sent struct {
	mu   sync.Mutex
	list []string
	// failFor is how many of the next sends fail (the session has no inbox yet)
	failFor int
}

func (s *sent) send(_ context.Context, session, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failFor > 0 {
		s.failFor--
		return errors.New("no live inbox yet")
	}
	s.list = append(s.list, session+"|"+text)
	return nil
}

func (s *sent) texts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.list...)
}

// newMod is a module over a database file in dir (the same dir for a "restarted" module).
func newMod(t *testing.T, dir string, src source, s *sent) *Module {
	t.Helper()
	m := New()
	m.logf = func(string, ...any) {}
	m.now = func() time.Time { return t0 }
	m.src, m.send, m.retry, m.redo = src, s.send, time.Millisecond, time.Millisecond
	st, err := openStore(filepath.Join(dir, "jobnotify.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.close() })
	m.store = st
	if _, ok, _ := st.since(); !ok {
		st.setSince(t0)
	}
	return m
}

func TestAJobEndedIsToldOnceHoweverManyEventsCarryIt(t *testing.T) {
	s := &sent{}
	m := newMod(t, t.TempDir(), &fakeSource{}, s)
	for i := 0; i < 3; i++ {
		m.onEvent(context.Background(), event(t, job("j_a", wire.StateFailed, t0)))
	}
	got := s.texts()
	if len(got) != 1 || !strings.HasPrefix(got[0], "sess-1|[wwd] lint 失敗（exit 1）（j_a）") {
		t.Fatalf("sent %q", got)
	}
}

func TestEveryTerminalStateIsToldAndOthersAreNot(t *testing.T) {
	s := &sent{}
	m := newMod(t, t.TempDir(), &fakeSource{}, s)
	ctx := context.Background()
	for i, st := range []wire.JobState{wire.StateSucceeded, wire.StateFailed, wire.StateCancelled, wire.StateSuperseded, wire.StateExpired} {
		m.onEvent(ctx, event(t, job("j_"+string(rune('a'+i)), st, t0)))
	}
	hit := job("j_hit", wire.StateSucceeded, t0)
	hit.Result.CacheHit = true
	m.onEvent(ctx, event(t, hit))
	if n := len(s.texts()); n != 6 || !strings.Contains(s.texts()[5], "命中快取") {
		t.Fatalf("told %d: %q", n, s.texts())
	}
	for _, st := range []wire.JobState{wire.StateQueued, wire.StateRunning} {
		m.onEvent(ctx, event(t, job("j_live_"+string(st), st, t0)))
	}
	noSession := job("j_nosess", wire.StateSucceeded, t0)
	noSession.Submitter.Session = ""
	m.onEvent(ctx, event(t, noSession))
	m.onEvent(ctx, wire.Event{Type: wire.EventFacts, Data: json.RawMessage(`{}`)})
	if n := len(s.texts()); n != 6 {
		t.Fatalf("a job that had not ended, a job without a session or another event was told: %q", s.texts())
	}
}

func TestAFastLaneJobIsToldOnlyWhenItWasLost(t *testing.T) {
	s := &sent{}
	m := newMod(t, t.TempDir(), &fakeSource{}, s)
	for _, st := range []wire.JobState{wire.StateSucceeded, wire.StateFailed, wire.StateCancelled} {
		j := job("j_local_"+string(st), st, t0)
		j.Local, j.Cacheable = true, false
		m.onEvent(context.Background(), event(t, j))
	}
	if len(s.texts()) != 0 {
		t.Fatalf("a fast-lane job that its caller reported was told: %q", s.texts())
	}
	l := job("j_lost", wire.StateFailed, t0)
	l.Local, l.Cacheable = true, false
	l.Result.Reason = "lost"
	m.onEvent(context.Background(), event(t, l))
	if got := s.texts(); len(got) != 1 || !strings.Contains(got[0], "j_lost") || !strings.Contains(got[0], "lost") {
		t.Fatalf("told %q", got)
	}
}

func TestADaemonRestartDoesNotTellAgain(t *testing.T) {
	dir := t.TempDir()
	s := &sent{}
	m := newMod(t, dir, &fakeSource{}, s)
	m.onEvent(context.Background(), event(t, job("j_a", wire.StateSucceeded, t0.Add(time.Minute))))
	m.store.close()
	m2 := newMod(t, dir, &fakeSource{}, s)
	m2.onEvent(context.Background(), event(t, job("j_a", wire.StateSucceeded, t0.Add(time.Minute))))
	m2.deliverPending(context.Background())
	if n := len(s.texts()); n != 1 {
		t.Fatalf("told %d times: %q", n, s.texts())
	}
}

func TestASessionWithoutAnInboxYetIsToldWhenItHasOne(t *testing.T) {
	s := &sent{failFor: 2}
	m := newMod(t, t.TempDir(), &fakeSource{}, s)
	ctx := context.Background()
	m.onEvent(ctx, event(t, job("j_a", wire.StateSucceeded, t0)))
	m.onEvent(ctx, event(t, job("j_a", wire.StateSucceeded, t0))) // the same event again is not another try
	if st, tries, _ := m.store.status("j_a"); st != statusPending || tries != 1 {
		t.Fatalf("status %s after %d tries", st, tries)
	}
	m.deliverPending(ctx)
	m.deliverPending(ctx)
	m.deliverPending(ctx)
	if st, tries, _ := m.store.status("j_a"); len(s.texts()) != 1 || st != statusSent || tries != 3 {
		t.Fatalf("told %q, status %s after %d tries", s.texts(), st, tries)
	}
}

func TestTriesCarryOverARestartAndTheLastOneGivesUp(t *testing.T) {
	dir := t.TempDir()
	s := &sent{failFor: 1000}
	ctx := context.Background()
	m := newMod(t, dir, &fakeSource{}, s)
	m.onEvent(ctx, event(t, job("j_a", wire.StateSucceeded, t0))) // try 1
	m.deliverPending(ctx)                                         // try 2
	for i := 0; i < 5; i++ {                                      // a restart before every try must not start the count over
		m.store.close()
		m = newMod(t, dir, &fakeSource{}, s)
		m.deliverPending(ctx)
	}
	st, tries, _ := m.store.status("j_a")
	if st != statusGivenUp || tries != maxTries || 1000-s.failFor != maxTries {
		t.Fatalf("status %s, tries %d, sends %d", st, tries, 1000-s.failFor)
	}
	m.deliverPending(ctx)
	if 1000-s.failFor != maxTries {
		t.Fatal("a notice that was given up was tried again")
	}
}

func TestACrashBetweenTheStoredTryAndItsResultStillCountsTheTry(t *testing.T) {
	dir := t.TempDir()
	s := &sent{}
	m := newMod(t, dir, &fakeSource{}, s)
	m.enqueue(job("j_a", wire.StateSucceeded, t0))
	for i := 0; i < maxTries; i++ {
		if _, err := m.store.beginTry("j_a", t0); err != nil { // tries stored, the send never came back
			t.Fatal(err)
		}
	}
	m.deliverPending(context.Background())
	if st, _, _ := m.store.status("j_a"); st != statusGivenUp || len(s.texts()) != 0 {
		t.Fatalf("status %s, sent %q", st, s.texts())
	}
}

func TestCatchUpTellsWhatEndedInTheWindowAndOverlapsNeverSendTwice(t *testing.T) {
	s := &sent{}
	src := &fakeSource{jobs: map[wire.JobState][]wire.Job{
		wire.StateSucceeded: {job("j_old", wire.StateSucceeded, t0.Add(-time.Hour)), job("j_new", wire.StateSucceeded, t0.Add(time.Minute)),
			job("j_edge", wire.StateSucceeded, t0.Add(-5*time.Minute))}, // inside the overlap
		wire.StateFailed: {job("j_f", wire.StateFailed, t0.Add(2*time.Minute))},
	}}
	m := newMod(t, t.TempDir(), src, s)
	m.catchUp(context.Background())
	m.catchUp(context.Background())
	got := strings.Join(s.texts(), "\n")
	if len(s.texts()) != 3 || strings.Contains(got, "j_old") || !strings.Contains(got, "j_new") || !strings.Contains(got, "j_f") || !strings.Contains(got, "j_edge") {
		t.Fatalf("told %q", s.texts())
	}
}

func TestANoticeNotDeliveredIsStillSentAfterTheWindowMovedPastIt(t *testing.T) {
	dir := t.TempDir()
	src := &fakeSource{jobs: map[wire.JobState][]wire.Job{wire.StateSucceeded: {job("j_a", wire.StateSucceeded, t0.Add(time.Minute))}}}
	s := &sent{failFor: 1}
	m := newMod(t, dir, src, s)
	now := t0.Add(time.Hour)
	m.now = func() time.Time { return now }
	m.catchUp(context.Background()) // the delivery fails; the window moves on to now
	if since, _, _ := m.store.since(); !since.Equal(now) || len(s.texts()) != 0 {
		t.Fatalf("window %v, told %q", since, s.texts())
	}
	m.store.close()
	m2 := newMod(t, dir, &fakeSource{}, s) // a restart; the job is far behind the window and the list has nothing new
	m2.now = func() time.Time { return now.Add(time.Hour) }
	m2.deliverPending(context.Background())
	if len(s.texts()) != 1 {
		t.Fatalf("told %q", s.texts())
	}
}

func TestMoreThanFiveThousandJobsAreNotToldAgain(t *testing.T) {
	s := &sent{}
	var many []wire.Job
	base := job("j_base", wire.StateSucceeded, t0.Add(time.Second))
	for i := 0; i < 5100; i++ {
		j := base
		j.ID = fmt.Sprintf("j_%05d", i)
		many = append(many, j)
	}
	src := &fakeSource{jobs: map[wire.JobState][]wire.Job{wire.StateSucceeded: many}}
	m := newMod(t, t.TempDir(), src, s)
	mem, err := openStore(":memory:") // 15 000 statements: a file would take half a minute under -race
	if err != nil {
		t.Fatal(err)
	}
	defer mem.close()
	mem.setSince(t0)
	m.store = mem
	// 5100 jobs already told, written in one transaction
	tx, err := mem.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range many {
		if _, err := tx.Exec(`INSERT INTO notices(job_id, session, text, state, finished_at, status, tries, updated_at) VALUES(?, 's', 't', 'succeeded', ?, 'sent', 1, ?)`,
			j.ID, t0.UnixMilli(), t0.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	m.catchUp(context.Background()) // a reconnect lists the same backlog again
	m.deliverPending(context.Background())
	if n := len(s.texts()); n != 0 {
		t.Fatalf("told %d jobs that were already told", n)
	}
}

func TestOldRowsAreDroppedByAgeAndPendingOnesAreKept(t *testing.T) {
	s := &sent{failFor: 2} // neither is delivered
	m := newMod(t, t.TempDir(), &fakeSource{}, s)
	m.enqueue(job("j_old", wire.StateSucceeded, t0))
	m.enqueue(job("j_wait", wire.StateSucceeded, t0))
	m.deliverOne(context.Background(), "j_old") // fails (failFor), stays pending
	m.deliverOne(context.Background(), "j_wait")
	m.store.settle("j_old", statusSent, "", t0)
	if err := m.store.prune(t0.Add(keep + time.Hour)); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := m.store.status("j_old"); st != "" {
		t.Fatalf("an old sent row stayed: %s", st)
	}
	if st, _, _ := m.store.status("j_wait"); st == "" {
		t.Fatal("a row was dropped that is not old enough")
	}
}

type failingSource struct {
	fakeSource
	failState wire.JobState
}

func (f *failingSource) Jobs(ctx context.Context, q client.JobsQuery) ([]wire.Job, error) {
	if q.State == f.failState {
		return nil, errors.New("wwd busy")
	}
	return f.fakeSource.Jobs(ctx, q)
}

func TestAFailedListLeavesTheCatchUpWindowWhereItWas(t *testing.T) {
	s := &sent{}
	src := &failingSource{failState: wire.StateFailed, fakeSource: fakeSource{jobs: map[wire.JobState][]wire.Job{
		wire.StateSucceeded: {job("j_ok", wire.StateSucceeded, t0.Add(time.Minute))},
		wire.StateFailed:    {job("j_f", wire.StateFailed, t0.Add(time.Minute))},
	}}}
	m := newMod(t, t.TempDir(), src, s)
	now := t0.Add(time.Hour)
	m.now = func() time.Time { return now }
	m.catchUp(context.Background())
	if since, _, _ := m.store.since(); !since.Equal(t0) || len(s.texts()) != 0 {
		t.Fatalf("window %v, told %q", since, s.texts())
	}
	src.failState = ""
	m.catchUp(context.Background())
	if since, _, _ := m.store.since(); len(s.texts()) != 2 || !since.Equal(now) {
		t.Fatalf("window %v, told %q", since, s.texts())
	}
}

func TestStopWaitsForACatchUpInFlight(t *testing.T) {
	s := &sent{}
	src := &blockingJobs{release: make(chan struct{}), entered: make(chan struct{})}
	m := newMod(t, t.TempDir(), src, s)
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-src.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no catch-up began")
	}
	stopped := make(chan struct{})
	go func() { m.Stop(context.Background()); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("Stop returned while a catch-up was still running")
	case <-time.After(100 * time.Millisecond):
	}
	close(src.release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after the catch-up ended")
	}
}

type blockingJobs struct {
	fakeSource
	release chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (b *blockingJobs) Jobs(context.Context, client.JobsQuery) ([]wire.Job, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return nil, nil
}

func TestTheStreamIsOpenedAgainAndTellsWhatArrives(t *testing.T) {
	s := &sent{}
	src := &fakeSource{events: []wire.Event{event(t, job("j_a", wire.StateSucceeded, t0.Add(time.Second)))}, opened: make(chan struct{})}
	m := newMod(t, t.TempDir(), src, s)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.run(ctx); close(done) }()
	select {
	case <-src.opened:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream was never opened")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not end with its context")
	}
	if n := len(s.texts()); n != 1 {
		t.Fatalf("told %d: %q", n, s.texts())
	}
}

func TestTextNamesWhyAJobFailedOrWasSuperseded(t *testing.T) {
	f := job("j_f", wire.StateFailed, t0)
	f.Result.Reason = "timeout"
	if got := Text(f); !strings.Contains(got, "失敗（exit 1）：timeout") || !strings.HasSuffix(got, "pdx jobs show j_f") {
		t.Fatalf("text %q", got)
	}
	by := "j_new"
	sup := job("j_s", wire.StateSuperseded, t0)
	sup.Result.SupersededBy = &by
	if got := Text(sup); !strings.Contains(got, "被取代（j_new）") {
		t.Fatalf("text %q", got)
	}
}
