package watchmod

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/devices"
	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/watch"
)

// The routes that start and wake the watcher (host-load-control spec §12.3–§12.4, plan W3b): POST /api/watch/start and /wake for the
// CLI (admin token), POST /api/peers/watch/start and /wake for a paired host. A cooldown is a 409 "cooldown" whose detail is the
// time the next try may be made (RFC3339); a start or wake still going in the background after the grace is a 202.

const maxSessionBody = 8 << 10

// wakeBody is the body of a wake: a missing prompt, "" and a text are all valid.
type wakeBody struct {
	Prompt *string `json:"prompt"`
}

// startReply is the answer of POST /api/watch/start: where the watcher is, or which host is coming up (202).
type startReply struct {
	Host      string `json:"host,omitempty"`
	HostID    string `json:"host_id"`
	State     string `json:"state,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

func (m *Module) requireAdmin(w http.ResponseWriter, r *http.Request, what string) bool {
	if _, isDevice := devices.PrincipalFrom(r.Context()); isDevice || !devices.IsAdmin(r.Context()) {
		m.writeErr(w, http.StatusForbidden, "forbidden", "only the admin token "+what)
		return false
	}
	return true
}

// writeSessionErr maps the refusals of a start or wake to their answers.
func (m *Module) writeSessionErr(w http.ResponseWriter, err error) {
	var cd *watch.CooldownError
	switch {
	case errors.As(err, &cd):
		m.writeErr(w, http.StatusConflict, "cooldown", cd.Until.Format(time.RFC3339))
	case errors.Is(err, watch.ErrBadPrompt):
		m.writeErr(w, http.StatusBadRequest, "bad_prompt", err.Error())
	case errors.Is(err, watch.ErrNoSession):
		m.writeErr(w, http.StatusServiceUnavailable, "no_session", err.Error())
	case errors.Is(err, watch.ErrPaneBusy):
		m.writeErr(w, http.StatusConflict, "pane_busy", err.Error())
	default:
		m.writeErr(w, http.StatusBadGateway, "start_failed", err.Error())
	}
}

// cooldowns are the failed starts the paired hosts told of (alias → until): a start that finds no watcher says when it may try again.
type cooldownBook struct {
	mu sync.Mutex
	at map[string]time.Time
}

func (b *cooldownBook) note(alias string, until time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.at == nil {
		b.at = map[string]time.Time{}
	}
	b.at[alias] = until
}

// earliest is the soonest time a cooldown ends, among those still running (the local one included).
func (m *Module) earliestCooldown() (time.Time, bool) {
	now := m.now()
	var best time.Time
	take := func(t time.Time) {
		if t.After(now) && (best.IsZero() || t.Before(best)) {
			best = t
		}
	}
	if m.sess != nil {
		take(m.sess.Get().CooldownUntil)
	}
	m.cooldowns.mu.Lock()
	for _, t := range m.cooldowns.at {
		take(t)
	}
	m.cooldowns.mu.Unlock()
	return best, !best.IsZero()
}

// handleStart serves POST /api/watch/start: make sure a watcher is awake somewhere, starting or waking one if none is.
func (m *Module) handleStart(w http.ResponseWriter, r *http.Request) {
	if !m.requireAdmin(w, r, "starts the watcher") {
		return
	}
	s, err := m.readSettings()
	if err != nil {
		m.writeErr(w, http.StatusInternalServerError, "no_settings", err.Error())
		return
	}
	ctx := r.Context()
	found, err := m.loc.Current(ctx, s.Targets, false)
	if err != nil {
		found, err = m.loc.Current(ctx, s.Targets, true)
	}
	if err == nil {
		m.writeJSON(w, http.StatusOK, startReply{Host: found.Alias, HostID: found.HostID, State: string(found.State), SessionID: found.SessionID})
		return
	}
	if !errors.Is(err, watch.ErrNoWatcher) {
		m.writeErr(w, http.StatusBadGateway, "start_failed", err.Error())
		return
	}
	if alias, _, ok := m.loc.Uncertain(); ok { // it is coming up there
		id := ""
		if m.isSelf(alias) {
			id = m.view().HostID
		} else if e, _, ok := m.peerEntry(alias); ok {
			id = e.HostID
		}
		m.writeJSON(w, http.StatusAccepted, startReply{Host: alias, HostID: id})
		return
	}
	if until, ok := m.earliestCooldown(); ok {
		m.writeSessionErr(w, &watch.CooldownError{Until: until})
		return
	}
	m.writeErr(w, http.StatusServiceUnavailable, "no_watcher", err.Error())
}

// handleWake serves POST /api/watch/wake: wake the watcher of this host, with an optional extra prompt.
func (m *Module) handleWake(w http.ResponseWriter, r *http.Request) {
	if !m.requireAdmin(w, r, "wakes the watcher") {
		return
	}
	var req wakeBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSessionBody)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		m.writeErr(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	m.wakeReply(w, r, req.Prompt, false)
}

// wakeReply wakes (or, for a start, starts) this host's watcher and writes the holder view; a peer gets the view, the CLI too.
func (m *Module) wakeReply(w http.ResponseWriter, r *http.Request, prompt *string, start bool) {
	if m.session == nil {
		m.writeSessionErr(w, watch.ErrNoSession)
		return
	}
	var ans watch.HolderAnswer
	var err error
	if start {
		ans, err = m.session.Start(r.Context())
	} else {
		p := ""
		if prompt != nil {
			p = *prompt
		}
		ans, err = m.session.Wake(r.Context(), p)
	}
	if err != nil {
		m.writeSessionErr(w, err)
		return
	}
	v := m.view()
	v.Starting = ans.Starting
	status := http.StatusOK
	if ans.Starting {
		status = http.StatusAccepted
	}
	m.writeJSON(w, status, v)
}

func (m *Module) handlePeerStart(w http.ResponseWriter, r *http.Request) {
	m.handlePeerSession(w, r, true)
}
func (m *Module) handlePeerWake(w http.ResponseWriter, r *http.Request) {
	m.handlePeerSession(w, r, false)
}

// handlePeerSession serves POST /api/peers/watch/start and /wake, with the holder route's order: the binding of the host principal
// to its live entry, the per-host rate limit and the body cap, the addressee.
func (m *Module) handlePeerSession(w http.ResponseWriter, r *http.Request, start bool) {
	what := "watch wake"
	if start {
		what = "watch start"
	}
	var entry config.PeerHost
	var ours string
	_, _, berr := peersmod.BindHostPrincipal(r, what, func(alias string) (config.PeerHost, bool) {
		var ok bool
		entry, ours, ok = m.peerEntry(alias)
		return entry, ok
	})
	if berr != nil {
		m.logf("[watch] %s refused (%s): %s", what, berr.Code, berr.Detail)
		m.writeErr(w, berr.Status, berr.Code, berr.Detail)
		return
	}
	var req struct {
		ToHostID string  `json:"to_host_id"`
		Prompt   *string `json:"prompt"`
	}
	switch st := peersmod.AdmitDecode(w, r, m.limit, entry.HostID, maxSessionBody, &req); st {
	case 0:
	case http.StatusTooManyRequests:
		m.writeErr(w, st, ipeers.ErrRateLimited, "host rate limit exceeded")
		return
	case http.StatusRequestEntityTooLarge:
		m.writeErr(w, st, ipeers.ErrBadRequest, "body over 8 KiB")
		return
	default:
		m.writeErr(w, st, ipeers.ErrBadRequest, "invalid JSON body")
		return
	}
	if req.ToHostID != ours {
		m.writeErr(w, http.StatusConflict, "wrong_host", "this request is addressed to another host")
		return
	}
	m.wakeReply(w, r, req.Prompt, start)
}
