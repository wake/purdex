package jobnotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

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

// fakeSource is wwd as the module sees it: an event stream and a change log, whose cursor is the number of entries already read.
type fakeSource struct {
	events   []wire.Event
	log      []wire.Job // the changes, oldest first; a job that changes again is appended again
	opened   chan struct{}
	noCursor bool     // a wwd that does not know ?after=: it answers without a cursor
	asked    []string // the cursors the module asked with
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

func (f *fakeSource) JobsAfter(_ context.Context, after string, limit int) (wire.JobsListResponse, error) {
	f.asked = append(f.asked, after)
	if f.noCursor {
		return wire.JobsListResponse{}, errors.New("wwd answered without a cursor")
	}
	n, err := strconv.Atoi(after)
	if err != nil || n > len(f.log) {
		return wire.JobsListResponse{}, fmt.Errorf("bad cursor %q", after)
	}
	end := len(f.log)
	if limit > 0 && n+limit < end {
		end = n + limit
	}
	return wire.JobsListResponse{Jobs: append([]wire.Job(nil), f.log[n:end]...), Cursor: strconv.Itoa(end)}, nil
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
// ev handles an event the way the module does and runs the delivery pass that its wake-up would start.
func (m *Module) ev(ctx context.Context, e wire.Event) {
	m.onEvent(ctx, e)
	m.deliverPending(ctx, true)
}

func (m *Module) catchUpNow() {
	m.catchUp(context.Background())
	m.deliverPending(context.Background(), true)
}

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
	if _, haveCursor, _ := st.cursor(); !haveCursor {
		if _, ok, _ := st.since(); !ok {
			st.setSince(t0) // the first run's window, like Init
		}
	}
	return m
}

func TestAJobEndedIsToldOnceHoweverManyEventsCarryIt(t *testing.T) {
	s := &sent{}
	m := newMod(t, t.TempDir(), &fakeSource{}, s)
	for i := 0; i < 3; i++ {
		m.ev(context.Background(), event(t, job("j_a", wire.StateFailed, t0)))
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
		m.ev(ctx, event(t, job("j_"+string(rune('a'+i)), st, t0)))
	}
	hit := job("j_hit", wire.StateSucceeded, t0)
	hit.Result.CacheHit = true
	m.ev(ctx, event(t, hit))
	if n := len(s.texts()); n != 6 || !strings.Contains(s.texts()[5], "命中快取") {
		t.Fatalf("told %d: %q", n, s.texts())
	}
	for _, st := range []wire.JobState{wire.StateQueued, wire.StateRunning} {
		m.ev(ctx, event(t, job("j_live_"+string(st), st, t0)))
	}
	noSession := job("j_nosess", wire.StateSucceeded, t0)
	noSession.Submitter.Session = ""
	m.ev(ctx, event(t, noSession))
	m.ev(ctx, wire.Event{Type: wire.EventFacts, Data: json.RawMessage(`{}`)})
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
		m.ev(context.Background(), event(t, j))
	}
	if len(s.texts()) != 0 {
		t.Fatalf("a fast-lane job that its caller reported was told: %q", s.texts())
	}
	l := job("j_lost", wire.StateFailed, t0)
	l.Local, l.Cacheable = true, false
	l.Result.Reason = "lost"
	m.ev(context.Background(), event(t, l))
	if got := s.texts(); len(got) != 1 || !strings.Contains(got[0], "j_lost") || !strings.Contains(got[0], "lost") {
		t.Fatalf("told %q", got)
	}
}

func TestADaemonRestartDoesNotTellAgain(t *testing.T) {
	dir := t.TempDir()
	s := &sent{}
	m := newMod(t, dir, &fakeSource{}, s)
	m.ev(context.Background(), event(t, job("j_a", wire.StateSucceeded, t0.Add(time.Minute))))
	m.store.close()
	m2 := newMod(t, dir, &fakeSource{}, s)
	m2.ev(context.Background(), event(t, job("j_a", wire.StateSucceeded, t0.Add(time.Minute))))
	m2.deliverPending(context.Background(), false)
	if n := len(s.texts()); n != 1 {
		t.Fatalf("told %d times: %q", n, s.texts())
	}
}

func TestASessionWithoutAnInboxYetIsToldWhenItHasOne(t *testing.T) {
	s := &sent{failFor: 2}
	m := newMod(t, t.TempDir(), &fakeSource{}, s)
	ctx := context.Background()
	m.ev(ctx, event(t, job("j_a", wire.StateSucceeded, t0)))
	m.ev(ctx, event(t, job("j_a", wire.StateSucceeded, t0))) // the same event again is not another try
	if st, tries, _ := m.store.status("j_a"); st != statusPending || tries != 1 {
		t.Fatalf("status %s after %d tries", st, tries)
	}
	m.deliverPending(ctx, false)
	m.deliverPending(ctx, false)
	m.deliverPending(ctx, false)
	if st, tries, _ := m.store.status("j_a"); len(s.texts()) != 1 || st != statusSent || tries != 3 {
		t.Fatalf("told %q, status %s after %d tries", s.texts(), st, tries)
	}
}

