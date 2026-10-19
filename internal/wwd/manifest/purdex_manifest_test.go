package manifest

import (
	"slices"
	"sort"
	"testing"
)

// aClassTasks are the tasks that may run on a host other than a Mac because they have been qualified there (spec §7: the `qualify`
// pair earns `task:<repo>/<task>`). A task is added here together with its `qualify` and the earned label in its labels, never alone.
var aClassTasks = map[string]bool{"tsc": true}

// macOnlyViolations names the tasks of m that can run on a host other than a Mac without being qualified there: a task that can leave the
// local machine needs os:darwin, or mserv (os:linux, with a worker on) would claim tsc, lint, the whole Go and SPA suites and fail them
// for what its machine lacks. Only a task on the A-class list may go without, and only with a `qualify` AND the earned label
// task:purdex/<name> in its labels (a `qualify` alone limits nobody: a job is claimable by any host that has the job's labels).
func macOnlyViolations(m *Manifest, aClass map[string]bool) []string {
	var bad []string
	for name, task := range m.Tasks {
		if task.Local.Allowed && task.Local.Max < 0 {
			continue // always local: it never becomes a job
		}
		if slices.Contains(task.Labels, "os:darwin") {
			continue
		}
		if aClass[name] && task.Qualify != nil && slices.Contains(task.Labels, "task:purdex/"+name) {
			continue
		}
		bad = append(bad, name)
	}
	sort.Strings(bad)
	return bad
}

func TestPurdexManifestTasksAreMacOnlyUnlessQualified(t *testing.T) {
	m, err := Load("../../../.wwd/tasks.toml")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Tasks) < 10 {
		t.Fatalf("the manifest has %d tasks: the test would prove nothing", len(m.Tasks))
	}
	if bad := macOnlyViolations(m, aClassTasks); len(bad) > 0 {
		t.Errorf("tasks that can run on another host and have no os:darwin label: %v", bad)
	}
}

func TestMacOnlyViolationsRules(t *testing.T) {
	q := &Qualify{Args: []string{"a"}, Reference: "air26"}
	m := &Manifest{Tasks: map[string]*Task{
		"mac":             {Labels: []string{"os:darwin", "repo:purdex"}},
		"bare":            {},
		"local-always":    {Local: LocalPolicy{Allowed: true, Max: -1}},
		"local-capped":    {Local: LocalPolicy{Allowed: true, Max: 3}},
		"a-with-earned":   {Labels: []string{"repo:purdex", "task:purdex/a-with-earned"}, Qualify: q},
		"a-no-qualify":    {Labels: []string{"repo:purdex", "task:purdex/a-no-qualify"}},
		"a-no-earned":     {Labels: []string{"repo:purdex"}, Qualify: q},
		"not-on-the-list": {Labels: []string{"repo:purdex", "task:purdex/not-on-the-list"}, Qualify: q},
	}}
	aClass := map[string]bool{"a-with-earned": true, "a-no-qualify": true, "a-no-earned": true}
	got := macOnlyViolations(m, aClass)
	want := []string{"a-no-earned", "a-no-qualify", "bare", "local-capped", "not-on-the-list"}
	if !slices.Equal(got, want) {
		t.Fatalf("violations %v, want %v", got, want)
	}
}

// tsc runs on a host other than a Mac only once that host has earned task:purdex/tsc: the label is in the task's labels (a job is claimable
// only by a host that holds all of them), it has no os:darwin, and its qualify names the reference host with no args (the whole run is the
// qualification). Dropping the earned label would let any worker that has tool:node24 claim it unqualified.
func TestTscIsClaimableOnlyByAHostThatEarnedIt(t *testing.T) {
	m, err := Load("../../../.wwd/tasks.toml")
	if err != nil {
		t.Fatal(err)
	}
	tsc := m.Tasks["tsc"]
	if tsc == nil || tsc.Qualify == nil {
		t.Fatalf("tsc has no qualify: %+v", tsc)
	}
	for _, l := range []string{"repo:purdex", "task:purdex/tsc", "tool:node24"} {
		if !slices.Contains(tsc.Labels, l) {
			t.Errorf("tsc lacks the label %s: %v", l, tsc.Labels)
		}
	}
	if slices.Contains(tsc.Labels, "os:darwin") {
		t.Errorf("tsc still needs os:darwin, so no other host could earn it: %v", tsc.Labels)
	}
	if tsc.Qualify.Reference != "air26" || len(tsc.Qualify.Args) != 0 {
		t.Errorf("qualify %+v", tsc.Qualify)
	}
	if bad := macOnlyViolations(m, map[string]bool{}); !slices.Contains(bad, "tsc") {
		t.Errorf("without the A-class entry tsc would be a violation (the test would prove nothing): %v", bad)
	}
}
