package nex

import (
	"context"
	"sync"
	"time"
)

// PR #1586 C2: after a successful resume the terminal's SessionStart hook
// may not have recorded its frame yet, so an immediate second request could
// pass the owner check and type a second `claude --resume S`. Two guards:
// a visibility barrier (wait for the frame) and a short recent-resume
// marker (covers the barrier timing out).

const (
	defaultOwnerVisiblePoll    = 100 * time.Millisecond
	defaultOwnerVisibleTimeout = 3 * time.Second
	recentResumeTTL            = 10 * time.Second
)

// recentResumeSet: session id -> the nowMs() at which it was resumed.
// The zero value is ready to use.
type recentResumeSet struct {
	mu sync.Mutex
	at map[string]int64
}

func (m *Module) markRecentResume(sid string) {
	if sid == "" {
		return
	}
	s := &m.recentResumes
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.at == nil {
		s.at = map[string]int64{}
	}
	now := nowMs()
	s.prune(now)
	s.at[sid] = now
}

// recentlyResumed: sid was resumed less than recentResumeTTL ago.
func (m *Module) recentlyResumed(sid string) bool {
	s := &m.recentResumes
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(nowMs())
	_, ok := s.at[sid]
	return ok
}

// prune drops the expired entries; the caller holds s.mu.
func (s *recentResumeSet) prune(now int64) {
	for sid, t := range s.at {
		if now-t >= recentResumeTTL.Milliseconds() {
			delete(s.at, sid)
		}
	}
}

// awaitOwnerVisible polls until S has a verified terminal frame, or
// ownerVisibleTimeout passes. A timeout is not an error: the marker covers
// the gap. handler names the caller for the log.
func (m *Module) awaitOwnerVisible(parent context.Context, handler, sid string) {
	poll := m.ownerVisiblePoll
	if poll <= 0 {
		poll = defaultOwnerVisiblePoll
	}
	deadline := time.Now().Add(m.ownerVisibleTimeout)
	for {
		ctx, cancel := detachedContext(parent, m.engineOpTimeout)
		terms, err := m.terminals.LiveBySessionID(ctx, "cc", sid)
		cancel()
		if err == nil {
			for _, t := range terms {
				if t.Verified {
					return
				}
			}
		}
		if !time.Now().Add(poll).Before(deadline) {
			m.logf("nex: %s %s: terminal owner not yet visible after %s; relying on the recent-resume marker", handler, sid, m.ownerVisibleTimeout)
			return
		}
		time.Sleep(poll)
	}
}

// afterResume runs once a resume succeeded: mark S, then wait for the
// terminal to become visible as its owner.
func (m *Module) afterResume(parent context.Context, handler, sid string) {
	m.markRecentResume(sid)
	m.awaitOwnerVisible(parent, handler, sid)
}
