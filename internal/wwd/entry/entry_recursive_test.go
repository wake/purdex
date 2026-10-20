package entry

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func purdexRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../..")
	require.NoError(t, err)
	return root
}

// With the purdex manifest as it is, the commands of the S2-b0b report (mlab, 764): a recursive pattern is not "a package", so it must never
// be the local task (go-pkg: a light slot, the whole suite), and a flag that only says how to run must not keep a command from its task.
func TestMatch_RecursivePatternsAreNotLocal(t *testing.T) {
	root := purdexRoot(t)
	for name, c := range map[string]struct {
		argv   []string
		task   string
		args   []string
		queued bool
	}{
		"the whole tree is go-all, through the queue":                    {[]string{"go", "test", "./..."}, "go-all", nil, true},
		"a recursive pattern is go-changed, through the queue":           {[]string{"go", "test", "./internal/..."}, "go-changed", []string{"./internal/..."}, true},
		"two recursive patterns are go-changed":                          {[]string{"go", "test", "./cmd/...", "./internal/wwd/..."}, "go-changed", []string{"./cmd/...", "./internal/wwd/..."}, true},
		"-count=1 does not keep the whole tree from go-all":              {[]string{"go", "test", "-count=1", "./..."}, "go-all", nil, true},
		"-race stays go-race":                                            {[]string{"go", "test", "-race", "./internal/wwd/wire/"}, "go-race", []string{"./internal/wwd/wire/"}, true},
		"-race on the whole tree is go-race, not an ad-hoc job":          {[]string{"go", "test", "-race", "./..."}, "go-race", []string{"./..."}, true},
		"one package is still the local task":                            {[]string{"go", "test", "./internal/wwd/wire/"}, "go-pkg", []string{"./internal/wwd/wire/"}, false},
		"-v does not keep one package from the local task":               {[]string{"go", "test", "-v", "./internal/wwd/wire/"}, "go-pkg", []string{"./internal/wwd/wire/"}, false},
		"-p 3 (two words) does not keep one package from the local task": {[]string{"go", "test", "-p", "3", "./internal/wwd/wire/"}, "go-pkg", []string{"./internal/wwd/wire/"}, false},
		"-timeout 5m and -count 1 (two words each) do not either":        {[]string{"go", "test", "-timeout", "5m", "-count", "1", "./internal/wwd/wire/"}, "go-pkg", []string{"./internal/wwd/wire/"}, false},
		"-json -failfast -parallel=2 do not either":                      {[]string{"go", "test", "-json", "-failfast", "-parallel=2", "./internal/wwd/wire/"}, "go-pkg", []string{"./internal/wwd/wire/"}, false},
		"-count=1 -v on the whole tree is still go-all":                  {[]string{"go", "test", "-count=1", "-v", "./..."}, "go-all", nil, true},
	} {
		r := Match(root, c.argv)
		assert.Equal(t, ActionRewrite, r.Action, name)
		assert.Equal(t, c.task, r.Task, name)
		assert.Equal(t, c.args, r.Args, name)
		assert.Equal(t, c.queued, r.Queued, name)
	}
}

// The typed -p, -timeout and -count are replaced by the task's own values, also when the task writes the flag (#3711 R1): go-all has -p 3 and
// -timeout 30m, and `go test -p 4 ./...` is still go-all, not go-changed (which has no -p).
func TestMatch_RunFlagsAreReplacedByTheTasksOwnValues(t *testing.T) {
	root := purdexRoot(t)
	for name, argv := range map[string][]string{
		"-p with another value":                {"go", "test", "-p", "4", "./..."},
		"-p= with another value":               {"go", "test", "-p=1", "./..."},
		"-timeout with another value":          {"go", "test", "-timeout", "5m", "./..."},
		"-timeout= with another value":         {"go", "test", "-timeout=90m", "./..."},
		"both, and -count":                     {"go", "test", "-p", "1", "-timeout", "1h", "-count=2", "./..."},
		"-json (the task has it) with a value": {"go", "test", "-json=false", "./..."},
	} {
		r := Match(root, argv)
		assert.Equal(t, ActionRewrite, r.Action, name)
		assert.Equal(t, "go-all", r.Task, name)
		assert.True(t, r.Queued, name)
	}
}

// A flag that picks what runs, or that is not a way of running at all, is another command: it is not the task, whatever else matches.
func TestMatch_FlagsThatChangeWhatRunsAreNotIgnored(t *testing.T) {
	root := purdexRoot(t)
	for name, argv := range map[string][]string{
		"-run on the whole tree":      {"go", "test", "-run", "TestX", "./..."},
		"-tags on the whole tree":     {"go", "test", "-tags", "e2e", "./..."},
		"-cover on the whole tree":    {"go", "test", "-cover", "./..."},
		"-shuffle on a package":       {"go", "test", "-shuffle=on", "./internal/wwd/wire/"},
		"-run on a recursive pattern": {"go", "test", "-run", "TestX", "./internal/..."},
	} {
		r := Match(root, argv)
		assert.NotEqual(t, ActionRewrite, r.Action, name+": must not become a task that runs something else")
		assert.NotEmpty(t, r.Hint, name)
		assert.NotEqual(t, "e2e", r.Hint, name+": the hint must name a task that is near the command")
	}
	// the mode of the purdex manifest is rewrite: an undeclared command is an ad-hoc job, here, on a heavy slot
	assert.Equal(t, ActionAdhoc, Match(root, []string{"go", "test", "-run", "TestX", "./..."}).Action)
}

// The hint is the task nearest to the command: its plain words agree the longest. A flag of the command that only says how to run, and the
// flags of the task, are not words of that comparison.
func TestMatch_TheHintIsTheNearestTask(t *testing.T) {
	root := purdexRoot(t)
	assert.Equal(t, "go-all", Match(root, []string{"go", "test", "-run", "TestX", "-count=1", "./..."}).Hint)
}

// The guard on the purdex manifest: whatever the recursive pattern, it never comes out as a task that does not go through the queue.
func TestMatch_NoRecursivePatternIsEverLocal(t *testing.T) {
	root := purdexRoot(t)
	patterns := []string{"./...", "...", "./internal/...", "./cmd/...", "internal/wwd/...", "./internal/wwd/entry/...", "./spa/..."}
	for _, p := range patterns {
		for _, flags := range [][]string{nil, {"-count=1"}, {"-v"}, {"-race"}, {"-p", "2", "-timeout", "1m"}} {
			argv := append(append([]string{"go", "test"}, flags...), p)
			r := Match(root, argv)
			if r.Action == ActionRewrite {
				assert.True(t, r.Queued, strings.Join(argv, " ")+" became the local task "+r.Task)
			}
		}
	}
}
