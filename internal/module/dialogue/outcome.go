package dialogue

import (
	"errors"
)

// errFormat is what a kind's apply returns when the answer is not what it expects (not the JSON, a missing field ...).
var errFormat = errors.New("dialogue: the answer is not in the expected form")

// errGone is what a kind's apply returns when the row is gone: there is nothing to store and no point in trying again.
var errGone = errors.New("dialogue: the row is gone")

// errNoHandler: a job of a kind nobody handles.
var errNoHandler = errors.New("dialogue: no handler for the job kind")

// apply maps a result onto the job's row and returns the job that continues it (a retry), if any.
func (c *channel) apply(l *lease, r result) (follow *job) {
	j := l.job
	// what this job has used so far, this call included: a retried attempt carries the earlier ones
	total := j.use.plus(usage{r.Usage.In, r.Usage.Out, r.Usage.CacheRead, r.LatencyMS})
	if !r.Answered {
		return c.failedCall(j, r, l.timeoutMS, total)
	}
	h := c.d.handlers[j.kind]
	if h == nil {
		c.finishUnrun(j, stateFailed, reasonAPI)
		return nil
	}
	err := h.apply(*j, r.Text, total)
	switch {
	case err == nil:
		h.after(*j)
		return nil
	case errors.Is(err, errFormat):
		return c.formatFailure(j, total)
	case errors.Is(err, errGone):
		h.finish(*j, stateFailed, reasonStore)
		return nil
	default:
		c.d.logf("[dialogue] store an answer (kind %s): %v", j.kind, err)
		c.keepPending(j, r.Text, total)
		return nil
	}
}

// failedCall maps a call that did not answer.
func (c *channel) failedCall(j *job, r result, timeoutMS int, total usage) *job {
	switch r.Reason {
	case "empty-reply":
		return c.formatFailure(j, total)
	case "api-error":
		if r.Error == "authentication_failed" {
			c.d.logf("[dialogue] the model call was refused for authentication")
			c.finishUnrun(j, stateFailed, reasonAuth)
			return nil
		}
		c.d.logf("[dialogue] the model call failed (status %d, kind %s)", r.Status, logKind(r.Error))
		c.finishUnrun(j, stateFailed, reasonAPI)
	case "aborted":
		if r.LatencyMS >= int64(timeoutMS) {
			c.finishUnrun(j, stateFailed, reasonTimeout)
		} else {
			c.finishUnrun(j, stateFailed, reasonStopped)
		}
	case "refused":
		c.finishUnrun(j, stateFailed, reasonRefused)
	default:
		c.d.logf("[dialogue] the model call failed (reason %s)", logKind(r.Reason))
		c.finishUnrun(j, stateFailed, reasonAPI)
	}
	return nil
}

// formatFailure: an empty reply or text that is not what the kind expects gets one retry as the same kind with attempt 2.
func (c *channel) formatFailure(j *job, total usage) *job {
	if j.attempt >= 2 {
		c.finishUnrun(j, stateFailed, reasonFormat)
		return nil
	}
	retry := *j
	retry.attempt = 2
	retry.use = total
	return &retry
}

// logKind keeps a text from the mod fit for a log line: a short snake_case kind, anything else is "other".
func logKind(s string) string {
	if len(s) == 0 || len(s) > 40 {
		return "other"
	}
	for _, ch := range s {
		if !(ch == '_' || ch == '-' || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')) {
			return "other"
		}
	}
	return s
}
