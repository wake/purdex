package push

import (
	"context"
	"log"
	"sort"
	"time"

	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/team"
)

// maxOpenKeys caps purdex.open_approval_keys (spec §6); open_approvals still reports the full count.
const maxOpenKeys = 32

// maxSessionIDKey bounds the session id a "c:" key may carry (a Claude Code session id is a 36-byte UUID). A longer one is
// not a real id: it would make 32 keys overflow the 4 KiB payload of every push of the host while its card is open, so such
// a card keys by its own approval id instead.
const maxSessionIDKey = 64

// sessionBudget bounds the one session read a snapshot makes; past it every approval falls back to its id.
const sessionBudget = time.Second

// sessionSkew is how much later than an approval a tmux session may have been created and still be the approval's own
// (tmux's #{session_created} has one-second resolution; the approval's clock is the daemon's).
const sessionSkew = 2 * time.Second

// readSessions is the one tmux read of a snapshot, bounded by the budget even when the reader ignores its context.
func (m *Module) readSessions(ctx context.Context) map[string]session.SessionRef {
	if m.sessions == nil {
		return nil
	}
	budget := m.sessBudget
	if budget <= 0 {
		budget = sessionBudget
	}
	// Single flight: a reader that ignores its context keeps its goroutine, so while one is outstanding no other starts.
	if !m.sessBusy.CompareAndSwap(false, true) {
		if m.sessStalled.CompareAndSwap(false, true) { // once per stall, not per Job
			log.Printf("[push] the previous tmux session read has not returned; not starting another (badge keys fall back to approval ids)")
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	type result struct {
		refs map[string]session.SessionRef
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		refs, err := m.sessions(ctx)
		m.sessStalled.Store(false)
		m.sessBusy.Store(false)
		ch <- result{refs, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			log.Printf("[push] could not read the tmux sessions: %v (badge keys fall back to approval ids)", r.err)
			return nil
		}
		return r.refs
	case <-ctx.Done():
		log.Printf("[push] the tmux session read took over %v (badge keys fall back to approval ids)", budget)
		return nil
	}
}

// ownSession is the session code of the tmux session an approval was raised in; "" when it cannot be vouched for. The name
// alone is not enough: a session killed and recreated under the same name is another session, and it was created after the
// approval. An unknown creation time on either side is not vouched for either.
func ownSession(refs map[string]session.SessionRef, name string, approvalAtMs int64) string {
	ref, ok := refs[name]
	if !ok || name == "" || ref.Created <= 0 || approvalAtMs <= 0 {
		return ""
	}
	if ref.Created*1000 > approvalAtMs+sessionSkew.Milliseconds() {
		return ""
	}
	return ref.Code
}

// openState reads the open approval set now: the count of the cards the badge counts (push.CountsForBadge: lead,
// self_relay, member_relay, adopt, hook_ask, hook_permission — whether or not they got a visible push) for
// `purdex.open_approvals`, and the sorted, de-duplicated badge keys for `purdex.open_approval_keys` ("s:<session_code>" when
// the approval's own tmux session resolves, else "c:<origin session_id>" when it has one, else "a:<approval_id>", at most
// maxOpenKeys). The Job's own approval is resolved from what the Job carries, independently of the open set (it may
// have closed while the Job was queued, or the open-set read may have failed): its session_code is snap.code. The sender
// asks once per Job and shares the answer across its devices; the tmux read is a single bounded call, made only if the Job's
// approval or a pushed open approval has a tmux origin. Nothing is kept between sends, so there is nothing to drift.
// ok=false (no reader, or the read failed) means the count and keys are unknown: the payload then leaves both fields out.
func (m *Module) openState(ctx context.Context, j Job) openSnap {
	var pushed []team.Approval
	openOK, ownOpen := false, false
	if r := m.reader; r != nil {
		open, err := r.OpenApprovals()
		if err != nil {
			log.Printf("[push] could not read the open approvals: %v (open_approvals left out of this push)", err)
		} else {
			openOK = true
			for _, a := range open {
				if j.Approval != nil && a.ID == j.Approval.ID {
					ownOpen = true
				}
				if push.CountsForBadge(string(a.Kind)) { // the cards the badge counts, pushed or not (badge spec §3.1)
					pushed = append(pushed, a)
				}
			}
		}
	}
	needSessions := j.Approval != nil && j.Approval.TmuxSession != ""
	for _, a := range pushed {
		if tmuxSessionOf(a.Origin.Tmux) != "" {
			needSessions = true
		}
	}
	var refs map[string]session.SessionRef
	if needSessions {
		refs = m.readSessions(ctx)
	}
	snap := openSnap{ok: openOK, ownOpen: ownOpen}
	if j.Approval != nil {
		snap.code = ownSession(refs, j.Approval.TmuxSession, j.Approval.CreatedAt)
	}
	if !openOK {
		return snap
	}
	snap.n = len(pushed)
	set := map[string]struct{}{}
	for _, a := range pushed {
		switch code := ownSession(refs, tmuxSessionOf(a.Origin.Tmux), a.CreatedAt); {
		case code != "":
			set["s:"+code] = struct{}{}
		case a.Origin.SessionID != "" && len(a.Origin.SessionID) <= maxSessionIDKey: // several cards of one unresolved session count once
			set["c:"+a.Origin.SessionID] = struct{}{}
		default:
			set["a:"+a.ID] = struct{}{}
		}
	}
	snap.keys = make([]string, 0, len(set))
	for k := range set {
		snap.keys = append(snap.keys, k)
	}
	sort.Strings(snap.keys)
	if len(snap.keys) > maxOpenKeys {
		snap.keys = snap.keys[:maxOpenKeys]
	}
	return snap
}
