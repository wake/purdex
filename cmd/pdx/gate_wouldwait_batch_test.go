package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	cfg, _ := gateEnv(t, "")
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

	gofile := filepath.Join(t.TempDir(), "go")
	release := func() { _ = os.WriteFile(gofile, nil, 0o644) }
	defer release()
	hold := make(chan int, 1)
	var hout, herr bytes.Buffer
	go func() {
		hold <- gateMain([]string{"--config", cfg, "--class", "heavy", "--fg", "--", "sh", "-c", "while [ ! -e " + gofile + " ]; do sleep 0.02; done"}, &hout, &herr)
	}()
	// The lock is taken before the holder record is written: until the note names the holder's command ("another command" is what
	// it says for a record not yet written), two looks can differ in who holds the slot (#3037).
	eventuallyOutput(t, func() bool {
		w := wouldWaitAuto(t, cfg, "go", "test", "./...")
		note, _ := w["note"].(string)
		return w["would_wait"] == true && strings.Contains(note, "sh -c")
	})
	same("a heavy slot held")
	got := wouldWaitBatchOf(t, cfg, argvs)
	assert.Equal(t, true, got[0]["would_wait"])
	assert.Equal(t, false, got[1]["would_wait"], "ls is not a command the gate holds")
	assert.Equal(t, "none", got[1]["class"])
	release()
	assert.Equal(t, 0, <-hold)
}

// Text output: one line per command, in order.
func TestGate_WouldWaitBatchTextHasALinePerCommand(t *testing.T) {
	cfg, _ := gateEnv(t, "")
	var out, errb bytes.Buffer
	require.Equal(t, 0, gateMain([]string{"would-wait", "--auto-batch", `[["ls"],["go","test"]]`, "--config", cfg}, &out, &errb), errb.String())
	assert.Equal(t, "no\nno\n", out.String())
}

// Anything that is not a non-empty list of non-empty argv lists, or that is mixed with the other forms, is a usage error.
func TestGate_WouldWaitBatchUsageErrors(t *testing.T) {
	cfg, _ := gateEnv(t, "")
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
