package core

import (
	"bytes"
	"log"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/config"
)

func TestRequestRestart_AcceptsOnceAndRecordsExecPath(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	calls := 0
	c.SetRestartHook(func() { calls++ })
	assert.Equal(t, "", c.RestartExecPath())

	require.NoError(t, c.RequestRestart("dev rebuild", "/repo/bin/pdx", nil))
	assert.Equal(t, 1, calls)
	assert.Equal(t, "/repo/bin/pdx", c.RestartExecPath(), "the exec path of the freshly built binary is kept for the re-exec")

	err := c.RequestRestart("dev rebuild", "/other/pdx", nil)
	assert.ErrorIs(t, err, ErrRestartInProgress)
	assert.Equal(t, 1, calls, "a second request must not fire the hook again")
	assert.Equal(t, "/repo/bin/pdx", c.RestartExecPath(), "a refused request must not overwrite the path")
}

func TestRequestRestart_Refusals(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	assert.ErrorIs(t, c.RequestRestart("x", "/p", nil), ErrRestartUnavailable)

	c.SetRestartHook(func() { t.Fatal("hook must not fire") })
	c.BeginShutdown()
	assert.ErrorIs(t, c.RequestRestart("x", "/p", nil), ErrShuttingDown)
	assert.Equal(t, "", c.RestartExecPath())
}

func TestRequestRestart_LogsReason(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	c.SetRestartHook(func() {})
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)
	require.NoError(t, c.RequestRestart("dev rebuild from 1.2.3.4:5 (abc to def)", "", nil))
	assert.Contains(t, buf.String(), "daemon restart requested by dev rebuild from 1.2.3.4:5 (abc to def)")
}
