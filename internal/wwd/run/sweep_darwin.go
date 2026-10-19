//go:build darwin

package run

import (
	"time"

	"github.com/wake/purdex/internal/wwd/facts"
)

// DefaultProcSource reads the process table with the facts' reader (sysctl and libproc, no fork).
func DefaultProcSource() ProcSource { return factsSource{} }

type factsSource struct{}

// JobProcesses scans the whole table and keeps the processes whose environment names the job (see ProcSource for the origin tag). A
// reader belongs to one goroutine, so each scan makes its own.
func (factsSource) JobProcesses(tag, jobID string) ([]int, error) {
	r, err := facts.NewProcReader("")
	if err != nil {
		return nil, err
	}
	snap, ctx, err := r.Snapshot(time.Now())
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, p := range snap.Procs {
		if j, ok := ctx.JobOf(p.PID); ok && j == jobID {
			if o, has := ctx.OriginOf(p.PID); tag == "" || !has || o == tag {
				pids = append(pids, p.PID)
			}
		}
	}
	return pids, nil
}
