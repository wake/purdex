package wire

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ParseAlert reads one alert of a list the way a reader that takes later releases' alerts does (S3-3a): the framing, the types of the
// fields it knows and Validate are checked as DecodeLenient does, and a field it does not know is skipped. An alert whose kind or level is
// unknown is returned with what could be read (so the reader can say which) and an error that errors.Is ErrUnknownAlertKind or
// ErrUnknownAlertLevel; any other error is a broken alert, and the reader refuses the list.
func ParseAlert(raw json.RawMessage) (Alert, error) {
	var a Alert
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&a); err != nil {
		return Alert{}, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Alert{}, errors.New("wire: trailing data after JSON value")
	}
	return a, a.Validate()
}

// IsUnknownAlert says err (from ParseAlert or Validate) is only an unknown kind or level.
func IsUnknownAlert(err error) bool {
	return errors.Is(err, ErrUnknownAlertKind) || errors.Is(err, ErrUnknownAlertLevel)
}

// MaxAlertSkips bounds how many different unknown names AlertSkips remembers. A peer that sends a new one at every read must not grow the
// reader or its log without end; once the limit is reached the reader says so once and no more.
const MaxAlertSkips = 64

// AlertSkips remembers the unknown kinds and levels a reader has skipped, so that it says so once for each, not at every read of the list
// (the Purdex daemon reads each host's list every 30 seconds). It remembers at most MaxAlertSkips of them. The zero value is ready for use.
type AlertSkips struct {
	mu       sync.Mutex
	seen     map[string]struct{}
	overflow bool
}

// First says whether this is the first time a was skipped for its unknown kind or level, and what to call it ("kind \"x\"" or "level \"y\"").
func (s *AlertSkips) First(a Alert, err error) (what string, first bool) {
	switch {
	case errors.Is(err, ErrUnknownAlertKind):
		what = fmt.Sprintf("kind %q", a.Kind)
	case errors.Is(err, ErrUnknownAlertLevel):
		what = fmt.Sprintf("level %q", a.Level)
	default:
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = map[string]struct{}{}
	}
	if _, ok := s.seen[what]; ok {
		return what, false
	}
	if len(s.seen) >= MaxAlertSkips {
		if s.overflow {
			return "", false
		}
		s.overflow = true
		return fmt.Sprintf("more than %d different unknown kinds or levels (this and the ones after it are left out, not named)", MaxAlertSkips), true
	}
	s.seen[what] = struct{}{}
	return what, true
}
