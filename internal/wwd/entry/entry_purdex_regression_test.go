package entry

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The commands typed every day in this repository, matched against its own manifest. The answers were taken before the order of the matches
// and the flags that may be left out changed (#3813, #3814), and none of them is allowed to move.
func TestMatch_ThePurdexCommandsOfEveryDayDoNotMove(t *testing.T) {
	root, err := filepath.Abs("../../..")
	require.NoError(t, err)
	spa := filepath.Join(root, "spa")

	type want struct {
		action Action
		task   string
		args   []string
		queued bool
	}
	cases := []struct {
		name string
		cwd  string
		cmd  []string
		want want
	}{
		{"one package", root, []string{"go", "test", "./cmd/wwd/"}, want{ActionRewrite, "go-pkg", []string{"./cmd/wwd/"}, false}},
		{"two packages", root, []string{"go", "test", "./cmd/wwd/", "./internal/wwd/entry/"}, want{ActionRewrite, "go-pkg", []string{"./cmd/wwd/", "./internal/wwd/entry/"}, false}},
		{"how-to-run flags", root, []string{"go", "test", "-count=1", "-v", "./cmd/wwd/"}, want{ActionRewrite, "go-pkg", []string{"./cmd/wwd/"}, false}},
		{"the race detector", root, []string{"go", "test", "-race", "./internal/wwd/advisor/"}, want{ActionRewrite, "go-race", []string{"./internal/wwd/advisor/"}, true}},
		{"the whole tree", root, []string{"go", "test", "./..."}, want{ActionRewrite, "go-all", nil, true}},
		{"a tree under internal", root, []string{"go", "test", "./internal/..."}, want{ActionRewrite, "go-changed", []string{"./internal/..."}, true}},
		{"a tree under cmd/pdx", root, []string{"go", "test", "./cmd/pdx/..."}, want{ActionRewrite, "go-changed", []string{"./cmd/pdx/..."}, true}},
		{"make lint", root, []string{"make", "lint"}, want{ActionRewrite, "lint", nil, true}},
		{"tsc", spa, []string{"pnpm", "tsc", "-b"}, want{ActionRewrite, "tsc", nil, true}},
		{"tsc forced", spa, []string{"pnpm", "tsc", "-b", "--force"}, want{ActionRewrite, "tsc-force", nil, true}},
		{"vitest one file", spa, []string{"pnpm", "vitest", "run", "src/main.test.tsx"}, want{ActionRewrite, "spa-file", []string{"src/main.test.tsx"}, false}},
		{"vitest one file with the reporter", spa, []string{"pnpm", "vitest", "run", "--reporter=json", "src/main.tsx"}, want{ActionRewrite, "spa-file", []string{"src/main.tsx"}, false}},
		{"vitest everything", spa, []string{"pnpm", "vitest", "run"}, want{ActionRewrite, "spa-full", nil, true}},
		{"vitest workers", spa, []string{"npx", "vitest", "run", "--maxWorkers=3"}, want{ActionRewrite, "spa-full", nil, true}},
		{"vitest related", spa, []string{"pnpm", "vitest", "related", "src/main.tsx"}, want{ActionRewrite, "spa-related", []string{"src/main.tsx"}, true}},
		{"vitest related run", spa, []string{"pnpm", "vitest", "related", "--run", "src/main.tsx"}, want{ActionRewrite, "spa-related", []string{"src/main.tsx"}, true}},
		{"the e2e selection alone", root, []string{"go", "test", "-run", "TestX", "./cmd/wwd/"}, want{ActionAdhoc, "", nil, false}},
		{"the e2e tag alone", root, []string{"go", "test", "-tags", "e2e", "./cmd/wwd/"}, want{ActionAdhoc, "", nil, false}},
		{"not a family command", root, []string{"sh", "scripts/release-gate_test.sh"}, want{ActionNone, "", nil, false}},
	}
	for _, c := range cases {
		r := Match(c.cwd, c.cmd)
		assert.Equal(t, c.want.action, r.Action, c.name)
		assert.Equal(t, c.want.task, r.Task, c.name)
		assert.Equal(t, c.want.args, r.Args, c.name)
		assert.Equal(t, c.want.queued, r.Queued, c.name)
	}
}
