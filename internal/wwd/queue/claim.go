package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/wwd/wire"
)

const (
	// defaultLeaseS and defaultRenewS are the lease a claim and each heartbeat hand out (spec 4.2: 60 s, renewed every 15 s).
	defaultLeaseS = 60
	defaultRenewS = 15
	// maxLapses: a job whose lease runs out this many times is failed as lost instead of requeued (spec 4.2). Infrastructure releases
	// count as attempts (the number shown) but not as lapses, so they never bring the end closer.
	maxLapses = 2
)

// Claim hands a queued job to a worker (spec 4.2, 4.3): one transaction, so of several claimers the first wins and the rest get a
// ConflictError for the state the job is in. The answer carries the job, a fresh claim_id and the lease; the claim_id is the only
// proof of ownership for everything that follows.
func (q *Queue) Claim(ctx context.Context, id string, req wire.ClaimRequest) (wire.ClaimResponse, error) {
	return q.claim(ctx, id, req, false)
}

// ClaimForPeers is Claim for a request from another host: an ad-hoc or fast-lane job is not found.
func (q *Queue) ClaimForPeers(ctx context.Context, id string, req wire.ClaimRequest) (wire.ClaimResponse, error) {
	return q.claim(ctx, id, req, true)
}

func (q *Queue) claim(ctx context.Context, id string, req wire.ClaimRequest, forPeers bool) (wire.ClaimResponse, error) {
	if err := req.Validate(); err != nil {
		return wire.ClaimResponse{}, err
	}
	claimID := wire.NewClaimID()
	var resp wire.ClaimResponse
	err := q.write(ctx, func(c *sql.Conn, changed *[]string) error {
		h, err := loadHeld(ctx, c, id, forPeers)
		if err != nil {
			return err
		}
		if h.state != wire.StateQueued || h.manual == wire.PositionHold {
			return &ConflictError{State: h.state} // a held job is queued and stays so: the worker moves on to the next
		}
		now := q.now().UnixMilli()
		// what the worker says it is, kept with the claim: the result it posts is filed under this, not under what the host is by then
		labels, fp := claimFacts(req)
		if _, err := c.ExecContext(ctx, `UPDATE jobs SET state = 'claimed', claim_worker = ?, claim_id = ?, claim_since = ?,
			lease_deadline = ?, lease_s = ?, cancel_requested = 0, updated_at = ?, claim_labels = ?, claim_fingerprint = ? WHERE id = ?`,
			req.Worker, claimID, now, q.deadline(now), q.leaseS, now, labels, fp, id); err != nil {
			return err
		}
		*changed = append(*changed, id)
		j, err := getOn(ctx, c, id, false)
		resp = wire.ClaimResponse{Job: j, ClaimID: claimID, LeaseS: q.leaseS, RenewS: q.renewS}
		return err
	})
	if err != nil {
		return wire.ClaimResponse{}, err
	}
	return resp, nil
}

// Start moves a claimed job to running, once the worker has its workspace and is about to run the command; it renews the lease. A
// repeated start with the same claim is the same answer.
func (q *Queue) Start(ctx context.Context, id, claimID string) (wire.Lease, error) {
	err := q.write(ctx, func(c *sql.Conn, changed *[]string) error {
		h, err := loadHeld(ctx, c, id, false)
		if err != nil {
			return err
		}
		if err := h.live(claimID); err != nil {
			return err
		}
		now := q.now().UnixMilli()
		if h.state == wire.StateClaimed {
			if _, err := c.ExecContext(ctx, `UPDATE jobs SET state = 'running', started_at = ?, lease_deadline = ?, updated_at = ? WHERE id = ?`, now, q.deadline(now), now, id); err != nil {
				return err
			}
			*changed = append(*changed, id)
			return nil
		}
		return q.renew(ctx, c, id, now)
	})
	if err != nil {
		return wire.Lease{}, err
	}
	return wire.Lease{LeaseS: q.leaseS, RenewS: q.renewS}, nil
}

