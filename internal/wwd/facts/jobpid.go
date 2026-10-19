package facts

// JobOfPID returns the wwd job id in the environment of process pid, by the same check as everything else that asks "is this a wwd job"
// (JobFromEnv: the WWD_JOB variable, with a value that looks like a job id). It reads that one process's environment — a kernel call on
// macOS, /proc/<pid>/environ elsewhere — and keeps nothing of it. The answer is false for a pid that is gone, that belongs to another
// user, or whose environment cannot be read; like all facts it is an observation, not an authorisation.
//
// The load watcher (internal/watch) asks it once for the root of a tree that crossed a threshold, not for every process, so a job's
// queue-tree is labelled as the worker's own and does not wake the watcher (#3447).
func JobOfPID(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	return jobOfPID(pid)
}
