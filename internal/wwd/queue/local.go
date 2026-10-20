package queue

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/wake/purdex/internal/wwd/wire"
)

// RecordLocal records a fast-lane run (spec 5.2) as a job that is running already, with the claim made: the submitter runs it in its own
// working tree on this host, so there is no snapshot to pin, no queue to wait in and nothing to supersede. The caller (the local-runs
// service) has checked the lock keys; the queue does not look at them. worker names this host. The answer is the claim and the lease:
// the runner heartbeats, posts its result and uploads its log with them like any worker, and if it disappears the job fails as lost
// (a fast-lane job is never requeued).
func (q *Queue) RecordLocal(ctx context.Context, req wire.LocalRunRequest, worker string) (wire.LocalRunResponse, error) {
	if err := req.Validate(); err != nil {
		return wire.LocalRunResponse{}, err
	}
	if worker == "" {
		return wire.LocalRunResponse{}, errors.New("queue: a local run needs a worker name")
	}
	id, claimID, now := q.newI(), wire.NewClaimID(), q.now().UnixMilli()
	err := q.write(ctx, func(c *sql.Conn, changed *[]string) error {
		if err := insertJob(ctx, c, id, q.opt.Origin, req.JobSubmission, now, false); err != nil {
			return err
		}
		if _, err := c.ExecContext(ctx, `UPDATE jobs SET state = 'running', local = 1, claim_worker = ?, claim_id = ?, claim_since = ?, started_at = ?,
			lease_deadline = ?, lease_s = ?, updated_at = ? WHERE id = ?`,
			worker, claimID, now, now, q.deadline(now), q.leaseS, now, id); err != nil {
			return err
		}
		*changed = append(*changed, id)
		return nil
	})
	if err != nil {
		return wire.LocalRunResponse{}, err
	}
	return wire.LocalRunResponse{ID: id, ClaimID: claimID, LeaseS: q.leaseS, RenewS: q.renewS}, nil
}

// endedChunk is how many ids one query asks about (SQLite's limit on variables is far higher; this keeps a statement small).
const endedChunk = 500

// Ended says which of ids are over: true for a job in a final state and for one the queue does not have (jobs are kept 7 days, then
// deleted), false for one that is still queued, claimed or running. Every id asked about is in the answer.
func (q *Queue) Ended(ctx context.Context, ids []string) (map[string]bool, error) {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	for start := 0; start < len(ids); start += endedChunk {
		chunk := ids[start:min(start+endedChunk, len(ids))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := q.db.QueryContext(ctx, `SELECT id, state FROM jobs WHERE id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, state string
			if err := rows.Scan(&id, &state); err != nil {
				rows.Close()
				return nil, err
			}
			if !strings.Contains(terminalStates, "'"+state+"'") {
				out[id] = false
			}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// LiveClaim is what the snapshot route needs to serve a job: the private record of where the snapshot is. GitDir never leaves the host
// (the wire job has no such field); State is the job's state, for the body of a 409.
type LiveClaim struct {
	State  wire.JobState
	GitDir string
	Commit string
	Base   string
}

// LiveClaim answers whether claimID is the claim the job is held under now (spec 4.2: a snapshot is served for a live claim only).
// A claim that is stale, released or unknown is a ConflictError carrying the job's state, and an unknown job is ErrNotFound. A fast-lane
// job has no snapshot to serve and is not found.
func (q *Queue) LiveClaim(ctx context.Context, id, claimID string) (LiveClaim, error) {
	var (
		lc      LiveClaim
		state   string
		claimed sql.NullString
	)
	switch err := q.db.QueryRowContext(ctx, `SELECT state, claim_id, git_dir, snap_commit, snap_base FROM jobs WHERE id = ? AND local = 0`, id).
		Scan(&state, &claimed, &lc.GitDir, &lc.Commit, &lc.Base); {
	case errors.Is(err, sql.ErrNoRows):
		return LiveClaim{}, ErrNotFound
	case err != nil:
		return LiveClaim{}, err
	}
	lc.State = wire.JobState(state)
	h := held{state: lc.State, claimID: claimed}
	if err := h.live(claimID); err != nil {
		return LiveClaim{}, err
	}
	return lc, nil
}
