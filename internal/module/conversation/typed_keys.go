package conversation

import (
	"net/http"

	"github.com/wake/purdex/internal/tmux"
)

// keysSubmit is the no-mod fallback (M4): blind typing into the owner pane, marked mode "keys". Allowed only when the light
// is `idle` (not running, not waiting) and no owner was seen for keysAfter (10 s). The light is read again immediately before
// the send, and the text and the Enter go out in ONE guarded send-keys call (one if-shell), so a refusal of the guard means
// nothing was typed and any other error means the text may have landed (409 text_left_in_terminal, the id stays unknown).
// Typed literally: LF stays a newline, a trailing @token gets a space so the completion menu does not take the Enter.
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
	m.keysSeen.put(c.id, keysRow{}, tune.now()) // before the send: a repeat never types again
	if m.lightOf(c.owner) != "idle" || q.HasOwner(c.owner.SessionID) {
		m.keysSeen.forget(c.id) // nothing went out
		refuse(w, http.StatusConflict, "no_owner_yet")
		return
	}
	err := c.td.keys.SendKeysIfInstanceAndMode(c.tmuxID, c.expected, c.owner.TmuxPaneID, padTrailingMention(c.text), tmux.KeyEnter)
	if err != nil {
		if reason, ok := guardReason(err); ok {
			m.keysSeen.forget(c.id) // the guard declined the whole call: nothing was typed, a retry is safe
			refuse(w, http.StatusConflict, reason)
			return
		}
		// tmux could not be completed: the text may be in the terminal. The id stays recorded as unknown.
		refuse(w, http.StatusConflict, "text_left_in_terminal")
		return
	}
	m.keysSeen.put(c.id, keysRow{handed: true}, tune.now())
	writeTyped(w, http.StatusOK, typedAnswer{Outcome: "handed", Mode: "keys"})
}
