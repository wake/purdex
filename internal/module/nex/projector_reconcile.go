package nex

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/wake/purdex/internal/core"
)

// The daemon's safety reconcile (spec 2026-10-08 §3.7, §8 R3-2).
//
// A delta is a row read again after a bus frame, so a change whose frame
// never came is never pushed: Nexen only logs a failed append, settles some
// states silently (§1 F2, nexen#162), and a kind nobody classified triggers
// nothing. A client would keep the stale row with nothing to tell it. So
// every reconcileInterval, while some client consumes the deltas, the
// projector walks the list and compares it with lastPushed — what it last
// gave the clients — to find such misses, count them and repair them. The
// counting is the point (coordinator decision 3): a missed push is a defect
// to see in the log, not one to paper over.
//
// Per page, read at ver R (each page under its own slot hold, projector_walk.go):
//   - a listed row with no lastPushed entry is unseen: never given to the
//     clients since the projector started (the seed, projector_epoch.go,
//     covers everything that existed at an epoch's start);
//   - a listed row whose status digest differs from its entry, or whose
//     entry says it was removed, is a mismatch suspect;
//   - an entry the page covers but does not list is a mismatch suspect of
//     field presence — unless its last push already showed it archived or
//     removed, which is why it is not listed.
//
// An entry newer than the page (ver > R) is not compared: the clients got
// news newer than the page.
//
// A suspect may be a push in flight — its frame came, its flush is still
// coalescing — so suspects are judged after a grace, unseen ones included:
// one whose entry was replaced meanwhile by a read newer than its page is
// benign. Every other one is counted (nex_delta_reconcile_unseen_total or
// nex_delta_mismatch_total), logged, and pushed through the normal flush
// path — marked dirty with the synthetic cause reconcileCause, read again,
// pushed as any delta is.
//
// Pushes are capped at reconcilePushCap per tick, oldest id first. A burst
// of more would look to a client like a dead connection (it is strict about
// its buffer, §3.5); the rest are found again next tick. The counters count
// every detection, pushed or deferred — a deferred one is counted again
// when it is found again.
//
// What it cannot see: a delta delivered but applied wrongly by a client.
// That is the SPA's own reconcile (§4.5).

const (
	// reconcileInterval is how often the reconcile runs (§3.7: about 120 s).
	reconcileInterval = 120 * time.Second

	// reconcileGrace is how long a suspect has to turn out to be a push in
	// flight. A flush is due at most coalesceCap after its frame, and its
	// read is a fraction of a millisecond (§3.8); a second covers that with
	// room for a slot held by a slow page.
	reconcileGrace = time.Second

	// reconcileSlotWait bounds each page's wait for the slot (§3.8): a tick
	// whose page waited longer gives up its turn, as nothing is late about
	// a reconcile.
	reconcileSlotWait = 2 * time.Second

	// reconcilePushCap is the most pushes one tick schedules (§8 R3-2).
	reconcilePushCap = 32

	// reconcileCause is the cause a reconcile's push carries, so a client
	// (and anyone reading a delta) can tell a repair from a change: it is
	// not a Nexen event kind, and never comes from the bus.
	reconcileCause = "pdx.reconcile"
)

// suspect is one difference a page showed, waiting for its grace.
type suspect struct {
	id     string
	ver    uint64 // the ver of the page that showed it
	unseen bool   // no lastPushed entry at all
	// The first field that differs, as pushed and as the page showed it
	// (field "presence" for a row listed or missing against its entry).
	field, pushed, actual string
}

// reconcile is one tick. It does nothing while no subscriber opted into
// nex.v1 (only those consume deltas), and holds walkMu throughout, so a
// tick never overlaps a seed or another tick. A walk that stopped early —
// a failed page, a slot busy past reconcileSlotWait, the page cap — still
// has its pages judged: each is true as of its own ver. Stop ends a tick
// at once, a grace included, and a tick Stop ended judges nothing.
func (p *projector) reconcile() {
	p.walkMu.Lock()
	defer p.walkMu.Unlock()
	if !p.events.HasSubscribersWanting(core.FeatureNexV1) {
		return
	}
	var suspects []suspect
	prev := ""
	err := p.walk(p.ctx, "reconcile", reconcileSlotWait, func(pg walkPage) error {
		suspects = append(suspects, p.suspectsIn(pg, prev)...)
		prev = pg.upTo
		return nil
	})
	if p.ctx.Err() != nil {
		return
	}
	if err != nil {
		p.logf("nex-delta: reconcile walk stopped: %v", err)
	}
	if len(suspects) > 0 && !p.pause(p.timing.grace) {
		return
	}
	p.judge(suspects)
	hold, wait := p.slot.maxima()
	p.logf("nex-delta: reconcile: nex_delta_mismatch_total=%d nex_delta_reconcile_unseen_total=%d nex_delta_slot_max_hold_ms=%d nex_delta_slot_max_wait_ms=%d",
		p.mismatchTotal.Load(), p.unseenTotal.Load(), hold, wait)
}

