package gate

import (
	"context"
	"os"
	"time"
)

// AssistWalk lets a waiting gate offer its ticket to the assist hosts by itself (the automatic walk, plan X2b). The caller gives it what
// only it can know; the gate decides with PickAssist, ProbeAssist and tryAssist. A gate with no AssistWalk never looks at an assist
// host (every host that is not mlab has none).
type AssistWalk struct {
	// Usable are the assist hosts that are on, ready for this command's repo and allowed for its session, by alias (the daemon's
	// answer, with the host records of assist.json). Empty when there are none or the daemon cannot be asked. It must not wait long:
	// it is called from the wait loop.
	Usable func() map[string]AssistHost
	// Reserved says mlab is reserved at now (assist.json's reservation).
	Reserved func(now time.Time) bool
	SSH, Git string // the programs ("" = ssh, git); tests put fakes here
}

// assistWalkEvery is how often a ticket that is still waiting offers itself again: each look costs one ssh.
const assistWalkEvery = 30 * time.Second

// walkEligible says whether a ticket may be offered to an assist host now. The queue's own rules still hold for it: a class with no
// slot waits for the count to be raised whatever a host could do, and a ticket on hold is not served anywhere. (The conflict rules of
// mlab are about what runs on mlab, and the host's own gate applies its own at the probe. The serving order is walkAhead's.)
func (g *Gate) walkEligible(cfg Config, class Class, ticket *Ticket) bool {
	if cfg.Slots.Paused(class) {
		return false
	}
	return rankOfTicket(g.pool(cfg, class).peekSnapshot(), ticket.Name).Rank != RankHold
}

// walkAhead says whether a ticket in front of this one in the serving order (front, then normal, then back, each by arrival) is for a
// command the host takes: then that one has the first claim on the host's slot and this one waits. A ticket in front whose command the
// host does not take cannot use the slot, so it holds nobody back ("those behind it may take slots it cannot use").
func walkAhead(snap Snapshot, ticket string, h AssistHost) bool {
	for _, e := range snap.Queue {
		if e.Name == ticket {
			return false
		}
		if e.Rank.Rank != RankHold && h.Takes(e.Argv) {
			return true
		}
	}
	return false
}

// walkAssist offers the ticket to the hosts PickAssist names, in order. handled is true when the command's run is over (its result
// is code). Otherwise the ticket is where it was, and the caller waits for a slot here. A host that is not fit now (no free dispatch
// slot, its gate would wait, it is over its limits, or the ssh failed) is left out or held off as ProbeAssist says, and the walk goes
// on to the next. A signal while the host is being asked ends the command (nothing was sent).
func (g *Gate) walkAssist(ctx context.Context, cfg Config, class Class, o Options, ticket *Ticket, d *Decision, wouldWait bool, sigs <-chan os.Signal) (code int, handled bool) {
	w := o.Walk
	if !g.walkEligible(cfg, class, ticket) {
		return 0, false
	}
	hosts := w.Usable()
	if len(hosts) == 0 {
		return 0, false
	}
	repo := RepoOfDir(g.cwdOf(o))
	now := g.Now()
	reserved := w.Reserved != nil && w.Reserved(now)
	for _, name := range PickAssist(cfg.Dir, PickInput{Hosts: hosts, Argv: o.Argv, Class: class, Repo: repo, WouldWait: wouldWait, Reserved: reserved, Now: now}) {
		h := hosts[name]
		if walkAhead(g.pool(cfg, class).peekSnapshot(), ticket.Name, h) {
			continue // someone in front of this ticket has the first claim on this host
		}
		// A host with no free dispatch slot is not a candidate (plan X2b item 3): this costs no ssh, and ServeOut decides for good.
		if _, free := observeDispatch(DispatchPool(cfg.Dir, name, dispatchSize(h.Slots), g.Now)); free <= 0 {
			continue
		}
		p, sig := g.probeUntilSignal(ctx, AssistRun{Host: name, SSH: w.SSH}, h, o.Argv, sigs)
		if sig != 0 {
			return sig, true
		}
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
		if code, handled := g.tryAssist(ctx, cfg, class, o, ticket, d, sigs); handled {
			return code, true
		}
	}
	return 0, false
}

// probeUntilSignal runs ProbeAssist, but ends it at once when a signal comes (or the caller's context ends): the second result is then
// the exit code of the interrupted command (128+n, 130 for the context), and 0 when the probe answered.
func (g *Gate) probeUntilSignal(ctx context.Context, a AssistRun, h AssistHost, argv []string, sigs <-chan os.Signal) (AssistProbe, int) {
	pctx, cancel := context.WithCancel(ctx)
	defer cancel()
	res := make(chan AssistProbe, 1)
	go func() { res <- g.ProbeAssist(pctx, a, h, argv) }()
	select {
	case p := <-res:
		return p, 0
	case sig := <-sigs:
		cancel()
		<-res
		return AssistProbe{}, 128 + signalNumber(sig)
	case <-ctx.Done():
		cancel()
		<-res
		return AssistProbe{}, 130
	}
}
