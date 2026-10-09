package teammod

import (
	"errors"
	"fmt"
	"net/http"
	"syscall"

	"github.com/wake/purdex/internal/team"
)

// The kill of an ADOPTED member (adopt plan PL-1d2, decision 13, spec D-U24-3): its tmux session and shell
// are the user's, so only its Claude Code process is signalled — SIGTERM, to the pid and start time the
// registry shows now and the process table agrees with right before the signal. Nothing here holds a lock
// across the registry read, the process check or the signal. The one race left is the milliseconds between
// the re-verification and kill(2) (a pid reused by another process of the same uid); it is stated, not closed.

// killAdopted ends adopted member mr's process. ended says nothing was left to signal (the session is not
// live, or its process is gone or reused): the caller marks the row gone instead of killed. why != "" is an
// error to answer (status, code), with nothing marked.
func (m *Module) killAdopted(mr memberRow) (ended bool, status int, code, why string) {
	o, live, err := m.origins.ResolveOriginBySession(mr.SessionID)
	if err != nil {
		return false, http.StatusServiceUnavailable, team.ErrNotReady, "registry unavailable; retry: " + err.Error()
	}
	if !live {
		return true, 0, "", ""
	}
	same, err := m.origins.SameProcess(o.PID, o.ProcStart)
	if err != nil {
		return false, http.StatusServiceUnavailable, team.ErrNotReady, "the member's process could not be verified; retry: " + err.Error()
	}
	if !same {
		return true, 0, "", "" // the process ended, or its pid was reused: never signalled
	}
	if o.PID <= 1 { // 0, a negative pid and init are never a target (kill(2) would signal a group or everything)
		return false, http.StatusInternalServerError, team.ErrKillFailed, fmt.Sprintf("refusing to signal pid %d", o.PID)
	}
	switch err := m.killProcess(o.PID); {
	case err == nil:
		return false, 0, "", ""
	case errors.Is(err, syscall.ESRCH): // ended meanwhile
		return true, 0, "", ""
	default:
		return false, http.StatusInternalServerError, team.ErrKillFailed, fmt.Sprintf("signalling process %d failed: %v", o.PID, err)
	}
}
