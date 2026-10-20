package entry

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// With the purdex manifest as it is: a task whose command selects what runs (-run, -tags: the wwd e2e task) is the task only when the command
// says the same, never when it leaves the selection out. `go test -tags e2e ./cmd/wwd/` runs the whole package; read as the e2e task it would
// silently shrink to TestE2E and move to the release queue (#3688 R2).
func TestMatch_ThePurdexE2ETaskNeedsItsSelection(t *testing.T) {
	root, err := filepath.Abs("../../..")
	require.NoError(t, err)

	full := Match(root, []string{"go", "test", "-tags", "e2e", "-run", "TestE2E", "-count=1", "-timeout", "20m", "./cmd/wwd/"})
	assert.Equal(t, ActionRewrite, full.Action)
	assert.Equal(t, "e2e", full.Task)
	assert.True(t, full.Queued, "a release task goes through the queue")

	for name, cmd := range map[string][]string{
		"the tag without the -run":  {"go", "test", "-tags", "e2e", "./cmd/wwd/"},
		"the -run without the tag":  {"go", "test", "-run", "TestE2E", "-count=1", "./cmd/wwd/"},
		"the tag and -timeout only": {"go", "test", "-tags", "e2e", "-timeout", "20m", "./cmd/wwd/"},
	} {
		r := Match(root, cmd)
		assert.NotEqual(t, "e2e", r.Task, name+": leaving the selection out is another command, not the e2e task")
		assert.NotEqual(t, ActionRewrite, r.Action, name)
	}

	// the daily single-package command is still the development task
	r := Match(root, []string{"go", "test", "./cmd/wwd/"})
	assert.Equal(t, ActionRewrite, r.Action)
	assert.Equal(t, "go-pkg", r.Task)
	assert.False(t, r.Queued)
}
