package manifest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

// wwdLinuxPackages are the packages the release gate runs on mserv (Linux): the wwd packages that build and pass there. They are named one
// by one because the others (fastlane, entry, the tmux-bound ones) need a Mac. The list is test-763.sh's PKGS, and every entry must exist.
var wwdLinuxPackages = []string{
	"internal/wwd/facts", "internal/wwd/derived", "internal/wwd/labels", "internal/wwd/clones", "internal/wwd/run", "internal/wwd/jobdb",
	"internal/wwd/store", "internal/wwd/queue", "internal/wwd/snapshot", "internal/wwd/worker/quarantine", "internal/wwd/worker/jobexec",
	"internal/wwd/worker/runner", "internal/wwd/worker/loop", "internal/wwd/worker/report", "internal/wwd/worker/pick", "internal/wwd/gateheld",
	"internal/wwd/daemon", "internal/hostmem", "internal/wwd/orphans", "internal/wwd/deps", "internal/wwd/install", "internal/wwd/slicecfg",
	"internal/wwd/qualify", "internal/wwd/manifest", "internal/wwd/wire", "internal/wwd/client", "internal/wwd/jobscmd", "internal/wwd/manifestref",
	"internal/wwd/advisor",
}

func TestPurdexWWDLinuxTasks(t *testing.T) {
	m, err := Load("../../../.wwd/tasks.toml")
	if err != nil {
		t.Fatal(err)
	}
	linux := m.Tasks["wwd-linux"]
	if linux == nil {
		t.Fatal("no wwd-linux task")
	}
	want := "go test -json"
	for _, p := range wwdLinuxPackages {
		if _, err := os.Stat(filepath.Join("../../..", filepath.FromSlash(p))); err != nil {
			t.Errorf("package %s of the Linux list does not exist: %v", p, err)
		}
		want += " ./" + p + "/"
	}
	if linux.Run != want {
		t.Errorf("wwd-linux runs\n  %q\nwant\n  %q", linux.Run, want)
	}
	vet := m.Tasks["wwd-linux-vet"]
	if vet == nil {
		t.Fatal("no wwd-linux-vet task")
	}
	if vet.Run != "go vet -tags mserv,e2e ./internal/wwd/run/ ./internal/wwd/worker/jobexec/ ./cmd/wwd/" {
		t.Errorf("wwd-linux-vet runs %q", vet.Run)
	}
	for name, task := range map[string]*Task{"wwd-linux": linux, "wwd-linux-vet": vet} {
		if task.Class != wire.ClassRelease || task.Args != ArgsNone {
			t.Errorf("%s class %q args %q, want release and none", name, task.Class, task.Args)
		}
		// a mserv worker takes only a job whose labels name its os: label (explicit_labels_only), and a Mac must never be given a Linux job
		for _, l := range []string{"os:linux", "repo:purdex", "tool:go1.26"} {
			if !slices.Contains(task.Labels, l) {
				t.Errorf("%s lacks the label %s: %v", name, l, task.Labels)
			}
		}
		if slices.Contains(task.Labels, "os:darwin") {
			t.Errorf("%s carries os:darwin: %v", name, task.Labels)
		}
		if !slices.Contains(task.Deps, "go.sum") {
			t.Errorf("%s has no go.sum dependency set: the worker fills the module cache from it: %v", name, task.Deps)
		}
	}
}

// The scripts' own tests, one job each: a `run` has no shell, so they cannot be one task, and separate jobs run side by side with a result each.
func TestPurdexScriptTestTasks(t *testing.T) {
	m, err := Load("../../../.wwd/tasks.toml")
	if err != nil {
		t.Fatal(err)
	}
	for name, script := range map[string]string{
		"wwd-scripts-mysql":   "scripts/wwd-mysql-setup_test.sh",
		"wwd-scripts-c2":      "scripts/wwd-c2-matrix_test.sh",
		"wwd-scripts-c2-stub": "scripts/wwd-c2-matrix_stub_test.sh",
		"wwd-scripts-deploy":  "scripts/wwd-deploy_test.sh",
		"wwd-scripts-gate":    "scripts/release-gate_test.sh",
	} {
		task := m.Tasks[name]
		if task == nil {
			t.Errorf("no %s task", name)
			continue
		}
		if task.Run != "sh "+script {
			t.Errorf("%s runs %q, want %q", name, task.Run, "sh "+script)
		}
		if _, err := os.Stat(filepath.Join("../../..", script)); err != nil {
			t.Errorf("%s: the script does not exist: %v", name, err)
		}
		if task.Class != wire.ClassRelease || task.Args != ArgsNone || task.Cacheable {
			t.Errorf("%s class %q args %q cacheable %v, want release, none, not cacheable (they start processes)", name, task.Class, task.Args, task.Cacheable)
		}
		if !slices.Contains(task.Labels, "os:darwin") || strings.Contains(strings.Join(task.Labels, " "), "tool:") {
			t.Errorf("%s labels %v: a Mac, and no tool a shell script does not need", name, task.Labels)
		}
	}
}

// The release gate script (scripts/release-gate.sh) sends these tasks; a task renamed or dropped here must be changed there too.
func TestReleaseGateTaskListMatchesTheManifest(t *testing.T) {
	m, err := Load("../../../.wwd/tasks.toml")
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("../../../scripts/release-gate.sh")
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, line := range strings.Split(string(src), "\n") {
		if rest, ok := strings.CutPrefix(line, "GATE_TASKS="); ok {
			listed = strings.Fields(strings.Trim(rest, `"' `))
		}
	}
	if len(listed) == 0 {
		t.Fatal("release-gate.sh has no GATE_TASKS= line")
	}
	for _, name := range listed {
		// lint is a merge task that the gate runs as well (make lint: gofmt, go vet, the Linux vet of wwd)
		if task := m.Tasks[name]; task == nil || (task.Class != wire.ClassRelease && name != "lint") {
			t.Errorf("release-gate.sh sends %s, which is not a release task (or lint) of the manifest", name)
		}
	}
	// every release task is in the gate, except mod-all (--mod: the lead sends it only when cmd/pdx/plugin changed)
	for name, task := range m.Tasks {
		if task.Class == wire.ClassRelease && name != "mod-all" && !slices.Contains(listed, name) {
			t.Errorf("release task %s is not sent by release-gate.sh", name)
		}
	}
}
