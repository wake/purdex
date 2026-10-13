package resourcesmod

import (
	"slices"
	"strings"
	"time"

	"github.com/wake/purdex/internal/resources"
)

// The advise shadow queue (lease v2 spec D-3′ "Mode advise"). In advise every
// request is granted at once, for real. What v2 would have done runs next to it,
// in memory: every request joins the shadow when the pass first sees it, each
// pass runs AdmitV2 over the shadow (the real zone and trend, the real held
// rows as "running", the gap counted from the shadow's own grants), and the
// shadow's grant writes v2_wait_ms on the row. A row that ends before its
// shadow grant records the time it had waited with v2_wait_open = 1, a lower
// bound. would_wait says whether the shadow did not grant the request in the
// pass of its own POST, and wait_reasons holds the shadow's reasons.
//
// Known bias: in advise the real commands are already running, so the shadow
// sees "running" earlier than lease mode would; its waits are an upper
// estimate. The shadow is lost on restart: rows pending in it then record
// nothing (v2_wait_ms stays -1).
//
// The shadow never slows the real pass. Under stateMu it only works out
// verdicts in memory; writing them to resources.db (and reading an ended row
// back) happens after the lock is released, in flushShadow, and a write that
// fails is retried at the next pass.

type shadowQueue struct {
	created map[string]time.Time // id -> created_at of the request
	last    time.Time            // the shadow's own newest grant
	// atOnce holds the ids the shadow granted in the very pass that first saw
	// them: the real grant of such a request may come in a later round (it was
	// cut for a baseline, say) and would_wait is still false.
	atOnce map[string]bool
	// pending are verdicts worked out and not yet written; gone are rows that
	// left the real tables before the shadow granted them (ended) and still
	// have to be settled.
	pending []shadowVerdict
	gone    []shadowGone
}

type shadowVerdict struct {
	id       string
	waitMS   int64
	open     bool
	reasons  string
	attempts int
}

type shadowGone struct {
	id       string
	created  time.Time
	attempts int
}

// shadowWriteAttempts is how often a verdict that cannot be written is tried.
const shadowWriteAttempts = 5

// shadowActive says whether the shadow holds anything, a queued request or the
// time of its last grant (which would hold back the first request of a later
// advise). Lock-free.
func (m *Module) shadowActive() bool {
	return m.shadowLen.Load() > 0 || m.shadowLastAtMS.Load() != 0 || m.shadowUnwritten.Load() > 0
}

// dropShadow forgets the whole shadow (a restart, or a switch away from advise).
func (m *Module) dropShadow() {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	m.shadow = shadowQueue{}
	m.shadowGen++ // a flush that is writing right now must not bring anything back
	m.shadowLastAtMS.Store(0)
	m.shadowReasons.reset()
	m.shadowLen.Store(0)
	m.shadowUnwritten.Store(0)
}

// shadowStep is the shadow's part of one advise pass: joins the new requests,
// notes the rows that left before their shadow grant, and grants at most one.
// It works in memory only. It returns the remaining gap the decision saw.
// stateMu is held.
func (m *Module) shadowStep(set resources.Settings, held, waiting []leaseRow, zone resources.Zone, trend resources.Trend, now time.Time) (gapLeft time.Duration) {
	sh := &m.shadow
	if sh.created == nil {
		sh.created = map[string]time.Time{}
	}
	if sh.atOnce == nil {
		sh.atOnce = map[string]bool{}
	}
	joined := map[string]bool{}
	present := make(map[string]bool, len(held)+len(waiting))
	for _, r := range held {
		present[r.ID] = true
	}
	for _, w := range waiting {
		present[w.ID] = true
		if _, ok := sh.created[w.ID]; !ok {
			sh.created[w.ID] = time.UnixMilli(w.CreatedAt)
			joined[w.ID] = true
		}
	}
	for id := range sh.atOnce {
		if !present[id] {
			delete(sh.atOnce, id) // the request is gone without a real grant
		}
	}
	// A row in the shadow that is neither held nor waiting ended before the
	// shadow granted it: its wait so far is a lower bound. Its end time is
	// read back outside the lock.
	for id, created := range sh.created {
		if !present[id] {
			sh.gone = append(sh.gone, shadowGone{id: id, created: created})
			delete(sh.created, id)
		}
	}

	if len(sh.created) > 0 {
		ws := make([]resources.V2Waiter, 0, len(sh.created))
		for id, created := range sh.created {
			ws = append(ws, resources.V2Waiter{ID: id, CreatedAt: created})
		}
		slices.SortFunc(ws, func(a, b resources.V2Waiter) int {
			if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
				return c
			}
			return strings.Compare(a.ID, b.ID)
		})
		var grantID string
		var reasons map[string]resources.V2Reason
		grantID, gapLeft, reasons = resources.AdmitV2(zone, trend, len(held) > 0, sh.last, ws, now, set)
		for id, r := range reasons {
			m.shadowReasons.observe(id, r, now)
		}
		if grantID != "" {
			sh.pending = append(sh.pending, shadowVerdict{id: grantID, waitMS: max(0, now.Sub(sh.created[grantID]).Milliseconds()),
				reasons: m.shadowReasons.peek(grantID, now)})
			delete(sh.created, grantID)
			m.shadowReasons.drop(grantID)
			sh.last = now
			if joined[grantID] {
				sh.atOnce[grantID] = true
			}
			m.shadowLastAtMS.Store(now.UnixMilli())
		}
	}
	m.shadowLen.Store(int32(len(sh.created)))
	m.shadowUnwritten.Store(int32(len(sh.pending) + len(sh.gone)))
	return gapLeft
}

