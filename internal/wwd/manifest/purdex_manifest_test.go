package manifest

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// aClassTasks are the tasks that may run on a host other than a Mac because they have been qualified there (spec §7: the `qualify`
// pair earns `task:<repo>/<task>`). A task is added here together with its `qualify` and the earned label in its labels, never alone.
var aClassTasks = map[string]bool{"tsc": true, "spa-related": true}

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
		if slices.Contains(task.Labels, "os:linux") {
			continue // a Linux task: a Mac's worker never holds os:linux, so it cannot be given the job (it runs on mserv)
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
		"linux-only":      {Labels: []string{"os:linux", "repo:purdex"}},
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

// The node and pnpm labels the SPA tasks carry: the worker advertises the major only (internal/wwd/labels/labels.go), so these are the
// exact strings a host has. A wrong major here leaves a task that no host can take, so they are checked whole, not by prefix.
const (
	nodeLabel = "tool:node24"
	pnpmLabel = "tool:pnpm10"
)

// toolNeeds says which tool: labels a task has to carry, from what it runs: a task that runs go (go test, make lint's gofmt and go vet)
// needs the Go of the repo's go.mod, one that runs pnpm needs node and pnpm, and one that runs claude needs tool:claude. goLabel is "tool:go<major>.<minor>".
func toolNeeds(task *Task, goLabel string) []string {
	var need []string
	if strings.Contains(task.Run, "go test") || strings.Contains(task.Run, "go vet") || strings.HasPrefix(task.Run, "make lint") {
		need = append(need, goLabel)
	}
	if strings.Contains(task.Run, "pnpm ") {
		need = append(need, nodeLabel, pnpmLabel)
	}
	if strings.HasPrefix(task.Run, "claude ") {
		need = append(need, "tool:claude") // no version in the label: a task needs the CLI, not a release (#3908)
	}
	return need
}

// A host without the tool must not claim the task (#3546): a task that runs go or pnpm carries the tool: labels for them, so a Mac whose
// worker has no go on its PATH never gets a job that fails for it. The Go label is the go.mod's major.minor, so raising Go makes this test
// red until the manifest is changed with it (the order is in docs/wwd-deploy.md).
func TestPurdexManifestTasksCarryTheToolsTheyRun(t *testing.T) {
	m, err := Load("../../../.wwd/tasks.toml")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("../../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	mm := regexp.MustCompile(`(?m)^go (\d+)\.(\d+)`).FindSubmatch(b)
	if mm == nil {
		t.Fatal("no go directive in go.mod")
	}
	goLabel := "tool:go" + string(mm[1]) + "." + string(mm[2])
	checked := 0
	for name, task := range m.Tasks {
		if task.Local.Allowed && task.Local.Max < 0 {
			continue
		}
		for _, need := range toolNeeds(task, goLabel) {
			checked++
			if !slices.Contains(task.Labels, need) {
				t.Errorf("task %s runs %q and has no %s label (labels %v)", name, task.Run, need, task.Labels)
			}
		}
		for _, l := range task.Labels { // a tool: label the task has no need of, or one with another version, is a task nobody can take
			if strings.HasPrefix(l, "tool:") && !slices.Contains(toolNeeds(task, goLabel), l) {
				t.Errorf("task %s carries %s, which is not what it runs needs (%v)", name, l, toolNeeds(task, goLabel))
			}
		}
	}
	if checked < 8 {
		t.Fatalf("only %d tool needs were checked: the test would prove nothing", checked)
	}
}

// An A-class task runs on a host other than a Mac only once that host has earned task:purdex/<name>: the label is in the task's labels (a
// job is claimable only by a host that holds all of them), it has no os:darwin, and its qualify names the reference host. Dropping the
// earned label would let any worker that has the tools claim it unqualified.
func TestAClassTasksAreClaimableOnlyByAHostThatEarnedThem(t *testing.T) {
	m, err := Load("../../../.wwd/tasks.toml")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		args []string
	}{
		{"tsc", nil}, // the whole run is the qualification
		{"spa-related", []string{"src/lib/crypto-hash.test.ts"}}, // one small test file is
	} {
		task := m.Tasks[c.name]
		if task == nil || task.Qualify == nil {
			t.Fatalf("%s has no qualify: %+v", c.name, task)
		}
		for _, l := range []string{"repo:purdex", "task:purdex/" + c.name, "tool:node24", "tool:pnpm10"} {
			if !slices.Contains(task.Labels, l) {
				t.Errorf("%s lacks the label %s: %v", c.name, l, task.Labels)
			}
		}
		if slices.Contains(task.Labels, "os:darwin") {
			t.Errorf("%s still needs os:darwin, so no other host could earn it: %v", c.name, task.Labels)
		}
		if task.Qualify.Reference != "air26" || !slices.Equal(task.Qualify.Args, c.args) {
			t.Errorf("%s qualify %+v", c.name, task.Qualify)
		}
		// What the reference host is asked to run has to exist: deleting a test file must not leave the qualification pointing at it (MON-12b-3).
		for _, a := range task.Qualify.Args {
			if _, err := os.Stat(filepath.Join("../../..", task.Cwd, a)); err != nil {
				t.Errorf("%s qualify names %s, which is not there: %v", c.name, a, err)
			}
		}
		if bad := macOnlyViolations(m, map[string]bool{}); !slices.Contains(bad, c.name) {
			t.Errorf("without the A-class entry %s would be a violation (the test would prove nothing): %v", c.name, bad)
		}
	}
}
