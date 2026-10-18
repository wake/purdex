package jobsmod

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
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

// streamSource plays one prepared stream per Watch call; a stream ends with ErrStreamEnded, and when none is left Watch waits for ctx.
type streamSource struct {
	mu      sync.Mutex
	streams [][]wire.Event
	err     error // when set, every Watch fails with it
	calls   int
}

func (s *streamSource) Watch(ctx context.Context, ready chan<- struct{}, fn func(wire.Event) bool) error {
	s.mu.Lock()
	s.calls++
	err := s.err
	var stream []wire.Event
	more := len(s.streams) > 0
	if more {
		stream, s.streams = s.streams[0], s.streams[1:]
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	for _, ev := range stream {
		if fn(ev) {
			return nil
		}
	}
	if more {
		return client.ErrStreamEnded
	}
	<-ctx.Done()
	return ctx.Err()
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
