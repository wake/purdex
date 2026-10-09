package workbook

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/wake/purdex/internal/peers"
)

// Refresh (spec §5.6, plan D11 / D14). Asked by the Mac's 「重整」 control or by the mod's /workbook refresh, queued like a
// job, run by a session whose mod announced workbook.refresh.

const (
	refreshTimeout  = 90_000           // ms: a fork sends the whole conversation, longer than a summary
	refreshHeadWait = 40 * time.Second // a refresh at the head with no capable session this long → failed: lost
)

// ReasonNothingToFork: failed — the session has not answered since it started or since /clear (the mod's fork said so).
const ReasonNothingToFork = "nothing_to_fork"

var (
	// ErrNotLive: no live session of the conversation whose mod announced workbook.refresh (a 409 not_live).
	ErrNotLive = errors.New("workbook: no live session of the conversation can run a refresh")
	// ErrStopped: the module is stopping and takes no new work.
	ErrStopped = errors.New("workbook: the module is stopping")
	// ErrRefreshPending: the conversation has a refresh under way (a 409 refresh_pending).
	ErrRefreshPending = errors.New("workbook: a refresh of the conversation is already pending")
)

// CapSession is a session whose live stream announced a capability, and when.
type CapSession struct {
	SID string
	At  time.Time
}

// refreshState is the engine's refresh bookkeeping.
type refreshState struct {
	rmu       sync.Mutex // one request at a time: the pending check, the insert and the enqueue are one step
	seq       int64
	availMu   sync.Mutex
	lastAvail map[string]bool // conv_key -> the last refresh_available value sent (absent = false)
}

// refreshSessions is the sessions that can run a refresh now.
func (e *Engine) refreshSessions() []CapSession {
	if e.d.RefreshSessions == nil {
		return nil
	}
	return e.d.RefreshSessions()
}

func (e *Engine) refreshCapable(sessionID string) bool {
	for _, c := range e.refreshSessions() {
		if c.SID == sessionID {
			return true
		}
	}
	return false
}

// refreshConvs is the conversations that have a session able to run a refresh, each with its newest-announcing session.
func (e *Engine) refreshConvs() map[string]CapSession {
	out := map[string]CapSession{}
	for _, c := range e.refreshSessions() {
		conv, err := e.convKey(c.SID)
		if err != nil {
			e.d.Logf("[workbook] conversation of a session: %v", err)
			continue
		}
		if cur, ok := out[conv]; !ok || c.At.After(cur.At) {
			out[conv] = c
		}
	}
	return out
}

// RefreshAvailable: some session of the conversation can run a refresh now (the conversation answer's refresh_available).
func (e *Engine) RefreshAvailable(conv string) bool {
	_, ok := e.refreshConvs()[conv]
	return ok
}

// RequestRefresh queues a refresh of the conversation of sessionID and returns its entry id. caller is the session that
// asked (the mod's /workbook refresh; "" for the Mac): it runs the job when it can, else the capable session whose stream
// announced workbook.refresh most recently. ErrNotLive when none can; ErrRefreshPending when one is already under way.
func (e *Engine) RequestRefresh(sessionID, caller string) (int64, error) {
	conv, err := e.convKey(sessionID)
	if err != nil {
		return 0, fmt.Errorf("conversation of a session: %w", err)
	}
	var run string
	var newest time.Time
	for _, c := range e.refreshSessions() {
		cc, err := e.convKey(c.SID)
		if err != nil || cc != conv {
			continue
		}
		if c.SID == caller {
			run, newest = c.SID, time.Time{}
			break
		}
		if run == "" || c.At.After(newest) {
			run, newest = c.SID, c.At
		}
	}
	if run == "" {
		return 0, ErrNotLive
	}
	e.rf.rmu.Lock()
	defer e.rf.rmu.Unlock()
	pending, err := e.d.Store.PendingRefresh(conv)
	if err != nil {
		return 0, err
	}
	if pending {
		return 0, ErrRefreshPending
	}
	now := e.d.Now()
	e.rf.seq++
	row := Entry{ConvKey: conv, HostID: e.d.HostID, Provider: "claude", SessionID: run, Ref: peers.RefID(run), PromptVer: PromptVersion,
		Kind: KindRefresh, TurnID: fmt.Sprintf("r:%d-%d", now.UnixMilli(), e.rf.seq), TurnAt: now.UnixMilli()}
	if e.d.Seats != nil {
		if seat, err := e.d.Seats.SeatOf(run); err != nil {
			e.d.Logf("[workbook] seat of a session: %v", err)
		} else {
			row.TeamID, row.Role = seat.TeamID, seat.Role
		}
	}
	id, _, err := e.d.Store.InsertPending(row)
	if err != nil {
		return 0, err
	}
	if !e.enqueue(&job{conv: conv, entryID: id, session: run, kind: JobRefresh, attempt: 1}) {
		return 0, ErrStopped // the engine stopped between the request and the queue: the row is already failed (stopped)
	}
	return id, nil
}

