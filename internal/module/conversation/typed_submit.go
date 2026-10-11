package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/promptq"
	"github.com/wake/purdex/internal/tmux"
)

// POST /api/sessions/{code}/submit (K-1a-2, spec docs/specs/2026-10-11-keystroke-submit-spec.md §3): put the message into
// Claude Code's composer and press Enter, so Claude Code's own queue owns it. With the Purdex mod: the mod fills the composer
// (promptq fill), the daemon checks it is still fresh and presses Enter through the generation / pane-mode guard. Without a
// mod, after 10 s of no owner: blind typing, marked mode "keys".

// TypedQueue is the part of promptq.Queue the route drives.
type TypedQueue interface {
	Lookup(clientMsgID string) (promptq.Result, bool)
	SubmitFill(ctx context.Context, sessionID, clientMsgID, text string) (promptq.Result, error)
	ClearFilled(ctx context.Context, clientMsgID string) (promptq.Result, error)
	MarkEntering(id string) error
	MarkEntered(id string) error
	MarkEnterRefused(id string) error
	MarkUnknown(id string) error
	MarkGaveUp(id string) error
	HasOwner(sessionID string) bool
	NoOwnerFor(sessionID string) time.Duration
}

// SessionLookup finds a tmux session by code (session.SessionProvider).
type SessionLookup interface {
	GetSession(code string) (*session.SessionInfo, error)
}

// PaneKeySender is the guarded Enter / typing (tmux.RealExecutor).
type PaneKeySender interface {
	SendKeysIfInstanceAndMode(sessionID, expectedInstance, paneID string, keys ...string) error
}

type typedDeps struct {
	q        TypedQueue
	sessions SessionLookup
	owners   agent.OwnerResolver
	keys     PaneKeySender
}

// typedTuning: zero values are the spec's; tests shorten them.
type typedTuning struct {
	lockWait, poll, finaliseFor, keysAfter, keyGap time.Duration
	now                                            func() time.Time
}

const (
	enterFreshness = 1500 * time.Millisecond // C1: the fill result may be at most this old when Enter goes out
	cleanupTimeout = promptq.DefaultWait + 2*time.Second
	maxKeysLedger  = 1024
)

func (m *Module) tuning() typedTuning {
	t := m.tune
	if t.lockWait <= 0 {
		t.lockWait = 2 * time.Second
	}
	if t.poll <= 0 {
		t.poll = 25 * time.Millisecond
	}
	if t.finaliseFor <= 0 {
		t.finaliseFor = promptq.DefaultHandTimeout + 5*time.Second
	}
	if t.keysAfter <= 0 {
		t.keysAfter = 10 * time.Second
	}
	if t.keyGap < 0 {
		t.keyGap = 0
	} else if t.keyGap == 0 {
		t.keyGap = 150 * time.Millisecond
	}
	if t.now == nil {
		t.now = time.Now
	}
	return t
}

// typedDeps: the injected set (tests), else the daemon's services.
func (m *Module) typedDeps() *typedDeps {
	if m.td != nil {
		return m.td
	}
	if m.core == nil || m.core.Registry == nil {
		return nil
	}
	d := &typedDeps{}
	var ok bool
	if svc, found := m.core.Registry.Get(promptq.Key); found {
		d.q, ok = svc.(TypedQueue)
	}
	if !ok {
		return nil
	}
	svc, found := m.core.Registry.Get(session.RegistryKey)
	if !found {
		return nil
	}
	if d.sessions, ok = svc.(SessionLookup); !ok {
		return nil
	}
	if svc, found = m.core.Registry.Get(agent.OwnerResolverKey); !found {
		return nil
	}
	if d.owners, ok = svc.(agent.OwnerResolver); !ok {
		return nil
	}
	if d.keys, ok = m.core.Tmux.(PaneKeySender); !ok {
		return nil
	}
	return d
}

// sidLocks is the per-conversation lock the typed route and the older conversation submit route share (M1): one send at a
// time per Claude Code session, a bounded wait, never a hang.
type sidLocks struct {
	mu sync.Mutex
	m  map[string]*sidLock
}

type sidLock struct {
	ch   chan struct{}
	refs int
}

