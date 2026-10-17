package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wake/purdex/internal/gate"
)

// `--` ends the flags: what follows it is the setting, even if it looks like a flag (review of #2944).
func TestGate_DoubleDashEndsTheFlags(t *testing.T) {
	cfg, _ := gateEnv(t, "")
	before, _ := os.ReadFile(cfg)
	var out, errb bytes.Buffer
	// "--config other.toml" is part of the (bad) value here, not an option: refused, the file is as it was
	assert.Equal(t, 1, gateMain([]string{"set", "--config", cfg, "--", "priority.mode=nice", "--config", "other.toml"}, &out, &errb))
	assert.Contains(t, errb.String(), "not a TOML value")
	after, _ := os.ReadFile(cfg)
	assert.Equal(t, string(before), string(after))
	_, err := os.Stat("other.toml")
	assert.True(t, os.IsNotExist(err), "nothing was written to the file the value named")

	errb.Reset()
	assert.Equal(t, 0, gateMain([]string{"set", "--config", cfg, "--", "priority.mode=none"}, &out, &errb), errb.String())
}

// --expect: a raise and a lower each succeed with the right old value and print the value read back; a stale one is exit 3 and the file
// is untouched.
func TestGateSet_ExpectIsACompareAndSwapAndPrintsTheReadBack(t *testing.T) {
	cfg, _ := gateEnv(t, "")
	var out, errb bytes.Buffer
	cur := gate.Defaults().Values["slots.heavy"]
	assert.Equal(t, 0, gateMain([]string{"set", "--config", cfg, "--expect", cur, "slots.heavy=3"}, &out, &errb), errb.String())
	assert.Contains(t, out.String(), "讀回：slots.heavy = 3")
	out.Reset()
	assert.Equal(t, 0, gateMain([]string{"set", "--config", cfg, "--expect", "3", "slots.heavy=1"}, &out, &errb), errb.String())
	assert.Contains(t, out.String(), "讀回：slots.heavy = 1")

	before, _ := os.ReadFile(cfg)
	errb.Reset()
	assert.Equal(t, 3, gateMain([]string{"set", "--config", cfg, "--expect", "3", "slots.heavy=9"}, &out, &errb))
	assert.Contains(t, errb.String(), "read it again")
	after, _ := os.ReadFile(cfg)
	assert.Equal(t, string(before), string(after))
	assert.Equal(t, 0, gateMain([]string{"set", "--config", cfg, "slots.heavy=2"}, &out, &errb), "no --expect: as before")
}
