package manifest

import (
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// aClassTasks are the tasks that may run on a host other than a Mac because they have been qualified there (spec §7: the `qualify`
// pair earns `task:<repo>/<task>`). None yet: a task is added here together with its `qualify` and the earned label in its labels, never
// alone.
var aClassTasks = map[string]bool{}

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

// toolNeedsExempt are the tasks whose tool needs are decided elsewhere: tsc and tsc-force carry their own labels (5b-2c-2).
var toolNeedsExempt = map[string]bool{"tsc": true, "tsc-force": true}

// The node and pnpm labels the SPA tasks carry: the worker advertises the major only (internal/wwd/labels/labels.go), so these are the
// exact strings a host has. A wrong major here leaves a task that no host can take, so they are checked whole, not by prefix.
const (
	nodeLabel = "tool:node24"
	pnpmLabel = "tool:pnpm10"
)

// toolNeeds says which tool: labels a task has to carry, from what it runs: a task that runs go (go test, make lint's gofmt and go vet)
// needs the Go of the repo's go.mod, and one that runs pnpm needs node and pnpm. goLabel is "tool:go<major>.<minor>".
func toolNeeds(task *Task, goLabel string) []string {
	var need []string
	if strings.Contains(task.Run, "go test") || strings.HasPrefix(task.Run, "make lint") {
		need = append(need, goLabel)
	}
	if strings.Contains(task.Run, "pnpm ") {
		need = append(need, nodeLabel, pnpmLabel)
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
		if toolNeedsExempt[name] || (task.Local.Allowed && task.Local.Max < 0) {
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
