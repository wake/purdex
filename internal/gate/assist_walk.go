package gate

import (
	"context"
	"time"
)

// AssistWalk lets a waiting gate offer its ticket to the assist hosts by itself (the automatic walk, plan X2b). The caller gives it what
// only it can know; the gate decides with PickAssist, ProbeAssist and tryAssist. A gate with no AssistWalk never looks at an assist
// host (every host that is not mlab has none).
type AssistWalk struct {
	// Usable are the assist hosts that are on, ready for this command's repo and allowed for its session, by alias (the daemon's
	// answer, with the host records of assist.json). Empty when there are none or the daemon cannot be asked.
	Usable func() map[string]AssistHost
	// Reserved says mlab is reserved at now (assist.json's reservation).
	Reserved func(now time.Time) bool
	SSH, Git string // the programs ("" = ssh, git); tests put fakes here
}

// assistWalkEvery is how often a ticket that is still waiting offers itself again: each look costs one ssh.
const assistWalkEvery = 30 * time.Second

// walkAssist offers the ticket to the hosts PickAssist names, in order. handled is true when the command's run is over (its result
// is code). Otherwise the ticket is where it was, and the caller waits for a slot here. A host that is not fit now (its gate would
// wait, it is over its limits, or the ssh failed) is held off as ProbeAssist says, and the walk goes on to the next.
func (g *Gate) walkAssist(ctx context.Context, cfg Config, class Class, o Options, ticket *Ticket, d *Decision, wouldWait bool) (code int, handled bool) {
	w := o.Walk
	hosts := w.Usable()
	if len(hosts) == 0 {
		return 0, false
	}
	repo := RepoOfDir(g.cwdOf(o))
	now := g.Now()
	reserved := w.Reserved != nil && w.Reserved(now)
	for _, name := range PickAssist(cfg.Dir, PickInput{Hosts: hosts, Argv: o.Argv, Class: class, Repo: repo, WouldWait: wouldWait, Reserved: reserved, Now: now}) {
		h := hosts[name]
		// A host with no free dispatch slot is not a candidate (plan X2b item 3): this costs no ssh, and ServeOut decides for good.
		if free, err := DispatchPool(cfg.Dir, name, dispatchSize(h.Slots), g.Now).freeCapacity(); err == nil && free <= 0 {
			continue
		}
		p := g.ProbeAssist(ctx, AssistRun{Host: name, SSH: w.SSH}, h, o.Argv)
		if !p.OK {
			g.say("%s", p.Why)
			if p.Hold > 0 {
				if err := HoldHost(cfg.Dir, name, repo, p.Hold, p.Why, g.Now()); err != nil {
					g.say("%s: the hold could not be written: %v", name, err)
				}
			}
			continue
		}
		o.Assist = &AssistTarget{Host: name, Dest: h.SSH, SSH: w.SSH, Git: w.Git, Repo: repo, Slots: h.Slots}
		if code, handled := g.tryAssist(ctx, cfg, class, o, ticket, d); handled {
			return code, true
		}
	}
	return 0, false
}
