package push

import (
	"errors"
	"log"
	"sync"
	"time"

	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/team"
)

// Approval pushes (P-1): one state per card, so a card is pushed once and never after it closed, whichever of the immediate
// path and the 60 s late path gets there. The state has its own mutex, never m.mu (snapshot() takes that one).
//
//	held     at the computer: the late check is scheduled
//	pushing  a push path reserved the card and is between "reserved" and "enqueued"
//	pushed   the push was queued
//	closed   a tombstone: the card closed, so a callback or push path that has not enqueued yet stops

// lateAfterDefault is how long a card waits at the computer before it is pushed anyway (user decision 2026-10-15).
const lateAfterDefault = 60 * time.Second

// apStateTTL is how long a card's state is kept: an id whose close event was dropped (the subscriber's queue drops past 256)
// cannot grow the map without bound.
const apStateTTL = 30 * time.Minute

var errNoReader = errors.New("no open-approvals reader")

type apState int

const (
	apNone apState = iota
	apHeld
	apPushing
	apPushed
	apClosed
)

type apEntry struct {
	state apState
	at    time.Time
}

type approvalPush struct {
	mu       sync.Mutex
	running  bool
	gen      uint64         // bumped by every start: a callback of an earlier run must not push into this one
	inflight sync.WaitGroup // push paths between "reserved" and "done"; Stop waits for them
	states   map[string]*apEntry
}

func (p *approvalPush) start() {
	p.mu.Lock()
	p.running = true
	p.gen++
	p.states = map[string]*apEntry{}
	p.mu.Unlock()
}

// stop ends new work and waits for the push paths already under way.
func (p *approvalPush) stop() {
	p.mu.Lock()
	p.running = false
	p.mu.Unlock()
	p.inflight.Wait()
}

// prune drops the entries older than the TTL. Caller holds p.mu.
func (p *approvalPush) prune(now time.Time) {
	for id, e := range p.states {
		if now.Sub(e.at) > apStateTTL {
			delete(p.states, id)
		}
	}
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func (m *Module) lateDelay() time.Duration { return lateAfterDefault }

func (m *Module) apNow() time.Time {
	if m.apClock != nil {
		return m.apClock()
	}
	return time.Now()
}

func logApproval(a team.Approval, what string) {
	log.Printf("[push] approval %s kind=%s %s", shortID(a.ID), a.Kind, what)
}

// closedApproval is the tombstone: whatever the card's state, it is closed now.
func (m *Module) closedApproval(a team.Approval) {
	p := &m.ap
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.running {
		return
	}
	now := m.apNow()
	p.prune(now)
	p.states[a.ID] = &apEntry{state: apClosed, at: now}
}

// openedApproval decides one newly opened approval of a pushed kind: pushed at once when away, held for the late check when a
// person is at the computer (the three yes / no kinds only; a hook_ask keeps today's rule and is not pushed then).
func (m *Module) openedApproval(a team.Approval, pa push.Approval) {
	p := &m.ap
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return
	}
	p.prune(m.apNow())
	gen := p.gen
	p.mu.Unlock()
	if !m.presence.AtComputer() {
		m.pushApproval(a, pa, gen, false, false)
		return
	}
	if a.Kind == team.KindHookAsk {
		logApproval(a, "not pushed: at_computer")
		return
	}
	if !m.holdApproval(a, pa, gen, false) {
		logApproval(a, "hold set full: pushed at once")
		m.pushApproval(a, pa, gen, false, true)
		return
	}
	logApproval(a, "held 60s (at_computer)")
}

// holdApproval puts a card that has not been pushed (unknown, or reserved by the caller's own push path) into held and
// schedules its late check. False when the hold set cannot take it (full, or stopping): the card is forgotten and the caller
// pushes at once. True also when there is nothing to hold (a repeated opened, closed already, an earlier run).
func (m *Module) holdApproval(a team.Approval, pa push.Approval, gen uint64, ownsReserve bool) bool {
	p := &m.ap
	p.mu.Lock()
	e := p.states[a.ID]
	// ownsReserve: the caller is the push path that holds the card as pushing. Any other caller (an opened event) may only hold a
	// card nobody knows: a repeated opened must never take the state of a push path or a late callback that is under way.
	if !p.running || p.gen != gen || (ownsReserve && (e == nil || e.state != apPushing)) || (!ownsReserve && e != nil) {
		p.mu.Unlock()
		return true
	}
	p.states[a.ID] = &apEntry{state: apHeld, at: m.apNow()} // recorded before the timer exists, so the callback finds it
	p.mu.Unlock()
	if m.holds.after(m.lateDelay(), func() { m.lateApproval(a, pa, gen) }) {
		return true
	}
	p.mu.Lock()
	if e := p.states[a.ID]; e != nil && e.state == apHeld {
		delete(p.states, a.ID)
	}
	p.mu.Unlock()
	return false
}

