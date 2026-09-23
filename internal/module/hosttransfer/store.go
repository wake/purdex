// Package hosttransfer is the relay for host transfer codes (spec §6): a
// client parks a list of hosts under a short code, another client redeems
// the code once and receives the list. Everything lives in memory only and
// nothing here logs a code or a payload.
package hosttransfer

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	codeTTL    = 10 * time.Minute
	maxLive    = 16
	failLimit  = 10
	failWindow = 60 * time.Second
	genTries   = 5
	codeLen    = 8
)

// alphabet is Crockford base32: no I, L, O, U.
const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var (
	ErrCapacity    = errors.New("hosttransfer: too many live codes")
	ErrUnavailable = errors.New("hosttransfer: could not allocate a code")
	ErrInvalidCode = errors.New("hosttransfer: invalid code")
	ErrRateLimited = errors.New("hosttransfer: rate limited")
)

type entry struct {
	payload   json.RawMessage
	expiresAt time.Time
	seq       uint64  // unique per Create: a late timer spares a newer entry under the same code
	timer     stopper // drops the entry at expiry even if no request ever sweeps
}

// Store is the in-memory code → payload table.
//
// An entry leaves memory at the latest when its TTL runs out (spec §6.1):
// each one arms a timer that deletes it, and every Create/Redeem also
// sweeps expired entries as a second line of defence.
type Store struct {
	mu          sync.Mutex
	entries     map[string]entry // key: canonical code
	seq         uint64
	failures    int
	windowStart time.Time // zero = no window open
	now         func() time.Time
	gen         func() (string, error)
	afterFunc   func(time.Duration, func()) stopper
}

// NewStore returns an empty store on the wall clock and crypto/rand codes.
func NewStore() *Store { return newStore(time.Now, generateCode) }

func newStore(now func() time.Time, gen func() (string, error)) *Store {
	return newStoreWith(now, gen, realAfterFunc)
}

func newStoreWith(now func() time.Time, gen func() (string, error), after func(time.Duration, func()) stopper) *Store {
	return &Store{entries: map[string]entry{}, now: now, gen: gen, afterFunc: after}
}

// stopper is the part of *time.Timer the store uses; tests inject fakes.
type stopper interface{ Stop() bool }

func realAfterFunc(d time.Duration, f func()) stopper { return time.AfterFunc(d, f) }

// generateCode draws 40 bits from crypto/rand and spells them as 8 Crockford
// base32 characters.
func generateCode() (string, error) {
	var b [5]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return encodeCode(b), nil
}

// encodeCode maps each 5-bit group of b, most significant first, to one
// alphabet character: 32 symbols for 5 bits, so no modulo bias.
func encodeCode(b [5]byte) string {
	var v uint64
	for _, x := range b {
		v = v<<8 | uint64(x)
	}
	out := make([]byte, codeLen)
	for i := codeLen - 1; i >= 0; i-- {
		out[i] = alphabet[v&31]
		v >>= 5
	}
	return string(out)
}

// normalise is the Crockford-style reading of a typed code: upper-case,
// dashes and whitespace dropped, I/L read as 1 and O as 0. Anything else is
// kept as typed and simply fails to match.
func normalise(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range strings.ToUpper(raw) {
		switch {
		case r == '-' || unicode.IsSpace(r):
			continue
		case r == 'I' || r == 'L':
			b.WriteByte('1')
		case r == 'O':
			b.WriteByte('0')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sweepLocked drops every expired entry. Caller holds mu.
func (s *Store) sweepLocked(now time.Time) {
	for code, e := range s.entries {
		if !now.Before(e.expiresAt) {
			s.dropLocked(code, e)
		}
	}
}

// dropLocked deletes e and stops its timer. Caller holds mu.
func (s *Store) dropLocked(code string, e entry) {
	if e.timer != nil {
		e.timer.Stop()
	}
	delete(s.entries, code)
}

// expire is an entry's timer: it drops the entry parked under code, but
// only if it is still the entry the timer was armed for.
func (s *Store) expire(code string, seq uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[code]; ok && e.seq == seq {
		delete(s.entries, code)
	}
}

// Create parks payload under a fresh code for codeTTL. It is not
// rate-limited; maxLive bounds it.
func (s *Store) Create(payload json.RawMessage) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.sweepLocked(now)
	if len(s.entries) >= maxLive {
		return "", time.Time{}, ErrCapacity
	}
	for i := 0; i < genTries; i++ {
		code, err := s.gen()
		if err != nil {
			return "", time.Time{}, ErrUnavailable
		}
		if _, live := s.entries[code]; live {
			continue
		}
		exp := now.Add(codeTTL)
		s.seq++
		seq := s.seq
		timer := s.afterFunc(codeTTL, func() { s.expire(code, seq) })
		s.entries[code] = entry{payload: payload, expiresAt: exp, seq: seq, timer: timer}
		return code, exp, nil
	}
	return "", time.Time{}, ErrUnavailable
}

// Redeem hands out the payload parked under raw and deletes it. Sweep, rate
// limit, lookup and delete are one critical section (spec §6.3), so of
// concurrent redeems of one code exactly one wins. Every miss — unknown,
// expired, used or malformed — is ErrInvalidCode and counts as a failure;
// the 10th failure in a window still answers ErrInvalidCode, and every
// redeem after it, right code or wrong, answers ErrRateLimited until the
// window (fixed from the first failure) ends. A success does not reset the
// counter, and a miss never touches a stored entry.
func (s *Store) Redeem(raw string) (json.RawMessage, time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.sweepLocked(now)

	if !s.windowStart.IsZero() {
		end := s.windowStart.Add(failWindow)
		if !now.Before(end) {
			s.failures = 0
			s.windowStart = time.Time{}
		} else if s.failures >= failLimit {
			return nil, end.Sub(now), ErrRateLimited
		}
	}

	code := normalise(raw)
	e, ok := s.entries[code]
	if !ok {
		if s.windowStart.IsZero() {
			s.windowStart = now
		}
		s.failures++
		return nil, 0, ErrInvalidCode
	}
	s.dropLocked(code, e)
	return e.payload, 0, nil
}

// Clear drops every entry and stops its timer.
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearLocked()
}

func (s *Store) clearLocked() {
	for code, e := range s.entries {
		s.dropLocked(code, e)
	}
}
