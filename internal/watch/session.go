package watch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// The state of starting and waking the watcher on this host (host-load-control spec §12.3–§12.4, plan W3b): which start or wake is
// in progress, and until when a failed one keeps the next from being tried. It is a file, so a daemon that restarts in the middle of
// a start does not start a second watcher, and a failed start is not retried at once by every event.

const (
	// StartCooldown is how long a start or wake that never registered keeps the next one away.
	StartCooldown = 30 * time.Minute
	// RegisterWithin is how long a typed start or wake has to show up as a verified watcher.
	RegisterWithin = 3 * time.Minute
	// StartGrace is how long a caller waits for a start or wake to finish before it is told "in progress" (a peer call is cut off
	// long before RegisterWithin; the job goes on in the background).
	StartGrace = 5 * time.Second
	// MaxWakePrompt bounds the wake prompt with the request's prompt added.
	MaxWakePrompt = 4 << 10
)

// ErrBadPrompt: a wake prompt that is too long, not UTF-8 or holds a control character.
var ErrBadPrompt = errors.New("the prompt must be at most 4 KB of text without control characters")

// CooldownError: a start or wake was refused because an earlier one failed; the next may be tried at Until.
type CooldownError struct{ Until time.Time }

func (e *CooldownError) Error() string {
	return fmt.Sprintf("a start failed recently: no new one until %s", e.Until.Format(time.RFC3339))
}

// InProgress is a start or wake that was begun and has not ended.
type InProgress struct {
	Kind      string    `json:"kind"` // start | wake
	Since     time.Time `json:"since"`
	SessionID string    `json:"session_id,omitempty"` // the session a wake resumes
	Frame     string    `json:"frame,omitempty"`      // the frame the holder had when it began: a start succeeds on a different one
}

// SessionState is the content of session.json.
type SessionState struct {
	InProgress    *InProgress `json:"in_progress,omitempty"`
	CooldownUntil time.Time   `json:"cooldown_until,omitempty"`
}

// SessionStore is session.json, written atomically. A file that cannot be read is set aside as ".corrupt" and reads as nothing in
// progress and no cooldown.
type SessionStore struct {
	mu   sync.Mutex
	path string
	cur  SessionState
}

// OpenSession reads the file at path (a missing file is the empty state; path "" keeps nothing on disk). The error, with a usable
// store, says what was set aside.
func OpenSession(path string) (*SessionStore, error) {
	s := &SessionStore{path: path}
	if path == "" {
		return s, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return s, err
	}
	if err := json.Unmarshal(raw, &s.cur); err != nil {
		_ = os.Rename(path, path+".corrupt")
		s.cur = SessionState{}
		return s, fmt.Errorf("%s unreadable, set aside as .corrupt: %w", path, err)
	}
	return s, nil
}

// Get is a copy of the state.
func (s *SessionStore) Get() SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneSession(s.cur)
}

func cloneSession(st SessionState) SessionState {
	if st.InProgress != nil {
		c := *st.InProgress
		st.InProgress = &c
	}
	return st
}

// Update changes the state with fn and writes it; when the write fails the state stays as it was.
func (s *SessionStore) Update(fn func(*SessionState)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneSession(s.cur)
	fn(&next)
	if s.path != "" {
		b, err := json.Marshal(next)
		if err != nil {
			return err
		}
		if _, err := writeAtomic(s.path, b); err != nil {
			return err
		}
	}
	s.cur = next
	return nil
}

// JoinWakePrompt is the wake prompt followed by the request's prompt, joined by a space (the wake prompt alone when the request has
// none, without a trailing space). The whole is at most 4 KB of UTF-8 without a control character.
func JoinWakePrompt(base, extra string) (string, error) {
	whole := base
	if extra != "" {
		whole = base + " " + extra
	}
	if len(whole) > MaxWakePrompt || !utf8.ValidString(whole) {
		return "", ErrBadPrompt
	}
	for _, r := range whole {
		if unicode.IsControl(r) {
			return "", ErrBadPrompt
		}
	}
	return strings.TrimSpace(whole), nil
}
