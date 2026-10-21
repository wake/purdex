package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func wouldWaitBatchOf(t *testing.T, cfg string, argvs [][]string) []map[string]any {
	t.Helper()
	b, _ := json.Marshal(argvs)
	var out, errb bytes.Buffer
	require.Equal(t, 0, gateMain([]string{"would-wait", "--auto-batch", string(b), "--json", "--config", cfg}, &out, &errb), errb.String())
	var ws []map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &ws), out.String())
	return ws
}

// The answer for each command is the one it would have had asked alone, whatever the state of the gate: free, and with a heavy
// slot held (the commands of that class wait, the others do not).
func TestGate_WouldWaitBatchAnswersAsOneByOne(t *testing.T) {
	cfg, _ := gateEnv(t, noAdmission)
	argvs := [][]string{{"go", "test", "./..."}, {"ls", "-la"}, {"./node_modules/.bin/tsc", "-b"}, {"pnpm", "run", "build"}, {"git", "status"}}
	same := func(label string) {
		t.Helper()
		got := wouldWaitBatchOf(t, cfg, argvs)
		require.Len(t, got, len(argvs), label)
		for i, argv := range argvs {
			assert.Equal(t, wouldWaitAuto(t, cfg, argv...), got[i], label+": "+argv[0]+" at "+string(rune('0'+i)))
		}
	}
	same("free")
	got := wouldWaitBatchOf(t, cfg, argvs)
	for i := range got {
		assert.Equal(t, false, got[i]["would_wait"], "nothing waits for a slot any more")
	}
	assert.Equal(t, "heavy", got[0]["class"])
	assert.Equal(t, "none", got[1]["class"], "ls is not a command the gate holds")
}

// Text output: one line per command, in order.
func TestGate_WouldWaitBatchTextHasALinePerCommand(t *testing.T) {
	cfg, _ := gateEnv(t, noAdmission)
	var out, errb bytes.Buffer
	require.Equal(t, 0, gateMain([]string{"would-wait", "--auto-batch", `[["ls"],["go","test"]]`, "--config", cfg}, &out, &errb), errb.String())
	assert.Equal(t, "no\nno\n", out.String())
}

// Anything that is not a non-empty list of non-empty argv lists, or that is mixed with the other forms, is a usage error.
func TestGate_WouldWaitBatchUsageErrors(t *testing.T) {
	cfg, _ := gateEnv(t, noAdmission)
	for _, args := range [][]string{
		{"would-wait", "--auto-batch", "", "--config", cfg},
		{"would-wait", "--auto-batch", "not json", "--config", cfg},
		{"would-wait", "--auto-batch", "[]", "--config", cfg},
		{"would-wait", "--auto-batch", `[[]]`, "--config", cfg},
		{"would-wait", "--auto-batch", `[["go"],"x"]`, "--config", cfg},
		{"would-wait", "--auto-batch", `{"a":1}`, "--config", cfg},
		{"would-wait", "--auto-batch", `[["go"]]`, "--auto", "--config", cfg, "--", "go", "test"},
		{"would-wait", "--auto-batch", `[["go"]]`, "--config", cfg, "--", "go", "test"},
	} {
		var out, errb bytes.Buffer
		assert.Equal(t, 2, gateMain(args, &out, &errb), "%v", args)
		assert.Empty(t, out.String(), "%v", args)
	}
}
