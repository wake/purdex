package resourcesmod

import (
	"cmp"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/team"
)

// handleGet serves the latest snapshot, optionally narrowed to one session.
// It never samples (hot-path rule): before the first tick ends it answers
// available = false, reason = warming_up.
func (m *Module) handleGet(w http.ResponseWriter, r *http.Request) {
	// The snapshot is the last tick's, whole: mode, available and reason come
	// from one tick, so they cannot contradict each other. A changed setting
	// shows from the next tick (at most one interval later).
	snap := m.current()
	m.addLeases(&snap)
	if r.URL.Query().Get("samples") == "1" {
		snap.Samples = []resources.TrendPoint{}
		for _, s := range m.ringCopy() {
			snap.Samples = append(snap.Samples, resources.TrendPoint{At: s.At.UnixMilli(), Mem: s.Mem, CPU: s.CPU, Pressure: s.Pressure})
		}
	}
	if sid := r.URL.Query().Get("session"); sid != "" {
		snap = onlySession(snap, sid)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(snap)
}

// recentWindow is how far back the snapshot's recent list looks.
const recentWindow = time.Hour

// addLeases fills the snapshot copy with the held leases, the queue (with
// positions) and the last ended leases, from resources.db. Three reads, not
// one statement (store.Listing), so the lists are one view of the rows. A read failure leaves the lists
// out (logged once per run of failures): the host figures stand on their own.
//
// Charge is what admission counts the lease for (Charge: its weight through
// the warmup, then its average, never below the floor); Use is the latest raw
// measurement of its tree, host percent, 0 until the first one.
func (m *Module) addLeases(snap *resources.Snapshot) {
	if m.store == nil {
		return
	}
	nowT := m.now()
	active, waiting, ended, positions, err := m.store.ListingWithPositions(resources.RecentLimit, nowT.Add(-recentWindow).UnixMilli())
	m.noteList(err)
	if err != nil {
		return
	}
	nowMs := nowT.UnixMilli()
	set := resources.DefaultSettings() // before the first tick
	if s := m.lastSettings.Load(); s != nil {
		set = *s
	}
	use := m.leaseUseSnapshot()
	last := m.lastGrantAtMS.Load()
	if set.Mode == resources.ModeAdvise {
		last = m.shadowLastAtMS.Load()
	}
	if last != 0 {
		if until := last + set.Gap().Milliseconds(); until > nowMs {
			snap.GapUntil = until
		}
	}
	reasons := m.reasons.snapshot()
	if set.Mode == resources.ModeAdvise {
		reasons = m.shadowReasons.snapshot() // in advise a waiter is held only in the shadow
	}
	for _, a := range active {
		charge := resources.Charge(resources.Lease{ID: a.ID, Weight: a.Weight, GrantedAt: time.UnixMilli(a.GrantedAt),
			Measured: a.EWMA, Samples: a.Samples}, nowT, set)
		snap.Leases = append(snap.Leases, resources.LeaseView{
			ID: a.ID, Kind: a.Kind, Weight: a.Weight, Charge: charge, Use: use[a.ID].Use,
			SessionID: a.SessionID, AgeS: max(0, (nowMs-a.GrantedAt)/1000), Overrun: a.Overrun,
		})
	}
	places := queuePlaces(positions, waiting)
	// Waiters are the wait-mode requests (no position); a position's requests are
	// listed under it.
	for _, w := range waiting {
		if w.PositionID != "" {
			continue
		}
		view := resources.WaiterView{
			ID: w.ID, Kind: w.Kind, Weight: w.Weight, SessionID: w.SessionID, Position: places[w.ID],
			WaitedS: max(0, (nowMs-w.CreatedAt)/1000), DeadlineInS: max(0, (w.DeadlineAt-nowMs)/1000),
		}
		view.ParkedUntil = w.ParkedUntil
		if rv, ok := reasons[w.ID]; ok {
			view.Reason, view.ReasonSince = string(rv.Reason), rv.Since.UnixMilli()
		}
		snap.Waiters = append(snap.Waiters, view)
	}
	snap.Positions = m.positionViews(positions, active, waiting, places, reasons, nowMs)
	for _, e := range ended {
		snap.Recent = append(snap.Recent, resources.RecentView{
			ID: e.ID, Kind: e.Kind, Weight: e.Weight, SessionID: e.SessionID, EndReason: e.EndReason,
			Overrun: e.Overrun, WouldWait: e.WouldWait, WaitedMS: e.WaitedMS,
			EndedAt: time.UnixMilli(e.EndedAt).UTC(),
		})
	}
}

// noteList logs a failed lease listing when it starts, and its recovery.
func (m *Module) noteList(err error) {
	m.noteMu.Lock()
	failing := err != nil
	changed := failing != m.listFailing
	m.listFailing = failing
	m.noteMu.Unlock()
	switch {
	case !changed:
	case failing:
		m.logf("[resources] list leases for /api/resources: %v", err)
	default:
		m.logf("[resources] listing leases works again")
	}
}

// current is the last published snapshot, or the warming-up one.
func (m *Module) current() resources.Snapshot {
	if s := m.latest.Load(); s != nil {
		return *s
	}
	return resources.Snapshot{
		SampledAt: m.now(),
		Reason:    resources.ReasonWarmingUp,
		Capacity:  resources.Capacity,
		Sessions:  []resources.SessionUse{},
		Mode:      resources.ModeMeasure,
	}
}

// onlySession returns a copy of s whose sessions are just sid's (none for an
// unknown id). The published snapshot is shared and stays untouched.
func onlySession(s resources.Snapshot, sid string) resources.Snapshot {
	kept := []resources.SessionUse{}
	for _, u := range s.Sessions {
		if u.SessionID == sid {
			kept = append(kept, u)
		}
	}
	s.Sessions = kept
	return s
}

// queuePlaces numbers the queue the way the pass orders it: queued positions and
// wait-mode requests together by created_at (a position first on the same
// millisecond). The key is a wait-mode row's id or posKey(position id).
func queuePlaces(positions []positionRow, waiting []leaseRow) map[string]int {
	type entry struct {
		key       string
		createdAt int64
		row       bool
	}
	var entries []entry
	for _, p := range positions {
		if p.State == resources.PosQueued {
			entries = append(entries, entry{posKey(p.ID), p.CreatedAt, false})
		}
	}
	for _, w := range waiting {
		if w.PositionID == "" {
			entries = append(entries, entry{w.ID, w.CreatedAt, true})
		}
	}
	slices.SortStableFunc(entries, func(a, b entry) int {
		if a.createdAt != b.createdAt {
			return cmp.Compare(a.createdAt, b.createdAt)
		}
		if a.row != b.row {
			return cmp.Compare(b2i(a.row), b2i(b.row))
		}
		return cmp.Compare(a.key, b.key)
	})
	out := make(map[string]int, len(entries))
	for i, e := range entries {
		out[e.key] = i + 1
	}
	return out
}

// originsBySession is what the peers module's origin resolver offers besides the
// roots: the addresses (names on the bus) of many sessions with one registry read.
type originsBySession interface {
	ResolveOriginsBySession(sessionIDs []string) (map[string]team.Origin, error)
}

// addressTTL is how long a session's address is kept: a registry read forks ps for
// each entry, and an address changes rarely.
const addressTTL = 30 * time.Second

type cachedAddress struct {
	address string
	until   int64
}

// addressesOf gives the address of each position's session. The ones not known, or
// older than addressTTL, are resolved together by one registry read; a snapshot is
// built on every push, so the read is not repeated per position or per snapshot.
func (m *Module) addressesOf(positions []positionRow, nowMs int64) map[string]string {
	out := make(map[string]string, len(positions))
	m.addrMu.Lock()
	defer m.addrMu.Unlock()
	var missing []string
	for _, p := range positions {
		if c, ok := m.addrCache[p.SessionID]; ok && c.until > nowMs {
			out[p.SessionID] = c.address
			continue
		}
		missing = append(missing, p.SessionID)
	}
	resolver, ok := m.roots.(originsBySession)
	if len(missing) == 0 || !ok {
		return out
	}
	found, err := resolver.ResolveOriginsBySession(missing)
	if err != nil {
		return out // the addresses are for display: try again at the next snapshot
	}
	if m.addrCache == nil {
		m.addrCache = map[string]cachedAddress{}
	}
	for _, id := range missing {
		addr := found[id].Address
		m.addrCache[id] = cachedAddress{address: addr, until: nowMs + addressTTL.Milliseconds()}
		out[id] = addr
	}
	// Forget the sessions that no position names any more.
	if len(m.addrCache) > 4*len(positions)+16 {
		keep := map[string]bool{}
		for _, p := range positions {
			keep[p.SessionID] = true
		}
		for id := range m.addrCache {
			if !keep[id] {
				delete(m.addrCache, id)
			}
		}
	}
	return out
}

// positionViews describes the live positions with their requests (V14): who, which
// command in which directory, the state and how long the window has left. The
// command text is visible to every session of the host (same user's agents); env
// is never stored, so it is never shown.
func (m *Module) positionViews(positions []positionRow, held, waiting []leaseRow, places map[string]int,
	reasons map[string]reasonView, nowMs int64) []resources.PositionView {
	if len(positions) == 0 {
		return nil
	}
	byPos := map[string][]leaseRow{}
	for _, r := range held {
		if r.PositionID != "" {
			byPos[r.PositionID] = append(byPos[r.PositionID], r)
		}
	}
	for _, r := range waiting {
		if r.PositionID != "" {
			byPos[r.PositionID] = append(byPos[r.PositionID], r)
		}
	}
	addresses := m.addressesOf(positions, nowMs)
	out := make([]resources.PositionView, 0, len(positions))
	for _, p := range positions {
		v := resources.PositionView{
			ID: p.ID, SessionID: p.SessionID, Ref: ipeers.RefID(p.SessionID), State: p.State, Place: places[posKey(p.ID)],
			CreatedAt: p.CreatedAt, WindowUntil: p.WindowUntil, FirstStartedAt: p.FirstStartedAt, Commands: p.CommandsStarted,
			Warned: p.Warned, Delivered: p.DeliveredAt > 0, NoticeResult: p.NoticeResult,
		}
		if p.FirstStartedAt > 0 {
			v.SpanS = max(0, (nowMs-p.FirstStartedAt)/1000)
		}
		v.Address = addresses[p.SessionID]
		if rv, ok := reasons[posKey(p.ID)]; ok {
			v.Reason, v.ReasonSince = string(rv.Reason), rv.Since.UnixMilli()
		}
		rows := byPos[p.ID]
		slices.SortStableFunc(rows, func(a, b leaseRow) int { return cmp.Compare(a.CreatedAt, b.CreatedAt) })
		for _, r := range rows {
			state := "queued"
			switch {
			case r.State == resources.StateHeld:
				state = "held"
			case r.Mode == resources.CallAsk && r.OfferedAt > 0:
				state = "asked"
			case r.Mode == resources.CallAsk:
				state = "waiting"
			}
			v.Requests = append(v.Requests, resources.PositionRequestView{
				ID: r.ID, Mode: r.Mode, Kind: r.Kind, Command: r.Command, Cwd: r.Cwd, State: state,
				AgeS: max(0, (nowMs-r.CreatedAt)/1000),
			})
		}
		out = append(out, v)
	}
	return out
}
