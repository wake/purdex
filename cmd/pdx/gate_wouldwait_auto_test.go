package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
	cfg, _ := gateEnv(t, "")
	assert.Equal(t, "none", wouldWaitAuto(t, cfg, "ls", "-la")["class"])
	assert.Equal(t, "heavy", wouldWaitAuto(t, cfg, "go", "test", "./...")["class"])
	assert.Equal(t, "heavy", wouldWaitAuto(t, cfg, "./node_modules/.bin/tsc", "-b")["class"], "argv[0] may be a path")
	assert.Equal(t, false, wouldWaitAuto(t, cfg, "go", "test", "./...")["would_wait"])

	// a held heavy slot: only the commands of that class would wait
	gofile := filepath.Join(t.TempDir(), "go")
	release := func() { _ = os.WriteFile(gofile, nil, 0o644) }
	defer release()
	hold := make(chan int, 1)
	var hout, herr bytes.Buffer
	go func() {
		hold <- gateMain([]string{"--config", cfg, "--class", "heavy", "--fg", "--", "sh", "-c", "while [ ! -e " + gofile + " ]; do sleep 0.02; done"}, &hout, &herr)
	}()
	eventuallyOutput(t, func() bool { return wouldWaitAuto(t, cfg, "go", "test", "./...")["would_wait"] == true })
	assert.Equal(t, false, wouldWaitAuto(t, cfg, "ls")["would_wait"], "a command the gate does not hold never waits")
	release()
	assert.Equal(t, 0, <-hold)
}

func TestGate_WouldWaitAutoNeedsACommand(t *testing.T) {
	cfg, _ := gateEnv(t, "")
	var out, errb bytes.Buffer
	assert.Equal(t, 2, gateMain([]string{"would-wait", "--auto", "--config", cfg}, &out, &errb))
	assert.Equal(t, 2, gateMain([]string{"would-wait", "--config", cfg, "--", "go", "test"}, &out, &errb))
}
