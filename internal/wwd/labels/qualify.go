package labels

import (
	"slices"
	"strings"
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

// ParseQualifyKey reads a half's supersede key back. ok is false for any other key. The candidate is a host alias (no colon); the earned
// label (task:<repo>/<task>) has a colon of its own, so it is what lies between the first colon after the alias and the last colon.
func ParseQualifyKey(key string) (candidate, own, role string, ok bool) {
	rest, found := strings.CutPrefix(key, "qualify:")
	if !found {
		return "", "", "", false
	}
	candidate, rest, found = strings.Cut(rest, ":")
	if !found || candidate == "" {
		return "", "", "", false
	}
	i := strings.LastIndex(rest, ":")
	if i <= 0 {
		return "", "", "", false
	}
	own, role = rest[:i], rest[i+1:]
	if role != RoleCandidate && role != RoleReference {
		return "", "", "", false
	}
	return candidate, own, role, true
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
