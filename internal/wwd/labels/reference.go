package labels

import (
	"slices"
	"sort"
	"strings"

	"github.com/wake/purdex/internal/wwd/wire"
)

// RefTask is a task of a repo's manifest that is qualified against a reference host (its `qualify.reference`): what the host needs
// to hold the task's label without qualifying, if it is that reference.
type RefTask struct {
	Repo      string
	Task      string
	Reference string   // the alias of the reference host
	Needs     []string // the task's labels (tool:*, os:*, ...): the host must advertise all of them
}

// TaskLabel is the earned label of a task: task:<repo>/<task>.
func TaskLabel(repo, task string) string { return "task:" + repo + "/" + task }

// envPrefixes are the labels a task may need as a precondition of its environment. Anything else a manifest lists (task:, host:, repo:,
// a label of its own) is not an environment requirement and grants nothing: a task that lists one is not held without qualifying.
var envPrefixes = []string{"os:", "arch:", "tool:"}

func isEnvRequirement(l string) bool {
	for _, p := range envPrefixes {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

// ReferenceLabels is the `task:` labels the host named alias holds without qualifying (spec §7): for each task whose reference is
// this host, when the host has the task's repo (repo:<name> among have) and every label the task needs. have is the host's labels
// without the task labels themselves. The result is sorted and has no duplicates.
func ReferenceLabels(alias string, have []string, tasks []RefTask) []string {
	out := []string{}
	for _, t := range tasks {
		if t.Reference != alias || alias == "" {
			continue
		}
		if !slices.Contains(have, "repo:"+t.Repo) {
			continue
		}
		ok := true
		for _, n := range t.Needs {
			if !isEnvRequirement(n) || !slices.Contains(have, n) {
				ok = false
				break
			}
		}
		if l := TaskLabel(t.Repo, t.Task); ok && wire.ValidTaskLabel(l) { // never a label the store would refuse to keep
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return slices.Compact(out)
}
