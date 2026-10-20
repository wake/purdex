package jobsmod

import "github.com/wake/purdex/internal/wwd/wire"

// PoolJob is a job as the App reads it from this daemon: wwd's Job, every field as wwd wrote it (the embedded struct's keys are flattened
// into the object), and the slot the job is charged (S11, spec §10) so that the queue strip and the task queue can tell heavy work from
// light. The slot is Purdex's own addition, derived from the job (wire.JobSlot); wwd's answer and wire.Job do not change.
type PoolJob struct {
	wire.Job
	Slot wire.Slot `json:"slot"`
}

// PoolJobsList is GET /api/pool/jobs: wwd's JobsListResponse with each job as a PoolJob. The cursor and the epoch are wwd's.
type PoolJobsList struct {
	Jobs   []PoolJob `json:"jobs"`
	Cursor string    `json:"cursor"`
	Epoch  string    `json:"epoch"`
}

// PoolJobDetail is GET /api/pool/jobs/{id}: wwd's JobDetail with the job as a PoolJob.
type PoolJobDetail struct {
	Job PoolJob `json:"job"`
	Log *string `json:"log"`
}

func poolJob(j wire.Job) PoolJob { return PoolJob{Job: j, Slot: wire.JobSlot(j)} }

// poolJobsList converts wwd's list job by job; no jobs is an empty array, never null.
func poolJobsList(r wire.JobsListResponse) PoolJobsList {
	out := PoolJobsList{Jobs: make([]PoolJob, len(r.Jobs)), Cursor: r.Cursor, Epoch: r.Epoch}
	for i, j := range r.Jobs {
		out.Jobs[i] = poolJob(j)
	}
	return out
}
