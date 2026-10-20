package run

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// ProcSource lists the processes whose environment carries the mark of a job, wherever they sit in the process tree. It reads
// the process table without starting anything (the facts' reader on macOS). The mark is (origin tag, job id): a process matches when its
// WWD_JOB is the id and its WWD_ORIGIN is the tag, or it has no WWD_ORIGIN at all (a command started before the upgrade; only for the
// transition, #3373: a process of that kind can only belong to a job an older wwd started, and nothing new makes one). An empty tag in the
// question matches by id alone.
type ProcSource interface {
	JobProcesses(tag, jobID string) ([]int, error)
}

// SweepMarked ends the processes that still carry the mark of jobID (by id alone, any origin) and returns the pids it found: for a
// run that is not started by Run (the fast lane's own), after it has cleared the command's process group. src nil (a host that has no
// reader) finds nothing.
func SweepMarked(src ProcSource, jobID string, killAfter time.Duration) ([]int, error) {
	if src == nil || jobID == "" {
		return nil, nil
	}
	return sweepBounded(src, "", jobID, killAfter)
}

// sweepPoll is how often a sweep looks again while it waits for processes to leave.
const sweepPoll = 50 * time.Millisecond

// sweepSlack is added to killAfter for the whole sweep's time budget: scans of a big process table take their time.
var sweepSlack = 5 * time.Second

var errAbandoned = errors.New("sweep: abandoned after its time budget")

// sweepBounded is sweep under one overall deadline, so a process table that cannot be read (a hung sysctl) never holds Run:
// past the budget it returns an error and the scan goroutine is left to finish by itself.
func sweepBounded(src ProcSource, tag, jobID string, killAfter time.Duration) ([]int, error) {
	type out struct {
		pids []int
		err  error
	}
	var abandoned atomic.Bool
	done := make(chan out, 1)
	go func() {
		pids, err := sweep(src, tag, jobID, killAfter, &abandoned)
		done <- out{pids, err}
	}()
	t := time.NewTimer(killAfter + sweepSlack)
	defer t.Stop()
	select {
	case o := <-done:
		return o.pids, o.err
	case <-t.C:
		abandoned.Store(true) // a scan that returns later signals nobody: Run is already over
		return nil, fmt.Errorf("sweep: the process table was not read within %v", killAfter+sweepSlack)
	}
}

// sweep ends the processes that carry the job's mark and are still there after the process group was ended: a tmux server,
// vite or playwright that a test started and that daemonized out of the group. It is cleanup, not a security boundary — a
// process that removes the mark from its environment is not found (a known limit, see the tests). TERM first, KILL after
// killAfter for those still marked on a second look (so a recycled pid is not killed), and it returns the pids it found.
func sweep(src ProcSource, tag, jobID string, killAfter time.Duration, abandoned *atomic.Bool) (found []int, err error) {
	if src == nil {
		return nil, nil
	}
	pids, err := src.JobProcesses(tag, jobID)
	if err != nil {
		return nil, err
	}
	pids = without(pids, os.Getpid())
	if len(pids) == 0 {
		return nil, nil
	}
	sort.Ints(pids)
	for _, p := range pids {
		if abandoned.Load() {
			return pids, errAbandoned
		}
		_ = syscall.Kill(p, syscall.SIGTERM)
	}
	deadline := time.Now().Add(killAfter)
	for time.Now().Before(deadline) {
		left, err := stillThere(src, tag, jobID, pids)
		if err != nil {
			return pids, err
		}
		if len(left) == 0 {
			return pids, nil
		}
		time.Sleep(sweepPoll)
	}
	left, err := stillThere(src, tag, jobID, pids)
	if err != nil {
		return pids, err // not confirmed: no KILL on a guess, the pid may belong to someone else by now
	}
	for _, p := range left {
		if abandoned.Load() {
			return pids, errAbandoned
		}
		_ = syscall.Kill(p, syscall.SIGKILL)
	}
	// give the kernel a moment to take them, so a caller that looks right after Run returns finds them gone
	for end := time.Now().Add(time.Second); time.Now().Before(end); time.Sleep(sweepPoll) {
		left, err := stillThere(src, tag, jobID, pids)
		if err != nil {
			return pids, err
		}
		if len(left) == 0 {
			break
		}
	}
	return pids, nil
}

// stillThere are the pids of before that the source still lists under the job's mark; an unreadable table is an error, never
// "nothing left".
func stillThere(src ProcSource, tag, jobID string, before []int) ([]int, error) {
	now, err := src.JobProcesses(tag, jobID)
	if err != nil {
		return nil, err
	}
	in := map[int]bool{}
	for _, p := range now {
		in[p] = true
	}
	var out []int
	for _, p := range before {
		if in[p] {
			out = append(out, p)
		}
	}
	return out, nil
}

func without(pids []int, drop int) []int {
	out := pids[:0:0]
	for _, p := range pids {
		if p != drop {
			out = append(out, p)
		}
	}
	return out
}

// escapedNote is the line a sweep adds to the log, so a reader of the summary's tail sees that this task leaks processes.
func escapedNote(pids []int) string {
	s := make([]string, len(pids))
	for i, p := range pids {
		s[i] = fmt.Sprint(p)
	}
	return fmt.Sprintf("\n[wwd: %d process(es) carrying the job mark were still running outside the process group and were ended (pid %s); this task leaks processes]\n",
		len(pids), strings.Join(s, ", "))
}
