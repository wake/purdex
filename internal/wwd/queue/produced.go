package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Produced is what the origin knows about how a succeeded job's result came to be (spec §4.5): the definition hash of the task as it was
// submitted, and what the worker that ended the job said about itself in the claim it posted the result under. Labels and Fingerprint are
// empty when that worker said nothing (an older worker): such a result is not filed for reuse.
type Produced struct {
	DefHash     string
	Labels      []string
	Fingerprint map[string]string
}

// claimFacts is the claim request's labels and fingerprint as the JSON the jobs row keeps; "" for a worker that sent none.
func claimFacts(req wire.ClaimRequest) (labels, fingerprint string) {
	if len(req.Labels) > 0 {
		if b, err := json.Marshal(req.Labels); err == nil {
			labels = string(b)
		}
	}
	if len(req.Fingerprint) > 0 {
		if b, err := json.Marshal(req.Fingerprint); err == nil {
			fingerprint = string(b)
		}
	}
	return labels, fingerprint
}

// producedOn reads what the claim that holds the job said about itself. A value that does not decode is treated as not said: the result is
// then simply not filed.
func producedOn(ctx context.Context, c interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, id string) (Produced, error) {
	var p Produced
	var labels, fp string
	if err := c.QueryRowContext(ctx, `SELECT def_hash, claim_labels, claim_fingerprint FROM jobs WHERE id = ?`, id).Scan(&p.DefHash, &labels, &fp); err != nil {
		return Produced{}, err
	}
	if labels != "" && json.Unmarshal([]byte(labels), &p.Labels) != nil {
		p.Labels = nil
	}
	if fp != "" && json.Unmarshal([]byte(fp), &p.Fingerprint) != nil {
		p.Fingerprint = nil
	}
	return p, nil
}

// reusable says whether a job that has just ended may have its result filed for reuse: a worker's success of a declared, cacheable task, not
// replaced by a newer submission while it ran, and not itself an answer from a stored result.
func reusable(j wire.Job) bool {
	r := j.Result
	return j.State == wire.StateSucceeded && j.Cacheable && !j.Local && !j.AdHoc &&
		r != nil && !r.CacheHit && r.SupersededBy == nil && r.Exit != nil && *r.Exit == 0
}

// comparable says whether a job that has just ended has an outcome to compare with the same work's on another host (spec §7): a worker's
// success or failure of a declared, cacheable task, with an exit of its own, not replaced by a newer submission while it ran, and not an
// answer from a stored result. A cancel, a timeout or an infrastructure error says nothing of the host's toolchain.
func comparable(j wire.Job) bool {
	r := j.Result
	return (j.State == wire.StateSucceeded || j.State == wire.StateFailed) && j.Cacheable && !j.Local && !j.AdHoc &&
		r != nil && !r.CacheHit && r.SupersededBy == nil && r.Exit != nil
}

// offerResult tells Options.OnSucceeded about a job that has just ended in this call (never an echo of an earlier result), once the end is
// committed. What the callback does cannot change the answer to the worker. (The outcome of the job is not offered: it is recorded inside
// the transaction of the end, see recordEndedOutcome.)
func (q *Queue) offerResult(ctx context.Context, j wire.Job, p Produced) {
	if q.opt.OnSucceeded == nil || !reusable(j) || j.Result.Log == "" {
		// no log: a result with none is not kept for reuse. If the log is still to come (the worker posts the result first), PutLog offers the
		// result when the log is in; if it never comes, the result is not kept.
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("queue: result of %s was not offered for reuse: %v", j.ID, r)
		}
	}()
	q.opt.OnSucceeded(context.WithoutCancel(ctx), j, p)
}
