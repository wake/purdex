package jobsmod

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/wire"
)

// streamSource is wwd as the module sees it: one prepared stream per Watch call (a stream ends with ErrStreamEnded, and when none is
// left Watch waits for ctx) and a change log whose cursor is the number of entries read.
type streamSource struct {
	mu      sync.Mutex
	streams [][]wire.Event
	err     error // when set, every Watch fails with it
	calls   int
	log     []wire.Job // the changes, oldest first; a job that changes again is appended again
	epoch   string     // the database's epoch in every list answer ("" as a wwd that predates epochs)
	noEpoch bool       // answer lists without an epoch whatever epoch is set
}

// Watch is wwd's stream: with a stream prepared it opens (ready), plays it and ends with ErrStreamEnded; with none, wwd is as good as
// not running and Watch waits until one is prepared or ctx ends.
func (s *streamSource) Watch(ctx context.Context, ready chan<- struct{}, fn func(wire.Event) bool) error {
	for {
		s.mu.Lock()
		s.calls++
		err := s.err
		var stream []wire.Event
		have := len(s.streams) > 0
		if have {
			stream, s.streams = s.streams[0], s.streams[1:]
		}
		s.mu.Unlock()
		if err != nil {
			return err
		}
		if have {
			if ready != nil {
				close(ready)
			}
			for _, ev := range stream {
				if fn(ev) {
					return nil
				}
			}
			return client.ErrStreamEnded
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
}

func (s *streamSource) JobsAfter(_ context.Context, after string, limit int) (wire.JobsListResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := strconv.Atoi(after)
	if err != nil {
		return wire.JobsListResponse{}, err
	}
	if n > len(s.log) { // a cursor past the head: the database was made again
		return wire.JobsListResponse{}, &client.CursorAheadError{CursorAhead: wire.CursorAhead{Reason: wire.CursorAheadReason, Head: strconv.Itoa(len(s.log)), Epoch: s.epoch}}
	}
	end := len(s.log)
	if limit > 0 && n+limit < end {
		end = n + limit
	}
	epoch := s.epoch
	if s.noEpoch {
		epoch = ""
	}
	return wire.JobsListResponse{Jobs: append([]wire.Job(nil), s.log[n:end]...), Cursor: strconv.Itoa(end), Epoch: epoch}, nil
}

func (s *streamSource) set(f func(*streamSource)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

func (s *streamSource) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func jobEvent(t *testing.T, host, id string, state wire.JobState) wire.Event {
	t.Helper()
	b, err := json.Marshal(wire.JobEvent{Host: host, Job: jobOf(t, id, state)})
	require.NoError(t, err)
	return wire.Event{Type: wire.EventJob, Data: b}
}

// started is a module whose jobs.changed goes to the returned channel, over src, with a short retry.
func started(t *testing.T, src eventSource) (*Module, chan core.HostEvent, func() []string) {
	t.Helper()
	m := New()
	m.src, m.retry = src, time.Millisecond
	out := make(chan core.HostEvent, 16)
	m.emit = func(ev core.HostEvent) { out <- ev }
	var mu sync.Mutex
	var logs []string
	m.logf = func(f string, a ...any) {
		mu.Lock()
		defer mu.Unlock()
		logs = append(logs, f)
	}
	require.NoError(t, m.Start(context.Background()))
	t.Cleanup(func() { m.Stop(context.Background()) })
	return m, out, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), logs...)
	}
}

func next(t *testing.T, out chan core.HostEvent) core.HostEvent {
	t.Helper()
	select {
	case ev := <-out:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no jobs.changed")
		return core.HostEvent{}
	}
}

