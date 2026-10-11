package promptq

import "context"

// The typed-submit state machine (K-1): one ledger row per client_msg_id for a fill, in the same ledger as submit.

// Phases of a typed submit (kind fill), one per client_msg_id, in the same ledger as submit:
//
//	queued -> filled -> entering -> entered | unknown      filled -> cleared      (a refused fill is cleared)
//
// `entering` is written BEFORE the daemon presses Enter (the at-most-once line): from there the text may have been sent,
// so it can no longer be cleared, and a repeat of the id never types again.
const (
	PhaseQueued   = "queued"
	PhaseFilled   = "filled"
	PhaseEntering = "entering"
	PhaseEntered  = "entered"
	PhaseCleared  = "cleared"
	PhaseUnknown  = "unknown"
)

// view is the entry's answer with its current phase. q.mu held.
func (q *Queue) view(e *entry) Result {
	r := e.res
	r.Phase = e.phase
	return r
}

// Lookup returns the recorded state of a typed submit by client_msg_id (never runs anything), false if the id is not in
// the ledger.
func (q *Queue) Lookup(clientMsgID string) (Result, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	e, ok := q.ledger[clientMsgID]
	if !ok || e.job.Kind != KindFill || clientMsgID == "" {
		if t, dead := q.tombs[clientMsgID]; dead && clientMsgID != "" {
			return t.res, true
		}
		return Result{}, false
	}
	return q.view(e), true
}

// step moves a settled fill row from one of the allowed phases to the next; anything else is ErrBadTransition.
func (q *Queue) step(clientMsgID, to string, from ...string) error {
	return q.stepWith(clientMsgID, to, func(e *entry) bool {
		for _, f := range from {
			if e.phase == f {
				return true
			}
		}
		return false
	}, nil)
}

// stepWith moves a settled fill row to `to` when allow says so, running onMove under the same lock.
func (q *Queue) stepWith(clientMsgID, to string, allow func(*entry) bool, onMove func(*entry)) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	e, ok := q.ledger[clientMsgID]
	if !ok || clientMsgID == "" || e.job.Kind != KindFill || e.state != stDone || !allow(e) {
		return ErrBadTransition
	}
	e.phase = to
	if onMove != nil {
		onMove(e)
	}
	return nil
}

// MarkEntering is written BEFORE Enter is pressed: it succeeds once, from filled only.
func (q *Queue) MarkEntering(id string) error {
	return q.stepWith(id, PhaseEntering, func(e *entry) bool { return e.phase == PhaseFilled }, func(e *entry) { e.enterMark = true })
}

// MarkEntered: Enter went out.
func (q *Queue) MarkEntered(id string) error { return q.step(id, PhaseEntered, PhaseEntering) }

// MarkEnterRefused: tmux definitively declined the Enter (nothing was sent), so the row steps back from entering to filled
// and the composer can be cleaned up with ClearFilled.
func (q *Queue) MarkEnterRefused(id string) error {
	return q.stepWith(id, PhaseFilled, func(e *entry) bool { return e.phase == PhaseEntering }, func(e *entry) { e.enterMark = false })
}

// MarkGaveUp settles a row whose cleanup did not succeed (ClearIf dropped not_ours, or no answer): cleared with the reason
// cleanup_gave_up, so it stops counting toward the open typed rows. Only a row that was never entering.
func (q *Queue) MarkGaveUp(id string) error {
	return q.stepWith(id, PhaseCleared, clearable, func(e *entry) { e.res.Reason = "cleanup_gave_up" })
}

// clearable: the row's text may be in the composer and Enter was never marked.
func clearable(e *entry) bool {
	return !e.settled && (e.phase == PhaseFilled || (e.phase == PhaseUnknown && !e.enterMark))
}

// MarkCleared: the fill was not sent (guard refused, stale, waiting, lost result) and the composer is being cleaned up.
// Allowed from filled, and from unknown as long as Enter was never marked; never after entering.
func (q *Queue) MarkCleared(id string) error { return q.stepWith(id, PhaseCleared, clearable, nil) }

// MarkUnknown: it may or may not have been sent.
func (q *Queue) MarkUnknown(id string) error {
	return q.step(id, PhaseUnknown, PhaseFilled, PhaseEntering)
}

// openPhase: the row's text may be in the composer or may have been sent, so the row must be remembered.
func openPhase(p string) bool { return p == PhaseFilled || p == PhaseEntering || p == PhaseUnknown }

// forgettable: a settled row that carries no such uncertainty. q.mu held.
func forgettable(e *entry) bool {
	return e.state == stDone && !(e.job.Kind == KindFill && openPhase(e.phase) && !e.settled)
}

// openTyped counts the rows that sweepLedger never forgets. q.mu held.
func (q *Queue) openTyped() int {
	n := 0
	for _, e := range q.ledger {
		if e.job.Kind == KindFill && e.state == stDone && openPhase(e.phase) && !e.settled {
			n++
		}
	}
	return n
}

// ClearFilled is the cleanup of a typed row that never got its Enter (phase filled, or unknown with Enter never marked): it
// asks the mod to clear the composer only if it still holds the row's text, and when the mod accepts, marks the row
// cleared. Any other row answers dropped / not_clearable without asking anything.
func (q *Queue) ClearFilled(ctx context.Context, clientMsgID string) (Result, error) {
	q.mu.Lock()
	e, ok := q.ledger[clientMsgID]
	if !ok || clientMsgID == "" || e.job.Kind != KindFill || e.state != stDone || !clearable(e) {
		q.mu.Unlock()
		return Result{Status: Dropped, Reason: "not_clearable"}, nil
	}
	sid, text := e.job.SessionID, e.job.Text
	q.mu.Unlock()
	r, err := q.ClearIf(ctx, sid, text)
	if err == nil && r.Status == Accepted {
		_ = q.MarkCleared(clientMsgID) // a row that moved on meanwhile (Enter marked) stays as it is
	}
	return r, err
}
