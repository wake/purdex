package peers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// The reservations of a member's name (name pool v2 spec §5.2). A spawn op gets its name when it is accepted, ten seconds or
// minutes before the session exists, and the name is held with the live ones until the op ends. They live in memory in the
// name service, so reserving, releasing and counting the held names are one section under the pool lock (heldPoolNames reads
// them there). They lapse by themselves ten minutes after their op was created, so a release that never came costs nothing.
// At boot they are rebuilt from the team module's open spawn ops (team.SpawnReservationsKey); the peers module never opens
// team.db.

// reservationTTL is how long a reservation holds its name without a release.
const reservationTTL = 10 * time.Minute

// reservationReadTimeout bounds the rebuild's read of the team module's open ops, which runs with the pool lock held: a team.db
// that does not answer, or a reader that waits for the name service (it holds the pool lock itself), costs the namers this
// long, once per pool-lock section, never more. Module.reservationReadTimeout overrides it (tests).
const reservationReadTimeout = 3 * time.Second

type reservation struct {
	opID, code, tmuxName string
	at                   time.Time
}

func (r reservation) liveAt(now time.Time) bool { return now.Sub(r.at) <= reservationTTL }

// reservationBook is the reservations in memory. Its own mutex is short: ReleaseReservation takes only that, never the pool lock
// (releasing can only free a name, so a pick that read the book a moment before is safe).
type reservationBook struct {
	mu       sync.Mutex
	byOp     map[string]reservation
	rebuilt  bool // the team module's open ops were read (or there is no reader)
	tried    bool // a rebuild was tried in this pool-lock section
	inFlight bool // a read of the open ops is still in flight (one that timed out is not cancelled: it is not started twice)
}

func (b *reservationBook) put(r reservation) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.byOp == nil {
		b.byOp = map[string]reservation{}
	}
	b.byOp[r.opID] = r
}

func (b *reservationBook) release(opID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.byOp, opID)
}

// get is the live reservation of an op.
func (b *reservationBook) get(opID string, now time.Time) (reservation, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.byOp[opID]
	return r, ok && r.liveAt(now)
}

// byTmux is the live reservation of the op whose tmux session is tmuxName.
func (b *reservationBook) byTmux(tmuxName string, now time.Time) (reservation, bool) {
	if tmuxName == "" {
		return reservation{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var best reservation
	found := false
	for _, r := range b.byOp {
		if r.tmuxName != tmuxName || !r.liveAt(now) {
			continue
		}
		// a tmux session name is unique in one tmux server, so two open ops under one name is a fault; the pick must not
		// depend on map order, so the older op (then the smaller id) wins
		if !found || r.at.Before(best.at) || (r.at.Equal(best.at) && r.opID < best.opID) {
			best, found = r, true
		}
	}
	return best, found
}

// holds says whether a live reservation of another op holds the code.
func (b *reservationBook) holds(code, exceptOp string, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, r := range b.byOp {
		if id != exceptOp && r.code == code && r.liveAt(now) {
			return true
		}
	}
	return false
}

// notFuture is at, but never later than now: a clock that ran ahead must not stretch the ten minutes.
func notFuture(at, now time.Time) time.Time {
	if at.After(now) {
		return now
	}
	return at
}

// codes are the names the live reservations hold; the lapsed are dropped on the way.
func (b *reservationBook) codes(now time.Time) map[string]bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]bool, len(b.byOp))
	for id, r := range b.byOp {
		if !r.liveAt(now) {
			delete(b.byOp, id)
			continue
		}
		out[r.code] = true
	}
	return out
}

// newSection starts a pool-lock section: a rebuild that failed in the one before may be tried again.
func (b *reservationBook) newSection() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tried = false
}

// shouldTry says whether this section has still to try the rebuild, and counts it as tried.
func (b *reservationBook) shouldTry() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.rebuilt || b.tried {
		return false
	}
	b.tried = true
	return true
}

// beginRead says whether a read may start: not while an earlier one, given up on at its deadline, has not returned.
func (b *reservationBook) beginRead() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.inFlight {
		return false
	}
	b.inFlight = true
	return true
}

func (b *reservationBook) reading() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.inFlight
}

func (b *reservationBook) endRead() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.inFlight = false
}

func (b *reservationBook) markRebuilt() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rebuilt = true
}

