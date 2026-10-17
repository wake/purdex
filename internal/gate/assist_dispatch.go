package gate

import (
	"context"
	"fmt"
	"time"
)

// AssistTarget asks a command to be sent to an assist host instead of waiting for a slot here (the hidden --assist-host; the
// automatic walk of X2b fills it too).
type AssistTarget struct {
	Host  string
	SSH   string // the ssh program ("" = ssh)
	Git   string // the git program ("" = git)
	Repo  string // the repo's name on the host ("" = the top level's basename)
	Slots int    // how many runs mlab keeps out there at once; 0 = 2
}

const defaultDispatchSlots = 2

// tryAssist sends the ticket's command to the assist host. handled is true when the run is over (the command's result, or the caller
// was cancelled) and code is the exit code. When it is false nothing ran for good: the ticket is back at its place, the dispatch
// slot is let go, and the caller waits on this host as it always did.
func (g *Gate) tryAssist(ctx context.Context, cfg Config, class Class, o Options, ticket *Ticket, d *Decision) (code int, handled bool) {
	a := o.Assist
	repo := a.Repo
	if repo == "" {
		repo = RepoOfDir(g.cwdOf(o))
	}
	if h, held := HeldHost(cfg.Dir, a.Host, repo, g.Now()); held {
		g.say("%s is held off until %s (%s); waiting on this host", a.Host, h.Until.Format("15:04:05"), h.Why)
		return 0, false
	}
	size := a.Slots
	if size <= 0 {
		size = defaultDispatchSlots
	}
	job := newJobID()
	slot, err := g.pool(cfg, class).ServeOut(ticket.Name, DispatchPool(cfg.Dir, a.Host, size, g.Now), g.waiterInfo(o), OutInfo{Host: a.Host, Job: job, At: g.Now()})
	if err != nil || slot == nil {
		if err != nil {
			g.say("%s: no dispatch slot (%v); waiting on this host", a.Host, err)
		} else {
			g.say("%s: all %d dispatch slots are taken; waiting on this host", a.Host, size)
		}
		return 0, false
	}
	res := g.RunAssist(ctx, o, AssistRun{Host: a.Host, Job: job, Repo: repo, SSH: a.SSH, Git: a.Git})
	d.Host, d.TransferS, d.RunS, d.Reclaimed = a.Host, res.TransferS, res.RunS, res.Reclaimed
	// Every way out lets the dispatch slot go: a return to the queue lets it go first (Ticket.Return), the others here.
	back := func(hold time.Duration, why string) {
		if hold > 0 {
			if err := HoldHost(cfg.Dir, a.Host, repo, hold, why, g.Now()); err != nil {
				g.say("%s: the hold could not be written: %v", a.Host, err)
			}
		}
		if err := ticket.Return(slot, g.Holders); err != nil {
			g.say("%s: the ticket could not be put back: %v", a.Host, err)
		}
	}
	switch {
	case res.Cancelled:
		ticket.Drop()
		slot.Release(g.Holders)
		d.Outcome = "assist"
		return res.Code, true
	case res.Reclaimed:
		g.say("%s was taken back; waiting again on mlab", a.Host)
		back(0, "")
		return 0, false
	case !res.Started:
		why := fmt.Sprint(res.Err)
		g.say("%s: %s; waiting on this host", a.Host, why)
		if res.NotAdmitted {
			back(holdAfterNoSlot, why)
		} else {
			back(holdAfterFailure, why)
		}
		return 0, false
	}
	ticket.Drop()
	slot.Release(g.Holders)
	d.Outcome = "assist"
	return res.Code, true
}