// lateApproval is the 60 s check: still running, still held, the card still open (read again) -> pushed, whatever the
// presence says. A card that cannot be read is pushed anyway (fail open).
func (m *Module) lateApproval(a team.Approval, pa push.Approval, gen uint64) {
	p := &m.ap
	p.mu.Lock()
	e := p.states[a.ID]
	cur := p.running && p.gen == gen
	ok := cur && e != nil && e.state == apHeld
	closed := cur && e != nil && e.state == apClosed
	p.mu.Unlock()
	if !ok {
		if closed {
			logApproval(a, "not pushed: closed")
		}
		return
	}
	open, err := m.cardOpen(a.ID)
	if m.apAfterRead != nil {
		m.apAfterRead(a.ID)
	}
	switch {
	case err != nil:
		logApproval(a, "read_failed: "+err.Error()+"; late push anyway")
	case !open:
		logApproval(a, "not pushed: closed")
		return
	default:
		logApproval(a, "late push")
	}
	m.pushApproval(a, pa, gen, true, true)
}

// cardOpen reads the open set now; an error when there is no reader or the read fails.
func (m *Module) cardOpen(id string) (bool, error) {
	r := m.reader
	if r == nil {
		return false, errNoReader
	}
	open, err := r.OpenApprovals()
	if err != nil {
		return false, err
	}
	for _, o := range open {
		if o.ID == id {
			return true, nil
		}
	}
	return false, nil
}

// pushApproval is the one push path (immediate or late). It reserves the card first (pushing), enqueues with the sender's
// own last open check, and settles the state; Stop waits for it. skipPresence: the late path and a full hold set push
// whatever the presence says.
func (m *Module) pushApproval(a team.Approval, pa push.Approval, gen uint64, late, skipPresence bool) {
	p := &m.ap
	p.mu.Lock()
	e := p.states[a.ID]
	prev := apNone
	if e != nil {
		prev = e.state
	}
	if !p.running || p.gen != gen || prev == apPushing || prev == apPushed || prev == apClosed {
		current := p.running && p.gen == gen
		p.mu.Unlock()
		switch {
		case !current:
		case prev == apClosed:
			logApproval(a, "not pushed: closed")
		default:
			logApproval(a, "not pushed: already_pushed")
		}
		return
	}
	p.states[a.ID] = &apEntry{state: apPushing, at: m.apNow()}
	p.inflight.Add(1)
	p.mu.Unlock()
	defer p.inflight.Done()
	if m.apAfterReserve != nil {
		m.apAfterReserve(a.ID)
	}
	// settle puts the card back to what it was when nothing was enqueued (unless it closed meanwhile).
	settle := func(to apState) {
		p.mu.Lock()
		defer p.mu.Unlock()
		cur := p.states[a.ID]
		if cur == nil || cur.state != apPushing {
			return // closed meanwhile: the tombstone stays
		}
		if to == apNone {
			delete(p.states, a.ID)
		} else {
			cur.state = to
		}
	}
	// The late timer is spent once it fired: a card put back to held would have no timer and never be pushed (nor ever
	// scheduled again, a repeated opened finds the state), so it goes back to unknown instead.
	if late && prev == apHeld {
		prev = apNone
	}
	snd := m.sender.Load()
	if snd == nil {
		settle(prev)
		return
	}
	if !skipPresence {
		if name := tmuxSessionOf(a.Origin.Tmux); name != "" && m.presence.ShowsName(name) {
			m.deferToLate(a, pa, gen, prev, settle, "shows_name")
			return
		}
	}
	devs := m.snapshot()
	if len(devs) == 0 {
		settle(prev)
		logApproval(a, "not pushed: no_devices")
		return
	}
	ids := make([]string, len(devs))
	for i, d := range devs {
		ids[i] = d.DeviceID
	}
	// Approval: the sender resolves session_code (#2235) from its one snapshot; nothing here may touch tmux.
	ja := &JobApproval{ID: a.ID, TmuxSession: tmuxSessionOf(a.Origin.Tmux), CreatedAt: a.CreatedAt}
	if !skipPresence && m.presence.AtComputer() { // the person came back since the check in openedApproval: nothing is told
		m.deferToLate(a, pa, gen, prev, settle, "at_computer")
		return
	}
	// Rule 8 (§4.4): the ask counts as pushed only when its push was actually queued for the devices.
	if snd.Enqueue(Job{DeviceIDs: ids, Approval: ja, DropIfClosed: true, Make: func(d push.Device) (push.Content, bool) {
		return push.ApprovalContent(pa, d.HostLabel, d.Locale)
	}}) {
		settle(apPushed)
		m.asks.Opened(a)
		if !late {
			logApproval(a, "pushed")
		}
		return
	}
	settle(prev)
	logApproval(a, "not pushed: queue_full")
}

// deferToLate is the answer of the immediate path to "a person is at the computer now" / "a Mac shows this session": the
// same as at the opening, wait for the late check and push then if the card is still open (a hook_ask keeps today's rule
// and is not pushed). Called with the card reserved (pushing) by pushApproval; a full hold set pushes at once.
func (m *Module) deferToLate(a team.Approval, pa push.Approval, gen uint64, prev apState, settle func(apState), why string) {
	if a.Kind == team.KindHookAsk {
		settle(prev)
		logApproval(a, "not pushed: "+why)
		return
	}
	if m.holdApproval(a, pa, gen, true) {
		logApproval(a, "held 60s ("+why+")")
		return
	}
	logApproval(a, "hold set full: pushed at once")
	m.pushApproval(a, pa, gen, false, true)
}
