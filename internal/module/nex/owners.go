package nex

import (
	"context"
	"errors"
	"fmt"
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
// then ID desc). On errOwnerScanTruncated it still returns what it found.
func (m *Module) liveWorkersFor(parent context.Context, sid string) ([]store.Execution, error) {
	if sid == "" {
		return nil, nil
	}
	return m.scanLiveWorkers(parent, func(e store.Execution) bool { return executionIsFor(e, sid) })
}

// scanLiveWorkers pages the non-archived executions and keeps the live ones
// that keep accepts, newest first.
func (m *Module) scanLiveWorkers(parent context.Context, keep func(store.Execution) bool) ([]store.Execution, error) {
	var out []store.Execution
	cursor := ""
	for page := 0; page < ownerScanMaxPages; page++ {
		ctx, cancel := detachedContext(parent, m.engineOpTimeout)
		res, err := m.sys.store.List(ctx, store.ListOptions{Cursor: cursor, Limit: ownerScanPageSize})
		cancel()
		if err != nil {
			return nil, fmt.Errorf("nex: listing executions: %w", err)
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