// A job event of wwd is a jobs.changed of that job: {host, job_id, state} (spec §14a), whatever the state.
func TestEvents_AJobEventIsAJobsChanged(t *testing.T) {
	_, out, _ := started(t, &streamSource{streams: [][]wire.Event{{
		jobEvent(t, "mlab", "j_a", wire.StateQueued), jobEvent(t, "air19", "j_b", wire.StateSucceeded)}}})
	for _, want := range []wire.JobsChanged{{Host: "mlab", JobID: "j_a", State: wire.StateQueued}, {Host: "air19", JobID: "j_b", State: wire.StateSucceeded}} {
		ev := next(t, out)
		assert.Equal(t, "jobs.changed", ev.Type)
		got, err := wire.Decode[wire.JobsChanged]([]byte(ev.Value))
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
}

// What is not a usable job event is not announced: other event types, an event that does not decode, one without a host. The
// valid one after them still is.
func TestEvents_OnlyAUsableJobEventIsAnnounced(t *testing.T) {
	noHost := jobEvent(t, "", "j_x", wire.StateRunning)
	_, out, logs := started(t, &streamSource{streams: [][]wire.Event{{
		{Type: wire.EventFacts, Data: json.RawMessage(`{}`)},
		{Type: wire.EventJob, Data: json.RawMessage(`{"nonsense":true}`)},
		noHost,
		jobEvent(t, "mlab", "j_ok", wire.StateRunning),
	}}})
	ev := next(t, out)
	got, _ := wire.Decode[wire.JobsChanged]([]byte(ev.Value))
	assert.Equal(t, "j_ok", got.JobID)
	select {
	case extra := <-out:
		t.Fatalf("an unusable event was announced: %+v", extra)
	case <-time.After(50 * time.Millisecond):
	}
	assert.NotEmpty(t, logs(), "an event that does not decode or has no host is logged")
}

// The stream ending (wwd restarted) is opened again, and what comes on the new one is announced.
func TestEvents_TheStreamIsOpenedAgainAfterItEnds(t *testing.T) {
	src := &streamSource{streams: [][]wire.Event{{jobEvent(t, "mlab", "j_1", wire.StateRunning)}, {jobEvent(t, "mlab", "j_2", wire.StateRunning)}}}
	_, out, _ := started(t, src)
	assert.Equal(t, "j_1", changedJob(t, next(t, out)))
	assert.Equal(t, "j_2", changedJob(t, next(t, out)))
	assert.GreaterOrEqual(t, src.callCount(), 2)
}

func changedJob(t *testing.T, ev core.HostEvent) string {
	t.Helper()
	c, err := wire.Decode[wire.JobsChanged]([]byte(ev.Value))
	require.NoError(t, err)
	return c.JobID
}

// wwd not running is retried and never fails Start; the same failure is logged once, not at every pause.
func TestEvents_WWDAbsentIsRetriedAndLoggedOnce(t *testing.T) {
	src := &streamSource{err: errors.New("wwd is not running")}
	_, _, logs := started(t, src)
	require.Eventually(t, func() bool { return src.callCount() >= 5 }, 5*time.Second, time.Millisecond)
	assert.Len(t, logs(), 1)
}

// Stop ends the loop even while the stream waits.
func TestEvents_StopEndsTheLoop(t *testing.T) {
	m := New()
	m.src, m.retry = &streamSource{}, time.Millisecond
	require.NoError(t, m.Start(context.Background()))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, m.Stop(ctx))
}

// Through the real core: the frame reaches a host-events subscriber as {"type":"jobs.changed","value":"{...}"}.
func TestEvents_ReachTheRealHostEvents(t *testing.T) {
	t.Setenv("PDX_WWD_SOCK", filepath.Join(t.TempDir(), "none.sock"))
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}, Registry: core.NewServiceRegistry()})
	m := New()
	require.NoError(t, m.Init(c))
	sub := c.Events.AddTestSubscriber()
	defer c.Events.RemoveTestSubscriber(sub)
	m.onEvent(jobEvent(t, "mlab", "j_a", wire.StateFailed))
	select {
	case frame := <-sub.SendCh():
		var ev core.HostEvent
		require.NoError(t, json.Unmarshal(frame, &ev))
		assert.Equal(t, "jobs.changed", ev.Type)
		got, err := wire.Decode[wire.JobsChanged]([]byte(ev.Value))
		require.NoError(t, err)
		assert.Equal(t, wire.JobsChanged{Host: "mlab", JobID: "j_a", State: wire.StateFailed}, got)
	case <-time.After(5 * time.Second):
		t.Fatal("the subscriber heard nothing")
	}
}

const (
	epochA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	epochB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func (m *Module) isBased() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.based
}

// The first read of the daemon's life only takes the cursor: the jobs wwd already has are not news.
func TestCatchUp_TheFirstReadAnnouncesNothing(t *testing.T) {
	src := &streamSource{epoch: epochA, streams: [][]wire.Event{nil}, log: []wire.Job{jobOf(t, "j_old1", wire.StateSucceeded), jobOf(t, "j_old2", wire.StateFailed)}}
	m, out, _ := started(t, src)
	require.Eventually(t, m.isBased, 5*time.Second, time.Millisecond)
	select {
	case ev := <-out:
		t.Fatalf("the history was announced: %+v", ev)
	case <-time.After(30 * time.Millisecond):
	}
}

