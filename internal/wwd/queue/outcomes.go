package queue

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

var jobIDRe = regexp.MustCompile(`^j_[0-9A-Za-z_-]{1,64}$`)

// What a recorded outcome was made under (spec §7): the host held the task's earned label when it claimed the job, or it is the task's
// reference host. A host that is neither has no standing, and its outcome is not compared: a result of a host nobody has vouched for
// differing from the reference's is not a disagreement about a label it does not hold.
const (
	StandingEarned    = "earned"
	StandingReference = "reference"
)

// Outcome is how one ended job of a declared task came out, on the host that made it.
type Outcome struct {
	JobID    string
	Host     string // result.host: who ran it
	Label    string // task:<repo>/<task>
	Tree     string // the snapshot's tree
	Task     string
	Args     []string
	DefHash  string    // the task's definition hash as submitted
	OK       bool      // the command exited 0
	Standing string    // StandingEarned or StandingReference
	At       time.Time // when it was recorded, on the origin's clock (retention counts from it)
}

func (o Outcome) validate() error {
	switch {
	case !jobIDRe.MatchString(o.JobID):
		return fmt.Errorf("outcome: job id %q", o.JobID)
	case !wire.ValidAlias(o.Host):
		return fmt.Errorf("outcome: host %q", o.Host)
	case !wire.ValidTaskLabel(o.Label):
		return fmt.Errorf("outcome: label %q", o.Label)
	case o.Tree == "" || o.Task == "" || o.DefHash == "":
		return fmt.Errorf("outcome: missing tree, task or definition hash")
	case o.Standing != StandingEarned && o.Standing != StandingReference:
		return fmt.Errorf("outcome: standing %q", o.Standing)
	case o.At.IsZero():
		return fmt.Errorf("outcome: no time")
	}
	return nil
}