// ensureReservations rebuilds the book from the team module's open ops, once. A reader that fails is tried again at the next use
// (and says so); a build without the team module starts with none. Called with the pool lock held.
func (m *Module) ensureReservations(ctx context.Context) {
	if !m.reservations.shouldTry() {
		return
	}
	var reader ipeers.SpawnReservationReader
	if m.core != nil && m.core.Registry != nil {
		if v, ok := m.core.Registry.Get(team.SpawnReservationsKey); ok {
			reader, _ = v.(ipeers.SpawnReservationReader)
		}
	}
	if reader == nil {
		m.reservations.markRebuilt()
		return
	}
	list, err := m.readOpenReservations(ctx, reader)
	if err != nil {
		m.logf("peers: name: cannot read the open spawn reservations, tried again at the next use: %v", err)
		return
	}
	now := m.now()
	for _, r := range list {
		res := reservation{opID: r.OpID, code: r.Code, tmuxName: r.TmuxName, at: notFuture(r.CreatedAt, now)}
		if !res.liveAt(now) {
			continue
		}
		if _, have := m.reservations.get(r.OpID, now); have { // this process's own reservation is as new as the row
			continue
		}
		if m.reservations.holds(r.Code, r.OpID, now) { // one name, one op: the one already held wins
			m.logf("peers: name: the open spawn op %s reserves %s, which another op holds; not rebuilt", r.OpID, r.Code)
			continue
		}
		m.reservations.put(res)
	}
	m.reservations.markRebuilt()
}

// readOpenReservations is reader.OpenReservations within reservationReadTimeout. The read runs in its own goroutine and is given
// up on at the deadline even if it ignores its context (a reader that calls back into the name service waits for the pool lock
// the caller holds): the caller must get the lock back. A read that finishes late is dropped.
func (m *Module) readOpenReservations(ctx context.Context, reader ipeers.SpawnReservationReader) ([]ipeers.Reservation, error) {
	timeout := m.reservationReadTimeout
	if timeout <= 0 {
		timeout = reservationReadTimeout
	}
	if !m.reservations.beginRead() {
		return nil, errors.New("the previous read of the open spawn ops has not returned")
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	type result struct {
		list []ipeers.Reservation
		err  error
	}
	done := make(chan result, 1)
	go func() {
		defer m.reservations.endRead()
		list, err := reader.OpenReservations(rctx)
		done <- result{list, err}
	}()
	select {
	case r := <-done:
		return r.list, r.err
	case <-rctx.Done():
		return nil, fmt.Errorf("the read did not finish in %v: %w", timeout, rctx.Err())
	}
}

// ReserveMember implements ipeers.NameService.
func (m *Module) ReserveMember(ctx context.Context, leadSessionID, opID, tmuxName string, at time.Time) (string, error) {
	if opID == "" {
		return "", errors.New("peers: reserve: empty op id")
	}
	if err := m.awaitPoolReady(ctx); err != nil {
		return "", err
	}
	unlock, err := m.lockPool(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()
	m.ensureReservations(ctx)
	if r, ok := m.reservations.get(opID, m.now()); ok {
		return r.code, nil
	}
	in, err := m.inputWithHeld(ctx, map[string]bool{})
	if err != nil {
		return "", err
	}
	if lead, ok := m.CodeOf(ctx, leadSessionID); ok {
		in.Pairs = in.LeadPairs[lead]
	}
	code, ok := pickForMember(in)
	if !ok {
		return "", nil
	}
	m.reservations.put(reservation{opID: opID, code: code, tmuxName: tmuxName, at: notFuture(at, m.now())})
	return code, nil
}

// ReserveExact implements ipeers.NameService.
func (m *Module) ReserveExact(ctx context.Context, code, opID, tmuxName string, at time.Time) error {
	if opID == "" {
		return errors.New("peers: reserve: empty op id")
	}
	if err := m.awaitPoolReady(ctx); err != nil {
		return err
	}
	unlock, err := m.lockPool(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	m.ensureReservations(ctx)
	if r, ok := m.reservations.get(opID, m.now()); ok {
		if r.code == code {
			return nil
		}
		return ipeers.ErrNameTaken // the op already holds another name here
	}
	// the pickable check and the held names see one generation of the pool
	m.poolGen.RLock()
	defer m.poolGen.RUnlock()
	if !slices.Contains(ipeers.PickablePool(), code) {
		return ipeers.ErrNameUnknown
	}
	held, err := m.heldPoolNames(ctx, map[string]bool{})
	if err != nil {
		return err
	}
	if held[code] {
		return ipeers.ErrNameTaken
	}
	m.reservations.put(reservation{opID: opID, code: code, tmuxName: tmuxName, at: notFuture(at, m.now())})
	return nil
}

// ReleaseReservation implements ipeers.NameService.
func (m *Module) ReleaseReservation(opID string) { m.reservations.release(opID) }

// tmuxNamesBySession maps a session id to the name of the tmux session its pane is in, from the sessions and owners the
// inventory pass has already read (no tmux call of its own; NP-0 result b′).
func tmuxNamesBySession(sessions []ipeers.SessionSummary, owners map[string]ipeers.Owner) map[string]string {
	out := make(map[string]string, len(owners))
	for _, s := range sessions {
		if o, ok := owners[s.Code]; ok && o.SessionID != "" && s.Name != "" {
			out[o.SessionID] = s.Name
		}
	}
	return out
}
