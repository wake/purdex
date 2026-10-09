package workbook

// ReasonStore: failed — the store would not take the entry's final state (issue #2324), so it was failed afterwards.
const ReasonStore = "store"

// finishRow ends an entry. When the store refuses, the queue must not stall on a row nobody can finish: the entry is
// remembered, the conversation moves on, and the reaper keeps trying to mark it failed (store) until the store takes it.
func (e *Engine) finishRow(id int64, state, reason string, out Output) {
	if _, err := e.d.Store.Finish(id, state, reason, out); err != nil {
		e.d.Logf("[workbook] end an entry: %v", err)
		e.orphan(id)
	}
}

// orphan remembers an entry whose final state could not be written.
func (e *Engine) orphan(id int64) {
	e.omu.Lock()
	if e.orphans == nil {
		e.orphans = map[int64]struct{}{}
	}
	e.orphans[id] = struct{}{}
	e.omu.Unlock()
}

// retryOrphans tries once more to fail each remembered entry. An entry that is no longer pending (a restart or another
// path settled it) is simply forgotten.
func (e *Engine) retryOrphans() {
	e.omu.Lock()
	ids := make([]int64, 0, len(e.orphans))
	for id := range e.orphans {
		ids = append(ids, id)
	}
	e.omu.Unlock()
	for _, id := range ids {
		if _, err := e.d.Store.Finish(id, StateFailed, ReasonStore, Output{}); err != nil {
			continue // still refused: the next tick tries again
		}
		e.omu.Lock()
		delete(e.orphans, id)
		e.omu.Unlock()
		e.notifyLine(id, false)
	}
}
