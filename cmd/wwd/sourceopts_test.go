package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `[owner] enabled = false` turns the owner rule off, not the reading: the facts still carry who is at the machine, which the monitoring
// page shows (spec §3.2: with an empty rule the reading is reported and never counted). So the owner source is always asked for (#3682).
func TestTheOwnerIsAlwaysAskedFor(t *testing.T) {
	opt := sourceOptions([]string{"/opt/tools"}, func() int { return 7 })
	require.NotNil(t, opt.Owner)
	require.NotNil(t, opt.OwnerMinutes)
	assert.Equal(t, 7, opt.OwnerMinutes(), "the idle limit follows the worker settings (the rule in force)")
	assert.Equal(t, []string{"/opt/tools"}, opt.ToolDirs)
}
