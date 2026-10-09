package push

import (
	"sync"

	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/team"
)

// openApprovals is the set of open approvals of the pushed kinds (lead, self_relay, member_relay, an answerable
// hook_ask): the count every push carries as `purdex.open_approvals`, from which the iOS notification extension sets the
// app-icon badge (push spec §6). Seeded from the approval feed's open snapshot, kept by its opened / closed events (the
// feed's goroutine) and read by the sender (its own goroutine), so guarded.
type openApprovals struct {
	mu  sync.Mutex
	ids map[string]struct{}
}

func newOpenApprovals() *openApprovals { return &openApprovals{ids: map[string]struct{}{}} }

// isPushedApproval: the kinds that have a phone push, the same rule push.ApprovalContent applies.
func isPushedApproval(a team.Approval) bool {
	_, pushed := push.ApprovalContent(toPushApproval(a), "", "en")
	return pushed
}

// Clear forgets everything: a (re)start begins from the feed's snapshot.
func (o *openApprovals) Clear() {
	o.mu.Lock()
	o.ids = map[string]struct{}{}
	o.mu.Unlock()
}

// Load adds the snapshot of open approvals the feed returned when it was subscribed.
func (o *openApprovals) Load(open []team.Approval) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, a := range open {
		if isPushedApproval(a) {
			o.ids[a.ID] = struct{}{}
		}
	}
}

func (o *openApprovals) Opened(a team.Approval) {
	if !isPushedApproval(a) {
		return
	}
	o.mu.Lock()
	o.ids[a.ID] = struct{}{}
	o.mu.Unlock()
}

func (o *openApprovals) Closed(id string) {
	o.mu.Lock()
	delete(o.ids, id)
	o.mu.Unlock()
}

// Count is how many are open now.
func (o *openApprovals) Count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.ids)
}
