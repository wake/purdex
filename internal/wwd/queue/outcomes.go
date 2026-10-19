package queue

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"slices"
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
	At       time.Time // when it ended, on the origin's clock (retention counts from it)
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

// RecordOutcome writes the outcome and the disagreements it makes (see recordOutcomeOn) in a transaction of its own. A job's own end
// records its outcome inside the transaction that ends it (Result), so this is for what is not a job's end.
func (q *Queue) RecordOutcome(ctx context.Context, o Outcome) ([]wire.Disagreement, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	var written []wire.Disagreement
	err := q.writeOnly(ctx, func(c *sql.Conn, _ *[]string) (err error) {
		written, err = recordOutcomeOn(ctx, c, o)
		return err
	})
	return written, err
}

// recordOutcomeOn writes the outcome on the connection of a transaction and, in it, a disagreement for each pair it makes with an earlier
// outcome of the same work from another host that came out differently. Of a pair, the label to be quarantined is the non-reference
// host's; when neither is the reference (both earned) both labels are, because nobody can tell which of the two is wrong (spec §7). Each
// other host is compared by its latest outcome of the work, so a long run of outcomes does not become a long run of disagreements and a
// host's earlier outcomes do not count once it has a later one. The same job again is the same outcome (nothing is written twice).
func recordOutcomeOn(ctx context.Context, c *sql.Conn, o Outcome) ([]wire.Disagreement, error) {
	key := o.workKey()
	at := o.At.UnixMilli()
	res, err := c.ExecContext(ctx, `INSERT OR IGNORE INTO task_outcomes (job_id, work_key, host, label, tree, task, ok, standing, at)
		VALUES (?,?,?,?,?,?,?,?,?)`, o.JobID, key, o.Host, o.Label, o.Tree, o.Task, b2i(o.OK), o.Standing, at)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil // this job was recorded before
	}
	// the latest outcome of each other host (by the index on work_key, host, seq, not by reading all of them) is the one to compare with
	rows, err := c.QueryContext(ctx, `SELECT job_id, host, standing, ok FROM task_outcomes AS t
		WHERE work_key = ? AND host != ?
		  AND seq = (SELECT max(seq) FROM task_outcomes WHERE work_key = t.work_key AND host = t.host)`, key, o.Host)
	if err != nil {
		return nil, err
	}
	type earlier struct{ job, host, standing string }
	var pairs []earlier
	for rows.Next() {
		var e earlier
		var ok int
		if err := rows.Scan(&e.job, &e.host, &e.standing, &ok); err != nil {
			rows.Close()
			return nil, err
		}
		if ok != b2i(o.OK) {
			pairs = append(pairs, e)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var written []wire.Disagreement
	for _, e := range pairs {
		for _, quarantine := range quarantined(o, e.host, e.standing) {
			other := e.host
			if quarantine == e.host {
				other = o.Host
			}
			r, err := c.ExecContext(ctx, `INSERT OR IGNORE INTO outcome_disagreements (host, label, other_host, tree, task, job_old, job_new, at)
				VALUES (?,?,?,?,?,?,?,?)`, quarantine, o.Label, other, o.Tree, o.Task, e.job, o.JobID, at)
			if err != nil {
				return nil, err
			}
			if n, _ := r.RowsAffected(); n > 0 {
				id, _ := r.LastInsertId()
				written = append(written, wire.Disagreement{ID: id, Label: o.Label, Host: quarantine, OtherHost: other, Tree: o.Tree, Task: o.Task,
					Jobs: [2]string{e.job, o.JobID}, At: o.At})
			}
		}
	}
	return written, nil
}

// recordEndedOutcome records, in the transaction that ends the job, how it came out (spec §7): when the job has an outcome to compare (see
// comparable) and its host vouches for the task: the claim's own labels held the task's label (earned), or the host is the task's
// reference. It is the same transaction as the end, so the outcome cannot be lost to a failure after the result is taken, and it is dated
// and judged as of the end: by the origin's clock, and by the reference as it was then (reference is asked before the transaction, since
// finding it may read manifests, which is not done with the write lock held). A host with neither is not compared: it has no label to
// lose. A job from before the definition hash was kept, or a claim that said nothing, is not recorded.
func (q *Queue) recordEndedOutcome(ctx context.Context, c *sql.Conn, j wire.Job, p Produced, reference string) error {
	if !comparable(j) || p.DefHash == "" || len(p.Labels) == 0 || j.Result.Host == "" {
		return nil
	}
	label := "task:" + j.Repo.Name + "/" + j.Task
	var standing string
	switch {
	case reference != "" && j.Result.Host == reference:
		standing = StandingReference
	case slices.Contains(p.Labels, label):
		standing = StandingEarned
	default:
		return nil
	}
	o := Outcome{JobID: j.ID, Host: j.Result.Host, Label: label, Tree: j.Snapshot.Tree, Task: j.Task, Args: j.Args, DefHash: p.DefHash,
		OK: *j.Result.Exit == 0, Standing: standing, At: q.now()}
	if err := o.validate(); err != nil {
		// something about the job cannot be recorded (a label that is not a task label, a host name that is not one): not worth failing the
		// end of the job over, and recording it again would fail the same way
		log.Printf("queue: outcome of %s was not recorded: %v", j.ID, err)
		return nil
	}
	found, err := recordOutcomeOn(ctx, c, o)
	if err != nil {
		return err
	}
	for _, f := range found {
		log.Printf("queue: outcomes: %s disagrees with %s on %s (tree %s): jobs %s and %s; the label of %s is to be quarantined",
			f.Host, f.OtherHost, f.Label, f.Tree, f.Jobs[0], f.Jobs[1], f.Host)
	}
	return nil
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
