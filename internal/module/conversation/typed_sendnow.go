package conversation

import (
	"net/http"

	"github.com/wake/purdex/internal/tmux"
)

// POST /api/sessions/{code}/send-now (MS2-2, plan docs/plans/2026-10-19-message-stages-step2-plan.md §1.3): press Claude Code's
// own "send now" (Ctrl+X Ctrl+S, measured in MS2-0, spec §4.1) so the messages waiting in its queue go out at once, interrupting
// the turn that is running. A draft in Claude Code's input is sent along (measured), so the route refuses when the input has
// text (D7); a dialog would take the keys, so it refuses while the light is waiting (L3); with nothing queued the keys do
// nothing, so it refuses (L4).
//
// The checks run in recall's order under the pane and session locks. The mod's read of the input and the keys are back to back:
// nothing else touches the mod or tmux between them, and the tmux guard (generation, session, not in a mode) is the one
// if-shell of the keys call. The mod cannot trigger send now itself (MS2-0: no such call), so a person typing into the terminal
// in the milliseconds between the read and the keys is a known limit. Send now adds no stage: the queued messages reach read
// through the ordinary matching. Mod owner required; client_msg_id is the idempotency key, and an answer after the keys is
// recorded under it (a repeat never presses them again), like recall.

type sendNowRequest struct {
	ClientMsgID          string `json:"client_msg_id"`
	ExpectedTmuxInstance string `json:"expected_tmux_instance"`
}

func (m *Module) handleSessionSendNow(w http.ResponseWriter, r *http.Request) {
	td := m.typedDeps()
	if td == nil {
		fail(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	var in sendNowRequest
	if status, code := decodeBodyErr(w, r, &in); code != "" {
		fail(w, status, code)
		return
	}
	switch {
	case !clientMsgIDRe.MatchString(in.ClientMsgID):
		fail(w, http.StatusBadRequest, "bad_client_msg_id")
		return
	case !tmux.ValidInstance(in.ExpectedTmuxInstance):
		fail(w, http.StatusBadRequest, "bad_expected_tmux_instance")
		return
	}
	tune := m.tuning()
	scode := r.PathValue("code")
	info, err := td.sessions.GetSession(scode)
	switch {
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal")
		return
	case info == nil:
		fail(w, http.StatusNotFound, "session_not_found")
		return
	case info.TmuxInstance != in.ExpectedTmuxInstance:
		refuse(w, http.StatusConflict, "generation")
		return
	}
	key := recallKey(scode, in.ExpectedTmuxInstance, in.ClientMsgID)
	if m.sendNows.replay(w, key, tune.now()) { // before the owner is resolved, like recall
		return
	}
	owner, ok := m.resolveClaudeOwner(w, r, td, scode)
	if !ok {
		return
	}
	release, ok := m.lockPaneAndSession(r.Context(), owner.TmuxPaneID, owner.SessionID, tune.lockWait)
	if !ok {
		refuse(w, http.StatusTooManyRequests, "busy")
		return
	}
	defer release()
	if m.sendNows.replay(w, key, tune.now()) { // a concurrent request with this id may have finished meanwhile
		return
	}
	again, ok := m.resolveClaudeOwner(w, r, td, scode)
	if !ok {
		return
	}
	if again.SessionID != owner.SessionID || again.TmuxPaneID != owner.TmuxPaneID {
		refuse(w, http.StatusConflict, "owner_changed")
		return
	}
	if !m.paneIsUnique(w, r, td, again) {
		return
	}
	if !td.q.HasOwner(again.SessionID) {
		refuse(w, http.StatusConflict, "no_owner")
		return
	}
	switch st, lightOK := m.freshLight(again); {
	case !lightOK: // fail closed: a dialog may be open that the light cannot show
		refuse(w, http.StatusConflict, "light_unavailable")
		return
	case st == "waiting":
		refuse(w, http.StatusConflict, "waiting")
		return
	}
	queued, hidden, ok := m.queuedOf(w, r.Context(), again.SessionID)
	if !ok {
		return
	}
	if len(queued) == 0 && hidden == 0 {
		refuse(w, http.StatusConflict, "nothing_queued")
		return
	}
	res, err := td.q.Read(r.Context(), again.SessionID)
	if !readOK(w, res, err) {
		return
	}
	if res.Text != "" {
		refuse(w, http.StatusConflict, "input_not_empty") // the draft would be sent along
		return
	}
	// Nothing between the read above and these keys.
	if err := td.keys.SendKeysIfInstanceAndMode(info.TmuxID, in.ExpectedTmuxInstance, again.TmuxPaneID, tmux.KeySendNow); err != nil {
		if reason, ok := guardReason(err); ok {
			refuse(w, http.StatusConflict, reason) // nothing was sent: a retry with this id is a fresh try
			return
		}
		// tmux could not be completed: the keys may or may not have gone. A repeat of this id never presses them again.
		a := typedAnswer{Outcome: "unknown", Reason: "keys_failed"}
		m.sendNows.put(key, http.StatusOK, a, tune.now())
		writeTyped(w, http.StatusOK, a)
		return
	}
	a := typedAnswer{Outcome: "sent_now"}
	m.sendNows.put(key, http.StatusOK, a, tune.now())
	writeTyped(w, http.StatusOK, a)
}