// Heartbeat renews the lease from the origin's clock and tells the worker whether it is to stop (a person asked to cancel, or a newer
// job superseded this one and the task is interruptible). It is not a state change, so it announces nothing.
func (q *Queue) Heartbeat(ctx context.Context, id, claimID string) (wire.HeartbeatResponse, error) {
	var cancel bool
	err := q.writeOnly(ctx, func(c *sql.Conn, changed *[]string) error {
		h, err := loadHeld(ctx, c, id, false)
		if err != nil {
			return err
		}
		if err := h.live(claimID); err != nil {
			return err
		}
		cancel = h.cancelRequested
		return q.renew(ctx, c, id, q.now().UnixMilli())
	})
	if err != nil {
		return wire.HeartbeatResponse{}, err
	}
	return wire.HeartbeatResponse{LeaseS: q.leaseS, RenewS: q.renewS, Cancel: cancel}, nil
}

func (q *Queue) renew(ctx context.Context, c *sql.Conn, id string, nowMS int64) error {
	_, err := c.ExecContext(ctx, `UPDATE jobs SET lease_deadline = ? WHERE id = ?`, q.deadline(nowMS), id)
	return err
}

// Release hands a claim back (spec 4.3): reclaim (the owner wants the machine) puts the job back in the queue without counting the
// attempt; infra (the host could not run it) counts one. A job somebody asked to cancel, or that a newer job superseded, is ended
// instead of requeued, since nothing is running to post its result and nobody wants it to run again. A fast-lane job is never
// requeued (it ran on its submitter's own tree): it fails with the release reason.
func (q *Queue) Release(ctx context.Context, id string, req wire.ReleaseRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	return q.write(ctx, func(c *sql.Conn, changed *[]string) error {
		h, err := loadHeld(ctx, c, id, false)
		if err != nil {
			return err
		}
		if err := h.live(req.ClaimID); err != nil {
			return err
		}
		counted := 0
		if req.Reason == wire.ReleaseInfra {
			counted = 1
		}
		if err := q.letGo(ctx, c, h, counted, false, string(req.Reason)); err != nil {
			return err
		}
		*changed = append(*changed, id)
		return nil
	})
}

// letGo takes a job back from its holder (released, or its lease lapsed): it ends if nobody wants it to run (cancelled, superseded) or
// cannot run again (fast lane, or its lease ran out for the last time), and goes back to the queue otherwise. counted is the attempt
// this costs (0 or 1); lapsed says the lease ran out, which is what is counted towards lost; failReason is the reason a fast-lane job,
// or one that ends for want of lapses, carries.
func (q *Queue) letGo(ctx context.Context, c *sql.Conn, h held, counted int, lapsed bool, failReason string) error {
	now := q.now().UnixMilli()
	res := wire.Result{Host: h.worker}
	state := wire.StateFailed
	switch {
	case h.supersededBy.Valid:
		state, res.Reason = wire.StateSuperseded, "superseded_by:"+h.supersededBy.String
	case h.cancelRequested:
		state, res.Reason = wire.StateCancelled, "cancelled"
	case h.local, lapsed && h.lapses+1 >= maxLapses:
		res.Reason = failReason
	default:
		_, err := c.ExecContext(ctx, `UPDATE jobs SET state = 'queued', attempts = attempts + ?, lapses = lapses + ?, claim_worker = NULL,
			claim_id = NULL, claim_since = NULL, started_at = NULL, lease_deadline = NULL, lease_s = NULL, updated_at = ? WHERE id = ?`, counted, b2i(lapsed), now, h.id)
		return err
	}
	// the counts are kept when the job ends too: the attempt and the lapse that ended it are part of its history
	if _, err := c.ExecContext(ctx, `UPDATE jobs SET attempts = attempts + ?, lapses = lapses + ? WHERE id = ?`, counted, b2i(lapsed), h.id); err != nil {
		return err
	}
	return endHeld(ctx, c, h, state, res, "", now)
}