func TestTriesCarryOverARestartAndTheLastOneGivesUp(t *testing.T) {
	dir := t.TempDir()
	s := &sent{failFor: 1000}
	ctx := context.Background()
	m := newMod(t, dir, &fakeSource{}, s)
	m.ev(ctx, event(t, job("j_a", wire.StateSucceeded, t0))) // try 1
	m.deliverPending(ctx, false)                             // try 2
	for i := 0; i < 5; i++ {                                 // a restart before every try must not start the count over
		m.store.close()
		m = newMod(t, dir, &fakeSource{}, s)
		m.deliverPending(ctx, false)
	}
	st, tries, _ := m.store.status("j_a")
	if st != statusGivenUp || tries != maxTries || 1000-s.failFor != maxTries {
		t.Fatalf("status %s, tries %d, sends %d", st, tries, 1000-s.failFor)
	}
	m.deliverPending(ctx, false)
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
	m.deliverPending(context.Background(), false)
	if st, _, _ := m.store.status("j_a"); st != statusGivenUp || len(s.texts()) != 0 {
		t.Fatalf("status %s, sent %q", st, s.texts())
	}
}

func TestTheFirstCatchUpTellsWhatEndedInTheWindowAndRepeatsNeverSendTwice(t *testing.T) {
	s := &sent{}
	src := &fakeSource{log: []wire.Job{
		job("j_old", wire.StateSucceeded, t0.Add(-3*time.Hour)), job("j_new", wire.StateSucceeded, t0.Add(time.Minute)),
		job("j_edge", wire.StateSucceeded, t0.Add(-30*time.Minute)), // inside the overlap
		job("j_f", wire.StateFailed, t0.Add(2*time.Minute)),
	}}
	m := newMod(t, t.TempDir(), src, s)
	m.catchUpNow()
	m.catchUpNow()
	got := strings.Join(s.texts(), "\n")
	if len(s.texts()) != 3 || strings.Contains(got, "j_old") || !strings.Contains(got, "j_new") || !strings.Contains(got, "j_f") || !strings.Contains(got, "j_edge") {
		t.Fatalf("told %q", s.texts())
	}
	if c, ok, _ := m.store.cursor(); !ok || c != "4" {
		t.Fatalf("cursor %q %v, want 4", c, ok)
	}
	if _, ok, _ := m.store.since(); ok {
		t.Fatal("the first-pass window stayed after a pass that read everything")
	}
	if want := []string{"0", "4"}; strings.Join(src.asked, ",") != strings.Join(want, ",") {
		t.Fatalf("asked with %v, want %v", src.asked, want)
	}
}

// The reason for the cursor (#3251): a job whose worker's clock runs hours behind has a finished time far in the past. Once the first
// pass is over, nothing about the time decides what is new, so it is told.
func TestAJobStampedByASlowClockIsToldAfterTheFirstPass(t *testing.T) {
	s := &sent{}
	src := &fakeSource{log: []wire.Job{job("j_a", wire.StateSucceeded, t0.Add(time.Minute))}}
	m := newMod(t, t.TempDir(), src, s)
	m.catchUpNow()
	src.log = append(src.log, job("j_slow", wire.StateSucceeded, t0.Add(-10*time.Hour))) // ended just now, stamped ten hours back
	m.catchUpNow()
	if got := strings.Join(s.texts(), "\n"); len(s.texts()) != 2 || !strings.Contains(got, "j_slow") {
		t.Fatalf("told %q", s.texts())
	}
}

func TestAJobThatChangesAfterItEndedIsToldOnce(t *testing.T) {
	s := &sent{}
	ended := job("j_a", wire.StateSucceeded, t0.Add(time.Minute))
	src := &fakeSource{log: []wire.Job{ended}}
	m := newMod(t, t.TempDir(), src, s)
	m.catchUpNow()
	src.log = append(src.log, ended) // its log was uploaded after the end: wwd lists the job again
	m.catchUpNow()
	if len(s.texts()) != 1 {
		t.Fatalf("told %q", s.texts())
	}
}

