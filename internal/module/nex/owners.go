package nex

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"lab.protype.tw/wake/nexen/store"
)

// Conversation-entity ownership (spec §4.2, D1): which executions are S's
// live worker stints.

const (
	ownerScanPageSize = 500 // store.MaxPageSize
	ownerScanMaxPages = 20
)

// errOwnerScanTruncated: the scan stopped at its page cap with more rows
// unread, so the result may be incomplete.
var errOwnerScanTruncated = errors.New("nex: owner scan hit its page cap")

// isLiveExecution: not archived and not terminated.
func isLiveExecution(e store.Execution) bool {
	return e.ArchivedAt == 0 && e.State != store.StateTerminated
}

// executionIsFor: the row belongs to Claude session sid, as its own session
// or as the one it resumes.
func executionIsFor(e store.Execution, sid string) bool {
	return sid != "" && (e.SessionID == sid || e.ResumeSessionID == sid)
}

// liveWorkersFor returns S's live executions, newest first (CreatedAt desc,
// then ID desc), with scanLiveWorkers' error contract.
func (m *Module) liveWorkersFor(parent context.Context, sid string) ([]store.Execution, error) {
	if sid == "" {
		return nil, nil
	}
	return m.scanLiveWorkers(parent, func(e store.Execution) bool { return executionIsFor(e, sid) })
}

// scanLiveWorkers pages the non-archived executions and keeps the live ones
// that keep accepts, newest first.
//
// On any error (a failed page, or errOwnerScanTruncated at the page cap) the
// result holds every live match found before the failure. Callers that must
// prove absence (the owner checks) fail closed on any error; Q1 and the
// overflow reconcile act on what was found (manual_resume.go).
func (m *Module) scanLiveWorkers(parent context.Context, keep func(store.Execution) bool) ([]store.Execution, error) {
	var out []store.Execution
	cursor := ""
	for page := 0; page < ownerScanMaxPages; page++ {
		ctx, cancel := detachedContext(parent, m.engineOpTimeout)
		res, err := m.sys.store.List(ctx, store.ListOptions{Cursor: cursor, Limit: ownerScanPageSize})
		cancel()
		if err != nil {
			sortNewestFirst(out)
			return out, fmt.Errorf("nex: listing executions: %w", err)
		}
		for _, e := range res.Items {
			if isLiveExecution(e) && keep(e) {
				out = append(out, e)
			}
		}
		if res.NextCursor == "" || res.NextCursor == cursor {
			sortNewestFirst(out)
			return out, nil
		}
		cursor = res.NextCursor
	}
	sortNewestFirst(out)
	return out, errOwnerScanTruncated
}

func sortNewestFirst(es []store.Execution) {
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].CreatedAt != es[j].CreatedAt {
			return es[i].CreatedAt > es[j].CreatedAt
		}
		return es[i].ID > es[j].ID
	})
}

// sidLockKey is the handoff-lock key for a Claude session id.
func sidLockKey(sid string) string { return "sid:" + sid }

// purdexSessionLabel marks an execution with the Claude session it carries (D17).
const purdexSessionLabel = "purdex.session_id"

// checkOwners: nil when nothing but the transferred owner holds S. allowExec /
// allowPane name that owner ("" = none).
// 409 session_owned {owner: "terminal", session_id, tmux_pane_id} |
// {owner: "worker", session_id, execution_id, state};
// {owner: "terminal", session_id, recent_resume: true} (just resumed, frame not yet recorded);
// 503 owner_check_failed when either lookup errs (a truncated worker scan
// counts as an error, and the partial result it carries is ignored: a
// partial scan cannot prove absence).
func (m *Module) checkOwners(parent context.Context, sid, allowExec, allowPane string) *handoffError {
	// A resume that just succeeded may not have its terminal frame recorded
	// yet. Handoff (allowPane != "") transfers the terminal itself, so only
	// the other callers are held back by the marker.
	if allowPane == "" && m.recentlyResumed(sid) {
		return &handoffError{http.StatusConflict, "session_owned", "this conversation was just resumed in a terminal",
			map[string]any{"owner": "terminal", "session_id": sid, "recent_resume": true}}
	}
	ctx, cancel := detachedContext(parent, m.engineOpTimeout)
	terms, err := m.terminals.LiveBySessionID(ctx, "cc", sid)
	cancel()
	if err != nil {
		return &handoffError{http.StatusServiceUnavailable, "owner_check_failed", "checking terminal owners: " + err.Error(), map[string]any{"session_id": sid}}
	}
	for _, t := range terms {
		if allowPane != "" && t.PaneID == allowPane {
			continue
		}
		if !t.Verified {
			// D1: an owner is a pid that still has its recorded start time. One
			// we cannot read is not an owner — but S is not provably free either,
			// so the transfer is refused as retryable (PR #1572 review A2).
			return &handoffError{http.StatusServiceUnavailable, "owner_check_failed", "cannot verify a terminal process recorded for this conversation",
				map[string]any{"session_id": sid, "tmux_pane_id": t.PaneID}}
		}
		return &handoffError{http.StatusConflict, "session_owned", "this conversation is open in a terminal",
			map[string]any{"owner": "terminal", "session_id": sid, "tmux_pane_id": t.PaneID}}
	}
	workers, err := m.liveWorkersFor(parent, sid)
	if err != nil {
		return &handoffError{http.StatusServiceUnavailable, "owner_check_failed", "checking worker owners: " + err.Error(), map[string]any{"session_id": sid}}
	}
	for _, e := range workers {
		if e.ID == allowExec {
			continue
		}
		return &handoffError{http.StatusConflict, "session_owned", "this conversation already has a live worker",
			map[string]any{"owner": "worker", "session_id": sid, "execution_id": e.ID, "state": string(e.State)}}
	}
	return nil
}
