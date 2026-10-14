package resourcesmod

import (
	"context"
	"fmt"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/resources"
)

// Nested runs (P2f-1). A `pdx lease run` below another one of the same session
// (a script the outer run started calls `pdx lease run` again) is the same job:
// it answers held with no row of its own and the command runs under the outer
// lease's weight. The daemon's process ancestry is the authority (no environment
// variable): the walk goes from the caller upward and stops before the agent,
// whose own lease (the mod's foreground one) never counts as an outer.

// nestedUnder finds the held process-scope row of session whose holder is an
// ancestor of holderPID (the caller itself is skipped), below agentPID. failed is
// true when the process table could not be read: the answer is then not_ready.
func (m *Module) nestedUnder(ctx context.Context, holderPID, agentPID int, session string) (outer leaseRow, ok, failed bool) {
	if m.store == nil || session == "" || agentPID <= 0 || holderPID <= 0 {
		return leaseRow{}, false, false
	}
	ctx, cancel := context.WithTimeout(ctx, sampleBudget)
	defer cancel()
	snap, err := m.procSnapshot(ctx)
	if err != nil {
		return leaseRow{}, false, true
	}
	var view ancestry = snap
	if m.ancestryOf != nil {
		view = m.ancestryOf(snap)
	}
	pid := holderPID
	for hop := 0; hop < maxAncestryHops; hop++ {
		parent, err := view.PPID(pid)
		if err != nil {
			return leaseRow{}, false, true
		}
		if parent <= 1 || parent == pid || parent == agentPID {
			return leaseRow{}, false, false
		}
		pid = parent
		rows, err := m.store.heldProcessRowsOf(session, pid)
		if err != nil {
			return leaseRow{}, false, true
		}
		if len(rows) == 0 {
			continue
		}
		got, err := view.Start(pid)
		if err != nil || got.IsZero() {
			continue // a start time that cannot be read vouches for nothing
		}
		for _, r := range rows {
			want, err := ipeers.ParseProcStart(r.HolderStart)
			if err == nil && got.Truncate(time.Second).Equal(want.Truncate(time.Second)) {
				return r, true, false
			}
		}
	}
	return leaseRow{}, false, false
}

// heldProcessRowsOf lists the held process-scope rows of a session whose holder
// is pid, oldest first.
func (s *leaseStore) heldProcessRowsOf(session string, pid int) ([]leaseRow, error) {
	rs, err := s.db.Query(`SELECT `+leaseCols+` FROM resource_leases
		WHERE state = 'held' AND scope = ? AND session_id = ? AND holder_pid = ? ORDER BY created_at, id`,
		resources.ScopeProcess, session, pid)
	if err != nil {
		return nil, fmt.Errorf("read nested outer: %w", err)
	}
	defer rs.Close()
	var out []leaseRow
	for rs.Next() {
		r, err := scanLease(rs)
		if err != nil {
			return nil, fmt.Errorf("read nested outer: %w", err)
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

// countNested adds one nested answer to the outer row. Best effort: the answer
// does not depend on it.
func (s *leaseStore) countNested(id string) {
	_, _ = s.db.Exec(`UPDATE resource_leases SET nested_runs = nested_runs + 1 WHERE id = ?`, id)
}
