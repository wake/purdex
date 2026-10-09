package push

import (
	"sync"
	"time"

	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/team"
)

// recentAskWindow: an ask that opened this recently still counts after it closed, because the agent's own `waiting`
// event for the same question can arrive after the ask has already been answered (spec §5.2 rule 8).
const recentAskWindow = 10 * time.Second

// openAsks is the set of hook_ask approvals that the phone was (or is being) told about, for rule 8: a `waiting`
// event of the same session is the same question and must not push twice. It holds only an answerable hook_ask (a
// terminal_only ask is not pushed, so the agent's own event stays the phone's only notice). Rebuilt from the approval
// feed's open snapshot, kept by its opened / closed events; in memory.
type openAsks struct {
	mu   sync.Mutex
	now  func() time.Time
	byID map[string]askEntry
}

type askEntry struct {
	sid, name string
	opened    time.Time
	open      bool
}

func newOpenAsks(now func() time.Time) *openAsks {
	if now == nil {
		now = time.Now
	}
	return &openAsks{now: now, byID: map[string]askEntry{}}
}

// isPushedAsk: an answerable hook_ask, the only kind that has a phone push to duplicate.
func isPushedAsk(a team.Approval) bool {
	if a.Kind != team.KindHookAsk {
		return false
	}
	_, pushed := push.ApprovalContent(toPushApproval(a), "", "en")
	return pushed
}

// Reset replaces the set with the snapshot of open approvals (a new subscription: the daemon restarted or the feed
// was re-armed).
func (o *openAsks) Reset(open []team.Approval) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.byID = map[string]askEntry{}
	now := o.now()
	for _, a := range open {
		if !isPushedAsk(a) {
			continue
		}
		opened := now
		if a.CreatedAt > 0 {
			opened = time.UnixMilli(a.CreatedAt)
		}
		o.byID[a.ID] = askEntry{sid: a.Origin.SessionID, name: tmuxSessionOf(a.Origin.Tmux), opened: opened, open: true}
	}
}

// Opened records a newly opened ask.
func (o *openAsks) Opened(a team.Approval) {
	if !isPushedAsk(a) {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pruneLocked(o.now())
	o.byID[a.ID] = askEntry{sid: a.Origin.SessionID, name: tmuxSessionOf(a.Origin.Tmux), opened: o.now(), open: true}
}

// Closed takes an ask out of the open set; it keeps counting only until its recent window ends.
func (o *openAsks) Closed(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if e, ok := o.byID[id]; ok {
		e.open = false
		o.byID[id] = e
	}
	o.pruneLocked(o.now())
}

// Has: some ask is open, or opened within the last 10 s, for the session with this agent session id or this tmux
// session name. An empty identifier never matches.
func (o *openAsks) Has(sessionID, tmuxName string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := o.now()
	for _, e := range o.byID {
		if !e.open && now.Sub(e.opened) >= recentAskWindow {
			continue
		}
		if (sessionID != "" && e.sid == sessionID) || (tmuxName != "" && e.name == tmuxName) {
			return true
		}
	}
	return false
}

func (o *openAsks) pruneLocked(now time.Time) {
	for id, e := range o.byID {
		if !e.open && now.Sub(e.opened) >= recentAskWindow {
			delete(o.byID, id)
		}
	}
}

// Len is the number of entries held (open ones and recently closed ones).
func (o *openAsks) Len() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.byID)
}