func TestACursorSurvivesARestartAndIsAskedWith(t *testing.T) {
	dir := t.TempDir()
	src := &fakeSource{log: []wire.Job{job("j_a", wire.StateSucceeded, t0.Add(time.Minute))}}
	s := &sent{}
	m := newMod(t, dir, src, s)
	m.catchUpNow()
	m.store.close()
	m2 := newMod(t, dir, src, s)
	src.asked = nil
	m2.catchUpNow()
	if strings.Join(src.asked, ",") != "1" || len(s.texts()) != 1 {
		t.Fatalf("asked with %v, told %q", src.asked, s.texts())
	}
	if _, ok, _ := m2.store.since(); ok {
		t.Fatal("a restart brought the first-pass window back")
	}
}

func TestAWwdWithoutACursorLeavesEverythingWhereItWas(t *testing.T) {
	s := &sent{}
	src := &fakeSource{noCursor: true, log: []wire.Job{job("j_a", wire.StateSucceeded, t0.Add(time.Minute))}}
	m := newMod(t, t.TempDir(), src, s)
	m.catchUpNow()
	if _, ok, _ := m.store.cursor(); ok || len(s.texts()) != 0 {
		t.Fatalf("a cursor was stored or a notice sent: %q", s.texts())
	}
	if _, ok, _ := m.store.since(); !ok {
		t.Fatal("the window was dropped although the pass did not read everything")
	}
}

func TestANoticeNotDeliveredIsStillSentAfterTheCursorMovedPastIt(t *testing.T) {
	dir := t.TempDir()
	src := &fakeSource{log: []wire.Job{job("j_a", wire.StateSucceeded, t0.Add(time.Minute))}}
	s := &sent{failFor: 1}
	m := newMod(t, dir, src, s)
	m.catchUpNow() // the delivery fails; the cursor moves on
	if c, _, _ := m.store.cursor(); c != "1" || len(s.texts()) != 0 {
		t.Fatalf("cursor %q, told %q", c, s.texts())
	}
	m.store.close()
	m2 := newMod(t, dir, &fakeSource{}, s) // a restart; the job is behind the cursor and wwd has nothing new
	m2.now = func() time.Time { return t0.Add(time.Hour) }
	m2.deliverPending(context.Background(), false)
	if len(s.texts()) != 1 {
		t.Fatalf("told %q", s.texts())
	}
}

func TestMoreThanOnePageOfJobsAreNotToldAgain(t *testing.T) {
	s := &sent{}
	var many []wire.Job
	base := job("j_base", wire.StateSucceeded, t0.Add(time.Second))
	for i := 0; i < 5100; i++ {
		j := base
		j.ID = fmt.Sprintf("j_%05d", i)
		many = append(many, j)
	}
	src := &fakeSource{log: many}
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
	m.catchUpNow() // a reconnect lists the same backlog again, in six pages
	m.deliverPending(context.Background(), false)
	if n := len(s.texts()); n != 0 {
		t.Fatalf("told %d jobs that were already told", n)
	}
	if want := "0,1000,2000,3000,4000,5000"; strings.Join(src.asked, ",") != want {
		t.Fatalf("asked with %v, want %s", src.asked, want)
	}
	if c, _, _ := mem.cursor(); c != "5100" {
		t.Fatalf("cursor %q", c)
	}
}

