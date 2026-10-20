package manifest

import (
	"slices"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

// S2: the entry rewrites what the manifest does not declare (an undeclared test or build command becomes an ad-hoc job), and the release
// gate's commands are tasks, so the lead's release run is `pdx test <task>` for each of them.
func TestPurdexManifestEntryRewritesUndeclaredCommands(t *testing.T) {
	m, err := Load("../../../.wwd/tasks.toml")
	if err != nil {
		t.Fatal(err)
	}
	if m.Entry.Mode != EntryRewrite {
		t.Fatalf("entry mode = %q, want rewrite", m.Entry.Mode)
	}
}

// The release tasks run what the release gate runs (CLAUDE.md "測試分層", the lead's test-<version>.sh, which this test cannot read): the strings
// are pinned here on purpose, so changing a release task's command is a decision made in this file too, and the gate script is compared by hand
// when it is. How the entry treats these commands is tested in internal/wwd/entry.
func TestPurdexReleaseTasksRunWhatTheReleaseGateRuns(t *testing.T) {
	m, err := Load("../../../.wwd/tasks.toml")
	if err != nil {
		t.Fatal(err)
	}
	for name, run := range map[string]string{
		"go-all":    "go test -json -p 3 -timeout 30m ./...",
		"spa-full":  "pnpm vitest run --maxWorkers=3 --reporter=json --outputFile={report}",
		"tsc-force": "pnpm tsc -b --force",
		"mod-all":   "go test -json ./cmd/pdx/...",
		"e2e":       "go test -json -tags e2e -run TestE2E -count=1 -timeout 20m ./cmd/wwd/",
	} {
		task := m.Tasks[name]
		if task == nil {
			t.Errorf("release task %s is missing", name)
			continue
		}
		if task.Run != run {
			t.Errorf("%s runs %q, want %q", name, task.Run, run)
		}
		if task.Class != wire.ClassRelease {
			t.Errorf("%s class = %q, want release", name, task.Class)
		}
	}
}

// e2e starts wwd daemons: it is never answered from a stored result (-count=1 says the same to go), never interrupted half way, and only a
// Mac with Go takes it.
func TestPurdexE2ETask(t *testing.T) {
	m, err := Load("../../../.wwd/tasks.toml")
	if err != nil {
		t.Fatal(err)
	}
	e2e := m.Tasks["e2e"]
	if e2e == nil {
		t.Fatal("no e2e task")
	}
	if e2e.Cacheable {
		t.Error("e2e must not be cacheable: it starts daemons and its result says nothing about the next commit's tree")
	}
	for _, l := range []string{"os:darwin", "tool:go1.26"} {
		if !slices.Contains(e2e.Labels, l) {
			t.Errorf("e2e lacks the label %s: %v", l, e2e.Labels)
		}
	}
	if e2e.Args != ArgsNone || e2e.Summary != "go-json" {
		t.Errorf("e2e args %q summary %q", e2e.Args, e2e.Summary)
	}
}
