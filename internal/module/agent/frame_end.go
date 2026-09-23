package agent

import (
	"log"

	"github.com/wake/purdex/internal/store"
)

// The reads and cleanup that follow a successful claim, as seams so tests can
// fail them AFTER the delete. Production never reassigns them.
var (
	eventsDeleteFn         = func(m *Module, sessionName string) error { return m.events.Delete(sessionName) }
	projectionForSessionFn = func(m *Module, sessionName string) (*SessionProjection, error) {
		return m.projectionForSession(sessionName)
	}
	projectPaneFn = func(m *Module, paneID string) (*SessionProjection, error) { return m.projectPane(paneID) }
	// claimDeleteFn is the claim itself, a seam so a test can make a caller
	// lose it without staging the race.
	claimDeleteFn = func(m *Module, frameID, sessionID string) (bool, error) {
		return m.frames.ClaimDelete(frameID, sessionID)
	}
)

// logAfterClaim records a failure that happened after an exit was claimed. The
// exit is still sent (degraded) — see afterFrameCleared and the SessionEnd
// path — so this is the only trace the failure leaves besides the error the
// caller returns.
func logAfterClaim(where string, frameID string, err error) {
	log.Printf("[agent] exit_cleanup_failed: where=%s frame=%s err=%v", where, frameID, err)
}

// claimFrameEnd is the ONE place a frame's run is ended, shared by the
// SessionEnd hook and the pid sweep (agent-last-state, #1381).
//
// It claims the frame with a conditional delete (store.ClaimDelete): of two
// callers ending the same frame at once, exactly one removes the row, and only
// that one gets the exit envelope back. The other gets (nil, false) and must
// send no exit — otherwise a late process-dead could overwrite a session-end,
// or one exit be delivered twice.
//
// requireSessionID narrows the claim to one run (see ClaimDelete): the
// SessionEnd path passes its payload's session id, so a late SessionEnd of an
// older run cannot delete the frame a newer run's SessionStart has taken over.
// The sweep passes "": a dead process has no newer run.
//
// exit is built by the caller from its pre-delete snapshot (nil for a child
// frame) and is returned only on a successful claim.
func (m *Module) claimFrameEnd(frame store.Frame, requireSessionID string, exit *Exit) (*Exit, bool, error) {
	claimed, err := claimDeleteFn(m, frame.FrameID, requireSessionID)
	if err != nil || !claimed {
		return nil, false, err
	}
	return exit, true, nil
}