func (l *sidLocks) acquire(ctx context.Context, key string, wait time.Duration) (release func(), ok bool) {
	l.mu.Lock()
	if l.m == nil {
		l.m = map[string]*sidLock{}
	}
	e := l.m[key]
	if e == nil {
		e = &sidLock{ch: make(chan struct{}, 1)}
		l.m[key] = e
	}
	e.refs++
	l.mu.Unlock()
	drop := func() {
		l.mu.Lock()
		if e.refs--; e.refs == 0 {
			delete(l.m, key)
		}
		l.mu.Unlock()
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case e.ch <- struct{}{}:
		return func() { <-e.ch; drop() }, true
	case <-t.C:
	case <-ctx.Done():
	}
	drop()
	return nil, false
}

// keysLedger remembers the ids typed blind (no mod, so no promptq row): a repeat never types again. It is in memory only:
// a daemon restart forgets it (and, unlike the promptq ledger, there is no tombstone).
type keysLedger struct {
	mu sync.Mutex
	m  map[string]keysRow
}

type keysRow struct {
	handed bool
	at     time.Time
}

func (k *keysLedger) get(id string) (keysRow, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	r, ok := k.m[id]
	return r, ok
}

func (k *keysLedger) put(id string, r keysRow) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil {
		k.m = map[string]keysRow{}
	}
	for key, old := range k.m {
		if len(k.m) >= maxKeysLedger || time.Since(old.at) > promptq.LedgerTTL {
			delete(k.m, key)
		}
	}
	r.at = time.Now()
	k.m[id] = r
}

func (k *keysLedger) forget(id string) {
	k.mu.Lock()
	delete(k.m, id)
	k.mu.Unlock()
}

type sessionSubmitRequest struct {
	Text                 string `json:"text"`
	ClientMsgID          string `json:"client_msg_id"`
	ExpectedTmuxInstance string `json:"expected_tmux_instance"`
}

