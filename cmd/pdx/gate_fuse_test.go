package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A depth that is not a count from 0 up (negative, text, overflow) blows the fuse instead of resetting it.
func TestGateTool_RecursionFuseDistrustsABadDepth(t *testing.T) {
	for _, v := range []string{"go=-100000", "go=abc", "go=99999999999999999999999", "go=", "go=abc,go=0", "go=8,go=0", "go=0,go=-1"} {
		_, cfg, _, got := toolEnv(t, "go", quietGate)
		t.Setenv("PDX_GATE_TOOL_DEPTH", v)
		var out, errb bytes.Buffer
		assert.Equal(t, 127, gateMain([]string{"--config", cfg, "--auto", "--tool", "go", "--", "version"}, &out, &errb), v)
		assert.Contains(t, errb.String(), "shim recursion", v)
		assert.Empty(t, got.path, v)
	}
}
