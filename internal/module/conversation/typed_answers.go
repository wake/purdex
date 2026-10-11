package conversation

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/wake/purdex/internal/promptq"
	"github.com/wake/purdex/internal/tmux"
)

// One answer shape for every status of the typed route: {outcome?, mode?, reason?, error?}. A non-2xx answer always carries
// reason and error (the same code), so the Mac and iOS apps parse one shape. outcome is "refused" when the request was
// understood and declined, "unknown" / "handed" on 200, and absent for a malformed or unservable request.
type typedAnswer struct {
	Outcome string `json:"outcome,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Error   string `json:"error,omitempty"`
	Text    string `json:"text,omitempty"` // recall: the queued messages that came back
}

func writeTyped(w http.ResponseWriter, status int, a typedAnswer) {
	b, _ := json.Marshal(a)
	writeJSON(w, status, b)
}

// fail answers a request that was not understood or cannot be served: no outcome, just the code.
func fail(w http.ResponseWriter, status int, code string) {
	writeTyped(w, status, typedAnswer{Reason: code, Error: code})
}

// refuse answers a request that was understood and declined.
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

// replayTyped answers a recorded promptq row (or tombstone): the state, never a second typing.
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

// answerKnown answers an id a ledger already holds (promptq row or tombstone, then the keys ledger); true when it did.
// Read-only: it never creates a job.
func (m *Module) answerKnown(w http.ResponseWriter, td *typedDeps, id string, tune typedTuning) bool {
	if rec, ok := td.q.Lookup(id); ok {
		replayTyped(w, rec)
		return true
	}
	if rec, ok := m.keysSeen.get(id, tune.now()); ok {
		out := "unknown"
		if rec.handed {
			out = "handed"
		}
		writeTyped(w, http.StatusOK, typedAnswer{Outcome: out, Mode: "keys"})
		return true
	}
	return false
}

// padTrailingBackslash appends one space when the text ends with an odd run of backslashes: Claude Code reads backslash +
// Enter as a newline, so the Enter would not submit.
func padTrailingBackslash(s string) string {
	n := len(s) - len(strings.TrimRight(s, `\`))
	if n%2 == 1 {
		return s + " "
	}
	return s
}

// padAnyTrailingBackslash appends one space after any trailing backslash. Keys mode only: measured on Claude Code 2.1.296, a
// run of two or three backslashes plus Enter does not submit either (padTrailingBackslash pads odd runs only).
func padAnyTrailingBackslash(s string) string {
	if strings.HasSuffix(s, `\`) {
		return s + " "
	}
	return s
}

// padTrailingMention appends a space after a final "@token" (the completion menu would take the Enter).
func padTrailingMention(s string) string {
	if i := strings.LastIndexAny(s, " \n"); strings.HasPrefix(s[i+1:], "@") {
		return s + " "
	}
	return s
}