// endHeld writes the end of a job its worker no longer holds. resultClaimID is the claim the result came with ("" when the origin ends
// it), which is what makes a repeated result an echo.
func endHeld(ctx context.Context, c *sql.Conn, h held, state wire.JobState, res wire.Result, resultClaimID string, nowMS int64) error {
	fin := time64(nowMS)
	if res.FinishedAt == nil {
		res.FinishedAt = &fin
	}
	if h.hasLog && res.Log == "" {
		res.Log = h.id + "/log" // the log came before the result: the result points at it
	}
	if h.supersededBy.Valid {
		by := h.supersededBy.String
		res.SupersededBy = &by
		if res.Reason == "" {
			res.Reason = "superseded_by:" + by
		}
	}
	b, err := json.Marshal(res)
	if err != nil {
		return err
	}
	var rc any
	if resultClaimID != "" {
		rc = resultClaimID
	}
	_, err = c.ExecContext(ctx, `UPDATE jobs SET state = ?, result = ?, result_claim_id = ?, finished_at = ?, lease_deadline = NULL, updated_at = ? WHERE id = ?`,
		string(state), string(b), rc, nowMS, nowMS, h.id)
	return err
}

// Result ends a job in the state its worker reports (spec 4.3). The claim must be live, except that the same result posted again with
// the claim that ended the job is answered with the job as it is, changing nothing and announcing nothing. The finish time ordering
// uses is the origin's own; the worker's started_at and finished_at are kept as information.
func (q *Queue) Result(ctx context.Context, id string, req wire.ResultRequest) (wire.Job, error) {
	if err := req.Validate(); err != nil {
		return wire.Job{}, err
	}
	var out wire.Job
	var produced Produced
	ended := false // this call ended the job: not the echo of a result posted before
	// the reference is asked first, but a failure of it matters only to a result that is going to end the job: the echo of one that did
	// must still be answered, however the hook is
	reference, refErr := q.referenceOf(ctx, id)
	err := q.write(ctx, func(c *sql.Conn, changed *[]string) error {
		h, err := loadHeld(ctx, c, id, false)
		if err != nil {
			return err
		}
		if h.ended() && h.resultClaimID.Valid && h.resultClaimID.String == req.ClaimID {
			out, err = getOn(ctx, c, id, false)
			return err
		}
		if err := h.live(req.ClaimID); err != nil {
			return err
		}
		if refErr != nil {
			return refErr // the result is not taken; the worker posts it again
		}
		res := wire.Result{Exit: req.Exit, Summary: req.Summary, Host: h.worker, StartedAt: req.StartedAt, FinishedAt: req.FinishedAt, Reason: req.Reason}
		if err := endHeld(ctx, c, h, req.State, res, req.ClaimID, q.now().UnixMilli()); err != nil {
			return err
		}
		*changed = append(*changed, id)
		if produced, err = producedOn(ctx, c, id); err != nil {
			return err
		}
		ended = true
		out, err = getOn(ctx, c, id, false)
		if err != nil {
			return err
		}
		// how it came out is recorded in this transaction: it cannot be lost to a failure after the result is taken, and it is dated and
		// judged as of this end
		return q.recordEndedOutcome(ctx, c, out, produced, reference)
	})
	if err != nil {
		return wire.Job{}, err
	}
	if ended {
		q.offerResult(ctx, out, produced)
	}
	return out, nil
}

// referenceOf is the task's reference host for a job, as Options.ReferenceHost says, asked before the transaction that ends the job (it may
// read manifests; not with the write lock held). "" when there is no hook, no such job, or no reference. A failure to look the job up, or a
// hook that panics, is an error: the result is not taken (the worker posts it again) rather than taken with an outcome judged without the
// reference it should have had, which could not be recorded again.
func (q *Queue) referenceOf(ctx context.Context, id string) (ref string, err error) {
	if q.opt.ReferenceHost == nil {
		return "", nil
	}
	var repo, task string
	switch err := q.db.QueryRowContext(ctx, `SELECT repo_name, task FROM jobs WHERE id = ?`, id).Scan(&repo, &task); {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil // Result answers that
	case err != nil:
		return "", err
	}
	defer func() {
		if r := recover(); r != nil {
			ref, err = "", fmt.Errorf("queue: reference of %s/%s: %v", repo, task, r)
		}
	}()
	return q.opt.ReferenceHost(repo, task), nil
}

