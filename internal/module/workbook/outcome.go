package workbook

// apply is what a result does to its entry. This PR only queues and leases; the outcome table, the validation and the
// re-write follow in the next one. Until then a reported result ends the entry as failed:stopped, so nothing is left
// pending (the daemon cannot run a job before the mod's routes exist anyway).
func (e *Engine) apply(l *lease, _ Result) (follow *job) {
	e.finishUnrun(l.job, StateFailed, ReasonStopped)
	return nil
}