// repoint moves a pending refresh's row to the session that takes the job: session, ref, team and role together (the row
// says who ran it). moved is false when the entry is no longer pending.
func (e *Engine) repoint(j *job, sessionID string) (moved bool, err error) {
	teamID, role := "", ""
	if e.d.Seats != nil {
		if seat, err := e.d.Seats.SeatOf(sessionID); err != nil {
			e.d.Logf("[workbook] seat of a session: %v", err)
		} else {
			teamID, role = seat.TeamID, seat.Role
		}
	}
	return e.d.Store.RepointSession(j.entryID, sessionID, peers.RefID(sessionID), teamID, role)
}

// applyRefresh maps a refresh job's result onto its entry (spec §5.6): the status replaced, the todo changes applied and
// the entry written in one transaction; no push, no thing of its own.
func (e *Engine) applyRefresh(j *job, l *lease, r Result) *job {
	if !r.Answered {
		return e.failedCall(j, r, l.timeoutMS)
	}
	res, err := ParseRefreshJSON(r.Text)
	if err != nil {
		return e.formatFailure(j)
	}
	fixed := RepairRefresh(res)
	tr, ok, err := e.d.Store.FinishRefresh(j.entryID, RefreshDone{Status: fixed.Status, Todos: ResolveTodos(fixed.Todos, l.ids),
		Usage: j.usage, LatencyMS: j.lat})
	if err != nil {
		e.d.Logf("[workbook] finish a refresh: %v", err)
		e.finishUnrun(j, StateFailed, ReasonStore)
		return nil
	}
	if ok {
		e.logTodos(tr)
	}
	return nil
}

// refreshWaiting: some queue has a refresh at its head with no lease (the only case failStaleRefreshHeads cares about).
func (e *Engine) refreshWaiting() bool {
	e.qmu.Lock()
	defer e.qmu.Unlock()
	for _, q := range e.convs {
		if q.lease == nil && len(q.waiting) > 0 && q.waiting[0].kind == JobRefresh {
			return true
		}
	}
	return false
}

// failStaleRefreshHeads ends a refresh that has waited at the head of its queue with no capable session for refreshHeadWait:
// failed lost, so the turns behind it go out. capConvs is the conversations that have a capable session now.
func (e *Engine) failStaleRefreshHeads(capConvs map[string]CapSession) {
	var lost []*job
	e.qmu.Lock()
	now := e.d.Now()
	for conv, q := range e.convs {
		head := len(q.waiting) > 0 && q.waiting[0].kind == JobRefresh && q.lease == nil
		if _, capable := capConvs[conv]; !head || capable {
			q.noCapSince = time.Time{}
			continue
		}
		if q.noCapSince.IsZero() {
			q.noCapSince = now
			continue
		}
		if now.Sub(q.noCapSince) >= refreshHeadWait && !e.qstopped {
			lost = append(lost, q.waiting[0])
			q.waiting = q.waiting[1:]
			q.noCapSince = time.Time{}
			q.signal()
		}
	}
	e.qmu.Unlock()
	for _, j := range lost {
		e.d.Logf("[workbook] a refresh found no session able to run it; it is marked lost")
		e.finishUnrun(j, StateFailed, ReasonLost)
	}
}

// AvailabilityChange is a conversation whose refresh_available value changed.
type AvailabilityChange struct {
	ConvKey   string
	Available bool
}

// SweepAvailability recomputes refresh_available for every conversation that had it or has a capable session now and
// returns only the ones whose value differs from the last one reported (plan D11): repeated sweeps report nothing, and
// one stream lapsing while another still serves the conversation reports nothing.
func (e *Engine) SweepAvailability() []AvailabilityChange {
	now := e.refreshConvs()
	e.rf.availMu.Lock()
	defer e.rf.availMu.Unlock()
	if e.rf.lastAvail == nil {
		e.rf.lastAvail = map[string]bool{}
	}
	var out []AvailabilityChange
	for conv := range now {
		if !e.rf.lastAvail[conv] {
			e.rf.lastAvail[conv] = true
			out = append(out, AvailabilityChange{ConvKey: conv, Available: true})
		}
	}
	for conv, was := range e.rf.lastAvail {
		if _, ok := now[conv]; was && !ok {
			delete(e.rf.lastAvail, conv)
			out = append(out, AvailabilityChange{ConvKey: conv, Available: false})
		}
	}
	return out
}
