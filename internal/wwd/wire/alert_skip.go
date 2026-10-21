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

// AlertSkips remembers the unknown kinds and levels a reader has skipped, so that it says so once for each, not at every read of the list
// (the Purdex daemon reads each host's list every 30 seconds). The zero value is ready for use.
type AlertSkips struct {
	mu   sync.Mutex
	seen map[string]struct{}
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
	s.seen[what] = struct{}{}
	return what, true
}
