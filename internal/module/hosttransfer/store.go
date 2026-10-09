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

	// Pairing entries (QR pairing spec §4): the 16-code cap counts only entries that can still be taken; claimed ones live
	// on as status tombstones until their original expiry, at most maxTombstones, oldest dropped first.
	maxTombstones = 64
	// A claim's failure limiter is per source address; the table of sources is bounded so a flood of addresses cannot grow it.
	maxClaimSources = 4096
)

// alphabet is Crockford base32: no I, L, O, U.
const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var (
	ErrCapacity    = errors.New("hosttransfer: too many live codes")
	ErrUnavailable = errors.New("hosttransfer: could not allocate a code")
	ErrInvalidCode = errors.New("hosttransfer: invalid code")
	ErrRateLimited = errors.New("hosttransfer: rate limited")
	// ErrClaimed is Delete of a pairing whose claim came first (nothing removed).
	ErrClaimed = errors.New("hosttransfer: already claimed")
	// ErrStopped is Redeem on a stopped store. It is not ErrInvalidCode: it
	// does not depend on the code (so it reveals nothing about one), it is
	// not counted as a failure, and it tells the client the relay is going
	// away rather than that it mistyped.
	ErrStopped = errors.New("hosttransfer: stopped")
)

type entry struct {
	payload   json.RawMessage
	pairing   bool      // a pairing entry (spec §4), not a Share-hosts transfer
	claimed   bool      // a claimed pairing: a status tombstone, payload dropped
	claimedAt time.Time // when it was claimed
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
	stopped     bool // permanent once set (Stop)
	failures    int
	windowStart time.Time               // zero = no window open
	claimFails  map[string]*claimWindow // per source address: the claim's own failure limiter (Redeem's is untouched)
	tombs       []tombRef               // claim order, oldest first
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
	return &Store{entries: map[string]entry{}, claimFails: map[string]*claimWindow{}, now: now, gen: gen, afterFunc: after}
}

type claimWindow struct {
	failures int
	start    time.Time
}

