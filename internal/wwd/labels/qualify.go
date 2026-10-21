package labels

import (
	"slices"
	"strings"

	"github.com/wake/purdex/internal/wwd/wire"
)

// QualifySession is the submitter session of the two halves of a qualification pair (the coordinator in package qualify queues them).
const QualifySession = "wwd-qualify"

// The two roles of a half.
const (
	RoleCandidate = "candidate"
	RoleReference = "reference"
)

// QualifyKey is the supersede key of a half: qualify:<candidate host>:<the task's earned label>:<role>.
func QualifyKey(candidate, own, role string) string {
	return "qualify:" + candidate + ":" + own + ":" + role
}

// QualifyRole says which role a supersede key is the key of for this candidate and earned label (ok false when it is neither). The key is
// rebuilt and compared, not parsed: an alias may hold a colon, so the key does not split back unambiguously.
func QualifyRole(key, candidate, own string) (role string, ok bool) {
	for _, r := range []string{RoleCandidate, RoleReference} {
		if key == QualifyKey(candidate, own, r) {
			return r, true
		}
	}
	return "", false
}

// QualifyHalfLabels is the label set of one half of a pair, worked out from the task's own labels: the candidate asks for what a host
// that has not earned the label can offer (no os:/arch:, not the task's own label) and the reference keeps every label the task has;
// both add host:<the host the half is meant to run on>. The coordinator builds the halves with it and a worker checks a remote half
// against it, so the two cannot drift.
func QualifyHalfLabels(taskLabels []string, own, role, host string) []string {
	var out []string
	for _, l := range taskLabels {
		if role == RoleCandidate && (l == own || strings.HasPrefix(l, "os:") || strings.HasPrefix(l, "arch:")) {
			continue
		}
		out = append(out, l)
	}
	return append(slices.Clone(out), "host:"+host)
}

// QualifyClass is the class both halves of a qualification pair run at, worked out from the task's own: the task's class capped at merge
// (release becomes merge), and dev when it is empty or not a class. A dev class half starves behind a steady stream of merge work (the
// pick score is 1000 per class rank plus minutes waited), so a pair follows the task. The coordinator sets it and the worker checks it.
func QualifyClass(task wire.JobClass) wire.JobClass {
	switch task {
	case wire.ClassRelease, wire.ClassMerge:
		return wire.ClassMerge
	}
	return wire.ClassDev
}