// suspectsIn compares one page with lastPushed (see the top of this file),
// prevUpTo being the previous page's upTo.
func (p *projector) suspectsIn(pg walkPage, prevUpTo string) []suspect {
	var out []suspect
	listed := make(map[string]bool, len(pg.rows))
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range pg.rows {
		listed[r.id] = true
		cur, ok := p.pushed[r.id]
		switch {
		case !ok:
			out = append(out, suspect{id: r.id, ver: pg.ver, unseen: true})
		case cur.ver > pg.ver:
		case cur.removed:
			out = append(out, suspect{id: r.id, ver: pg.ver, field: "presence", pushed: "absent", actual: "present"})
		default:
			if field, was, is, differs := diffDigest(cur.digest, r.digest); differs {
				out = append(out, suspect{id: r.id, ver: pg.ver, field: field, pushed: was, actual: is})
			}
		}
	}
	for id, cur := range p.pushed {
		if !listed[id] && pg.covers(prevUpTo, id) && cur.ver < pg.ver && !cur.removed && !cur.digest.Archived {
			out = append(out, suspect{id: id, ver: pg.ver, field: "presence", pushed: "present", actual: "absent"})
		}
	}
	return out
}

// judge settles suspects once their grace is over: a suspect whose entry
// is now newer than its page is benign; every other one is counted, logged,
// and — the first reconcilePushCap of them by id — marked dirty with
// reconcileCause, due at once.
func (p *projector) judge(suspects []suspect) {
	sort.Slice(suspects, func(i, j int) bool { return suspects[i].id < suspects[j].id })
	var lines, push []string
	p.mu.Lock()
	for _, s := range suspects {
		if cur, ok := p.pushed[s.id]; ok && cur.ver > s.ver {
			continue
		}
		if s.unseen {
			lines = append(lines, fmt.Sprintf("nex-delta: unseen exec=%s total=%d", s.id, p.unseenTotal.Add(1)))
		} else {
			lines = append(lines, fmt.Sprintf("nex-delta: missed push exec=%s field=%s pushed=%s actual=%s total=%d",
				s.id, s.field, s.pushed, s.actual, p.mismatchTotal.Add(1)))
		}
		if len(push) < reconcilePushCap {
			push = append(push, s.id)
		}
	}
	now := p.now()
	for _, id := range push {
		p.markLocked(id, mark{kinds: []string{reconcileCause}, at: now, reMark: true})
	}
	p.mu.Unlock()
	for _, l := range lines {
		p.logf("%s", l)
	}
	if len(push) > 0 {
		p.wake()
	}
}

// diffDigest names the first field in which actual differs from pushed,
// with both values, in the order of §3.7's list.
func diffDigest(pushed, actual rowDigest) (field, was, is string, differs bool) {
	switch {
	case pushed.State != actual.State:
		return "state", orNone(pushed.State), orNone(actual.State), true
	case pushed.PermissionRequest != actual.PermissionRequest:
		return "pending_permission", orNone(pushed.PermissionRequest), orNone(actual.PermissionRequest), true
	case pushed.Archived != actual.Archived:
		return "archived", strconv.FormatBool(pushed.Archived), strconv.FormatBool(actual.Archived), true
	case pushed.TurnCount != actual.TurnCount:
		return "turn_count", strconv.FormatInt(pushed.TurnCount, 10), strconv.FormatInt(actual.TurnCount, 10), true
	case pushed.LastTurnReason != actual.LastTurnReason:
		return "last_turn_reason", orNone(pushed.LastTurnReason), orNone(actual.LastTurnReason), true
	case pushed.TerminalReason != actual.TerminalReason:
		return "terminal_reason", orNone(pushed.TerminalReason), orNone(actual.TerminalReason), true
	}
	return "", "", "", false
}

// orNone spells an empty value out for the log.
func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// pause waits d, or until the projector stops; it reports whether the wait
// ran its course.
func (p *projector) pause(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-p.ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
