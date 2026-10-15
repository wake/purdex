package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
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
