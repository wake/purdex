package agent

import (
	"fmt"

	agentpkg "github.com/wake/purdex/internal/agent"
)

// interruptBeforeWriteFn runs between the interrupt's read of the sender's
// frame and its conditional write: a seam so a test can let another hook win
// that window. Production never reassigns it.
var interruptBeforeWriteFn = func(*Module) {}

type interruptOutcome int

const (
	interruptNoFrame    interruptOutcome = iota // the sender has no frame: nothing written
	interruptSuperseded                         // a newer event wrote the frame: idle would be older, nothing written
	interruptWritten                            // the frame is idle now
)

// idleInterruptedFrame sets the sender's own frame idle for an Esc
// (PostToolUseFailure with is_interrupt, spec §7), but only if the interrupt
// is newer than anything the frame already holds. The frame is re-read, a
// frame stamped after the interrupt (last_seen_at > ts) is left alone, and
// otherwise idle is written conditionally on the last_seen_at that was read
// (UpsertIfUnchanged): a hook that wrote in between — a new prompt's running,
// a permission ask's waiting — makes the write lose, and the loop reads again
// and judges again, up to proxyUpsertMaxAttempts. The row written back is the
// one just read, so Subagents are never round-tripped from an older baseline
// (#632 R7; every Subagents mutation also moves last_seen_at, which the
// condition covers). It never creates a frame.
func (m *Module) idleInterruptedFrame(req EventRequest, ts int64) (interruptOutcome, error) {
	recorded := false
	for attempt := 0; attempt < proxyUpsertMaxAttempts; attempt++ {
		frame, err := m.frames.GetByIdentity(req.TmuxPaneID, req.SenderPID, req.SenderStartTime)
		if err != nil {
			return 0, err
		}
		if frame == nil {
			return interruptNoFrame, nil
		}
		if frame.LastSeenAt > ts {
			return interruptSuperseded, nil
		}
		if !recorded && m.probeOrch != nil {
			// Same ordering rule as the main path: the hook is authoritative,
			// so the probe grace window opens before the status is written.
			m.probeOrch.recordHookAt(req.TmuxSession)
			recorded = true
		}
		interruptBeforeWriteFn(m)
		next := *frame
		next.Status = agentpkg.StatusIdle
		next.LastSeenAt = ts
		ok, _, err := m.frames.UpsertIfUnchanged(next, frame.LastSeenAt)
		if err != nil {
			return 0, err
		}
		if ok {
			return interruptWritten, nil
		}
	}
	return 0, fmt.Errorf("interrupt idle: exceeded %d retries for %s/%d", proxyUpsertMaxAttempts, req.TmuxPaneID, req.SenderPID)
}
