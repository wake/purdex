package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// xcodebuild and swift are exec'd at once, a build or a test run too (S0-a): they went through a heavy slot before (#3111). What holds them now is
// the wwd's entry and queue.
func TestGateTool_XcodebuildAndSwiftBuildsAreExecedAtOnce(t *testing.T) {
	for _, c := range []struct {
		tool string
		args []string
	}{
		{"xcodebuild", []string{"test", "-scheme", "App"}},
		{"xcodebuild", []string{"-scheme", "App"}},
		{"swift", []string{"build", "-c", "release"}},
		{"swift", []string{"test"}},
	} {
		real, cfg, gateDir, got := toolEnv(t, c.tool, quietGate)
		var out, errb bytes.Buffer
		argv := append([]string{"--config", cfg, "--auto", "--tool", c.tool, "--"}, c.args...)
		assert.Equal(t, 0, gateMain(argv, &out, &errb), c.tool+" "+c.args[0]+": "+errb.String())
		assert.Equal(t, real, got.path, c.tool+": exec'd")
		_, err := os.Stat(gateDir)
		assert.True(t, os.IsNotExist(err), c.tool+": no slot")
	}
}

func TestGateTool_XcodebuildListAndSwiftVersionPassAtOnce(t *testing.T) {
	for _, c := range []struct {
		tool string
		args []string
	}{
		{"xcodebuild", []string{"-list"}},
		{"xcodebuild", []string{"-version"}},
		{"swift", []string{"--version"}},
		{"swift", []string{"package", "resolve"}},
	} {
		real, cfg, gateDir, got := toolEnv(t, c.tool, quietGate)
		var out, errb bytes.Buffer
		argv := append([]string{"--config", cfg, "--auto", "--tool", c.tool, "--"}, c.args...)
		assert.Equal(t, 0, gateMain(argv, &out, &errb), errb.String())
		assert.Equal(t, real, got.path, c.tool+" "+c.args[0])
		_, err := os.Stat(gateDir)
		assert.True(t, os.IsNotExist(err), c.tool+": the hot path leaves no gate state")
	}
}