type tombRef struct {
	code string
	seq  uint64
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
// rate-limited; maxLive bounds it. A stopped store is ErrUnavailable.
func (s *Store) Create(payload json.RawMessage) (string, time.Time, error) {
	return s.create(payload, codeTTL, false)
}

// CreatePairing parks a pairing entry (spec §4.1) for ttl. Transfers and unclaimed pairings share the 16 live codes.
func (s *Store) CreatePairing(payload json.RawMessage, ttl time.Duration) (string, time.Time, error) {
	return s.create(payload, ttl, true)
}

// takeableLocked counts the entries that can still be taken: tombstones are not among them.
func (s *Store) takeableLocked() int {
	n := 0
	for _, e := range s.entries {
		if !e.claimed {
			n++
		}
	}
	return n
}

func (s *Store) create(payload json.RawMessage, ttl time.Duration, pairing bool) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return "", time.Time{}, ErrUnavailable
	}
	now := s.now()
	s.sweepLocked(now)
	if s.takeableLocked() >= maxLive {
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
		exp := now.Add(ttl)
		s.seq++
		seq := s.seq
		timer := s.afterFunc(ttl, func() { s.expire(code, seq) })
		s.entries[code] = entry{payload: payload, pairing: pairing, expiresAt: exp, seq: seq, timer: timer}
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
// counter, and a miss never touches a stored entry. A stopped store is
// ErrStopped, whatever the code, and counts nothing.
func (s *Store) Redeem(raw string) (json.RawMessage, time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return nil, 0, ErrStopped
	}
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
	if !ok || e.pairing { // a pairing code is not a transfer: a miss, and left as it is
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

// Stop drops every entry, stops its timer and closes the store for good, in
// one critical section: once Stop returns no entry is left and none can be
// added. Module Stop calls it; the daemon stops modules before it shuts
// the HTTP server down, so requests can still arrive afterwards.
func (s *Store) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	s.clearLocked()
}

func (s *Store) clearLocked() {
	for code, e := range s.entries {
		s.dropLocked(code, e)
	}
}

// Claim takes a pairing entry for the phone at source (spec §4.2): one-time, the entry becomes a status tombstone and the
// rows are returned. Its failure limiter is its own, per source address (failLimit per failWindow; over it nothing is taken,
// even a right code), so claim traffic cannot lock out the admin's Redeem and one node can only lock out itself. A transfer
// code, an unknown, expired or already claimed code is ErrInvalidCode and counts as a failure; a transfer is left alone.
func (s *Store) Claim(source, raw string) (json.RawMessage, time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return nil, 0, ErrStopped
	}
	now := s.now()
	s.sweepLocked(now)

	w := s.claimFails[source]
	if w != nil && !now.Before(w.start.Add(failWindow)) {
		delete(s.claimFails, source)
		w = nil
	}
	if w != nil && w.failures >= failLimit {
		return nil, w.start.Add(failWindow).Sub(now), ErrRateLimited
	}
	if w == nil && len(s.claimFails) >= maxClaimSources {
		s.purgeClaimWindowsLocked(now)
		if len(s.claimFails) >= maxClaimSources {
			return nil, failWindow, ErrRateLimited // the table is full of live windows: refuse rather than grow
		}
	}

	code := normalise(raw)
	e, ok := s.entries[code]
	if !ok || !e.pairing || e.claimed {
		if w == nil {
			w = &claimWindow{start: now}
			s.claimFails[source] = w
		}
		w.failures++
		return nil, 0, ErrInvalidCode
	}
	rows := e.payload
	e.payload, e.claimed, e.claimedAt = nil, true, now
	s.entries[code] = e // the timer and expiry stay: the tombstone lives until the original expiry
	s.tombs = append(s.tombs, tombRef{code, e.seq})
	s.trimTombstonesLocked()
	return rows, 0, nil
}

func (s *Store) purgeClaimWindowsLocked(now time.Time) {
	for src, w := range s.claimFails {
		if !now.Before(w.start.Add(failWindow)) {
			delete(s.claimFails, src)
		}
	}
}

// trimTombstonesLocked keeps at most maxTombstones status tombstones, dropping the oldest claimed first.
func (s *Store) trimTombstonesLocked() {
	live := s.tombs[:0]
	for _, t := range s.tombs {
		if e, ok := s.entries[t.code]; ok && e.seq == t.seq && e.claimed {
			live = append(live, t)
		}
	}
	for len(live) > maxTombstones {
		if e, ok := s.entries[live[0].code]; ok && e.seq == live[0].seq {
			s.dropLocked(live[0].code, e)
		}
		live = live[1:]
	}
	s.tombs = live
}

// PairingStatus is the answer of GET /api/host-transfer/pairings/{code}.
type PairingStatus struct {
	Claimed   bool
	ClaimedAt time.Time
	ExpiresAt time.Time
}

// Status reports a pairing entry (live or tombstone); ok is false for a transfer, an unknown, expired or dropped code.
func (s *Store) Status(raw string) (PairingStatus, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.now())
	e, ok := s.entries[normalise(raw)]
	if !ok || !e.pairing {
		return PairingStatus{}, false
	}
	return PairingStatus{Claimed: e.claimed, ClaimedAt: e.claimedAt, ExpiresAt: e.expiresAt}, true
}

// DeletePairing is decided under the same lock as Claim: an unclaimed pairing is removed (nil), a claimed one stays
// (ErrClaimed), anything else — a transfer included — is ErrInvalidCode and untouched.
func (s *Store) DeletePairing(raw string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.now())
	code := normalise(raw)
	e, ok := s.entries[code]
	if !ok || !e.pairing {
		return ErrInvalidCode
	}
	if e.claimed {
		return ErrClaimed
	}
	s.dropLocked(code, e)
	return nil
}
