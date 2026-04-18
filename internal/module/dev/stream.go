package dev

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"sync"
)

type BuildEventType string

const (
	BuildEventPhase  BuildEventType = "phase"
	BuildEventStdout BuildEventType = "stdout"
	BuildEventStderr BuildEventType = "stderr"
	BuildEventDone   BuildEventType = "done"
	BuildEventError  BuildEventType = "error"
)

type BuildEvent struct {
	Type  BuildEventType `json:"type"`
	Phase string         `json:"phase,omitempty"`
	Line  string         `json:"line,omitempty"`
	Error string         `json:"error,omitempty"`
}

// BuildSession broadcasts build events to any number of subscribers and keeps
// a full replay buffer so late subscribers can catch up from the beginning.
type BuildSession struct {
	mu     sync.Mutex
	events []BuildEvent
	subs   map[chan BuildEvent]struct{}
	closed bool
}

func newBuildSession() *BuildSession {
	return &BuildSession{subs: make(map[chan BuildEvent]struct{})}
}

// append stores the event in the replay buffer and fans out to current
// subscribers. Slow subscribers (full buffer) drop the live event; they can
// still recover it from the replay on the next subscribe.
func (s *BuildSession) append(ev BuildEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.events = append(s.events, ev)
	for ch := range s.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// subscribe returns a live channel, a snapshot of past events, and an
// unsubscribe function. If the session has already finished, the channel is
// returned already-closed.
func (s *BuildSession) subscribe() (<-chan BuildEvent, []BuildEvent, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	replay := append([]BuildEvent(nil), s.events...)
	ch := make(chan BuildEvent, 64)
	if s.closed {
		close(ch)
		return ch, replay, func() {}
	}
	s.subs[ch] = struct{}{}
	unsub := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
	}
	return ch, replay, unsub
}

// finish closes the session: appends no further events, closes every live
// subscriber channel. Callers should append the terminal event (done/error)
// before calling finish.
func (s *BuildSession) finish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	for ch := range s.subs {
		close(ch)
	}
	s.subs = nil
}

// streamCmd runs a command inside `dir`, emits a phase-start event, then one
// stdout/stderr event per line until the process exits. It does not emit
// done/error events — the caller decides those based on overall pipeline
// outcome.
func streamCmd(ctx context.Context, session *BuildSession, phase, dir, name string, args ...string) error {
	session.append(BuildEvent{Type: BuildEventPhase, Phase: phase})

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdout.Close()
		return err
	}
	if err := cmd.Start(); err != nil {
		// cmd.Wait() closes these on the happy path; on Start() failure we
		// must close them manually or the pipe read-ends leak.
		stdout.Close()
		stderr.Close()
		return err
	}

	var wg sync.WaitGroup
	scan := func(r io.Reader, typ BuildEventType) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			session.append(BuildEvent{Type: typ, Line: sc.Text()})
		}
	}
	wg.Add(2)
	go scan(stdout, BuildEventStdout)
	go scan(stderr, BuildEventStderr)
	wg.Wait()

	return cmd.Wait()
}
