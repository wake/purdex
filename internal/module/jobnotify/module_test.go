package jobnotify

import (
	"context"
	"encoding/json"
	"errors"
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
	err  error
}

func (s *sent) send(session, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = append(s.list, session+"|"+text)
	return s.err
}

func (s *sent) texts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.list...)
}

func newMod(t *testing.T, src source, s *sent) *Module {
	t.Helper()
	m := New()
	m.logf = func(string, ...any) {}
	m.now = func() time.Time { return t0 }
	m.dir = t.TempDir()
	m.src, m.send, m.retry = src, s.send, time.Millisecond
	m.store = openStore(filepath.Join(m.dir, "wwd-notified.json"), t0)
	return m
}

func TestAJobEndedIsToldOnceHoweverManyEventsCarryIt(t *testing.T) {
	s := &sent{}
	m := newMod(t, &fakeSource{}, s)
	for i := 0; i < 3; i++ {
		m.onEvent(event(t, job("j_a", wire.StateFailed, t0)))
	}
	got := s.texts()
	if len(got) != 1 || !strings.HasPrefix(got[0], "sess-1|[wwd] lint 失敗（exit 1）（j_a）") {
		t.Fatalf("sent %q", got)
	}
}

func TestEveryTerminalStateIsToldAndOthersAreNot(t *testing.T) {
	s := &sent{}
	m := newMod(t, &fakeSource{}, s)
	for i, st := range []wire.JobState{wire.StateSucceeded, wire.StateFailed, wire.StateCancelled, wire.StateSuperseded, wire.StateExpired} {
		m.onEvent(event(t, job("j_"+string(rune('a'+i)), st, t0)))
	}
	hit := job("j_hit", wire.StateSucceeded, t0)
	hit.Result.CacheHit = true
	m.onEvent(event(t, hit))
	if n := len(s.texts()); n != 6 {
		t.Fatalf("told %d: %q", n, s.texts())
	}
	if !strings.Contains(s.texts()[5], "命中快取") {
		t.Fatalf("cache hit text %q", s.texts()[5])
	}
	for _, st := range []wire.JobState{wire.StateQueued, wire.StateRunning} {
		m.onEvent(event(t, job("j_live_"+string(st), st, t0)))
	}
	local := job("j_local", wire.StateSucceeded, t0)
	local.Local = true
	m.onEvent(event(t, local))
	noSession := job("j_nosess", wire.StateSucceeded, t0)
	noSession.Submitter.Session = ""
	m.onEvent(event(t, noSession))
	m.onEvent(wire.Event{Type: wire.EventFacts, Data: json.RawMessage(`{}`)})
	if n := len(s.texts()); n != 6 {
		t.Fatalf("a job that had not ended, a fast-lane job, a job without a session or another event was told: %q", s.texts())
	}
}

func TestADaemonRestartDoesNotTellAgain(t *testing.T) {
	s := &sent{}
	m := newMod(t, &fakeSource{}, s)
	m.onEvent(event(t, job("j_a", wire.StateSucceeded, t0.Add(time.Minute))))
	m2 := newMod(t, &fakeSource{}, s)
	m2.dir = m.dir
	m2.store = openStore(filepath.Join(m.dir, "wwd-notified.json"), t0.Add(time.Hour))
	m2.onEvent(event(t, job("j_a", wire.StateSucceeded, t0.Add(time.Minute))))
	if n := len(s.texts()); n != 1 {
		t.Fatalf("told %d times: %q", n, s.texts())
	}
	if !m2.store.since().Equal(t0.Add(time.Minute)) {
		t.Fatalf("the catch-up window starts at %v", m2.store.since())
	}
}

func TestAnUnreachableSessionIsNotRetriedForever(t *testing.T) {
	s := &sent{err: errors.New("the watcher has no live inbox")}
	m := newMod(t, &fakeSource{}, s)
	m.onEvent(event(t, job("j_a", wire.StateSucceeded, t0)))
	m.onEvent(event(t, job("j_a", wire.StateSucceeded, t0)))
	if n := len(s.texts()); n != 1 {
		t.Fatalf("tried %d times", n)
	}
}

func TestCatchUpTellsOnlyWhatEndedSinceTheLastOneHandled(t *testing.T) {
	s := &sent{}
	src := &fakeSource{jobs: map[wire.JobState][]wire.Job{
		wire.StateSucceeded: {job("j_old", wire.StateSucceeded, t0.Add(-time.Hour)), job("j_new", wire.StateSucceeded, t0.Add(time.Minute))},
		wire.StateFailed:    {job("j_f", wire.StateFailed, t0.Add(2*time.Minute))},
	}}
	m := newMod(t, src, s)
	m.catchUp(context.Background())
	m.catchUp(context.Background())
	got := strings.Join(s.texts(), "\n")
	if len(s.texts()) != 2 || strings.Contains(got, "j_old") || !strings.Contains(got, "j_new") || !strings.Contains(got, "j_f") {
		t.Fatalf("told %q", s.texts())
	}
}

func TestTheStreamIsOpenedAgainAndTellsWhatArrives(t *testing.T) {
	s := &sent{}
	src := &fakeSource{events: []wire.Event{event(t, job("j_a", wire.StateSucceeded, t0.Add(time.Second)))}, opened: make(chan struct{})}
	m := newMod(t, src, s)
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
