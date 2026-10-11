package promptq

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
		return Result{}, false
	}
	return q.view(e), true
}

// step moves a settled fill row from one of the allowed phases to the next; anything else is ErrBadTransition.
func (q *Queue) step(clientMsgID, to string, from ...string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	e, ok := q.ledger[clientMsgID]
	if !ok || clientMsgID == "" || e.job.Kind != KindFill || e.state != stDone {
		return ErrBadTransition
	}
	for _, f := range from {
		if e.phase == f {
			e.phase = to
			return nil
		}
	}
	return ErrBadTransition
}

// MarkEntering is written BEFORE Enter is pressed: it succeeds once, from filled only.
func (q *Queue) MarkEntering(id string) error { return q.step(id, PhaseEntering, PhaseFilled) }

// MarkEntered: Enter went out.
func (q *Queue) MarkEntered(id string) error { return q.step(id, PhaseEntered, PhaseEntering) }

// MarkCleared: the fill was not sent (guard refused, stale, waiting) and the composer is being cleaned up. Not after entering.
func (q *Queue) MarkCleared(id string) error { return q.step(id, PhaseCleared, PhaseFilled) }

// MarkUnknown: it may or may not have been sent.
func (q *Queue) MarkUnknown(id string) error {
	return q.step(id, PhaseUnknown, PhaseFilled, PhaseEntering)
}
