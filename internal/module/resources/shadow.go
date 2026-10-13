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
// All of it runs under stateMu, on the pass.

type shadowQueue struct {
	created map[string]time.Time // id -> created_at of the request
	last    time.Time            // the shadow's own newest grant
}

// dropShadow forgets the whole shadow (a restart, or a switch away from advise).
func (m *Module) dropShadow() {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	m.shadow = shadowQueue{}
	m.shadowLastAtMS.Store(0)
	m.shadowReasons.reset()
	m.shadowLen.Store(0)
}

// shadowStep is the shadow's part of one advise pass: joins the new requests,
// settles the rows that ended, and grants at most one. It returns the ids the
// shadow granted in this pass. stateMu is held.
func (m *Module) shadowStep(set resources.Settings, held, waiting []leaseRow, zone resources.Zone, trend resources.Trend, now time.Time) map[string]bool {
	sh := &m.shadow
	if sh.created == nil {
		sh.created = map[string]time.Time{}
	}
	present := make(map[string]bool, len(held)+len(waiting))
	for _, r := range held {
		present[r.ID] = true
	}
	for _, w := range waiting {
		present[w.ID] = true
		if _, ok := sh.created[w.ID]; !ok {
			sh.created[w.ID] = time.UnixMilli(w.CreatedAt)
		}
	}
	// A row in the shadow that is neither held nor waiting ended before the
	// shadow granted it: its wait so far is a lower bound.
	for id, created := range sh.created {
		if present[id] {
			continue
		}
		row, ok, err := m.store.Get(id)
		if err != nil {
			m.logf("[resources] shadow: read %s: %v", id, err)
			continue
		}
		if ok && row.State != resources.StateEnded {
			continue // moved between the reads; the next pass settles it
		}
		if ok {
			if err := m.store.SetShadow(id, max(0, row.EndedAt-created.UnixMilli()), true, m.shadowReasons.peek(id, time.UnixMilli(row.EndedAt))); err != nil {
				m.logf("[resources] shadow: record %s: %v", id, err)
			}
		}
		delete(sh.created, id)
		m.shadowReasons.drop(id)
	}

	granted := map[string]bool{}
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
		grantID, _, reasons := resources.AdmitV2(zone, trend, len(held) > 0, sh.last, ws, now, set)
		for id, r := range reasons {
			m.shadowReasons.observe(id, r, now)
		}
		if grantID != "" {
			wait := max(0, now.Sub(sh.created[grantID]).Milliseconds())
			if err := m.store.SetShadow(grantID, wait, false, m.shadowReasons.peek(grantID, now)); err != nil {
				m.logf("[resources] shadow: record %s: %v", grantID, err)
			}
			delete(sh.created, grantID)
			m.shadowReasons.drop(grantID)
			sh.last = now
			m.shadowLastAtMS.Store(now.UnixMilli())
			granted[grantID] = true
		}
	}
	m.shadowLen.Store(int32(len(sh.created)))
	return granted
}
