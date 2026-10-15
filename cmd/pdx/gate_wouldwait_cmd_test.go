package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGate_WouldWaitAnswersNoWhenFreeAndYesWithAReasonWhenTaken(t *testing.T) {
	cfg, _ := gateEnv(t, "")
	var out, errb bytes.Buffer
	assert.Equal(t, 0, gateMain([]string{"would-wait", "--config", cfg}, &out, &errb), errb.String())
	assert.Equal(t, "no\n", out.String())

	out.Reset()
	assert.Equal(t, 0, gateMain([]string{"would-wait", "--json", "--class", "light", "--config", cfg}, &out, &errb))
	var w map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &w))
	assert.Equal(t, false, w["would_wait"])
	assert.Equal(t, "light", w["class"])

	// a held heavy slot: run a command that holds it while we ask
	gofile := filepath.Join(t.TempDir(), "go")
	release := func() { _ = os.WriteFile(gofile, nil, 0o644) }
	defer release()
	hold := make(chan int, 1)
	var hout, herr bytes.Buffer
	go func() {
		hold <- gateMain([]string{"--config", cfg, "--class", "heavy", "--fg", "--", "sh", "-c", "while [ ! -e " + gofile + " ]; do sleep 0.02; done"}, &hout, &herr)
	}()
	eventuallyOutput(t, func() bool {
		out.Reset()
		gateMain([]string{"would-wait", "--config", cfg}, &out, &errb)
		return bytes.HasPrefix(out.Bytes(), []byte("yes (slots): waiting for a heavy slot"))
	})
	release()
	assert.Equal(t, 0, <-hold)
}

func TestGate_WouldWaitUsage(t *testing.T) {
	cfg, _ := gateEnv(t, "")
	var out, errb bytes.Buffer
	assert.Equal(t, 2, gateMain([]string{"would-wait", "--class", "none", "--config", cfg}, &out, &errb))
	assert.Equal(t, 2, gateMain([]string{"would-wait", "extra", "--config", cfg}, &out, &errb))
}

func eventuallyOutput(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("the condition never held")
}