// writeShadow persists one verdict (a seam for tests).
func (m *Module) writeShadow(id string, waitMS int64, open bool, reasons string) error {
	if m.shadowSet != nil {
		return m.shadowSet(id, waitMS, open, reasons)
	}
	return m.store.SetShadow(id, waitMS, open, reasons)
}

// flushShadow writes what shadowStep worked out, with stateMu NOT held: the
// pending verdicts, and the lower bounds of the rows that ended first (their
// end time is read from the table). A write that fails goes back for the next
// pass, up to shadowWriteAttempts times.
func (m *Module) flushShadow() {
	m.stateMu.Lock()
	pend, gone := m.shadow.pending, m.shadow.gone
	gen := m.shadowGen
	m.shadow.pending, m.shadow.gone = nil, nil
	m.shadowUnwritten.Store(0)
	m.stateMu.Unlock()
	if len(pend) == 0 && len(gone) == 0 {
		return
	}

	var retryV []shadowVerdict
	var retryG []shadowGone
	var rejoin []shadowGone
	for _, v := range pend {
		if err := m.writeShadow(v.id, v.waitMS, v.open, v.reasons); err != nil {
			m.logf("[resources] shadow: record %s: %v", v.id, err)
			if v.attempts++; v.attempts < shadowWriteAttempts {
				retryV = append(retryV, v)
			}
		}
	}
	for _, g := range gone {
		row, ok, err := m.store.Get(g.id)
		switch {
		case err != nil:
			m.logf("[resources] shadow: read %s: %v", g.id, err)
			if g.attempts++; g.attempts < shadowWriteAttempts {
				retryG = append(retryG, g)
			}
			continue
		case !ok:
			m.shadowReasons.drop(g.id) // pruned: nothing to record
			continue
		case row.State != resources.StateEnded:
			rejoin = append(rejoin, g) // it moved between the reads: still the shadow's
			continue
		}
		endedAt := time.UnixMilli(row.EndedAt)
		reasons := m.shadowReasons.peek(g.id, endedAt)
		if err := m.writeShadow(g.id, max(0, row.EndedAt-g.created.UnixMilli()), true, reasons); err != nil {
			m.logf("[resources] shadow: record %s: %v", g.id, err)
			if g.attempts++; g.attempts < shadowWriteAttempts {
				retryG = append(retryG, g)
			}
			continue
		}
		m.shadowReasons.drop(g.id)
	}

	if len(retryV)+len(retryG)+len(rejoin) == 0 {
		return
	}
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	if m.shadowGen != gen {
		return // the shadow was dropped while this flush was writing: its leftovers are stale
	}
	m.shadow.pending = append(m.shadow.pending, retryV...)
	m.shadow.gone = append(m.shadow.gone, retryG...)
	if m.shadow.created == nil {
		m.shadow.created = map[string]time.Time{}
	}
	for _, g := range rejoin {
		m.shadow.created[g.id] = g.created
	}
	m.shadowLen.Store(int32(len(m.shadow.created)))
	m.shadowUnwritten.Store(int32(len(m.shadow.pending) + len(m.shadow.gone)))
}
