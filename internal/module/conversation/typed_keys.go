package conversation

import (
	"net/http"
	"time"

	"github.com/wake/purdex/internal/tmux"
)

// keysSubmit is the no-mod fallback (M4): blind typing into the owner pane, marked mode "keys". Allowed only when the light
// is `idle` (not running, not waiting) and no owner was seen for keysAfter (10 s).
//
// Typing scheme, measured on Claude Code 2.1.296 (2026-10-11, isolated tmux server, spec docs/specs/2026-10-11-keystroke-submit-spec.md
// §2): text and Enter in ONE send-keys call are read as a paste and the Enter is swallowed (a single line of 90 chars or more,
// or multi-line text), so it is three steps: (1) the text alone, LF bytes kept (they stay newlines in the composer), (2) a
// settle (typedTuning.keysSettle, 150 ms; 0 ms already worked, the rest is margin) and the light read once more, (3) the Enter
// as its own guarded send. The light is read before the text and again before the Enter: a dialog that opened in between must
// not get the Enter. A trailing @token and any trailing backslash get a space (backslash plus Enter is a newline, and a
// completion menu would take the Enter).
//
// Failure classes: a guard refusal of the TEXT call typed nothing (clean refusal, the id is forgotten). Anything that goes
// wrong after the text may have left it in the terminal unsent (409 text_left_in_terminal, the id stays unknown and a repeat
// never types again): a tmux error on the text, a light that is no longer idle, and any refusal or error on the Enter.
//
// Residual risks of keys mode (spec §5), accepted because the fallback only runs when the mod is absent:
//   - there is no dialog detection (only the light): a permission or trust dialog the light does not show would take the keys;
//   - a draft already in the composer is not seen, so our text would be prefixed by it;
//   - vim mode: Claude Code's vim keybindings in normal mode would read the text as commands.
func (m *Module) keysSubmit(w http.ResponseWriter, c *typedCtx, light string, tune typedTuning) {
	q := c.td.q
	if light != "idle" || q.NoOwnerFor(c.owner.SessionID) < tune.keysAfter {
		refuse(w, http.StatusConflict, "no_owner_yet")
		return
	}
	// Atomic claim before anything is typed: a concurrent request with the same id (even on another session, which holds a
	// different lock) gets the recorded answer and never types.
	row, claimed, full := m.keysSeen.claim(c.id, c.owner.SessionID, tune.now())
	if full { // every row is a request still typing: never evict one, refuse cleanly
		refuse(w, http.StatusTooManyRequests, "busy")
		return
	}
	if !claimed {
		if row.sid != c.owner.SessionID {
			refuse(w, http.StatusConflict, "client_msg_id_reused")
			return
		}
		out := "unknown"
		if row.handed {
			out = "handed"
		}
		writeTyped(w, http.StatusOK, typedAnswer{Outcome: out, Mode: "keys"})
		return
	}
	defer m.keysSeen.settle(c.id) // whatever way this ends, the claim becomes an ordinary row (or was forgotten)
	st, ok := m.freshLight(c.owner)
	if !ok { // fail closed: an unreadable light is not an idle one
		m.keysSeen.forget(c.id) // nothing went out
		refuse(w, http.StatusConflict, "light_unavailable")
		return
	}
	if st != "idle" || q.HasOwner(c.owner.SessionID) {
		m.keysSeen.forget(c.id) // nothing went out
		refuse(w, http.StatusConflict, "no_owner_yet")
		return
	}
	text := padTrailingMention(c.text) // c.text is already backslash-padded in typed_submit.go
	if err := c.td.keys.SendKeysIfInstanceAndMode(c.tmuxID, c.expected, c.owner.TmuxPaneID, text); err != nil {
		if reason, ok := guardReason(err); ok {
			m.keysSeen.forget(c.id) // the guard declined the text call: nothing was typed, a retry is safe
			refuse(w, http.StatusConflict, reason)
			return
		}
		// tmux could not be completed: the text may be in the terminal. The id stays recorded as unknown.
		refuse(w, http.StatusConflict, "text_left_in_terminal")
		return
	}
	time.Sleep(tune.keysSettle)
	// From here the text is in the terminal: anything but a clean Enter leaves it there unsent (the id stays unknown).
	// Only the light is re-checked (the owner gate applies before typing): a mod that reconnects meanwhile must not strand the text.
	if st, ok := m.freshLight(c.owner); !ok || st != "idle" {
		refuse(w, http.StatusConflict, "text_left_in_terminal")
		return
	}
	if err := c.td.keys.SendKeysIfInstanceAndMode(c.tmuxID, c.expected, c.owner.TmuxPaneID, tmux.KeyEnter); err != nil {
		refuse(w, http.StatusConflict, "text_left_in_terminal")
		return
	}
	m.keysSeen.put(c.id, keysRow{handed: true, sid: c.owner.SessionID}, tune.now())
	writeTyped(w, http.StatusOK, typedAnswer{Outcome: "handed", Mode: "keys"})
}