// wwd's stream does not replay: a job that changed while it was down is announced when the stream is open again (the cursor of the
// first read is where the look starts). One that did not change is not.
func TestCatchUp_AJobThatChangedWhileTheStreamWasDownIsAnnounced(t *testing.T) {
	src := &streamSource{epoch: epochA, log: []wire.Job{jobOf(t, "j_old", wire.StateSucceeded)}, streams: [][]wire.Event{nil}}
	m, out, _ := started(t, src) // the first stream opens and ends at once; the next Watch waits
	require.Eventually(t, m.isBased, 5*time.Second, time.Millisecond)
	src.set(func(s *streamSource) {
		s.log = append(s.log, jobOf(t, "j_gap", wire.StateSucceeded)) // changed while the link was cut
		s.streams = append(s.streams, nil)                            // the stream is opened again
	})
	require.Equal(t, "j_gap", changedJob(t, next(t, out)))
	select {
	case ev := <-out:
		t.Fatalf("a job that did not change was announced: %+v", ev)
	case <-time.After(30 * time.Millisecond):
	}
}

// A different epoch is a wwd.db made again: it is read from the start and its jobs are announced, the newest maxCatchUp at most.
func TestCatchUp_AnotherEpochIsReadFromTheStart(t *testing.T) {
	src := &streamSource{epoch: epochA, log: []wire.Job{jobOf(t, "j_a", wire.StateSucceeded)}, streams: [][]wire.Event{nil}}
	m, out, _ := started(t, src)
	require.Eventually(t, m.isBased, 5*time.Second, time.Millisecond)
	var fresh []wire.Job
	for i := 0; i < maxCatchUp+5; i++ {
		fresh = append(fresh, jobOf(t, "j_n"+strconv.Itoa(i), wire.StateRunning))
	}
	src.set(func(s *streamSource) { s.epoch, s.log = epochB, fresh; s.streams = append(s.streams, nil) })
	var got []string
	for i := 0; i < maxCatchUp; i++ {
		got = append(got, changedJob(t, next(t, out)))
	}
	assert.Equal(t, "j_n5", got[0], "the newest %d of the new database, oldest first", maxCatchUp)
	assert.Equal(t, "j_n"+strconv.Itoa(maxCatchUp+4), got[len(got)-1])
	select {
	case ev := <-out:
		t.Fatalf("more than maxCatchUp announced: %+v", ev)
	case <-time.After(30 * time.Millisecond):
	}
}

// A cursor past the new head (the new wwd.db is shorter) is read from the start too.
func TestCatchUp_ACursorPastTheHeadIsReadFromTheStart(t *testing.T) {
	src := &streamSource{epoch: epochA, streams: [][]wire.Event{nil}}
	src.log = []wire.Job{jobOf(t, "j_1", wire.StateSucceeded), jobOf(t, "j_2", wire.StateSucceeded), jobOf(t, "j_3", wire.StateSucceeded)}
	m, out, _ := started(t, src)
	require.Eventually(t, m.isBased, 5*time.Second, time.Millisecond)
	src.set(func(s *streamSource) {
		s.log = []wire.Job{jobOf(t, "j_new", wire.StateRunning)}
		s.streams = append(s.streams, nil)
	})
	assert.Equal(t, "j_new", changedJob(t, next(t, out)))
}

// A wwd that has lost its epochs, under a known one, cannot be trusted: nothing is announced and the cursor does not move.
func TestCatchUp_NoEpochUnderAKnownOneMovesNothing(t *testing.T) {
	src := &streamSource{epoch: epochA, log: []wire.Job{jobOf(t, "j_a", wire.StateSucceeded)}, streams: [][]wire.Event{nil}}
	m, out, logs := started(t, src)
	require.Eventually(t, m.isBased, 5*time.Second, time.Millisecond)
	src.set(func(s *streamSource) {
		s.noEpoch = true
		s.log = append(s.log, jobOf(t, "j_x", wire.StateSucceeded))
		s.streams = append(s.streams, nil)
	})
	require.Eventually(t, func() bool { return len(logs()) > 0 }, 5*time.Second, time.Millisecond)
	select {
	case ev := <-out:
		t.Fatalf("announced under an untrusted cursor: %+v", ev)
	case <-time.After(30 * time.Millisecond):
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	assert.Equal(t, "1", m.cursor)
}
