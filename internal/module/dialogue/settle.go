package dialogue

import "errors"

// An answer the kind could not store (its apply returned an error other than errFormat or errGone, e.g. the store refused
// the write) must not stall the session's queue and must not be lost: the lease is released, the answer is kept here and
// the reaper applies it again at each tick until the kind takes it, up to maxApplyRetries times. The base (workbook
// settle.go) kept an "orphan" entry and retried marking it failed(store); here the whole apply is retried, since the
// answer is the expensive part. Retries run only in the reaper goroutine's tick (never from Next or Result), and an
// answer is claimed while it is being applied, so the same answer never has two applies running.

type pendingApply struct {
	job   job
	text  string
	use   usage
	tries int  // retries made so far (the first apply is not counted)
	busy  bool // claimed by a retry in progress; omu guards it
}

// keepPending remembers an answer whose apply failed.
func (c *channel) keepPending(j *job, text string, u usage) {
	c.omu.Lock()
	c.pending[j.id] = &pendingApply{job: *j, text: text, use: u}
	c.omu.Unlock()
}

// retryPending applies each remembered answer that nobody else is applying once more. Taken answers are forgotten and
// handed to the kind's after; an answer that is gone, in the wrong form, or tried maxApplyRetries times ends its row.
func (c *channel) retryPending() {
	c.omu.Lock()
	var todo []*pendingApply
	for _, p := range c.pending {
		if !p.busy {
			p.busy = true
			todo = append(todo, p)
		}
	}
	c.omu.Unlock()
	for _, p := range todo {
		c.retryOne(p)
	}
}

func (c *channel) retryOne(p *pendingApply) {
	id := p.job.id
	h := c.d.handlers[p.job.kind]
	if h == nil {
		c.forget(id)
		return
	}
	err := h.apply(p.job, p.text, p.use)
	switch {
	case err == nil:
		c.forget(id)
		h.after(p.job)
	case errors.Is(err, errGone):
		h.finish(p.job, stateFailed, reasonStore)
		c.forget(id)
	case errors.Is(err, errFormat): // cannot happen for the same text; end the row rather than loop
		h.finish(p.job, stateFailed, reasonFormat)
		c.forget(id)
	default:
		c.omu.Lock()
		p.tries++
		giveUp := p.tries >= maxApplyRetries
		if !giveUp {
			p.busy = false
		}
		c.omu.Unlock()
		if giveUp {
			c.d.logf("[dialogue] an answer could not be stored after %d tries (kind %s): %v", p.tries, p.job.kind, err)
			h.finish(p.job, stateFailed, reasonStore)
			c.forget(id)
		}
	}
}

func (c *channel) forget(id string) {
	c.omu.Lock()
	delete(c.pending, id)
	c.omu.Unlock()
}

// dropPending ends the rows of the answers still not stored when the channel stops (failed, stopped). The kind's finish
// only moves a row that is still pending, so it cannot cover an answer that was stored meanwhile.
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
