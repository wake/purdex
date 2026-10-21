package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func wouldWaitAuto(t *testing.T, cfg string, argv ...string) map[string]any {
	t.Helper()
	var out, errb bytes.Buffer
	args := append([]string{"would-wait", "--auto", "--json", "--config", cfg, "--"}, argv...)
	require.Equal(t, 0, gateMain(args, &out, &errb), errb.String())
	var w map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &w))
	return w
}

// --auto classifies the command by its words, as a shim would, and answers for that class.
func TestGate_WouldWaitAutoClassifiesTheCommand(t *testing.T) {
	cfg, _ := gateEnv(t, noAdmission)
	assert.Equal(t, "none", wouldWaitAuto(t, cfg, "ls", "-la")["class"])
	assert.Equal(t, "heavy", wouldWaitAuto(t, cfg, "go", "test", "./...")["class"])
	assert.Equal(t, "heavy", wouldWaitAuto(t, cfg, "./node_modules/.bin/tsc", "-b")["class"], "argv[0] may be a path")
	assert.Equal(t, false, wouldWaitAuto(t, cfg, "go", "test", "./...")["would_wait"])

	assert.Equal(t, false, wouldWaitAuto(t, cfg, "ls")["would_wait"])
}

func TestGate_WouldWaitAutoNeedsACommand(t *testing.T) {
	cfg, _ := gateEnv(t, noAdmission)
	var out, errb bytes.Buffer
	assert.Equal(t, 2, gateMain([]string{"would-wait", "--auto", "--config", cfg}, &out, &errb))
	assert.Equal(t, 2, gateMain([]string{"would-wait", "--config", cfg, "--", "go", "test"}, &out, &errb))
}
