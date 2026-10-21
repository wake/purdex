package push

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/wake/purdex/internal/push"
)

// maxPresenceBody caps PUT /api/push/presence (spec §5.4); maxPresenceEntries caps how many windows are remembered.
const (
	maxPresenceBody    = 16 << 10
	maxPresenceEntries = 64
)

// Presence is what the Mac App windows have reported (spec §5.4, R6): which sessions a window with the user in front
// of it shows. In memory only: a daemon restart forgets it, and a Mac re-sends within 20 s. The bounds keep a buggy
// client from growing memory; they are not an access control.
type Presence struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]presenceEntry // by client id
}

type presenceEntry struct {
	active  bool
	codes   map[string]struct{}
	names   map[string]struct{}
	expires time.Time

	// v2 (push-away-presence spec §4.1): the machine's own idle / lock state as of receivedAt. A v2 entry answers
	// AtComputer and never feeds the legacy session match.
	v2          bool
	idleMs      int64
	locked      bool
	lockedMs    int64
	awayAfterMs int64
	receivedAt  time.Time
}

// lockedAwayMs is how long after the screen lock the user counts as away (spec §2.2); fixed.
const lockedAwayMs = 60_000

func NewPresence(now func() time.Time) *Presence {
	if now == nil {
		now = time.Now
	}
	return &Presence{now: now, entries: map[string]presenceEntry{}}
}

// Put records one window's report (already validated). Expired entries go first; a new client at the cap replaces
// the one that expires soonest; a known client replaces only itself.
func (p *Presence) Put(r push.PresenceRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	p.sweepLocked(now)
	if _, known := p.entries[r.ClientID]; !known && len(p.entries) >= maxPresenceEntries {
		var soonestID string
		var soonest time.Time
		for id, e := range p.entries {
			if soonestID == "" || e.expires.Before(soonest) {
				soonestID, soonest = id, e.expires
			}
		}
		delete(p.entries, soonestID)
	}
	e := presenceEntry{active: r.Active, codes: map[string]struct{}{}, names: map[string]struct{}{}, expires: now.Add(time.Duration(r.TTLMs) * time.Millisecond)}
	for _, s := range r.Sessions {
		e.codes[s.Code] = struct{}{}
		if s.Name != "" {
			e.names[s.Name] = struct{}{}
		}
	}
	if r.IsV2() {
		e.v2, e.idleMs, e.locked, e.lockedMs, e.awayAfterMs, e.receivedAt = true, *r.IdleMs, *r.Locked, *r.LockedMs, *r.AwayAfterMs, now
	}
	p.entries[r.ClientID] = e
}

// AtComputer: some unexpired v2 entry says the person is at its machine now. The report is as of its receipt, so the
// time since then counts as more idle (or more locked). Locked: the lock is younger than a minute; otherwise: the idle
// time is under that Mac's own away-after.
func (p *Presence) AtComputer() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.atComputerLocked(p.now())
}

func (p *Presence) atComputerLocked(now time.Time) bool {
	for _, e := range p.entries {
		if !e.v2 || !now.Before(e.expires) {
			continue
		}
		elapsed := now.Sub(e.receivedAt).Milliseconds()
		if elapsed < 0 {
			elapsed = 0
		}
		if e.locked {
			if e.lockedMs+elapsed < lockedAwayMs {
				return true
			}
		} else if e.idleMs+elapsed < e.awayAfterMs {
			return true
		}
	}
	return false
}

// PresenceState is AtComputer with the difference between "away" and "nobody says anything" (push.PresenceStateReader): known is true when at
// least one unexpired v2 entry exists, and atComputer is then AtComputer's answer. A v1 entry is not a reading of the machine.
func (p *Presence) PresenceState() (atComputer, known bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	for _, e := range p.entries {
		if e.v2 && now.Before(e.expires) {
			return p.atComputerLocked(now), true
		}
	}
	return false, false
}

// V2Counts is the number of unexpired v2 entries and whether any says the person is at the computer: numbers only.
func (p *Presence) V2Counts() (v2Entries, atComputer int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	for _, e := range p.entries {
		if e.v2 && now.Before(e.expires) {
			v2Entries++
		}
	}
	if p.atComputerLocked(now) {
		atComputer = 1
	}
	return v2Entries, atComputer
}

func (p *Presence) sweepLocked(now time.Time) {
	for id, e := range p.entries {
		if !now.Before(e.expires) {
			delete(p.entries, id)
		}
	}
}

// Len is the number of entries held (expired ones go on the next Put).
func (p *Presence) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}

// Counts is the number of unexpired windows and how many of them say the user is there: numbers only (no client id, no
// session name), for /api/info, so an operator can see that the Macs are reporting.
func (p *Presence) Counts() (entries, active int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	for _, e := range p.entries {
		if !now.Before(e.expires) {
			continue
		}
		entries++
		if e.active {
			active++
		}
	}
	return entries, active
}

// ShowsCode: some unexpired, active window shows the session with this code (an agent event, §5.4).
func (p *Presence) ShowsCode(code string) bool {
	return code != "" && p.shows(func(e presenceEntry) bool { _, ok := e.codes[code]; return ok })
}

// ShowsName: the same, by tmux session name (an approval's origin, §5.4).
func (p *Presence) ShowsName(name string) bool {
	return name != "" && p.shows(func(e presenceEntry) bool { _, ok := e.names[name]; return ok })
}

func (p *Presence) shows(match func(presenceEntry) bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	for _, e := range p.entries {
		if !e.v2 && e.active && now.Before(e.expires) && match(e) {
			return true
		}
	}
	return false
}

// handlePresence is PUT /api/push/presence.
func (m *Module) handlePresence(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPresenceBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_body")
		return
	}
	if !utf8.Valid(body) {
		writeError(w, http.StatusBadRequest, "invalid_utf8")
		return
	}
	var req push.PresenceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	m.pres.Put(req)
	w.WriteHeader(http.StatusNoContent)
}