// workKey identifies the work an outcome is of: two outcomes with one key are of the same snapshot, task, arguments and definition,
// under the same label. The arguments keep their order (another order is another run).
func (o Outcome) workKey() string {
	args := o.Args
	if args == nil {
		args = []string{}
	}
	b, err := json.Marshal([]any{1, o.Label, o.Tree, o.Task, args, o.DefHash})
	if err != nil { // strings and a slice of strings cannot fail to encode
		panic("queue: outcome: " + err.Error())
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// RecordOutcome writes the outcome and, in the same transaction, a disagreement for each pair it makes with an earlier outcome of the same
// work from another host that came out differently. Of a pair, the label to be quarantined is the non-reference host's; when neither is
// the reference (both earned) both labels are, because nobody can tell which of the two is wrong (spec §7). Only the most recent earlier
// outcome of each other host is paired, so a long run of outcomes does not become a long run of disagreements. The same job again is
// the same outcome (nothing is written twice). Returns the disagreements it wrote.
func (q *Queue) RecordOutcome(ctx context.Context, o Outcome) ([]wire.Disagreement, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	key := o.workKey()
	at := o.At.UnixMilli()
	var written []wire.Disagreement
	err := q.writeOnly(ctx, func(c *sql.Conn, _ *[]string) error {
		written = nil
		res, err := c.ExecContext(ctx, `INSERT OR IGNORE INTO task_outcomes (job_id, work_key, host, label, tree, task, ok, standing, at)
			VALUES (?,?,?,?,?,?,?,?,?)`, o.JobID, key, o.Host, o.Label, o.Tree, o.Task, b2i(o.OK), o.Standing, at)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil // this job was recorded before
		}
		// the latest outcome of each other host is the one to compare with: the host's earlier ones are superseded by it (a host that passed
		// and then failed agrees with a failing host), so the latest is chosen first and only then is it asked whether it differs
		rows, err := c.QueryContext(ctx, `SELECT job_id, host, standing, ok FROM task_outcomes
			WHERE work_key = ? AND host != ? ORDER BY seq DESC`, key, o.Host)
		if err != nil {
			return err
		}
		type earlier struct{ job, host, standing string }
		var pairs []earlier
		seen := map[string]bool{}
		for rows.Next() {
			var e earlier
			var ok int
			if err := rows.Scan(&e.job, &e.host, &e.standing, &ok); err != nil {
				rows.Close()
				return err
			}
			if seen[e.host] {
				continue
			}
			seen[e.host] = true
			if ok != b2i(o.OK) {
				pairs = append(pairs, e)
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, e := range pairs {
			for _, quarantine := range quarantined(o, e.host, e.standing) {
				other := e.host
				if quarantine == e.host {
					other = o.Host
				}
				r, err := c.ExecContext(ctx, `INSERT OR IGNORE INTO outcome_disagreements (host, label, other_host, tree, task, job_old, job_new, at)
					VALUES (?,?,?,?,?,?,?,?)`, quarantine, o.Label, other, o.Tree, o.Task, e.job, o.JobID, at)
				if err != nil {
					return err
				}
				if n, _ := r.RowsAffected(); n > 0 {
					id, _ := r.LastInsertId()
					written = append(written, wire.Disagreement{ID: id, Label: o.Label, Host: quarantine, OtherHost: other, Tree: o.Tree, Task: o.Task,
						Jobs: [2]string{e.job, o.JobID}, At: o.At})
				}
			}
		}
		return nil
	})
	return written, err
}

// EndedJob is a job that ended with an outcome to compare, with what its claim said about the host.
type EndedJob struct {
	Job      wire.Job
	Produced Produced
}

// UnrecordedOutcomes lists, newest first and at most limit of them, the jobs that ended within the last window with an outcome to compare
// (see comparable) and have no row in task_outcomes. The outcome of a job is written after its result is committed, and that write can
// fail (a locked database): the worker's repeat of the result is an echo and offers nothing again, and a restart forgets it. The jobs
// themselves keep everything the record needs, so the daemon asks for these now and then and records them again. Recording is
// idempotent. A job whose host has no standing for the task is listed every time until it leaves the window, which is why the list is
// bounded and newest first: a pile of them cannot keep the recent ones from being seen.
func (q *Queue) UnrecordedOutcomes(ctx context.Context, window time.Duration, limit int) ([]EndedJob, error) {
	cutoff := q.now().Add(-window).UnixMilli()
	rows, err := q.db.QueryContext(ctx, `SELECT id FROM jobs
		WHERE state IN ('succeeded','failed') AND cacheable = 1 AND local = 0 AND adhoc = 0 AND def_hash != '' AND claim_labels != ''
		  AND finished_at >= ? AND NOT EXISTS (SELECT 1 FROM task_outcomes o WHERE o.job_id = jobs.id)
		ORDER BY seq DESC LIMIT ?`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var out []EndedJob
	for _, id := range ids {
		j, err := q.Get(ctx, id)
		if err != nil {
			continue // gone since (retention)
		}
		if !comparable(j) {
			continue
		}
		p, err := producedOn(ctx, q.db, id)
		if err != nil {
			continue
		}
		out = append(out, EndedJob{Job: j, Produced: p})
	}
	return out, nil
}

// Origin is the alias of the host this queue belongs to: the one that records the outcomes it offers.
func (q *Queue) Origin() string { return q.opt.Origin }

// quarantined says which of the two hosts of a pair of different outcomes have the label quarantined: the non-reference one; both when
// neither is the reference; none when both are (a task has one reference host, so it does not arise).
func quarantined(o Outcome, otherHost, otherStanding string) []string {
	switch {
	case o.Standing == StandingReference && otherStanding == StandingReference:
		return nil
	case o.Standing == StandingReference:
		return []string{otherHost}
	case otherStanding == StandingReference:
		return []string{o.Host}
	default:
		return []string{o.Host, otherHost}
	}
}

// Disagreements returns the disagreements that concern host with a number above after, oldest first, at most limit of them (limit is
// capped at wire.MaxDisagreements), and whether more were waiting. For the list a reader asks with ?subject=<alias>&after=<id>.
func (q *Queue) Disagreements(ctx context.Context, host string, after int64, limit int) (items []wire.Disagreement, more bool, err error) {
	if limit <= 0 || limit > wire.MaxDisagreements {
		limit = wire.MaxDisagreements
	}
	rows, err := q.db.QueryContext(ctx, `SELECT seq, label, host, other_host, tree, task, job_old, job_new, at FROM outcome_disagreements
		WHERE host = ? AND seq > ? ORDER BY seq LIMIT ?`, host, after, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	items = []wire.Disagreement{}
	for rows.Next() {
		var d wire.Disagreement
		var atMS int64
		if err := rows.Scan(&d.ID, &d.Label, &d.Host, &d.OtherHost, &d.Tree, &d.Task, &d.Jobs[0], &d.Jobs[1], &atMS); err != nil {
			return nil, false, err
		}
		d.At = time64(atMS)
		if len(items) == limit {
			more = true
			break
		}
		items = append(items, d)
	}
	return items, more, rows.Err()
}

// pruneOutcomes drops the outcomes and disagreements recorded before cutoff (unix ms): they go when the jobs they describe do.
func (q *Queue) pruneOutcomes(ctx context.Context, cutoffMS int64) (int, error) {
	n := 0
	for _, stmt := range []string{`DELETE FROM task_outcomes WHERE at < ?`, `DELETE FROM outcome_disagreements WHERE at < ?`} {
		res, err := q.db.ExecContext(ctx, stmt, cutoffMS)
		if err != nil {
			return n, err
		}
		k, _ := res.RowsAffected()
		n += int(k)
	}
	return n, nil
}