// held is the part of a job row that decides who may do what to it.
type held struct {
	id              string
	state           wire.JobState
	claimID         sql.NullString
	resultClaimID   sql.NullString
	worker          string
	attempts        int
	lapses          int
	cancelRequested bool
	local           bool
	hasLog          bool
	manual          wire.Position
	supersededBy    sql.NullString
}

func loadHeld(ctx context.Context, c *sql.Conn, id string, forPeers bool) (held, error) {
	h := held{id: id}
	var state string
	var worker sql.NullString
	var cancel, local int
	var logSize sql.NullInt64
	var manual string
	query := `SELECT state, claim_id, result_claim_id, claim_worker, attempts, lapses, cancel_requested, local, superseded_by, log_size, manual FROM jobs WHERE id = ?`
	if forPeers {
		query += ` AND adhoc = 0 AND local = 0`
	}
	switch err := c.QueryRowContext(ctx, query, id).Scan(&state, &h.claimID, &h.resultClaimID, &worker, &h.attempts, &h.lapses, &cancel, &local, &h.supersededBy, &logSize, &manual); {
	case errors.Is(err, sql.ErrNoRows):
		return held{}, ErrNotFound
	case err != nil:
		return held{}, err
	}
	h.state, h.worker, h.cancelRequested, h.local = wire.JobState(state), worker.String, cancel == 1, local == 1
	h.hasLog = logSize.Valid
	h.manual = wire.Position(manual)
	return h, nil
}

// live is nil when claimID is the claim the job is held under now; otherwise it is the 409 for the job's state, and the caller changes
// nothing. It does not look at the lease deadline, on purpose: a lease is taken back by the sweep's transaction (lapseLeases), which also
// clears the claim, so until then nobody else holds the job and a late heartbeat or result from the one holder it has is honoured (the
// worker retries a result for up to 10 minutes, spec 4.2). Once the sweep has run, the claim is stale and every call is a 409. Two
// executions cannot overlap either way: a job is only ever claimed again after that transaction.
func (h held) live(claimID string) error {
	if (h.state == wire.StateClaimed || h.state == wire.StateRunning) && h.claimID.Valid && h.claimID.String == claimID {
		return nil
	}
	return &ConflictError{State: h.state}
}

func (h held) ended() bool {
	switch h.state {
	case wire.StateSucceeded, wire.StateFailed, wire.StateCancelled, wire.StateSuperseded, wire.StateExpired:
		return true
	}
	return false
}

// deadline is when a lease granted at nowMS runs out, on the origin's clock.
func (q *Queue) deadline(nowMS int64) int64 { return nowMS + int64(q.leaseS)*1000 }

// lapseLeases takes back every held job whose lease ran out (spec 4.2): it goes back to the queue with the attempt counted, and fails
// as lost when that was its last attempt. A fast-lane job is not requeued either: it fails as lost at once.
func (q *Queue) lapseLeases(ctx context.Context, c *sql.Conn, nowMS int64, changed *[]string) (int, error) {
	rows, err := c.QueryContext(ctx, `SELECT id FROM jobs WHERE state IN ('claimed','running') AND lease_deadline < ? ORDER BY seq`, nowMS)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for _, id := range ids {
		h, err := loadHeld(ctx, c, id, false)
		if err != nil {
			return 0, fmt.Errorf("lapse %s: %w", id, err)
		}
		if err := q.letGo(ctx, c, h, 1, true, "lost"); err != nil {
			return 0, err
		}
		*changed = append(*changed, id)
	}
	return len(ids), nil
}

// extendHeldLeases gives every claimed or running job a full lease from now. New calls it, so a queue opened at wwd's start does it
// before the first sweep, and workers that kept running while the origin was down can still renew (spec 4.2). wwd opens one queue per
// process; opening another over the same database is the same as a restart.
func extendHeldLeases(ctx context.Context, db *sql.DB, nowMS int64, defaultLeaseS int) error {
	_, err := db.ExecContext(ctx, `UPDATE jobs SET lease_deadline = ? + coalesce(lease_s, ?) * 1000 WHERE state IN ('claimed','running')`, nowMS, defaultLeaseS)
	return err
}
