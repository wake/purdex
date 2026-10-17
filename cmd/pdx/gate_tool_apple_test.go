package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// xcodebuild and swift go through the gate (#3111): a build or a test run takes a heavy slot (the real binary runs under it), a call that only
// prints is exec'd at once and leaves no gate state. Mutation gate: the classifier forgets xcodebuild / swift → the first two go red.
func TestGateTool_XcodebuildTestAndSwiftBuildRunUnderASlot(t *testing.T) {
	for _, c := range []struct {
		tool string
		args []string
	}{
		{"xcodebuild", []string{"test", "-scheme", "App"}},
		{"xcodebuild", []string{"-scheme", "App"}},
		{"swift", []string{"build", "-c", "release"}},
		{"swift", []string{"test"}},
	} {
		_, cfg, gateDir, got := toolEnv(t, c.tool, quietGate)
		var out, errb bytes.Buffer
		argv := append([]string{"--config", cfg, "--auto", "--tool", c.tool, "--"}, c.args...)
		assert.Equal(t, 7, gateMain(argv, &out, &errb), c.tool+" "+c.args[0]+": "+errb.String())
		assert.Empty(t, got.path, c.tool+": not exec'd past the gate")
		_, err := os.Stat(filepath.Join(filepath.Dir(cfg), "ran-"+c.tool))
		assert.NoError(t, err, "the real "+c.tool+" ran")
		_, err = os.Stat(gateDir)
		assert.NoError(t, err, c.tool+": a slot was taken")
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
