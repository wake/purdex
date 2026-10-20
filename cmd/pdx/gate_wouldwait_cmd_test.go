package main

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGate_WouldWaitAnswersNo(t *testing.T) {
	cfg, _ := gateEnv(t, noAdmission)
	var out, errb bytes.Buffer
	assert.Equal(t, 0, gateMain([]string{"would-wait", "--config", cfg}, &out, &errb), errb.String())
	assert.Equal(t, "no\n", out.String())

	out.Reset()
	assert.Equal(t, 0, gateMain([]string{"would-wait", "--json", "--class", "light", "--config", cfg}, &out, &errb))
	var w map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &w))
	assert.Equal(t, false, w["would_wait"])
	assert.Equal(t, "light", w["class"])

	assert.Equal(t, false, w["would_wait"], "and a held heavy slot changes nothing: see gate_direct_test.go")
}

func TestGate_WouldWaitUsage(t *testing.T) {
	cfg, _ := gateEnv(t, noAdmission)
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
