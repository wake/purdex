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
}

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
	p.entries[r.ClientID] = e
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
		if e.active && now.Before(e.expires) && match(e) {
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
