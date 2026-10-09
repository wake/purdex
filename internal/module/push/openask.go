package push

import (
	"sync"
	"time"

	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/team"
)

// askRetention is how long a closed ask's interval is kept: a `waiting` event is held for 2 s, so an interval that
// ended more than a minute ago can never overlap one.
const askRetention = time.Minute

// maxAskIntervals bounds the intervals kept; the oldest closed one goes first.
const maxAskIntervals = 1024

// openAsks records, per session, when the answerable hook_ask approvals were open (push spec §5.2 rule 8): an interval
// [opened, closed) per ask, still running while it is open. A `waiting` event is a duplicate of an AskUserQuestion push
// exactly when its own window (its arrival until its hold ends) overlaps one of those intervals; an ask that was
// opened and answered before the event arrived does not hide it. Only an answerable hook_ask counts (a terminal_only
// ask is not pushed, so the agent's own event stays the phone's only notice). Rebuilt from the approval feed's open
// snapshot, kept by its opened / closed events; in memory.
type openAsks struct {
	mu   sync.Mutex
	now  func() time.Time
	byID map[string]askInterval
}

type askInterval struct {
	sid, name      string
	opened, closed time.Time // closed is the zero time while the ask is open
}

func newOpenAsks(now func() time.Time) *openAsks {
	if now == nil {
		now = time.Now
	}
	return &openAsks{now: now, byID: map[string]askInterval{}}
}

// isPushedAsk: an answerable hook_ask, the only kind that has a phone push to duplicate.
func isPushedAsk(a team.Approval) bool {
	if a.Kind != team.KindHookAsk {
		return false
	}
	_, pushed := push.ApprovalContent(toPushApproval(a), "", "en")
	return pushed
}

// Clear forgets everything: a (re)start begins from the feed's snapshot, not from what an earlier run last saw.
func (o *openAsks) Clear() {
	o.mu.Lock()
	o.byID = map[string]askInterval{}
	o.mu.Unlock()
}

// Load adds the snapshot of open approvals the feed returned when it was subscribed. It adds, never clears.
func (o *openAsks) Load(open []team.Approval) {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := o.now()
	for _, a := range open {
		if !isPushedAsk(a) {
			continue
		}
		opened := now
		if a.CreatedAt > 0 {
			opened = time.UnixMilli(a.CreatedAt)
		}
		o.byID[a.ID] = askInterval{sid: a.Origin.SessionID, name: tmuxSessionOf(a.Origin.Tmux), opened: opened}
	}
}

// Opened starts the ask's interval.
func (o *openAsks) Opened(a team.Approval) {
	if !isPushedAsk(a) {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	now := o.now()
	o.pruneLocked(now)
	o.byID[a.ID] = askInterval{sid: a.Origin.SessionID, name: tmuxSessionOf(a.Origin.Tmux), opened: now}
}

// Closed ends the ask's interval (the first close wins).
func (o *openAsks) Closed(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := o.now()
	if e, ok := o.byID[id]; ok && e.closed.IsZero() {
		e.closed = now
		o.byID[id] = e
	}
	o.pruneLocked(now)
}

// Now is the clock the intervals are on, for a caller that stamps an arrival.
func (o *openAsks) Now() time.Time { return o.now() }

// Overlaps: some ask of the session with this agent session id or this tmux session name was open at some moment of
// [from, to] — it opened by `to` and had not closed by `from`. An empty identifier never matches.
func (o *openAsks) Overlaps(sessionID, tmuxName string, from, to time.Time) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, e := range o.byID {
		if !((sessionID != "" && e.sid == sessionID) || (tmuxName != "" && e.name == tmuxName)) {
			continue
		}
		if !e.opened.After(to) && (e.closed.IsZero() || e.closed.After(from)) {
			return true
		}
	}
	return false
}

func (o *openAsks) pruneLocked(now time.Time) {
	for id, e := range o.byID {
		if !e.closed.IsZero() && now.Sub(e.closed) >= askRetention {
			delete(o.byID, id)
		}
	}
	for len(o.byID) > maxAskIntervals { // over the cap: the oldest closed interval goes
		var oldest string
		var oldestClosed time.Time
		for id, e := range o.byID {
			if !e.closed.IsZero() && (oldest == "" || e.closed.Before(oldestClosed)) {
				oldest, oldestClosed = id, e.closed
			}
		}
		if oldest == "" {
			return // all of them are open: nothing may be dropped
		}
		delete(o.byID, oldest)
	}
}

// Len is the number of intervals held (open ones and recently closed ones).
func (o *openAsks) Len() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.byID)
}
