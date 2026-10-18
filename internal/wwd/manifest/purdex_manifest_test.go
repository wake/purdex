package manifest

import (
	"slices"
	"testing"
)

// aClassTasks are the tasks that may run on a host other than a Mac because they have been qualified there (spec §7: the `qualify`
// pair earns `task:<repo>/<task>`). None yet: a task is added here together with its `qualify` and the host's label, never alone.
var aClassTasks = map[string]bool{}

// Purdex's own manifest: every task that can leave the local machine needs os:darwin, or mserv (os:linux, with a worker on) would claim
// tsc, lint, the whole Go and SPA suites and fail them for what its machine lacks. Only a task on the A-class list, which also has a
// qualify, may go without.
func TestPurdexManifestTasksAreMacOnlyUnlessQualified(t *testing.T) {
	m, err := Load("../../../.wwd/tasks.toml")
	if err != nil {
		t.Fatal(err)
	}
	for name, task := range m.Tasks {
		if task.Local.Allowed && task.Local.Max < 0 {
			continue // always local: it never becomes a job
		}
		if slices.Contains(task.Labels, "os:darwin") {
			continue
		}
		if aClassTasks[name] && task.Qualify != nil {
			continue
		}
		t.Errorf("task %q can run on another host and has no os:darwin label (labels %v)", name, task.Labels)
	}
}
