package workbook

import "strings"

// apply maps a result onto the entry (spec §5.1 table, §5.4) and returns the job that continues it, if any.
func (e *Engine) apply(l *lease, r Result) (follow *job) {
	j := l.job
	j.usage.In += r.Usage.In
	j.usage.Out += r.Usage.Out
	j.usage.CacheRead += r.Usage.CacheRead
	j.lat += r.LatencyMS
	if j.kind == JobRewrite {
		return e.applyRewrite(j, r)
	}
	if !r.Answered {
		return e.failedCall(j, r, l.timeoutMS)
	}
	sum, err := ParseModelJSON(r.Text)
	if err != nil {
		return e.formatFailure(j)
	}
	if sum.Skip {
		res, ok, err := e.d.Store.FinishSkippedV2(j.entryID, ReasonModel, j.usage, j.lat, ResolveTodos(sum.Todos, l.ids), "model")
		if err != nil {
			e.d.Logf("[workbook] finish a skipped entry: %v", err)
			e.orphan(j.entryID)
		}
		e.logTodos(res)
		if ok {
			e.notifyLine(j.entryID, false)
		}
		return nil
	}
	fixed, rewrite := Repair(sum)
	res, ok, err := e.d.Store.SetPushLineV2(j.entryID, PushLineV2{Thing: fixed.Thing, Push: fixed.Push, Status: fixed.Status,
		Usage: j.usage, Todos: ResolveTodos(fixed.Todos, l.ids), By: "model"})
	if err != nil {
		e.d.Logf("[workbook] write a push line: %v", err)
		e.finishUnrun(j, StateFailed, ReasonAPI)
		return nil
	}
	if !ok {
		return nil // the entry is not pending any more
	}
	e.logTodos(res)
	e.notifyLine(j.entryID, true) // the push never waits for the re-write
	j.out = Output{Thing: fixed.Thing, Push: fixed.Push, Entry: fixed.Entry, ThingDone: fixed.ThingDone}
	if !rewrite {
		e.finishOK(j)
		return nil
	}
	e.qmu.Lock()
	capped := e.capReached()
	e.qmu.Unlock()
	if capped {
		j.entry = fixed.Entry
		e.finishCut(j)
		return nil
	}
	return &job{conv: j.conv, entryID: j.entryID, session: j.session, kind: JobRewrite, attempt: 1,
		entry: fixed.Entry, out: j.out, usage: j.usage, lat: j.lat}
}

func (e *Engine) finishOK(j *job) {
	out := j.out
	out.LatencyMS, out.Usage = j.lat, j.usage
	e.finishRow(j.entryID, StateOK, "", out)
}

// applyRewrite: whatever the re-write does, the entry ends ok — thing and push are out already; a failed or empty
// re-write just means the entry is cut.
func (e *Engine) applyRewrite(j *job, r Result) *job {
	if r.Answered {
		if t := strings.TrimSpace(r.Text); t != "" {
			j.entry = t
		}
	} else {
		e.d.Logf("[workbook] a re-write did not answer (%s); the entry is cut", logKind(r.Reason))
	}
	e.finishCut(j)
	return nil
}

// failedCall maps a call that did not answer (spec §5.1 table).
func (e *Engine) failedCall(j *job, r Result, timeoutMS int) *job {
	switch r.Reason {
	case "empty-reply":
		return e.formatFailure(j)
	case "api-error":
		if r.Error == "authentication_failed" {
			e.d.Logf("[workbook] the summariser call was refused for authentication")
			e.finishUnrun(j, StateFailed, ReasonAuth)
			return nil
		}
		e.d.Logf("[workbook] the summariser call failed (status %d, kind %s)", r.Status, logKind(r.Error))
		e.finishUnrun(j, StateFailed, ReasonAPI)
	case "aborted":
		if r.LatencyMS >= int64(timeoutMS) {
			e.finishUnrun(j, StateFailed, ReasonTimeout)
		} else {
			e.finishUnrun(j, StateFailed, ReasonStopped)
		}
	case "refused":
		e.finishUnrun(j, StateFailed, ReasonRefused)
	default:
		e.d.Logf("[workbook] the summariser call failed (reason %s)", logKind(r.Reason))
		e.finishUnrun(j, StateFailed, ReasonAPI)
	}
	return nil
}

// formatFailure: an empty reply or text that is not the JSON gets one retry as the same kind with attempt 2.
func (e *Engine) formatFailure(j *job) *job {
	if j.attempt >= 2 {
		e.finishUnrun(j, StateFailed, ReasonFormat)
		return nil
	}
	retry := *j
	retry.attempt = 2
	return &retry
}

func (e *Engine) logTodos(res TodoResult) {
	if res.CapIgnored > 0 {
		e.d.Logf("[workbook] %d todo adds were ignored: the list is full", res.CapIgnored)
	}
	if res.EmptyTitle > 0 {
		e.d.Logf("[workbook] %d todo adds were ignored: no title", res.EmptyTitle)
	}
}

// logKind keeps a text from the mod fit for a log line: a short snake_case kind, anything else is "other".
func logKind(s string) string {
	if len(s) == 0 || len(s) > 40 {
		return "other"
	}
	for _, c := range s {
		if !(c == '_' || c == '-' || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			return "other"
		}
	}
	return s
}