type typedAnswer struct {
	Outcome string `json:"outcome"`
	Mode    string `json:"mode,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Error   string `json:"error,omitempty"`
}

func writeTyped(w http.ResponseWriter, status int, a typedAnswer) {
	b, _ := json.Marshal(a)
	writeJSON(w, status, b)
}

func refuse(w http.ResponseWriter, status int, reason string) {
	writeTyped(w, status, typedAnswer{Outcome: "refused", Reason: reason, Error: reason})
}

// guardReason maps a declined guard to the refusal it is; ok is false for any other error (tmux could not be reached).
func guardReason(err error) (string, bool) {
	switch {
	case errors.Is(err, tmux.ErrPaneBusy):
		return "pane_busy", true
	case errors.Is(err, tmux.ErrGenerationMismatch):
		return "generation", true
	case errors.Is(err, tmux.ErrPaneMismatch):
		return "pane_mismatch", true
	}
	return "", false
}

// typedCtx is what one request decided before it started to send.
type typedCtx struct {
	td       *typedDeps
	id, text string
	expected string
	tmuxID   string
	owner    agent.PaneOwner
}

func (m *Module) handleSessionSubmit(w http.ResponseWriter, r *http.Request) {
	td := m.typedDeps()
	if td == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	var in sessionSubmitRequest
	if !decodeBody(w, r, &in) {
		return
	}
	switch {
	case !clientMsgIDRe.MatchString(in.ClientMsgID):
		writeError(w, http.StatusBadRequest, "bad_client_msg_id")
		return
	case !tmux.ValidInstance(in.ExpectedTmuxInstance):
		writeError(w, http.StatusBadRequest, "bad_expected_tmux_instance")
		return
	}
	text, code := sanitizePrompt(in.Text) // the one sanitiser; a leading "/" or "!" is refused in both modes
	if code != "" {
		writeError(w, http.StatusBadRequest, code)
		return
	}
	scode := r.PathValue("code")
	info, err := td.sessions.GetSession(scode)
	switch {
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal")
		return
	case info == nil:
		writeError(w, http.StatusNotFound, "session_not_found")
		return
	}
	owner, found, err := td.owners.ResolveSessionOwner(r.Context(), scode)
	switch {
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "owner_lookup_failed")
		return
	case !found || owner.AgentType != "cc" || owner.SessionID == "" || owner.TmuxPaneID == "":
		refuse(w, http.StatusConflict, "not_claude")
		return
	}
	tune := m.tuning()
	release, ok := m.locks.acquire(r.Context(), owner.SessionID, tune.lockWait)
	if !ok {
		refuse(w, http.StatusTooManyRequests, "busy")
		return
	}
	defer release()

	if rec, ok := td.q.Lookup(in.ClientMsgID); ok { // at-most-once: a recorded id is answered, never typed again
		replayTyped(w, rec)
		return
	}
	if rec, ok := m.keysSeen.get(in.ClientMsgID); ok {
		if rec.handed {
			writeTyped(w, http.StatusOK, typedAnswer{Outcome: "handed", Mode: "keys"})
		} else {
			writeTyped(w, http.StatusOK, typedAnswer{Outcome: "unknown", Mode: "keys"})
		}
		return
	}
	if info.TmuxInstance != in.ExpectedTmuxInstance {
		refuse(w, http.StatusConflict, "generation")
		return
	}
	light := m.lightOf(owner)
	if light == "waiting" {
		refuse(w, http.StatusConflict, "waiting")
		return
	}
	c := &typedCtx{td: td, id: in.ClientMsgID, text: text, expected: in.ExpectedTmuxInstance, tmuxID: info.TmuxID, owner: owner}
	if td.q.HasOwner(owner.SessionID) {
		m.fillSubmit(w, r, c, tune)
		return
	}
	m.keysSubmit(w, c, light, tune)
}

// lightOf is the frame's current light: the cheap read when there is one, else what the owner lookup saw.
func (m *Module) lightOf(o agent.PaneOwner) string {
	if m.light != nil {
		if st, ok := m.light.LightStatus(o.SessionID, o.FrameID); ok {
			return st
		}
	}
	return o.Status
}

// replayTyped answers a recorded row: the state, never a second typing.
func replayTyped(w http.ResponseWriter, rec promptq.Result) {
	switch rec.Phase {
	case promptq.PhaseEntered:
		writeTyped(w, http.StatusOK, typedAnswer{Outcome: "handed", Mode: "fill"})
	case promptq.PhaseCleared:
		reason := rec.Reason
		if reason == "" {
			reason = "cleared"
		}
		refuse(w, http.StatusConflict, reason)
	default: // queued / filled / entering / unknown: it may or may not be in the composer or sent
		writeTyped(w, http.StatusOK, typedAnswer{Outcome: "unknown", Mode: "fill"})
	}
}

func (m *Module) fillSubmit(w http.ResponseWriter, r *http.Request, c *typedCtx, tune typedTuning) {
	td := c.td
	res, err := td.q.SubmitFill(r.Context(), c.owner.SessionID, c.id, c.text)
	switch {
	case errors.Is(err, promptq.ErrBusy):
		refuse(w, http.StatusTooManyRequests, "busy")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	switch res.Status {
	case promptq.Accepted:
		m.enter(w, c, res, tune)
	case promptq.Dropped:
		refuse(w, http.StatusConflict, res.Reason) // nothing was typed, or the mod cleaned it: no Enter
	case promptq.Busy:
		refuse(w, http.StatusTooManyRequests, "busy")
	case promptq.NoMod:
		refuse(w, http.StatusConflict, "no_owner_yet")
	case promptq.Timeout:
		refuse(w, http.StatusConflict, "mod_timeout") // never handed out: nothing typed, safe to send again
	default: // unknown: our text may be in the composer
		if res.Reason == "client_gone" || r.Context().Err() != nil {
			go m.finalise(td, c.id, tune)
		} else {
			m.cleanup(td, c.id)
		}
		writeTyped(w, http.StatusOK, typedAnswer{Outcome: "unknown", Mode: "fill"})
	}
}

// enter: the fill is in the composer. C1: only a fresh result and a light that is still not waiting get an Enter; the
// `entering` mark is written before the keys go out (the at-most-once line).
func (m *Module) enter(w http.ResponseWriter, c *typedCtx, res promptq.Result, tune typedTuning) {
	td := c.td
	if res.ReceivedAt <= 0 || tune.now().Sub(time.UnixMilli(res.ReceivedAt)) > enterFreshness {
		m.cleanup(td, c.id)
		refuse(w, http.StatusConflict, "stale")
		return
	}
	if m.lightOf(c.owner) == "waiting" {
		m.cleanup(td, c.id)
		refuse(w, http.StatusConflict, "waiting")
		return
	}
	if err := td.q.MarkEntering(c.id); err != nil {
		refuse(w, http.StatusConflict, "conflict")
		return
	}
	err := td.keys.SendKeysIfInstanceAndMode(c.tmuxID, c.expected, c.owner.TmuxPaneID, tmux.KeyEnter)
	if err == nil {
		_ = td.q.MarkEntered(c.id)
		writeTyped(w, http.StatusOK, typedAnswer{Outcome: "handed", Mode: "fill"})
		return
	}
	// tmux declined (or could not be reached): nothing was sent, so the row steps back and the composer is cleaned
	_ = td.q.MarkEnterRefused(c.id)
	m.cleanup(td, c.id)
	if reason, ok := guardReason(err); ok {
		refuse(w, http.StatusConflict, reason)
		return
	}
	writeError(w, http.StatusBadGateway, "tmux_error")
}

// cleanup clears our text out of the composer (best effort, one retry) and always lets the row reach an end state: when both
// tries fail the row is settled as cleanup_gave_up, so it stops counting toward the open typed rows.
func (m *Module) cleanup(td *typedDeps, id string) {
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		res, err := td.q.ClearFilled(ctx, id)
		cancel()
		if err == nil && (res.Status == promptq.Accepted || res.Reason == "not_clearable") {
			return
		}
	}
	_ = td.q.MarkGaveUp(id)
}

// finalise is what a caller that went away leaves behind: it waits (bounded) for the fill to settle and, if our text landed
// in the composer and no Enter will ever be sent for it, cleans it up.
func (m *Module) finalise(td *typedDeps, id string, tune typedTuning) {
	deadline := time.Now().Add(tune.finaliseFor)
	for {
		r, ok := td.q.Lookup(id)
		if !ok {
			return
		}
		if r.Status != "" || !time.Now().Before(deadline) {
			if r.Phase == promptq.PhaseFilled || r.Phase == promptq.PhaseUnknown {
				m.cleanup(td, id)
			}
			return
		}
		time.Sleep(tune.poll)
	}
}

// keysSubmit is the no-mod fallback (M4): only when no owner has been seen for keysAfter and the light is calm. The text is
// typed literally (LF stays a newline, a trailing @token gets a space so the completion menu does not take the Enter), then
// Enter, each through the same guard and aimed at the owner pane.
func (m *Module) keysSubmit(w http.ResponseWriter, c *typedCtx, light string, tune typedTuning) {
	if c.td.q.NoOwnerFor(c.owner.SessionID) < tune.keysAfter || (light != "idle" && light != "running") {
		refuse(w, http.StatusConflict, "no_owner_yet")
		return
	}
	m.keysSeen.put(c.id, keysRow{})
	for i, chunk := range []string{padTrailingMention(c.text), tmux.KeyEnter} {
		if i > 0 && tune.keyGap > 0 {
			time.Sleep(tune.keyGap)
		}
		if err := c.td.keys.SendKeysIfInstanceAndMode(c.tmuxID, c.expected, c.owner.TmuxPaneID, chunk); err != nil {
			if i > 0 {
				// The text is already typed and the Enter was refused: it stays in the terminal, the id stays recorded as
				// unknown (a repeat never types again) and the deck tells the user to look at the terminal.
				refuse(w, http.StatusConflict, "text_left_in_terminal")
				return
			}
			m.keysSeen.forget(c.id) // nothing went out: a retry is safe
			if reason, ok := guardReason(err); ok {
				refuse(w, http.StatusConflict, reason)
			} else {
				writeError(w, http.StatusBadGateway, "tmux_error")
			}
			return
		}
	}
	m.keysSeen.put(c.id, keysRow{handed: true})
	writeTyped(w, http.StatusOK, typedAnswer{Outcome: "handed", Mode: "keys"})
}

// padTrailingMention appends a space after a final "@token".
func padTrailingMention(s string) string {
	if i := strings.LastIndexAny(s, " \n"); strings.HasPrefix(s[i+1:], "@") {
		return s + " "
	}
	return s
}
