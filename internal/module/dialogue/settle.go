package dialogue

import "errors"

// An answer the kind could not store (its apply returned an error other than errFormat, e.g. the store refused the
// write) must not stall the session's queue and must not be lost: the lease is released, the answer is kept here and the
// reaper applies it again at each tick until the kind takes it. The base (workbook settle.go) kept an "orphan" entry and
// retried marking it failed(store); here the whole apply is retried, since the answer is the expensive part.

type pendingApply struct {
	job  job
	text string
	use  usage
}

// keepPending remembers an answer whose apply failed.
func (c *channel) keepPending(j *job, text string, u usage) {
	c.omu.Lock()
	c.pending[j.id] = &pendingApply{job: *j, text: text, use: u}
	c.omu.Unlock()
}

// retryPending applies each remembered answer once more. Taken (or now in the wrong form) answers are forgotten.
func (c *channel) retryPending() {
	c.omu.Lock()
	todo := make(map[string]*pendingApply, len(c.pending))
	for id, p := range c.pending {
		todo[id] = p
	}
	c.omu.Unlock()
	for id, p := range todo {
		h := c.d.handlers[p.job.kind]
		if h == nil {
			c.forget(id)
			continue
		}
		err := h.apply(p.job, p.text, p.use)
		switch {
		case err == nil:
			c.forget(id)
		case errors.Is(err, errFormat): // cannot happen for the same text; end the row rather than loop
			h.finish(p.job, stateFailed, reasonFormat)
			c.forget(id)
		}
	}
}

func (c *channel) forget(id string) {
	c.omu.Lock()
	delete(c.pending, id)
	c.omu.Unlock()
}

// dropPending ends the rows of the answers still not stored when the channel stops (failed, stopped).
func (c *channel) dropPending() {
	c.omu.Lock()
	left := c.pending
	c.pending = map[string]*pendingApply{}
	c.omu.Unlock()
	for _, p := range left {
		if h := c.d.handlers[p.job.kind]; h != nil {
			h.finish(p.job, stateFailed, reasonStopped)
		}
	}
}
