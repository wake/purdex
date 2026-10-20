package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Filter selects jobs for List.
type Filter struct {
	States           []wire.JobState // any of these states; empty = all
	SubmitterSession string          // only this session's jobs
	// ForPeers is a listing for another host: ad-hoc and fast-lane (local) jobs are left out whole (spec 4.4, 8.3, 11), not just their args.
	ForPeers bool
	Limit    int // the oldest Limit jobs; 0 = all
}

const jobColumns = `id, origin, repo_name, repo_root, repo_remote, snap_commit, snap_tree, snap_base, snap_rel_cwd,
	task, args, class, labels, locks, cacheable, timeout_s, supersede_key, interruptible, queue_timeout_s, adhoc,
	sub_session, sub_ref, sub_team, sub_host, created_at, state, attempts, claim_worker, claim_since, result, local, manual, manual_seq, started_at`

// Get returns one job (the local view: ad-hoc jobs included). The claim_id is never part of it.
func (q *Queue) Get(ctx context.Context, id string) (wire.Job, error) {
	return getOn(ctx, q.db, id, false)
}

// GetForPeers is Get for a request from another host: an ad-hoc job is not found, whatever its id (spec 4.4, 8.3, 11).
func (q *Queue) GetForPeers(ctx context.Context, id string) (wire.Job, error) {
	return getOn(ctx, q.db, id, true)
}

// rowQuerier is a *sql.DB or a *sql.Conn: a job is read on a connection inside a transaction, or on the pool.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func getOn(ctx context.Context, rq rowQuerier, id string, forPeers bool) (wire.Job, error) {
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE id = ?`
	if forPeers {
		query += ` AND adhoc = 0 AND local = 0`
	}
	j, err := scanJob(rq.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return wire.Job{}, ErrNotFound
	}
	return j, err
}

// List returns the jobs that match f, oldest first.
func (q *Queue) List(ctx context.Context, f Filter) ([]wire.Job, error) {
	jobs, _, err := q.list(ctx, f, nil)
	return jobs, err
}

// CountQueued is the number of jobs waiting in the queue (state queued), for facts.queue.queued (spec §13.2).
func (q *Queue) CountQueued(ctx context.Context) (int, error) {
	var n int
	err := q.db.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE state = ?`, string(wire.StateQueued)).Scan(&n)
	return n, err
}

// revScanner lets scanJob read a row that has the job's rev after the job's columns.
type revScanner struct {
	r   scanner
	rev *int64
}

func (s revScanner) Scan(dest ...any) error { return s.r.Scan(append(dest, s.rev)...) }

// list is List; with after set it is the cursor read (jobs whose rev is above it, oldest change first) and returns each job's rev.
func (q *Queue) list(ctx context.Context, f Filter, after *int64) ([]wire.Job, []int64, error) {
	var where []string
	var args []any
	if after != nil {
		where = append(where, "rev > ?")
		args = append(args, *after)
	}
	if len(f.States) > 0 {
		marks := make([]string, len(f.States))
		for i, s := range f.States {
			marks[i] = "?"
			args = append(args, string(s))
		}
		where = append(where, "state IN ("+strings.Join(marks, ",")+")")
	}
	if f.SubmitterSession != "" {
		where = append(where, "sub_session = ?")
		args = append(args, f.SubmitterSession)
	}
	if f.ForPeers {
		where = append(where, "adhoc = 0", "local = 0")
	}
	query := `SELECT ` + jobColumns
	order := ` ORDER BY seq`
	if after != nil {
		query += `, rev`
		order = ` ORDER BY rev`
	}
	query += ` FROM jobs`
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, " AND ")
	}
	query += order
	if f.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := []wire.Job{}
	var revs []int64
	for rows.Next() {
		var rev int64
		var src scanner = rows
		if after != nil {
			src = revScanner{r: rows, rev: &rev}
		}
		j, err := scanJob(src)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, j)
		if after != nil {
			revs = append(revs, rev)
		}
	}
	return out, revs, rows.Err()
}

type scanner interface{ Scan(dest ...any) error }

func scanJob(r scanner) (wire.Job, error) {
	var (
		j                                      wire.Job
		args, labels, locks, class, state      string
		cacheable, interruptible, adhoc, local int
		created, claimSince, startedAt         sql.NullInt64
		attempts                               int
		claimWorker, result                    sql.NullString
		manual                                 string
	)
	if err := r.Scan(&j.ID, &j.Origin, &j.Repo.Name, &j.Repo.Root, &j.Repo.Remote,
		&j.Snapshot.Commit, &j.Snapshot.Tree, &j.Snapshot.Base, &j.Snapshot.RelCwd,
		&j.Task, &args, &class, &labels, &locks, &cacheable, &j.TimeoutS, &j.SupersedeKey, &interruptible, &j.QueueTimeoutS, &adhoc,
		&j.Submitter.Session, &j.Submitter.Ref, &j.Submitter.Team, &j.Submitter.Host, &created, &state, &attempts, &claimWorker, &claimSince, &result, &local, &manual, &j.ManualSeq, &startedAt); err != nil {
		return wire.Job{}, err
	}
	j.Class, j.State, j.Manual = wire.JobClass(class), wire.JobState(state), wire.Position(manual)
	j.Cacheable, j.Interruptible, j.AdHoc, j.Local = cacheable == 1, interruptible == 1, adhoc == 1, local == 1
	j.CreatedAt = time64(created.Int64)
	for dst, src := range map[*[]string]string{&j.Args: args, &j.Labels: labels, &j.Locks: locks} {
		if err := json.Unmarshal([]byte(src), dst); err != nil {
			return wire.Job{}, err
		}
		if *dst == nil {
			*dst = []string{}
		}
	}
	if startedAt.Valid {
		t := time64(startedAt.Int64)
		j.StartedAt = &t
	}
	// a claim is shown while someone holds it; attempt is the number this claim is (counted attempts so far, plus this one)
	if claimWorker.Valid && (j.State == wire.StateClaimed || j.State == wire.StateRunning) {
		j.Claim = &wire.Claim{Worker: claimWorker.String, Attempt: attempts + 1, Since: time64(claimSince.Int64)}
	}
	if result.Valid {
		var r wire.Result
		if err := json.Unmarshal([]byte(result.String), &r); err != nil {
			return wire.Job{}, err
		}
		if err := r.Validate(); err != nil { // a result is checked when it is read back as when it was posted (the digest, WWD-5b-2r)
			return wire.Job{}, fmt.Errorf("job %s: stored result: %w", j.ID, err)
		}
		j.Result = &r
	}
	return j, nil
}

// time64 is a unix-millisecond time as the origin stored it, in UTC.
func time64(ms int64) time.Time { return time.UnixMilli(ms).UTC() }
