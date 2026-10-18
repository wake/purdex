package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Submit stores a job from a submission (the caller is the local socket handler: submissions are never taken from a peer). In the
// same transaction it supersedes the older jobs with the same supersede_key (spec 4.2): a queued one is superseded at once; a
// claimed or running one is told to stop at its next heartbeat when its task is interruptible, and otherwise runs to its end with
// only superseded_by recorded. The new job's id is returned; Publish is told about the new job and about each one it changed.
func (q *Queue) Submit(ctx context.Context, s wire.JobSubmission) (wire.JobSubmissionResponse, error) {
	if err := s.Validate(); err != nil {
		return wire.JobSubmissionResponse{}, err
	}
	id := q.newI()
	now := q.now()
	// The snapshot is pinned before the job exists, and outside the database's write lock (git takes its time): a job whose snapshot
	// cannot be fetched must not be queued.
	pinned := false
	if q.pin != nil {
		if err := q.recordPin(ctx, id, s.GitDir, now.UnixMilli()); err != nil {
			return wire.JobSubmissionResponse{}, fmt.Errorf("queue: submit: %w", err)
		}
		if err := q.pin.Pin(ctx, s.GitDir, id, s.Snapshot.Commit); err != nil {
			q.takeBackPin(ctx, s.GitDir, id) // a pin that failed half way, or none: either way the record of it must not be left
			return wire.JobSubmissionResponse{}, fmt.Errorf("%w: %v", ErrSnapshotRefused, err)
		}
		pinned = true
	}
	err := q.write(ctx, func(c *sql.Conn, changed *[]string) error {
		if err := insertJob(ctx, c, id, q.opt.Origin, s, now.UnixMilli(), pinned); err != nil {
			return err
		}
		*changed = append(*changed, id)
		if s.SupersedeKey == "" {
			return nil
		}
		superseded, err := supersedeOlder(ctx, c, id, s.SupersedeKey, now.UnixMilli())
		*changed = append(*changed, superseded...)
		return err
	})
	if err != nil {
		if pinned {
			q.takeBackPin(ctx, s.GitDir, id)
		}
		return wire.JobSubmissionResponse{}, fmt.Errorf("queue: submit: %w", err)
	}
	return wire.JobSubmissionResponse{ID: id}, nil
}

// recordPin writes the pin down before it is made (see the git_pins table).
func (q *Queue) recordPin(ctx context.Context, id, gitDir string, nowMS int64) error {
	_, err := q.db.ExecContext(ctx, `INSERT OR IGNORE INTO git_pins (id, git_dir, at) VALUES (?, ?, ?)`, id, gitDir, nowMS)
	return err
}

// takeBackPin removes the pin a failed submission made, unless a job with that id exists (then the pin is that job's).
func (q *Queue) takeBackPin(ctx context.Context, gitDir, id string) {
	ctx = context.WithoutCancel(ctx)
	var one int
	if err := q.db.QueryRowContext(ctx, `SELECT 1 FROM jobs WHERE id = ?`, id).Scan(&one); err == nil {
		return
	}
	if err := q.pin.Unpin(ctx, gitDir, id); err == nil {
		_, _ = q.db.ExecContext(ctx, `DELETE FROM git_pins WHERE id = ?`, id)
	} // else the record stays, and the sweep takes the pin back once the grace has passed
}

func insertJob(ctx context.Context, c *sql.Conn, id, origin string, s wire.JobSubmission, nowMS int64, pinned bool) error {
	list := func(v []string) string {
		if v == nil {
			v = []string{}
		}
		b, _ := json.Marshal(v)
		return string(b)
	}
	_, err := c.ExecContext(ctx, `INSERT INTO jobs (
			id, origin, repo_name, repo_root, repo_remote, snap_commit, snap_tree, snap_base, snap_rel_cwd,
			task, args, class, labels, locks, cacheable, timeout_s, supersede_key, interruptible, queue_timeout_s, adhoc,
			sub_session, sub_ref, sub_team, sub_host, created_at, state, updated_at, git_dir, pinned)
		VALUES (?,?,?,?,?,?,?,?,?, ?,?,?,?,?,?,?,?,?,?,?, ?,?,?,?,?,?,?,?,?)`,
		id, origin, s.Repo.Name, s.Repo.Root, s.Repo.Remote, s.Snapshot.Commit, s.Snapshot.Tree, s.Snapshot.Base, s.Snapshot.RelCwd,
		s.Task, list(s.Args), string(s.Class), list(s.Labels), list(s.Locks), b2i(s.Cacheable), s.TimeoutS, s.SupersedeKey,
		b2i(s.Interruptible), s.QueueTimeoutS, b2i(s.AdHoc),
		s.Submitter.Session, s.Submitter.Ref, s.Submitter.Team, s.Submitter.Host, nowMS, string(wire.StateQueued), nowMS, s.GitDir, b2i(pinned))
	return err
}

// supersedeOlder applies the supersede rule to every non-terminal job with the key, except the new one, and returns the ids it
// changed.
func supersedeOlder(ctx context.Context, c *sql.Conn, newID, key string, nowMS int64) ([]string, error) {
	rows, err := c.QueryContext(ctx, `SELECT id, state, interruptible FROM jobs
		WHERE supersede_key = ? AND id != ? AND state IN ('queued','claimed','running') ORDER BY seq`, key, newID)
	if err != nil {
		return nil, err
	}
	type old struct {
		id, state string
		intr      bool
	}
	var olds []old
	for rows.Next() {
		var o old
		var intr int
		if err := rows.Scan(&o.id, &o.state, &intr); err != nil {
			rows.Close()
			return nil, err
		}
		o.intr = intr == 1
		olds = append(olds, o)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var changed []string
	for _, o := range olds {
		if o.state == string(wire.StateQueued) {
			if err := finishWithoutRunning(ctx, c, o.id, wire.StateSuperseded, "superseded_by:"+newID, &newID, nowMS); err != nil {
				return nil, err
			}
		} else {
			// claimed or running: the worker owns it. An interruptible task is told to stop; any other runs on, and the result it
			// posts will carry superseded_by.
			if _, err := c.ExecContext(ctx, `UPDATE jobs SET superseded_by = ?, cancel_requested = cancel_requested OR ?, updated_at = ? WHERE id = ?`,
				newID, b2i(o.intr), nowMS, o.id); err != nil {
				return nil, err
			}
		}
		changed = append(changed, o.id)
	}
	return changed, nil
}

// finishWithoutRunning ends a queued job from the origin's side (superseded, cancelled, expired): nothing ran, so there is no exit.
func finishWithoutRunning(ctx context.Context, c *sql.Conn, id string, state wire.JobState, reason string, supersededBy *string, nowMS int64) error {
	fin := time64(nowMS)
	b, err := json.Marshal(wire.Result{Reason: reason, FinishedAt: &fin, SupersededBy: supersededBy})
	if err != nil {
		return err
	}
	var by any
	if supersededBy != nil {
		by = *supersededBy
	}
	_, err = c.ExecContext(ctx, `UPDATE jobs SET state = ?, result = ?, superseded_by = coalesce(?, superseded_by), finished_at = ?, updated_at = ? WHERE id = ?`,
		string(state), string(b), by, nowMS, nowMS, id)
	return err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
