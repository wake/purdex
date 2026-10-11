package conversation

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/wake/purdex/internal/promptq"
	"github.com/wake/purdex/internal/tmux"
)

// typedCtx is what one request decided before it started to send.
type typedCtx struct {
	td       *typedDeps
	id, text string
	expected string
	tmuxID   string
	owner    agentOwner
}

// fillSubmit is the mod path: the mod fills the composer, then the daemon presses Enter. Locks are held by the caller.
func (m *Module) fillSubmit(w http.ResponseWriter, r *http.Request, c *typedCtx, tune typedTuning) {
	td := c.td
	res, err := td.q.SubmitFill(r.Context(), c.owner.SessionID, c.id, c.text)
	switch {
	case errors.Is(err, promptq.ErrBusy):
		refuse(w, http.StatusTooManyRequests, "busy")
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal")
		return
	}
	switch res.Status {
	case promptq.Accepted:
		// Once the fill is accepted the Enter goes out even if the client disconnected meanwhile: sending beats leaving text
		// behind in the composer (spec §5).
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
			m.spawnFinaliser(c, tune)
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
	st, lightOK := m.freshLight(c.owner)
	if !lightOK { // fail closed: a dialog may have appeared during the fill
		m.cleanup(td, c.id)
		refuse(w, http.StatusConflict, "light_unavailable")
		return
	}
	if st == "waiting" {
		m.cleanup(td, c.id)
		refuse(w, http.StatusConflict, "waiting")
		return
	}
	if err := td.q.MarkEntering(c.id); err != nil {
		m.cleanup(td, c.id) // the row is not ours to enter, but our text may still be in the composer
		refuse(w, http.StatusConflict, "conflict")
		return
	}
	err := td.keys.SendKeysIfInstanceAndMode(c.tmuxID, c.expected, c.owner.TmuxPaneID, tmux.KeyEnter)
	if err == nil {
		_ = td.q.MarkEntered(c.id)
		writeTyped(w, http.StatusOK, typedAnswer{Outcome: "handed", Mode: "fill"})
		return
	}
	if reason, ok := guardReason(err); ok {
		// tmux declined: nothing was sent, so the row steps back and the composer is cleaned
		_ = td.q.MarkEnterRefused(c.id)
		m.cleanup(td, c.id)
		refuse(w, http.StatusConflict, reason)
		return
	}
	// tmux could not be completed: the Enter may or may not have gone. The row is unknown; our text is cleared best effort
	// (safe: clear_if only clears the composer when it still holds exactly our text, so a sent message is untouched).
	_ = td.q.MarkUnknown(c.id)
	cleared := false
	for i := 0; i < 2 && !cleared; i++ { // one retry; a clear_if that is dropped (not_ours) or unanswered did not clear
		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		res, err := td.q.ClearIf(ctx, c.owner.SessionID, c.text)
		cancel()
		cleared = err == nil && res.Status == promptq.Accepted
	}
	if !cleared {
		// The row is unknown with its Enter marked, so MarkGaveUp (never-entering rows only) does not apply; the ledger sweep
		// settles such a row at once, so it does not count as open. The text itself may be stuck: say so.
		log.Printf("typed submit %s: clean-up after an unknown Enter failed twice; the text may be stuck in the composer", c.id)
	}
	writeTyped(w, http.StatusOK, typedAnswer{Outcome: "unknown", Mode: "fill"})
}

// cleanup clears our text out of the composer (best effort, one retry) and always lets the row reach an end state: when both
// tries fail the row is settled as cleanup_gave_up, so it stops counting toward the open typed rows. Callers hold the locks.
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