func TestOldRowsAreDroppedByAgeAndPendingOnesAreKept(t *testing.T) {
	s := &sent{failFor: 2} // neither is delivered
	m := newMod(t, t.TempDir(), &fakeSource{}, s)
	m.enqueue(job("j_old", wire.StateSucceeded, t0))
	m.enqueue(job("j_wait", wire.StateSucceeded, t0))
	m.deliverPending(context.Background(), true) // both fail (failFor) and stay pending
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

func TestAFailedReadLeavesTheCursorAndTheWindowWhereTheyWere(t *testing.T) {
	s := &sent{}
	src := &failingSource{fail: true, fakeSource: fakeSource{log: []wire.Job{
		job("j_ok", wire.StateSucceeded, t0.Add(time.Minute)), job("j_f", wire.StateFailed, t0.Add(time.Minute)),
	}}}
	m := newMod(t, t.TempDir(), src, s)
	m.catchUpNow()
	if _, ok, _ := m.store.cursor(); ok || len(s.texts()) != 0 {
		t.Fatalf("a cursor was stored, told %q", s.texts())
	}
	if _, ok, _ := m.store.since(); !ok {
		t.Fatal("the window was dropped by a pass that failed")
	}
	src.fail = false
	m.catchUpNow()
	if c, _, _ := m.store.cursor(); len(s.texts()) != 2 || c != "2" {
		t.Fatalf("cursor %q, told %q", c, s.texts())
	}
}

type failingSource struct {
	fakeSource
	fail bool
}

func (f *failingSource) JobsAfter(ctx context.Context, after string, limit int) (wire.JobsListResponse, error) {
	if f.fail {
		return wire.JobsListResponse{}, errors.New("wwd busy")
	}
	return f.fakeSource.JobsAfter(ctx, after, limit)
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

func (b *blockingJobs) JobsAfter(context.Context, string, int) (wire.JobsListResponse, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return wire.JobsListResponse{Cursor: "0"}, nil
}

type breakingSource struct {
	fakeSource
	m *Module
}

// JobsAfter lists a job and, as it does, makes the outbox unwritable.
func (b *breakingSource) JobsAfter(ctx context.Context, after string, limit int) (wire.JobsListResponse, error) {
	b.m.store.db.Exec(`DROP TABLE notices`)
	return b.fakeSource.JobsAfter(ctx, after, limit)
}

func TestACatchUpThatCannotQueueAJobKeepsItsCursor(t *testing.T) {
	src := &breakingSource{fakeSource: fakeSource{log: []wire.Job{job("j_a", wire.StateSucceeded, t0.Add(time.Minute))}}}
	m := newMod(t, t.TempDir(), src, &sent{})
	src.m = m
	m.catchUp(context.Background())
	if _, ok, _ := m.store.cursor(); ok {
		t.Fatal("the cursor moved although a job was not queued")
	}
	if _, ok, _ := m.store.since(); !ok {
		t.Fatal("the window was dropped although a job was not queued")
	}
}

// metaBreakingSource makes the window unwritable while the last page is read.
type metaBreakingSource struct {
	fakeSource
	m *Module
}

func (b *metaBreakingSource) JobsAfter(ctx context.Context, after string, limit int) (wire.JobsListResponse, error) {
	b.m.store.db.Exec(`DROP TABLE meta`)
	return b.fakeSource.JobsAfter(ctx, after, limit)
}

// The cursor of the last page and the end of the first-pass window are one commit: if the window cannot be ended, the cursor does not
// move either, so no restart can find a cursor with the window still in force (it would skip a job stamped by a slow clock).
func TestTheLastCursorAndTheEndOfTheWindowAreOneCommit(t *testing.T) {
	src := &metaBreakingSource{fakeSource: fakeSource{log: []wire.Job{job("j_a", wire.StateSucceeded, t0.Add(time.Minute))}}}
	m := newMod(t, t.TempDir(), src, &sent{})
	src.m = m
	m.catchUp(context.Background())
	if c, ok, _ := m.store.cursor(); ok {
		t.Fatalf("the cursor %q was committed although the window could not be ended", c)
	}
}

func TestStopGivesUpAtItsDeadlineWhenAListIgnoresCancellation(t *testing.T) {
	src := &blockingJobs{release: make(chan struct{}), entered: make(chan struct{})}
	m := newMod(t, t.TempDir(), src, &sent{})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-src.entered
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := m.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop = %v", err)
	}
	// the core calls Close after a Stop that timed out: with the loop still stuck, the database must stay open under it
	closed := make(chan error, 1)
	go func() { closed <- m.Close() }()
	time.Sleep(100 * time.Millisecond)
	if _, _, err := m.store.since(); err != nil {
		t.Fatalf("the database was closed under a running loop: %v", err)
	}
	close(src.release)
	if err := <-closed; err != nil {
		t.Fatalf("Close after the loops ended: %v", err)
	}
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
	m.deliverPending(context.Background(), true) // the delivery loop's pass after the wake-up
	if n := len(s.texts()); n != 1 {
		t.Fatalf("told %d: %q", n, s.texts())
	}
}

func TestASlowSendDoesNotHoldTheEventStream(t *testing.T) {
	block := make(chan struct{})
	m := newMod(t, t.TempDir(), &fakeSource{}, &sent{})
	m.send = func(ctx context.Context, _, _ string) error {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	loop := make(chan struct{})
	go func() { m.redoLoop(ctx); close(loop) }()
	m.onEvent(ctx, event(t, job("j_a", wire.StateSucceeded, t0)))
	time.Sleep(50 * time.Millisecond) // the loop is now inside the blocked send
	returned := make(chan struct{})
	go func() { m.onEvent(ctx, event(t, job("j_b", wire.StateSucceeded, t0))); close(returned) }()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("an event waited for a send that was blocked")
	}
	cancel()
	select {
	case <-loop:
	case <-time.After(5 * time.Second):
		t.Fatal("the delivery loop did not end with its context")
	}
	close(block)
}

func TestTheModuleDoesNotStartWithoutAWindowItCanWrite(t *testing.T) {
	m := New()
	m.dbPath = t.TempDir() // a directory: the database cannot be opened
	m.src = &fakeSource{}
	m.send = (&sent{}).send
	if err := m.Init(nil); err == nil {
		t.Fatal("Init succeeded without a database")
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
