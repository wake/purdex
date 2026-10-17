package run

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"
)

// ProcSource lists the processes whose environment carries the mark of a job, wherever they sit in the process tree. It reads
// the process table without starting anything (the facts' reader on macOS).
type ProcSource interface {
	JobProcesses(jobID string) ([]int, error)
}

// sweepPoll is how often a sweep looks again while it waits for processes to leave.
const sweepPoll = 50 * time.Millisecond

// sweep ends the processes that carry the job's mark and are still there after the process group was ended: a tmux server,
// vite or playwright that a test started and that daemonized out of the group. It is cleanup, not a security boundary — a
// process that removes the mark from its environment is not found (a known limit, see the tests). TERM first, KILL after
// killAfter for those still marked on a second look (so a recycled pid is not killed), and it returns the pids it found.
func sweep(src ProcSource, jobID string, killAfter time.Duration) (found []int, err error) {
	if src == nil {
		return nil, nil
	}
	pids, err := src.JobProcesses(jobID)
	if err != nil {
		return nil, err
	}
	pids = without(pids, os.Getpid())
	if len(pids) == 0 {
		return nil, nil
	}
	sort.Ints(pids)
	for _, p := range pids {
		_ = syscall.Kill(p, syscall.SIGTERM)
	}
	deadline := time.Now().Add(killAfter)
	for time.Now().Before(deadline) {
		if len(stillThere(src, jobID, pids)) == 0 {
			return pids, nil
		}
		time.Sleep(sweepPoll)
	}
	for _, p := range stillThere(src, jobID, pids) {
		_ = syscall.Kill(p, syscall.SIGKILL)
	}
	// give the kernel a moment to take them, so a caller that looks right after Run returns finds them gone
	for end := time.Now().Add(time.Second); time.Now().Before(end) && len(stillThere(src, jobID, pids)) > 0; {
		time.Sleep(sweepPoll)
	}
	return pids, nil
}

// stillThere are the pids of before that the source still lists under the job's mark.
func stillThere(src ProcSource, jobID string, before []int) []int {
	now, err := src.JobProcesses(jobID)
	if err != nil {
		return nil
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
	return out
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
